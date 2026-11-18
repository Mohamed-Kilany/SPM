#Requires -Version 5.1
<#
.SYNOPSIS
Mirrors every image used by the Compose stack for air-gapped installation.

.DESCRIPTION
Without -Registry, saves each image as a tar archive plus SHA256 checksums.
With -Registry, also retags and pushes each image to a private registry.
#>
[CmdletBinding()]
param(
    [string]$OutputDirectory,
    [string]$Registry
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
if (-not $OutputDirectory) {
    $OutputDirectory = Join-Path $repoRoot 'images'
}
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null

Push-Location $repoRoot
try {
    $images = @(docker compose config --images | Sort-Object -Unique)
    if ($images.Count -eq 0) { throw 'No images were discovered in docker-compose.yaml' }

    Write-Host 'Building local images first...' -ForegroundColor Cyan
    docker compose build
    if ($LASTEXITCODE -ne 0) { throw 'docker compose build failed' }

    $manifest = @()
    foreach ($image in $images) {
        docker image inspect $image *> $null
        if ($LASTEXITCODE -ne 0) {
            Write-Host "Pulling $image" -ForegroundColor Cyan
            docker pull $image
            if ($LASTEXITCODE -ne 0) { throw "Could not pull $image" }
        } else {
            Write-Host "Using local image $image" -ForegroundColor DarkGray
        }

        $safeName = ($image -replace '[^A-Za-z0-9_.-]', '_')
        $archive = Join-Path $OutputDirectory "$safeName.tar"
        Write-Host "Saving $archive" -ForegroundColor Cyan
        docker save -o $archive $image
        if ($LASTEXITCODE -ne 0) { throw "Could not save $image" }

        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash
        Add-Content -Path (Join-Path $OutputDirectory 'SHA256SUMS') -Value "$hash  $(Split-Path -Leaf $archive)"
        $manifest += [pscustomobject]@{ source = $image; archive = (Split-Path -Leaf $archive); sha256 = $hash }

        if ($Registry) {
            $target = "$($Registry.TrimEnd('/'))/$image"
            Write-Host "Pushing $target" -ForegroundColor Cyan
            docker tag $image $target
            docker push $target
            if ($LASTEXITCODE -ne 0) { throw "Could not push $target" }
        }
    }

    $manifest | ConvertTo-Json -Depth 3 | Set-Content -Path (Join-Path $OutputDirectory 'manifest.json') -Encoding utf8
    Write-Host "Mirrored $($images.Count) image(s) to $OutputDirectory" -ForegroundColor Green
} finally {
    Pop-Location
}
