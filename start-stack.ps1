# SPM Platform — one-command local stack bring-up.
#
# Responsibilities:
#   1. build the Vue SPA image with Docker (no host npm build),
#   2. start PostgreSQL 18, NATS JetStream and Gitea,
#   3. provision the Gitea admin user, API token, registration token and org,
#   4. start the API, UI, runner and remaining services.
#
# Safe to re-run: tokens are rotated and existing users/orgs are reused.

$ErrorActionPreference = 'Stop'
# Native tools such as `gitea admin user create` write expected diagnostics to
# stderr. Do not let PowerShell turn those into terminating errors; every call
# below still checks $LASTEXITCODE explicitly.
if (Get-Variable -Name PSNativeCommandUseErrorActionPreference -ErrorAction SilentlyContinue) {
    $PSNativeCommandUseErrorActionPreference = $false
}

function Wait-HttpOk {
    param(
        [Parameter(Mandatory = $true)][string]$Url,
        [string]$Name = $Url,
        [int]$TimeoutSeconds = 180
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 3
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300) {
                Write-Host "  $Name is ready." -ForegroundColor Green
                return
            }
        } catch {
            # Service is still starting.
        }
        Start-Sleep -Seconds 2
    }

    throw "$Name did not become ready within $TimeoutSeconds seconds ($Url)."
}

function Set-EnvValue {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Key,
        [Parameter(Mandatory = $true)][string]$Value
    )

    $lines = [System.Collections.Generic.List[string]]::new()
    if (Test-Path -LiteralPath $Path) {
        foreach ($line in Get-Content -LiteralPath $Path) { $lines.Add($line) }
    }

    $pattern = '^' + [regex]::Escape($Key) + '='
    $found = $false
    for ($i = 0; $i -lt $lines.Count; $i++) {
        if ($lines[$i] -match $pattern) {
            $lines[$i] = "$Key=$Value"
            $found = $true
            break
        }
    }
    if (-not $found) { $lines.Add("$Key=$Value") }

    [System.IO.File]::WriteAllLines($Path, [string[]]$lines, [System.Text.UTF8Encoding]::new($false))
}

function Get-EnvValue {
    param([string]$Path, [string]$Key, [string]$Default)

    if (-not (Test-Path -LiteralPath $Path)) { return $Default }
    $match = Get-Content -LiteralPath $Path |
        Where-Object { $_ -match ('^' + [regex]::Escape($Key) + '=') } |
        Select-Object -Last 1
    if (-not $match) { return $Default }
    return $match.Substring($match.IndexOf('=') + 1)
}

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

$envPath = Join-Path $root '.env'
if (-not (Test-Path -LiteralPath $envPath)) {
    throw ".env not found. Copy the documented local environment file before starting the stack."
}

Write-Host 'Step 0/8: Checking Docker and building the UI image...' -ForegroundColor Cyan
& docker info *> $null
if ($LASTEXITCODE -ne 0) { throw 'Docker is not running or is not reachable.' }

& docker compose build ui_static
if ($LASTEXITCODE -ne 0) { throw 'Failed to build the UI image.' }

Write-Host 'Step 1/8: Starting PostgreSQL 18, NATS, Gitea and k3s...' -ForegroundColor Cyan
& docker compose up -d postgres nats gitea k3s
if ($LASTEXITCODE -ne 0) { throw 'Failed to start infrastructure containers.' }

$giteaPort = Get-EnvValue -Path $envPath -Key 'GITEA_HTTP_PORT' -Default '3000'
$giteaUrl = "http://localhost:$giteaPort"

Write-Host 'Step 2/8: Waiting for Gitea and k3s to report healthy...' -ForegroundColor Cyan
Wait-HttpOk -Url "$giteaUrl/api/healthz" -Name 'Gitea' -TimeoutSeconds 240

$k3sDeadline = (Get-Date).AddSeconds(300)
while ((Get-Date) -lt $k3sDeadline) {
    & docker compose exec -T k3s kubectl get --raw=/readyz *> $null
    if ($LASTEXITCODE -eq 0) {
        Write-Host '  k3s is ready.' -ForegroundColor Green
        break
    }
    Start-Sleep -Seconds 2
}
if ((Get-Date) -ge $k3sDeadline) {
    throw 'k3s did not become ready within 300 seconds.'
}

Write-Host '  Waiting for CoreDNS...' -ForegroundColor Cyan
& docker compose exec -T k3s kubectl -n kube-system rollout status deployment/coredns --timeout=180s
if ($LASTEXITCODE -ne 0) { throw 'CoreDNS did not become ready within 180 seconds.' }
Write-Host '  CoreDNS is ready.' -ForegroundColor Green

Write-Host 'Step 3/8: Ensuring the Gitea admin user exists...' -ForegroundColor Cyan
$adminReady = $false
for ($attempt = 1; $attempt -le 20; $attempt++) {
    $createOutput = ''
    try {
        $createOutput = (& docker compose exec -T -u 1000 gitea gitea admin user create `
            --admin --username spm-admin --password spm_local_dev_password `
            --email admin@spm.local --must-change-password=false 2>&1 | Out-String)
    } catch {
        # Gitea reports an existing user on stderr with a non-zero exit code.
        $createOutput = "$_"
    }

    if ($LASTEXITCODE -eq 0) {
        Write-Host "  Gitea admin user 'spm-admin' created." -ForegroundColor Green
        $adminReady = $true
        break
    }

    if ($createOutput -match 'already exists') {
        Write-Host "  Gitea admin user 'spm-admin' already exists." -ForegroundColor Yellow
        $adminReady = $true
        break
    }

    Write-Host '  Gitea database is still initializing, retrying in 3 seconds...'
    Start-Sleep -Seconds 3
}

if (-not $adminReady) { throw 'Gitea admin user could not be provisioned.' }

Write-Host 'Step 4/8: Generating Gitea API and runner registration tokens...' -ForegroundColor Cyan
$tokenName = 'spm-api-' + (Get-Date -Format 'yyyyMMddHHmmss')
$tokenOutput = (& docker compose exec -T -u 1000 gitea gitea admin user generate-access-token `
    --username spm-admin --token-name $tokenName --scopes all --raw 2>&1)
if ($LASTEXITCODE -ne 0) { throw "Failed to generate a Gitea API token: $tokenOutput" }

$token = $tokenOutput |
    ForEach-Object { "$_".Trim() } |
    Where-Object { $_ -and $_ -notmatch '^(level|time)=' } |
    Select-Object -Last 1
if (-not $token) { throw 'Gitea returned an empty API token.' }

$runnerTokenOutput = (& docker compose exec -T -u 1000 gitea gitea actions generate-runner-token 2>&1)
$runnerToken = $null
if ($LASTEXITCODE -eq 0) {
    $runnerToken = $runnerTokenOutput |
        ForEach-Object { "$_".Trim() } |
        Where-Object { $_ -and $_ -notmatch '^(level|time)=' } |
        Select-Object -Last 1
}

if (-not $runnerToken) {
    # Fallback for Gitea builds that expose the token through the API.
    $headers = @{ Authorization = "token $token" }
    try {
        $registration = Invoke-RestMethod -Method Get `
            -Uri "$giteaUrl/api/v1/admin/runners/registration-token" -Headers $headers
        $runnerToken = $registration.token
    } catch {
        Write-Warning 'Runner registration token could not be generated. The runner will not start until GITEA_RUNNER_TOKEN is set.'
    }
}

Set-EnvValue -Path $envPath -Key 'GITEA_API_TOKEN' -Value $token
if ($runnerToken) {
    Set-EnvValue -Path $envPath -Key 'GITEA_RUNNER_TOKEN' -Value $runnerToken
}
Write-Host '  Tokens written to .env (values are not printed).' -ForegroundColor Green

Write-Host 'Step 5/8: Ensuring the Gitea organization exists...' -ForegroundColor Cyan
$headers = @{
    Authorization  = "token $token"
    'Content-Type' = 'application/json'
}
$orgBody = @{ username = 'spm'; visibility = 'public' } | ConvertTo-Json
try {
    $null = Invoke-RestMethod -Method Post -Uri "$giteaUrl/api/v1/orgs" -Headers $headers -Body $orgBody
    Write-Host "  Organization 'spm' created." -ForegroundColor Green
} catch {
    if ($_.Exception.Message -match '422') {
        Write-Host "  Organization 'spm' already exists." -ForegroundColor Yellow
    } else {
        Write-Warning "Failed to create organization 'spm': $($_.Exception.Message)"
    }
}

Write-Host 'Step 6/8: Importing Kubernetes images and installing Argo CD...' -ForegroundColor Cyan
& docker compose run --rm -T --no-deps k8s_images
if ($LASTEXITCODE -ne 0) { throw 'Failed to import the Kubernetes images into k3s.' }

& docker compose run --rm -T --no-deps k8s_bootstrap
if ($LASTEXITCODE -ne 0) { throw 'Failed to install Argo CD into k3s.' }

$argoDeployments = @(
    'argocd-applicationset-controller',
    'argocd-dex-server',
    'argocd-notifications-controller',
    'argocd-redis',
    'argocd-repo-server',
    'argocd-server'
)
foreach ($deployment in $argoDeployments) {
    & docker compose exec -T k3s kubectl -n argocd rollout status "deployment/$deployment" --timeout=300s
    if ($LASTEXITCODE -ne 0) { throw "Argo CD deployment '$deployment' did not become ready." }
}
& docker compose exec -T k3s kubectl -n argocd rollout status statefulset/argocd-application-controller --timeout=300s
if ($LASTEXITCODE -ne 0) { throw 'Argo CD application controller did not become ready.' }

Write-Host 'Step 7/8: Building and starting the API...' -ForegroundColor Cyan
& docker compose up -d --build api_backend
if ($LASTEXITCODE -ne 0) { throw 'Failed to build/start the API.' }

$apiPort = Get-EnvValue -Path $envPath -Key 'API_PORT' -Default '8080'
Wait-HttpOk -Url "http://localhost:$apiPort/health" -Name 'SPM API' -TimeoutSeconds 120

Write-Host 'Step 8/8: Building and starting the UI, runner and delivery services...' -ForegroundColor Cyan
& docker compose up -d --build
if ($LASTEXITCODE -ne 0) { throw 'Failed to start the remaining services. Run docker compose ps for details.' }

# k3s pods cannot resolve Docker Compose service names. Argo CD still needs to
# clone repositories from the internal Gitea service, so publish a stable
# in-cluster Service/Endpoints record that points at the current Gitea
# container IP. Re-running the stack refreshes the endpoint after any recreate.
Write-Host '  Bridging the internal Gitea service into k3s for Argo CD...' -ForegroundColor Cyan
$giteaIp = (& docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' spm-gitea-1).Trim()
if (-not $giteaIp) { throw 'Could not determine the Gitea container IP for the Argo CD repository bridge.' }
$giteaBridge = @"
apiVersion: v1
kind: Service
metadata:
  name: gitea
  namespace: argocd
spec:
  ports:
    - name: http
      port: 3000
      targetPort: 3000
---
apiVersion: v1
kind: Endpoints
metadata:
  name: gitea
  namespace: argocd
subsets:
  - addresses:
      - ip: $giteaIp
    ports:
      - name: http
        port: 3000
"@
$giteaBridge | docker compose exec -T k3s kubectl apply -f -
if ($LASTEXITCODE -ne 0) { throw 'Failed to create the Gitea bridge inside k3s.' }

$argocdPort = Get-EnvValue -Path $envPath -Key 'ARGOCD_PORT' -Default '9090'
Wait-HttpOk -Url "http://localhost:$argocdPort/api/version" -Name 'Argo CD' -TimeoutSeconds 300

$uiPort = Get-EnvValue -Path $envPath -Key 'UI_PORT' -Default '80'
Write-Host ''
Write-Host 'SPM Platform is running.' -ForegroundColor Green
Write-Host "  UI:       http://localhost:$uiPort"
Write-Host "  API:      http://localhost:$apiPort"
Write-Host "  Gitea:    $giteaUrl"
Write-Host "  Argo CD:  http://localhost:$argocdPort"
Write-Host "  k3s API:  https://localhost:$(Get-EnvValue -Path $envPath -Key 'K3S_API_PORT' -Default '6443')"
Write-Host '  Login:    admin / Admin@1234'
Write-Host ''
Write-Host 'Tip: docker compose ps shows service health; docker compose logs -f api_backend tails API logs.'
