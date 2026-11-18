#Requires -Version 5.1
<#
.SYNOPSIS
Creates a consistent PostgreSQL custom-format backup of the SPM database.

.DESCRIPTION
Runs pg_dump inside the running postgres container, copies the dump to the
host, and verifies that pg_restore can read its table of contents.
#>
[CmdletBinding()]
param(
    [string]$OutputDirectory
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
if (-not $OutputDirectory) {
    $OutputDirectory = Join-Path $repoRoot 'backups'
}
$timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$containerPath = "/tmp/spm-$timestamp.dump"

New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$outputPath = Join-Path $OutputDirectory "spm-$timestamp.dump"

Push-Location $repoRoot
try {
    $user = if ($env:POSTGRES_USER) { $env:POSTGRES_USER } else { 'spm' }
    $database = if ($env:POSTGRES_DB) { $env:POSTGRES_DB } else { 'spm' }

    Write-Host "Creating database backup for '$database'..." -ForegroundColor Cyan
    docker compose exec -T postgres pg_dump -U $user -d $database -Fc --file=$containerPath
    if ($LASTEXITCODE -ne 0) { throw 'pg_dump failed' }

    docker compose cp "postgres:$containerPath" $outputPath
    if ($LASTEXITCODE -ne 0) { throw 'Could not copy the backup out of the container' }

    docker compose exec -T postgres pg_restore -l $containerPath | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'The generated backup failed pg_restore validation' }

    docker compose exec -T postgres rm -f $containerPath | Out-Null

    $size = (Get-Item $outputPath).Length
    Write-Host "Backup created and validated: $outputPath ($size bytes)" -ForegroundColor Green
    Write-Output $outputPath
} finally {
    Pop-Location
}
