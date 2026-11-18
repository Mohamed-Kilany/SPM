# SPM Platform — Enterprise Architecture

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
| `ai_service` | CPU-only embeddings, semantic search and RAG |

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

- CPU-only embedding model for semantic retrieval.
- CPU-only small instruction model for RAG answers.
- API backend assembles authorized project context and sends it to the AI service.
- The AI service never reads PostgreSQL directly.
- Models are baked into the image or mounted from a mirrored model volume for air-gapped installs.
- If the model is unavailable, deterministic lexical retrieval and template recommendations keep the feature usable.

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

Every phase requires:

- `docker compose build` and healthy containers.
- Go formatting, vet, unit tests and integration tests.
- Vue typecheck, Vitest and production image build.
- Playwright E2E with axe WCAG 2.1 A/AA checks.
- No external requests, browser errors or console errors.
- SSE-driven state updates without manual refresh.
