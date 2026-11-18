#Requires -Version 5.1
<#
.SYNOPSIS
Runs a transaction-scoped PostgreSQL load test for the workspace indexes.

.DESCRIPTION
Inserts 10,000 work items and 100,000 activity events inside a transaction,
runs EXPLAIN ANALYZE on the main workspace queries, then rolls everything back.
No test data remains in the database.
#>
[CmdletBinding()]
param(
    [string]$ProjectId
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$user = if ($env:POSTGRES_USER) { $env:POSTGRES_USER } else { 'spm' }
$database = if ($env:POSTGRES_DB) { $env:POSTGRES_DB } else { 'spm' }

Push-Location $repoRoot
try {
    if (-not $ProjectId) {
        $ProjectId = (docker compose exec -T postgres psql -U $user -d $database -tAc `
            'SELECT id FROM projects ORDER BY created_at ASC LIMIT 1').Trim()
    }
    if (-not $ProjectId) {
        throw 'No project found. Create a project before running the load test.'
    }

    $sql = @"
\timing on
BEGIN;
INSERT INTO work_items (project_id, type, title, status, priority)
SELECT :'project_id'::uuid, 'task', 'Load test item ' || g, 'backlog', 'medium'
FROM generate_series(1, 10000) AS g;

INSERT INTO activity_events (project_id, entity_type, action, summary)
SELECT :'project_id'::uuid, 'work_item', 'load_test', 'Synthetic load event ' || g
FROM generate_series(1, 100000) AS g;

ANALYZE work_items;
ANALYZE activity_events;

EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
SELECT count(*) FROM work_items WHERE project_id = :'project_id'::uuid;

EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
SELECT id, title, status FROM work_items
WHERE project_id = :'project_id'::uuid
ORDER BY updated_at DESC LIMIT 50;

EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
SELECT id, entity_type, action, summary, created_at FROM activity_events
WHERE project_id = :'project_id'::uuid
ORDER BY created_at DESC LIMIT 50;

EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
SELECT status, count(*) FROM work_items
WHERE project_id = :'project_id'::uuid GROUP BY status;

ROLLBACK;
"@

    Write-Host "Running load test against project $ProjectId..." -ForegroundColor Cyan
    $sql | docker compose exec -T postgres psql -U $user -d $database -v ON_ERROR_STOP=1 -v project_id=$ProjectId
    if ($LASTEXITCODE -ne 0) { throw 'Load test SQL failed' }
    Write-Host 'Load test completed and rolled back.' -ForegroundColor Green
} finally {
    Pop-Location
}
