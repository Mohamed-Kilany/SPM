# SPM Platform — Enterprise v2 Specification

> **Status:** active target specification for the v2 redesign, not a statement of current
> capability. **Last verified:** 2026-11-19 against `master` at
> [`666108f`](https://github.com/Mohamed-Kilany/SPM/commit/666108f).
>
> Checked items have code and automated-test evidence in this repository. They are implemented
> slices, not completed phases. Unchecked items remain outstanding. The required pull-request gate
> runs unit tests and image builds, not the integration or Playwright suites; see
> [`ARCHITECTURE.md`](ARCHITECTURE.md#9-verification) and `QAT-02`/`QAT-03` in
> [`TASKS.md`](../TASKS.md). Card-level status is tracked only in `TASKS.md`.

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
9. **AI is a service.** A Python CPU-only retrieval and answer service uses TF-IDF/cosine by
   default, with an optional local GGUF model, over live SPM data. Embeddings are tracked as
   `AI-01`.
10. **Docker is the build system.** The UI, API, tests and AI service are built by Docker. No host
    `npm run build` is required to run the stack.

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

Checked items have code and automated-test evidence. A checked item is an implemented slice, not a
completed phase; see the status note at the top of this document.

### Phase E1 — Foundation, identity and governance
**Evidence:** [`api/internal/auth`](../api/internal/auth), [`api/cmd/api/main.go`](../api/cmd/api/main.go),
[`ui/src/views/ProfileView.vue`](../ui/src/views/ProfileView.vue),
[`ui/src/views/AdminUsersView.vue`](../ui/src/views/AdminUsersView.vue),
[`ui/src/components/workspace/WorkspaceSettings.vue`](../ui/src/components/workspace/WorkspaceSettings.vue).

- [x] Single navigation shell; remove duplicate workspace tabs
- [x] Profile screen: display name, email, avatar, password change
- [x] Admin users and global roles: list, create, edit, activate/deactivate, password reset
- [x] Project roles and membership management
- [x] Project settings persistence: metadata, key prefix, default branch, policy
- [x] Docker-built UI in the production stack
- [x] Remove artificial memory caps from the enterprise Compose stack
- [ ] Migration `014_enterprise_identity.sql` — identity schema currently lives in
  `002_create_users.sql` and `003_create_project_members.sql`; the planned consolidated migration
  is not present
- [x] Unit, API and UI tests

### Phase E2 — Code workspace and navigation
**Evidence:** [`ui/src/components/workspace/WorkspaceCode.vue`](../ui/src/components/workspace/WorkspaceCode.vue),
[`ui/src/components/workspace/WorkspaceTraceability.vue`](../ui/src/components/workspace/WorkspaceTraceability.vue),
[`api/internal/gitengine`](../api/internal/gitengine).

- [x] Bitbucket-style Code section backed by Gitea
- [x] Repository, branch, commit, PR and review views
- [ ] Pull-request checks view
- [ ] Merge policy and merge action verified against a real Gitea in the required gate
- [x] Clickable parent/feature/test/build/deployment links
- [ ] Gitea deep links and repository browser handoff
- [ ] Code UI tests

### Phase E3 — Search, analytics and reporting
**Evidence:** [`api/internal/search`](../api/internal/search),
[`ui/src/views/SearchView.vue`](../ui/src/views/SearchView.vue),
[`ui/src/components/workspace/WorkspaceReports.vue`](../ui/src/components/workspace/WorkspaceReports.vue),
[`ui/src/components/charts`](../ui/src/components/charts).

- [x] JQL-style query parser and API
- [x] Global search with filters and saved queries (saved in browser storage)
- [ ] Recent searches
- [x] Donut, bar, line and burn-up/burn-down charts
- [ ] Configurable chart builder
- [x] CSV/JSON report export
- [ ] Scheduled report definitions
- [x] Search parser and handler tests
- [ ] Chart tests

### Phase E4 — Automated Git/CI/CD
**Evidence:** [`api/internal/webhook`](../api/internal/webhook); build and deployment routes are
read-only projections of Gitea Actions and Argo CD.

- [ ] Gitea Actions workflow ingestion verified from a real repository in the required gate
- [ ] Argo CD reconciliation and health synchronization verified in the required gate
- [x] Build and deployment records are webhook projections, not manual-entry writes
- [ ] Integration tests against real Gitea and Argo CD
- [ ] Pipeline and deployment SSE updates
- [ ] Failure/rollback tests

### Phase E5 — AI retrieval and answers
**Evidence:** [`ai/app.py`](../ai/app.py), [`ai/test_app.py`](../ai/test_app.py),
[`ai/README.md`](../ai/README.md), [`ui/src/components/workspace/AiCopilot.vue`](../ui/src/components/workspace/AiCopilot.vue).

- [x] Python CPU-only AI service
- [x] Local lexical (TF-IDF/cosine) index over API-supplied SPM artifacts
- [x] Semantic-search endpoint over supplied artifacts
- [x] Extractive question answering with citations and deterministic risk insights
- [x] AI copilot in the project overview
- [ ] Suggestion cards in search, planning, test and delivery
- [x] AI health reporting, including engine mode and whether the GGUF model is enabled
- [ ] Offline GGUF provisioning exercised by an automated test
- [x] AI service tests

### Phase E6 — Hardening
- [ ] Audit coverage and retention
- [ ] Performance targets with large datasets
- [ ] Backup/restore and air-gapped verification
- [ ] Accessibility, keyboard and responsive verification
- [ ] Full Docker E2E and operations sign-off

## 6. Acceptance rules

These are release requirements, not a description of current behaviour.

- Every mutation emits an activity event and updates the UI through SSE.
- Every write is authorized by global and project role.
- No feature is considered done without a Docker build, API test and UI test.
- No manual refresh control is required in normal operation.
- No runtime external network request is required.
- All secrets stay in environment variables and never appear in URLs or logs.

## 7. Current status

The v2 redesign is in progress. The following capabilities are implemented and covered by the
required gate or by in-repository tests:

- authentication, `/auth/me`, profile edits and password change;
- admin user and global-role management;
- project members, roles, settings and delivery policy;
- the persistent navigation shell and the project workspace surfaces (overview, planning, board,
  tests, code, delivery, reports and traceability);
- JQL-style search with saved browser-local filters;
- report charts and CSV/JSON export;
- a Python AI service using TF-IDF retrieval, extractive answers and deterministic insights.

Outstanding work includes the consolidated identity migration, a checks view, verified Gitea/Argo
integration, scheduled reports, chart tests, the AI embedding upgrade, the hardened integration/E2E
gate and the full hardening phase. [`TASKS.md`](../TASKS.md) tracks each card and its status.
