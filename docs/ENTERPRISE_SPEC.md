# SPM Platform — Enterprise v2 Specification

> Status: **active redesign**. This document replaces the deleted v1 task file.
> Every phase below is implemented in Docker and verified against the Compose stack.

## 1. Product intent

SPM is an offline-first, enterprise software delivery and test management platform. It is the single system of record from product requirement through code, CI, deployment, health and production feedback.

It must work without SaaS dependencies and must remain usable on a private network with no outbound internet access after images and models are mirrored.

## 2. Non-negotiable outcomes

1. **One navigation model.** No duplicate project tab bars. A single persistent sidebar owns global and project navigation.
2. **Identity and access are first-class.** Users, profile, avatars, password changes, global roles, project roles, membership management and audit.
3. **Projects persist completely.** Project metadata, key prefix, default branch, environments, members and delivery policy are saved and reloadable.
4. **Code is a first-class workspace.** A Bitbucket-style surface backed by Gitea: repositories, branches, commits, pull requests, reviews, checks, merge and traceability.
5. **Delivery is automated.** Gitea Actions and Argo CD populate builds, deployments and health through webhooks and reconciliation. Manual entry is an administrative fallback, not the normal path.
6. **Search is queryable.** A JQL-style query language across all artifacts with saved filters and a global command surface.
7. **Artifacts are navigable.** Parent, feature, test, branch, pull request, build and deployment links open the linked record.
8. **Analytics are visual.** Pie/donut, bar, line, burn-up/burn-down and configurable metric charts.
9. **AI is a service.** A Python CPU-only semantic search and RAG service provides suggestions and recommendations over live SPM data.
10. **Docker is the build system.** The UI, API, tests and AI service are built by Docker. No host `npm run build` is required to run the stack.

## 3. Personas and roles

### Global roles

| Role | Capabilities |
|---|---|
| `admin` | Full platform administration, users, roles, all projects, audit |
| `member` | Create projects, access memberships, normal work |
| `viewer` | Read-only access to memberships |

### Project roles

| Role | Capabilities |
|---|---|
| `owner` | Full project control, membership, settings, delivery and destructive actions |
| `maintainer` | Settings, planning, code, delivery, test management |
| `developer` | Planning, work items, code, builds, non-production deployment |
| `tester` | Test cases, test runs, defects, read code/delivery |
| `viewer` | Read-only |

Project roles are enforced server-side. UI visibility is not security.

## 4. Enterprise surface map

### Global
- Home / portfolio
- Projects
- Search and saved queries
- Insights
- Administration → Users and roles
- Profile and security
- Audit log

### Project
- Overview
- Planning
- Board
- Test lab
- Code (Gitea-backed)
- Delivery
- Reports and analytics
- Traceability
- Settings (details, members, environments, integrations, policy)

## 5. Phase plan

### Phase E1 — Foundation, identity and governance
- [ ] Single navigation shell; remove duplicate workspace tabs
- [ ] Profile screen: display name, email, avatar, password change
- [ ] Admin users and global roles: list, create, edit, activate/deactivate, password reset
- [ ] Project roles and membership management
- [ ] Project settings persistence: metadata, key prefix, default branch, policy
- [ ] Docker-built UI in the production stack
- [ ] Remove artificial memory caps from the enterprise Compose stack
- [ ] Migration `014_enterprise_identity.sql`
- [ ] Unit, API and UI tests

### Phase E2 — Code workspace and navigation
- [ ] Bitbucket-style Code section backed by Gitea
- [ ] Repository, branch, commit, PR, review and checks views
- [ ] Merge policy and merge action with real Gitea synchronization
- [ ] Clickable parent/feature/test/build/deployment links
- [ ] Gitea deep links and repository browser handoff
- [ ] Code UI tests

### Phase E3 — Search, analytics and reporting
- [ ] JQL-style query parser and API
- [ ] Global search with filters, saved queries and recent searches
- [ ] Donut, bar, line, burn-up/burn-down charts
- [ ] Configurable chart builder
- [ ] Export CSV/JSON and scheduled report definitions
- [ ] Search and chart tests

### Phase E4 — Automated Git/CI/CD
- [ ] Gitea Actions workflow ingestion from a real repository
- [ ] Argo CD reconciliation and health synchronization
- [ ] Remove manual build/deployment as the default path
- [ ] Integration tests against real Gitea and Argo CD
- [ ] Pipeline and deployment SSE updates
- [ ] Failure/rollback tests

### Phase E5 — AI semantic search and RAG
- [ ] Python CPU-only AI service
- [ ] Local embedding index over SPM artifacts
- [ ] Semantic search endpoint
- [ ] RAG question answering and recommendations
- [ ] Suggestion cards in search, planning, test and delivery
- [ ] Offline model provisioning and health checks
- [ ] AI service tests

### Phase E6 — Hardening
- [ ] Audit coverage and retention
- [ ] Performance targets with large datasets
- [ ] Backup/restore and air-gapped verification
- [ ] Accessibility, keyboard and responsive verification
- [ ] Full Docker E2E and operations sign-off

## 6. Acceptance rules

- Every mutation emits an activity event and updates the UI through SSE.
- Every write is authorized by global and project role.
- No feature is considered done without a Docker build, API test and UI test.
- No manual refresh control is required in normal operation.
- No runtime external network request is required.
- All secrets stay in environment variables and never appear in URLs or logs.

## 7. Current status

The v2 redesign is in progress. Completed items are checked as each Docker-verified slice lands. The v1 code remains as a compatibility base while the enterprise surfaces are added.
