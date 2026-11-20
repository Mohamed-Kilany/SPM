# SPM Platform — Enterprise Architecture

> **Last verified:** 2026-11-19 against `master` at [`666108f`](https://github.com/Mohamed-Kilany/SPM/commit/666108f).
> Evidence: [PR gate](../.github/workflows/pr-gate.yml), [CodeQL](../.github/workflows/codeql.yml),
> service code and tests under [`api/`](../api), [`ui/`](../ui) and [`ai/`](../ai).

## 1. Runtime topology

```
Browser
  │  HTTPS / SSE
  ▼
Envoy edge
  ├── Vue 3 SPA (built in Docker, served by internal nginx)
  ├── /api/*  → Go API
  └── /ai/*   → Python AI service
        │
        ├── PostgreSQL 18 (system of record)
        ├── NATS JetStream (events, SSE fan-out, invalidation)
        ├── Gitea (Git, pull requests, Actions)
        └── Argo CD (deployment reconciliation)
```

All services run in Docker Compose. The production UI image is built from `ui/Dockerfile`; the host `ui/dist` directory is not a runtime dependency.

## 2. Services

| Service | Responsibility |
|---|---|
| `ui_static` | Multi-stage Vue build served by nginx; no host build artifacts |
| `ui_envoy` | Public edge, SPA routing, API proxy, SSE-safe timeouts |
| `api_backend` | REST API, RBAC, workspace aggregate, webhooks, SSE |
| `postgres` | Normalized SDLC/STLC data, audit and delivery evidence |
| `nats` | Durable event stream and user invalidation |
| `gitea` | Offline Git provider, pull requests and Actions |
| `act_runner` | Gitea Actions runner |
| `argocd` | GitOps deployment and health reconciliation |
| `ai_service` | CPU-only TF-IDF retrieval, extractive answers, optional local GGUF generation |

## 3. Domain model

- **Identity:** users, global roles, project memberships, project roles.
- **Planning:** PRD → Epic → Feature → Work Item (Story/Task/Subtask/Bug/Spike/Chore).
- **Testing:** Test Case → Steps → Test Run → Result → Step Result → Bug.
- **Code:** Repository → Branch → Commit → Pull Request → Review → Checks.
- **Delivery:** Build → Deployment → Health Check → Rollback.
- **Traceability:** artifact links plus derived coverage and gap analysis.
- **Audit:** append-only activity events.

## 4. Authorization

Two layers are mandatory:

1. Global role from JWT (`admin`, `member`, `viewer`).
2. Project role from PostgreSQL (`owner`, `maintainer`, `developer`, `tester`, `viewer`).

Project writes resolve the membership from the database on every request so role changes take effect immediately. Global admins bypass project membership checks.

## 5. Real-time model

- Mutations write `activity_events`.
- `workspace.changed` is published to NATS JetStream.
- The worker updates the project snapshot and publishes SSE updates.
- The Vue workspace refreshes on every SSE connect/reconnect and on domain events.
- Normal operation has no manual refresh controls or live-status chrome.

## 6. AI service

The AI service is a stateless Python FastAPI application:

- The default retrieval engine is TF-IDF/cosine over the context supplied by the API. It is
  deterministic, CPU-only and requires no model download.
- The default answer engine is extractive: it selects and cites sentences from the supplied
  artifacts. `/insights` produces deterministic delivery-risk and coverage findings.
- No embedding model is shipped or downloaded today. Embedding-based retrieval is tracked as
  `AI-01` in [`TASKS.md`](../TASKS.md).
- An optional local GGUF model can generate answers through `llama-cpp-python`. It is enabled only
  when `SPM_AI_MODEL_PATH` points to a mounted model, runs with `n_gpu_layers=0`, and falls back to
  the extractive engine if the model is missing or fails.
- The API backend assembles authorized project context and sends it to the AI service.
- The AI service never reads PostgreSQL directly and makes no external network calls.
- For air-gapped installs, an optional GGUF file is mirrored and mounted; it is not baked into the
  default image.

## 7. Security

- JWT HS256 with short expiry.
- Single-use SSE tickets; JWTs never in query strings.
- Webhook HMAC or bearer tokens with constant-time comparison.
- Password hashing with bcrypt.
- Login and webhook rate limiting.
- Security headers at nginx and Envoy.
- No telemetry, analytics or external fonts.
- Secrets only from environment or secret stores.

## 8. Docker and delivery

- `docker compose up -d --build` is the supported build/run path.
- The test stack is isolated in `docker-compose.test.yml`.
- UI, API, integration and E2E images are built from source.
- No artificial memory limits are applied to the enterprise stack.
- Images can be mirrored to a private registry for air-gapped installation.

## 9. Verification

### 9.1 Required pull-request gate

The pull-request-only workflow in [`.github/workflows/pr-gate.yml`](../.github/workflows/pr-gate.yml)
runs on every non-draft PR targeting `master`:

- Go: `go mod verify`, `gofmt` check, `go vet ./...`, `govulncheck`, OpenAPI contract check,
  `go test ./... -race`, static binary build.
- UI: `npm audit --audit-level=high`, OpenAPI lint with Redocly, `vue-tsc --noEmit`, Vitest unit
  tests, Vite production build.
- AI: `pip-audit` against the pinned requirements, `pytest` for the FastAPI service.
- Compose: `docker compose config` for the production and isolated test stacks.
- Images: Docker build, Syft SBOM generation and Trivy HIGH/CRITICAL scan of the API, UI and AI
  images.
- Security: dependency review; CodeQL for Go, JavaScript/TypeScript and Python.

A separate CodeQL workflow scans on pull requests. All listed contexts are required on `master`.

### 9.2 Supply chain

The Images job publishes three SPDX SBOMs and three SARIF scan reports as the
`supply-chain-evidence` artifact on every pull request. SBOM generation and scanning failures
fail the required context; scan findings are advisory until the image baseline is clean. The
promotion rule, exception register, disclosure policy and the image-signing gap are documented
in [`SECURITY.md`](../SECURITY.md). Dependency vulnerabilities are blocking today:
`govulncheck`, `npm audit --audit-level=high` and `pip-audit` all run with a clean baseline.

### 9.3 API contract

`docs/openapi.yaml` is generated from `api/internal/server.Routes()`, which is checked
against the real Gin registration by `TestRoutesMatchRegistration`. Three additional checks
prevent drift:

- `TestOpenAPIContractMatchesRouter` requires an exact method/path match in both directions.
- `TestOpenAPIOperationsAreComplete` requires auth metadata, security, success and error
  responses, path parameters and request bodies.
- `TestUIClientPathsExist` requires every path used by `ui/src/api/client.ts` to exist in the
  contract.

The Go job runs `go run ./cmd/openapi -check`; the UI job runs
`npx @redocly/cli lint docs/openapi.yaml`. Regenerate with `scripts/openapi.ps1` (or
`cd api && go run ./cmd/openapi -write`). The generator is deterministic and preserves the
hand-maintained schemas under `components` while removing unreachable components. The versioning
and deprecation policy is in [`API.md`](API.md).

### 9.4 Not in the required gate

These exist but run locally or manually today:

- Go integration tests in `api/tests/integration` (build tag `integration`), including PostgreSQL,
  NATS, Gitea and SSE scenarios. Tracked as `QAT-02`.
- Playwright end-to-end tests in `ui/e2e`, including axe accessibility checks. Tracked as `QAT-03`.
- Running-stack health checks from `scripts/verify.ps1 -CheckStack`.

No document should claim these run on every PR until the workflows are added and required.

### 9.5 Phase-completion target

A phase is complete only when its Docker build, automated API/UI tests and required E2E/integration
coverage are green. Until `QAT-02` and `QAT-03` land, phase checkboxes in
[`ENTERPRISE_SPEC.md`](ENTERPRISE_SPEC.md) mark implemented slices with code/test evidence, not
fully gated phases.

## 10. Release and versioning

`master` is the integration line; a release is an annotated `vMAJOR.MINOR.PATCH` tag on a merged
`master` commit. Staging and production deploy that same tag, never a branch head.

Build metadata is injected from the tag at image build time:

- API: `-ldflags -X main.version/commit/buildDate`; every health probe returns
  `build.version`, `build.commit` and `build.built_at`.
- UI: `VITE_APP_VERSION` is embedded at Vite build time and shown in the navigation footer.
- AI: `SPM_AI_VERSION` is read at process start and returned by `/health`.

`scripts/release.ps1` prepares the changelog pull request and tags the merged version.
`scripts/changelog.ps1` generates grouped entries from first-parent conventional commits, pull
request numbers and task IDs. Pushing a tag starts
[`.github/workflows/release.yml`](../.github/workflows/release.yml), which verifies the tag is on
`master`, re-runs the supply-chain checks, builds and scans the images, archives SBOM/SARIF
evidence and publishes the GitHub release. It never commits or opens pull requests.

Schema migrations are embedded in the API and applied at startup. Releases follow expand/contract:
the previous tag must remain compatible with the expanded schema for the rollback window. The
deploy order, rollback procedure, compatibility rules and the `OPS-09` rehearsal handoff are in
[`RELEASING.md`](RELEASING.md).
