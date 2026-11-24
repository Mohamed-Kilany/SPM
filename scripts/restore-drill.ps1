#Requires -Version 5.1
<#
.SYNOPSIS
Runs a non-destructive restore drill against an SPM backup bundle.

.DESCRIPTION
Verifies every bundle checksum, restores each database dump into a throwaway
database on the running PostgreSQL instance, compares schema fingerprints with
the live databases, checks that every foreign key is validated, and lists every
volume archive. Live databases, volumes and services are never modified.

The drill writes a JSON and Markdown report and updates the backup status file
so the API can alert on stale or failed drills. It does not measure full-stack
RTO; that requires restoring into an isolated Compose project and is documented
in docs/BACKUP-RESTORE.md.

.PARAMETER BundlePath
Bundle to drill.

.PARAMETER KeyFile
AES-256 key for an encrypted bundle. Defaults to SPM_BACKUP_KEY_FILE.

.PARAMETER ReportDirectory
Report destination. Defaults to <repo>/backups/drills.

.EXAMPLE
$env:SPM_BACKUP_KEY_FILE = 'D:\secure\spm-backup.key'
.\scripts\restore-drill.ps1 -BundlePath .\backups\spm-backup-20261124-120000.tar.enc
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$BundlePath,
    [string]$KeyFile,
    [string]$BackupctlPath,
    [string]$ReportDirectory,
    [switch]$KeepExtracted
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$resolved = (Resolve-Path -LiteralPath $BundlePath -ErrorAction Stop).Path
if (-not $ReportDirectory) { $ReportDirectory = Join-Path $repoRoot 'backups/drills' }
$timestamp = [DateTime]::UtcNow.ToString('yyyyMMddHHmmss')
$started = [DateTime]::UtcNow
$total = [System.Diagnostics.Stopwatch]::StartNew()
$extracted = Join-Path (Join-Path $repoRoot 'backups/.drills') $timestamp
$scratchDatabases = [System.Collections.Generic.List[string]]::new()

if (-not $KeyFile) { $KeyFile = $env:SPM_BACKUP_KEY_FILE }
if ($KeyFile) {
    $KeyFile = (Resolve-Path -LiteralPath $KeyFile -ErrorAction Stop).Path
}
$statusFile = if ($env:SPM_BACKUP_STATUS_FILE) { $env:SPM_BACKUP_STATUS_FILE } else { Join-Path $repoRoot 'backups/backup-status.json' }

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

function Invoke-ComposeOutput {
    param([string[]]$Arguments)
    $output = & docker compose @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
    return ($output | Out-String).Trim()
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

function Invoke-Psql {
    param(
        [string]$Database,
        [string]$Query
    )
    return Invoke-ComposeOutput @('exec', '-T', 'postgres', 'psql', '-U', $script:postgresUser, '-d', $Database, '-At', '-c', $Query)
}

function Get-SchemaFingerprint {
    param([string]$Database)
    $tables = [int](Invoke-Psql -Database $Database -Query "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE';")
    $invalidForeignKeys = [int](Invoke-Psql -Database $Database -Query "SELECT count(*) FROM pg_constraint WHERE contype = 'f' AND NOT convalidated;")
    $hasMigrations = (Invoke-Psql -Database $Database -Query "SELECT to_regclass('public.schema_migrations') IS NOT NULL;") -eq 't'
    $migrations = 0
    if ($hasMigrations) {
        $migrations = [int](Invoke-Psql -Database $Database -Query 'SELECT count(*) FROM schema_migrations;')
    }
    $hasGiteaVersion = (Invoke-Psql -Database $Database -Query "SELECT to_regclass('public.version') IS NOT NULL;") -eq 't'
    return [ordered]@{
        tables = $tables
        invalid_foreign_keys = $invalidForeignKeys
        has_schema_migrations = $hasMigrations
        schema_migrations = $migrations
        has_gitea_version = $hasGiteaVersion
    }
}

function Get-VolumeArchiveInfo {
    param(
        [string]$ArchivePath,
        [string]$HostDirectory
    )
    $name = Split-Path -Leaf $ArchivePath
    $listing = & docker run --rm --volume "${HostDirectory}:/backup:ro" --entrypoint tar postgres:18-alpine -tf "/backup/$name"
    if ($LASTEXITCODE -ne 0) {
        throw "Volume archive $name is not readable"
    }
    $entries = @($listing | Where-Object { $_ -and $_ -ne './' })
    return [ordered]@{
        archive = $name
        entries = $entries.Count
        bytes = (Get-Item -LiteralPath $ArchivePath).Length
    }
}

$report = [ordered]@{
    schema_version = 1
    drill_id = $timestamp
    started_at = $started.ToString('o')
    host = if ($env:COMPUTERNAME) { $env:COMPUTERNAME } else { [System.Net.Dns]::GetHostName() }
    bundle = $resolved
    bundle_sha256 = ''
    encrypted = [bool]$KeyFile
    verified = $false
    database_restore_seconds = 0
    total_seconds = 0
    databases = @()
    volumes = @()
    result = 'fail'
    errors = @()
    notes = 'Database restore and schema checks only. Full isolated-stack RTO is a manual quarterly drill.'
}

$errors = [System.Collections.Generic.List[string]]::new()
$databaseResults = [System.Collections.Generic.List[object]]::new()
$volumeResults = [System.Collections.Generic.List[object]]::new()
$backupctl = $null
try {
    $backupctl = Resolve-Backupctl -Explicit $BackupctlPath
    $postgresContainer = (Invoke-ComposeOutput @('ps', '-q', 'postgres'))
    if (-not $postgresContainer) { throw 'The postgres service is not running.' }
    $script:postgresUser = if ($env:POSTGRES_USER) { $env:POSTGRES_USER } else { 'spm' }

    Write-Host 'Verifying the bundle...' -ForegroundColor Cyan
    $verifyArgs = @('verify', '-bundle', $resolved)
    if ($KeyFile) { $verifyArgs += @('-key-file', $KeyFile) }
    & $backupctl @verifyArgs | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Bundle verification failed.' }
    $report.verified = $true
    $report.bundle_sha256 = (Get-FileHash -LiteralPath $resolved -Algorithm SHA256).Hash.ToLowerInvariant()

    Write-Host 'Extracting the bundle...' -ForegroundColor Cyan
    $unpackArgs = @('unpack', '-input', $resolved, '-output', $extracted)
    if ($KeyFile) { $unpackArgs += @('-key-file', $KeyFile) }
    & $backupctl @unpackArgs | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not extract the bundle.' }

    $databaseStopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    $dumps = @(Get-ChildItem -LiteralPath (Join-Path $extracted 'databases') -Filter '*.dump' -File)
    if ($dumps.Count -eq 0) { throw 'The bundle contains no database dumps.' }
    foreach ($dump in $dumps) {
        $safeBase = $dump.BaseName -replace '[^a-zA-Z0-9_]', '_'
        $scratch = "spm_drill_${timestamp}_$safeBase"
        if ($scratch.Length -gt 63) { $scratch = $scratch.Substring(0, 63) }
        $containerPath = "/tmp/$scratch.dump"
        $databaseResult = [ordered]@{
            source_database = $dump.BaseName
            scratch_database = $scratch
            restore_seconds = 0
            source = $null
            restored = $null
            ok = $false
            error = ''
        }
        try {
            Write-Host "Restoring $($dump.BaseName) into $scratch..." -ForegroundColor Cyan
            Invoke-Compose @('exec', '-T', 'postgres', 'dropdb', '-U', $script:postgresUser, '--if-exists', $scratch) | Out-Null
            Invoke-Compose @('exec', '-T', 'postgres', 'createdb', '-U', $script:postgresUser, $scratch) | Out-Null
            $scratchDatabases.Add($scratch)
            $restoreWatch = [System.Diagnostics.Stopwatch]::StartNew()
            try {
                Invoke-Compose @('cp', $dump.FullName, "postgres:$containerPath") | Out-Null
                Invoke-Compose @('exec', '-T', 'postgres', 'pg_restore', '--no-owner', '--no-privileges', '-U', $script:postgresUser, '-d', $scratch, $containerPath) | Out-Null
            } finally {
                $restoreWatch.Stop()
                try {
                    Invoke-Compose @('exec', '-T', 'postgres', 'rm', '-f', $containerPath) | Out-Null
                } catch {
                    Write-Warning "Could not remove $containerPath from the postgres container: $($_.Exception.Message)"
                }
            }

            $source = Get-SchemaFingerprint -Database $dump.BaseName
            $restored = Get-SchemaFingerprint -Database $scratch
            $databaseResult.source = $source
            $databaseResult.restored = $restored
            $databaseResult.restore_seconds = [math]::Round($restoreWatch.Elapsed.TotalSeconds, 2)

            if ($restored.tables -ne $source.tables) {
                throw "table count differs: restored $($restored.tables), source $($source.tables)"
            }
            if ($restored.tables -le 0) {
                throw 'restored database has no public tables'
            }
            if ($restored.invalid_foreign_keys -ne 0) {
                throw "restored database has $($restored.invalid_foreign_keys) unvalidated foreign keys"
            }
            if ($source.has_schema_migrations -ne $restored.has_schema_migrations) {
                throw 'schema_migrations presence differs from the source database'
            }
            if ($source.has_schema_migrations -and $source.schema_migrations -ne $restored.schema_migrations) {
                throw "schema_migrations count differs: restored $($restored.schema_migrations), source $($source.schema_migrations)"
            }
            if ($source.has_gitea_version -ne $restored.has_gitea_version) {
                throw 'Gitea version table presence differs from the source database'
            }
            $databaseResult.ok = $true
        } catch {
            $databaseResult.error = $_.Exception.Message
            $errors.Add("database $($dump.BaseName): $($_.Exception.Message)")
        } finally {
            $databaseResults.Add($databaseResult)
        }
    }
    $databaseStopwatch.Stop()
    $report.database_restore_seconds = [math]::Round($databaseStopwatch.Elapsed.TotalSeconds, 2)

    $volumeDirectory = Join-Path $extracted 'volumes'
    $archives = @(Get-ChildItem -LiteralPath $volumeDirectory -Filter '*.tar' -File)
    foreach ($archive in $archives) {
        try {
            $volumeResults.Add((Get-VolumeArchiveInfo -ArchivePath $archive.FullName -HostDirectory $volumeDirectory))
        } catch {
            $errors.Add("volume $($archive.Name): $($_.Exception.Message)")
        }
    }

    if ($errors.Count -eq 0) {
        $report.result = 'pass'
    }
} catch {
    $errors.Add($_.Exception.Message)
} finally {
    foreach ($scratch in $scratchDatabases) {
        try {
            Invoke-Compose @('exec', '-T', 'postgres', 'dropdb', '-U', $script:postgresUser, '--if-exists', $scratch) | Out-Null
        } catch {
            Write-Warning "Could not drop $scratch`: $($_.Exception.Message)"
        }
    }
    $total.Stop()
    $report.total_seconds = [math]::Round($total.Elapsed.TotalSeconds, 2)
    $report.finished_at = [DateTime]::UtcNow.ToString('o')
    $report.databases = @($databaseResults)
    $report.volumes = @($volumeResults)
    $report.errors = @($errors)
    if ($errors.Count -gt 0) { $report.result = 'fail' }

    New-Item -ItemType Directory -Path $ReportDirectory -Force | Out-Null
    $jsonPath = Join-Path $ReportDirectory "drill-$timestamp.json"
    $markdownPath = Join-Path $ReportDirectory "drill-$timestamp.md"
    $report | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $jsonPath -Encoding UTF8
    $lines = @(
        "# Restore drill $timestamp",
        '',
        "- Result: **$($report.result)**",
        "- Host: $($report.host)",
        "- Bundle: ``$($report.bundle)``",
        "- SHA-256: ``$($report.bundle_sha256)``",
        "- Encrypted: $($report.encrypted)",
        "- Checksums verified: $($report.verified)",
        "- Database restore seconds: $($report.database_restore_seconds)",
        "- Total seconds: $($report.total_seconds)",
        '',
        '## Databases',
        '',
        '| Source | Scratch | Tables | Migrations | Invalid FKs | Seconds | Result |',
        '|---|---|---:|---:|---:|---:|---|'
    )
    foreach ($database in $report.databases) {
        $lines += "| $($database.source_database) | $($database.scratch_database) | $($database.restored.tables) | $($database.restored.schema_migrations) | $($database.restored.invalid_foreign_keys) | $($database.restore_seconds) | $(if ($database.ok) { 'pass' } else { 'fail' }) |"
    }
    $lines += @('', '## Volumes', '', '| Archive | Entries | Bytes |', '|---|---:|---:|')
    foreach ($volume in $report.volumes) {
        $lines += "| $($volume.archive) | $($volume.entries) | $($volume.bytes) |"
    }
    if ($errors.Count -gt 0) {
        $lines += @('', '## Errors', '')
        foreach ($error in $errors) { $lines += "- $error" }
    }
    $lines += @('', '_Database restore and schema checks only; full isolated-stack RTO is a manual quarterly drill._')
    $lines | Set-Content -LiteralPath $markdownPath -Encoding UTF8

    try {
        if ($backupctl -and (Test-Path -LiteralPath $backupctl)) {
            if ($report.result -eq 'pass') {
                & $backupctl status -file $statusFile -set verified | Out-Null
                & $backupctl status -file $statusFile -set drill-ok | Out-Null
            } else {
                & $backupctl status -file $statusFile -set drill-failed | Out-Null
            }
        }
    } catch {
        Write-Warning "Could not update the drill status: $($_.Exception.Message)"
    }

    if (-not $KeepExtracted -and (Test-Path -LiteralPath $extracted)) {
        Remove-Item -LiteralPath $extracted -Recurse -Force -ErrorAction SilentlyContinue
    }
    Write-Host "Drill report: $markdownPath" -ForegroundColor $(if ($report.result -eq 'pass') { 'Green' } else { 'Red' })
}

if ($report.result -ne 'pass') {
    throw "Restore drill failed: $($errors -join '; ')"
}
Write-Host "Restore drill passed in $($report.total_seconds) seconds." -ForegroundColor Green
