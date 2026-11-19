#Requires -Version 5.1
<#
.SYNOPSIS
Runs the local supply-chain checks required to be clean before a pull request.

.DESCRIPTION
Go: reachable vulnerabilities with govulncheck.
UI: lockfile audit at high severity.
AI: Python advisories for the pinned production requirements.

Image SBOM generation and Trivy scanning require built images, so they run in
the required "Images: docker build" CI job and are published as the
supply-chain-evidence artifact. They are not duplicated here.

Prerequisites: Go (matching api/go.mod), Node 24 with installed dependencies,
and Python with pip-audit available. pip-audit is installed on demand.
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$apiRoot = Join-Path $repoRoot 'api'
$uiRoot = Join-Path $repoRoot 'ui'
$aiRoot = Join-Path $repoRoot 'ai'
$failures = [System.Collections.Generic.List[string]]::new()

function Invoke-Step {
    param(
        [string]$Name,
        [string]$WorkingDirectory,
        [scriptblock]$Action
    )
    Write-Host ""
    Write-Host "==> $Name" -ForegroundColor Cyan
    Push-Location $WorkingDirectory
    try {
        & $Action
        if ($LASTEXITCODE -ne 0) {
            throw "$Name exited with code $LASTEXITCODE"
        }
        Write-Host "    PASS" -ForegroundColor Green
    } catch {
        $failures.Add("${Name}: $($_.Exception.Message)")
        Write-Host "    FAIL: $($_.Exception.Message)" -ForegroundColor Red
    } finally {
        Pop-Location
    }
}

Invoke-Step 'Go reachable vulnerabilities' $apiRoot {
    & go run golang.org/x/vuln/cmd/govulncheck@v1.10.0 ./...
}

Invoke-Step 'UI lockfile audit (high and critical)' $uiRoot {
    & npm audit --audit-level=high
}

Invoke-Step 'AI dependency audit' $aiRoot {
    & python -m pip install --quiet pip-audit==2.10.1
    if ($LASTEXITCODE -ne 0) { throw 'pip-audit installation failed' }
    & python -m pip_audit -r requirements-test.txt --progress-spinner off
}

Write-Host ""
if ($failures.Count -gt 0) {
    Write-Host "Supply-chain checks failed:" -ForegroundColor Red
    $failures | ForEach-Object { Write-Host " - $_" -ForegroundColor Red }
    exit 1
}

Write-Host "Supply-chain checks passed." -ForegroundColor Green
