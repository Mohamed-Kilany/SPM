#Requires -Version 5.1
<#
.SYNOPSIS
Restores an SPM backup bundle or a legacy PostgreSQL dump.

.DESCRIPTION
For a bundle (spm-backup-*.tar or *.tar.enc) the script verifies every payload
checksum, extracts the bundle, restores both PostgreSQL databases and, unless
-DatabaseOnly is passed, replaces the Gitea, NATS and attachments volumes.
Configuration files in the bundle are extracted for the operator but are not
applied automatically.

For a legacy .dump file the script keeps the original behaviour and restores
only the SPM database.

This is destructive. Set SPM_RESTORE_CONFIRM=YES to acknowledge replacing the
current data. Stop the API before restoring; the script does that for you.

.PARAMETER BackupPath
Bundle or legacy dump to restore.

.PARAMETER KeyFile
AES-256 key for an encrypted bundle. Defaults to SPM_BACKUP_KEY_FILE.

.PARAMETER DatabaseOnly
Restore databases only. Volume archives in the bundle are verified but not
applied.

.PARAMETER KeepExtracted
Keep the extracted bundle directory (contains configuration and secrets).

.EXAMPLE
$env:SPM_RESTORE_CONFIRM = 'YES'
$env:SPM_BACKUP_KEY_FILE = 'D:\secure\spm-backup.key'
.\scripts\restore.ps1 -BackupPath .\backups\spm-backup-20261124-120000.tar.enc
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$BackupPath,
    [string]$KeyFile,
    [switch]$DatabaseOnly,
    [switch]$KeepExtracted,
    [string]$BackupctlPath
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$resolved = (Resolve-Path -LiteralPath $BackupPath -ErrorAction Stop).Path
$timestamp = [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss')

if ($env:SPM_RESTORE_CONFIRM -ne 'YES') {
    throw 'Refusing to restore. Set SPM_RESTORE_CONFIRM=YES to replace the current data.'
}
if (-not $KeyFile) { $KeyFile = $env:SPM_BACKUP_KEY_FILE }
if ($KeyFile) {
    $KeyFile = (Resolve-Path -LiteralPath $KeyFile -ErrorAction Stop).Path
}

function Invoke-Docker {
    param([string[]]$Arguments)
    & docker @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "docker $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
}

function Invoke-Compose {
    param([string[]]$Arguments)
    & docker compose @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
}

function Resolve-Backupctl {
    param([string]$Explicit)
    if ($Explicit) {
        return (Resolve-Path -LiteralPath $Explicit -ErrorAction Stop).Path
    }
    $binaryName = if ($env:OS -eq 'Windows_NT') { 'backupctl.exe' } else { 'backupctl' }
    $tool = Join-Path (Join-Path $repoRoot 'backups/.tools') $binaryName
    if (Test-Path -LiteralPath $tool) {
        return $tool
    }
    $go = Get-Command go -ErrorAction SilentlyContinue
    if (-not $go) {
        throw "backupctl was not found. Build it with 'go build -o $tool ./cmd/backupctl' in api/ or pass -BackupctlPath."
    }
    New-Item -ItemType Directory -Path (Split-Path -Parent $tool) -Force | Out-Null
    Push-Location (Join-Path $repoRoot 'api')
    try {
        & go build -trimpath -o $tool ./cmd/backupctl
        if ($LASTEXITCODE -ne 0) { throw 'go build ./cmd/backupctl failed' }
    } finally {
        Pop-Location
    }
    return $tool
}

function Restore-Database {
    param(
        [string]$DumpPath,
        [string]$Database
    )
    $containerPath = "/tmp/spm-restore-$Database.dump"
    Write-Host "Restoring database '$Database'..." -ForegroundColor Cyan
    try {
        Invoke-Compose @('cp', $DumpPath, "postgres:$containerPath")
        Invoke-Compose @('exec', '-T', 'postgres', 'pg_restore', '-l', $containerPath) | Out-Null
        Invoke-Compose @('exec', '-T', 'postgres', 'pg_restore', '--clean', '--if-exists', '--no-owner', '--no-privileges',
            '-U', $script:postgresUser, '-d', $Database, $containerPath)
    } finally {
        try {
            Invoke-Compose @('exec', '-T', 'postgres', 'rm', '-f', $containerPath) | Out-Null
        } catch {
            Write-Warning "Could not remove $containerPath from the postgres container: $($_.Exception.Message)"
        }
    }
}

function Restore-Volume {
    param(
        [string]$ArchiveName,
        [string]$VolumeName,
        [string]$HostDirectory
    )
    Write-Host "Restoring volume $VolumeName from $ArchiveName..." -ForegroundColor Cyan
    Invoke-Docker @(
        'run', '--rm',
        '--volume', "${VolumeName}:/target",
        '--volume', "${HostDirectory}:/backup:ro",
        '--entrypoint', 'sh',
        'postgres:18-alpine',
        '-c', "rm -rf /target/* /target/.[!.]* /target/..?* 2>/dev/null; tar -C /target -xf /backup/$ArchiveName"
    )
}

$extracted = $null
$stopped = [System.Collections.Generic.List[string]]::new()
$useBundle = $resolved -match '\.tar(\.enc)?$'
try {
    Invoke-Compose @('ps', '-q', 'postgres') | Out-Null

    if ($useBundle) {
        $backupctl = Resolve-Backupctl -Explicit $BackupctlPath
        Write-Host 'Verifying the bundle...' -ForegroundColor Cyan
        $verifyArgs = @('verify', '-bundle', $resolved)
        if ($KeyFile) { $verifyArgs += @('-key-file', $KeyFile) }
        & $backupctl @verifyArgs | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Bundle verification failed; refusing to restore.' }

        $extracted = Join-Path (Join-Path $repoRoot 'backups/.restore') $timestamp
        $unpackArgs = @('unpack', '-input', $resolved, '-output', $extracted)
        if ($KeyFile) { $unpackArgs += @('-key-file', $KeyFile) }
        & $backupctl @unpackArgs | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Could not extract the bundle.' }
    }

    $postgresContainer = (Invoke-Compose @('ps', '-q', 'postgres') | Out-String).Trim()
    if (-not $postgresContainer) { throw 'The postgres service is not running.' }
    $projectOutput = & docker inspect -f '{{ index .Config.Labels "com.docker.compose.project" }}' $postgresContainer
    if ($LASTEXITCODE -ne 0) {
        throw 'Could not inspect the postgres container to determine the Compose project name.'
    }
    $project = ($projectOutput | Out-String).Trim()
    if (-not $project) { throw 'Could not determine the Compose project name.' }
    $script:postgresUser = if ($env:POSTGRES_USER) { $env:POSTGRES_USER } else { 'spm' }

    Write-Host 'Stopping writers...' -ForegroundColor Cyan
    $servicesToStop = @('api_backend')
    if ($useBundle) { $servicesToStop += @('gitea') }
    if ($useBundle -and -not $DatabaseOnly) { $servicesToStop += @('nats') }
    foreach ($service in $servicesToStop) {
        Invoke-Compose @('stop', $service) | Out-Null
        $stopped.Add($service)
    }

    if ($useBundle) {
        $dumps = @(Get-ChildItem -LiteralPath (Join-Path $extracted 'databases') -Filter '*.dump' -File)
        if ($dumps.Count -eq 0) { throw 'The bundle contains no database dumps.' }
        foreach ($dump in $dumps) {
            Restore-Database -DumpPath $dump.FullName -Database $dump.BaseName
        }

        if (-not $DatabaseOnly) {
            $volumeDirectory = Join-Path $extracted 'volumes'
            $volumeArchives = @(Get-ChildItem -LiteralPath $volumeDirectory -Filter '*.tar' -File)
            foreach ($archive in $volumeArchives) {
                $volumeName = "${project}_$($archive.BaseName)"
                & docker volume inspect $volumeName 2>$null | Out-Null
                if ($LASTEXITCODE -ne 0) {
                    throw "Volume $volumeName does not exist. Start the stack once so Compose creates it."
                }
                Restore-Volume -ArchiveName $archive.Name -VolumeName $volumeName -HostDirectory $volumeDirectory
            }
        }
    } else {
        Restore-Database -DumpPath $resolved -Database $(if ($env:POSTGRES_DB) { $env:POSTGRES_DB } else { 'spm' })
    }

    Write-Host 'Starting services...' -ForegroundColor Cyan
    foreach ($service in $stopped) {
        Invoke-Compose @('start', $service) | Out-Null
    }
    $stopped.Clear()

    $deadline = (Get-Date).AddSeconds(120)
    do {
        Start-Sleep -Seconds 2
        try {
            $health = Invoke-RestMethod -Uri 'http://localhost:8080/health/ready' -TimeoutSec 5
            if ($health.status -eq 'ready') {
                Write-Host 'Restore completed; API is ready.' -ForegroundColor Green
                if ($extracted -and $KeepExtracted) {
                    Write-Host "Extracted configuration kept at: $extracted" -ForegroundColor Yellow
                }
                return
            }
        } catch {
            # Retry until the deadline.
        }
    } while ((Get-Date) -lt $deadline)

    throw 'Restore completed but the API did not become ready within 120 seconds.'
} finally {
    foreach ($service in $stopped) {
        try {
            Invoke-Compose @('start', $service) | Out-Null
        } catch {
            Write-Warning "Could not restart $service`: $($_.Exception.Message)"
        }
    }
    if ($extracted -and -not $KeepExtracted -and (Test-Path -LiteralPath $extracted)) {
        Remove-Item -LiteralPath $extracted -Recurse -Force -ErrorAction SilentlyContinue
    }
}
