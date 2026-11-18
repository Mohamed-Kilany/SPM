#Requires -Version 5.1
<#
.SYNOPSIS
Runs the full local verification gate for the SPM platform.

.DESCRIPTION
Backend: formatting check, vet, build and tests.
Frontend: typecheck, unit tests and production build.
Compose: validates the stack definition and checks running health when requested.
#>
[CmdletBinding()]
param(
    [switch]$SkipFrontend,
    [switch]$CheckStack
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$apiRoot = Join-Path $repoRoot 'api'
$uiRoot = Join-Path $repoRoot 'ui'
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

Invoke-Step 'Go formatting' $apiRoot {
    $unformatted = @(& gofmt -l internal cmd)
    if ($LASTEXITCODE -ne 0) { throw 'gofmt failed' }
    if ($unformatted.Count -gt 0) {
        throw "Unformatted files:`n$($unformatted -join "`n")"
    }
}

Invoke-Step 'Docker Go gate (vet, tests, build)' $repoRoot {
    & docker build --target go_test -f Dockerfile.test -t spm-go-test .
}

if (-not $SkipFrontend) {
    Invoke-Step 'Docker UI gate (typecheck, Vitest, production build)' $repoRoot {
        & docker build --target node_test -f Dockerfile.test -t spm-node-test .
    }
}

Invoke-Step 'Docker AI gate (FastAPI semantic search, RAG and insights tests)' $repoRoot {
    & docker build -f ai/Dockerfile.test -t spm-ai-test .
    if ($LASTEXITCODE -ne 0) { throw 'AI test image build failed' }
    & docker run --rm spm-ai-test
}

Invoke-Step 'Compose configuration' $repoRoot { docker compose config --quiet }

if ($CheckStack) {
    Invoke-Step 'Stack health' $repoRoot {
        $health = Invoke-RestMethod -Uri 'http://localhost:8080/health/ready' -TimeoutSec 10
        if ($health.status -ne 'ready') {
            throw "API readiness returned '$($health.status)'"
        }
    }
}

Write-Host ""
if ($failures.Count -gt 0) {
    Write-Host "Verification failed:" -ForegroundColor Red
    foreach ($failure in $failures) {
        Write-Host " - $failure" -ForegroundColor Red
    }
    exit 1
}

Write-Host "All verification checks passed." -ForegroundColor Green
