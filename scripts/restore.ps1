#Requires -Version 5.1
<#
.SYNOPSIS
Restores an SPM PostgreSQL backup into the running Compose stack.

.DESCRIPTION
Stops the API to avoid writes, restores the custom-format dump with cleanup,
then starts the API and waits for readiness. This is destructive: set
SPM_RESTORE_CONFIRM=YES to acknowledge replacing the current database.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$BackupPath
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$resolved = Resolve-Path -LiteralPath $BackupPath -ErrorAction Stop

if ($env:SPM_RESTORE_CONFIRM -ne 'YES') {
    throw 'Refusing to restore. Set SPM_RESTORE_CONFIRM=YES to replace the current database.'
}

$containerPath = '/tmp/spm-restore.dump'
Push-Location $repoRoot
try {
    $user = if ($env:POSTGRES_USER) { $env:POSTGRES_USER } else { 'spm' }
    $database = if ($env:POSTGRES_DB) { $env:POSTGRES_DB } else { 'spm' }

    Write-Host 'Stopping the API...' -ForegroundColor Cyan
    docker compose stop api_backend | Out-Null

    Write-Host 'Copying the backup into PostgreSQL...' -ForegroundColor Cyan
    docker compose cp $resolved "postgres:$containerPath"
    if ($LASTEXITCODE -ne 0) { throw 'Could not copy the backup into the container' }

    Write-Host 'Restoring the database...' -ForegroundColor Cyan
    docker compose exec -T postgres pg_restore --clean --if-exists --no-owner --no-privileges `
        -U $user -d $database $containerPath
    if ($LASTEXITCODE -ne 0) { throw 'pg_restore failed' }

    docker compose exec -T postgres rm -f $containerPath | Out-Null

    Write-Host 'Starting the API...' -ForegroundColor Cyan
    docker compose start api_backend | Out-Null

    $deadline = (Get-Date).AddSeconds(90)
    do {
        Start-Sleep -Seconds 2
        try {
            $health = Invoke-RestMethod -Uri 'http://localhost:8080/health/ready' -TimeoutSec 5
            if ($health.status -eq 'ready') {
                Write-Host 'Restore completed; API is ready.' -ForegroundColor Green
                return
            }
        } catch {
            # Retry until the deadline.
        }
    } while ((Get-Date) -lt $deadline)

    throw 'Restore completed but the API did not become ready within 90 seconds'
} finally {
    Pop-Location
}
