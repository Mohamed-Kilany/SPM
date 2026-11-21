# SPM Platform

SPM is an offline-first, enterprise software delivery and test management platform. It connects product requirements, planning, work items, tests, Git, CI, deployments, health and production feedback in one traceable workspace.

The platform is designed for private networks and air-gapped installations. It has no SaaS dependency and no runtime telemetry.

> **Last verified:** 2026-11-19 against `master` at [`666108f`](https://github.com/Mohamed-Kilany/SPM/commit/666108f).
> Evidence: [PR gate](.github/workflows/pr-gate.yml), [CodeQL](.github/workflows/codeql.yml),
> [delivery plan](TASKS.md).

## Enterprise v2 target surface

The current work is a v2 redesign. The target surface includes:

- One persistent navigation shell with global and project sections.
- User profiles, password changes, avatars, global roles and project roles.
- Capability-based authorization and an append-only, hash-chained audit log with
  admin search, export and chain verification.
- Project settings and membership management that persist.
- A Bitbucket-style Code workspace backed by Gitea.
- Automated Gitea Actions and Argo CD delivery evidence.
- JQL-style search, saved filters and linked artifacts.
- Donut, bar, line and burn-up/burn-down analytics.
- A Python CPU-only retrieval and answer service: TF-IDF/cosine by default, with an optional local GGUF model; embeddings are tracked as `AI-01`.
- A Docker-built UI image and a Docker Compose stack, with image builds in the pull-request gate.

This is the target surface, not a claim that every item is complete. [TASKS.md](TASKS.md) is the
card-level delivery source of truth.

See [docs/ENTERPRISE_SPEC.md](docs/ENTERPRISE_SPEC.md) and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Quick start

Prerequisites: Docker Desktop or Docker Engine with Compose.

```powershell
Copy-Item .env.example .env
# edit .env secrets, then:
.\start-stack.ps1
```

The stack builds the UI and API from source:

- UI: http://localhost
- API: http://localhost:8080
- Gitea: http://localhost:3000
- Argo CD: http://localhost:9090

Demo login: `admin` / `Admin@1234` (change it after first login).

## Docker-only builds

```powershell
docker compose build
docker compose up -d
```

The production UI is built by `ui/Dockerfile`. Host `ui/dist` is not mounted into the runtime container.

## Verification

The required pull-request gate (`.github/workflows/pr-gate.yml`) runs:

| Area | Checks |
|---|---|
| Go | `go mod verify`, `gofmt`, `go vet`, `govulncheck`, `go test ./... -race`, static build |
| API contract | route inventory matches `docs/openapi.yaml`; Redocly lint |
| UI | `npm audit --audit-level=high`, `vue-tsc --noEmit`, Vitest, Vite production build |
| AI | `pip-audit`, `pytest` for the FastAPI service |
| Compose | `docker compose config` for the production and test stacks |
| Images | Docker build, Syft SPDX SBOM and Trivy HIGH/CRITICAL scan of the API, UI and AI images |
| Security | dependency review, CodeQL, Dependabot alerts and secret scanning with push protection |

The Images job publishes `supply-chain-evidence` (three SBOMs and three SARIF reports) for every
pull request. SBOM generation and scanning are required, but scan findings are advisory until
the image baseline is clean; the promotion rule, exception register and disclosure policy are in
[SECURITY.md](SECURITY.md). `scripts/supply-chain.ps1` runs the dependency checks locally.

The API contract is generated from the canonical route inventory in
[`api/internal/server`](api/internal/server) and cannot drift silently:
`api/internal/server/contract_test.go` checks route coverage, operation fields and
UI-client path parity, `go run ./cmd/openapi -check` fails on an out-of-date
document, and Redocly validates the schema. Regenerate after changing a route:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/openapi.ps1
```

See [docs/API.md](docs/API.md) for the source-of-truth map and the versioning policy.

The gate does **not** currently run the Go integration suite (`api/tests/integration`, build tag
`integration`) or Playwright end-to-end tests (`ui/e2e`). Both run locally and are tracked as
`QAT-02` and `QAT-03` in [TASKS.md](TASKS.md).

Local commands (the full gate and E2E require a local Docker engine):

```powershell
# Full backend/frontend/AI/Compose gate
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1

# Include running-stack health checks
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -CheckStack

# Dependency vulnerability checks (no Docker required)
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/supply-chain.ps1

# Regenerate or verify the OpenAPI contract
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/openapi.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/openapi.ps1 -Check

# Isolated integration stack
docker compose -f docker-compose.test.yml up --build --exit-code-from test_runner

# Browser E2E (Playwright, system Chrome)
cd ui
npm run test:e2e
```

## Repository layout

```
api/                 Go API, migrations, domain services
ui/                  Vue 3 SPA, Dockerfile, Playwright E2E
docs/                Enterprise specification and architecture
SECURITY.md          Disclosure, supply-chain and exception policy
CHANGELOG.md         Generated release history
scripts/             Verification, backup, restore, load and mirroring tools
envoy/ nginx/        Edge and static-serving configuration
docker-compose.yaml  Production stack
docker-compose.test.yml  Isolated test stack
```

## Operations

- Backup/restore: `scripts/backup.ps1`, `scripts/restore.ps1`.
- Load test: `scripts/loadtest.ps1`.
- Air-gapped image mirroring: `scripts/mirror-images.ps1` (a verified runbook is `OPS-02`).
- Release: `scripts/release.ps1` prepares the changelog pull request and tags the merged version.
  The full process, promotion and rollback model is in [docs/RELEASING.md](docs/RELEASING.md).
- Changelog: [CHANGELOG.md](CHANGELOG.md), generated by `scripts/changelog.ps1`.
- Work items: fields, comments/mentions, watchers, links and attachment policy are documented
  in [docs/WORK-ITEMS.md](docs/WORK-ITEMS.md).
- Sprints and releases: start/close, carry-over, velocity, freeze, scope exceptions and sign-off
  are documented in [docs/PLANNING-LIFECYCLE.md](docs/PLANNING-LIFECYCLE.md).
- Test plans and suites: ordered membership by filter or CSV, run scope resolution and immutable
  run snapshots are documented in [docs/TEST-MANAGEMENT.md](docs/TEST-MANAGEMENT.md).
- Health: `/health/live`, `/health/ready`, `/health`; each probe reports the build `version`,
  `commit` and `built_at` injected from the release tag.
- UI version: shown in the navigation footer from `VITE_APP_VERSION`.
- Metrics: `/metrics` is served by a separate internal listener (`METRICS_ADDR`, default
  `0.0.0.0:9090` in Compose; the port is not published). Set `METRICS_TOKEN` to require a bearer
  token. The metric catalog, cardinality rules and example queries are in
  [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md). Tracing is tracked as `OBS-02`.

## Status

Enterprise v2 is under active implementation. The verification table above describes exactly what
the required gate proves today. Checked items in [docs/ENTERPRISE_SPEC.md](docs/ENTERPRISE_SPEC.md)
have code and test evidence; a checked item is not a completed phase. [TASKS.md](TASKS.md) is the
single source of truth for delivery status.
