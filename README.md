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
| Go | `gofmt`, `go vet`, `go test ./... -race`, static build |
| UI | `vue-tsc --noEmit`, Vitest, Vite production build |
| AI | `pytest` for the FastAPI service |
| Compose | `docker compose config` for the production and test stacks |
| Images | Docker build of the API, UI and AI images |
| Security | dependency review and CodeQL for Go, JavaScript/TypeScript and Python |

The gate does **not** currently run the Go integration suite (`api/tests/integration`, build tag
`integration`) or Playwright end-to-end tests (`ui/e2e`). Both run locally and are tracked as
`QAT-02` and `QAT-03` in [TASKS.md](TASKS.md).

Local commands (require a local Docker engine):

```powershell
# Full backend/frontend/AI/Compose gate
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1

# Include running-stack health checks
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -CheckStack

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
scripts/             Verification, backup, restore, load and mirroring tools
envoy/ nginx/        Edge and static-serving configuration
docker-compose.yaml  Production stack
docker-compose.test.yml  Isolated test stack
```

## Operations

- Backup/restore: `scripts/backup.ps1`, `scripts/restore.ps1`.
- Load test: `scripts/loadtest.ps1`.
- Air-gapped image mirroring: `scripts/mirror-images.ps1` (a verified runbook is `OPS-02`).
- Health: `/health/live`, `/health/ready`, `/health`.
- Metrics: not yet exposed. Tracked as `OBS-01` in `TASKS.md`; there is no Prometheus endpoint today.

## Status

Enterprise v2 is under active implementation. The verification table above describes exactly what
the required gate proves today. Checked items in [docs/ENTERPRISE_SPEC.md](docs/ENTERPRISE_SPEC.md)
have code and test evidence; a checked item is not a completed phase. [TASKS.md](TASKS.md) is the
single source of truth for delivery status.
