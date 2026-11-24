#Requires -Version 5.1
<#
.SYNOPSIS
Creates an encrypted, checksummed SPM backup bundle and applies retention.

.DESCRIPTION
Captures both PostgreSQL databases, the Gitea, NATS and attachments volumes,
and the deployment configuration into one bundle. The bundle contains a
manifest.json with a SHA-256 digest for every payload file, is optionally
encrypted with AES-256-GCM, is verified before it is reported as successful,
and can be copied to an off-host directory.

The job writes a JSON status file consumed by the API metrics collector and
optionally posts failures to SPM_BACKUP_ALERT_WEBHOOK.

Encryption is required unless -AllowUnencrypted is passed. The key is never
written into the bundle; set SPM_BACKUP_KEY_FILE or pass -KeyFile.

.PARAMETER OutputDirectory
Backup destination. Defaults to SPM_BACKUP_OUTPUT_DIR or <repo>/backups.

.PARAMETER KeyFile
AES-256 key. Defaults to SPM_BACKUP_KEY_FILE. Create one with:
  backupctl keygen -output C:\secure\spm-backup.key

.PARAMETER AllowUnencrypted
Permit a bundle without encryption. Intended for local development only.

.PARAMETER IncludeK3s
Also archive k3s_data. Argo CD state is declarative and rebuildable from k8s/
and the Gitea manifest repository, so this is off by default.

.PARAMETER OffHostDirectory
Copy the final bundle and its checksum to this directory (for example a
mounted NAS share or a synced backup volume). Defaults to
SPM_BACKUP_OFFHOST_DIR. SPM_BACKUP_REQUIRE_OFFHOST=true makes the job fail
when no off-host destination is configured.

.PARAMETER SkipRetention
Do not prune old bundles after a successful backup.

.EXAMPLE
$env:SPM_BACKUP_KEY_FILE = 'D:\secure\spm-backup.key'
.\scripts\backup.ps1
#>
[CmdletBinding()]
param(
    [string]$OutputDirectory,
    [string]$KeyFile,
    [switch]$AllowUnencrypted,
    [switch]$IncludeK3s,
    [string]$OffHostDirectory,
    [int]$KeepLast = 0,
    [int]$KeepDays = 0,
    [switch]$SkipRetention,
    [string]$BackupctlPath
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$timestamp = [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss')
$hostName = if ($env:COMPUTERNAME) { $env:COMPUTERNAME } else { [System.Net.Dns]::GetHostName() }
$scope = [System.Collections.Generic.List[string]]::new()
$stopwatch = [System.Diagnostics.Stopwatch]::StartNew()

if (-not $OutputDirectory) {
    $OutputDirectory = if ($env:SPM_BACKUP_OUTPUT_DIR) { $env:SPM_BACKUP_OUTPUT_DIR } else { Join-Path $repoRoot 'backups' }
}
if (-not $KeyFile) { $KeyFile = $env:SPM_BACKUP_KEY_FILE }
if (-not $OffHostDirectory) { $OffHostDirectory = $env:SPM_BACKUP_OFFHOST_DIR }
if ($KeepLast -le 0) {
    $KeepLast = if ($env:SPM_BACKUP_KEEP_LAST) { [int]$env:SPM_BACKUP_KEEP_LAST } else { 7 }
}
if ($KeepDays -le 0) {
    $KeepDays = if ($env:SPM_BACKUP_KEEP_DAYS) { [int]$env:SPM_BACKUP_KEEP_DAYS } else { 30 }
}

$statusFile = if ($env:SPM_BACKUP_STATUS_FILE) { $env:SPM_BACKUP_STATUS_FILE } else { Join-Path $OutputDirectory 'backup-status.json' }
$stagingRoot = Join-Path $OutputDirectory '.staging'
$staging = Join-Path $stagingRoot $timestamp

function Write-FailureStatus {
    param([string]$Message)
    if ($script:backupctl -and (Test-Path -LiteralPath $script:backupctl)) {
        try {
            & $script:backupctl status -file $statusFile -set failure -error $Message -duration "$([int]$stopwatch.Elapsed.TotalSeconds)s" | Out-Null
        } catch {
            Write-Warning "Could not record failure status: $($_.Exception.Message)"
        }
    }
    if ($env:SPM_BACKUP_ALERT_WEBHOOK) {
        try {
            $body = @{
                text = "SPM backup failed on $hostName`: $Message"
                host = $hostName
                error = $Message
            } | ConvertTo-Json -Compress
            Invoke-RestMethod -Method Post -Uri $env:SPM_BACKUP_ALERT_WEBHOOK -ContentType 'application/json' -Body $body -TimeoutSec 15 | Out-Null
        } catch {
            Write-Warning "Could not post the backup alert: $($_.Exception.Message)"
        }
    }
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
        $resolved = Resolve-Path -LiteralPath $Explicit -ErrorAction Stop
        return $resolved.Path
    }
    $binaryName = if ($env:OS -eq 'Windows_NT') { 'backupctl.exe' } else { 'backupctl' }
    $toolDirectory = Join-Path $OutputDirectory '.tools'
    $tool = Join-Path $toolDirectory $binaryName
    if (Test-Path -LiteralPath $tool) {
        return $tool
    }
    $go = Get-Command go -ErrorAction SilentlyContinue
    if (-not $go) {
        throw "backupctl was not found. Install Go, pass -BackupctlPath, or copy the backupctl binary to $tool."
    }
    New-Item -ItemType Directory -Path $toolDirectory -Force | Out-Null
    Push-Location (Join-Path $repoRoot 'api')
    try {
        & go build -trimpath -o $tool ./cmd/backupctl
        if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $tool)) {
            throw 'go build ./cmd/backupctl failed'
        }
    } finally {
        Pop-Location
    }
    return $tool
}

function Test-RequiredCommand {
    param([string]$Name)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required command '$Name' was not found on PATH."
    }
}

function Save-Database {
    param(
        [string]$Database,
        [string]$Destination
    )
    $containerPath = "/tmp/spm-backup-$Database-$timestamp.dump"
    Write-Host "  $Database" -ForegroundColor Cyan
    try {
        Invoke-Compose @('exec', '-T', 'postgres', 'pg_dump', '-U', $script:postgresUser, '-d', $Database, '-Fc', "--file=$containerPath")
        Invoke-Compose @('cp', "postgres:$containerPath", $Destination)
        Invoke-Compose @('exec', '-T', 'postgres', 'pg_restore', '-l', $containerPath) | Out-Null
    } finally {
        try {
            Invoke-Compose @('exec', '-T', 'postgres', 'rm', '-f', $containerPath) | Out-Null
        } catch {
            Write-Warning "Could not remove $containerPath from the postgres container: $($_.Exception.Message)"
        }
    }
}

function Save-Volume {
    param(
        [string]$LogicalName,
        [string]$VolumeName,
        [string]$HostDirectory
    )
    Write-Host "  $LogicalName ($VolumeName)" -ForegroundColor Cyan
    Invoke-Docker @(
        'run', '--rm',
        '--volume', "${VolumeName}:/source:ro",
        '--volume', "${HostDirectory}:/backup",
        '--entrypoint', 'tar',
        'postgres:18-alpine',
        '-C', '/source', '-cf', "/backup/$LogicalName.tar", '.'
    )
    Invoke-Docker @(
        'run', '--rm',
        '--volume', "${HostDirectory}:/backup:ro",
        '--entrypoint', 'tar',
        'postgres:18-alpine',
        '-tf', "/backup/$LogicalName.tar"
    ) | Out-Null
    $script:scope.Add("volume:$LogicalName")
}

$backupctl = $null
$plainBundle = $null
try {
    Test-RequiredCommand 'docker'
    Invoke-Compose @('version') | Out-Null

    if (-not (Test-Path -LiteralPath $OutputDirectory)) {
        New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
    }
    if (-not $KeyFile) {
        if (-not $AllowUnencrypted) {
            throw 'Refusing to create an unencrypted backup. Set SPM_BACKUP_KEY_FILE, pass -KeyFile, or use -AllowUnencrypted for a local-only run.'
        }
        Write-Warning 'Creating an UNENCRYPTED bundle because -AllowUnencrypted was passed.'
    } else {
        $KeyFile = (Resolve-Path -LiteralPath $KeyFile -ErrorAction Stop).Path
    }
    if ($env:SPM_BACKUP_REQUIRE_OFFHOST -eq 'true' -and -not $OffHostDirectory) {
        throw 'SPM_BACKUP_REQUIRE_OFFHOST=true but SPM_BACKUP_OFFHOST_DIR is not set.'
    }

    $backupctl = Resolve-Backupctl -Explicit $BackupctlPath
    $script:postgresUser = if ($env:POSTGRES_USER) { $env:POSTGRES_USER } else { 'spm' }
    $databases = @(
        $(if ($env:POSTGRES_DB) { $env:POSTGRES_DB } else { 'spm' }),
        $(if ($env:GITEA_DB) { $env:GITEA_DB } else { 'git' })
    )

    $postgresContainer = (Invoke-Compose @('ps', '-q', 'postgres') | Out-String).Trim()
    if (-not $postgresContainer) {
        throw 'The postgres service is not running. Start the stack before taking a backup.'
    }
    $projectOutput = & docker inspect -f '{{ index .Config.Labels "com.docker.compose.project" }}' $postgresContainer
    if ($LASTEXITCODE -ne 0) {
        throw 'Could not inspect the postgres container to determine the Compose project name.'
    }
    $project = ($projectOutput | Out-String).Trim()
    if (-not $project) {
        throw 'Could not determine the Compose project name from the postgres container.'
    }

    New-Item -ItemType Directory -Path (Join-Path $staging 'databases') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $staging 'volumes') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $staging 'config') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $staging 'metadata') -Force | Out-Null
    $DestinationVolumeDirectory = Join-Path $staging 'volumes'

    Write-Host 'Dumping PostgreSQL databases...' -ForegroundColor Cyan
    foreach ($database in $databases) {
        Save-Database -Database $database -Destination (Join-Path $staging "databases/$database.dump")
        $scope.Add("database:$database")
    }

    Write-Host 'Archiving volumes...' -ForegroundColor Cyan
    $volumes = [ordered]@{
        gitea_data       = 'gitea_data'
        nats_data        = 'nats_data'
        attachments_data = 'attachments_data'
    }
    if ($IncludeK3s) {
        $volumes['k3s_data'] = 'k3s_data'
    }
    foreach ($entry in $volumes.GetEnumerator()) {
        $volumeName = "${project}_$($entry.Value)"
        & docker volume inspect $volumeName 2>$null | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw "Volume $volumeName does not exist; refusing to create an incomplete backup."
        }
        Save-Volume -LogicalName $entry.Key -VolumeName $volumeName -HostDirectory $DestinationVolumeDirectory
    }

    Write-Host 'Capturing configuration...' -ForegroundColor Cyan
    $config = Join-Path $staging 'config'
    $configFiles = @('docker-compose.yaml', 'docker-compose.test.yml', '.env.example', 'start-stack.ps1')
    foreach ($file in $configFiles) {
        $source = Join-Path $repoRoot $file
        if (Test-Path -LiteralPath $source) {
            Copy-Item -LiteralPath $source -Destination $config
        }
    }
    foreach ($directory in @('nats', 'envoy', 'k8s', 'scripts')) {
        $source = Join-Path $repoRoot $directory
        if (Test-Path -LiteralPath $source) {
            Copy-Item -LiteralPath $source -Destination $config -Recurse
        }
    }
    if ($KeyFile) {
        $environmentFile = Join-Path $repoRoot '.env'
        if (Test-Path -LiteralPath $environmentFile) {
            Copy-Item -LiteralPath $environmentFile -Destination (Join-Path $config '.env')
        }
    } else {
        Write-Warning 'Skipping .env because the bundle is not encrypted.'
    }

    $commit = (& git -C $repoRoot rev-parse HEAD 2>$null | Out-String).Trim()
    $describe = (& git -C $repoRoot describe --tags --always --dirty 2>$null | Out-String).Trim()
    @(
        "commit: $commit",
        "describe: $describe",
        "created_utc: $([DateTime]::UtcNow.ToString('o'))",
        "host: $hostName"
    ) | Set-Content -LiteralPath (Join-Path $staging 'metadata/git.txt') -Encoding ASCII
    & docker compose images | Set-Content -LiteralPath (Join-Path $staging 'metadata/images.txt') -Encoding ASCII
    & docker version | Set-Content -LiteralPath (Join-Path $staging 'metadata/docker.txt') -Encoding ASCII

    Write-Host 'Packing and verifying the bundle...' -ForegroundColor Cyan
    $plainBundle = Join-Path $OutputDirectory ".spm-$timestamp.tar"
    & $backupctl pack -root $staging -output $plainBundle -host $hostName -commit $describe | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'backupctl pack failed' }

    if ($KeyFile) {
        $finalBundle = Join-Path $OutputDirectory "spm-backup-$timestamp.tar.enc"
        & $backupctl seal -input $plainBundle -output $finalBundle -key-file $KeyFile | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'backupctl seal failed' }
        Remove-Item -LiteralPath $plainBundle -Force
        $plainBundle = $null
        & $backupctl verify -bundle $finalBundle -key-file $KeyFile | Out-Null
    } else {
        $finalBundle = Join-Path $OutputDirectory "spm-backup-$timestamp.tar"
        Move-Item -LiteralPath $plainBundle -Destination $finalBundle
        $plainBundle = $null
        & $backupctl verify -bundle $finalBundle | Out-Null
    }
    if ($LASTEXITCODE -ne 0) { throw 'backupctl verify rejected the bundle' }

    $hash = (Get-FileHash -LiteralPath $finalBundle -Algorithm SHA256).Hash.ToLowerInvariant()
    $size = (Get-Item -LiteralPath $finalBundle).Length
    Set-Content -LiteralPath "$finalBundle.sha256" -Value "$hash  $(Split-Path -Leaf $finalBundle)" -Encoding ASCII

    if ($OffHostDirectory) {
        Write-Host "Copying the bundle to $OffHostDirectory..." -ForegroundColor Cyan
        if (-not (Test-Path -LiteralPath $OffHostDirectory)) {
            New-Item -ItemType Directory -Path $OffHostDirectory -Force | Out-Null
        }
        Copy-Item -LiteralPath $finalBundle -Destination $OffHostDirectory -Force
        Copy-Item -LiteralPath "$finalBundle.sha256" -Destination $OffHostDirectory -Force
        $remote = Join-Path $OffHostDirectory (Split-Path -Leaf $finalBundle)
        $remoteHash = (Get-FileHash -LiteralPath $remote -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($remoteHash -ne $hash) {
            throw "Off-host copy checksum mismatch: $remoteHash != $hash"
        }
    } elseif ($env:SPM_BACKUP_REQUIRE_OFFHOST -eq 'true') {
        throw 'Off-host copy is required but no destination was configured.'
    } else {
        Write-Warning 'No off-host destination configured. Copy the bundle off the host before the next disaster.'
    }

    $elapsed = [int]$stopwatch.Elapsed.TotalSeconds
    $statusArgs = @('status', '-file', $statusFile, '-set', 'success', '-bundle', $finalBundle, '-sha256', $hash, '-size', "$size", '-duration', "${elapsed}s")
    if ($KeyFile) { $statusArgs += '-encrypted' }
    & $backupctl @statusArgs | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not record the backup status' }
    & $backupctl status -file $statusFile -set verified | Out-Null

    if (-not $SkipRetention) {
        Write-Host 'Applying retention...' -ForegroundColor Cyan
        try {
            & $backupctl retention -directory $OutputDirectory -keep-last $KeepLast -keep-days $KeepDays -apply
            if ($LASTEXITCODE -ne 0) { throw 'backupctl retention failed' }
        } catch {
            # A retention problem does not make the verified bundle unusable.
            Write-Warning "Retention failed: $($_.Exception.Message)"
        }
    }

    Remove-Item -LiteralPath $staging -Recurse -Force -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $stagingRoot) {
        $leftovers = @(Get-ChildItem -LiteralPath $stagingRoot -Directory)
        if ($leftovers.Count -eq 0) {
            Remove-Item -LiteralPath $stagingRoot -Force -ErrorAction SilentlyContinue
        }
    }

    Write-Host ''
    Write-Host "Backup verified: $finalBundle" -ForegroundColor Green
    Write-Host "  size:   $size bytes"
    Write-Host "  sha256: $hash"
    Write-Host "  scope:  $($scope -join ', ')"
    Write-Host "  status: $statusFile"
    Write-Output $finalBundle
} catch {
    Write-FailureStatus -Message $_.Exception.Message
    if ($plainBundle -and (Test-Path -LiteralPath $plainBundle)) {
        Remove-Item -LiteralPath $plainBundle -Force -ErrorAction SilentlyContinue
    }
    if (Test-Path -LiteralPath $staging) {
        Remove-Item -LiteralPath $staging -Recurse -Force -ErrorAction SilentlyContinue
    }
    throw
} finally {
    $stopwatch.Stop()
}
