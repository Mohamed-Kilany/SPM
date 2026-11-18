# SPM Platform

SPM is an offline-first, enterprise software delivery and test management platform. It connects product requirements, planning, work items, tests, Git, CI, deployments, health and production feedback in one traceable workspace.

The platform is designed for private networks and air-gapped installations. It has no SaaS dependency and no runtime telemetry.

## Enterprise v2 direction

The current work is a v2 redesign. The target surface includes:

- One persistent navigation shell with global and project sections.
- User profiles, password changes, avatars, global roles and project roles.
- Project settings and membership management that persist.
- A Bitbucket-style Code workspace backed by Gitea.
- Automated Gitea Actions and Argo CD delivery evidence.
- JQL-style search, saved filters and linked artifacts.
- Donut, bar, line and burn-up/burn-down analytics.
- A Python CPU-only semantic search and RAG service.
- Docker-built UI and Docker-only verification.

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

```powershell
# Full backend/frontend/stack gate
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
- Air-gapped image mirroring: `scripts/mirror-images.ps1`.
- Health: `/health/live`, `/health/ready`, `/health`.
- Metrics: Prometheus-format endpoint from the API.

## Status

Enterprise v2 is under active implementation. The specification tracks completed and pending phases; no phase is marked complete until it is Docker-built and tested.
