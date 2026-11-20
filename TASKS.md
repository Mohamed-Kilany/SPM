# SPM Platform — Enterprise Delivery Task Plan

> **Status:** Active · **Baseline:** clean `master` bootstrap · **Owner:** Mohamed Kilany
> **Last updated:** 2026-11-18 · **Review cadence:** weekly, and at every milestone exit
>
> This document is the delivery source of truth for sequencing, branch rules, acceptance
> criteria and UI direction. `docs/ENTERPRISE_SPEC.md` defines product intent,
> `docs/ARCHITECTURE.md` defines the runtime topology, and `docs/openapi.yaml` defines the
> HTTP contract. Where those documents disagree with the code or with this plan, the
> discrepancy is a defect and must be fixed by a task in this plan — never silently ignored.

---

## Contents

1. [How to use this document](#1-how-to-use-this-document)
2. [Operating model](#2-operating-model)
3. [Definition of Ready and Definition of Done](#3-definition-of-ready-and-definition-of-done)
4. [Baseline snapshot and gap register](#4-baseline-snapshot-and-gap-register)
5. [UI research synthesis and design system](#5-ui-research-synthesis-and-design-system)
6. [Screen-by-screen redesign blueprints](#6-screen-by-screen-redesign-blueprints)
7. [Phased task plan](#7-phased-task-plan)
8. [Cross-cutting engineering standards](#8-cross-cutting-engineering-standards)
9. [Milestones and sequencing](#9-milestones-and-sequencing)
10. [Risk register](#10-risk-register)
11. [Architecture decision log](#11-architecture-decision-log)
12. [Metrics and release certification](#12-metrics-and-release-certification)
13. [References](#13-references)
14. [Appendix A — branch, commit and label conventions](#appendix-a--branch-commit-and-label-conventions)
15. [Appendix B — glossary](#appendix-b--glossary)
16. [Appendix C — task index and status summary](#appendix-c--task-index-and-status-summary)

---

## 1. How to use this document

### 1.1 Rules of engagement

1. **One task card, one branch, one pull request.** A card may be split before work starts,
   but two unrelated cards never share a branch.
2. **No implementation starts before the card meets the Definition of Ready.** If a
   dependency is missing, the card is blocked, not "worked around".
3. **No card is complete until it meets the Definition of Done.** "Works on my machine"
   is not evidence; the PR gate is the evidence.
4. **Production is `master`.** It is advanced only by merging a pull request whose required
   checks are green. There are no direct pushes, no force pushes and no branch deletion.
5. **CI runs on pull requests only.** The workflows in `.github/workflows/` are triggered by
   `pull_request` events, not by `push` or individual commits. A pull request is the unit of
   verification. Draft PRs are allowed and encouraged for early feedback.
6. **Task IDs are permanent.** `GOV-03` always means the same task. If a task is dropped,
   mark it `CANCELLED` with a reason; never reuse the ID.
7. **Every UI change starts from the screen blueprint in Section 6.** If the blueprint and
   the implementation disagree, update the blueprint in the same PR or explain why.
8. **Every mutation is authorized server-side, audited and streamed.** UI hiding is never a
   security control.
9. **Every card carries tests, documentation, telemetry and rollback notes.** These are not
   "follow-up work".
10. **Prefer boring, reversible, observable changes.** Enterprise quality is the ability to
    change the system safely, not the number of features shipped.

### 1.2 Card lifecycle

```text
BACKLOG ──► READY ──► IN PROGRESS ──► IN REVIEW ──► VERIFIED ──► DONE
                │                         │
                └── BLOCKED ◄─────────────┘
```

| Status | Meaning | Exit condition |
|---|---|---|
| `BACKLOG` | Captured, not refined | Refined and prioritized |
| `READY` | Meets Definition of Ready | Branch created |
| `IN PROGRESS` | Branch exists, draft PR open | Implementation + local checks complete |
| `IN REVIEW` | PR ready for review | Required checks green |
| `VERIFIED` | Merged and observed in the target environment | Post-merge verification complete |
| `DONE` | Acceptance criteria evidenced | Card closed |
| `BLOCKED` | Cannot proceed | Blocker removed and recorded |

### 1.3 Status vocabulary used in this file

`DONE` (verified in the baseline), `TODO`, `IN PROGRESS`, `BLOCKED`, `CANCELLED`.
The baseline bootstrap cards (`BASE-01` to `BASE-03`) are the only cards that were completed
outside the normal PR flow, because they created the flow itself. Every later card follows
the branch-and-PR rule.

### 1.4 Definition of "production"

Production is the `master` branch plus a tagged release (`vMAJOR.MINOR.PATCH`) deployed by
Argo CD from the release manifest repository. A merge to `master` is deployable but is not
automatically a production release. Release approval is a separate, audited action.

---

## 2. Operating model

### 2.1 Branching model

Trunk-based development with short-lived branches. `master` is the only long-lived branch.
Release tags are immutable; there are no release branches unless a security hotfix must be
backported, in which case a temporary `release/x.y` branch is allowed and must be deleted
after the backport.

| Change | Branch pattern | Example |
|---|---|---|
| Feature | `feature/<area>-<slug>` | `feature/authz-capabilities` |
| Bug fix | `fix/<area>-<slug>` | `fix/membership-session-invalidation` |
| Behaviour change | `change/<area>-<slug>` | `change/projects-default-table` |
| Documentation | `docs/<slug>` | `docs/openapi-truth` |
| Chore / tooling | `chore/<slug>` | `chore/supply-chain-baseline` |
| Release backport | `release/<version>` | `release/2.4` |

Rules:

- Branch from the current `master`; never from another feature branch unless the dependency
  is explicitly recorded in the card.
- Rebase on `master` at least once per day while the PR is open. Never merge `master` into a
  feature branch with a merge commit; keep history linear.
- Delete the branch after merge. The PR is the permanent record.
- A branch may live for at most **three working days**. If the card cannot fit, split it.
- Target PR size: **≤ 400 changed lines** excluding lockfiles, generated OpenAPI, migrations
  and snapshots. A larger PR needs a written justification in the PR description.
- One reviewer is enough while the team has one maintainer; the target is two approvals once
  a second maintainer exists (`GOV-12`).
- **Authorship:** while the project has one maintainer, every human commit on `master` is
  authored by that maintainer. Dependabot is the sole accepted non-human author: it may open
  dependency/security-update PRs, which run the full gate and are squash-merged like any other
  change. No other bot or automation may author history (`ADR-023`).

### 2.2 Pull request contract

Every PR must:

1. Link one or more task IDs from this document.
2. State the user-visible outcome and the risk/blast radius.
3. Include verification evidence and screenshots for UI changes.
4. Include migration, rollout and rollback notes where relevant.
5. Keep the PR template checklist honest. An unchecked box needs an explanation.
6. Pass all required checks **after the last push**. Stale approvals are dismissed.
7. Resolve all review conversations before merge.
8. Use **squash merge** only. The squash commit subject must follow Conventional Commits and
   include the task ID, for example:
   `feat(authz): enforce capability matrix (GOV-01)`.
9. Never contain secrets, kubeconfigs, private keys, database dumps, `.env` files or build
   output. Secret scanning and the dependency-review gate are backstops, not permission.
10. Never weaken a check, skip a test, add `continue-on-error`, or edit branch protection to
    get a merge. Fix the cause or revert the change.
11. Only Dependabot may author a PR as a bot. Dependabot PRs carry the `dependencies` label,
    run the full gate, and are never merged through a bypass. Security updates take precedence
    over routine version updates (`ADR-023`).

### 2.3 Required checks (PR gate)

The workflows are **pull-request-only**. The required status contexts are:

| Workflow | Job name (required context) | What it proves |
|---|---|---|
| `PR gate` | `Go: fmt, vet, test, build` | `gofmt`, `go vet`, race-enabled unit tests and a production build |
| `PR gate` | `UI: typecheck, test, build` | Vue/TypeScript correctness, Vitest suite and a production Vite build |
| `PR gate` | `AI: pytest` | FastAPI service health, search, citations and insight tests |
| `PR gate` | `Compose: config` | Production and isolated test Compose definitions resolve |
| `PR gate` | `Images: docker build` | API, UI and AI images build from source with the real Dockerfiles |
| `PR gate` | `Security: dependency review` | No high-severity vulnerability introduced by dependency changes |
| `CodeQL` | `CodeQL (go)` | Go security and quality analysis |
| `CodeQL` | `CodeQL (javascript-typescript)` | UI security and quality analysis |
| `CodeQL` | `CodeQL (python)` | AI service security and quality analysis |

Additional checks are added only when they are deterministic, actionable and fast enough to
keep the median PR under 20 minutes. Flaky or advisory checks are not made required until
they are stable; they run as non-blocking annotations first.

Dependabot opens two classes of PR: weekly grouped version updates (minor/patch per ecosystem)
and grouped security updates. Both use the required contexts above; `Security: dependency
review` is the diff-specific gate. Security-update PRs are triaged before version updates, and
no Dependabot PR may be merged unless every required check is green (`ADR-023`).

### 2.4 Branch protection on `master`

The repository enforces:

- Pull request required before merging.
- Required status checks above, with **strict** mode (branch must be up to date).
- Required conversation resolution.
- Required linear history.
- No force pushes.
- No branch deletion.
- Administrators are subject to the same rules (`enforce_admins: true`).
- Approvals: **0 required while there is one maintainer**; raise to 1 with dismissal of stale
  approvals as soon as a second maintainer exists. Self-approval is never accepted. Dependabot
  cannot approve; once a second maintainer exists, its PRs also require one human approval.
- Squash merge is the only enabled merge method.

> **Bootstrap note:** branch protection is enabled only after a small validation PR has run
> the gate once. This proves the check names and avoids locking `master` behind a check that
> has never executed.

### 2.5 Merge, release and rollback

| Event | Procedure |
|---|---|
| Normal change | Branch → PR → green gate → squash merge → delete branch |
| Release | Tag `master` after the milestone exit gate; promote the tag through staging to production via Argo CD |
| Rollback | Revert the offending PR with a new PR (`fix/revert-...`), or redeploy the previous immutable tag. Never force-push. |
| Hotfix | `fix/...` branch, full PR gate, tag a patch release; no direct push even during an incident |
| Break-glass | Only for an active security incident. Requires written incident ID, a second admin if available, temporary protection change, and a corrective PR within 24 hours. The change is reviewed afterwards. |

### 2.6 Environments

| Environment | Purpose | Data | Verification |
|---|---|---|---|
| Local | Optional developer loop | Synthetic | Host tools or explicit local Docker; never the authority |
| CI | Ephemeral PR verification | Synthetic | GitHub Actions only |
| Staging | Production-like integration, migrations, E2E | Anonymized/synthetic | Automated smoke + manual sign-off |
| Production | Customer system of record | Real | SLOs, alerts, audit, release certification |

Local Docker is **not** required for routine work and is not started by the agent unless the
user explicitly asks. GitHub Actions is the verification environment. The product itself
continues to ship as Docker/Compose and Kubernetes manifests; "no local Docker" is a
development-workflow rule, not a product constraint.

### 2.7 Secrets and configuration

- Secrets live in GitHub Environments, the deployment secret store or Kubernetes Secrets
  managed by External Secrets. Never in Git, images, logs, URLs or PR descriptions.
- `.env.example` contains placeholders only. `.env*` is ignored.
- `kube-config.yaml` and every kubeconfig/key/certificate are ignored and must never be
  committed. The baseline history was rebuilt to guarantee this.
- Rotate any credential that may have been exposed. Removing a file from the latest commit is
  not enough; history must be purged and the credential rotated.
- Every secret has an owner, a rotation interval and a documented consumer.

### 2.8 Commit and PR conventions

Conventional Commits: `feat`, `fix`, `change`, `docs`, `test`, `refactor`, `perf`, `build`,
`ci`, `chore`, `revert`. Scope is the area (`authz`, `ui`, `search`, `ai`, `ops`). Subject is
imperative, lower-case, no trailing period. The body explains *why*, the risk and the
rollback. Breaking changes use `!` and a `BREAKING CHANGE:` footer.

---

## 3. Definition of Ready and Definition of Done

### 3.1 Definition of Ready (DoR)

A card may enter `IN PROGRESS` only when all of the following are true:

- [ ] The problem, affected persona and desired outcome are written in one paragraph.
- [ ] Acceptance criteria are observable and testable, not subjective.
- [ ] The screen blueprint (Section 6) or API contract is identified and linked.
- [ ] The authorization matrix is explicit: global role, project role, capability and
      ownership rules.
- [ ] Data changes are identified: tables, columns, constraints, indexes, migration strategy,
      backfill, retention and rollback.
- [ ] API changes are identified: routes, request/response, errors, pagination, idempotency,
      rate limits, OpenAPI impact.
- [ ] Telemetry is identified: activity/audit event, metric, trace span, log fields.
- [ ] Failure modes are identified: empty, loading, partial, offline, permission denied,
      conflict, timeout, dependency down.
- [ ] Test plan is identified: unit, integration, contract, E2E, accessibility, performance,
      security.
- [ ] Rollout and rollback are identified: feature flag, migration order, compatibility.
- [ ] Dependencies are merged or explicitly scheduled.
- [ ] No unresolved security or privacy question remains.
- [ ] The card is estimated (S/M/L/XL) and fits the three-day branch rule, or is split.

### 3.2 Definition of Done (DoD)

A card is `DONE` only when all of the following are evidenced in the PR and the target
environment:

**Code and correctness**

- [ ] Implementation matches the acceptance criteria and the screen/API contract.
- [ ] Server-side authorization uses the capability matrix; UI gating is additive only.
- [ ] Every mutation emits an append-only activity/audit event with actor, target, action,
      before/after where applicable, request ID and IP/user agent.
- [ ] Every mutation updates the relevant UI through SSE or a documented invalidation path.
- [ ] Errors use the stable error envelope and do not leak internals.
- [ ] Concurrency is handled with optimistic locking or an explicit conflict response.
- [ ] No new external runtime network dependency.

**Tests**

- [ ] Unit tests cover the happy path, boundaries and failure modes.
- [ ] Authorization tests cover every role/capability combination affected.
- [ ] Integration tests cover the real database and event path where the change touches them.
- [ ] Contract tests or OpenAPI validation pass.
- [ ] UI tests cover the primary interaction and keyboard path.
- [ ] Accessibility checks pass (axe and manual keyboard/screen-reader spot check).
- [ ] Coverage does not decrease; critical modules meet the thresholds in `QAT-01`.
- [ ] The PR gate is green on the final commit.

**UI and UX**

- [ ] Layout follows the blueprint: spacing, page frame, density and responsive rules.
- [ ] Loading, empty, error, partial and permission-denied states are designed and tested.
- [ ] Keyboard-only operation works; focus is visible and managed.
- [ ] Screen-reader name/role/state is correct for custom widgets.
- [ ] Contrast meets WCAG 2.2 AA; target size meets 24×24 CSS px.
- [ ] 375 px, 768 px, 1280 px and 1440 px layouts are checked.
- [ ] No horizontal page scroll at supported breakpoints (data tables may scroll inside
      their own region with a visible affordance).
- [ ] Copy follows the content rules (sentence case, active voice, no jargon).

**Operations and documentation**

- [ ] Migration is expand/contract safe, indexed, reversible and tested on a production-like
      dataset.
- [ ] OpenAPI and the generated/typed client are updated.
- [ ] README, runbook, architecture and `TASKS.md` status are updated.
- [ ] Metrics, traces, logs and alerts are in place; dashboards are linked in the PR.
- [ ] Feature flag or rollout plan exists for risky changes.
- [ ] Backup/restore impact is considered; destructive changes have a verified backup.
- [ ] Rollback is rehearsed or explicitly documented with commands.
- [ ] The change is observed in staging for the agreed soak period.

### 3.3 UI-specific quality bar

- No duplicate navigation. The sidebar is the only project navigation surface.
- A page has exactly one `<h1>`; region headings descend in order.
- The primary action is visible without scrolling at 1280 px and reachable by keyboard.
- A table with more than 50 rows paginates or virtualizes; it never renders unbounded.
- A destructive action requires explicit confirmation with the object name and consequence.
- A save action with unsaved changes warns before navigation.
- A stale or failed SSE connection is visible but does not block reading.
- A chart always has a text alternative and a data-table fallback.
- A status is never conveyed by colour alone.

---

## 4. Baseline snapshot and gap register

### 4.1 Baseline snapshot (2026-11-18)

| Area | Baseline reality |
|---|---|
| Repository | Public `Mohamed-Kilany/SPM`; clean orphan baseline on `master`; no leaked kubeconfig in history; `master` protected with 9 required contexts, admins enforced |
| API | Go 1.25 module, Gin, pgx/PostgreSQL, NATS JetStream, SSE, webhooks, Gitea/Argo CD clients |
| UI | Vue 3.5, TypeScript, Pinia, Vue Router, Tailwind 3, Vite 7 |
| AI | Python 3.12 FastAPI; TF-IDF lexical retrieval plus optional local GGUF; extractive answer fallback |
| Data | PostgreSQL 18 with 15 migrations (`000`–`014`) |
| Runtime | Docker Compose production and isolated test stacks; Envoy edge; nginx static UI |
| Tests | 128 Go unit tests, 21 Go integration tests, 13 Vitest tests, 4 pytest tests, 1 Playwright spec |
| Local gate | `scripts/verify.ps1` passed before the CI migration (Go fmt/vet/unit/build, UI typecheck/Vitest/build, AI pytest, Compose config) |
| CI | PR-only gate and CodeQL; `master` protected with strict required checks, admins enforced, linear history and conversation resolution |
| Known drift | README claims a Prometheus endpoint that does not exist; `ARCHITECTURE.md` overstates embeddings/RAG and E2E coverage; `docs/openapi.yaml` is stale; `ENTERPRISE_SPEC.md` checkboxes are all unchecked |

### 4.2 Gap register

Severity: **P0** = release blocker / security or data-loss risk, **P1** = required for
enterprise operation, **P2** = important but not blocking, **P3** = later.

| ID | Area | Gap | Evidence | Impact | Severity | Task |
|---|---|---|---|---|---|---|
| GAP-01 | Authorization | One coarse project-write gate covers all writes; testers can mutate planning/code artifacts | `api/cmd/api/main.go:194`; `api/internal/auth/rbac.go:51` | Privilege escalation inside a project; audit/compliance failure | P0 | GOV-01 |
| GAP-02 | Authorization | Membership/role changes do not invalidate reads; reads trust JWT project IDs first | `rbac.go:228`; `handler_project.go:128-309`; only password change invalidates | Removed member can read until token expiry | P0 | GOV-02 |
| GAP-03 | Code review | Review status is a client-supplied string; no reviewer identity, no self-approval prevention | `handler_delivery.go:266-451`; `pull_requests.review_status` | Anyone with write can "approve" and merge their own change | P0 | GOV-03 |
| GAP-04 | Audit | Admin user create/update/deactivate/reset emits no audit event | `api/internal/auth/admin.go` | No accountability for privileged actions | P0 | GOV-04, GOV-05 |
| GAP-05 | Audit | No global audit log, retention, export or tamper evidence | No audit table/API/UI beyond activity events | Cannot investigate incidents or satisfy auditors | P0 | GOV-04 |
| GAP-06 | Identity | No SSO/OIDC, SCIM, MFA, passkeys or session/device management | Password login only | Weak enterprise identity posture | P1 | GOV-06..GOV-09 |
| GAP-07 | Identity | No lockout, step-up authentication, scoped API tokens or offboarding workflow | `auth/handler.go`, `auth/admin.go` | Credential stuffing and orphaned access risk | P1 | GOV-05, GOV-10, GOV-11 |
| GAP-08 | Webhooks | Signature/replay/idempotency guarantees are not consistently enforced across providers | `api/internal/webhook/*` | Forged or duplicated delivery events | P1 | COD-08, DEL-01 |
| GAP-09 | Supply chain | No SBOM, image signing, provenance or admission verification | No pipeline step | Tampered image risk; audit gap | P1 | OPS-07 |
| GAP-10 | Transport | No TLS/mTLS in the documented production topology; JWT uses one HS256 secret | `docs/ARCHITECTURE.md`; `auth` middleware | Credential/data exposure on the network | P1 | OPS-05, OPS-06 |
| GAP-11 | Data | No optimistic locking; concurrent edits are last-write-wins | No `version` columns in SDLC tables | Silent data loss | P0 | PLN-01 |
| GAP-12 | Data | Constraint/index/cascade coverage is inconsistent; no drift check | `012_sdlc_core.sql`, `013_integrations_reports.sql` | Orphans, slow queries, inconsistent state | P1 | PLN-01, OPS-09 |
| GAP-13 | Data | No retention, erasure, legal hold or data-export workflow | No policy implementation | Privacy/compliance exposure | P1 | OPS-11 |
| GAP-14 | Operations | Backup/restore is scripted but not drilled; air-gapped runbook is missing | `docs/` has no `AIRGAPPED.md`; scripts exist | Unproven RPO/RTO; failed offline install | P0 | OPS-01, OPS-02, OPS-03 |
| GAP-15 | Operations | No Kubernetes/Helm packaging despite Argo CD in the topology | `k8s/argocd/install.yaml` only | Production deployment is Compose-only | P1 | OPS-04 |
| GAP-16 | Operations | No upgrade/rollback compatibility policy or migration rollback drills | No runbook | Risky releases and irreversible migrations | P1 | OPS-09, OPS-12 |
| GAP-17 | Code | Code workspace has no checks/build visibility or logs | `ui/src/components/workspace/WorkspaceCode.vue` | Cannot answer "why can't I merge?" | P1 | COD-03, COD-05 |
| GAP-18 | Code | No review threads/comments, reviewer assignment or CODEOWNERS | PR update is a single status field | Reviews are not auditable or actionable | P1 | GOV-03, COD-04 |
| GAP-19 | Code | No branch-protection synchronization or drift detection | Gitea policy is external | UI and provider disagree | P2 | COD-06 |
| GAP-20 | Code | No blame, code search, large-repo pagination or virtualized tree | `WorkspaceCode.vue` | Poor usability at scale | P2 | COD-02, COD-07 |
| GAP-21 | Search | Saved searches are `localStorage` only | `SearchView.vue:49-76` | Not shared, not durable, not auditable | P1 | SRH-03 |
| GAP-22 | Search | Search omits PRs, builds, deployments, comments, audit and code | `search` handler and `SearchResponse` groups | Incomplete system of record | P1 | SRH-02 |
| GAP-23 | Search | No facets, server-side recent searches, sharing or result highlighting | `SearchView.vue` | Slow discovery; no team workflows | P2 | SRH-03, SRH-04 |
| GAP-24 | Analytics | No line chart or configurable chart builder | `WorkspaceReports.vue`, chart components | Cannot trend metrics over time | P1 | ANL-01 |
| GAP-25 | Analytics | No scheduled reports or async export jobs | Export is synchronous CSV/JSON | No recurring stakeholder reporting | P2 | ANL-02, ANL-03 |
| GAP-26 | Analytics | No metric catalog, timezone policy, drill-down or cross-filtering | Reports are ad hoc | Inconsistent numbers; low trust | P2 | ANL-04..ANL-06 |
| GAP-27 | AI | Retrieval is TF-IDF, not embeddings; docs claim embeddings | `ai/app.py:250`; `docs/ARCHITECTURE.md:67` | Semantic quality is lower than advertised | P1 | AI-01, BASE-04 |
| GAP-28 | AI | No persistent vector index, incremental refresh or permission-aware retrieval | `ai/app.py` is stateless over supplied documents | Stale results; cross-project leak risk | P0 | AI-02, AI-03 |
| GAP-29 | AI | No citation grounding, evaluation set, feedback loop or prompt-injection defense | `ai/app.py` extractive only | Hallucination/injection risk when LLM enabled | P1 | AI-04, AI-06, AI-07 |
| GAP-30 | AI | Copilot appears only on Overview | `WorkspaceOverview.vue`, `AiCopilot.vue` | AI is not available where work happens | P2 | AI-05 |
| GAP-31 | Observability | README claims a Prometheus endpoint; no metrics endpoint exists | `README.md:83`; no `/metrics` route | False operational claim; no SLOs | P0 | BASE-04, OBS-01 |
| GAP-32 | Observability | No OpenTelemetry traces, dashboards, alerts or SLOs | No OTel dependencies/config | Blind in production | P1 | OBS-02..OBS-05 |
| GAP-33 | Observability | Logs lack a consistent correlation/redaction policy | `observability` middleware only | Slow incident response; leak risk | P2 | OBS-03 |
| GAP-34 | UX | Page frames, spacing, typography and state patterns are inconsistent across screens | Views use hand-rolled `max-w`, padding and headers | Cognitive load; slower delivery | P1 | UX-02, UX-05 |
| GAP-35 | UX | No data-table system; lists are bespoke per screen | Board/Tests/Delivery/Admin implement their own rows | Inconsistent sorting/filtering/a11y | P1 | UX-06 |
| GAP-36 | UX | No command palette, global create, shortcut map or recent items | Sidebar navigation only | Slow expert workflows | P2 | UX-09, UX-10 |
| GAP-37 | UX | Accessibility is not gated in CI; axe runs only in one manual E2E spec | `ui/e2e/linked-sdlc.spec.ts` | Regressions reach users | P1 | UX-11, QAT-03 |
| GAP-38 | UX | No visual regression, responsive matrix, density preference or dark/high-contrast mode | No screenshot tests/themes | Visual drift; poor large-screen use | P2 | UX-12, UX-13 |
| GAP-39 | UX | Missing admin audit, system health, notifications/inbox surfaces | Router has only users/profile | No operational cockpit | P2 | UX-14, UI-15, UI-16 |
| GAP-40 | UX | No i18n/timezone/locale foundation | Hard-coded strings/date formatting | Blocks international deployment | P3 | UX-16 |
| GAP-41 | Testing | PR gate runs unit tests but not the 21 integration tests or Playwright E2E | `.github/workflows/pr-gate.yml`; `api/tests/integration` | DB/NATS/Gitea/Argo regressions merge undetected | P0 | QAT-02, QAT-03 |
| GAP-42 | Testing | No coverage thresholds, contract tests, fuzz/property tests or performance budgets | No config | Quality regressions are invisible | P1 | QAT-01, QAT-04, QAT-05 |
| GAP-43 | Testing | One Playwright test covers the whole linked flow; no cross-browser/device matrix | `ui/e2e/linked-sdlc.spec.ts` | Fragile E2E; low coverage | P2 | QAT-03 |
| GAP-44 | Docs | OpenAPI is stale: lists removed routes, misses new ones | `docs/openapi.yaml` | Broken clients/integrations | P0 | BASE-05 |
| GAP-45 | Docs | `ENTERPRISE_SPEC.md` checkboxes are all unchecked despite shipped work; README/ARCHITECTURE overclaim | Docs | Loss of trust; wrong onboarding | P1 | BASE-04 |
| GAP-46 | Performance | No load test, capacity model, query budgets or bundle budgets | `scripts/loadtest.ps1` exists but no gate | Surprises at scale | P1 | QAT-05, QAT-06, OPS-08 |

### 4.3 Documentation truth corrections (must land in `BASE-04`)

| Document | Current claim | Required correction |
|---|---|---|
| `README.md:83` | "Metrics: Prometheus-format endpoint from the API" | Replace with "Metrics: not yet exposed; tracked by OBS-01" until the endpoint exists and is tested |
| `README.md:81` | Air-gapped image mirroring implies a complete runbook | Link `docs/AIRGAPPED.md` only after `OPS-02` creates and verifies it |
| `docs/ARCHITECTURE.md:34,63-72` | "CPU-only embeddings" and RAG model baked into the image | State the actual baseline: TF-IDF + optional GGUF; embeddings are `AI-01` |
| `docs/ARCHITECTURE.md:95-102` | Verification implies integration/E2E run in every phase | State what the PR gate actually runs and link `QAT-02`/`QAT-03` |
| `docs/ENTERPRISE_SPEC.md:71-120` | Every phase unchecked | Mark verified items and link evidence; do not check unverified work |
| `docs/openapi.yaml` | Stale routes and schemas | Regenerate from the router/contract tests in `BASE-05`; fail CI on drift |

---

## 5. UI research synthesis and design system

### 5.1 Method and sources

The UI direction is benchmarked against the three dominant enterprise delivery tools, using
their official documentation rather than screenshots alone. The goal is not to copy a
competitor; it is to adopt proven information architecture and interaction patterns while
keeping SPM's single-navigation and offline-first constraints.

| Benchmark | Sources reviewed | Patterns worth adopting |
|---|---|---|
| **Jira** | Navigation overview; issue view field layout; board/backlog customization; quick filters; command palette | Persistent global sidebar + project context; issue detail with main content left and context fields right; activity tabs; board quick filters and configurable cards; WIP limits |
| **Azure DevOps** | Web portal navigation; breadcrumbs/selectors; work item form; Boards/Kanban; Test Plans | Vertical hubs; project/team selector with breadcrumbs; work-item header with state/owner/area/iteration; Details/History/Links pivots; board columns and swimlanes; test plan/run hierarchy |
| **GitHub** | Repository header/tabs; PR conversation/commits/checks/files; Projects table/board/roadmap; command palette; review flow | Repo header + local tabs; split code tree/file; PR tabs including Checks; review comments and merge box; Table/Board/Roadmap project layouts; `Ctrl/Cmd+K` command palette; high-density tables |
| **Enterprise UX practice** | Dense B2B workflow guidance; data-table research; WCAG 2.2 | Saved views, master-detail, progressive disclosure, inline editing, keyboard shortcuts, density modes, explicit empty/error states |

### 5.2 What each benchmark does well — and what SPM should do differently

| Dimension | Jira | Azure DevOps | GitHub | SPM decision |
|---|---|---|---|---|
| Navigation | Global sidebar, project nav, top bar | Vertical hubs + breadcrumbs + team selector | Repo header tabs | **One persistent sidebar** (global + project sections) + top bar + breadcrumb. No second project tab bar. |
| Work item detail | Main content + right context panel + activity tabs | Header fields + Details/History/Links pivots | Issue/PR split with right sidebar | **Three-zone detail**: main content, context rail, activity timeline. Tabs only for genuinely different content. |
| Board | Configurable cards, quick filters, WIP limits | Columns, swimlanes, WIP limits, card fields | Table/Board/Roadmap layouts | **Board + Table + Roadmap** views over the same saved filter; WIP limits and swimlanes. |
| Code | Basic dev tooling | Repos + pipelines | Best-in-class PR/checks/diff | **Adopt GitHub's PR tab model** and checks visibility; keep Gitea as the provider. |
| Search | JQL, saved filters | Work item queries | Command palette + search | **JQL-style query + facets + saved/shared views + command palette**. |
| Density | Comfortable/compact | Compact enterprise tables | High-density tables | **Three densities** (comfortable/default/compact) persisted per user. |
| A11y | Improving | Strong enterprise requirements | Strong keyboard/command support | **WCAG 2.2 AA as a merge gate**, not a retrofit. |

### 5.3 Design principles

1. **Orientation before action.** Every screen answers: where am I, what is this object, what
   state is it in, and what can I do next — in that order.
2. **One navigation model.** The sidebar owns navigation. Content never grows a second tab
   bar for the same destinations.
3. **Progressive disclosure.** Show the decision-critical fields first; move the long tail to
   a context rail, drawer or "More" menu. Never hide a required field without a reason.
4. **Density with breathing room.** Enterprise screens are information-rich but not cramped:
   a 4 px spacing system, 44–48 px table rows, 12–16 px card padding, 24 px section rhythm.
5. **Stable page frame.** Title, actions, filters and content occupy predictable positions on
   every screen. Users should not re-learn layout per page.
6. **URL is state.** Filters, tabs, selected items and search queries are encoded in the URL
   so views are shareable and back/forward works.
7. **Optimistic where safe, confirmed where destructive.** Inline edits feel instant; delete,
   merge, deploy and role changes require confirmation and audit.
8. **Evidence over decoration.** Status, health and progress always link to the evidence
   (build, test run, deployment, audit event).
9. **Keyboard parity.** Every primary workflow is operable without a mouse; shortcuts are
   discoverable, not secret.
10. **Offline honesty.** When SSE or a dependency is down, say so and keep read paths usable;
    never fake live data.

### 5.4 Design tokens

The existing indigo/slate palette is retained for continuity, but tokens replace ad hoc
Tailwind values in new work. Tokens are defined once in `ui/src/style.css` and the Tailwind
theme; screens consume semantic names, not raw hex values.

**Spacing scale (4 px base):** `0, 2, 4, 6, 8, 12, 16, 20, 24, 32, 40, 48, 64`.
Use 4/8 for icon gaps, 12/16 for control padding, 20/24 for card padding, 24/32 for section
gaps, 40/48 for page-level separation. Never use arbitrary values such as `mt-[13px]`.

**Typography (system stack, no external fonts):**

| Token | Size / line height | Weight | Use |
|---|---|---|---|
| `text-2xs` | 11 / 16 | 600 | Uppercase labels, table meta |
| `text-xs` | 12 / 16 | 400–600 | Helper text, badges, dense tables |
| `text-sm` | 13 / 20 | 400–600 | Body, controls, table cells |
| `text-base` | 14 / 20 | 400–600 | Default body |
| `text-md` | 16 / 24 | 500–600 | Card titles, section headings |
| `text-lg` | 20 / 28 | 600 | Page section headings |
| `text-xl` | 24 / 32 | 600 | Page titles |
| `text-2xl` | 30 / 36 | 600 | Marketing/login only |

**Color semantics:**

| Token | Value | Use |
|---|---|---|
| `canvas` | `#f8fafc` | App background |
| `surface` | `#ffffff` | Cards, panels, tables |
| `surface-subtle` | `#f1f5f9` | Hover, table header, inset panels |
| `border` | `#e2e8f0` | Default 1 px border |
| `border-strong` | `#cbd5e1` | Inputs, selected outlines |
| `text` | `#0f172a` | Primary text |
| `text-muted` | `#475569` | Secondary text |
| `text-faint` | `#64748b` | Meta, placeholders (verify contrast) |
| `primary` | `#4f46e5` | Primary action, active nav |
| `primary-hover` | `#4338ca` | Hover/pressed |
| `success` | `#059669` | Healthy, passed, done |
| `warning` | `#d97706` | At risk, pending |
| `danger` | `#e11d48` | Failed, blocked, destructive |
| `info` | `#0284c7` | Informational, in progress |

Status mapping (never colour-only): `backlog` slate, `todo` sky, `in_progress` indigo,
`blocked` rose, `in_review` amber, `done` emerald, `cancelled` slate with strikethrough.
Every status pill includes a text label and, where useful, an icon.

**Radius:** 6 px controls, 10 px cards, 14 px modals/drawers, 999 px pills.
**Borders:** 1 px `border`; focus ring 2 px `primary` with 2 px offset.
**Elevation:** `sm` for sticky headers, `md` for popovers, `lg` for modals; avoid shadows on
static cards.
**Motion:** 120 ms for hover/focus, 180 ms for popovers, 240 ms for drawers; all motion
respects `prefers-reduced-motion`.
**Z-index:** base 0, sticky 10, dropdown 20, drawer 40, modal 50, toast 60, command palette 70.

### 5.5 Page frame and layout grid

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ TOP BAR 56 px                                                                │
│ [logo] [workspace] [⌘K search]                     [+ Create] [?] [bell] [me] │
├───────────────┬──────────────────────────────────────────────────────────────┤
│ SIDEBAR       │ PROJECT HEADER 64 px                                         │
│ 256 px        │ breadcrumbs · key · name · health · star · actions           │
│ (64 collapsed)├──────────────────────────────────────────────────────────────┤
│               │ PAGE HEADER 72 px                                            │
│ Global        │ title · description · primary/secondary actions              │
│  Home         ├──────────────────────────────────────────────────────────────┤
│  Projects     │ TOOLBAR 48 px                                                │
│  Search       │ filters · view switch · sort · density · columns             │
│  Insights     ├──────────────────────────────────────────────────────────────┤
│  Inbox        │ CONTENT                                                      │
│               │ ┌──────────────────────────────────────┬───────────────────┐ │
│ Project       │ │ main column (8–9 of 12)              │ context rail      │ │
│  Overview     │ │                                      │ 320–360 px        │ │
│  Planning     │ │                                      │ sticky            │ │
│  Board        │ └──────────────────────────────────────┴───────────────────┘ │
│  Test lab     │                                                              │
│  Code         │                                                              │
│  Delivery     │                                                              │
│  Reports      │                                                              │
│  Traceability │                                                              │
│  Settings     │                                                              │
│               │                                                              │
│ Admin         │                                                              │
│ Profile       │                                                              │
└───────────────┴──────────────────────────────────────────────────────────────┘
```

- Content max width: **1440 px**; page gutters **16 px** at <640, **24 px** at 640–1279,
  **32 px** at ≥1280. Data-heavy tables may go full-bleed inside the content region with
  16–24 px inner padding.
- Grid: 12 columns, 24 px gutter, 32 px margin at ≥1280. Dashboard cards use 4/6/8/12 spans.
- Sidebar: 256 px expanded, 64 px collapsed; project section appears only inside a project.
- Context rail: 320 px at 1280–1439, 360 px at ≥1440; becomes a bottom sheet or drawer below
  1280.
- Drawers: 480 px for detail, 640 px for editors, 800 px for complex previews; full-screen
  below 768 px.
- Modals: 480/640/800 px; never wider than `calc(100vw - 32px)`.
- Sticky elements: top bar, page toolbar, table header, save bar. Sticky offsets account for
  the top bar (56 px) and, in project pages, the project header (64 px).

### 5.6 Breakpoints and responsive behaviour

| Breakpoint | Width | Behaviour |
|---|---|---|
| `xs` | < 640 | Single column; sidebar becomes a drawer; context rail becomes a bottom sheet; tables become stacked cards or horizontally scrollable with a visible hint |
| `sm` | 640–767 | Two-column stat grids; forms single column; dialogs full width minus 24 px |
| `md` | 768–1023 | Sidebar drawer or 64 px rail; context rail hidden behind "Details"; tables keep key columns |
| `lg` | 1024–1279 | Sidebar 256 px; main + context rail if space allows; tables show more columns |
| `xl` | 1280–1439 | Full frame; context rail 320 px; 3-column dashboards |
| `2xl` | ≥1440 | Content max 1440 px; context rail 360 px; wider tables; no stretching beyond max |

Rules: never hide the primary action; collapse secondary actions into an overflow menu.
Never require horizontal page scroll. A horizontally scrollable data region must have a
shadow/fade affordance and keyboard scrolling. Test at 200% zoom.

### 5.7 Component inventory (target)

| Family | Components | Notes |
|---|---|---|
| Actions | Button, IconButton, SplitButton, Menu, Toolbar | 4 sizes; loading state keeps width; destructive variant |
| Inputs | TextField, Textarea, Select, Combobox, MultiSelect, DatePicker, Checkbox, Radio, Switch, FileDrop | Label + hint + error; 36/40 px height; `aria-describedby` |
| Navigation | Sidebar, TopBar, Breadcrumbs, Tabs, Pagination, Stepper | Tabs are for content panes, never duplicate sidebar destinations |
| Data | DataTable, VirtualTable, ColumnPicker, FilterBar, SavedView, BulkBar, EmptyState | Sticky header, sortable, selectable, density-aware |
| Feedback | Toast, Banner, InlineAlert, Progress, Skeleton, Spinner, StatusPill, HealthDot | Status always has text/icon |
| Overlay | Modal, Drawer, Popover, Tooltip, CommandPalette, ConfirmDialog | Focus trap, escape, restore focus |
| Content | Card, PageHeader, SectionHeader, DescriptionList, Timeline, Avatar, Badge, Kbd | Consistent 24 px section rhythm |
| Charts | Line, Bar, Donut, Burn, Sparkline, ChartCard, DataTableFallback | Accessible colours + table fallback |
| Domain | WorkItemRow, TestStepEditor, PullRequestTabs, CheckRow, EnvironmentCard, TraceLink | Compose primitives; no one-off styling |

### 5.8 Interaction standards

- **Hover** reveals secondary actions but never hides required information.
- **Selection** is persistent across pages where the action supports it (bulk edit), and the
  bulk bar states the count and the scope ("12 selected on this page" vs "all 240 matching").
- **Inline edit** saves on blur/Enter, shows a saving indicator, and offers undo for 5 seconds.
- **Drag and drop** always has a keyboard alternative (Move up/down, Move to column) and an
  optimistic rollback with a toast.
- **Filters** are chips with a clear "Clear all"; the URL reflects them.
- **Pagination** defaults to 50 rows; options 25/50/100; keyset pagination for large sets.
- **SSE** shows a subtle "Live" indicator; on disconnect, show "Reconnecting…" and keep the
  last data visible; never silently discard edits.
- **Destructive actions** require typing the object name only for irreversible operations
  (delete project, remove environment); otherwise a confirm dialog with consequences.
- **Unsaved changes** guard on route change and browser unload.
- **Toasts** are for transient success/failure; persistent problems use a banner.

### 5.9 Keyboard map

| Shortcut | Action |
|---|---|
| `Ctrl/Cmd + K` | Open command palette (navigation, create, search, actions) |
| `/` | Focus search on list screens |
| `G` then `P` | Go to Projects |
| `G` then `S` | Go to Search |
| `G` then `I` | Go to Inbox |
| `C` | Create (context-aware) |
| `?` | Open shortcut help |
| `Esc` | Close overlay/drawer; clear selection |
| `J` / `K` | Move down/up in a list |
| `Enter` | Open focused item |
| `X` | Toggle selection |
| `E` | Edit focused item (where permitted) |
| `Ctrl/Cmd + Enter` | Submit form/comment |
| `Alt + ↑/↓` | Move item within a board column (keyboard drag alternative) |

Shortcuts never fire while typing in an input, textarea, select or contenteditable. The
shortcut help is generated from a single registry so it cannot drift.

### 5.10 Accessibility standard (WCAG 2.2 AA)

- Semantic landmarks: `header`, `nav`, `main`, `aside`, `footer`; one `<h1>` per page.
- Every control has an accessible name; icon-only buttons use `aria-label` and a tooltip.
- Custom widgets follow the ARIA Authoring Practices: combobox, listbox, menu, tabs, dialog,
  grid. Keyboard behaviour is tested, not assumed.
- Focus is always visible (2 px ring, 2 px offset) and never trapped outside a modal.
- Colour contrast ≥ 4.5:1 for normal text, ≥ 3:1 for large text and UI boundaries.
- Target size ≥ 24×24 CSS px; primary touch targets ≥ 44×44.
- Status is conveyed by text + icon + colour.
- Motion respects `prefers-reduced-motion`; no autoplaying animation.
- Tables have `<caption>`, scope headers, sortable `aria-sort`, and a row count announcement.
- Charts have a text summary and a data-table fallback.
- Errors are announced (`role="alert"`), associated with the field and actionable.
- The app works at 200% zoom and at 320 px width.

### 5.11 Content and microcopy rules

- Sentence case for labels, buttons and headings; no Title Case except product names.
- Verbs for actions ("Create project", "Merge pull request"), nouns for destinations.
- Say what happened and what to do next: "Merge blocked: 1 required check is failing. View
  checks." Not "Error 422".
- Use "work item", "test case", "test run", "pull request", "build", "deployment",
  "environment" consistently. Do not mix "ticket", "issue" and "task" for the same object.
- Dates: relative ("2 hours ago") with an absolute timestamp in the tooltip; absolute dates
  use the user's locale/timezone. Never ambiguous `03/04/2026`.
- Empty states explain why the space is empty and offer the next action, not just "No data".
- Permission-denied states explain which role is required and who can grant it.

### 5.12 Chart standards

- One question per chart; title states the metric and the period.
- Show units, timezone, aggregation and "last updated" in the card footer.
- Use the semantic palette; max 6 categorical colours, then group into "Other".
- Line charts: max 4 series; direct labels where possible; no dual axes.
- Bar charts: sort by value unless time-based; start at zero.
- Donut: only for part-to-whole with ≤ 5 slices; otherwise use a bar chart.
- Burn charts: show scope, completed and ideal; annotate sprint/release boundaries.
- Every chart has a table view toggle and an export action.
- Empty/insufficient data states explain the required data rather than rendering a blank box.

### 5.13 Design QA checklist (per screen)

- [ ] Page frame matches the blueprint: top bar, project header, page header, toolbar, content.
- [ ] Spacing uses the 4 px scale; no arbitrary pixel values.
- [ ] One primary action; secondary actions are grouped or overflowed.
- [ ] Table rows are 44–48 px (default) and the header is sticky.
- [ ] Loading uses skeletons that match the final layout; no layout shift.
- [ ] Empty, error, partial and permission-denied states are present.
- [ ] 375/768/1280/1440 px screenshots attached to the PR.
- [ ] Keyboard-only walkthrough passes; focus order is logical.
- [ ] Screen-reader names/roles/states are correct.
- [ ] Contrast and target size pass.
- [ ] No duplicate navigation or dead-end route.
- [ ] Every status links to its evidence.

---

## 6. Screen-by-screen redesign blueprints

Each blueprint is the contract for the corresponding `UI-*` implementation card. "Current"
describes the baseline file; "target" is the destination. Dimensions are CSS pixels at 100%
zoom. All screens inherit the page frame, tokens and accessibility rules from Section 5.

### 6.1 Global shell and navigation

**Current:** `App.vue`, `AppSidebar.vue`. A 256 px white sidebar holds global links, admin
links and the current project's nine tabs; a mobile top bar appears below `lg`. The project
workspace header repeats breadcrumbs and title. There is no global search, create menu,
command palette, inbox or collapsed rail.

**Jobs:** orient the user, switch projects, reach any destination in one click, create from
anywhere, search globally, see notifications and identity.

**Target layout (≥1280 px):**

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 56  [SPM] [Project switcher ▾]      [⌘K  Search or jump to…]   [+ Create ▾]  │
│                                                         [?] [bell 3] [MK ▾]  │
├───────────────┬──────────────────────────────────────────────────────────────┤
│ 256           │ 64  Projects / Atlas Commerce            ● Healthy   ☆  ⋯    │
│               ├──────────────────────────────────────────────────────────────┤
│ WORKSPACE     │                                                              │
│  Home         │  Page header 72                                              │
│  Projects  ●  │  Title                                            [Primary]  │
│  Search       │  Description                                  [Secondary]   │
│  Insights     │                                                              │
│  Inbox    3   │  Toolbar 48                                                  │
│               │  [filters] [view] [sort] [density] [columns]                 │
│ CURRENT       │                                                              │
│  Atlas ▾      │  Content                                                     │
│  Overview     │                                                              │
│  Planning     │                                                              │
│  Board        │                                                              │
│  Test lab  12 │                                                              │
│  Code         │                                                              │
│  Delivery     │                                                              │
│  Reports      │                                                              │
│  Traceability │                                                              │
│  Settings     │                                                              │
│               │                                                              │
│ ADMIN         │                                                              │
│  Users        │                                                              │
│  Audit        │                                                              │
│  Health       │                                                              │
│               │                                                              │
│ [avatar] Name │                                                              │
│ Role     ⏻    │                                                              │
└───────────────┴──────────────────────────────────────────────────────────────┘
```

**Region specification**

| Region | Size | Rules |
|---|---|---|
| Top bar | 56 px, sticky, `z-10` | Logo 32 px; project switcher max 240 px; command trigger flexes to max 560 px; create button primary; notification bell with unread count; avatar menu |
| Sidebar | 256 px, collapsible to 64 px | Sections: Workspace, Current project (only inside a project), Administration (role-gated), footer identity. 32 px section gaps, 4 px item gaps, 36 px item height, 8 px radius. Active item uses primary tint + 3 px left rail. |
| Project switcher | Popover 360 px | Search field, recent projects (5), "All projects", pinned projects; keyboard navigable |
| Create menu | Menu 240 px | Context-aware: New project (global), work item, test case, PR, environment, release (project); disabled items explain the required role |
| Notification bell | Popover 400 px | Unread first, grouped by day, "Mark all read", "Open inbox" |
| Avatar menu | Menu 240 px | Profile, preferences, keyboard shortcuts, theme/density, sign out |

**Rules**

- The sidebar is the only project navigation. Do not add a project tab bar inside the page.
- Collapse the sidebar to 64 px at 1024–1279 px and when the user chooses "Compact rail";
  persist the preference. Hover/focus expands a flyout label.
- The top bar never scrolls away. Page content starts below it with an 8 px separation.
- The project switcher and breadcrumbs must agree; switching projects preserves the current
  tab where it exists, otherwise goes to Overview.
- The mobile header is 56 px, with menu, title, search icon and avatar; the sidebar opens as a
  full-height drawer with a backdrop and focus trap.

**States:** loading (sidebar skeleton, 3 items), signed-out (no shell), permission-filtered
(hidden admin section), SSE disconnected (small "Reconnecting" chip near the project name),
unread notifications (badge 1–99, then "99+").

**Responsive:** 64 px rail at 1024–1279; drawer below 1024; bottom-safe-area padding on
mobile; no horizontal scroll.

**Accessibility:** `<nav aria-label="Primary">`; `aria-current="page"`; skip link to
`#main`; the drawer traps focus and restores it; the command trigger has `aria-keyshortcuts`.

**Acceptance criteria**

- [ ] Any destination is reachable in ≤ 2 interactions at 1280 px.
- [ ] No duplicate navigation exists on any project screen.
- [ ] The shell persists across route changes without re-rendering the sidebar state.
- [ ] Collapsed rail, mobile drawer and keyboard navigation are covered by tests.
- [ ] The project switcher is searchable and shows recent/pinned projects.

**Tasks:** `UX-04`, `UX-05`, `UX-09`, `UX-10`.

### 6.2 Sign in

**Current:** `ui/src/views/LoginView.vue`. Centred card with username/password, error alert
and a demo hint. No SSO, caps-lock warning, show-password control or environment context.

**Jobs:** authenticate quickly, understand the environment, recover from errors, and reach
SSO when configured.

**Target layout (≥1024 px):**

```text
┌───────────────────────────────────────────┬──────────────────────────────────┐
│ 44% brand panel                           │ 56% form panel                   │
│                                           │                                  │
│  [SPM mark]  SPM Platform                 │      Sign in                     │
│  Enterprise delivery, one system of       │      Use your organization       │
│  record.                                  │      account.                    │
│                                           │                                  │
│  ✓ Requirements → code → deployment       │  [ SSO with your organization ]  │
│  ✓ Tests, evidence and traceability       │  ─────────── or ───────────      │
│  ✓ Offline-first, air-gapped ready        │  Username                        │
│                                           │  [________________________]      │
│  ● Production · v2.4.0                    │  Password                  Show  │
│                                           │  [________________________]      │
│  No external telemetry.                   │  [ ] Keep me signed in           │
│                                           │  [        Sign in        ]       │
│                                           │  Forgot password? · Help         │
└───────────────────────────────────────────┴──────────────────────────────────┘
```

**Specification**

- Brand panel hidden below 1024 px; form centred at max 400 px with 32 px padding.
- Form fields 40 px high, 16 px gap; primary button full width, 44 px.
- SSO button appears only when OIDC is configured; it is the first action.
- Show/hide password button with `aria-pressed`; caps-lock warning appears on keyup.
- Error banner above the form, `role="alert"`, not below the button.
- Demo credentials are never shown in production; in development they appear in a muted
  "Local demo" note with a copy action.
- Environment badge uses the deployment environment, not a hard-coded string.
- Footer: version, privacy/security links, locale/timezone hint.

**States:** idle, submitting (button spinner, inputs disabled), invalid credentials, locked
account (with unlock guidance), SSO unavailable (fallback explanation), offline (service
unreachable, with retry), expired session (informational banner).

**Responsive:** single column below 1024 px, 24 px page padding, no brand panel; 375 px
keyboard does not cover the submit button.

**Accessibility:** `<main>` with one `<h1>`; labels always visible; `autocomplete="username"`
and `current-password`; error linked with `aria-describedby`; focus moves to the error or the
first invalid field; no time-limited content.

**Acceptance criteria**

- [ ] Password managers autofill correctly.
- [ ] Enter submits; the button has a visible loading state and cannot double-submit.
- [ ] Invalid credentials do not reveal whether the username exists.
- [ ] The SSO path is hidden when unconfigured and fully keyboard operable when configured.
- [ ] The page passes axe with zero serious/critical violations.

**Tasks:** `UI-01`, `GOV-06`, `GOV-08`.

### 6.3 Projects portfolio

**Current:** `ui/src/views/ProjectListView.vue`. Three stat cards and a responsive card grid
with monogram, description, counts, open bugs, completion and repository name. No search,
sort, saved views, table view, pagination, archive or bulk actions.

**Jobs:** find the right project, assess portfolio health, create a project, and manage
archived work.

**Target layout:**

```text
Page header 72
Projects                                             [New project]
Manage delivery across every workspace.              12 active · 2 archived

Toolbar 48
[Search projects…] [All ▾] [Health ▾] [Owner ▾]   [Table|Cards] [Density] [Columns]

Saved views:  All  ·  Active  ·  At risk  ·  My projects  ·  Archived

┌──────────────────────────────────────────────────────────────────────────────┐
│ Project ↑        Key    Health   Work   Bugs   Coverage   Release   Activity │
├──────────────────────────────────────────────────────────────────────────────┤
│ ● Atlas Commerce ATL    ● On track 128    3     82%       v2.4    2h ago   │
│ ● Payments Core  PAY    ▲ At risk   64     9     61%       v1.8    1d ago   │
│ ○ Legacy Portal  LEG    ○ Paused     0     0      —        —       3w ago   │
└──────────────────────────────────────────────────────────────────────────────┘
50 per page                                                    1–12 of 14  ‹ ›
```

**Specification**

- Default view is a **table** at ≥1024 px and **cards** below; the user's choice persists.
- Table columns: Project (name + description + star), Key, Health, Work items, Open bugs, Test
  coverage, Next release, Members, Last activity, overflow. Columns are configurable; Project
  and Key are fixed.
- Health is derived (release readiness + blocked items + failing checks) and links to the
  evidence; never a free-text field.
- Row click opens Overview; `Enter` opens; `Space` selects; the overflow menu has Open,
  Copy key, Pin, Archive (permission-gated).
- Bulk selection shows a 48 px bulk bar with Archive, Export and Clear.
- Saved views are URL-encoded and shareable; "At risk" is a system view, not user-editable.
- Cards (below 1024 px or card view): 280 px min, 20 px padding, 16 px gaps; show monogram,
  name, description (2 lines), health, 3 counts, completion bar and last activity. Max 2
  actions on the card; the rest in overflow.
- Creation is a 640 px modal with name, key suggestion, description, template and visibility;
  validation inline; on success route to the new project's Overview with a success toast.
- Empty state: illustration-free, explains project creation and required role, with a
  "Create project" primary action and a "Browse demo data" secondary (development only).

**States:** loading skeleton table/cards, empty portfolio, no search matches (clear filters),
error with retry, permission-denied create, archived view read-only banner.

**Responsive:** below 768 px, cards stack; table switches to 2-line rows with Project, Health
and Activity only; filters collapse into a "Filters" button with a count.

**Accessibility:** table caption, `aria-sort`, row selection checkboxes with labels, sticky
header, keyboard row navigation; cards are links with a single accessible name; bulk bar is
announced.

**Acceptance criteria**

- [ ] A user can find a project by name/key in ≤ 2 interactions.
- [ ] Sorting, filtering, saved views and pagination survive reload via the URL.
- [ ] Health always opens the evidence that produced it.
- [ ] The table virtualizes or paginates; 500 projects do not freeze the page.
- [ ] Create/edit/archive respect global and project roles.

**Tasks:** `UI-02`, `UX-06`, `PLN-04`.

### 6.4 Project Overview

**Current:** `ui/src/components/workspace/WorkspaceOverview.vue`. A header with counts, four
summary cards, "Connected delivery chain", recent activity, environment health and release
readiness; the AI copilot is on this screen only.

**Jobs:** answer "is this project healthy, what changed, what is at risk, and what should I do
next?" in under 30 seconds.

**Target layout (≥1280 px, 12-column grid):**

```text
Page header 72
Atlas Commerce  ·  ATL                             [Open board] [New work item]
Release v2.4 · 6 days to go · ● On track

KPI strip 4 × 112 px, 16 px gaps
[Work items 128 / 64 done] [Open bugs 3] [Test coverage 82%] [Release readiness 78%]

┌───────────────────────────────────────────────┬──────────────────────────────┐
│ MAIN 8 columns                                │ CONTEXT 4 columns            │
│                                               │                              │
│ Delivery chain (card, 24 px padding)          │ Environment health           │
│ PRD → Epic → Feature → Work → Test → Build →  │ ● Production  v2.3.1  2h     │
│ Deploy → Health        [7 stages, clickable]  │ ● Staging     v2.4.0  18m    │
│                                               │ ● Development v2.4.1  5m     │
│ Release readiness (card)                      │                              │
│ [progress] 78%  · 4 blockers  · 2 failing     │ Release blockers (3)         │
│ checks  · 1 unapproved PR                     │ · PAY-142 failing check      │
│                                               │ · ATL-88 missing approval    │
│ Recent activity (timeline, 8 items)           │ · ATL-91 test failed         │
│ ● Ana merged PR #482          12m             │                              │
│ ● Build #129 passed           18m             │ Upcoming milestones          │
│ ● Deployment prod succeeded   2h              │ v2.4  25 Nov                 │
│                                               │ v2.5  09 Dec                 │
│                                               │                              │
│                                               │ AI insights                  │
│                                               │ [3 suggestions →]            │
└───────────────────────────────────────────────┴──────────────────────────────┘
```

**Specification**

- KPI strip: four equal cards, 112 px high, 20 px padding; value 24/32 semibold; label 11 px
  uppercase; delta/context 12 px; each card links to the filtered list that produced it.
- Delivery chain: horizontal 7-stage stepper at ≥1024 px, vertical timeline below; each stage
  shows count, health dot and a link; the whole stage is clickable, not only the count.
- Release readiness: progress bar + four evidence rows (blockers, failing checks, unapproved
  PRs, failing tests); rows link to the exact filtered view. Do not show a percentage without
  the evidence.
- Activity: 8 items, relative time with absolute tooltip, actor avatar, object link; "View all"
  opens the audit/activity drawer; new items animate in only when the user is at the top.
- Environment health: one row per environment with name, version, health, last deployment and
  a link to Delivery; failures are rose with an icon and a short reason.
- Context rail is sticky with 24 px gaps; at <1280 px it moves below the main column; at
  <768 px it becomes a "Details" tab.
- AI insights: max 3 cards with "why" evidence and accept/dismiss; if the AI service is down,
  show a muted "AI insights unavailable" with a retry, never an empty card.

**States:** loading (KPI skeletons + card skeletons), empty project (onboarding checklist:
create PRD, create work item, connect repo, create environment), partial data (show known
values, mark unknown as "—"), SSE disconnected, error.

**Accessibility:** KPI values announced as text; the stepper is an ordered list with links;
progress bars have `role="progressbar"` and an accessible value; activity uses a `<ol>`.

**Acceptance criteria**

- [ ] The screen answers health, change, risk and next action without horizontal scrolling.
- [ ] Every number links to its evidence.
- [ ] The empty state leads to the first useful action.
- [ ] SSE updates do not reorder the page under the user's pointer.
- [ ] The AI panel degrades honestly when the service is unavailable.

**Tasks:** `UI-03`, `ANL-05`, `OBS-01`, `AI-05`.

### 6.5 Planning

**Current:** `ui/src/components/workspace/WorkspacePlanning.vue`. A PRD/epic/feature hierarchy
with a detail editor, collapsible epics, inline forms and a selected-item panel. No table or
timeline view, no context rail, no bulk actions or pagination.

**Jobs:** shape the product tree (PRD → Epic → Feature → Work item), edit details, understand
status/coverage, and move work into a sprint/release.

**Target layout (≥1280 px):**

```text
Page header 72
Planning                                           [+ New ▾]  [Import]
PRD → Epic → Feature → Work item

Toolbar 48
[Search…] [Type ▾] [Status ▾] [Owner ▾]   [Tree|Table|Timeline]  [Expand all]

┌──────────────┬───────────────────────────────────────────┬───────────────────┐
│ HIERARCHY    │ DETAIL                                     │ CONTEXT           │
│ 280 px       │ flexible, min 560                          │ 320 px            │
│              │                                            │                   │
│ ▾ PRD-1      │ Checkout redesign            [Edit] [⋯]   │ Status   In review│
│   ▾ Epic A   │ Feature · FEAT-12 · Owner Ana              │ Owner    Ana      │
│     Feature  │                                            │ Priority High     │
│       Work   │ Description                                │ Sprint   Sprint 8 │
│   ▸ Epic B   │ …                                          │ Release  v2.4     │
│              │                                            │                   │
│ [+ PRD]      │ Acceptance criteria                        │ Linked            │
│              │ [ ] Guest checkout                         │ · 3 work items    │
│              │ [x] Saved cards                            │ · 2 tests         │
│              │                                            │ · 1 PR            │
│              │ Activity                                   │                   │
│              │ ● Ana changed status  2h                   │ Coverage 82%      │
└──────────────┴───────────────────────────────────────────┴───────────────────┘
```

**Specification**

- Three panes: hierarchy 280 px fixed, detail fluid min 560 px, context 320/360 px. Below
  1280 px the context rail becomes a "Details" drawer; below 1024 px the hierarchy becomes a
  drawer and the detail takes the full width.
- Tree rows: 36 px, indent 16 px per level, chevron 24 px, type icon, title, status dot,
  count. Keyboard: ↑/↓ move, →/← expand/collapse, Enter open, `E` edit.
- Toolbar: search is debounced 250 ms; filters are chips; view switch uses a segmented control;
  "Expand all" is persisted per user.
- Detail header: type, key, title (inline editable), status pill, owner avatar, primary action
  (Edit or Save), overflow (Duplicate, Move, Archive, Delete). Title is a single-line input in
  edit mode, not a modal.
- Description is rich text or Markdown with a preview; autosave every 2 seconds after change,
  with an explicit "Saved" state. Do not autosave empty required fields.
- Acceptance criteria: ordered checklist with add/reorder/delete; each item can link to a test
  case; completed items are struck through but still readable.
- Context rail: status, owner, priority, sprint, release, labels, linked artifacts, coverage,
  watchers. Each field is inline-editable with permission checks; optimistic update with
  rollback and toast on failure.
- Activity: last 10 events with actor/time; "View all" opens the audit drawer filtered to the
  object.
- Table view: columns Key, Title, Type, Status, Owner, Priority, Sprint, Release, Updated;
  grouping by epic/feature; bulk status/owner/sprint changes.
- Timeline view: features/epics on a horizontal time axis by sprint/release; no drag on the
  first release (read-only planning), drag is `PLN-03`.

**States:** no PRDs (empty state with "Create your first PRD"), no selection (detail pane
explains how to select), unsaved changes guard, conflict (409 with "Reload / Overwrite"
choice), permission-denied edit, AI suggestion loading.

**Accessibility:** tree uses `role="tree"`/`treeitem` with `aria-expanded`/`aria-level`;
detail headings descend; inline edit announces save state; checklist items have labels.

**Acceptance criteria**

- [ ] Creating a PRD → epic → feature → work item works without leaving the screen.
- [ ] The tree, table and timeline reflect the same filter and URL state.
- [ ] Context fields update optimistically and roll back visibly on failure.
- [ ] Keyboard-only tree navigation and editing pass.
- [ ] 500 nodes remain usable (virtualized tree or paginated children).

**Tasks:** `UI-04`, `PLN-01`, `PLN-02`, `PLN-03`, `PLN-05`.

### 6.6 Board

**Current:** `ui/src/components/workspace/WorkspaceBoard.vue`. A Kanban board with search,
type/feature/sprint filters, columns and cards. No saved views, WIP limits, swimlanes,
bulk selection, keyboard drag or virtualization; the legacy `ProjectBoardView.vue` still
exists at `/projects/:id/legacy`.

**Jobs:** move work through the flow, spot bottlenecks, and change status/owner/sprint fast.

**Target layout:**

```text
Page header 72
Board                                      [+ New work item]  [Configure]
Sprint 8 · 42 items · 6 days left          [Live ●]

Toolbar 48
[Search…] [Assignee ▾] [Type ▾] [Priority ▾] [More ▾]  [Board|Table|Roadmap]
Quick filters:  My items  ·  Blocked  ·  Bugs  ·  Unassigned
Saved view: Sprint 8 (shared) ▾

Metrics strip 64
[Throughput 18/wk] [Cycle time 3.2d] [WIP 14/20] [Blocked 3]

┌─────────────┬─────────────┬─────────────┬─────────────┬─────────────┐
│ TO DO 5/6   │ IN PROGRESS │ REVIEW 3/4  │ DONE        │             │
│ ─────────   │ 8/8 ⚠       │ ─────────   │ ─────────   │             │
│ ┌─────────┐ │ ┌─────────┐ │ ┌─────────┐ │ ┌─────────┐ │             │
│ │ATL-142  │ │ │ATL-138  │ │ │ATL-131  │ │ │ATL-120  │ │             │
│ │Title    │ │ │Title    │ │ │Title    │ │ │Title    │ │             │
│ │● High   │ │ │● High   │ │ │● Med    │ │ │✓       │ │             │
│ │@Ana  3 │ │ │@Sam  5  │ │ │@Lee  2  │ │ │@Ana  1 │ │             │
│ └─────────┘ │ └─────────┘ │ └─────────┘ │ └─────────┘ │             │
│ + Add       │ + Add       │ + Add       │             │             │
└─────────────┴─────────────┴─────────────┴─────────────┴─────────────┘
```

**Specification**

- Column width: 280 px min, 320 px preferred; gap 16 px; columns scroll horizontally inside
  the board region, with a fade affordance. Column headers are sticky within the board.
- Column header: name, count/WIP limit, overflow (limit, sort, collapse). Over-limit columns
  use an amber header and a warning icon, not a red block.
- Card: 12 px padding, 10 px radius, 1 px border; line 1 key + type icon + priority; line 2
  title (2 lines max); line 3 assignee avatar + points + due date + blocked icon; hover shows
  a drag handle and overflow. Card click opens a 480 px detail drawer; `Enter` opens.
- Metrics strip: four compact metrics with tooltips defining the calculation and period.
- Quick filters are toggle chips; they combine with facets and are reflected in the URL.
- Saved views: personal/shared, default view per user, "Save as" and "Manage views".
- Drag: HTML5 pointer drag with a clear drop indicator; optimistic status update; on failure
  restore the card and show a retry toast. Keyboard: focus card, `Alt+↑/↓` moves within a
  column, `Alt+←/→` moves across columns, `Enter` opens, `Space` selects.
- Bulk: `X` toggles selection; a 48 px bulk bar offers Status, Assignee, Sprint, Priority,
  Delete; scope is explicit.
- Swimlanes: group by assignee, priority, epic or none; lane headers are 40 px sticky.
- The legacy board route is removed or redirected to the workspace board after parity; no
  duplicate board implementation remains.

**States:** empty sprint (create or pull from backlog), all columns empty, filter no-match,
over-limit, SSE disconnected (banner "Live updates paused"), conflict on drag, permission
denied for drag (read-only cursor + explanation).

**Responsive:** below 1024 px, columns become a horizontal snap carousel one column wide;
below 768 px, the board becomes a grouped list with a status segmented control; the detail
drawer is full-screen.

**Accessibility:** board is a `list` of columns, each a `list` of cards; drag has a keyboard
alternative; the live region announces moves and errors; column WIP status is text, not colour
only.

**Acceptance criteria**

- [ ] A card can be created, moved, edited and completed without a mouse.
- [ ] WIP limits warn but do not silently block; the policy is explained.
- [ ] Saved views and filters survive reload and are shareable.
- [ ] 2,000 cards across 6 columns remain responsive (virtualized columns).
- [ ] The legacy board route no longer renders a second implementation.

**Tasks:** `UI-05`, `BRD-01`, `BRD-02`, `BRD-03`, `PLN-01`.

### 6.7 Test lab

**Current:** `ui/src/components/workspace/WorkspaceTests.vue`. A two-column layout with test
cases, runs, coverage by feature, a run detail panel and step results; large forms are inline.

**Jobs:** author test cases, plan and execute runs, record evidence, and create defects
without losing context.

**Target layout (≥1280 px):**

```text
Page header 72
Test lab                                   [+ New test case] [Start run]
Quality evidence for Atlas Commerce · 82% coverage

Sub-navigation 44
[Test cases 128] [Test runs 24] [Plans 6] [Coverage]

┌───────────────────────────────┬──────────────────────────────────────────────┐
│ LIST 360 px                   │ DETAIL / RUNNER                              │
│ [Search…] [Feature ▾] [Status]│                                              │
│ ┌───────────────────────────┐ │ TC-142  Guest checkout with saved card       │
│ │ TC-142 Guest checkout…    │ │ ● Passed · Feature: Checkout · Owner Ana    │
│ │ ● Passed  P1  Checkout    │ │                                              │
│ ├───────────────────────────┤ │ Tabs: Details · Steps · Runs · Evidence      │
│ │ TC-143 Expired card…      │ │                                              │
│ │ ○ Not run P1  Checkout    │ │ Steps                                        │
│ └───────────────────────────┘ │ #  Action                    Expected  Result│
│                               │ 1  Open checkout              Page     ✓     │
│ 50 per page                   │ 2  Enter saved card           Token    ✓     │
│                               │ 3  Confirm payment            Success  ✗     │
│                               │ [+ Add step]                                 │
│                               │                                              │
│                               │ [Save] [Run this case]                       │
└───────────────────────────────┴──────────────────────────────────────────────┘
```

**Specification**

- Sub-navigation is local to the Test lab, not a duplicate of the sidebar. It uses tabs with
  counts and preserves filters per tab.
- Test cases list: 360 px, 64 px rows, search/filter, status/priority/feature, 50 per page;
  selected row uses a primary tint and a 3 px left rail.
- Test case detail: header with key, title, status, priority, feature, owner, last result;
  tabs for Details, Steps, Runs, Evidence. Steps are a table with order, action, expected,
  data, result; drag reorder with keyboard alternatives.
- Test run execution mode: a focused full-width view with a progress header ("3 of 12"),
  current step card, Pass/Fail/Blocked/Skip buttons, actual result, evidence upload, notes,
  previous/next, and a finish summary. Keyboard: `P` pass, `F` fail, `B` blocked, `S` skip,
  `←/→` navigate; confirm before finishing with incomplete steps.
- Creating a bug from a failed step opens a 640 px modal prefilled with project, feature,
  test case, run, step, environment, build/deployment and evidence; the link is created in
  both directions.
- Coverage tab: matrix feature × test type with coverage %, gaps, and a link to the gap
  register; export.
- Plans tab: plan → suite → case hierarchy; add cases by filter or CSV; assign testers;
  schedule a run.
- The run detail is a page/drawer, not a nested card inside a card.

**States:** no cases (create/import), no runs, run in progress (resume), run completed
(summary), failed step with evidence, conflict (another tester updated the case), offline.

**Responsive:** below 1280 px, list becomes a 320 px drawer; below 768 px, list and detail are
separate routes with a back link; the runner is full-screen.

**Accessibility:** step result buttons are a radio group with labels; the progress region is
`aria-live="polite"`; evidence upload has a text alternative; tables are keyboard navigable.

**Acceptance criteria**

- [ ] A tester can complete a run and file a linked bug without leaving the runner.
- [ ] Step results, actual results and evidence persist and are auditable.
- [ ] Coverage gaps link to the exact missing cases.
- [ ] Keyboard-only run execution works.
- [ ] Large suites (1,000 cases) paginate/virtualize.

**Tasks:** `UI-06`, `TST-01`..`TST-05`, `ANL-05`.

### 6.8 Code

**Current:** `ui/src/components/workspace/WorkspaceCode.vue`. A large single component with
repository list/tree, file blob, commits, branches, pull requests and changed files. Checks,
build logs, review threads and merge-policy evidence are not visible.

**Jobs:** browse code, understand a change, review it, see why it is blocked, and merge safely.

**Target layout — repository landing (≥1280 px):**

```text
Repository header 56
[spm/atlas-commerce ▾]  [main ▾]  [Clone ▾]                    [New PR]
Local tabs 44
[Code] [Pull requests 12] [Commits] [Branches] [Checks]

┌───────────────────┬──────────────────────────────────────────────────────────┐
│ TREE 280          │ FILE                                                     │
│ [Search files…]   │ src/checkout/PaymentService.ts              [Raw] [⋯]    │
│ ▾ src             │ 1  import ...                                            │
│   ▾ checkout      │ 2  export function pay(...) {                            │
│     Payment.ts    │ …                                                        │
│   ▸ tests         │                                                          │
│ ▸ README.md       │                                                          │
│                   │                                                          │
│ Branch: main      │ Last commit: ATL-142 · Add saved cards · 2h ago          │
└───────────────────┴──────────────────────────────────────────────────────────┘
```

**Target layout — pull request detail:**

```text
PR header 72
#482  Add saved cards to guest checkout          ● Open  [Merge ▾]
ATL-142 · ana → main · 3 commits · 2 files · +128 −42

Tabs 44
[Conversation 6] [Commits 3] [Checks 4] [Files changed 2]

┌───────────────────────────────────────────────────────┬──────────────────────┐
│ MAIN                                                  │ MERGE BOX 320        │
│ Description                                           │ ● 3 of 4 checks pass │
│ Review threads (resolved/unresolved)                  │ ✓ Build                 │
│ Comment composer                                      │ ✗ Tests (view logs)     │
│                                                       │ ✓ Security              │
│                                                       │ ✓ Review (1)            │
│                                                       │ ────────────────────    │
│                                                       │ [Merge pull request]    │
│                                                       │ Squash · Delete branch  │
└───────────────────────────────────────────────────────┴──────────────────────┘
```

**Specification**

- Repository header: repo selector (searchable), branch selector (searchable, recent,
  protected badge), clone menu (HTTPS/SSH, copy), new PR. Uses Gitea as the provider; deep
  links open the exact Gitea page in a new tab.
- Local tabs: Code, Pull requests, Commits, Branches, Checks. These are local to the Code
  workspace, not project navigation.
- Code browser: tree 280 px, file fluid, optional outline 240 px at ≥1440. Syntax highlighting
  must be offline (no CDN). Large files (>1 MB) show a guarded viewer with "Open raw" and
  "Download". Binary files show metadata, not garbage.
- File header: path breadcrumb, raw/download/copy, blame toggle, history. Blame is a gutter
  with author/time tooltip; line selection supports permalink.
- PR list: table with number, title, author, status, checks, review, updated, size; filters
  (open/draft/merged/closed, author, reviewer, label), search, 50 per page.
- PR detail: tabs Conversation, Commits, Checks, Files changed. Conversation holds description,
  timeline, review threads and composer. Commits show SHA, message, author, date, verified
  badge. Checks show workflow/job/step, status, duration, logs link, required badge. Files
  changed shows a unified/split toggle, per-file collapse, viewed checkbox, line comments.
- Merge box: policy checklist (approvals, checks, conflicts, branch up to date), merge method
  (merge/squash/rebase per project policy), delete-branch toggle, merge button disabled with a
  reason. Self-approval is impossible; the current user is labeled "(you)".
- Reviews: approve, request changes, comment; comments are threaded, resolvable, and tied to
  a commit SHA. Stale approvals are marked after a new push.
- Checks failure: inline summary, expandable job list, log link, rerun guidance (provider
  action if supported, otherwise a documented external link). Never fake a rerun.

**States:** no repositories (connect/provision), empty repo, no PRs, detached/unknown branch,
file too large, binary file, checks running/failed/queued, merge blocked, conflict, permission
denied, provider unreachable (read-only cached view if available).

**Responsive:** below 1280 px, tree becomes a drawer; below 1024 px, PR merge box moves below
the main column; below 768 px, tabs scroll horizontally and the diff uses unified view by
default.

**Accessibility:** diff table has row/column semantics; line comments are reachable by
keyboard; status/check icons have text; the merge button's disabled reason is programmatically
associated; code blocks have a copy button with an accessible name.

**Acceptance criteria**

- [ ] A reviewer can see every required check and its failure reason before merging.
- [ ] Review comments are threaded, attributable and resolvable.
- [ ] Merge is blocked when policy fails and the reason is explicit.
- [ ] The code browser works offline with no CDN requests.
- [ ] 10,000-file repositories remain navigable (lazy tree, pagination).

**Tasks:** `UI-07`, `COD-01`..`COD-07`, `GOV-03`.

### 6.9 Delivery

**Current:** `ui/src/components/workspace/WorkspaceDelivery.vue`. Environment pipeline, source
control summary, pull requests, Gitea Actions builds, releases and Argo CD deployments as
stacked cards/tables; rollback and approvals are partial.

**Jobs:** answer "what is deployed where, is it healthy, what changed, and can I promote or
roll back safely?"

**Target layout:**

```text
Page header 72
Delivery                                         [New release] [Configure]
Automated evidence from Gitea Actions and Argo CD          ● Live

Environment strip 4 × 148 px, 16 px gap
┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ Development  │ │ Staging      │ │ Production   │ │ DR           │
│ v2.4.1       │ │ v2.4.0       │ │ v2.3.1       │ │ v2.3.1       │
│ ● Healthy    │ │ ● Healthy    │ │ ▲ Degraded   │ │ ○ Unknown    │
│ 5m ago       │ │ 18m ago      │ │ 2h ago       │ │ 1d ago       │
│ [Details]    │ │ [Promote]    │ │ [Rollback]   │ │ [Details]    │
└──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘

Tabs 44
[Deployments] [Builds] [Releases] [Approvals]

Toolbar 48
[Search…] [Environment ▾] [Status ▾] [Date ▾]        [Table|Timeline]

Table
│ Status │ Version │ Environment │ Commit │ Started │ Duration │ Actor │ ⋯ │
│ ✓      │ v2.4.0  │ Staging     │ a1b2c3 │ 18m     │ 2m 14s   │ Ana   │   │
│ ✗      │ v2.3.2  │ Production  │ d4e5f6 │ 2h      │ 1m 02s   │ Sam   │   │
```

**Specification**

- Environment cards: 148 px high, 20 px padding; name, deployed version, health, last deploy,
  one primary action (Promote/Rollback/Details) and an overflow. Health links to the Argo app
  and health checks. Unknown is explicit, never green by default.
- Tabs are content views, not navigation. Each preserves filters in the URL.
- Deployments table: status, version, environment, commit (short SHA + copy), started,
  duration, actor, strategy, health, links. Row click opens a 640 px drawer with timeline,
  logs link, manifest diff, approvals and rollback.
- Builds table: workflow, run number, branch, commit, status, duration, triggered by, checks.
  A build detail drawer shows jobs/steps, logs link, artifacts and the PR/work item link.
- Releases: version, scope, readiness, freeze window, approvals, deployed environments;
  "New release" opens a 640 px wizard (scope, version, notes, environments, approvals).
- Approvals: pending/approved/rejected with actor, role, timestamp, comment; separation of
  duties (requester cannot approve their own deployment when policy requires).
- Rollback: confirm dialog states target version, environment, impact and audit; requires the
  deployment capability; runs as an Argo CD operation with progress and a final state.
- Promotion: shows the diff between current and target version, policy checks and approvals.
- Live updates: SSE updates rows in place; a "Live" chip shows connected/reconnecting; new
  events at the top are announced, not auto-scrolled.

**States:** no environments, no builds/deployments (connect provider), provider down,
deployment in progress, failed with logs, rollback in progress, approval required, permission
denied, stale data.

**Responsive:** environment cards 2×2 at 768–1279, 1 column below 768; tables switch to
stacked rows on mobile; drawers full-screen.

**Accessibility:** status and health are text + icon; the timeline is an ordered list; the
rollback dialog requires explicit confirmation and announces the result; tables have captions
and sortable headers.

**Acceptance criteria**

- [ ] Every environment shows version, health, last deploy and evidence.
- [ ] A failed deployment exposes the failing job/log link and the commit.
- [ ] Rollback and promotion require the correct capability and are audited.
- [ ] Approvals cannot be self-approved when policy forbids it.
- [ ] SSE updates do not duplicate rows or lose scroll position.

**Tasks:** `UI-08`, `DEL-01`..`DEL-04`, `GOV-03`.

### 6.10 Reports and analytics

**Current:** `ui/src/components/workspace/WorkspaceReports.vue`. Work distribution (donut/bar),
burn-up/down, release readiness, test trend, sprint execution and feature coverage. No line
chart, report library, chart builder, scheduling or drill-down.

**Jobs:** answer a metric question, compare periods, save/share a report, schedule delivery,
and export evidence.

**Target layout (≥1280 px):**

```text
Page header 72
Reports                                      [New report] [Export]
Project Atlas Commerce · Sprint 8 · 01–30 Nov 2026

┌──────────────┬───────────────────────────────────────────────────────────────┐
│ REPORT LIBRARY│ Filter bar 48                                                │
│ 240 px       │ [Project ▾] [Sprint ▾] [Release ▾] [Date range ▾] [Apply]     │
│              │                                                               │
│ ★ Overview   │ ┌───────────────────────────┐ ┌───────────────────────────┐   │
│   Delivery   │ │ Throughput                │ │ Cycle time                │   │
│   Quality    │ │ [line chart]              │ │ [line chart]              │   │
│   Tests      │ │ 18 items/wk  ▲ 12%        │ │ 3.2 days  ▼ 0.4           │   │
│   Releases   │ │ [Table view] [⋯]          │ │ [Table view] [⋯]          │   │
│              │ └───────────────────────────┘ └───────────────────────────┘   │
│ Saved        │ ┌───────────────────────────┐ ┌───────────────────────────┐   │
│  Sprint 8    │ │ Work distribution         │ │ Release readiness         │   │
│  Quality Q4  │ │ [donut + legend]          │ │ [progress + evidence]     │   │
│              │ └───────────────────────────┘ └───────────────────────────┘   │
│ + New view   │                                                               │
└──────────────┴───────────────────────────────────────────────────────────────┘
```

**Specification**

- Report library 240 px: system reports (Overview, Delivery, Quality, Tests, Releases), saved
  reports, shared reports, "New view". Searchable at > 8 items.
- Filter bar: project, sprint, release, date range, environment, team; "Apply" is explicit for
  expensive queries; the active filter set is shown as chips and in the URL.
- Chart cards: 2 columns at ≥1280 px, 1 below; 24 px padding; title, metric value + delta,
  chart, footer with period/aggregation/last updated, table toggle and overflow (Edit,
  Duplicate, Export, Schedule, Delete).
- Chart builder drawer (640 px): metric, dimension, aggregation, filters, chart type, compare
  period, sort, limit; live preview; validation; save to library.
- Line chart is a first-class chart type (`ANL-01`), with max 4 series, zero baselines, direct
  labels, tooltip and keyboard-accessible points.
- Scheduled reports: recipient list, cadence, timezone, format (CSV/JSON/PDF), delivery
  channel (email/webhook), enabled toggle, next run, last result; failures are visible.
- Exports are async jobs for > 5,000 rows: queued/running/completed/failed, download link with
  expiry, audit event.
- Drill-down: clicking a bar/segment opens the filtered artifact list in a drawer or route;
  cross-filtering updates the other cards where the metric supports it.
- Data freshness: "Updated 18s ago" and a refresh-on-SSE indicator; never a manual refresh
  requirement.

**States:** no data for period, insufficient data for a chart, query running, query timeout,
export running/failed, scheduled report failed, permission denied.

**Accessibility:** every chart has a text summary and a table toggle; colour palette is
colour-blind safe; legend items are toggles with keyboard support; the chart canvas/SVG has
`role="img"` and an `aria-label`, with the table providing the detailed data.

**Acceptance criteria**

- [ ] A line chart can be created, saved, shared and exported.
- [ ] Every chart can be read as a table.
- [ ] Scheduled reports produce an auditable delivery result.
- [ ] Filters and report selection survive reload via the URL.
- [ ] A 100k-row export does not block the API event loop.

**Tasks:** `UI-09`, `ANL-01`..`ANL-06`.

### 6.11 Traceability

**Current:** `ui/src/components/workspace/WorkspaceTraceability.vue`. A matrix and end-to-end
evidence list plus a gap register. No faceted filters, drill-down or export.

**Jobs:** prove that a requirement is covered by tests, code, build and deployment; find gaps.

**Target layout:**

```text
Page header 72
Traceability                                      [Export matrix]
Evidence from requirement to production health

Filter bar 48
[Feature ▾] [Release ▾] [Coverage ▾] [Owner ▾] [Show gaps only]

┌──────────────────────────────────────────────────────────────────────────────┐
│ Requirement      │ Tests │ Code │ Build │ Deploy │ Health │ Coverage          │
├──────────────────┼───────┼──────┼───────┼────────┼────────┼───────────────────┤
│ ATL-142 Guest…   │ 4/4   │ #482 │ #129  │ prod   │ ●      │ ● Complete        │
│ ATL-143 Saved…   │ 2/3   │ #480 │ #128  │ stage  │ ●      │ ▲ Partial         │
│ ATL-144 Refund…  │ 0/2   │ —    │ —     │ —      │ —      │ ✗ Gap             │
└──────────────────────────────────────────────────────────────────────────────┘

Gap register (below, collapsible)
│ Severity │ Gap │ Requirement │ Owner │ Age │ Action │
```

**Specification**

- Matrix: sticky first column, 44 px rows, 40 px header; cells show count/status + link; a
  cell click opens a 480 px drawer with the linked artifacts and evidence.
- Coverage states: Complete (all required evidence), Partial (some), Gap (none), Not
  applicable (with reason). Never infer completeness from a single link.
- Filters: feature, release, sprint, owner, coverage state, date; "Show gaps only" is a
  first-class toggle.
- Gap register: severity, gap, requirement, owner, age, suggested action; sortable; links to
  the artifact; export CSV.
- Export matrix: CSV/JSON, includes filter context and generated timestamp; async for large
  matrices.
- Empty state explains how links are created (work item ↔ test ↔ PR ↔ build ↔ deployment).

**Responsive:** below 1024 px, the matrix becomes a list of requirement cards with evidence
counts; the gap register remains a table with horizontal scroll inside its region.

**Accessibility:** matrix is a real `<table>` with scope headers; coverage is text + icon;
drawer focus is trapped; export status is announced.

**Acceptance criteria**

- [ ] Every matrix cell links to the exact evidence.
- [ ] Gaps are distinguishable from not-applicable.
- [ ] Filters and "gaps only" are shareable via URL.
- [ ] Export includes the filter context.
- [ ] The matrix remains usable with 1,000 requirements (virtualized rows).

**Tasks:** `UI-10`, `TST-05`, `PLN-02`.

### 6.12 Project settings

**Current:** `ui/src/components/workspace/WorkspaceSettings.vue`. Project details and members,
with role selects and remove actions. No environments, integrations, delivery policy,
notifications or danger zone.

**Jobs:** configure the project safely, manage members and roles, connect systems, and change
destructive settings with clear consequences.

**Target layout:**

```text
Page header 72
Settings
Project configuration and access

┌──────────────────┬───────────────────────────────────────────────────────────┐
│ SETTINGS NAV 240 │ FORM 720 max                                              │
│                  │                                                           │
│ Details          │ Project details                                           │
│ Members 12       │ Name          [Atlas Commerce.....................]       │
│ Environments 4   │ Key prefix    [ATL]  (immutable after creation)           │
│ Integrations     │ Description   [...................................]       │
│ Delivery policy  │ Default branch[main ▾]                                    │
│ Notifications    │ Timezone      [UTC ▾]                                     │
│ Danger zone      │                                                           │
│                  │ [Save changes]  (sticky bar when dirty)                   │
└──────────────────┴───────────────────────────────────────────────────────────┘
```

**Specification**

- Settings nav 240 px, sticky; sections are routes (`/projects/:id/settings/members`) so they
  are linkable; the sidebar highlights the current section.
- Form content max 720 px; labels above fields; 24 px between fields; 40 px between sections;
  12 px helper text; inline validation on blur and submit.
- Sticky save bar appears only when dirty: "Unsaved changes" + Discard + Save; route change
  and unload guards.
- Details: name, key prefix (read-only after creation), description, default branch, timezone,
  visibility, owner. Changing the owner requires a confirmation and a second owner exists.
- Members: searchable table (name, username, role, joined, last active), invite by directory
  search, role select with capability summary, remove with last-owner protection, bulk role
  change, pending invitations, audit link.
- Environments: name, type, URL, health check URL, deploy strategy, requires approval,
  position; reorder; delete with confirmation and linked deployment warning.
- Integrations: Gitea URL/org/token status, Argo CD URL/token status, webhook secret rotation,
  AI service status; "Test connection" with a result and timestamp; secrets are write-only and
  never displayed.
- Delivery policy: merge policy (require approved review, required checks, linear history,
  delete branch), deployment approvals (quorum, roles), freeze windows, rollback policy.
- Notifications: per-event subscriptions (assignments, mentions, failures, approvals) and
  channel preferences.
- Danger zone: archive project, transfer ownership, delete project. Delete requires typing the
  project key, shows impact counts and retains an audit record. Only owner/admin.

**States:** clean/dirty/saving/saved, validation error, conflict (409), permission denied
(read-only with reason), integration test running/success/failure, destructive confirmation.

**Responsive:** below 1024 px, settings nav becomes a select or horizontal scroll; forms full
width; save bar fixed above the safe area.

**Accessibility:** fieldsets/legends for groups; error summary at the top of the form linked to
fields; role select has a description; destructive dialogs have explicit labels; status
messages are announced.

**Acceptance criteria**

- [ ] Every setting persists and reloads exactly.
- [ ] Role changes take effect on the next request (no stale access).
- [ ] Secrets are never returned by the API or rendered in the UI.
- [ ] Destructive actions require explicit confirmation and are audited.
- [ ] Unsaved changes cannot be lost accidentally.

**Tasks:** `UI-11`, `GOV-01`, `GOV-02`, `DEL-03`, `OPS-06`.

### 6.13 Search

**Current:** `ui/src/views/SearchView.vue`. Query input, system filters, examples, saved
filters in `localStorage`, result tabs for work items, features, test cases and projects.

**Jobs:** find any artifact across projects, refine quickly, save/share the query, and act on
results without losing the search.

**Target layout (≥1280 px):**

```text
Page header 72
Search                                              [Saved views ▾] [Export]
Search every artifact you can access.

Query bar 56
[🔍  status:blocked AND assignee:me            ] [Search] [Build query ▾]
Recent:  my open bugs  ·  release:v2.4  ·  failing checks

┌──────────────────┬───────────────────────────────────────────────────────────┐
│ FACETS 260       │ Results 1–50 of 214 for status:blocked                    │
│                  │ [All 214] [Work 128] [Tests 24] [PRs 18] [Builds 12] [...] │
│ Project          │                                                           │
│  ☑ Atlas 128     │ ┌───────────────────────────────────────────────────────┐ │
│  ☐ Payments 64   │ │ ATL-142  Bug  ● Blocked  P1  @Ana  Atlas  2h          │ │
│ Type             │ │ Guest checkout fails with saved card                  │ │
│  ☑ Bug 42        │ │ …highlighted snippet…                    [Open] [⋯]    │ │
│  ☐ Story 86      │ ├───────────────────────────────────────────────────────┤ │
│ Status           │ │ ATL-131  Task ● Blocked  P2  @Sam  Atlas  5h          │ │
│  ☑ Blocked 18    │ │ …                                                     │ │
│ Assignee         │ └───────────────────────────────────────────────────────┘ │
│  ☑ Me 9          │                                                           │
│                  │ 50 per page                              1–50 of 214 ‹ ›│
└──────────────────┴───────────────────────────────────────────────────────────┘
```

**Specification**

- The query bar supports a text mode and a visual query builder (field, operator, value,
  chips); switching preserves the query. Syntax help is a popover, not a permanent wall of
  text.
- Recent searches are server-side, per user, last 10; saved searches are named, shareable,
  pinned and permission-aware.
- Facets: project, type, status, priority, assignee, reporter, sprint, release, date, has
  links; counts reflect the current query and access. Multi-select with "Clear".
- Result groups: work items, features, test cases, projects, pull requests, builds,
  deployments, comments, audit (permission-filtered). Tabs show counts; "All" interleaves by
  relevance or updated date.
- Result row: 64–72 px, key/type/status/priority, title, highlighted snippet, project, owner,
  updated; hover reveals Open, Copy link, Add to saved view. Keyboard `J/K`, `Enter`.
- Highlighting is safe (no HTML injection); snippets respect the query terms.
- Bulk actions on selected results: add to saved view, export, open in new tabs (limit 10).
- The command palette reuses the same search API with a compact result model; it does not
  duplicate query logic.

**States:** idle (recent/saved/examples), searching, no results (suggest removing facets),
error with retry, permission-filtered note, query syntax error with position, slow query hint.

**Responsive:** facets become a "Filters (3)" drawer below 1024 px; result rows collapse to
two lines; the query bar wraps but stays sticky under the top bar.

**Accessibility:** combobox pattern for the query builder; facet groups are fieldsets; result
count is `aria-live="polite"`; highlighted text is not the only indicator; saved view menu is
keyboard navigable.

**Acceptance criteria**

- [ ] Saved and recent searches are server-side and survive device changes.
- [ ] Every artifact type in the system is searchable within permissions.
- [ ] Facet counts match the result set.
- [ ] Query errors explain the offending token and position.
- [ ] Search p95 < 300 ms for 100k artifacts in the reference dataset.

**Tasks:** `UI-12`, `SRH-01`..`SRH-05`.

### 6.14 Profile and security

**Current:** `ui/src/views/ProfileView.vue`. Display name, email, avatar URL, password change.
No sessions, MFA, preferences or notification settings.

**Jobs:** manage identity, secure the account, review active sessions, and set personal
preferences.

**Target layout (≥1024 px):**

```text
Page header 72
My profile

┌──────────────────────┬────────────────────────────────────────────────────────┐
│ IDENTITY CARD 320    │ Tabs 44                                                │
│ [avatar]             │ [Profile] [Security] [Sessions] [Preferences]          │
│ Mohamed Kilany       │                                                        │
│ @mkilany             │ Display name  [Mohamed Kilany................]         │
│ Admin · SPM          │ Email         [mohamed@example.com..........]         │
│ [Change avatar]      │ Job title     [..............................]         │
│                      │ Timezone      [UTC ▾]                                  │
│ Member since 2025    │ Language      [English ▾]                              │
│                      │                                                        │
│                      │ [Save changes]                                         │
└──────────────────────┴────────────────────────────────────────────────────────┘
```

**Specification**

- Identity card: avatar with upload/crop (offline, local), display name, username, global
  role, member since, project count; no editable fields duplicated in the card.
- Profile tab: display name, email, job title, timezone, language, avatar; email change
  requires current password and sends a verification event (or is admin-approved in offline
  mode).
- Security tab: change password (current/new/confirm, strength meter, breach-list check if
  available offline), MFA/TOTP setup with QR + recovery codes, passkeys later, API tokens
  (create/scopes/expiry/revoke, show once).
- Sessions tab: device, browser, IP, location (if available), last active, current badge,
  revoke; "Revoke all other sessions" requires confirmation.
- Preferences tab: theme (system/light/dark/high contrast), density, sidebar behavior,
  default landing page, notification channels, keyboard shortcuts.
- Save bar per tab; no global "Save all" that silently saves hidden changes.

**States:** clean/dirty/saving, avatar upload progress/error, password mismatch/weak, MFA
enrolment/verification, session revoke confirmation, permission denied for admin-only fields.

**Responsive:** identity card stacks above the tabs below 1024 px; tabs scroll horizontally;
forms full width.

**Accessibility:** avatar upload has a text alternative and preview; password fields have
show/hide and strength text; QR has a text secret fallback; session table has captions.

**Acceptance criteria**

- [ ] Profile changes persist and are reflected in the shell immediately.
- [ ] Password change invalidates other sessions.
- [ ] MFA enrolment and recovery codes work offline.
- [ ] Session revocation is immediate and audited.
- [ ] Preferences persist per user and apply on next load.

**Tasks:** `UI-13`, `GOV-08`, `GOV-09`, `GOV-10`, `GOV-11`.

### 6.15 Administration — users and roles

**Current:** `ui/src/views/AdminUsersView.vue`. A user table with create/edit forms, role and
active state, and password reset. No search/filter, bulk actions, user drawer, project
membership view, session view or audit link.

**Jobs:** onboard, change roles, deactivate and audit users without losing the last admin.

**Target layout:**

```text
Page header 72
Users and roles                                   [Invite user] [Export]
12 active · 2 disabled · 3 admins

Toolbar 48
[Search name, username, email…] [Role ▾] [Status ▾] [Project ▾] [Columns]

┌──────────────────────────────────────────────────────────────────────────────┐
│ ☐ Name            Username   Role    Projects  Status    Last active   ⋯    │
├──────────────────────────────────────────────────────────────────────────────┤
│ ☐ Ana Silva       asilva     Member  4         Active    2h ago             │
│ ☐ Sam Lee         slee       Admin   2         Active    1d ago             │
│ ☐ Lee Chen        lchen      Viewer  0         Disabled  3w ago             │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Specification**

- Table: selection, name/avatar, username, email (hidden by default via column picker), global
  role, project count, status, last active, overflow. 50 per page; server-side search/filter.
- Bulk actions: activate/deactivate, change role (with last-admin guard), export; confirmation
  states the count and impact.
- Invite flow: 640 px modal with email, display name, global role, initial projects, send
  invite / set temporary password; password is never displayed after creation.
- User drawer (480 px): header (avatar, name, username, status), tabs Profile, Roles, Projects,
  Sessions, Audit. Profile edits; role changes with capability summary; project memberships
  with role; sessions with revoke; audit filtered to the user.
- Deactivation: confirmation explains session revocation, project access removal and
  reassignment; last-admin and self-lockout protections are visible.
- Password reset: generates a one-time link/temporary password, requires step-up auth, audits
  the action and never logs the secret.
- Empty/error/loading states; disabled users are visually distinct but still readable.

**Responsive:** table becomes stacked user cards below 768 px; drawer full-screen; bulk bar
sticky.

**Accessibility:** table caption/sort; row selection labels; drawer focus trap; role changes
announced; destructive confirmations have explicit object names.

**Acceptance criteria**

- [ ] An admin can find a user by name, username or email.
- [ ] Last-admin and self-lockout protections are enforced server-side and surfaced in the UI.
- [ ] Deactivation invalidates sessions and removes access on the next request.
- [ ] Every admin action is audited with actor, target and before/after.
- [ ] The table remains usable with 10,000 users (server-side pagination).

**Tasks:** `UI-14`, `GOV-04`, `GOV-05`, `GOV-12`.

### 6.16 Administration — audit log

**Current:** none. Activity events exist per project, but there is no global, searchable,
tamper-evident audit surface.

**Jobs:** investigate who changed what, when, from where and with what result; export evidence.

**Target layout:**

```text
Page header 72
Audit log                                         [Export] [Verify chain ✓]
Append-only record of privileged and security-relevant actions.

Filter bar 48
[Time range ▾] [Actor ▾] [Action ▾] [Project ▾] [Result ▾] [Search…]

┌──────────────────────────────────────────────────────────────────────────────┐
│ Time (UTC)        Actor     Action              Target          Project  Res  │
├──────────────────────────────────────────────────────────────────────────────┤
│ 18 Nov 14:32:08   mkilany   user.role_changed   slee → admin    —        ✓    │
│ 18 Nov 14:30:41   asilva    project.member_add  lchen → tester  ATL      ✓    │
│ 18 Nov 14:28:02   slee      auth.login_failed   asilva          —        ✗    │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Specification**

- Default range: last 24 hours; presets 1h/24h/7d/30d/custom; UTC displayed with local
  timezone in the tooltip and a user preference.
- Columns: timestamp, actor (name + id), action (namespace.verb), target (type + id/name),
  project, result, IP, request ID. Column picker for the long tail.
- Row click opens a 640 px drawer: summary, actor, target, before/after JSON diff, request
  metadata, related events, copy request ID, link to the object.
- Filters are server-side and URL-encoded; saved audit views are shareable (admin only).
- Export CSV/JSON for the filtered range; large exports are async and audited.
- Retention banner: "Retained for 365 days · Exported copies are the operator's
  responsibility." Chain verification shows verified/broken with the last verified sequence.
- No edit/delete actions. The UI must not offer them.
- Sensitive values are redacted at write time, not hidden only in the UI.

**States:** loading, empty range, no matches, export running/failed, chain verification
failed (critical banner), permission denied.

**Responsive:** table columns collapse to time/actor/action/target; drawer full-screen.

**Accessibility:** table caption and sort; diff is a definition list or table, not colour-only;
timestamps have accessible absolute values; live region for export status.

**Acceptance criteria**

- [ ] Every privileged action appears within 5 seconds.
- [ ] Before/after values are available where applicable and redacted where required.
- [ ] The hash chain verifies and tampering is detectable.
- [ ] Export matches the active filters and is itself audited.
- [ ] No API or UI path can mutate or delete an audit record.

**Tasks:** `UI-15`, `GOV-04`, `OBS-06`, `OPS-11`.

### 6.17 Administration — system health

**Current:** none. Health endpoints exist (`/health`, `/health/live`, `/health/ready`) but no
operator surface.

**Jobs:** see whether the platform and its dependencies are healthy, what changed, and where to
look next.

**Target layout:**

```text
Page header 72
System health                                     [Refresh 10s ▾] [Run checks]
Production · build 2.4.0 · commit a1b2c3 · 18 Nov 2026 14:32 UTC

Service grid 3 × 2, 16 px gaps
┌──────────────────┐ ┌──────────────────┐ ┌──────────────────┐
│ API              │ │ PostgreSQL       │ │ NATS JetStream   │
│ ● Healthy        │ │ ● Healthy        │ │ ● Healthy        │
│ p95 42 ms        │ │ 18/20 conns      │ │ lag 12 msgs      │
│ [Details]        │ │ [Details]        │ │ [Details]        │
└──────────────────┘ └──────────────────┘ └──────────────────┘
┌──────────────────┐ ┌──────────────────┐ ┌──────────────────┐
│ Gitea            │ │ Argo CD          │ │ AI service       │
│ ● Healthy        │ │ ▲ Degraded       │ │ ● Healthy        │
│ 1.24.7           │ │ 2 apps out sync  │ │ tfidf-extractive │
└──────────────────┘ └──────────────────┘ └──────────────────┘

Dependencies / queues
│ Component │ Status │ Latency │ Errors 5m │ Saturation │ Last check │
│ NATS      │ ●      │ 12 ms   │ 0          │ 8%         │ 5s ago     │
```

**Specification**

- Service cards: name, status, one key metric, version/build, last check, details link; status
  is Healthy/Degraded/Unknown/Unhealthy with text + icon.
- Dependency table: latency, error rate, saturation, queue lag, last check; sortable.
- Details drawer: recent checks, error samples, links to logs/traces/dashboards, runbook.
- "Run checks" triggers an audited, rate-limited check; it does not restart services.
- Read-only for non-admins; admin-only access; no secrets displayed.
- If a metric is unavailable, show "Not instrumented" with the task link, not a fake zero.

**States:** all healthy, degraded, unknown (insufficient data), check running, check failed,
stale data.

**Responsive:** cards 2 columns at 768–1279, 1 below; table scrolls within its region.

**Accessibility:** status text + icon; the auto-refresh can be paused; live updates are not
announced on every tick, only on state changes.

**Acceptance criteria**

- [ ] Every service in the topology has a card and an owner/runbook link.
- [ ] Degraded/unknown is distinguishable from unhealthy.
- [ ] No secret or connection string is exposed.
- [ ] The page works when a dependency is down (it is the page you need then).
- [ ] Metrics link to the exact dashboard panel.

**Tasks:** `UI-16`, `OBS-01`..`OBS-05`, `OPS-03`.

### 6.18 Notifications inbox

**Current:** none. Toasts exist for transient feedback; there is no durable inbox.

**Jobs:** see assignments, mentions, approvals, failures and due dates; act without hunting.

**Target layout:**

```text
Page header 72
Inbox                                              [Mark all read] [Preferences]
3 unread

Tabs 44
[All 24] [Unread 3] [Mentions] [Assignments] [Approvals] [Failures]

┌───────────────────────────────┬──────────────────────────────────────────────┐
│ LIST 420                      │ DETAIL                                       │
│ [Search…]                     │ ATL-142 assigned to you                      │
│ ● Ana assigned ATL-142  2m    │ Ana Silva · 2m ago                           │
│ ○ Build #129 failed     18m   │ [Open work item] [Mark read] [Snooze]        │
│ ○ Approval requested    1h    │                                              │
│                               │ Body / context / links                       │
└───────────────────────────────┴──────────────────────────────────────────────┘
```

**Specification**

- List rows: unread dot, actor avatar, event text, object key, relative time, project; 64 px.
- Detail: title, actor/time, body, primary action, mark read/unread, snooze (1h/tomorrow/
  custom), unsubscribe from this thread.
- Preferences: event types, channels (in-app/email/webhook), quiet hours, digest cadence.
- Notifications are generated from domain events, not client-side polling. Unread counts are
  consistent across the shell and inbox.
- No email dependency for in-app notifications; email is optional and offline-configurable.

**States:** empty (all caught up), unread only, no matches, detail loading, notification
expired (object deleted), permission lost.

**Responsive:** list and detail become separate routes below 1024 px; filters in a drawer.

**Accessibility:** list is a `listbox` or list with `aria-selected`; unread is text + dot;
mark-all is announced; keyboard `J/K`, `Enter`, `U`.

**Acceptance criteria**

- [ ] Unread count matches the list and updates live.
- [ ] Deep links open the exact object and mark read.
- [ ] Preferences suppress channels without losing the in-app record.
- [ ] Notification text never leaks an object the user cannot access.

**Tasks:** `UX-14`, `PLN-02`, `GOV-04`.

### 6.19 Command palette

**Current:** none.

**Jobs:** navigate, create, search and run actions from the keyboard without learning the IA.

**Target layout:**

```text
┌──────────────────────────────────────────────────────────────┐
│ 🔍  Search or type a command…                         Esc    │
├──────────────────────────────────────────────────────────────┤
│ RECENT                                                       │
│  ↗ ATL-142 Guest checkout fails                  Work item   │
│  ↗ Atlas Commerce                                Project     │
│                                                              │
│ NAVIGATE                                                     │
│  → Projects                                       G P        │
│  → Search                                         G S        │
│  → Inbox                                          G I        │
│                                                              │
│ CREATE                                                       │
│  + New work item                                  C          │
│  + New test case                                             │
│                                                              │
│ ACTIONS                                                      │
│  ⚙ Open project settings                                     │
│  ⌨ Keyboard shortcuts                             ?          │
└──────────────────────────────────────────────────────────────┘
```

**Specification**

- Overlay 640 px wide, max 70vh, centred horizontally, 12vh from the top; backdrop blur and
  click-outside close; `Esc` closes and restores focus.
- Input autofocuses; results update after 150 ms (local actions) and 250 ms (server search);
  loading indicator in the input.
- Groups: Recent, Navigate, Create, Search results, Actions. Max 5 per group, "Show all" for
  search.
- Keyboard: ↑/↓ move, `Enter` run, `Tab`/`Shift+Tab` cycle groups, `Ctrl/Cmd+Enter` open in a
  new tab, `Esc` close.
- Actions are permission-filtered; disabled actions are omitted or explain why.
- Recent items are per user, server-side; no sensitive titles in the palette if the user lost
  access.
- The palette reuses the search API and navigation registry; no duplicated route lists.

**States:** idle, loading, no results (suggest search), error (search unavailable; local
actions still work), permission-filtered.

**Accessibility:** `role="dialog"` with `aria-modal`, combobox + listbox pattern,
`aria-activedescendant`, result count announced, focus trap, no keyboard trap.

**Acceptance criteria**

- [ ] `Ctrl/Cmd+K` opens from every authenticated screen.
- [ ] Navigation, create and search actions work without a mouse.
- [ ] Recent items and permissions are respected.
- [ ] The palette never shows an object the user cannot open.
- [ ] Screen-reader users can identify the active option and group.

**Tasks:** `UX-09`, `UX-10`, `SRH-01`.

---

## 7. Phased task plan

### 7.0 Conventions

- **Status:** `DONE`, `TODO`, `IN PROGRESS`, `BLOCKED`, `CANCELLED`.
- **Priority:** `P0` release/security/data blocker, `P1` enterprise requirement, `P2`
  important, `P3` later.
- **Estimate:** `S` ≤ 1 day, `M` 2–3 days, `L` 4–5 days, `XL` > 5 days (must be split before
  starting).
- Every card is independently releasable behind a flag when it cannot ship atomically.
- "Tests" lists the minimum evidence; the PR gate must still be green.
- Task IDs are referenced in branch names, PR titles and the changelog.

### Phase 0 — Baseline, truth and delivery gate

#### BASE-01 · Clean baseline and secret purge
- **Status:** DONE · **Priority:** P0 · **Estimate:** S · **Branch:** bootstrap (flow exception)
- **Scope:** Rebuild the repository as a single orphan commit; exclude the tracked
  `kube-config.yaml`; expire reflog and garbage-collect the old objects; add the hardened
  `.gitignore`; add `origin`; push `master`.
- **Acceptance criteria:**
  - [x] `kube-config.yaml` is not tracked and not reachable in any commit or object.
  - [x] `.env`, kubeconfigs, keys, certificates, dumps, backups, models and build output are
    ignored; `.env.example` remains tracked.
  - [x] `git fsck --unreachable` shows no secret-bearing commit after GC.
  - [x] `master` is pushed to `https://github.com/Mohamed-Kilany/SPM`.
- **Tests/evidence:** `git log --all -- kube-config.yaml` is empty; `git rev-list --all
  --objects` contains no kubeconfig path; `git status` is clean apart from ignored local files.
- **Docs:** `.gitignore` comments explain the policy; README quick start remains valid.

#### BASE-02 · PR-only CI gate
- **Status:** DONE · **Priority:** P0 · **Estimate:** M · **Branch:** bootstrap (flow exception)
- **Scope:** Add `pr-gate.yml` and `codeql.yml` triggered only by `pull_request` to `master`;
  cover Go, UI, AI, Compose, Docker image builds, dependency review and CodeQL; add the PR
  template and Dependabot.
- **Acceptance criteria:**
  - [x] No workflow has a `push`, `pull_request_target` or `schedule` trigger.
  - [x] All jobs are deterministic on a clean checkout.
  - [x] The Compose job creates a throwaway `.env` from `.env.example`.
  - [x] CodeQL covers Go, JavaScript/TypeScript and Python.
  - [x] Dependabot opens PRs (which run the gate) rather than pushing updates.
  - [x] Dependabot config carries labels, assignee/reviewer, a `chore(deps)` commit prefix,
    separately grouped security updates, and per-ecosystem open-PR limits (`ADR-023`).
- **Tests/evidence:** YAML parses; the first validation PR runs every job successfully; a
  Dependabot PR with a dependency diff runs `Security: dependency review` successfully.
- **Docs:** PR template checklist mirrors the jobs.

#### BASE-03 · Branch protection and merge policy
- **Status:** DONE · **Priority:** P0 · **Estimate:** S · **Branch:** bootstrap (flow exception)
- **Scope:** After the validation PR proves the check names, enable protection on `master`:
  PR required, strict checks, conversation resolution, linear history, no force push/delete,
  admins enforced, squash-only.
- **Acceptance criteria:**
  - [x] The required contexts match the workflow job names exactly (9 contexts).
  - [x] A direct push to `master` is rejected (`GH006`, "Changes must be made through a pull request").
  - [x] A PR with a failing required check cannot merge (`9 of 9 required status checks are expected`).
  - [x] A PR with unresolved conversations cannot merge.
  - [x] The first protected PR is merged through the protected flow.
- **Tests/evidence:** `gh api repos/Mohamed-Kilany/SPM/branches/master/protection` returned
  strict=true, the 9 contexts, 0 approvals, stale-review dismissal, `enforce_admins=true`,
  linear history, conversation resolution, and no force push/delete; `git push origin master`
  was rejected; `gh pr merge` on a red Dependabot PR was rejected; the BASE-03 docs PR merged
  only after a green gate and a resolved review thread.
- **Docs:** Section 2.4 and Section 4.1.

#### BASE-04 · Documentation truth pass
- **Status:** DONE · **Priority:** P0 · **Estimate:** S · **Branch:** `docs/truth-baseline`
- **Depends on:** BASE-03
- **Scope:** Correct every false or stale claim in `README.md`, `docs/ARCHITECTURE.md`,
  `docs/ENTERPRISE_SPEC.md` and `docs/openapi.yaml`. Add "Last verified" dates and evidence
  links. Mark only work that the PR gate actually proves.
- **Out of scope:** Implementing the missing features.
- **Acceptance criteria:**
  - [x] README no longer claims a Prometheus endpoint; it links `OBS-01`.
  - [x] ARCHITECTURE describes TF-IDF + optional GGUF, not embeddings, until `AI-01` lands.
  - [x] ARCHITECTURE states exactly what CI runs (unit, not integration/E2E) and links the
    tasks that close the gap.
  - [x] ENTERPRISE_SPEC checkboxes reflect verified evidence only.
  - [x] OpenAPI is either regenerated or clearly marked as stale with a link to `BASE-05`.
- **Tests/evidence:** The four documents now carry a "Last verified" date and links to the
  workflow, service code and tests that support the claims; relative links resolve; the docs-only
  PR passed all 9 required contexts before merge.
- **Docs/telemetry:** This card is documentation-only; no runtime change.

#### BASE-05 · OpenAPI contract pipeline
- **Status:** DONE · **Priority:** P0 · **Estimate:** M · **Branch:** `feature/api-openapi-contract`
- **Depends on:** BASE-04
- **Scope:** Make the HTTP contract executable. Move route registration into an importable package,
  generate `docs/openapi.yaml` from the canonical route inventory, add contract tests that fail
  when a route is missing or undocumented, lint with Redocly, and prove UI-client path parity.
- **Acceptance criteria:**
  - [x] Every registered route appears in OpenAPI with method, auth, request, response and
    error envelope (81 method/path pairs).
  - [x] Removed routes are absent; new routes are present; every regenerated run removes
    unreachable components.
  - [x] CI fails on undocumented routes, an out-of-date document or invalid schemas through the
    existing Go and UI jobs; no required context names changed.
  - [x] A contract test proves the hand-written UI client targets documented paths.
  - [x] `scripts/openapi.ps1` and `go run ./cmd/openapi -write` regenerate deterministically.
- **Tests/evidence:** `TestRoutesMatchRegistration`, `TestOpenAPIContractMatchesRouter`,
  `TestOpenAPIOperationsAreComplete`, `TestUIClientPathsExist`, `go run ./cmd/openapi -check`,
  Redocly lint in the UI job, and a green required gate on the pull request. A `schemathesis`
  smoke run needs an ephemeral stack and remains part of `QAT-04`.
- **Docs/telemetry:** [`docs/API.md`](docs/API.md) documents the source-of-truth map, regeneration
  commands, versioning and deprecation policy. No route is deprecated today, so no deprecation
  header is emitted.

#### BASE-06 · Supply chain and dependency automation baseline
- **Status:** DONE · **Priority:** P1 · **Estimate:** M · **Branch:** `chore/supply-chain-baseline`
- **Depends on:** BASE-02
- **Scope:** Add lockfile integrity checks, `govulncheck`, `npm audit`/`pip-audit` triage,
  SBOM generation (`syft`), image scanning (`trivy`) and a documented exception policy. Keep
  Dependabot alerts and automated security fixes enabled, with grouped security-update PRs and
  per-ecosystem limits (`ADR-023`). Start advisory, then make high/critical findings blocking
  once the baseline is clean. Signing, provenance and admission verification are delivered by
  `OPS-07`, which consumes the SBOMs and scan evidence produced here.
- **Acceptance criteria:**
  - [x] SBOMs are produced for the API, UI and AI images (Syft SPDX JSON, uploaded as the
    `supply-chain-evidence` artifact on every pull request).
  - [x] High/critical vulnerabilities have an owner, due date and remediation or exception.
    Reachable Go, npm high/critical and Python advisories block the gate; image findings follow
    the documented promotion rule and `SC-002` review.
  - [x] Image signing is explicitly scoped to `OPS-07` by the task index; this card produces the
    evidence it consumes and makes no signed-image claim until that task lands.
  - [x] Dependabot PRs run the full PR gate; the config targets `master` with no bypass.
  - [x] Dependabot alerts and automated security fixes are enabled (verified through the GitHub
    API); grouped `*-security` update groups exist for Actions, Go, npm and pip.
  - [x] No secret-scanning alert is ignored: secret scanning and push protection are enabled and
    the open alert count is 0.
- **Tests/evidence:** `go mod verify`, `govulncheck` v1.10.0 (0 reachable vulnerabilities after
  upgrading Go to 1.27.4 and `golang.org/x/net` to v0.60.0), `npm audit --audit-level=high`
  (0 high/critical), `pip-audit` 2.10.1 (0 findings), SBOM/Trivy artifacts from the Images job,
  and `scripts/supply-chain.ps1`.
- **Docs/telemetry:** [`SECURITY.md`](SECURITY.md) documents private disclosure, supported
  versions, response targets, supply-chain controls, the exception register, the image-scan
  promotion rule and the release security checklist. This uses the repository-root path so
  GitHub surfaces it in the Security tab; `BASE-07` extends it with community files.

#### BASE-07 · Repository community and security files
- **Status:** DONE · **Priority:** P2 · **Estimate:** S · **Branch:** `docs/community-files`
- **Scope:** Extend the `SECURITY.md` created by `BASE-06` if any disclosure gap remains, and add
  `CONTRIBUTING.md` (branch/PR rules from this document), issue forms (bug, feature), support
  policy and code of conduct. A public security issue form is intentionally replaced by the
  GitHub private-advisory contact link, because a public template would contradict the
  disclosure policy.
- **Acceptance criteria:**
  - [x] SECURITY.md explains private reporting and does not ask for public exploits.
  - [x] CONTRIBUTING.md links this plan, the PR template, the verification commands and the
    contract workflow.
  - [x] Issue forms request reproduction, expected/actual behavior, environment and sanitized
    logs.
  - [x] No template encourages pasting secrets or customer data; each form contains a warning
    and a private-reporting redirect.
- **Tests/evidence:** All issue-form YAML parses; referenced labels (`bug`, `type/feature`) exist;
  GitHub private vulnerability reporting returns `enabled: true`; relative links resolve in the
  branch; `config.yml` disables blank issues and points to private advisories. Rendered forms are
  served only from `master`, so the post-merge render check is recorded in the activity log.
- **Docs/telemetry:** Root `CONTRIBUTING.md`, `SUPPORT.md`, `CODE_OF_CONDUCT.md`,
  `.github/ISSUE_TEMPLATE/*`; `SECURITY.md` cross-links.

#### BASE-08 · Environment, release and versioning model
- **Status:** DONE · **Priority:** P1 · **Estimate:** M · **Branch:** `docs/release-model`
- **Scope:** Define local/CI/staging/production, versioning (`MAJOR.MINOR.PATCH`), release
  tagging, changelog generation, promotion, rollback, migration order and compatibility
  (N/N-1 API, database expand/contract). Add `docs/RELEASING.md` and automate the changelog.
- **Acceptance criteria:**
  - [x] A release can be produced from `master` with one documented command/workflow:
    `scripts/release.ps1 -Version` prepares the changelog PR, `-Tag` verifies and pushes the
    annotated tag, and `.github/workflows/release.yml` publishes the release.
  - [x] The changelog is generated from first-parent conventional commits, pull-request numbers
    and task IDs; `scripts/changelog.ps1` is the single generator and `CHANGELOG.md` is checked
    in.
  - [x] Rollback to the previous tag is documented with commands, compatibility rules and the
    database constraint. The recurring rehearsal is explicitly owned by `OPS-09`; this card does
    not claim an unrehearsed rollback.
  - [x] Migration and application deploy order is explicit: backup, API with forward migrations,
    readiness gate, UI/AI, smoke check, expand/contract across two releases.
  - [x] Version is visible in the UI footer and in `/health` as `build.version`,
    `build.commit` and `build.built_at`, injected from the tag at build time.
- **Tests/evidence:** `scripts/changelog.ps1` output against the real history and `-Section`
  extraction; `scripts/release.ps1` parser and dry-run validation; `actionlint` on the workflow;
  Go tests for injected and default health metadata; Vitest for version normalization; AI pytest;
  a scratch-tag release dry run is executed after merge and recorded in the activity log.
- **Docs/telemetry:** `docs/RELEASING.md`, `CHANGELOG.md`, release workflow, build metadata in
  `/health` and the UI. The Prometheus build-info metric is delivered by `OBS-01` as
  `spm_build_info`; this card does not duplicate it.

#### BASE-09 · Retire the legacy project board
- **Status:** TODO · **Priority:** P2 · **Estimate:** S · **Branch:** `change/retire-legacy-board`
- **Depends on:** UI-05
- **Scope:** Remove `ProjectBoardView.vue` and the `/projects/:id/legacy` route after the
  workspace board reaches parity; redirect old links to the workspace board with the same
  filters.
- **Acceptance criteria:**
  - [ ] No route renders the legacy board.
  - [ ] Old bookmarks redirect correctly.
  - [ ] No dead component, test or style remains.
  - [ ] Bundle size decreases or stays flat.
- **Tests/evidence:** router test, grep for `legacy`, bundle report.
- **Docs/telemetry:** Remove references from README/architecture.

### Phase 1 — Identity, authorization, audit and governance

#### GOV-01 · Capability matrix and server-side enforcement
- **Status:** DONE · **Priority:** P0 · **Estimate:** L · **Branch:** `feature/authz-capabilities`
- **Depends on:** BASE-05
- **Scope:** Replace the single project-write gate with explicit capabilities. Define the
  canonical matrix for `owner`, `maintainer`, `developer`, `tester`, `viewer` and global roles;
  add `RequireProjectCapability` middleware that resolves membership from PostgreSQL on every
  request; attach the capability set to `/auth/me` for UI gating.
- **Capability matrix (implemented):**

| Capability | Owner | Maintainer | Developer | Tester | Viewer | Global admin |
|---|---|---|---|---|---|---|
| `project.read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `planning.write` | ✓ | ✓ | ✓ | — | — | ✓ |
| `work_item.write` | ✓ | ✓ | ✓ | ✓ (defects/status only) | — | ✓ |
| `test.write` | ✓ | ✓ | ✓ | ✓ | — | ✓ |
| `code.read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `code.write` | ✓ | ✓ | ✓ | — | — | ✓ |
| `pr.review` | ✓ | ✓ | ✓ | — | — | ✓ |
| `pr.merge` | ✓ | ✓ | — | — | — | ✓ |
| `delivery.read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `delivery.deploy` | ✓ | ✓ | ✓ (non-prod) | — | — | ✓ |
| `delivery.rollback` | ✓ | ✓ | — | — | — | ✓ |
| `settings.write` | ✓ | ✓ | — | — | — | ✓ |
| `member.write` | ✓ | ✓ | — | — | — | ✓ |
| `audit.read` | ✓ | ✓ | — | — | — | ✓ |

- **Acceptance criteria:**
  - [x] Every project route declares exactly one capability in
    `api/internal/server/metadata.go` and attaches `RequireProjectCapability`; the contract test
    rejects a missing or invalid capability on a project route and a mutating route without one.
    No route uses the old coarse write gate.
  - [x] A tester cannot create, edit or delete planning artifacts, repositories, branches or pull
    requests: those routes require `planning.write` or `code.write`, which the matrix withholds.
    Tester work-item creation is restricted to `type: "bug"`, and ticket/work-item edit and delete
    are refused by the handlers; status changes remain allowed.
  - [x] A developer cannot change settings or members (`settings.write`/`member.write` absent),
    merge (`pr.merge` absent), roll back (`delivery.rollback` absent) or create a production
    environment (`POST /environments` refuses `type: "production"` for developers).
  - [x] A global viewer stays read-only even with an owner membership row: effective capabilities
    intersect with `project.read`, `code.read` and `delivery.read`, and the route-level test walks
    every capability-gated route.
  - [x] Global admins retain break-glass access on every project route without a membership row.
    Each request logs `authz_break_glass` with request ID, user, project, capability, method and
    path; durable audit persistence is delivered by `GOV-04`.
  - [x] The UI gates board, planning, code, delivery, reports, tests and settings actions through
    `auth.can(projectId, capability)`. Settings explains the missing `settings.write`/
    `member.write` capability, and the board explains the tester restriction.
  - [x] Table-driven tests cover the full role × capability matrix, every project route × every
    project role, global viewer read-only enforcement, global admin break-glass and fail-closed
    membership lookup errors.
- **Tests/evidence:** `api/internal/auth/rbac_test.go` covers the matrix, `/auth/me` capability
  derivation and the middleware (owner/maintainer/developer/tester/viewer, non-member,
  global-viewer, unknown-role, lookup-error and immediate role-change cases).
  `api/internal/server/routes_test.go` walks every capability-gated route for each project role,
  for a global viewer with an owner membership, and for a global admin with no membership.
  Full `go test ./...`, `go vet ./...`, OpenAPI `-check`, UI `vue-tsc`, Vitest (16 tests) and the
  production build pass; the generated contract carries 61 `x-spm-capability` operations.
- **Docs/telemetry:** [`docs/AUTHZ.md`](docs/AUTHZ.md) defines the matrix, tester and developer
  restrictions, ownership rules, denial contract and `/auth/me` shape;
  [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) summarizes enforcement. Denials log
  `authz_denied`; break-glass logs `authz_break_glass`; caller-controlled project IDs and
  paths have CR/LF stripped so they cannot forge log entries. Audit storage and
  capability-change audit events are delivered by `GOV-04`; session/SSE invalidation is `GOV-02`.

#### GOV-02 · Membership and session invalidation
- **Status:** DONE · **Priority:** P0 · **Estimate:** M · **Branch:** `fix/membership-session-invalidation`
- **Depends on:** GOV-01
- **Scope:** Make membership changes effective immediately. Invalidate the user's tokens on
  add/update/remove/role change; stop trusting JWT project IDs for reads unless backed by a
  current membership check or token epoch; close/invalidate SSE streams for removed members.
- **Acceptance criteria:**
  - [x] Removing a member denies the next read. The membership resolver reads PostgreSQL on every
    request and returns `403`; the same change also invalidates the user's JWTs, so the next
    request normally returns `401 TOKEN_INVALIDATED` first. Both paths fail closed.
  - [x] Changing a role changes capabilities on the next request without re-issuing the token.
  - [x] SSE streams for a removed member close immediately through the NATS authorization
    invalidation subject; reconnecting is denied by the membership check, so no data is replayed.
  - [x] Archiving a project denies every non-admin request (the membership lookup joins
    `projects.status`), removes it from `/projects` and `/auth/me`, and closes every SSE stream.
    A deleted project fails closed because the membership row no longer resolves.
  - [x] Token invalidation is written to the NATS-backed `user_invalidation` KV and enforced by
    every API replica; the SSE eviction is published on core NATS, which fans out to every
    replica. A two-replica middleware test proves the shared KV rejects the token on both.
- **Tests/evidence:** `api/internal/auth` covers archived-project denial, admin break-glass on an
  archived project, immediate role changes and shared invalidation across two replicas.
  `api/internal/sse` covers user-scope and project-scope eviction, replay safety and connection
  accounting. `api/internal/metrics` covers the invalidation counter. Full `go test ./...`,
  `go vet ./...` and OpenAPI `-check` pass.
- **Docs/telemetry:** [`docs/AUTHZ.md`](docs/AUTHZ.md) defines the invalidation contract and
  [`docs/OBSERVABILITY.md`](docs/OBSERVABILITY.md) lists
  `spm_authz_invalidations_total{scope,reason}`. Membership add/update/remove writes an activity
  event; durable append-only audit storage is delivered by `GOV-04`. Invalidation publish failures log
  `authz_invalidation_failed` and remain best-effort because the database lookup is the
  enforcement point.

#### GOV-03 · Reviewer identity and anti-self-approval
- **Status:** TODO · **Priority:** P0 · **Estimate:** L · **Branch:** `feature/code-reviews-identity`
- **Depends on:** GOV-01, COD-03
- **Scope:** Add first-class pull request reviews: `pr_reviews` (reviewer, state, body,
  commit SHA, timestamps), review comments/threads, reviewer assignment and merge policy
  evaluation using real identities. Sync with Gitea where possible; never trust a client
  `review_status` string.
- **Acceptance criteria:**
  - [ ] A user cannot approve their own PR when policy forbids it.
  - [ ] An approval is tied to a reviewer and a commit SHA; a new push marks it stale.
  - [ ] Merge policy requires N distinct non-author approvals and all required checks.
  - [ ] Request changes blocks merge until resolved/dismissed by an authorized reviewer.
  - [ ] Review comments are threaded, resolvable and auditable.
  - [ ] The UI merge box explains every blocking reason.
- **Tests/evidence:** API tests for self-approval, stale approval, duplicate approval,
  changes-requested, merge policy; integration with Gitea review state.
- **Docs/telemetry:** `docs/CODE-REVIEW.md`, review audit events, review latency metric.

#### GOV-04 · Append-only audit log service
- **Status:** DONE · **Priority:** P0 · **Estimate:** L · **Branch:** `feature/audit-log`
- **Depends on:** GOV-01
- **Scope:** Add a global, tamper-evident audit log for security and privileged actions.
  Include actor, action, target, project, result, IP, user agent, request ID, before/after
  (redacted), and a hash chain. Deny update/delete at the database level. Add admin API and UI.
- **Acceptance criteria:**
  - [x] Auth, admin, membership, settings, token, merge, deploy and rollback actions emit an
    audit event. Covered producers are `auth.login`, password change, stream-ticket issue,
    admin user create/update, project create/update, settings, member add/update/remove,
    repository/branch/pull-request create, pull-request merge, environment/deployment create,
    deployment status/rollback and Argo CD observations. Exports and retention drops are also
    recorded.
  - [x] The table is append-only via trigger/permissions; update/delete fails.
    `015_audit_log.sql` installs `audit_events_deny_mutation()` on every partition; it raises
    `42501` for `UPDATE`, `DELETE` and `TRUNCATE` even for the table owner.
  - [x] A verification job/endpoint detects a broken chain. `GET /api/v1/admin/audit/verify`
    walks the retained log under an advisory lock, verifies
    `sha256(prev_hash || '|' || payload)`, distinguishes `hash_mismatch`, `broken_link` and
    `missing_anchor`, and reports the first bad event. The verifier runs as part of daily
    maintenance and refreshes the chain gauges.
  - [x] Filters, cursor pagination, detail and CSV/JSON export work. The admin API supports
    `action` prefix, `actor_id`, `project_id`, `result`, `from`/`to`, `limit` and an opaque
    keyset cursor; detail returns the stored before/after state; export caps at 50,000 rows and
    neutralises spreadsheet formula injection.
  - [x] Retention is configurable; expired partitions are dropped only by an audited job.
    `AUDIT_RETENTION_DAYS` defaults to 365 and `0` disables pruning. `audit_prune()` only drops
    whole months through `audit_drop_partition()`, which writes
    `audit.retention.partition_dropped` and stores the dropped partition's last hash in
    `audit_chain_anchors` so later verification still succeeds.
  - [x] Sensitive values are redacted before storage. `audit.Redact` masks password, token,
    secret, authorization, cookie, credential, API-key and session-ID keys recursively and
    truncates oversized strings; password changes store `password_changed: true` only.
- **Tests/evidence:** `api/internal/audit` covers recursive redaction, truncation, retention
  parsing, CSV injection, cursor validation, valid/tampered/broken/anchored chains, filter
  validation, disabled-logger safety, handler status codes and export headers.
  `api/internal/server` proves the four admin routes are registered and present in the generated
  OpenAPI contract; `api/internal/metrics` covers the audit write, chain and pruning series.
  Migration `015_audit_log.sql` defines the partition registry, chain trigger and audited drop
  path. Full `go build ./...`, `go vet ./...`, `go test ./... -count=1` and OpenAPI `-check`
  pass; UI `typecheck`, `test` and `build` pass.
- **Docs/telemetry:** [`docs/AUDIT.md`](docs/AUDIT.md) is the operational contract (schema,
  chain, retention, redaction, event catalog, incident response).
  `spm_audit_writes_total{outcome}`, `spm_audit_write_duration_seconds`,
  `spm_audit_chain_valid`, `spm_audit_chain_rows_checked`,
  `spm_audit_chain_last_verified_timestamp_seconds` and
  `spm_audit_partitions_pruned_total` are exported on the internal `/metrics` listener and
  documented in [`docs/OBSERVABILITY.md`](docs/OBSERVABILITY.md) with failed-write and broken-chain
  alerts. Audit write failures log `audit_write_failed` and never roll back the business action.
  Admin UI: `/admin/audit` with filters, pagination, detail, verify and export.

#### GOV-05 · Admin action audit and step-up authentication
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/admin-step-up`
- **Depends on:** GOV-04, GOV-08
- **Scope:** Audit every admin user action and require step-up authentication (password or
  MFA) for role changes, deactivation, password reset, token creation and secret rotation.
  Add a required reason field for high-impact actions.
- **Acceptance criteria:**
  - [ ] Admin create/update/deactivate/reset/token actions appear in the audit log.
  - [ ] Sensitive actions reject a session that has not stepped up within 5 minutes.
  - [ ] The UI prompts for step-up and explains why.
  - [ ] Last-admin and self-lockout protections remain enforced.
  - [ ] The reason is stored and visible in the audit detail.
- **Tests/evidence:** step-up expiry test, audit assertions, last-admin test.
- **Docs/telemetry:** admin action metric, step-up failure event.

#### GOV-06 · OIDC single sign-on
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/auth-oidc`
- **Depends on:** GOV-05
- **Scope:** Add OIDC authorization-code + PKCE login with discovery, state/nonce, group/role
  mapping, JIT provisioning, account linking, RP-initiated logout and local-login fallback.
- **Acceptance criteria:**
  - [ ] Login works with a mock IdP and a real test IdP.
  - [ ] State/nonce/redirect URI validation rejects tampering.
  - [ ] Group mapping assigns global roles deterministically; unmapped users are denied or
    provisioned as viewer per policy.
  - [ ] Account linking requires an authenticated local session or admin action.
  - [ ] Logout clears the local session and redirects to the IdP end-session endpoint.
  - [ ] Local admin break-glass login remains available and audited.
- **Tests/evidence:** mock IdP integration tests, replay/state tests, role-mapping tests.
- **Docs/telemetry:** `docs/SSO.md`, login success/failure metrics.

#### GOV-07 · SCIM 2.0 provisioning
- **Status:** TODO · **Priority:** P2 · **Estimate:** L · **Branch:** `feature/auth-scim`
- **Depends on:** GOV-06
- **Scope:** Implement SCIM `/Users` and `/Groups` for create/update/deactivate, with bearer
  token auth, filtering, pagination and audit. Map groups to global roles and optionally
  project memberships.
- **Acceptance criteria:**
  - [ ] Create/update/deactivate via SCIM is idempotent and audited.
  - [ ] Deprovision invalidates sessions and removes memberships per policy.
  - [ ] Filtering and pagination follow SCIM 2.0.
  - [ ] Tokens are scoped, hashed and revocable.
  - [ ] Contract tests cover the supported subset.
- **Tests/evidence:** SCIM contract tests, deprovision integration test.
- **Docs/telemetry:** `docs/SCIM.md`, provisioning metrics.

#### GOV-08 · MFA, TOTP and passkeys
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/auth-mfa`
- **Depends on:** GOV-05
- **Scope:** Add TOTP enrolment with QR/recovery codes, enforcement policy per role, step-up
  verification and WebAuthn/passkey registration/login where the browser supports it.
- **Acceptance criteria:**
  - [ ] Enrolment requires password confirmation and verifies a code before enabling.
  - [ ] Recovery codes are hashed, single-use and regenerable.
  - [ ] Policy can require MFA for admins and optionally all users.
  - [ ] Disabling MFA requires step-up and is audited.
  - [ ] Passkeys can be registered, listed, renamed and revoked.
  - [ ] Lost-device recovery has an admin-assisted, audited path.
- **Tests/evidence:** TOTP vector tests, recovery-code tests, WebAuthn mock tests.
- **Docs/telemetry:** `docs/MFA.md`, MFA adoption and failure metrics.

#### GOV-09 · Session and device management
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/auth-sessions`
- **Depends on:** GOV-02
- **Scope:** Persist sessions (JTI, user, IP, user agent, created/last seen/revoked), list them
  in the profile, revoke one/all, and expire idle sessions. Introduce short-lived access tokens
  with refresh rotation if required by the security model.
- **Acceptance criteria:**
  - [ ] Active sessions are listed with device/browser/IP/last seen.
  - [ ] Revoking a session blocks the next request and closes its SSE stream.
  - [ ] "Revoke all others" requires confirmation and is audited.
  - [ ] Refresh rotation detects reuse and revokes the family.
  - [ ] Session limits and idle timeout are configurable.
- **Tests/evidence:** session revocation integration test, refresh-reuse test.
- **Docs/telemetry:** session count metric, revocation audit event.

#### GOV-10 · Password policy, lockout and reset
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/auth-password-policy`
- **Depends on:** GOV-08
- **Scope:** Raise the minimum to 12 characters, add a local common-password check and strength
  meter, progressive lockout/rate limiting, single-use reset tokens with expiry, and no forced
  periodic rotation (NIST guidance).
- **Acceptance criteria:**
  - [ ] Weak/common passwords are rejected with actionable guidance.
  - [ ] Repeated failures trigger progressive delays/lockout without account enumeration.
  - [ ] Reset tokens are hashed, single-use, expire and are audited.
  - [ ] Password change invalidates other sessions.
  - [ ] Admins can require a reset without seeing the password.
- **Tests/evidence:** policy unit tests, lockout integration test, reset-token replay test.
- **Docs/telemetry:** password failure metric (no usernames in logs), lockout alert.

#### GOV-11 · Scoped API tokens for automation
- **Status:** TODO · **Priority:** P2 · **Estimate:** M · **Branch:** `feature/auth-api-tokens`
- **Depends on:** GOV-01
- **Scope:** Add user/service API tokens with scopes, expiry, last-used, hashed storage and
  revocation. Use them for CI/webhook automation instead of passwords.
- **Acceptance criteria:**
  - [ ] Tokens are shown once, stored hashed and never logged.
  - [ ] Scopes map to capabilities and are enforced server-side.
  - [ ] Expired/revoked tokens fail with a clear error.
  - [ ] Last used and creator are visible; revocation is audited.
  - [ ] Token creation requires step-up.
- **Tests/evidence:** scope matrix tests, expiry/revocation tests.
- **Docs/telemetry:** token usage metric, `docs/API-TOKENS.md`.

#### GOV-12 · Ownership transfer, offboarding and permission-aware UI
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/auth-offboarding`
- **Depends on:** GOV-01, GOV-02
- **Scope:** Add explicit ownership transfer, user deactivation impact preview (owned projects,
  open assignments, approvals, tokens), reassignment workflow and a permission-aware UI that
  explains why an action is unavailable.
- **Acceptance criteria:**
  - [ ] Deactivation shows affected projects/assignments and requires reassignment choices.
  - [ ] Ownership transfer requires the target to accept or an admin to confirm.
  - [ ] A deactivated user cannot authenticate, refresh, stream or use tokens.
  - [ ] Disabled actions have an accessible reason ("Requires project maintainer").
  - [ ] The last owner/admin protections remain enforced.
- **Tests/evidence:** offboarding integration test, ownership transfer test, UI permission
  tests.
- **Docs/telemetry:** offboarding checklist, audit events.

### Phase 2 — Design system and application shell

#### UX-01 · Information architecture and heuristic audit
- **Status:** TODO · **Priority:** P1 · **Estimate:** S · **Branch:** `docs/ux-ia-audit`
- **Depends on:** BASE-04
- **Scope:** Inventory every route, screen, action and data object; map jobs-to-be-done;
  run a heuristic audit (navigation, density, consistency, feedback, error prevention,
  accessibility); produce the navigation map and a screen/task matrix.
- **Acceptance criteria:**
  - [ ] Every route and workspace tab is listed with its owner, primary job and task ID.
  - [ ] Duplicate or dead surfaces are identified (including the legacy board).
  - [ ] The navigation map matches Section 6 and the sidebar blueprint.
  - [ ] Top 20 usability defects are ranked by impact and linked to tasks.
  - [ ] The audit includes keyboard, screen-reader and 200% zoom observations.
- **Tests/evidence:** Document review with screenshots; no code change.
- **Docs/telemetry:** `docs/UX-IA.md`; linked from this plan.

#### UX-02 · Design tokens and Tailwind theme
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/ui-design-tokens`
- **Depends on:** UX-01
- **Scope:** Implement the tokens from Section 5.4 in CSS variables and the Tailwind theme:
  spacing, typography, colour semantics, radius, elevation, focus, motion, z-index, density.
  Remove ad hoc values from shared components first, then screens.
- **Acceptance criteria:**
  - [ ] Tokens are the only source of colour/spacing/type in new code.
  - [ ] A token gallery route renders every token and component state.
  - [ ] Contrast is verified for text, borders and status colours.
  - [ ] Density tokens change row/card heights without breaking layout.
  - [ ] No external font or CDN request is introduced.
- **Tests/evidence:** Visual token gallery, axe on the gallery, grep for arbitrary hex values
  in new components.
- **Docs/telemetry:** `docs/DESIGN-SYSTEM.md` token table.

#### UX-03 · Accessible primitive component library
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ui-primitives`
- **Depends on:** UX-02
- **Scope:** Build the component inventory in Section 5.7 with Vue 3 + TypeScript: button,
  input, select, combobox, checkbox, radio, switch, field, badge, tooltip, popover, menu,
  dialog, drawer, tabs, toast, banner, skeleton, empty state, pagination, breadcrumbs, avatar,
  progress, status pill, health dot, kbd.
- **Acceptance criteria:**
  - [ ] Every component has a typed API, disabled/loading/error states and a keyboard path.
  - [ ] Dialogs/drawers trap focus, close on Escape and restore focus.
  - [ ] Combobox/menu/tabs follow the ARIA Authoring Practices.
  - [ ] Components are tested with Vue Test Utils and axe.
  - [ ] No component depends on a screen-specific store or API.
- **Tests/evidence:** Unit tests per component, keyboard interaction tests, axe tests,
  visual snapshots.
- **Docs/telemetry:** Component gallery + usage notes.

#### UX-04 · Global application frame
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ui-app-frame`
- **Depends on:** UX-03
- **Scope:** Implement the 56 px top bar, 256/64 px sidebar, project switcher, create menu,
  notification bell, avatar menu, mobile drawer, skip link and collapsed-rail persistence.
- **Acceptance criteria:**
  - [ ] The sidebar is the only project navigation surface.
  - [ ] The project switcher is searchable and shows recent/pinned projects.
  - [ ] The create menu is context-aware and permission-filtered.
  - [ ] The mobile drawer traps focus and restores it on close.
  - [ ] The shell does not remount on route changes.
  - [ ] Collapsed state and scroll position persist per user.
- **Tests/evidence:** Router integration tests, keyboard tests, responsive snapshots.
- **Docs/telemetry:** Navigation map and shortcut registry updated.

#### UX-05 · Page frame, breadcrumbs and action bar
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/ui-page-frame`
- **Depends on:** UX-04
- **Scope:** Standardize the 64 px project header, 72 px page header, breadcrumbs, description,
  primary/secondary actions, 48 px toolbar and sticky save bar. Provide a `<PageFrame>` API
  used by every screen.
- **Acceptance criteria:**
  - [ ] Every project screen uses the same frame and spacing.
  - [ ] One `<h1>` per page; actions have a consistent order.
  - [ ] Breadcrumbs reflect the object hierarchy and are keyboard navigable.
  - [ ] The toolbar is optional and does not shift the content when absent.
  - [ ] Sticky offsets account for the top bar/project header.
- **Tests/evidence:** Route-level snapshots, axe, 375/768/1280/1440 screenshots.
- **Docs/telemetry:** Page frame usage guide.

#### UX-06 · Data table system
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ui-data-table`
- **Depends on:** UX-03
- **Scope:** Build a reusable table with sorting, column visibility, density, selection, bulk
  actions, sticky header, pagination/keyset paging, empty/loading/error states and optional
  virtualization. Provide a saved-view abstraction that serializes to the URL.
- **Acceptance criteria:**
  - [ ] Sorting and column choice persist in the URL for supported screens.
  - [ ] Selection supports page and "all matching" scopes with a clear count.
  - [ ] The header is sticky and the table scrolls inside its region.
  - [ ] Row heights follow the density token (40/48/56).
  - [ ] 1,000 rows remain usable with virtualization or pagination.
  - [ ] `aria-sort`, caption and keyboard navigation are correct.
- **Tests/evidence:** Component tests, performance test with 1,000 rows, axe.
- **Docs/telemetry:** Table usage guide + density rules.

#### UX-07 · Form system and validation patterns
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/ui-forms`
- **Depends on:** UX-03
- **Scope:** Standardize field layout, labels, hints, errors, required markers, validation
  timing, error summary, dirty tracking, unsaved-changes guard and optimistic save feedback.
- **Acceptance criteria:**
  - [ ] Labels are always visible; placeholders are never the only label.
  - [ ] Errors are linked with `aria-describedby` and announced.
  - [ ] The error summary links to each invalid field.
  - [ ] Dirty forms warn on route change and unload.
  - [ ] Saving states keep layout stable and prevent double submit.
- **Tests/evidence:** Form component tests, keyboard tests, axe.
- **Docs/telemetry:** Form patterns guide.

#### UX-08 · Chart system and accessible fallbacks
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ui-charts`
- **Depends on:** UX-03, UX-06
- **Scope:** Add a line chart to the existing bar/donut/burn components; standardize chart
  cards, legends, tooltips, empty/insufficient states, data-table fallback and export hooks.
  Avoid a charting dependency that ships telemetry or requires a CDN.
- **Acceptance criteria:**
  - [ ] Line, bar, donut and burn charts share tokens and interaction rules.
  - [ ] Every chart has a table toggle and an `aria-label` summary.
  - [ ] Tooltips are keyboard accessible and do not obscure the data.
  - [ ] Charts resize without layout shift.
  - [ ] No chart makes a network request at runtime.
- **Tests/evidence:** Chart unit tests, axe, visual snapshots at 3 widths.
- **Docs/telemetry:** Chart standards in `docs/DESIGN-SYSTEM.md`.

#### UX-09 · Command palette
- **Status:** TODO · **Priority:** P2 · **Estimate:** M · **Branch:** `feature/ui-command-palette`
- **Depends on:** UX-04, UX-03
- **Scope:** Implement `Ctrl/Cmd+K` with navigation, create, recent items and search results,
  permission filtering, keyboard navigation and a single navigation/action registry.
- **Acceptance criteria:**
  - [ ] Opens from every authenticated screen and restores focus on close.
  - [ ] Navigation and create actions work without a mouse.
  - [ ] Recent items and results respect permissions.
  - [ ] The active option and result count are announced.
  - [ ] No route list is duplicated outside the registry.
- **Tests/evidence:** Keyboard tests, permission tests, axe on the dialog.
- **Docs/telemetry:** Shortcut registry + user guide.

#### UX-10 · Keyboard shortcuts and shortcut help
- **Status:** TODO · **Priority:** P2 · **Estimate:** S · **Branch:** `feature/ui-keyboard-shortcuts`
- **Depends on:** UX-09
- **Scope:** Implement the shortcut map from Section 5.9, a `?` help dialog generated from the
  registry, and guards that prevent shortcuts while typing.
- **Acceptance criteria:**
  - [ ] Shortcuts work consistently across list/detail screens.
  - [ ] Help is generated from one registry and cannot drift.
  - [ ] Inputs/textareas/contenteditable are never hijacked.
  - [ ] Shortcuts are discoverable and documented.
  - [ ] Conflict handling is explicit (browser/OS shortcuts win).
- **Tests/evidence:** Keyboard event tests, help dialog snapshot.
- **Docs/telemetry:** Shortcut table in the UI and docs.

#### UX-11 · Accessibility baseline and CI gate
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `fix/ui-a11y-baseline`
- **Depends on:** UX-03..UX-05
- **Scope:** Audit the current screens against WCAG 2.2 AA; fix landmarks, headings, names,
  focus, contrast, target size, table semantics and motion. Add axe checks to the Playwright
  suite and a manual screen-reader script (NVDA/VoiceOver).
- **Acceptance criteria:**
  - [ ] Zero serious/critical axe violations on the primary flows.
  - [ ] Keyboard-only completion of login, project creation, work item creation, search and
    settings.
  - [ ] Focus order is logical and visible on every screen.
  - [ ] Screen-reader names/roles/states are correct for custom widgets.
  - [ ] 200% zoom and 320 px width are usable.
  - [ ] Reduced motion is respected.
- **Tests/evidence:** axe report, keyboard walkthrough, screen-reader notes, zoom screenshots.
- **Docs/telemetry:** `docs/ACCESSIBILITY.md` and the manual test script.

#### UX-12 · Responsive layouts and density modes
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/ui-density-responsive`
- **Depends on:** UX-06
- **Scope:** Implement comfortable/default/compact density, persisted per user, and verify the
  breakpoint matrix in Section 5.6 across all screens. Replace card grids with tables/stacked
  rows where appropriate.
- **Acceptance criteria:**
  - [ ] Density changes row/card heights without clipping or overlap.
  - [ ] 375/768/1024/1280/1440 px layouts are tested for every primary screen.
  - [ ] No page-level horizontal scroll at supported widths.
  - [ ] Tables scroll inside their region with a visible affordance.
  - [ ] The preference persists across devices.
- **Tests/evidence:** Playwright viewport matrix, screenshots, no-scroll assertions.
- **Docs/telemetry:** Density guidance and breakpoint table.

#### UX-13 · Visual regression and component gallery
- **Status:** TODO · **Priority:** P2 · **Estimate:** M · **Branch:** `test/ui-visual-regression`
- **Depends on:** UX-03, UX-11
- **Scope:** Add a component gallery route (development only) and Playwright screenshot tests
  for primitives, page frames and primary screens at fixed viewports. Store baselines in the
  repo with a size budget.
- **Acceptance criteria:**
  - [ ] The gallery renders every primitive state.
  - [ ] Screenshot tests run in CI for touched UI paths.
  - [ ] Diffs are reviewable and do not require a local Docker stack.
  - [ ] Flaky screenshots are stabilized (fonts, animations, time, random data).
  - [ ] Baselines are updated only through a reviewed PR.
- **Tests/evidence:** CI screenshot job, baseline artifacts.
- **Docs/telemetry:** Visual testing guide.

#### UX-14 · Notifications inbox and preferences
- **Status:** TODO · **Priority:** P2 · **Estimate:** L · **Branch:** `feature/ui-inbox`
- **Depends on:** UX-04, GOV-04
- **Scope:** Implement the inbox blueprint: durable notifications, unread counts, filters,
  detail, mark read/unread, snooze, preferences and deep links. Generate from domain events,
  not polling.
- **Acceptance criteria:**
  - [ ] Unread count matches the list and updates live.
  - [ ] Deep links open the object and mark read.
  - [ ] Preferences suppress channels without deleting the in-app record.
  - [ ] Notifications never leak inaccessible objects.
  - [ ] Empty/error/expired states are handled.
- **Tests/evidence:** API + UI tests, SSE test, permission test.
- **Docs/telemetry:** Notification event catalog.

#### UX-15 · Theme, high-contrast and reduced-motion support
- **Status:** TODO · **Priority:** P3 · **Estimate:** M · **Branch:** `feature/ui-themes`
- **Depends on:** UX-02, UX-11
- **Scope:** Add system/light/dark/high-contrast themes driven by tokens, persist the choice,
  and verify contrast and chart colours in every theme. Respect reduced motion.
- **Acceptance criteria:**
  - [ ] No hard-coded colours outside tokens.
  - [ ] Contrast passes in all themes.
  - [ ] Charts and status colours remain distinguishable.
  - [ ] The theme follows the OS by default and persists per user.
  - [ ] Motion is disabled/reduced when requested.
- **Tests/evidence:** Theme screenshot matrix, axe, contrast tests.
- **Docs/telemetry:** Theme tokens and usage.

#### UX-16 · Internationalization and timezone foundation
- **Status:** TODO · **Priority:** P3 · **Estimate:** L · **Branch:** `feature/i18n-foundation`
- **Depends on:** UX-05
- **Scope:** Externalize UI strings, add locale/timezone-aware date/number formatting, plural
  rules and RTL-ready layout primitives. No machine translation; start with English and one
  test locale.
- **Acceptance criteria:**
  - [ ] No user-facing string is hard-coded in components.
  - [ ] Dates/times use the user's timezone and locale, with UTC available.
  - [ ] Plurals and interpolation are handled by the i18n layer.
  - [ ] Layout survives a 30% longer test locale.
  - [ ] RTL primitives are available but not enabled until a locale requires them.
- **Tests/evidence:** String extraction check, locale screenshot, date/time tests.
- **Docs/telemetry:** `docs/I18N.md`.

### Phase 3 — Screen redesigns

Each card implements the matching blueprint in Section 6. A screen card is not complete until
its loading, empty, error, partial, permission-denied and responsive states exist.

#### UI-01 · Sign-in screen
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/ui-login`
- **Depends on:** UX-05, UX-11, GOV-06
- **Scope:** Implement the split sign-in blueprint, SSO button, show-password, caps-lock,
  environment/version badge, error handling and demo-note gating.
- **Acceptance criteria:**
  - [ ] Password managers autofill and Enter submits.
  - [ ] Invalid credentials do not enumerate users.
  - [ ] SSO is hidden when unconfigured and fully keyboard operable when configured.
  - [ ] Loading prevents double submit.
  - [ ] 375/768/1280 layouts pass; axe has no serious/critical issues.
- **Tests/evidence:** Component tests, Playwright login flow, axe.
- **Docs/telemetry:** Login success/failure metrics; no credentials in logs.

#### UI-02 · Projects portfolio
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ui-projects`
- **Depends on:** UX-06, UX-12
- **Scope:** Implement the table/card portfolio, saved views, health, search, sort, filters,
  pagination, bulk archive and create modal.
- **Acceptance criteria:**
  - [ ] Table is the default ≥1024 px; cards below; choice persists.
  - [ ] URL state survives reload and supports sharing.
  - [ ] Health links to evidence; no free-text health field.
  - [ ] 500 projects remain responsive.
  - [ ] Create/archive respect roles.
- **Tests/evidence:** Playwright create/filter/sort, performance test, axe.
- **Docs/telemetry:** Portfolio view usage; create/archive audit events.

#### UI-03 · Project overview
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ui-overview`
- **Depends on:** UX-05, UX-08, ANL-05
- **Scope:** Implement the KPI strip, delivery chain, release readiness, activity, environment
  rail, milestones and AI insights with evidence links.
- **Acceptance criteria:**
  - [ ] Every number links to its filtered evidence.
  - [ ] Empty project shows an onboarding checklist.
  - [ ] SSE updates preserve scroll/pointer position.
  - [ ] AI degrades honestly when unavailable.
  - [ ] Context rail moves below main at <1280 px.
- **Tests/evidence:** Playwright, SSE test, axe, screenshot matrix.
- **Docs/telemetry:** Overview metrics definitions.

#### UI-04 · Planning workspace
- **Status:** TODO · **Priority:** P1 · **Estimate:** XL · **Branch:** `feature/ui-planning` (split into 4a/4b)
- **Depends on:** UX-05, UX-06, UX-07, PLN-01
- **Scope:** Implement the three-pane tree/detail/context layout, tree/table/timeline views,
  inline editing, acceptance criteria, linked artifacts and activity.
- **Acceptance criteria:**
  - [ ] PRD → epic → feature → work item creation works without leaving the screen.
  - [ ] All three views share filters and URL state.
  - [ ] Context edits are optimistic with rollback.
  - [ ] Keyboard tree navigation works.
  - [ ] 500 nodes remain usable.
- **Tests/evidence:** Playwright CRUD, keyboard test, performance test, axe.
- **Docs/telemetry:** Planning object model + activity events.

#### UI-05 · Delivery board
- **Status:** TODO · **Priority:** P1 · **Estimate:** XL · **Branch:** `feature/ui-board` (split into 5a/5b)
- **Depends on:** UX-06, BRD-01, BRD-02
- **Scope:** Implement columns, WIP limits, swimlanes, quick filters, saved views, metrics,
  drag/keyboard moves, bulk actions and the detail drawer.
- **Acceptance criteria:**
  - [ ] Create/move/edit/complete works without a mouse.
  - [ ] WIP limits warn with an explanation; policy is explicit.
  - [ ] Saved views/filters are shareable.
  - [ ] 2,000 cards remain responsive.
  - [ ] The legacy board route no longer renders.
- **Tests/evidence:** Playwright drag + keyboard, performance test, axe.
- **Docs/telemetry:** Board metric definitions, move audit events.

#### UI-06 · Test lab
- **Status:** TODO · **Priority:** P1 · **Estimate:** XL · **Branch:** `feature/ui-test-lab` (split into 6a/6b/6c)
- **Depends on:** UX-05, UX-06, TST-01..TST-04
- **Scope:** Implement cases/runs/plans/coverage, the step editor, the focused run mode,
  evidence, bug creation and coverage gaps.
- **Acceptance criteria:**
  - [ ] A run can be completed and a bug filed without leaving the runner.
  - [ ] Step results/evidence persist and are auditable.
  - [ ] Keyboard shortcuts work in the runner.
  - [ ] Coverage gaps link to missing cases.
  - [ ] 1,000 cases paginate/virtualize.
- **Tests/evidence:** Playwright run flow, API tests, axe.
- **Docs/telemetry:** Test evidence model, run metrics.

#### UI-07 · Code workspace
- **Status:** TODO · **Priority:** P1 · **Estimate:** XL · **Branch:** `feature/ui-code` (split into 7a/7b/7c)
- **Depends on:** UX-05, UX-06, COD-01..COD-05, GOV-03
- **Scope:** Implement repository header, local tabs, tree/file browser, PR list, PR tabs
  (Conversation/Commits/Checks/Files), review threads, merge box and checks evidence.
- **Acceptance criteria:**
  - [ ] A reviewer sees every required check and failure reason before merge.
  - [ ] Review comments are threaded/attributable/resolvable.
  - [ ] Merge is blocked with an explicit reason when policy fails.
  - [ ] No CDN is required for syntax highlighting.
  - [ ] 10,000-file repos remain navigable.
- **Tests/evidence:** Playwright PR review/merge, API tests, performance test, axe.
- **Docs/telemetry:** Code workspace guide, review/merge metrics.

#### UI-08 · Delivery control room
- **Status:** TODO · **Priority:** P1 · **Estimate:** XL · **Branch:** `feature/ui-delivery` (split into 8a/8b)
- **Depends on:** UX-05, UX-06, DEL-01..DEL-04
- **Scope:** Implement environment cards, deployments/builds/releases/approvals tabs, filters,
  detail drawers, promotion/rollback, approvals and live updates.
- **Acceptance criteria:**
  - [ ] Every environment shows version, health, last deploy and evidence.
  - [ ] Failed deployments expose the failing job/log link and commit.
  - [ ] Promotion/rollback require capability and are audited.
  - [ ] Self-approval is impossible when policy forbids it.
  - [ ] SSE updates do not duplicate rows or lose scroll.
- **Tests/evidence:** Playwright deploy/rollback/approval, API tests, axe.
- **Docs/telemetry:** DORA metrics, deployment audit events.

#### UI-09 · Reports and analytics
- **Status:** TODO · **Priority:** P1 · **Estimate:** XL · **Branch:** `feature/ui-reports` (split into 9a/9b)
- **Depends on:** UX-08, ANL-01..ANL-06
- **Scope:** Implement the report library, filters, chart grid, line charts, chart builder,
  schedules, async export and drill-down.
- **Acceptance criteria:**
  - [ ] A line chart can be created, saved, shared and exported.
  - [ ] Every chart has a table view.
  - [ ] Scheduled reports show delivery results.
  - [ ] Filters/report selection survive reload.
  - [ ] 100k-row export does not block the API.
- **Tests/evidence:** Playwright chart builder/export, API tests, performance test, axe.
- **Docs/telemetry:** Metric catalog + report definitions.

#### UI-10 · Traceability matrix
- **Status:** TODO · **Priority:** P2 · **Estimate:** L · **Branch:** `feature/ui-traceability`
- **Depends on:** UX-06, TST-05
- **Scope:** Implement filters, matrix, evidence drawer, gap register and export.
- **Acceptance criteria:**
  - [ ] Every cell links to exact evidence.
  - [ ] Gaps are distinguishable from not-applicable.
  - [ ] Filters and "gaps only" are shareable.
  - [ ] Export includes filter context.
  - [ ] 1,000 requirements remain usable.
- **Tests/evidence:** Playwright, export test, performance test, axe.
- **Docs/telemetry:** Traceability rules + coverage definitions.

#### UI-11 · Project settings
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ui-settings`
- **Depends on:** UX-07, GOV-01, GOV-02
- **Scope:** Implement settings sub-navigation, details, members, environments, integrations,
  delivery policy, notifications, danger zone and the sticky save bar.
- **Acceptance criteria:**
  - [ ] Every setting persists and reloads exactly.
  - [ ] Role changes take effect on the next request.
  - [ ] Secrets are write-only and never rendered.
  - [ ] Destructive actions require confirmation and are audited.
  - [ ] Unsaved changes cannot be lost accidentally.
- **Tests/evidence:** Playwright settings/members/danger zone, API tests, axe.
- **Docs/telemetry:** Settings reference + audit events.

#### UI-12 · Global search
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ui-search`
- **Depends on:** UX-06, SRH-01..SRH-04
- **Scope:** Implement query bar/builder, facets, grouped results, saved/recent searches,
  highlighting, pagination and bulk actions.
- **Acceptance criteria:**
  - [ ] Saved/recent searches are server-side and survive devices.
  - [ ] Every artifact type is searchable within permissions.
  - [ ] Facet counts match results.
  - [ ] Query errors identify the token/position.
  - [ ] p95 < 300 ms on the reference dataset.
- **Tests/evidence:** Playwright search/save/facet, API tests, performance test, axe.
- **Docs/telemetry:** Query language reference + search metrics.

#### UI-13 · Profile and security
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ui-profile`
- **Depends on:** UX-07, GOV-08..GOV-11
- **Scope:** Implement identity card, Profile/Security/Sessions/Preferences tabs, avatar,
  password, MFA, tokens, session revocation and preferences.
- **Acceptance criteria:**
  - [ ] Profile changes reflect in the shell immediately.
  - [ ] Password change invalidates other sessions.
  - [ ] MFA enrolment/recovery works offline.
  - [ ] Session revocation is immediate and audited.
  - [ ] Preferences persist per user.
- **Tests/evidence:** Playwright profile/security, API tests, axe.
- **Docs/telemetry:** Account security guide.

#### UI-14 · Admin users and roles
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ui-admin-users`
- **Depends on:** UX-06, GOV-05, GOV-12
- **Scope:** Implement the user table, filters, bulk actions, invite flow, user drawer
  (Profile/Roles/Projects/Sessions/Audit), deactivation and reset.
- **Acceptance criteria:**
  - [ ] Search by name/username/email works.
  - [ ] Last-admin/self-lockout protections are enforced and visible.
  - [ ] Deactivation invalidates sessions and removes access.
  - [ ] Every admin action is audited with before/after.
  - [ ] 10,000 users paginate server-side.
- **Tests/evidence:** Playwright admin flow, API tests, performance test, axe.
- **Docs/telemetry:** Admin runbook + audit events.

#### UI-15 · Audit log
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ui-audit-log`
- **Depends on:** GOV-04, UX-06
- **Scope:** Implement filters, table, detail drawer with before/after diff, export and chain
  verification.
- **Acceptance criteria:**
  - [ ] Every privileged action appears within 5 seconds.
  - [ ] Before/after is available and redacted correctly.
  - [ ] Chain verification detects tampering.
  - [ ] Export matches filters and is itself audited.
  - [ ] No mutate/delete path exists in UI or API.
- **Tests/evidence:** Playwright audit filter/export, tamper test, axe.
- **Docs/telemetry:** Audit retention/export runbook.

#### UI-16 · System health
- **Status:** TODO · **Priority:** P2 · **Estimate:** M · **Branch:** `feature/ui-system-health`
- **Depends on:** OBS-01..OBS-05
- **Scope:** Implement service cards, dependency table, details drawer and runbook links.
- **Acceptance criteria:**
  - [ ] Every service in the topology has a card and owner.
  - [ ] Degraded/unknown/unhealthy are distinguishable.
  - [ ] No secret/connection string is exposed.
  - [ ] The page works when a dependency is down.
  - [ ] Metrics link to the exact dashboard panel.
- **Tests/evidence:** Playwright with mocked degraded states, API tests, axe.
- **Docs/telemetry:** On-call runbook.

### Phase 4 — Planning, board and test management depth

#### PLN-01 · Data integrity, constraints and optimistic locking
- **Status:** DONE · **Priority:** P0 · **Estimate:** L · **Branch:** `feature/planning-data-integrity`
- **Depends on:** BASE-05
- **Scope:** Add `version` columns and `If-Match`/`expected_version` handling to PRDs, epics,
  features, work items, test cases, runs, sprints and releases. Enforce foreign keys, cascade
  rules, unique keys and indexes. Add a schema-drift test and an expand/contract migration
  template.
- **Acceptance criteria:**
  - [x] Concurrent edits return `409 CONFLICT` with the current version. A stale `If-Match`,
    `expected_version` query parameter or body field is rejected with `409 REVISION_CONFLICT`,
    `current_version` and the current strong `ETag`; a missing token is `428
    REVISION_REQUIRED` and a malformed one is `400 INVALID_REVISION`.
  - [x] The UI reloads or merges explicitly; no silent overwrite. Board, planning, delivery and
    test mutations send the freshest stored `revision`; on `409` the workspace refreshes, the
    modal stays open and the user gets an explicit retry message.
  - [x] Orphaned child rows are impossible or explicitly soft-deleted. `ON DELETE CASCADE` and
    `SET NULL` are explicit on all 73 foreign keys; `work_items_no_self_parent` rejects direct
    self-parent writes.
  - [x] Every foreign key has an index where the query path needs it. `016_planning_revisions`
    adds the 33 missing indexes; `schema_test.go` fails if any of the 73 foreign keys lacks a
    leading index.
  - [x] Migrations follow expand/contract and are tested forward/backward. The forward script is
    idempotent and additive; `rollback/016_planning_revisions.sql` is the contract companion.
    A PGlite run applied 001–016, re-applied 016, verified the objects, applied the contract
    script and re-expanded successfully.
  - [x] A schema snapshot test fails on unintended drift. `schema_test.go` asserts migration
    order, the eight revision columns, every trigger, the self-parent check, rollback
    completeness and FK-index coverage; schema changes must update the test and rollback
    companion in the same PR.
- **Tests/evidence:** `api/internal/workspace/revision_test.go` covers token parsing
  (`"N"`, `N`, `W/"N"`, `*`, negatives, garbage), precedence (header → query → body), the
  `428`/`400` paths and conflict metric labels; `handler_tests.go` covers revisioned test-case
  and test-run mutations. `api/internal/migrate/schema_test.go` covers migration order,
  revision columns/triggers, rollback completeness and all foreign-key indexes;
  `api/internal/metrics` covers `spm_planning_conflicts_total`. The PGlite forward/rollback
  harness proved eight revisioned tables, 33 new indexes, stale update/delete rejection,
  wildcard `If-Match`, forced client revision overwritten by the trigger, self-parent rejection,
  rollback and re-expand. Full `go build ./...`, `go vet ./...`, `go test ./... -count=1`,
  OpenAPI `-check`, Redocly lint, and UI `typecheck`, `test` and `build` pass. Merged as PR #26
  at `07ecdae`.
- **Docs/telemetry:** [`docs/DATA-MODEL.md`](docs/DATA-MODEL.md) documents the hierarchy,
  cascade rules, revision semantics and index invariants;
  [`docs/MIGRATION-GUIDE.md`](docs/MIGRATION-GUIDE.md) documents expand/contract, the `016`
  walkthrough, forward/backward validation and the planned-rollback procedure.
  `spm_planning_conflicts_total{resource}` counts rejected stale writes with bounded lowercase
  resource labels and is documented in [`docs/OBSERVABILITY.md`](docs/OBSERVABILITY.md).

#### PLN-02 · Work item detail, comments, links and watchers
- **Status:** DONE · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/planning-work-item-detail`
- **Depends on:** PLN-01
- **Scope:** Build the work item detail drawer/page: title/description, acceptance criteria,
  status, owner, priority, sprint, release, labels, parent/children, links, attachments,
  comments, watchers, activity and traceability. Support optimistic updates and audit.
- **Acceptance criteria:**
  - [x] Create/edit/comment/link/watch works from the board, planning and search.
  - [x] Comments support mentions and are sanitized/escaped.
  - [x] Links are bidirectional and visible on both artifacts.
  - [x] Attachments are size/type restricted, scanned and permission-checked.
  - [x] Every change emits an activity/audit event and SSE update.
  - [x] The drawer is keyboard operable and deep-linkable.
- **Tests/evidence:** Migration `017` adds labels, watchers, mentions, attachment scan state and
  their indexes; `schema_test.go` checks the forward and contract scripts stay complete, and a
  PGlite rehearsal applied all 17 migrations, re-applied `017`, exercised label containment,
  stale-revision rejection, watcher/mention uniqueness and cascades, attachment scan checks and
  the full rollback. `handler_collaboration_test.go` covers comment sanitization, mention
  parsing (including trailing punctuation), label normalization and the link allowlist;
  `attachments_test.go` covers traversal, type/magic mismatches, active markup, EICAR, size
  limits, scanner outage and storage. UI `typecheck`, 23 Vitest tests (including mention
  parsing) and `build` pass; OpenAPI `-check` is current. Merged as PR #28 at `0c7c020` after
  all nine required checks passed and the PGlite rehearsal verified the full contract companion.
- **Docs/telemetry:** [`docs/WORK-ITEMS.md`](docs/WORK-ITEMS.md) documents every field, the
  collaboration endpoints, attachment limits/scanning/permissions, activity/audit/SSE emission
  and the deep-link/keyboard contract. Compose persists attachments in `attachments_data`.

#### PLN-03 · Sprint and release lifecycle
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/planning-sprints-releases`
- **Depends on:** PLN-01
- **Scope:** Add sprint start/close, carry-over, capacity, velocity, release scope/freeze,
  readiness, sign-off and milestone dates. Make state transitions explicit and audited.
- **Acceptance criteria:**
  - [ ] Starting a sprint requires dates and a goal; overlapping active sprints are rejected.
  - [ ] Closing a sprint moves incomplete work per policy (backlog/next sprint) with a preview.
  - [ ] Capacity/velocity use documented formulas and timezone.
  - [ ] Release freeze blocks scope changes unless an authorized exception is recorded.
  - [ ] Sign-off records the actor, evidence and timestamp.
- **Tests/evidence:** State-machine tests, carry-over integration tests, Playwright lifecycle.
- **Docs/telemetry:** Sprint/release runbook, velocity metric definition.

#### PLN-04 · Bulk import, export and templates
- **Status:** TODO · **Priority:** P2 · **Estimate:** M · **Branch:** `feature/planning-bulk-io`
- **Depends on:** PLN-01
- **Scope:** Add CSV import/export for work items, test cases and requirements with validation,
  dry-run preview, error report and idempotency; add project/PRD templates.
- **Acceptance criteria:**
  - [ ] Import validates every row and reports line/field errors without partial writes.
  - [ ] Dry-run shows create/update/skip counts.
  - [ ] Export includes filter context and stable headers.
  - [ ] Imports are permission-checked, size-limited and audited.
  - [ ] Templates create a consistent starter tree.
- **Tests/evidence:** Fixture imports, malformed CSV tests, idempotency test.
- **Docs/telemetry:** Import format reference.

#### PLN-05 · Labels, custom fields and saved filters
- **Status:** TODO · **Priority:** P2 · **Estimate:** L · **Branch:** `feature/planning-custom-fields`
- **Depends on:** PLN-01
- **Scope:** Add project-scoped labels and a small typed custom-field system (text, number,
  select, date, user) with validation, filtering and display in tables/boards. Add saved
  filters shared across planning/board/test.
- **Acceptance criteria:**
  - [ ] Labels are reusable, colored by token, and filterable.
  - [ ] Custom fields are typed, validated and permission-controlled.
  - [ ] Deleting a field preserves data or requires an explicit migration.
  - [ ] Saved filters are shareable, permission-aware and URL-backed.
  - [ ] No schema change is required to add a field value.
- **Tests/evidence:** Field validation tests, filter tests, performance test.
- **Docs/telemetry:** Custom-field administration guide.

#### BRD-01 · Saved views, WIP limits and swimlanes
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/board-saved-views`
- **Depends on:** PLN-05
- **Scope:** Add personal/shared saved views (filters, columns, swimlanes, sort), WIP limits per
  column, quick filters and view management.
- **Acceptance criteria:**
  - [ ] A view captures filters, swimlane, sort and visible fields.
  - [ ] Shared views respect project membership and can be pinned.
  - [ ] WIP limits warn and expose the policy; optional hard block is explicit.
  - [ ] Quick filters combine with facets.
  - [ ] The default view is per user and survives reload.
- **Tests/evidence:** API/UI tests, permission tests, Playwright saved view.
- **Docs/telemetry:** Board view guide.

#### BRD-02 · Drag, keyboard moves and optimistic updates
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/board-interactions`
- **Depends on:** BRD-01
- **Scope:** Implement pointer drag with drop indicators, optimistic status changes, rollback,
  keyboard moves (`Alt+arrows`), multi-select and bulk move.
- **Acceptance criteria:**
  - [ ] Drag works with mouse/touch and does not fire duplicate mutations.
  - [ ] A failed move restores the card and offers retry.
  - [ ] Keyboard moves work across columns and announce the new position.
  - [ ] Bulk move is transactional or reports per-item results.
  - [ ] WIP and permission rules are enforced server-side.
- **Tests/evidence:** Playwright drag/keyboard, API conflict tests, axe.
- **Docs/telemetry:** Move audit events + failure metric.

#### BRD-03 · Board performance and scale
- **Status:** TODO · **Priority:** P2 · **Estimate:** M · **Branch:** `perf/board-scale`
- **Depends on:** BRD-02
- **Scope:** Virtualize columns/cards, paginate or lazy-load, debounce filters, cache snapshots,
  and measure interaction latency with 2,000+ cards.
- **Acceptance criteria:**
  - [ ] Initial board render < 1.5 s on the reference dataset.
  - [ ] Drag/move interaction < 100 ms perceived.
  - [ ] Scrolling at 60 fps on a mid-range laptop.
  - [ ] Memory stays bounded after 10 minutes of SSE updates.
  - [ ] No layout shift when cards load.
- **Tests/evidence:** Performance trace, Playwright stress test, bundle analysis.
- **Docs/telemetry:** Board performance budget.

#### TST-01 · Test plans and suites
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/tests-plans-suites`
- **Depends on:** PLN-01
- **Scope:** Add plan → suite → test case hierarchy, assignment, scheduling and run creation
  from a plan/suite/filter.
- **Acceptance criteria:**
  - [ ] Plans/suites can be created, reordered and archived.
  - [ ] Cases can be added by filter or CSV without duplicates.
  - [ ] A run snapshots the case version and plan context.
  - [ ] Assignees are validated as project members.
  - [ ] Deleting a plan preserves historical runs or requires explicit confirmation.
- **Tests/evidence:** API hierarchy tests, snapshot test, Playwright plan flow.
- **Docs/telemetry:** Test plan model + run snapshot rules.

#### TST-02 · Test case versioning and step editor
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/tests-case-editor`
- **Depends on:** TST-01
- **Scope:** Add versioned test cases, an ordered step editor (action, expected, data), shared
  steps, preconditions, priority, type, feature link and change history.
- **Acceptance criteria:**
  - [ ] Editing a case creates a new version; runs keep the old version.
  - [ ] Steps reorder with keyboard and pointer; no data loss.
  - [ ] Validation requires at least one step with an action and expected result.
  - [ ] History shows who changed what and when.
  - [ ] Import/export round-trips without losing steps.
- **Tests/evidence:** Versioning tests, editor Playwright, import/export tests.
- **Docs/telemetry:** Test case versioning guide.

#### TST-03 · Focused test run execution
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/tests-run-mode`
- **Depends on:** TST-02
- **Scope:** Build the focused runner: progress, step pass/fail/blocked/skip, actual result,
  evidence upload, notes, previous/next, finish summary and resume.
- **Acceptance criteria:**
  - [ ] Keyboard shortcuts (P/F/B/S, arrows) work without a mouse.
  - [ ] Partial results save automatically and resume after reload.
  - [ ] Evidence is stored with type/size limits and linked to the step.
  - [ ] Finishing with incomplete steps requires confirmation.
  - [ ] A failed step can create a linked bug in one action.
- **Tests/evidence:** Playwright run flow, autosave test, keyboard test, upload test.
- **Docs/telemetry:** Test execution runbook + pass-rate metric.

#### TST-04 · Defect creation and traceability
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/tests-defect-links`
- **Depends on:** TST-03, PLN-02
- **Scope:** Create a bug from a failed step/run with prefilled context, evidence, environment,
  build/deployment and bidirectional links; support bulk bug creation from failures.
- **Acceptance criteria:**
  - [ ] The bug contains test case, run, step, environment and build/deployment.
  - [ ] The test result links back to the bug.
  - [ ] Duplicate prevention warns when a similar open bug exists.
  - [ ] Bulk creation reports per-item results.
  - [ ] Creating a bug is audited and emits SSE.
- **Tests/evidence:** API link tests, Playwright create-bug flow, duplicate test.
- **Docs/telemetry:** Defect workflow + traceability rules.

#### TST-05 · Automated result ingestion and coverage
- **Status:** TODO · **Priority:** P2 · **Estimate:** L · **Branch:** `feature/tests-automation-ingest`
- **Depends on:** TST-01, DEL-01
- **Scope:** Ingest JUnit XML (and a generic JSON format) from CI, map results to test cases,
  detect flaky tests, and compute requirement/feature coverage. Add a coverage matrix and gap
  register.
- **Acceptance criteria:**
  - [ ] JUnit XML import maps by suite/classname/name and reports unmatched tests.
  - [ ] Duplicate ingestion is idempotent by run ID/commit.
  - [ ] Flaky detection uses pass/fail history over a configurable window.
  - [ ] Coverage distinguishes covered, partial, gap and not-applicable.
  - [ ] The matrix links to the exact evidence.
- **Tests/evidence:** XML fixture tests, idempotency test, flaky history test, Playwright matrix.
- **Docs/telemetry:** Ingestion format, coverage formula, flaky metric.

### Phase 5 — Code workspace and automated delivery

#### COD-01 · Repository inventory and synchronization
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/code-repo-sync`
- **Depends on:** GOV-01
- **Scope:** Synchronize repositories, branches, commits, tags and default branch from Gitea;
  add pagination, caching, last-sync status and manual reconciliation (audited).
- **Acceptance criteria:**
  - [ ] Repository list/tree/branches/commits load from the provider with pagination.
  - [ ] Default branch and protection state are stored and refreshed.
  - [ ] Sync is idempotent and records last success/error.
  - [ ] A deleted/renamed repo is detected and surfaced, not silently stale.
  - [ ] Large histories paginate; no unbounded query.
- **Tests/evidence:** Mock Gitea integration tests, pagination tests, sync idempotency.
- **Docs/telemetry:** Sync runbook + provider latency metric.

#### COD-02 · Code browser, blame and history
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/code-browser`
- **Depends on:** COD-01
- **Scope:** Build the tree/file browser with offline syntax highlighting, raw/download/copy,
  blame, line permalinks, history and large/binary file guards.
- **Acceptance criteria:**
  - [ ] No external CDN/font; highlighting works offline.
  - [ ] Files > 1 MB show a guarded viewer, not a frozen page.
  - [ ] Binary files show metadata and download.
  - [ ] Blame shows author/time and supports permalinks.
  - [ ] 10,000-file trees load lazily and remain navigable.
- **Tests/evidence:** Fixture repos, performance test, Playwright tree/file/blame.
- **Docs/telemetry:** Code browser guide.

#### COD-03 · Pull request list, detail and checks
- **Status:** TODO · **Priority:** P1 · **Estimate:** XL · **Branch:** `feature/code-prs` (split into 3a/3b)
- **Depends on:** COD-01
- **Scope:** Build the PR list and detail tabs (Conversation, Commits, Checks, Files changed),
  check runs/jobs/steps, logs links, required badges and status mapping.
- **Acceptance criteria:**
  - [ ] PR list filters by state/author/reviewer/label and paginates.
  - [ ] Checks show required/optional, status, duration and failure reason.
  - [ ] A failed check links to the job/log and the commit.
  - [ ] Files changed supports unified/split, collapse and viewed state.
  - [ ] The conversation timeline is complete and attributable.
- **Tests/evidence:** Mock/provider tests, Playwright PR/checks, performance test.
- **Docs/telemetry:** PR/check status mapping.

#### COD-04 · Review threads, reviewers and assignments
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/code-review-threads`
- **Depends on:** COD-03, GOV-03
- **Scope:** Add reviewer assignment (users/teams), review threads with line anchors, resolve/
  unresolve, suggestions, stale markers and CODEOWNERS-driven required reviewers.
- **Acceptance criteria:**
  - [ ] Reviewers can be requested/removed and see their queue.
  - [ ] Threads anchor to file/line/commit and survive rebases as stale where appropriate.
  - [ ] Resolve/unresolve is permission-checked and audited.
  - [ ] CODEOWNERS requires the matching owner when policy enables it.
  - [ ] The author cannot approve their own PR.
- **Tests/evidence:** API thread tests, stale-anchor tests, CODEOWNERS tests, Playwright review.
- **Docs/telemetry:** Review workflow + review latency metric.

#### COD-05 · Merge box, policies and merge methods
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/code-merge-policy`
- **Depends on:** COD-04, TST-05
- **Scope:** Implement the merge box with approvals, checks, conflicts, branch freshness,
  merge/squash/rebase policy, delete-branch and post-merge state transitions.
- **Acceptance criteria:**
  - [ ] The merge button is disabled with an explicit reason until all policies pass.
  - [ ] Merge method is enforced by project policy; unsupported methods are hidden.
  - [ ] Conflicts are detected before merge and explained.
  - [ ] Post-merge work item/branch/build/deployment links update correctly.
  - [ ] Merge is idempotent and audited; a repeated request does not double-merge.
- **Tests/evidence:** Policy matrix tests, conflict test, idempotency test, Playwright merge.
- **Docs/telemetry:** Merge policy reference + merge metric.

#### COD-06 · Branch protection synchronization and drift
- **Status:** TODO · **Priority:** P2 · **Estimate:** M · **Branch:** `feature/code-branch-protection`
- **Depends on:** COD-05
- **Scope:** Read and display Gitea branch protection; detect drift against project policy;
  offer a reviewed, audited reconciliation action. Do not silently overwrite provider rules.
- **Acceptance criteria:**
  - [ ] Protected branches and required checks are visible.
  - [ ] Drift is shown with the exact difference.
  - [ ] Reconciliation requires `settings.write` and is audited.
  - [ ] A provider failure leaves the last known state marked stale.
  - [ ] No secrets/tokens are exposed in the UI.
- **Tests/evidence:** Mock drift tests, reconciliation test, Playwright drift view.
- **Docs/telemetry:** Branch protection runbook.

#### COD-07 · Code search, blame performance and large repositories
- **Status:** TODO · **Priority:** P2 · **Estimate:** L · **Branch:** `feature/code-search`
- **Depends on:** COD-02
- **Scope:** Add permission-aware code search (filename/content), result highlighting, filters
  (repo/branch/path/language), pagination and a performance budget for large repos.
- **Acceptance criteria:**
  - [ ] Search never returns code from a repository the user cannot read.
  - [ ] Results show path, line, branch and a safe snippet.
  - [ ] Queries are bounded and time-limited.
  - [ ] p95 < 500 ms on the reference index.
  - [ ] No external search SaaS is required.
- **Tests/evidence:** Permission tests, performance test, Playwright search.
- **Docs/telemetry:** Code search index/runbook.

#### COD-08 · Webhook signature, replay and idempotency hardening
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/webhook-hardening`
- **Depends on:** GOV-01
- **Scope:** Enforce HMAC/bearer verification with constant-time comparison, timestamp/replay
  windows, idempotency keys, delivery deduplication, signature rotation and dead-letter
  handling for Gitea, Argo CD and generic providers.
- **Acceptance criteria:**
  - [ ] Invalid/missing signatures are rejected and rate-limited.
  - [ ] Replayed deliveries are ignored or return the original result.
  - [ ] Duplicate event IDs do not create duplicate builds/deployments.
  - [ ] Secret rotation supports an overlap window.
  - [ ] Failed deliveries are visible, retryable and audited.
- **Tests/evidence:** Signature/replay/idempotency tests, rotation test, DLQ test.
- **Docs/telemetry:** Webhook security runbook + delivery metrics.

#### DEL-01 · Gitea Actions ingestion and build evidence
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/delivery-gitea-actions`
- **Depends on:** COD-08
- **Scope:** Ingest workflow runs, jobs, steps, artifacts, logs links, durations, commit/PR and
  trigger identity. Model build status transitions and link to work items.
- **Acceptance criteria:**
  - [ ] Builds are created/updated only from provider evidence, not manual entry.
  - [ ] Jobs/steps/status/duration/log links are visible.
  - [ ] Re-runs update the same build identity or create a linked attempt.
  - [ ] Builds link to commit, PR, work item and deployment.
  - [ ] Stale/in-flight builds reconcile after restart.
- **Tests/evidence:** Provider fixtures, reconciliation test, Playwright build detail.
- **Docs/telemetry:** Build model + ingestion runbook.

#### DEL-02 · Argo CD reconciliation and health
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/delivery-argocd`
- **Depends on:** COD-08
- **Scope:** Reconcile Argo CD applications, sync status, health, revision, history and drift;
  link deployments to environments, commits, builds and work items. Support webhook and
  polling reconciliation.
- **Acceptance criteria:**
  - [ ] Environment shows app sync/health/revision/history.
  - [ ] Drift and degraded health are visible with evidence.
  - [ ] Reconciliation is idempotent and survives provider downtime.
  - [ ] Deployment history links to the exact revision/manifest.
  - [ ] No manual deployment entry is the normal path.
- **Tests/evidence:** Mock Argo tests, reconciliation test, Playwright delivery.
- **Docs/telemetry:** Argo runbook + sync/health metrics.

#### DEL-03 · Promotion, approvals and rollback
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/delivery-promotion-rollback`
- **Depends on:** DEL-02, GOV-03
- **Scope:** Implement promotion between environments, approval policies (roles, quorum,
  separation of duties), freeze windows, rollback to a previous revision and full audit.
- **Acceptance criteria:**
  - [ ] Promotion shows the version diff and policy checks.
  - [ ] Approvals use real identities; self-approval is blocked when policy requires it.
  - [ ] Freeze windows block promotion unless an audited exception is granted.
  - [ ] Rollback requires `delivery.rollback`, states impact and records the outcome.
  - [ ] A failed operation leaves the environment in a known, reported state.
- **Tests/evidence:** Policy/approval tests, rollback integration test, Playwright flow.
- **Docs/telemetry:** Deployment approval/rollback runbook.

#### DEL-04 · DORA metrics, SSE and outbox reliability
- **Status:** TODO · **Priority:** P2 · **Estimate:** L · **Branch:** `feature/delivery-dora-outbox`
- **Depends on:** DEL-01..DEL-03
- **Scope:** Compute deployment frequency, lead time, change failure rate and MTTR from real
  events; publish SSE updates; add a transactional outbox with retries and DLQ for events.
- **Acceptance criteria:**
  - [ ] DORA metrics have documented formulas, timezone and data window.
  - [ ] Metrics reconcile with the underlying builds/deployments.
  - [ ] SSE updates are ordered, deduplicated and recover after reconnect.
  - [ ] Events survive a NATS restart via the outbox.
  - [ ] Failed events go to a DLQ with an operator retry action.
- **Tests/evidence:** Metric formula tests, outbox replay test, SSE reconnect test.
- **Docs/telemetry:** DORA definitions + dashboard.

### Phase 6 — Search, analytics and reporting

#### SRH-01 · Query language v2 and field registry
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/search-query-v2`
- **Depends on:** BASE-05
- **Scope:** Formalize the JQL-style grammar (boolean operators, grouping, negation, ranges,
  relative dates, quoted strings), a typed field registry, validation with token positions,
  query limits and stable error codes. Share the registry with the UI builder.
- **Acceptance criteria:**
  - [ ] Grammar is documented with examples and precedence.
  - [ ] Invalid queries return a token, position and human-readable reason.
  - [ ] Field/operator/type mismatches are rejected before hitting the database.
  - [ ] Relative dates resolve in the user's timezone with a documented "now".
  - [ ] Query complexity is bounded (terms, nesting, result window).
  - [ ] The UI query builder and text mode produce equivalent ASTs.
- **Tests/evidence:** Parser table tests, fuzz tests, golden AST tests, UI builder tests.
- **Docs/telemetry:** `docs/SEARCH-QUERY.md`, query error metric.

#### SRH-02 · Search across every artifact type
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/search-all-artifacts`
- **Depends on:** SRH-01, GOV-01
- **Scope:** Index and search projects, PRDs, epics, features, work items, test cases, test
  runs, repositories, commits, PRs, builds, deployments, comments and audit (permission-
  filtered). Add result normalization, highlighting and deep links.
- **Acceptance criteria:**
  - [ ] Every artifact type has a result model, snippet and deep link.
  - [ ] Results never include objects outside the user's capabilities.
  - [ ] Comments and audit results are opt-in/filtered to avoid noise.
  - [ ] Deleted/archived objects are excluded or clearly marked.
  - [ ] The result count matches the grouped counts.
- **Tests/evidence:** Per-type API tests, permission matrix tests, deep-link tests.
- **Docs/telemetry:** Indexed fields and freshness.

#### SRH-03 · Server-side saved searches, recent and sharing
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/search-saved-views`
- **Depends on:** SRH-01
- **Scope:** Replace `localStorage` saved searches with server-side definitions: name, query,
  filters, visibility (private/project/shared), pin, owner, timestamps. Add recent searches
  (last 10) and sharing.
- **Acceptance criteria:**
  - [ ] Saved/recent searches survive device changes and logout/login.
  - [ ] Sharing respects project membership; a shared query never leaks inaccessible results.
  - [ ] Duplicate names are handled deterministically.
  - [ ] Deletion/rename is permission-checked and audited.
  - [ ] The UI migrates or discards old `localStorage` entries cleanly.
- **Tests/evidence:** API CRUD/sharing tests, migration test, Playwright save/apply.
- **Docs/telemetry:** Saved-view model + usage metric.

#### SRH-04 · Facets, counts and search performance
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/search-facets-perf`
- **Depends on:** SRH-02
- **Scope:** Add faceted counts (project, type, status, priority, assignee, sprint, release,
  date), multi-select, "clear all", keyset pagination and PostgreSQL FTS/trigram indexes.
- **Acceptance criteria:**
  - [ ] Facet counts match the filtered result set.
  - [ ] Facets are permission-aware.
  - [ ] p95 < 300 ms for 100k artifacts on the reference dataset.
  - [ ] Keyset pagination does not skip/duplicate rows under concurrent updates.
  - [ ] Query plans show index usage; no sequential scan on the hot path.
- **Tests/evidence:** Load test, `EXPLAIN` evidence, facet consistency test.
- **Docs/telemetry:** Search SLO + slow-query log.

#### SRH-05 · Search relevance, highlighting and zero-result recovery
- **Status:** TODO · **Priority:** P2 · **Estimate:** M · **Branch:** `feature/search-relevance`
- **Depends on:** SRH-02
- **Scope:** Tune ranking (exact key > title > body > recency), safe highlighting, synonyms
  for common terms and a zero-result recovery panel (remove facets, broader terms, search all
  projects).
- **Acceptance criteria:**
  - [ ] Exact key matches rank first.
  - [ ] Highlighting is escaped and cannot inject HTML.
  - [ ] Zero results suggest concrete next actions.
  - [ ] Ranking is covered by golden queries.
  - [ ] No result reveals a title the user cannot access.
- **Tests/evidence:** Golden ranking tests, XSS test, Playwright zero-result.
- **Docs/telemetry:** Relevance tuning notes.

#### ANL-01 · Line chart and configurable chart builder
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/analytics-line-builder`
- **Depends on:** UX-08, SRH-01
- **Scope:** Add line/area charts and a chart builder (metric, dimension, aggregation, filters,
  compare, series limit, sort) with live preview, save and duplicate.
- **Acceptance criteria:**
  - [ ] Line charts support time series, multiple series (≤ 4) and null gaps.
  - [ ] The builder validates metric/dimension compatibility.
  - [ ] A saved chart reopens with the same definition and permissions.
  - [ ] Charts render from a shared metric API, not client-side aggregation of raw rows.
  - [ ] Every chart has a table fallback.
- **Tests/evidence:** Builder tests, chart rendering tests, API tests, axe.
- **Docs/telemetry:** Metric/dimension catalog.

#### ANL-02 · Report definitions and scheduled delivery
- **Status:** TODO · **Priority:** P2 · **Estimate:** L · **Branch:** `feature/analytics-scheduled-reports`
- **Depends on:** ANL-01
- **Scope:** Add report definitions (charts/tables), schedules (cron, timezone), recipients,
  format and delivery channel (offline SMTP/webhook), run history, retries and pause/resume.
- **Acceptance criteria:**
  - [ ] Schedules use the user's timezone and handle DST correctly.
  - [ ] Each run records status, duration, artifact link and error.
  - [ ] Failures retry with backoff and are visible to the owner.
  - [ ] Recipients are permission-checked; a report never includes inaccessible data.
  - [ ] Pausing/deleting a schedule is audited.
- **Tests/evidence:** Scheduler tests with a fake clock, DST tests, delivery tests.
- **Docs/telemetry:** Scheduling runbook + delivery metrics.

#### ANL-03 · Asynchronous export jobs
- **Status:** TODO · **Priority:** P2 · **Estimate:** M · **Branch:** `feature/analytics-async-export`
- **Depends on:** ANL-02
- **Scope:** Move large CSV/JSON/PDF exports to background jobs with progress, expiry,
  download authorization and audit. Keep small exports synchronous.
- **Acceptance criteria:**
  - [ ] Exports > 5,000 rows are queued and do not block the request.
  - [ ] Job status/progress is visible and survives reload.
  - [ ] Download links expire and are permission-checked.
  - [ ] Export content matches the active filters and includes context metadata.
  - [ ] Every export is audited (who, what, when, rows).
- **Tests/evidence:** Worker tests, large-dataset test, authorization test.
- **Docs/telemetry:** Export job metrics.

#### ANL-04 · Metric catalog, timezone and semantic consistency
- **Status:** TODO · **Priority:** P2 · **Estimate:** M · **Branch:** `feature/analytics-metric-catalog`
- **Depends on:** ANL-01
- **Scope:** Define every metric once (name, description, formula, source, grain, timezone,
  owner, freshness) and serve it through one API. Replace ad hoc calculations in the UI.
- **Acceptance criteria:**
  - [ ] Every chart/report uses a catalog metric, not a bespoke query.
  - [ ] Formulas and denominators are documented and tested.
  - [ ] Timezone/day-boundary behavior is explicit and consistent.
  - [ ] Metric versioning handles definition changes.
  - [ ] The UI shows the definition in a tooltip/info panel.
- **Tests/evidence:** Formula unit tests, API contract tests, cross-screen consistency test.
- **Docs/telemetry:** `docs/METRICS.md` with owners.

#### ANL-05 · Dashboards, drill-down and cross-filtering
- **Status:** TODO · **Priority:** P2 · **Estimate:** L · **Branch:** `feature/analytics-dashboards`
- **Depends on:** ANL-04
- **Scope:** Add the global Insights dashboard and project report dashboards with filters,
  drill-down to artifact lists, cross-filtering and shareable URL state.
- **Acceptance criteria:**
  - [ ] A chart segment click opens the exact filtered list.
  - [ ] Cross-filtering updates compatible cards and is reversible.
  - [ ] Dashboard state is shareable via URL.
  - [ ] Dashboards load within the performance budget.
  - [ ] Permission filtering applies before aggregation.
- **Tests/evidence:** Playwright drill-down, URL state tests, performance test.
- **Docs/telemetry:** Dashboard catalog.

#### ANL-06 · Materialized views, freshness and scale
- **Status:** TODO · **Priority:** P2 · **Estimate:** L · **Branch:** `perf/analytics-materialized-views`
- **Depends on:** ANL-04
- **Scope:** Add materialized views/rollups for expensive metrics, incremental refresh,
  freshness timestamps and refresh observability.
- **Acceptance criteria:**
  - [ ] Expensive reports no longer scan raw tables on every request.
  - [ ] Freshness is visible and bounded by the documented SLO.
  - [ ] Refresh is incremental and idempotent.
  - [ ] A failed refresh keeps the last good data and alerts.
  - [ ] Backfills are resumable and audited.
- **Tests/evidence:** Refresh tests, freshness test, query-plan comparison.
- **Docs/telemetry:** Refresh runbook + lag metric.

### Phase 7 — AI: semantic retrieval, RAG and assistance

#### AI-01 · Real local embeddings
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ai-embeddings`
- **Depends on:** BASE-04
- **Scope:** Replace TF-IDF as the primary retrieval path with a CPU-only embedding model
  (ONNX/sentence-transformers), packaged for offline install with checksum, license and
  version metadata. Keep lexical retrieval as a fallback.
- **Acceptance criteria:**
  - [ ] The model runs CPU-only, offline, with no runtime download.
  - [ ] Model version/checksum/license are recorded and health-exposed.
  - [ ] Embedding quality is measured against a golden query set.
  - [ ] The image size and cold-start latency are documented and budgeted.
  - [ ] Lexical fallback works when the model is unavailable.
- **Tests/evidence:** Embedding quality benchmark, health test, offline test, latency test.
- **Docs/telemetry:** `docs/AI.md`, model provisioning guide.

#### AI-02 · Persistent vector index with incremental updates
- **Status:** TODO · **Priority:** P0 · **Estimate:** L · **Branch:** `feature/ai-vector-index`
- **Depends on:** AI-01
- **Scope:** Build a persistent, project-scoped vector index for SPM artifacts with incremental
  upsert/delete, rebuild, versioning and freshness metrics. Do not depend on an external
  vector SaaS.
- **Acceptance criteria:**
  - [ ] Index updates within the documented freshness SLO after a mutation.
  - [ ] Deletes/archives remove vectors; rebuild is deterministic.
  - [ ] Index version is compatible with the model version.
  - [ ] A corrupted index can be rebuilt without data loss.
  - [ ] Index size/staleness are observable.
- **Tests/evidence:** Upsert/delete tests, rebuild test, freshness test, corruption recovery.
- **Docs/telemetry:** Index runbook + freshness/size metrics.

#### AI-03 · Permission-aware retrieval
- **Status:** TODO · **Priority:** P0 · **Estimate:** M · **Branch:** `fix/ai-permission-retrieval`
- **Depends on:** AI-02, GOV-01
- **Scope:** Enforce authorization before retrieval and again before answer assembly. Never
  send documents, snippets or citations from projects the user cannot read.
- **Acceptance criteria:**
  - [ ] Cross-project retrieval is impossible for non-members.
  - [ ] Revoked membership takes effect on the next query.
  - [ ] Citations link only to accessible objects.
  - [ ] The AI service receives only authorized context.
  - [ ] Permission-filtered tests cover every artifact type.
- **Tests/evidence:** Adversarial retrieval tests, revocation test, citation access test.
- **Docs/telemetry:** AI security model + denied-retrieval metric.

#### AI-04 · Grounded RAG with citations and refusal
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ai-grounded-rag`
- **Depends on:** AI-03
- **Scope:** Answer only from retrieved, authorized context; return citations, confidence and
  an explicit "insufficient evidence" response when grounding is weak. Separate retrieval,
  ranking and generation for testability.
- **Acceptance criteria:**
  - [ ] Every factual answer includes citations to accessible evidence.
  - [ ] Unsupported questions return a refusal with suggested next steps.
  - [ ] The answer does not invent keys, dates or statuses.
  - [ ] Confidence is calibrated against the evaluation set.
  - [ ] The LLM path is optional; the extractive path remains honest.
- **Tests/evidence:** Groundedness eval, citation tests, refusal tests, hallucination audit.
- **Docs/telemetry:** RAG behavior + evaluation report.

#### AI-05 · Suggestions in planning, tests, code and delivery
- **Status:** TODO · **Priority:** P2 · **Estimate:** L · **Branch:** `feature/ai-context-suggestions`
- **Depends on:** AI-04
- **Scope:** Add context-aware suggestion cards: duplicate work detection, missing acceptance
  criteria, test coverage gaps, risky PRs, failing-check triage and release risk. Every
  suggestion explains why and links to evidence.
- **Acceptance criteria:**
  - [ ] Suggestions appear where the work happens, not only on Overview.
  - [ ] Each card has a "why" with evidence links and accept/dismiss.
  - [ ] Dismissals are remembered per user/object.
  - [ ] No suggestion blocks a workflow if the AI service is down.
  - [ ] Suggestion acceptance rate is measured.
- **Tests/evidence:** UI tests, API tests, degradation test, feedback metrics.
- **Docs/telemetry:** Suggestion catalog + acceptance metric.

#### AI-06 · Offline evaluation and feedback loop
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/ai-evaluation`
- **Depends on:** AI-04
- **Scope:** Build a versioned evaluation set (queries, expected documents, grounded answers),
  offline metrics (precision@k, recall, MRR, groundedness, citation validity), regression
  gates and user feedback capture.
- **Acceptance criteria:**
  - [ ] Evaluation runs offline and in CI with deterministic results.
  - [ ] A model/index/prompt change that regresses metrics fails the PR.
  - [ ] Feedback (helpful/not, reason) is stored and reviewed.
  - [ ] Metrics are reported by project and artifact type.
  - [ ] No customer data is required for the golden set.
- **Tests/evidence:** Evaluation harness, regression report, feedback tests.
- **Docs/telemetry:** `docs/AI-EVALS.md`, quality dashboard.

#### AI-07 · Prompt injection, PII and output safety
- **Status:** TODO · **Priority:** P0 · **Estimate:** M · **Branch:** `fix/ai-safety`
- **Depends on:** AI-04
- **Scope:** Treat retrieved content as untrusted; delimit and sanitize instructions, block
  tool/action execution from model output, redact PII/secrets before embedding/logging, and
  sanitize rendered Markdown/HTML.
- **Acceptance criteria:**
  - [ ] Injected instructions in a work item cannot change system behavior or trigger actions.
  - [ ] Secrets/PII are redacted before indexing and logging.
  - [ ] Model output is rendered as text/safe Markdown; no raw HTML.
  - [ ] Safety tests cover prompt injection, data exfiltration and malicious links.
  - [ ] A security review signs off before the LLM path is enabled by default.
- **Tests/evidence:** Adversarial prompt suite, redaction tests, XSS tests.
- **Docs/telemetry:** AI threat model + incident runbook.

#### AI-08 · Model lifecycle, offline provisioning and rollback
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/ai-model-lifecycle`
- **Depends on:** AI-01
- **Scope:** Version models, verify checksums/signatures, mirror into the air-gapped registry,
  expose health/version, support rollback and pin the model per deployment.
- **Acceptance criteria:**
  - [ ] A model upgrade is a reviewed, reversible deployment.
  - [ ] Checksums/signatures are verified before load.
  - [ ] Health shows model name/version/mode and readiness.
  - [ ] Rollback restores the previous model/index compatibility.
  - [ ] No runtime download or external license check.
- **Tests/evidence:** Provisioning test, checksum failure test, rollback test.
- **Docs/telemetry:** Model registry + air-gap mirroring steps.

#### AI-09 · AI service resilience and degradation
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `fix/ai-resilience`
- **Depends on:** AI-04
- **Scope:** Add timeouts, circuit breaking, bounded concurrency, request size limits, retries
  with jitter and graceful fallback when the AI service is slow/down.
- **Acceptance criteria:**
  - [ ] API requests to AI time out within the documented budget.
  - [ ] A circuit breaker prevents cascading failures.
  - [ ] The UI shows "AI unavailable" without blocking the page.
  - [ ] Concurrency/size limits protect the CPU-only service.
  - [ ] Metrics/alerts cover latency, errors and saturation.
- **Tests/evidence:** Timeout/circuit tests, load test, degradation test.
- **Docs/telemetry:** AI SLO + runbook.

### Phase 8 — Observability, operations and hardening

#### OBS-01 · Prometheus metrics endpoint
- **Status:** DONE · **Priority:** P0 · **Estimate:** M · **Branch:** `feature/observability-metrics`
- **Depends on:** BASE-04
- **Scope:** Implement `/metrics` in Prometheus format with RED metrics for HTTP, dependency
  health/latency, Go runtime, DB pool, NATS lag, SSE connections, audit write failures, AI
  latency and build/deployment reconciliation. Protect it from public exposure.
- **Acceptance criteria:**
  - [x] `/metrics` returns valid Prometheus text and is scraped successfully. The endpoint is
    served by a separate internal listener (`METRICS_ADDR`); Compose enables it on port 9090
    without publishing the port, and `METRICS_TOKEN` optionally requires a bearer token.
  - [x] Request metrics include route template, method, status and duration; raw identifiers are
    never used as labels and unmatched paths collapse to `unmatched`.
  - [x] DB, NATS, Gitea, Argo CD and AI dependency metrics exist. NATS exposes connectivity,
    subscriptions and message/byte counters; consumer lag is not exposed because the API uses
    core NATS subjects with no durable consumer backlog to measure.
  - [x] The endpoint is internal-only (separate, unpublished port) and can additionally require
    a bearer token. It is absent from the public router and `docs/openapi.yaml`.
  - [x] README and `docs/ARCHITECTURE.md` now describe the endpoint, its exposure and the metric
    contract instead of claiming metrics do not exist.
- **Tests/evidence:** `api/internal/metrics` tests cover the scrape output, build info, token
  auth, route-template labels, unmatched routes, SSE gauge, AI/dependency/reconciliation
  counters and nil-registry safety; `api/internal/sse` tests the connection gauge. The metric
  catalog and cardinality review are in `docs/OBSERVABILITY.md`.
- **Docs/telemetry:** `docs/OBSERVABILITY.md`, `/metrics` collectors and the `spm_build_info`
  gauge. Audit write-failure metrics arrive with the `GOV-04` audit subsystem as
  `spm_audit_writes_total{outcome="failure"}`; exporting a counter that nothing increments would
  be a false claim.

#### OBS-02 · OpenTelemetry tracing
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/observability-tracing`
- **Depends on:** OBS-01
- **Scope:** Add OTel traces for inbound HTTP, DB queries, NATS publish/consume, outbound
  Gitea/Argo/AI calls and background jobs; propagate W3C context; sample intelligently.
- **Acceptance criteria:**
  - [ ] A request produces a trace spanning API, DB and outbound calls.
  - [ ] NATS consumers link to the producer trace.
  - [ ] Sensitive values are not recorded as attributes.
  - [ ] Sampling is configurable; errors are always sampled.
  - [ ] Trace IDs appear in logs and error responses (request ID).
- **Tests/evidence:** Trace export test, propagation test, attribute redaction review.
- **Docs/telemetry:** Tracing setup + span naming.

#### OBS-03 · Structured logs, correlation and redaction
- **Status:** TODO · **Priority:** P2 · **Estimate:** M · **Branch:** `feature/observability-logs`
- **Depends on:** OBS-01
- **Scope:** Standardize JSON logs with timestamp, level, service, version, request ID, trace
  ID, actor/project where safe, route, status and duration. Add redaction for tokens,
  passwords, cookies, authorization headers and PII.
- **Acceptance criteria:**
  - [ ] Every request log has a request ID and trace ID.
  - [ ] Secrets/PII are redacted by a central encoder, not per call site.
  - [ ] Log levels are consistent and configurable.
  - [ ] Logs are shipped/retained per policy.
  - [ ] A test proves known secret patterns never appear.
- **Tests/evidence:** Redaction tests, log schema test, retention test.
- **Docs/telemetry:** Logging standard + retention policy.

#### OBS-04 · Dashboards, alerts and runbooks
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/observability-dashboards`
- **Depends on:** OBS-01, OBS-02
- **Scope:** Provision Grafana dashboards (API, DB, NATS, SSE, AI, delivery, audit) and
  Alertmanager rules with runbook links, severity and routing. No dashboard that requires
  manual assembly.
- **Acceptance criteria:**
  - [ ] Dashboards are versioned as code and provision automatically.
  - [ ] Alerts have owner, severity, runbook and a tested firing path.
  - [ ] No alert is based on a metric that does not exist.
  - [ ] Dashboards show the golden signals and dependency health.
  - [ ] A synthetic failure triggers the expected alert in staging.
- **Tests/evidence:** Provisioning test, alert firing test, dashboard review.
- **Docs/telemetry:** Runbook index + on-call guide.

#### OBS-05 · SLOs, error budgets and capacity signals
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/observability-slos`
- **Depends on:** OBS-04
- **Scope:** Define SLOs for availability, API latency, search latency, SSE freshness, delivery
  ingestion freshness and AI latency; implement error-budget burn alerts and capacity signals.
- **Acceptance criteria:**
  - [ ] Each SLO has a metric, target, window and owner.
  - [ ] Burn-rate alerts fire before the budget is exhausted.
  - [ ] Error budget policy is documented (freeze, review, escalation).
  - [ ] Capacity signals cover DB connections, NATS lag, disk, CPU and AI queue.
  - [ ] SLO reports are reviewed at each milestone.
- **Tests/evidence:** SLO queries, burn-rate simulation, review notes.
- **Docs/telemetry:** `docs/SLO.md`.

#### OBS-06 · Telemetry privacy and retention
- **Status:** TODO · **Priority:** P2 · **Estimate:** S · **Branch:** `docs/telemetry-privacy`
- **Depends on:** OBS-03
- **Scope:** Classify telemetry data, document retention, prohibit customer content/PII in
  metrics/traces/logs, and add a review checklist for new instrumentation.
- **Acceptance criteria:**
  - [ ] Every signal has a classification and retention period.
  - [ ] High-cardinality identifiers are excluded from metrics.
  - [ ] Logs/traces have a documented access policy.
  - [ ] New instrumentation requires a privacy review.
  - [ ] Air-gapped installs can disable export entirely.
- **Tests/evidence:** Data inventory review, sample audit.
- **Docs/telemetry:** `docs/TELEMETRY-PRIVACY.md`.

#### OPS-01 · Backup, restore and verification drills
- **Status:** TODO · **Priority:** P0 · **Estimate:** L · **Branch:** `feature/ops-backup-restore`
- **Depends on:** OPS-09
- **Scope:** Make backup/restore production-grade for PostgreSQL, Gitea, NATS, Argo CD and
  configuration. Encrypt backups, store off-host, verify integrity, document RPO/RTO, and run
  scheduled restore drills into an isolated environment.
- **Acceptance criteria:**
  - [ ] Backups run on schedule and on demand; failures alert.
  - [ ] Restore into a clean environment reproduces data and passes integrity checks.
  - [ ] RPO ≤ 15 minutes (WAL/continuous archiving) and RTO ≤ 2 hours are demonstrated.
  - [ ] Encryption keys are managed separately from backups.
  - [ ] A drill report records date, duration, gaps and owners.
- **Tests/evidence:** Restore drill, checksum/integrity test, timed RTO measurement.
- **Docs/telemetry:** `docs/BACKUP-RESTORE.md`, backup age/failure metrics.

#### OPS-02 · Air-gapped installation and upgrade runbook
- **Status:** TODO · **Priority:** P0 · **Estimate:** L · **Branch:** `docs/airgapped-runbook`
- **Depends on:** OPS-01, OPS-07
- **Scope:** Write and verify the missing air-gapped runbook: image/model mirroring, registry,
  checksums/signatures, host prerequisites, install, upgrade, rollback, troubleshooting and
  offline license/telemetry behavior.
- **Acceptance criteria:**
  - [ ] A disconnected environment can install from mirrored artifacts only.
  - [ ] Every image/model is pinned by digest/checksum.
  - [ ] Upgrade and rollback are documented and rehearsed.
  - [ ] No runtime request leaves the network.
  - [ ] README links the runbook only after verification.
- **Tests/evidence:** Dry-run in a network-isolated environment; packet capture shows no
  outbound traffic; install log.
- **Docs/telemetry:** `docs/AIRGAPPED.md`.

#### OPS-03 · Disaster recovery and point-in-time recovery
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ops-disaster-recovery`
- **Depends on:** OPS-01
- **Scope:** Define DR scenarios (DB loss, node loss, registry loss, NATS loss, Gitea loss,
  Argo loss), PITR procedure, failover/failback, data integrity checks and communication.
- **Acceptance criteria:**
  - [ ] Each scenario has a runbook, owner and tested RTO/RPO.
  - [ ] PITR restores to a timestamp with verified consistency.
  - [ ] Failover/failback is rehearsed at least once per milestone.
  - [ ] Data reconciliation covers Postgres, Gitea and NATS.
  - [ ] Post-incident review is required.
- **Tests/evidence:** DR game day report, PITR test.
- **Docs/telemetry:** `docs/DISASTER-RECOVERY.md`.

#### OPS-04 · Kubernetes, Helm and Argo CD packaging
- **Status:** TODO · **Priority:** P1 · **Estimate:** XL · **Branch:** `feature/ops-kubernetes` (split)
- **Depends on:** OPS-05, OPS-06
- **Scope:** Produce a Helm chart/manifests for API, UI, AI, Postgres (or external), NATS,
  Gitea (or external), Envoy/ingress; readiness/liveness probes, resources, PDB, NetworkPolicy,
  HPA, service accounts and Argo CD app-of-apps.
- **Acceptance criteria:**
  - [ ] `helm template`/`kubeconform` passes in CI.
  - [ ] Pods become ready with the documented probes and resources.
  - [ ] NetworkPolicy allows only required traffic.
  - [ ] Secrets are referenced, not inlined.
  - [ ] Argo CD syncs the stack and reports healthy.
  - [ ] Rolling update and rollback work without downtime.
- **Tests/evidence:** Kind/e2e install, kubeconform, Argo sync test.
- **Docs/telemetry:** `docs/KUBERNETES.md`, chart values reference.

#### OPS-05 · TLS, mTLS and certificate lifecycle
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/ops-tls`
- **Depends on:** OPS-04
- **Scope:** Terminate TLS at the edge, enable HSTS/secure cookies, encrypt internal traffic
  (mTLS where supported), and document certificate issuance/rotation for online and offline
  environments.
- **Acceptance criteria:**
  - [ ] All external endpoints are HTTPS-only.
  - [ ] Internal service traffic is encrypted or explicitly risk-accepted.
  - [ ] Certificates have owners and rotation procedures.
  - [ ] Expiry is monitored and alerted.
  - [ ] Offline CA/import path is documented.
- **Tests/evidence:** TLS scan, expiry alert test, rotation rehearsal.
- **Docs/telemetry:** `docs/TLS.md`, cert expiry metric.

#### OPS-06 · Secrets management and rotation
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/ops-secrets`
- **Depends on:** OPS-04
- **Scope:** Move secrets to a managed store (Vault/External Secrets/SOPS per deployment),
  add JWT `kid`/rotation, webhook secret overlap, DB credential rotation and a secret inventory.
- **Acceptance criteria:**
  - [ ] No secret is stored in Git, images or plain env files in production.
  - [ ] JWT rotation supports an overlap window and old-key rejection.
  - [ ] Webhook secrets rotate without dropping valid deliveries.
  - [ ] Access to secrets is least-privilege and audited.
  - [ ] A rotation is rehearsed and documented.
- **Tests/evidence:** Rotation tests, secret scan, access review.
- **Docs/telemetry:** `docs/SECRETS.md`, rotation age metric.

#### OPS-07 · SBOM, image signing and admission verification
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/ops-supply-chain`
- **Depends on:** BASE-06
- **Scope:** Generate SBOMs, sign images, attach provenance and enforce verification at
  admission (Kyverno/OPA) or deploy time. Document the exception process.
- **Acceptance criteria:**
  - [ ] Every release image has an SBOM and signature.
  - [ ] Unsigned/unverified images are rejected by policy.
  - [ ] Vulnerability scans gate high/critical findings or require an exception.
  - [ ] Provenance is linked to the source commit and workflow run.
  - [ ] Air-gapped verification works offline.
- **Tests/evidence:** Signature verification, admission denial test, SBOM inspection.
- **Docs/telemetry:** `docs/SUPPLY-CHAIN.md`.

#### OPS-08 · Load, capacity and cost model
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `test/ops-load-capacity`
- **Depends on:** OBS-05
- **Scope:** Build k6 scenarios for login, workspace load, board, search, SSE, webhook ingest
  and AI; define capacity per 100/1,000 users; test DB connections, NATS lag, CPU/memory and
  storage growth.
- **Acceptance criteria:**
  - [ ] Reference dataset and scenarios are reproducible.
  - [ ] p95 latency budgets are met or documented as gaps.
  - [ ] The system degrades predictably under overload.
  - [ ] Capacity/cost per 100 users is documented.
  - [ ] Results are attached to the release certification.
- **Tests/evidence:** k6 report, resource graphs, bottleneck list.
- **Docs/telemetry:** `docs/CAPACITY.md`.

#### OPS-09 · Migration safety and rollback
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `feature/ops-migration-safety`
- **Depends on:** PLN-01
- **Scope:** Enforce expand/contract migrations, lock timeouts, online index creation, backfill
  jobs, pre-migration backups and a tested rollback/forward-fix procedure. Add a CI migration
  linter.
- **Acceptance criteria:**
  - [ ] Destructive changes require a two-step release.
  - [ ] Migrations run with lock/statement timeouts.
  - [ ] Large backfills are batched and resumable.
  - [ ] A pre-migration backup is verified.
  - [ ] Rollback/forward-fix is documented and rehearsed.
- **Tests/evidence:** Migration up/down on a production-like dataset, lock-time test.
- **Docs/telemetry:** `docs/MIGRATIONS.md`, migration duration metric.

#### OPS-10 · Chaos and failure drills
- **Status:** TODO · **Priority:** P2 · **Estimate:** L · **Branch:** `test/ops-chaos`
- **Depends on:** OPS-03, OBS-04
- **Scope:** Run controlled failure experiments: DB down/slow, NATS down, Gitea down, Argo
  down, AI down, disk full, network latency and pod kill. Verify degradation, alerts and
  recovery.
- **Acceptance criteria:**
  - [ ] Each experiment has a hypothesis, blast radius and abort condition.
  - [ ] The UI degrades honestly and recovers without data loss.
  - [ ] Alerts fire and runbooks are accurate.
  - [ ] No experiment runs in production without approval.
  - [ ] Findings become tasks with owners.
- **Tests/evidence:** Game-day report, traces/logs, follow-up tasks.
- **Docs/telemetry:** `docs/CHAOS.md`.

#### OPS-11 · Privacy, retention, export and erasure
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `feature/ops-data-governance`
- **Depends on:** GOV-04, OPS-01
- **Scope:** Implement data classification, retention policies, user/project export, erasure
  (with legal hold), audit retention and deletion verification. Cover Postgres, Gitea, NATS,
  backups and logs.
- **Acceptance criteria:**
  - [ ] Retention is configurable per data class and enforced by jobs.
  - [ ] Export produces a complete, machine-readable package.
  - [ ] Erasure removes/anonymizes data across stores and is verified.
  - [ ] Legal hold blocks deletion and is audited.
  - [ ] Backups follow the documented expiry.
- **Tests/evidence:** Export/erasure integration tests, retention job tests, backup expiry test.
- **Docs/telemetry:** `docs/DATA-GOVERNANCE.md`.

#### OPS-12 · Upgrade, rollback and version compatibility
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `docs/upgrade-policy`
- **Depends on:** BASE-08, OPS-09
- **Scope:** Define supported upgrade paths (N/N−1), API/UI compatibility, DB migration order,
  Gitea/Argo/Postgres version support, deprecation policy and rollback limits.
- **Acceptance criteria:**
  - [ ] Supported versions and upgrade paths are documented.
  - [ ] The API keeps N−1 compatibility for the documented window.
  - [ ] Deprecations have a timeline and warnings.
  - [ ] An upgrade from the previous release is rehearsed.
  - [ ] Rollback limits (irreversible migrations) are explicit.
- **Tests/evidence:** Upgrade rehearsal report, compatibility tests.
- **Docs/telemetry:** `docs/UPGRADING.md`.

#### OPS-13 · Project and tenant isolation verification
- **Status:** TODO · **Priority:** P0 · **Estimate:** M · **Branch:** `test/ops-isolation`
- **Depends on:** GOV-01, GOV-02
- **Scope:** Prove that project isolation holds across API, search, AI, SSE, exports, audit and
  webhooks. Add negative tests for IDOR, cross-project aggregation and cache leakage.
- **Acceptance criteria:**
  - [ ] A user cannot read/write/stream another project's data by changing IDs.
  - [ ] Search/AI/exports never aggregate inaccessible projects.
  - [ ] SSE tickets are project-scoped and single-use.
  - [ ] Webhook payloads cannot mutate a project they are not bound to.
  - [ ] Tests run in CI against the real database.
- **Tests/evidence:** Isolation test suite, adversarial cases, CI report.
- **Docs/telemetry:** Isolation guarantees in `SECURITY.md`.

#### OPS-14 · Resource quotas, storage growth and cost controls
- **Status:** TODO · **Priority:** P3 · **Estimate:** S · **Branch:** `docs/ops-cost-model`
- **Depends on:** OPS-08
- **Scope:** Document CPU/memory/storage budgets, attachment/retention growth, AI model size,
  backup storage and per-100-user cost. Add alerts for disk/queue/quota thresholds.
- **Acceptance criteria:**
  - [ ] Resource requests/limits are documented for every service.
  - [ ] Storage growth is projected and monitored.
  - [ ] Quotas/alerts exist for attachments, exports, AI index and backups.
  - [ ] Cost per 100 users is estimated with assumptions.
  - [ ] The model is reviewed each release.
- **Tests/evidence:** Capacity report, quota alert test.
- **Docs/telemetry:** `docs/CAPACITY.md` cost section.

### Phase 9 — Quality engineering and release certification

#### QAT-01 · Coverage thresholds and ratchet
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `test/coverage-thresholds`
- **Depends on:** BASE-02
- **Scope:** Add coverage collection for Go, Vitest and pytest with thresholds and a ratchet
  (no decrease). Exclude generated files, but not error branches.
- **Acceptance criteria:**
  - [ ] Go overall ≥ 75%, authz/search/merge ≥ 90%.
  - [ ] UI stores/composables ≥ 80%, critical components ≥ 70%.
  - [ ] AI service ≥ 80%.
  - [ ] Coverage cannot decrease on a PR.
  - [ ] Reports are published as CI artifacts.
- **Tests/evidence:** Coverage report in CI, threshold failure test.
- **Docs/telemetry:** Coverage policy.

#### QAT-02 · Integration tests with real dependencies in CI
- **Status:** TODO · **Priority:** P0 · **Estimate:** L · **Branch:** `test/ci-integration`
- **Depends on:** BASE-02, OPS-09
- **Scope:** Run the 21 Go integration tests against real PostgreSQL and NATS (and a real or
  contract-tested Gitea) in GitHub Actions. Apply migrations, seed deterministic data, and
  publish logs/artifacts on failure.
- **Acceptance criteria:**
  - [ ] Integration tests run on every PR touching API/migrations.
  - [ ] PostgreSQL/NATS are real services, not mocks.
  - [ ] Migrations run from zero and from the previous release.
  - [ ] Failures upload logs and traces.
  - [ ] Runtime stays within the PR budget.
- **Tests/evidence:** CI job logs, migration-from-zero test.
- **Docs/telemetry:** `docs/TESTING.md` integration section.

#### QAT-03 · Playwright E2E and axe in CI
- **Status:** TODO · **Priority:** P0 · **Estimate:** L · **Branch:** `test/ci-e2e`
- **Depends on:** QAT-02
- **Scope:** Run the existing linked SDLC flow plus new critical flows (login, project, work
  item, board move, test run, PR review/merge, search, settings) in CI against a Compose-built
  stack. Add axe scans and artifact upload (trace/video/screenshot).
- **Acceptance criteria:**
  - [ ] E2E runs on every PR (or a labeled subset for docs-only PRs) without local Docker.
  - [ ] Deterministic seed data; no dependence on previous runs.
  - [ ] Traces/videos are uploaded on failure.
  - [ ] Axe fails on serious/critical violations.
  - [ ] No external network request is required.
- **Tests/evidence:** CI E2E report, flake rate, axe report.
- **Docs/telemetry:** E2E guide + test data.

#### QAT-04 · Contract, property and fuzz testing
- **Status:** TODO · **Priority:** P2 · **Estimate:** M · **Branch:** `test/contract-fuzz`
- **Depends on:** BASE-05, SRH-01
- **Scope:** Add OpenAPI contract tests (schemathesis), Go fuzz tests for query/date parsers,
  and property tests for permissions, pagination and state machines.
- **Acceptance criteria:**
  - [ ] Contract tests cover success and error responses.
  - [ ] Fuzz targets run for a bounded time in CI/nightly (PR-safe budget).
  - [ ] Property tests cover authz invariants and pagination stability.
  - [ ] Failures produce a minimal reproducer.
  - [ ] No flaky random seed.
- **Tests/evidence:** Fuzz corpus, contract report, property test logs.
- **Docs/telemetry:** Testing guide.

#### QAT-05 · Frontend performance budgets
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `test/frontend-performance`
- **Depends on:** UX-12
- **Scope:** Add bundle-size budgets, Lighthouse CI or equivalent, and runtime budgets (LCP,
  INP, CLS) for primary screens. Fail PRs that regress beyond the threshold.
- **Acceptance criteria:**
  - [ ] Initial JS ≤ 300 KB gzip (or a documented tighter budget).
  - [ ] LCP ≤ 2.5 s, INP ≤ 200 ms, CLS ≤ 0.1 on the reference profile.
  - [ ] Route-level code splitting is enforced.
  - [ ] Regressions fail with the offending chunk/route.
  - [ ] Budgets are documented and reviewed each release.
- **Tests/evidence:** Bundle report, Lighthouse report, budget failure test.
- **Docs/telemetry:** `docs/PERFORMANCE.md`.

#### QAT-06 · API load and soak tests
- **Status:** TODO · **Priority:** P1 · **Estimate:** L · **Branch:** `test/api-load`
- **Depends on:** OPS-08
- **Scope:** Add k6/Go load scenarios for login, workspace aggregate, board, search, SSE,
  webhook ingest and AI; run a soak test for memory/goroutine leaks.
- **Acceptance criteria:**
  - [ ] p95 budgets are defined per endpoint.
  - [ ] SSE connections are stable for the soak duration.
  - [ ] No goroutine/memory leak beyond the threshold.
  - [ ] Results are comparable across runs (fixed dataset).
  - [ ] Bottlenecks have owners/tasks.
- **Tests/evidence:** Load report, profile/pprof evidence.
- **Docs/telemetry:** Load test runbook.

#### QAT-07 · Authorization and security regression suite
- **Status:** TODO · **Priority:** P0 · **Estimate:** L · **Branch:** `test/security-regression`
- **Depends on:** GOV-01, OPS-13
- **Scope:** Add automated tests for the capability matrix, IDOR, CSRF, SSRF, XSS, rate limits,
  webhook replay, upload limits, session revocation, self-approval and secret leakage.
- **Acceptance criteria:**
  - [ ] Every route has a negative authorization test.
  - [ ] IDOR tests cover project/artifact IDs.
  - [ ] XSS tests cover comments, descriptions, filenames and AI output.
  - [ ] Rate-limit tests cover login/webhooks/search.
  - [ ] Security tests run in CI and block on failure.
- **Tests/evidence:** Security test suite, CI report.
- **Docs/telemetry:** `docs/SECURITY-TESTING.md`.

#### QAT-08 · Test data factories and deterministic fixtures
- **Status:** TODO · **Priority:** P1 · **Estimate:** M · **Branch:** `test/data-factories`
- **Depends on:** QAT-02
- **Scope:** Build factories/fixtures for users, projects, planning, tests, code, builds and
  deployments with deterministic seeds, cleanup and no production data.
- **Acceptance criteria:**
  - [ ] Tests create isolated data with one call.
  - [ ] Fixtures cover every role and capability.
  - [ ] Seeds are reproducible and documented.
  - [ ] Cleanup is automatic and safe.
  - [ ] No real customer/PII data is used.
- **Tests/evidence:** Factory tests, parallel isolation test.
- **Docs/telemetry:** Test data guide.

#### QAT-09 · Flake detection and quarantine policy
- **Status:** TODO · **Priority:** P2 · **Estimate:** S · **Branch:** `docs/flake-policy`
- **Depends on:** QAT-03
- **Scope:** Detect flaky tests from CI history, quarantine with an owner and expiry, and forbid
  silent skips/retries that hide failures.
- **Acceptance criteria:**
  - [ ] Flake rate is measured per test.
  - [ ] Quarantine requires an owner, reason and expiry.
  - [ ] Quarantined tests are still reported, not deleted.
  - [ ] A retry policy is explicit and visible.
  - [ ] Expired quarantines fail the build.
- **Tests/evidence:** Flake report, quarantine metadata test.
- **Docs/telemetry:** `docs/FLAKY-TESTS.md`.

#### QAT-10 · Release certification and go/no-go
- **Status:** TODO · **Priority:** P1 · **Estimate:** S · **Branch:** `docs/release-certification`
- **Depends on:** QAT-01..QAT-09, OPS-01..OPS-12
- **Scope:** Define the release certification checklist with evidence: tests, security, a11y,
  performance, backup restore, migration rehearsal, air-gap check, docs and rollback.
- **Acceptance criteria:**
  - [ ] Every release has a signed checklist with links to evidence.
  - [ ] A failed gate blocks the release.
  - [ ] Exceptions have an owner, expiry and risk acceptance.
  - [ ] The checklist is reviewed in the go/no-go meeting.
  - [ ] Post-release verification confirms health and rollback readiness.
- **Tests/evidence:** Completed certification for the first production release.
- **Docs/telemetry:** `docs/RELEASE-CERTIFICATION.md`.

---

## 8. Cross-cutting engineering standards

These standards apply to every task. A PR that violates them is not "done", even if the
feature works.

### 8.1 API standards

- REST over JSON; resource names are plural nouns; state transitions use explicit verbs
  (`/merge`, `/rollback`, `/promote`) or `PATCH` with a state field.
- Every response uses the stable error envelope:
  `{ "error": { "code": "SNAKE_CASE", "message": "Human readable", "details": ... },
  "request_id": "..." }`.
- Validation errors include field paths and are safe to display.
- Pagination is cursor/keyset for large or live collections; page/page_size is allowed for
  small admin tables. Responses include `next_cursor`, `has_more` and a total only when cheap.
- Filtering, sorting and search are server-side and allow-listed; never interpolate SQL.
- Mutations accept `Idempotency-Key` where retries are possible (webhooks, merges, deployments,
  exports). Replays return the original result.
- Concurrency uses `ETag`/`If-Match` or `expected_version`; a mismatch returns `409` with the
  current representation.
- Rate limits are per identity/IP and per endpoint class (auth, search, webhooks, AI, exports).
  `429` includes `Retry-After`.
- Auth is `Authorization: Bearer <JWT>` for users and scoped tokens for automation. SSE uses a
  single-use ticket, never a JWT in the query string.
- API versioning is explicit (`/api/v1`); breaking changes require a new version or a
  documented deprecation window with `Sunset`/`Deprecation` headers.
- OpenAPI is generated/validated in CI and is the source for the typed client.
- All timestamps are RFC 3339 UTC at the API boundary; formatting is a UI concern.
- No internal error, SQL, stack trace or secret is returned to a client.

### 8.2 Data standards

- PostgreSQL is the system of record for SPM metadata; Gitea owns Git objects; Argo CD owns
  deployment state. SPM stores evidence and links, not copies that can silently drift.
- Primary keys are UUIDv7 (time-ordered) for new tables; foreign keys are indexed.
- Every table has `created_at`, `updated_at` (UTC) and, where edits are possible, `version`.
- Soft delete is used only where recovery/audit requires it; otherwise hard delete with
  explicit cascades and audit.
- Migrations are expand/contract, forward-only in production, with a separately tested
  rollback/forward-fix. Destructive steps require a prior release.
- Migrations use lock/statement timeouts and online index creation; backfills are batched and
  resumable.
- No query returns unbounded rows from a user-controlled list.
- Multi-tenant/project isolation is enforced in SQL predicates and middleware, not only in the
  UI. Every query that touches project data includes the project scope.
- Audit/activity tables are append-only and excluded from normal update paths.
- PII is classified, minimized and redacted from logs/traces. Retention/erasure is implemented
  in `OPS-11`.

### 8.3 Frontend standards

- Vue 3 `<script setup>` + TypeScript strict. No `any` without a written reason.
- Server state lives in a store/composable with explicit loading/error/empty states; URL state
  is the source of truth for filters, tabs and selection where shareability matters.
- Business data is never persisted only in `localStorage`. Preferences may be local, but saved
  searches, views and drafts are server-side.
- SSE is the live-update path; no polling loops. On disconnect, show a non-blocking status and
  reconnect with backoff; reconcile with a full fetch after reconnect.
- Components are presentational or screen-level; no screen imports another screen's internals.
- One `<h1>` per page; landmarks and heading order are enforced.
- All interactive elements are real buttons/links; clickable `div`s are forbidden.
- Forms use the shared field/validation system; no bespoke error markup.
- Tables use the shared data table; no unbounded `v-for` over API results.
- Charts use the shared chart system and always provide a table fallback.
- No CDN, external font, analytics or telemetry script. The app must work fully offline.
- Bundle budgets are enforced in `QAT-05`; route-level code splitting is required.
- Destructive actions require confirmation; irreversible actions require typed confirmation.
- Empty states explain the next action; errors are actionable and do not blame the user.

### 8.4 Testing standards

- Test behaviour, not implementation details. A refactor should not require rewriting tests.
- Unit tests cover pure logic, parsers, state machines and edge cases.
- Integration tests cover the database, migrations, NATS, webhooks and SSE with real
  dependencies.
- Contract tests cover the OpenAPI surface and error envelope.
- E2E covers the critical user journeys only; it is not a replacement for unit/integration.
- Every bug fix adds a regression test that fails before the fix.
- Authorization changes add a matrix test for every affected role/capability.
- UI changes add keyboard and axe checks, plus screenshots at 375/768/1280/1440.
- Performance-sensitive changes add a benchmark or load scenario.
- Tests are deterministic: fixed clock/timezone, seeded data, no network, no order dependence.
- Flaky tests are quarantined with an owner and expiry (`QAT-09`), never silently retried.

### 8.5 Security standards

- Target OWASP ASVS Level 2 for the application and Level 3 for authentication/audit where
  practical.
- Threat-model every new surface (auth, uploads, webhooks, AI, exports, admin).
- Authorization is deny-by-default and enforced server-side on every request.
- Validate input on the server; encode output in the UI. Convert Markdown to safe HTML with an
  allow-list.
- CSRF protection for cookie-based flows; SameSite cookies; no secrets in URLs.
- SSRF protection for integrations/webhooks: allow-list schemes/hosts, block link-local and
  metadata endpoints, bound redirects.
- Uploads are type/size limited, stored outside the web root, served with safe content types
  and `Content-Disposition`.
- Security headers: CSP (no `unsafe-inline` where avoidable), HSTS, `X-Content-Type-Options`,
  `Referrer-Policy`, `Permissions-Policy`, frame-ancestors.
- Rate-limit authentication, password reset, search, exports, AI and webhooks.
- Secrets are never logged, returned, embedded in images or committed. Rotation is rehearsed.
- Dependency and container scans gate high/critical findings; exceptions expire.
- Security incidents follow the disclosure/response policy in `SECURITY.md`.

### 8.6 Observability standards

- Every service exposes health (liveness/readiness) and metrics.
- HTTP metrics use route templates, not raw paths; status class, method and duration buckets.
- Traces cover inbound requests, DB, NATS, outbound integrations and background jobs.
- Logs are structured JSON with request/trace IDs and redacted sensitive fields.
- Every alert has an owner, severity, runbook and a tested firing path.
- SLOs exist for availability, latency, freshness and AI latency; error budgets drive release
  decisions.
- Dashboards are provisioned as code, not clicked together in production.

### 8.7 Performance budgets

| Surface | Budget |
|---|---|
| API p95 (workspace aggregate) | ≤ 400 ms at the reference dataset |
| API p95 (list/search) | ≤ 300 ms |
| API p95 (mutation) | ≤ 250 ms |
| SSE update latency | ≤ 2 s p95 from event to UI |
| Web initial JS | ≤ 300 KB gzip |
| LCP / INP / CLS | ≤ 2.5 s / ≤ 200 ms / ≤ 0.1 |
| Board interaction | ≤ 100 ms perceived; 2,000 cards usable |
| Search index freshness | ≤ 60 s p95 |
| AI answer latency | ≤ 3 s p95 CPU-only; bounded by timeout |
| Export job start | ≤ 30 s p95 |

Budgets are enforced by `QAT-05`, `QAT-06` and `OPS-08`; a regression requires a written
exception with an owner and expiry.

### 8.8 Documentation standards

- Every feature has a short operator/user guide and a link from the README or in-app help.
- Every API change updates OpenAPI and the typed client in the same PR.
- Every runbook has prerequisites, steps, verification, rollback and escalation.
- Every architecture claim links to code, a test or a dashboard. If it cannot, it is marked
  "planned" and linked to a task.
- `TASKS.md` status is updated when a card moves to `DONE`; the PR includes the task ID.
- Dates are ISO 8601; times are UTC unless the user's timezone is explicit.
- Screenshots in docs are current; stale screenshots are removed, not left to rot.

### 8.9 Definition of production-ready (service level)

A service is production-ready only when it has:

- [ ] Health endpoints and startup/readiness probes.
- [ ] Resource requests/limits and a capacity estimate.
- [ ] Metrics, traces, logs and at least one dashboard.
- [ ] SLOs and alerts with runbooks.
- [ ] Backups/restore or a documented stateless recovery.
- [ ] TLS and secret management.
- [ ] A rollback path and a tested upgrade.
- [ ] Authorization and audit coverage.
- [ ] Load test results at the expected scale.
- [ ] An owner and an on-call/escalation path.

---

## 9. Milestones and sequencing

### 9.1 Dependency graph (high level)

```text
BASE-01..03 (done)
   │
   ├── BASE-04..08 ──► GOV-01 ──► GOV-02 ──► GOV-03 ──► GOV-04 ──► GOV-05
   │                     │                                  │
   │                     └──► GOV-06..12                    └──► UI-15
   │
   ├── UX-01 ──► UX-02 ──► UX-03 ──► UX-04 ──► UX-05 ──► UX-06 ──► UX-07
   │                                      │         │
   │                                      └──► UX-08 ──► ANL-01
   │                                      └──► UX-09/10/11/12/13
   │
   ├── PLN-01 ──► PLN-02..05 ──► BRD-01..03
   │       │
   │       └──► TST-01..05
   │
   ├── COD-01..08 ──► DEL-01..04
   │
   ├── SRH-01..05 ──► ANL-01..06
   │
   ├── AI-01..09
   │
   └── OBS-01..06 ──► OPS-01..14 ──► QAT-01..10
```

### 9.2 Milestones

| Milestone | Theme | Cards | Exit criteria |
|---|---|---|---|
| **M0 — Trustworthy baseline** | Repo, gate, truth | BASE-01..04 | Clean history, PR-only gate, branch protection, docs truthful, first validation PR merged |
| **M1 — Governed identity** | AuthZ, audit, identity | GOV-01..05 (minimum), BASE-05..07 | Capability matrix enforced, sessions invalidated, reviews attributable, audit append-only |
| **M2 — Design system and shell** | UX foundation | UX-01..13 | Tokens/primitives/frame/table/forms/charts/a11y gate in place; screens can be rebuilt without new primitives |
| **M3 — Core delivery surfaces** | Projects, planning, board, tests | UI-02..UI-06, UI-10..UI-13, PLN/BRD/TST | A team can plan, execute and test without the legacy board or localStorage |
| **M4 — Code and delivery control** | Code, PRs, CI/CD | COD-01..08, DEL-01..04, UI-07/08 | Review-to-merge and build-to-deploy are evidence-based and permission-safe |
| **M5 — Discovery and intelligence** | Search, analytics, AI | SRH-01..05, ANL-01..06, AI-01..09, UI-09/12 | Search covers all artifacts; reports are scheduled/exportable; AI is grounded and permission-safe |
| **M6 — Enterprise certification** | Ops, security, quality | OBS-01..06, OPS-01..14, QAT-01..10, UI-14..16 | Restore/air-gap/DR drills pass; SLOs/alerts live; release certification signed |

### 9.3 Suggested first ten PRs

1. `docs/truth-baseline` — BASE-04. Cheap, removes false claims, validates the gate again.
2. `feature/api-openapi-contract` — BASE-05. Prevents contract drift before more API work.
3. `feature/authz-capabilities` — GOV-01. The highest-risk gap.
4. `fix/membership-session-invalidation` — GOV-02. Closes the read-after-removal hole.
5. `feature/audit-log` — GOV-04. Required by every later admin surface.
6. `feature/code-reviews-identity` — GOV-03. Required for safe code/delivery.
7. `docs/ux-ia-audit` — UX-01. Locks the navigation/IA before UI work.
8. `feature/ui-design-tokens` + `feature/ui-primitives` — UX-02/03.
9. `feature/ui-app-frame` + `feature/ui-page-frame` — UX-04/05.
10. `feature/ui-data-table` — UX-06. Unlocks the portfolio, board, tests, delivery and admin.

### 9.4 Capacity and WIP model

- One maintainer: **WIP limit 2** (one feature + one hardening/docs), plus one PR in review.
- 60% feature delivery, 25% hardening/security/ops, 15% support/incident.
- Reserve one day per week for dependency/security updates and documentation truth.
- Any P0 security/data issue preempts feature work.
- A milestone is not declared until its exit evidence is linked in the milestone table.
- If a card exceeds its estimate by 50%, stop and split/re-plan; do not "push through".

---

## 10. Risk register

Probability/impact: H/M/L. The owner is accountable for mitigation and review.

| ID | Risk | P | I | Mitigation | Owner | Trigger |
|---|---|---|---|---|---|---|
| R-01 | Secret/key exposure in Git or images | M | H | `.gitignore`, secret scanning, push protection, SBOM/signing, rotation runbook | Maintainer | Any secret-scan alert |
| R-02 | Privilege escalation via coarse RBAC | H | H | GOV-01/02, negative authz tests, audit denied attempts | Maintainer | Any 403 bypass report |
| R-03 | Self-approval / unreviewed merge | H | H | GOV-03, reviewer identity, branch protection, merge policy | Maintainer | Merge without a non-author approval |
| R-04 | Data loss during migration | M | H | Expand/contract, backups, PITR drill, migration linter | Maintainer | Migration > 5 min or destructive DDL |
| R-05 | Cross-project data leak via search/AI/SSE | M | H | Permission-filtered retrieval, isolation tests, SSE tickets | Maintainer | Any cross-project result |
| R-06 | AI hallucination or prompt injection | H | M | Grounding/citations, refusal, injection tests, LLM off by default | Maintainer | Unsafe answer or action |
| R-07 | Gitea/Argo API drift or webhook loss | M | M | Contract tests, idempotency, outbox, reconciliation, DLQ | Maintainer | Sync lag > SLO |
| R-08 | Performance collapse at scale | M | H | Budgets, load tests, indexes, virtualization, capacity model | Maintainer | p95 > budget for 2 windows |
| R-09 | CI flakiness and slow PRs | H | M | Deterministic seeds, quarantine, sharding, caching, <20 min budget | Maintainer | Median PR > 25 min or flake > 2% |
| R-10 | Solo-maintainer bus factor | H | H | Documentation, runbooks, ADRs, second maintainer, break-glass | Owner | No merge capacity for 3 days |
| R-11 | Public-repo supply-chain attack | L | H | Pinned actions, least-privilege tokens, CodeQL, dependency review, no `pull_request_target` | Maintainer | Unreviewed workflow change |
| R-12 | Air-gapped install/upgrade failure | M | H | OPS-02 mirroring + network-isolated rehearsal | Maintainer | Any outbound request/critical doc gap |
| R-13 | Backup exists but restore fails | M | H | Restore drills, integrity checks, RTO/RPO evidence | Maintainer | Failed restore or backup age > RPO |
| R-14 | UI scope creep delays core delivery | H | M | Blueprints, DoR, WIP limits, split XL cards, milestone gates | Maintainer | Card > 50% over estimate |
| R-15 | Documentation drifts from reality | H | M | BASE-04 truth pass, docs-in-PR rule, evidence links | Maintainer | Claim without evidence |
| R-16 | Dependency vulnerability exploited | M | H | Dependabot, scans, patch SLA, exception expiry | Maintainer | Critical CVE in runtime path |
| R-17 | GitHub Actions outage blocks delivery | L | M | Local/CI-equivalent scripts, documented emergency path, no force push | Maintainer | Actions unavailable > 4 h |
| R-18 | Data retention/privacy non-compliance | M | H | OPS-11, classification, export/erasure tests | Maintainer | Customer/legal request |

Risk review cadence: weekly during active delivery, at every milestone exit, and after any
incident. A risk with no owner or no mitigation is escalated to `BLOCKED`.

---

## 11. Architecture decision log

Architecture decisions are recorded here so that future work does not re-litigate settled
questions or silently reverse them. An ADR is **immutable once accepted**: to change a
decision, add a new ADR that supersedes it and update the status of the old one. Every
significant PR that introduces an architectural change must add or supersede an ADR in the
same PR.

**ADR status vocabulary:** `PROPOSED`, `ACCEPTED`, `SUPERSEDED` (link to replacement),
`DEPRECATED` (still in force, scheduled for removal), `REJECTED` (with reason kept for the
record).

### 11.1 ADR index

| ADR | Title | Status | Date | Related tasks |
|---|---|---|---|---|
| ADR-001 | Trunk-based development with a protected `master` | ACCEPTED | 2026-11-18 | BASE-03 |
| ADR-002 | Squash-only merges and linear history | ACCEPTED | 2026-11-18 | BASE-03 |
| ADR-003 | GitHub Actions is the verification authority | ACCEPTED | 2026-11-18 | BASE-02 |
| ADR-004 | Orphan baseline instead of history rewrite | ACCEPTED | 2026-11-18 | BASE-01 |
| ADR-005 | Retain the existing runtime stack | ACCEPTED | 2026-11-18 | — |
| ADR-006 | Capability-based authorization | ACCEPTED | 2026-11-18 | GOV-01 |
| ADR-007 | PostgreSQL as system of record; providers own their domains | ACCEPTED | 2026-11-18 | COD-01, DEL-02 |
| ADR-008 | Append-only, hash-chained audit log | ACCEPTED | 2026-11-18 | GOV-04 |
| ADR-009 | Server-Sent Events for live updates | ACCEPTED | 2026-11-18 | DEL-04, AI-09 |
| ADR-010 | Server-side saved views; URL is UI state | ACCEPTED | 2026-11-18 | SRH-03, BRD-01 |
| ADR-011 | Custom design system on Tailwind tokens | ACCEPTED | 2026-11-18 | UX-02, UX-03 |
| ADR-012 | Offline-first with zero runtime SaaS dependencies | ACCEPTED | 2026-11-18 | OPS-02 |
| ADR-013 | Local-first AI with grounded retrieval | ACCEPTED | 2026-11-18 | AI-01..AI-09 |
| ADR-014 | OpenAPI as an executable contract | ACCEPTED | 2026-11-18 | BASE-05 |
| ADR-015 | Compose for delivery; Helm/Kubernetes for production | ACCEPTED | 2026-11-18 | OPS-04 |
| ADR-016 | External secret management with rotation | ACCEPTED | 2026-11-18 | OPS-06 |
| ADR-017 | Self-hosted observability stack | ACCEPTED | 2026-11-18 | OBS-01..OBS-05 |
| ADR-018 | Durable in-app notifications from domain events | ACCEPTED | 2026-11-18 | UX-14, PLN-02 |
| ADR-019 | Local identity baseline with optional enterprise federation | ACCEPTED | 2026-11-18 | GOV-06..GOV-10 |
| ADR-020 | Feature flags in configuration, not a SaaS flag service | ACCEPTED | 2026-11-18 | BASE-08 |
| ADR-021 | Dependency policy: pinned, reviewed, mirrored | ACCEPTED | 2026-11-18 | BASE-06, OPS-07 |
| ADR-022 | Data retention and erasure as first-class jobs | ACCEPTED | 2026-11-18 | OPS-11 |
| ADR-023 | Solo-authored history with Dependabot as the only bot author | ACCEPTED | 2026-11-18 | BASE-02, BASE-06 |

### ADR-001 · Trunk-based development with a protected `master`

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** The project has a single maintainer today and must remain safe if the team
  grows. Long-lived branches hide integration risk and make reviews stale.
- **Decision:** `master` is the only long-lived branch. Work happens on short-lived
  `feature/`, `fix/`, `change/`, `docs/` and `chore/` branches that live at most three working
  days. `master` is protected: pull request required, strict required checks, conversation
  resolution, linear history, no force push, no deletion, admins enforced.
- **Consequences:** Integration problems surface within days, not weeks. Every change has a
  reviewable PR record. Small cards are mandatory; large work must be split. Release branches
  are allowed only for security backports and must be deleted afterwards.
- **Alternatives considered:** GitFlow (rejected: release branches and merge commits add
  overhead for a continuously deployed product); environment branches (rejected: drift and
  cherry-pick risk).

### ADR-002 · Squash-only merges and linear history

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** A readable history is an operational asset; noisy merge commits and
  work-in-progress commits obscure intent and complicate revert.
- **Decision:** Only squash merge is enabled. The squash subject follows Conventional Commits
  and includes the task ID. Merge commits and rebase merges are disabled. Feature branches are
  deleted after merge.
- **Consequences:** One commit per PR on `master`; `git revert` is the standard rollback for a
  bad change. PR titles become permanent history and must be written carefully. Multiple
  logical changes in one PR are discouraged because they collapse into one commit.
- **Alternatives considered:** Rebase merge (rejected: requires disciplined commit cleanup and
  produces many commits per PR); merge commits (rejected: non-linear history, harder revert).

### ADR-003 · GitHub Actions is the verification authority

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** Local verification depends on the developer's machine, Docker availability and
  cached state. It is not reproducible evidence.
- **Decision:** The PR gate in GitHub Actions is the authority for merge. Workflows run only
  on `pull_request` events. Local scripts remain for fast feedback but never substitute for
  the gate. Local Docker is optional and is not started by the agent unless explicitly
  requested.
- **Consequences:** Every claim of "verified" links to a workflow run. CI minutes and runtime
  are a budget to manage (target median PR < 20 minutes). Flaky checks are fixed or
  quarantined, never bypassed.
- **Alternatives considered:** Self-hosted runners from day one (deferred: cost/complexity
  until private-network integration tests require them); local Docker gate (rejected: not
  reproducible, not evidence).

### ADR-004 · Orphan baseline instead of history rewrite

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** The pre-bootstrap local history contained `kube-config.yaml` with a client
  private key. The repository had never been pushed, and the remote was empty.
- **Decision:** Rebuild the repository as a single orphan commit containing only the current
  working tree, honoring a hardened `.gitignore`; expire the reflog and garbage-collect the
  old objects; push that single commit to `master`. Do not attempt a filter-repo rewrite and
  do not push the old history anywhere.
- **Consequences:** No secret-bearing object is reachable from any ref. Local commit history
  before the baseline is intentionally lost. The exposed credential must still be rotated
  because it existed on disk and may have been copied elsewhere.
- **Alternatives considered:** `git filter-repo` (rejected: unnecessary risk and complexity
  for an unpushed repository); deleting the file in a new commit (rejected: history would
  still leak the key).

### ADR-005 · Retain the existing runtime stack

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** The baseline already implements the domain in Go/Gin, Vue 3, FastAPI,
  PostgreSQL, NATS JetStream, Gitea and Argo CD, packaged with Compose and Envoy.
- **Decision:** Keep this stack and harden it. Do not rewrite the backend, replace the
  database, or adopt a heavyweight frontend framework. Improvements target correctness,
  security, accessibility and operability rather than novelty.
- **Consequences:** The team's existing knowledge remains valuable; risk is concentrated in
  missing enterprise controls rather than an unproven rewrite. Some ergonomics (e.g. custom
  data table, design system) must be built rather than imported.
- **Alternatives considered:** Next.js/React rewrite (rejected: no functional gain, large
  risk); a BaaS or SaaS backend (rejected: violates offline/air-gap requirement).

### ADR-006 · Capability-based authorization

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** A single project-write gate let testers mutate planning/code artifacts and
  trusted JWT project IDs for reads. Roles alone do not express the required granularity.
- **Decision:** Define a canonical capability matrix (see `GOV-01`) and enforce it server-side
  per route with middleware that resolves current membership. JWT claims are hints, never
  authority. The capability set is exposed on `/auth/me` for UI gating only.
- **Consequences:** Every write route must declare a capability; tests must cover the
  role × capability matrix. Membership changes must invalidate sessions (`GOV-02`). Denials
  are audited. Global admins retain audited break-glass access.
- **Alternatives considered:** More roles (rejected: role explosion and ambiguous overlap);
  policy engine such as OPA/Cedar (deferred: viable later, but a typed Go matrix is simpler
  and testable now).

### ADR-007 · PostgreSQL as system of record; providers own their domains

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** Git objects live in Gitea, deployment state in Argo CD, events in NATS.
  Duplicating those domains creates drift and reconciliation bugs.
- **Decision:** PostgreSQL stores SPM metadata, links and immutable evidence snapshots. Gitea
  is authoritative for Git objects and reviews where applicable; Argo CD is authoritative for
  deployment/sync/health; NATS is transport, not storage of record. SPM stores references,
  status snapshots and evidence, and reconciles them explicitly.
- **Consequences:** Every integration needs reconciliation, staleness markers and idempotent
  ingestion. The UI must show "stale/unknown" rather than guessing. Provider outages degrade
  evidence freshness but do not corrupt SPM data.
- **Alternatives considered:** Mirroring full Git history and manifests into PostgreSQL
  (rejected: storage cost, drift, no benefit); treating NATS as the source of truth
  (rejected: retention and query limitations).

### ADR-008 · Append-only, hash-chained audit log

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** Admin actions emitted no audit events and there was no global audit surface.
  Auditors require tamper evidence and retention.
- **Decision:** Add a dedicated audit table with actor, action, target, project, result,
  request metadata, redacted before/after and a per-record hash chain. Deny `UPDATE`/`DELETE`
  via permissions and triggers. Provide filtered read, export and chain verification APIs.
- **Consequences:** Audit writes are on the hot path for mutations and must be reliable and
  low-latency; a failed audit write fails the mutation. Schema changes to audited objects must
  consider the redaction rules. Retention is enforced by partition drops, themselves audited.
- **Alternatives considered:** Application logs only (rejected: not queryable, not tamper
  evident); external SIEM as primary store (rejected: offline/air-gap requirement).

### ADR-009 · Server-Sent Events for live updates

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** The UI needs live project updates without polling; the edge is Envoy and the
  backend is Go with NATS fan-out.
- **Decision:** Use SSE with single-use tickets, NATS-backed fan-out per project/user, ordered
  event IDs and reconnect with `Last-Event-ID`. No WebSocket, no client polling loops.
- **Consequences:** Proxies must not buffer SSE and must allow long-lived connections. Every
  event needs a stable ID and a dedupe strategy. On reconnect, the client reconciles with a
  full fetch. Horizontal scaling requires NATS, not sticky sessions.
- **Alternatives considered:** WebSocket (rejected: bidirectional not needed, more complex
  auth/proxy); short polling (rejected: load and latency).

### ADR-010 · Server-side saved views; URL is UI state

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** Saved searches lived in `localStorage`, so they were per-device and invisible
  to teams. Filters were not shareable.
- **Decision:** Saved searches, board/report views and table configurations are server-side,
  named, permission-aware and shareable. Transient UI state (filters, sort, tab, selection
  scope, pagination cursor) is encoded in the URL.
- **Consequences:** Views survive device changes and can be reviewed. The URL can grow; use
  compact parameter names and omit defaults. Server APIs must validate view definitions and
  enforce access at read time.
- **Alternatives considered:** `localStorage` (rejected: not durable/shareable); a separate
  state service (rejected: unnecessary dependency).

### ADR-011 · Custom design system on Tailwind tokens

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** Screens used hand-rolled spacing, headers and tables, producing inconsistency
  and accessibility gaps. A full component framework would add weight and opinionated styling.
- **Decision:** Keep Tailwind as the utility engine but define semantic design tokens and
  build an accessible Vue primitive library in-repo (buttons, inputs, dialogs, tables, charts,
  etc.). No component framework that ships its own theme, telemetry or CDN assets.
- **Consequences:** More upfront work for primitives and tests; long-term consistency,
  smaller bundle and full control. Every new pattern must be added to the gallery and tested.
- **Alternatives considered:** Vuetify/PrimeVue/Element Plus (rejected: theme fights, bundle
  size, accessibility gaps, difficult offline auditing); CSS-in-JS (rejected: runtime cost).

### ADR-012 · Offline-first with zero runtime SaaS dependencies

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** The product must run on private networks and air-gapped installations with no
  outbound internet after mirroring.
- **Decision:** No runtime CDN, external font, analytics, crash reporting, feature-flag SaaS,
  email SaaS or model download. All assets, models and images are packaged and pinned by
  digest/checksum. Telemetry is self-hosted and export is optional.
- **Consequences:** Bundle size and image size are deliberate costs. Model and image mirroring
  must be documented and rehearsed (`OPS-02`). Any proposed dependency must pass an offline
  review in the PR.
- **Alternatives considered:** SaaS integrations with offline fallback (rejected: violates the
  core product promise and creates data-egress risk).

### ADR-013 · Local-first AI with grounded retrieval

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** The AI service is TF-IDF extractive with an optional local GGUF model, while
  documentation claimed embeddings/RAG. There is no vector index and no permission-aware
  retrieval.
- **Decision:** Evolve the service in stages: (1) keep lexical extraction as the honest
  baseline; (2) add CPU-only embeddings and a persistent project-scoped vector index;
  (3) enforce authorization before retrieval and answer assembly; (4) require citations and
  explicit refusal for weak grounding; (5) keep the LLM path optional and off by default until
  safety and evaluation gates pass.
- **Consequences:** Quality improvements are measurable and reversible. The service must run
  within CPU/memory budgets and expose model/index versions. Prompt injection is treated as a
  first-class threat.
- **Alternatives considered:** External LLM/vector SaaS (rejected: offline requirement and
  data-egress risk); LLM-only without retrieval (rejected: hallucination and cost).

### ADR-014 · OpenAPI as an executable contract

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** `docs/openapi.yaml` was stale: it listed removed routes and missed new ones.
  Clients could not trust it.
- **Decision:** OpenAPI 3.1 is generated from or validated against the router and handler
  types. A route-coverage test fails when a registered route is undocumented or a documented
  route is absent. The UI consumes generated types or a contract-tested client.
- **Consequences:** API changes require contract updates in the same PR. Breaking changes
  require a version bump and deprecation window. Generated artifacts are never hand-edited.
- **Alternatives considered:** Hand-maintained spec (rejected: proven to drift); gRPC/protobuf
  (rejected: browser/tooling cost for the current surface).

### ADR-015 · Compose for delivery; Helm/Kubernetes for production

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** Compose is the development and small-install packaging. Argo CD is already in
  the topology, but no Kubernetes packaging exists.
- **Decision:** Keep Docker Compose as the reference deployment for local, CI integration and
  small air-gapped installs. Add a supported Helm chart and Kubernetes manifests for
  production-scale installs, reconciled by Argo CD. Both paths share images and configuration
  contracts.
- **Consequences:** Two deployment surfaces must be tested; configuration must be documented
  once and mapped to both. Production features (PDB, HPA, NetworkPolicy, probes) live in the
  chart. Compose remains the fast path for verification.
- **Alternatives considered:** Compose-only (rejected: no production HA/scaling story);
  Kubernetes-only (rejected: too heavy for local/small installs and CI).

### ADR-016 · External secret management with rotation

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** Secrets were supplied through `.env` and Compose environment variables. JWT
  used a single HS256 secret with no rotation.
- **Decision:** Production secrets live in a managed store (Vault, External Secrets or
  Kubernetes Secrets sourced externally, selected per deployment). JWT uses `kid` with
  rotation overlap. Webhook secrets support dual-secret rotation. Database credentials rotate
  on a schedule. `.env.example` contains placeholders only.
- **Consequences:** Local development still uses `.env`, but production never does. Secret
  inventory, ownership and rotation age are tracked. A leaked secret triggers rotation and an
  incident review, not just removal from Git.
- **Alternatives considered:** SOPS-encrypted files in Git (acceptable for small offline
  installs, but not the default); plain environment variables (rejected: leak-prone).

### ADR-017 · Self-hosted observability stack

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** There was no metrics endpoint, tracing, dashboards, alerts or SLOs, despite a
  README claim of Prometheus metrics.
- **Decision:** Use OpenTelemetry instrumentation exported to a self-hosted Prometheus/
  Grafana/Tempo (or equivalent) stack. Dashboards and alerts are provisioned as code. SLOs
  and error-budget burn alerts are defined per service.
- **Consequences:** Instrumentation is part of the definition of done, not an afterthought.
  Cardinality and PII rules apply to every metric, log and span. Air-gapped installs can
  disable export while keeping local health endpoints.
- **Alternatives considered:** Vendor APM (rejected: outbound dependency); logs only
  (rejected: insufficient for latency/SLO analysis).

### ADR-018 · Durable in-app notifications from domain events

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** Only transient toasts existed; assignments, mentions, approvals and failures
  had no durable inbox.
- **Decision:** Generate notifications from domain events into a per-user store with read/
  unread, snooze and preferences. Deliver in-app in real time over SSE; email/webhook channels
  are optional and offline-configurable. Notification payloads are permission-checked at read
  time.
- **Consequences:** Event schemas become part of the public internal contract. Duplicate
  suppression and idempotency are required. A deleted object must not leave a readable
  notification with sensitive content.
- **Alternatives considered:** Client-side derivation from activity feeds (rejected: no
  read state, expensive); email-first (rejected: offline deployments may have no SMTP).

### ADR-019 · Local identity baseline with optional enterprise federation

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** Only password login exists. Enterprise buyers expect SSO, MFA and provisioning,
  while air-gapped sites may have no IdP at all.
- **Decision:** Keep local password authentication as a supported baseline and break-glass
  path. Add OIDC (authorization code + PKCE), optional SCIM, TOTP/passkeys, session management
  and scoped tokens. Local admin remains available and audited when federation is configured.
- **Consequences:** Identity code must support both modes without divergence in authorization.
  Account linking and deprovisioning need explicit flows. MFA policy is configurable per role.
- **Alternatives considered:** SSO-only (rejected: breaks air-gapped/simple installs);
  local-only (rejected: fails enterprise requirements).

### ADR-020 · Feature flags in configuration, not a SaaS flag service

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** Risky changes need staged rollout, but external flag services violate the
  offline requirement.
- **Decision:** Use typed, environment/project-scoped flags defined in configuration (and
  optionally a database table with an audited admin UI). Flags are removed after rollout;
  stale flags fail a lint check.
- **Consequences:** Every flag has an owner, default, expiry and removal task. Flag state is
  visible in the audit log when changed. Testing must cover both states for critical flags.
- **Alternatives considered:** LaunchDarkly/Unleash SaaS (rejected: outbound dependency);
  long-lived flags (rejected: combinatorial complexity).

### ADR-021 · Dependency policy: pinned, reviewed, mirrored

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** The project ships into air-gapped environments and runs in a public repository
  with a supply-chain threat model.
- **Decision:** Pin runtime dependencies and GitHub Actions to immutable versions/digests
  where practical; review every dependency change; generate SBOMs; scan images; mirror all
  artifacts for offline installs. New dependencies require a written justification covering
  license, maintenance, size and offline behavior.
- **Consequences:** Upgrade cadence is deliberate and batched (Dependabot groups). Emergency
  patches follow the security SLA. Removing a dependency is as valid as adding one.
- **Alternatives considered:** Floating versions (rejected: unreproducible); vendoring
  everything (rejected: maintenance burden except where required for offline builds).

### ADR-022 · Data retention and erasure as first-class jobs

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** There was no retention, erasure, legal hold or export workflow, which is a
  privacy and compliance exposure for an enterprise system of record.
- **Decision:** Classify data, define retention per class, implement scheduled enforcement,
  user/project export, erasure with legal hold and verification across PostgreSQL, Gitea,
  NATS, backups and logs.
- **Consequences:** Deletion is a workflow with evidence, not a `DELETE` statement. Legal hold
  overrides erasure and is audited. Backups expire on a documented schedule; restored data
  must re-apply retention.
- **Alternatives considered:** Manual DBA procedures (rejected: unauditable, error-prone);
  indefinite retention (rejected: compliance and cost).

### ADR-023 · Solo-authored history with Dependabot as the only bot author

- **Status:** ACCEPTED · **Date:** 2026-11-18
- **Context:** The repository has one human maintainer. Unreviewed bot commits would blur
  authorship and undermine the PR contract, but free GitHub-native dependency automation
  materially reduces supply-chain risk and is expected in a public, enterprise-grade project.
  Disabling it to keep a strictly human history would remove vulnerability alerts, automated
  security fixes, dependency review and grouped update PRs for no security benefit.
- **Decision:** All human history is authored by the maintainer and reaches `master` only
  through a green PR. Dependabot is retained as the sole accepted non-human author. Its
  version-update PRs are weekly, grouped per ecosystem for minor/patch, labeled
  (`dependencies`, `type/chore`, `area/*`), assigned and review-requested to the maintainer,
  prefixed `chore(deps)`, and capped per ecosystem. Security updates are grouped separately
  and triaged before routine updates. Dependabot cannot bypass branch protection or approve,
  and every bot PR runs the full PR gate. Any other bot requires a new ADR.
- **Consequences:** `master` contains `chore(deps)` squash commits authored by
  `dependabot[bot]`; the maintainer remains accountable for triaging, merging or closing every
  bot PR and for the patch SLA. Secret scanning, push protection, CodeQL, dependency review,
  vulnerability alerts and automated security fixes remain enabled at no cost.
- **Alternatives considered:** Disabling Dependabot for a strictly human history (rejected:
  loses free security coverage); auto-merging bot PRs (rejected: bypasses the gate and major
  upgrades need judgment); a third-party dependency bot (rejected: extra supply-chain surface
  with no benefit over native automation).

---

## 12. Metrics and release certification

### 12.1 Why these metrics exist

Metrics are used to make decisions, not to decorate dashboards. Each metric below has a
single definition, an owner, a data source and a target. A metric without an owner is deleted.
Metrics are reviewed weekly and at every milestone exit; a red metric produces a task, not an
excuse.

### 12.2 Delivery health metrics

| Metric | Definition | Source | Target | Owner |
|---|---|---|---|---|
| Lead time for change | Median time from first commit on a branch to production deployment | Git + deployment events | ≤ 3 days | Maintainer |
| Cycle time | Median time from `IN PROGRESS` to `VERIFIED` | Task/PR timestamps | ≤ 2 days | Maintainer |
| Deployment frequency | Production deployments per week | Argo CD events | ≥ 3/week once live | Maintainer |
| Change failure rate | Deployments requiring rollback/hotfix ÷ total deployments | Deployment + incident records | ≤ 10% | Maintainer |
| MTTR | Median time from incident start to service restored | Incident records | ≤ 1 hour | Maintainer |
| PR size | Median changed lines excluding lockfiles/generated | GitHub API | ≤ 400 | Maintainer |
| Review latency | Median time from ready-for-review to first review | GitHub API | ≤ 4 working hours | Maintainer |
| PR gate duration | Median wall-clock time for required checks | Actions API | ≤ 20 minutes | Maintainer |
| Flake rate | Flaky test outcomes ÷ total test outcomes | CI history | ≤ 2% | Maintainer |
| Escaped defects | Defects found after release ÷ total defects | Issue tracker | ≤ 5% | Maintainer |
| Rework rate | PRs with changes requested ÷ merged PRs | GitHub API | ≤ 25% | Maintainer |

### 12.3 Product and quality metrics

| Metric | Definition | Source | Target |
|---|---|---|---|
| API availability | Successful requests ÷ total (5xx excluded from success) | Prometheus | ≥ 99.9% monthly |
| API latency p95 | Workspace aggregate endpoint | Prometheus histogram | ≤ 400 ms |
| List/search latency p95 | List and search endpoints | Prometheus histogram | ≤ 300 ms |
| SSE freshness p95 | Event created → client received | Client + server timestamps | ≤ 2 s |
| Search index freshness p95 | Mutation committed → indexed | Index job metrics | ≤ 60 s |
| AI answer latency p95 | Request received → answer returned (CPU-only) | AI metrics | ≤ 3 s |
| AI groundedness | Answers with valid citations ÷ answers | Evaluation harness | ≥ 95% |
| Audit coverage | Privileged mutations with an audit event ÷ total privileged mutations | Audit reconciliation job | 100% |
| Authorization denial rate | 403s ÷ authorized requests (per capability) | Metrics | Monitored, not targeted |
| Test coverage | Line coverage (Go/UI/AI) | Coverage reports | Go ≥ 75%, UI ≥ 80%, AI ≥ 80% |
| Accessibility | Serious/critical axe violations on primary flows | axe CI | 0 |
| Web performance | LCP / INP / CLS | Lighthouse CI | 2.5 s / 200 ms / 0.1 |
| Bundle size | Initial JS gzip | Build report | ≤ 300 KB |
| Backup restore success | Successful drills ÷ attempted drills | Drill reports | 100% |
| Restore time | Measured RTO in drills | Drill reports | ≤ 2 hours |
| Air-gap purity | Outbound requests during isolated install | Network capture | 0 |

### 12.4 Release certification gates

A release is a tagged, certified artifact. Merging to `master` makes a build deployable; it
does not authorize a production release. The release owner runs this checklist and attaches
evidence links. **Any unchecked gate blocks the release** unless an exception is recorded with
an owner, expiry and risk acceptance.

| Gate | Evidence required | Blocking |
|---|---|---|
| Code complete | All milestone cards `DONE` or explicitly deferred with a task ID | Yes |
| PR gate | Latest `master` commit has all required checks green | Yes |
| Integration tests | Real PostgreSQL/NATS run green on the release commit | Yes |
| E2E | Critical journeys green; axe zero serious/critical | Yes |
| Security | No open critical/high vulnerability without an approved exception; secret scan clean | Yes |
| Dependencies | SBOM generated; licenses reviewed; images signed | Yes |
| Data | Migrations rehearsed forward and rollback/forward-fix tested on production-like data | Yes |
| Backup | Pre-release backup verified; restore drill within the last 30 days | Yes |
| Performance | Load test within budget at the reference scale | Yes |
| Accessibility | WCAG 2.2 AA spot check and automated report | Yes |
| Air-gapped | Install/upgrade from mirrored artifacts verified (or explicitly out of scope for this release) | Conditional |
| Observability | Dashboards, alerts and SLOs exist for new/changed surfaces | Yes |
| Documentation | README, runbooks, OpenAPI, changelog and `TASKS.md` updated | Yes |
| Rollback | Previous tag deployable; rollback procedure rehearsed or documented with commands | Yes |
| Sign-off | Release owner + reviewer (second maintainer when available) | Yes |

### 12.5 Release readiness scorecard

Score each dimension 0–2 and publish the result with the tag. A total below 24/30, or a zero in
any dimension, blocks the release.

| Dimension | 0 | 1 | 2 |
|---|---|---|---|
| Tests | Missing/failing | Unit only | Unit + integration + E2E |
| Security | Open critical | Open high with exception | Clean or accepted low/medium |
| Data safety | No rollback plan | Documented | Rehearsed forward + rollback |
| Operations | No runbook | Draft runbook | Drills passed, alerts tested |
| Observability | Blind | Basic metrics/logs | Metrics + traces + SLO alerts |
| Accessibility | Violations | Partial | Zero serious/critical, keyboard verified |
| Performance | Unknown | Measured | Within budget at scale |
| Documentation | Stale | Updated | Updated + evidence links |
| Air-gap | Unknown | Documented | Rehearsed isolated install |
| Supply chain | Unsigned | SBOM only | SBOM + signature + admission policy |

### 12.6 Post-release verification

Within 30 minutes of a production deployment, the release owner verifies:

- [ ] `/health`, `/health/live` and `/health/ready` are green for every replica.
- [ ] Error rate and p95 latency are within SLO for 15 minutes.
- [ ] A synthetic login, project read and search succeed.
- [ ] SSE connections establish and deliver a test event.
- [ ] The deployed commit/version matches the tag.
- [ ] No new alert fired; no audit write failures.
- [ ] Rollback command is confirmed ready, and the previous image digest is known.

If any check fails, roll back first and investigate afterwards. A rollback is a successful
operation, not a failure of process.

### 12.7 Exceptions register

Exceptions are recorded in `docs/EXCEPTIONS.md` with: ID, gate, reason, risk, compensating
control, owner, created date, expiry date and linked task. Expired exceptions fail the release
gate. An exception may not waive secret scanning, authorization checks or data-loss risks.

### 12.8 Reporting cadence

| Cadence | Audience | Content |
|---|---|---|
| Weekly | Maintainer | Delivery metrics, gate health, open P0/P1, WIP, dependency alerts |
| Milestone exit | Owner | Scorecard, demo evidence, risk review, milestone certification |
| Per release | Release owner | Certification checklist, scorecard, post-release verification |
| Incident | Maintainer | Timeline, impact, root cause, corrective tasks with owners |
| Monthly | Stakeholders | Roadmap vs. actual, SLO trends, capacity and cost, top risks |

---

## 13. References

### 13.1 Internal sources of truth

| Document | Purpose | Owner | Status |
|---|---|---|---|
| `TASKS.md` | Delivery sequencing, acceptance criteria, UI direction | Maintainer | Active |
| `docs/ENTERPRISE_SPEC.md` | Product intent and non-negotiable outcomes | Maintainer | Active |
| `docs/ARCHITECTURE.md` | Runtime topology and service responsibilities | Maintainer | Active |
| `docs/API.md` | Contract source map and versioning policy | Maintainer | Active |
| `docs/openapi.yaml` | HTTP contract | Maintainer | Generated, enforced by `BASE-05` |
| `.github/workflows/pr-gate.yml` | Required PR checks | Maintainer | Active |
| `.github/workflows/codeql.yml` | Security analysis | Maintainer | Active |
| `.github/pull_request_template.md` | PR evidence contract | Maintainer | Active |
| `.github/ISSUE_TEMPLATE/` | Bug and feature forms, private security contact | Maintainer | Active (`BASE-07`) |
| `CONTRIBUTING.md` | Branch, PR, commit and verification rules | Maintainer | Active (`BASE-07`) |
| `SUPPORT.md` | Support scope and response expectations | Maintainer | Active (`BASE-07`) |
| `CODE_OF_CONDUCT.md` | Community standards and enforcement | Maintainer | Active (`BASE-07`) |
| `.github/dependabot.yml` | Dependency and security update automation (`ADR-023`) | Maintainer | Active |
| `.github/workflows/release.yml` | Tag-triggered release publishing and evidence | Maintainer | Active (`BASE-08`) |
| `SECURITY.md` | Disclosure, supply-chain and exception policy | Maintainer | Active (`BASE-06`); community links added by `BASE-07` |
| `docs/AIRGAPPED.md` | Offline install/upgrade runbook | Maintainer | Planned (`OPS-02`) |
| `docs/AUDIT.md` | Audit model and retention | Maintainer | Active (`GOV-04`) |
| `docs/AUTHZ.md` | Capability matrix and enforcement | Maintainer | Active (`GOV-01`) |
| `docs/METRICS.md` | Metric catalog and formulas | Maintainer | Planned (`ANL-04`) |
| `docs/SLO.md` | SLOs and error budgets | Maintainer | Planned (`OBS-05`) |
| `docs/RELEASING.md` | Release, promotion, migration order and rollback procedure | Maintainer | Active (`BASE-08`) |
| `CHANGELOG.md` | Generated release history | Maintainer | Active (`BASE-08`) |
| `docs/OBSERVABILITY.md` | Metric catalog, naming, cardinality and exposure policy | Maintainer | Active (`OBS-01`) |

### 13.2 Standards and specifications

| Standard | Version | Use in SPM |
|---|---|---|
| WCAG | 2.2 AA | Accessibility merge gate |
| WAI-ARIA Authoring Practices | Current | Custom widget behavior (combobox, menu, tabs, dialog, grid) |
| OWASP ASVS | 4.0 Level 2 (Level 3 for auth/audit) | Security requirements and test coverage |
| OWASP Top 10 | 2021 | Threat model and regression suite |
| OpenAPI Specification | 3.1 | Executable HTTP contract |
| Semantic Versioning | 2.0.0 | Release tags |
| Conventional Commits | 1.0.0 | Commit and PR titles |
| SLSA | Level 3 target | Supply-chain provenance |
| SCIM | 2.0 | User/group provisioning |
| OpenID Connect Core | 1.0 | SSO |
| RFC 6238 (TOTP) | — | MFA |
| RFC 7519 (JWT) | — | Access tokens, `kid` rotation |
| RFC 3339 | — | API timestamps (UTC) |
| JUnit XML | — | Automated test result ingestion |
| NIST SP 800-63B | — | Password and authentication policy |
| DORA | Four keys | Delivery metrics |
| Prometheus exposition format | — | Metrics endpoint |
| OpenTelemetry | Current | Traces, metrics, logs correlation |
| SPDX / CycloneDX | — | SBOM formats |

### 13.3 Benchmark and practice sources

| Area | Source | Used for |
|---|---|---|
| Navigation and IA | Jira navigation and project sidebar documentation | Sidebar sections, project switcher, global create |
| Issue layout | Jira issue view and field configuration documentation | Three-zone work item detail, context rail |
| Boards | Jira board/backlog and quick-filter documentation | Saved views, WIP limits, quick filters |
| Azure DevOps navigation | Azure DevOps web portal navigation documentation | Hubs, breadcrumbs, project/team selector |
| Azure DevOps work items | Work item form and Boards/Test Plans documentation | Header fields, pivots, test plan hierarchy |
| GitHub repository UX | GitHub Docs: repository header, tabs, code browsing | Code workspace, local tabs, file tree |
| GitHub pull requests | GitHub Docs: PR conversation, reviews, checks, merge box | PR tabs, review threads, merge policy evidence |
| GitHub Projects | GitHub Docs: table/board/roadmap layouts | View switching over a shared filter |
| GitHub command palette | GitHub Docs: keyboard shortcuts and command palette | `Ctrl/Cmd+K` behavior and discoverability |
| Enterprise UX practice | Nielsen Norman Group: data tables, master-detail, progressive disclosure | Density, inline edit, empty states |
| Data tables | W3C WAI table/grid patterns | Sorting, selection, keyboard semantics |
| Chart accessibility | WCAG non-text contrast and text alternatives | Table fallbacks and summaries |
| DORA | *Accelerate* / DORA quick check | Delivery metrics definitions |
| Testing pyramid | Google Testing Blog and Martin Fowler | Unit/integration/E2E balance |
| Trunk-based development | trunkbaseddevelopment.com | Branch lifetime and merge rules |

### 13.4 Tooling references

| Tool | Role | Constraint |
|---|---|---|
| GitHub Actions | PR verification | PR-only triggers; no `pull_request_target` |
| CodeQL | Static analysis | Go, JavaScript/TypeScript, Python |
| Dependabot | Dependency and security updates | Sole bot author; weekly grouped minor/patch PRs, grouped security PRs, labels/assignee, full gate (`ADR-023`) |
| `gofmt`, `go vet`, `go test -race` | Go quality | Required in the gate |
| `vue-tsc`, Vitest, Vite | UI quality | Required in the gate |
| pytest | AI quality | Required in the gate |
| Playwright + axe | E2E and accessibility | Required for critical flows |
| Redocly CLI | OpenAPI lint | Required in the UI job (`BASE-05`) |
| Schemathesis | Contract testing | Added by `QAT-04` |
| Syft / Trivy / Cosign | SBOM, scanning, signing | Syft and Trivy in `BASE-06`; signing and admission in `OPS-07` |
| k6 | Load and soak testing | Added by `OPS-08`/`QAT-06` |
| Helm / kubeconform | Kubernetes packaging | Added by `OPS-04` |
| Prometheus / Grafana / OTel | Observability | Self-hosted; added by `OBS-01`..`OBS-05` |

---

## Appendix A — branch, commit and label conventions

### 14.1 Branch names

```text
feature/<area>-<short-slug>     feature/authz-capabilities
fix/<area>-<short-slug>         fix/membership-session-invalidation
change/<area>-<short-slug>      change/projects-default-table
docs/<short-slug>               docs/openapi-truth
test/<short-slug>               test/ci-integration
perf/<short-slug>               perf/board-scale
chore/<short-slug>              chore/supply-chain-baseline
release/<major.minor>           release/2.4
```

Rules: lower-case kebab-case, ASCII only, no task ID in the branch name (the PR carries it),
no personal prefixes, maximum 60 characters. Branches are deleted after merge.

### 14.2 Commit and squash subject

```text
<type>(<scope>): <imperative summary> (<TASK-ID>)
```

- Types: `feat`, `fix`, `change`, `docs`, `test`, `refactor`, `perf`, `build`, `ci`, `chore`,
  `revert`.
- Scope: `authz`, `auth`, `audit`, `ui`, `board`, `planning`, `tests`, `code`, `delivery`,
  `search`, `analytics`, `ai`, `ops`, `obs`, `ci`, `deps`, `docs`.
- Summary: imperative, lower-case, no trailing period, ≤ 72 characters.
- Task ID is required for PR squash subjects; dependency-only PRs use the ecosystem as the
  task reference (e.g. `(deps)`).
- Breaking changes: add `!` after the scope and a `BREAKING CHANGE:` footer in the PR body.

Examples:

```text
feat(authz): enforce project capability matrix (GOV-01)
fix(auth): invalidate sessions on membership removal (GOV-02)
change(ui): default projects to the table view (UI-02)
docs(ops): add air-gapped installation runbook (OPS-02)
test(ci): run integration tests against PostgreSQL and NATS (QAT-02)
```

### 14.3 Pull request titles

The PR title becomes the squash commit subject, so it must follow the same format. Draft PRs
may use `WIP:` until they are ready; the prefix must be removed before merge. A PR that spans
multiple tasks uses the primary task ID and lists the rest in the body.

### 14.4 Label taxonomy

| Group | Labels | Meaning |
|---|---|---|
| Type | `type/feature`, `type/fix`, `type/change`, `type/docs`, `type/test`, `type/chore` | Change classification; drives changelog |
| Area | `area/api`, `area/ui`, `area/ai`, `area/data`, `area/security`, `area/ops`, `area/docs` | Ownership and review routing |
| Priority | `priority/p0`, `priority/p1`, `priority/p2`, `priority/p3` | Matches the gap register |
| Size | `size/xs` (< 50), `size/s` (< 150), `size/m` (< 400), `size/l` (< 800), `size/xl` (> 800) | Review effort; `xl` requires justification |
| Status | `status/blocked`, `status/needs-design`, `status/needs-data`, `status/needs-security-review` | Current impediment |
| Risk | `risk/low`, `risk/medium`, `risk/high` | Blast radius; high requires rollback rehearsal |
| Special | `dependencies`, `breaking-change`, `security`, `accessibility`, `performance` | Cross-cutting signals |

Rules: every PR has exactly one `type/*`, at least one `area/*`, one `priority/*` and one
`size/*`. Labels are applied by the author and verified by the reviewer. A `priority/p0` PR
preempts other work and must be reviewed the same day. Dependabot applies `dependencies`,
`type/chore` and the relevant `area/*` label automatically from `.github/dependabot.yml`; those
labels must exist in the repository.

### 14.5 Issue and task conventions

- Issue titles use the same `<type>(<scope>): summary` form when they correspond to a task.
- Every issue links its task ID and acceptance criteria; the task remains the source of truth.
- Bugs include: environment, version/commit, steps, expected, actual, logs/trace ID, impact,
  and whether data was affected.
- Security issues are never filed publicly; use the process in `SECURITY.md` (`BASE-06`,
  extended by `BASE-07`).
- Closed issues reference the merging PR; the PR references the issue.

---

## Appendix B — glossary

| Term | Meaning in SPM |
|---|---|
| **Artifact** | Any traceable object: PRD, epic, feature, work item, test case, run, PR, build, deployment, environment |
| **Capability** | A named permission (`pr.merge`) enforced server-side, independent of role names |
| **Evidence** | Immutable proof linked to an artifact (check result, test result, deployment revision, audit event) |
| **Global role** | Account-level role: `admin`, `member`, `viewer` |
| **Project role** | Membership role: `owner`, `maintainer`, `developer`, `tester`, `viewer` |
| **Work item** | A unit of delivery work (story, task, bug, spike) under a feature |
| **Feature** | A deliverable slice under an epic, linked to requirements and tests |
| **Epic** | A large body of work under a PRD |
| **PRD** | Product requirement document; the root of the planning tree |
| **Test case** | A versioned, ordered set of steps with expected results |
| **Test run** | An execution of test cases against a build/environment, with results and evidence |
| **Test plan** | A hierarchy of suites and cases scheduled for execution |
| **Traceability** | The verified link chain from requirement → test → code → build → deployment → health |
| **Coverage** | The proportion of requirements with complete evidence, not merely linked tests |
| **Gap** | A required evidence link that is missing; distinct from "not applicable" |
| **Delivery chain** | The ordered stages from PRD to production health shown on Overview |
| **Promotion** | Moving a verified release from one environment to the next |
| **Rollback** | Reverting an environment to a previous known-good revision |
| **Freeze window** | A period when deployments/scope changes require explicit approval |
| **Separation of duties** | A policy preventing the requester from approving their own change |
| **Outbox** | A transactional table ensuring events are published exactly once after commit |
| **DLQ** | Dead-letter queue for events that repeatedly fail processing |
| **SSE** | Server-Sent Events; the one-way live-update transport |
| **Ticket (SSE)** | A short-lived, single-use credential for opening an SSE stream |
| **Saved view** | A named, server-side combination of filters, columns, sort and layout |
| **Command palette** | The `Ctrl/Cmd+K` overlay for navigation, creation and search |
| **Density** | Row/card spacing mode: comfortable, default, compact |
| **Design token** | A named semantic value for spacing, color, type, radius, elevation or motion |
| **ADR** | Architecture decision record; immutable once accepted |
| **DoR / DoD** | Definition of Ready / Definition of Done |
| **Gate** | A required, automated check that blocks merge or release |
| **Certification** | The signed release checklist with evidence links |
| **RPO / RTO** | Maximum acceptable data loss / maximum acceptable restore time |
| **SLO / error budget** | Service-level objective and the tolerated amount of failure |
| **Air-gapped** | A network with no outbound internet access after artifacts are mirrored |
| **DORA metrics** | Deployment frequency, lead time, change failure rate, MTTR |

---

## Appendix C — task index and status summary

### C.1 Totals

| Phase | Prefix | Cards | Done | In progress | Todo |
|---|---|---|---|---|---|
| 0 - Baseline, truth and delivery gate | `BASE` | 9 | 8 | 0 | 1 |
| 1 - Identity, authorization, audit | `GOV` | 12 | 3 | 0 | 9 |
| 2 — Design system and shell | `UX` | 16 | 0 | 0 | 16 |
| 3 — Screen redesigns | `UI` | 16 | 0 | 0 | 16 |
| 4 - Planning, board, test depth | `PLN`, `BRD`, `TST` | 13 | 2 | 0 | 11 |
| 5 — Code and delivery | `COD`, `DEL` | 12 | 0 | 0 | 12 |
| 6 — Search and analytics | `SRH`, `ANL` | 11 | 0 | 0 | 11 |
| 7 — AI | `AI` | 9 | 0 | 0 | 9 |
| 8 - Observability and operations | `OBS`, `OPS` | 20 | 1 | 0 | 19 |
| 9 — Quality and certification | `QAT` | 10 | 0 | 0 | 10 |
| **Total** | - | **128** | **14** | **0** | **114** |

### C.2 Card index by phase

| Phase | Task IDs |
|---|---|
| 0 | `BASE-01` … `BASE-09` |
| 1 | `GOV-01` … `GOV-12` |
| 2 | `UX-01` … `UX-16` |
| 3 | `UI-01` … `UI-16` |
| 4 | `PLN-01` … `PLN-05`, `BRD-01` … `BRD-03`, `TST-01` … `TST-05` |
| 5 | `COD-01` … `COD-08`, `DEL-01` … `DEL-04` |
| 6 | `SRH-01` … `SRH-05`, `ANL-01` … `ANL-06` |
| 7 | `AI-01` … `AI-09` |
| 8 | `OBS-01` … `OBS-06`, `OPS-01` … `OPS-14` |
| 9 | `QAT-01` … `QAT-10` |

### C.3 P0 blockers (must close before any production release)

| Task | Why it blocks release |
|---|---|
| `GOV-03` | Self-approval and client-supplied review state make reviews meaningless |
| `AI-02` | Without a persistent index, AI results are stale and unbounded |
| `AI-03` | Without permission-aware retrieval, AI can leak cross-project data |
| `AI-07` | Prompt injection is an unmitigated action/exfiltration risk |
| `OPS-01` | Backups exist but restore is unproven |
| `OPS-02` | The air-gapped install path is undocumented and unverified |
| `OPS-13` | Project isolation is not proven across search/AI/SSE/exports |
| `QAT-02` | Integration tests are not in the PR gate |
| `QAT-03` | E2E and axe are not in the PR gate |
| `QAT-07` | No automated authorization/security regression suite |

### C.4 Change log of this document

| Date | Change | Author |
|---|---|---|
| 2026-11-18 | Initial baseline: operating model, gap register, UI research/blueprints, 128 task cards, standards, milestones, risks, ADRs, metrics, references, appendices | Maintainer |
| 2026-11-18 | Retained and fortified Dependabot as the sole accepted bot author; added ADR-023, authorship policy, label/assignment/grouping checks and supply-chain notes | Maintainer |
| 2026-11-18 | Closed BASE-03: enabled strict protection on `master`, enforced admins, proved direct-push and red-PR rejection, and recorded the first protected merge | Maintainer |
| 2026-11-19 | Closed BASE-04: corrected README, architecture, enterprise spec and OpenAPI claims; added verified-evidence dates and links; marked OpenAPI stale pending BASE-05 | Maintainer |
| 2026-11-19 | Resolved the last Dependabot alert with a scoped `minimatch` ^11.0.9 override (BASE-06 slice); the default branch now has 0 open Dependabot alerts | Maintainer |
| 2026-11-19 | Closed BASE-05: moved route registration into `internal/server`, generated the 81-route OpenAPI contract, added route/operation/UI-parity tests, wired drift and Redocly checks into the existing gate, and documented versioning in `docs/API.md` | Maintainer |
| 2026-11-19 | Closed BASE-06 baseline: upgraded Go to 1.27.4 and `x/net` to v0.60.0, made govulncheck/npm audit/pip-audit blocking, added Syft SBOM and Trivy evidence to the Images job, and published `SECURITY.md` with the exception register and image-signing handoff to `OPS-07` | Maintainer |
| 2026-11-19 | Closed BASE-07: added `CONTRIBUTING.md`, `SUPPORT.md`, `CODE_OF_CONDUCT.md`, bug and feature issue forms, and a private-advisory issue contact link; disabled blank issues and cross-linked the policy set from `SECURITY.md`. The public security template was replaced by private reporting so no issue form can solicit exploit details | Maintainer |
| 2026-11-19 | Verified BASE-07 after merge: all three files are present in `.github/ISSUE_TEMPLATE` on `master`; private vulnerability reporting remains enabled and the forms reference existing labels | Maintainer |
| 2026-11-19 | Closed BASE-08: added `docs/RELEASING.md` with the environment, promotion, migration and rollback model; embedded tag-derived version metadata in the API health probes, UI footer and AI service; added `scripts/changelog.ps1`, `scripts/release.ps1`, `CHANGELOG.md` and the tag-triggered `release.yml` that verifies master ancestry, re-runs supply-chain checks and publishes SBOM/SARIF evidence | Maintainer |
| 2026-11-20 | Verified the BASE-08 release dry run: pushed the scratch tag `v0.0.0-rc.1` from `master`; `release.yml` verify, images and publish jobs all succeeded; GitHub created a prerelease with generated changelog notes and six assets (`sbom-api/ui/ai.spdx.json`, `trivy-api/ui/ai.sarif`); the release and both local and remote tags were then deleted, leaving no published release | Maintainer |
| 2026-11-20 | Closed OBS-01: added a Prometheus endpoint on a separate internal listener with optional bearer auth, RED/Go/process/PostgreSQL/NATS/SSE/Gitea/Argo/AI/reconciliation/build-info collectors, bounded route-template labels and `docs/OBSERVABILITY.md`. Post-merge verification confirmed `api/internal/metrics` and the observability contract on `master`, PR #21 merged with all required checks green, and 0 open Dependabot or secret-scanning alerts | Maintainer |
| 2026-11-20 | Closed GOV-01: replaced the coarse project-write gate with 14 explicit capabilities resolved from `project_members` on every request; added DB-backed `RequireProjectCapability`, global viewer/admin overlays, tester and developer restrictions, owner-grant protection, `/auth/me` per-project capabilities, `x-spm-capability` OpenAPI metadata and capability-gated UI actions. Added `docs/AUTHZ.md`, CR/LF sanitization for caller-controlled authorization log fields and exhaustive route × role authorization tests | Maintainer |
| 2026-11-20 | Closed GOV-02: archived projects now deny non-admin access through the per-request membership join and disappear from `/projects` and `/auth/me`; membership add/update/remove/ownership-transfer and admin role changes invalidate JWTs through the NATS-backed KV (one-second boundary covers same-second tokens) and publish `spm.authz.invalidation` so every replica evicts the affected SSE streams, including project-wide eviction on archive. Added `spm_authz_invalidations_total`, two-replica invalidation, eviction and archived-project tests, and updated `docs/AUTHZ.md`, `docs/OBSERVABILITY.md` and `docs/ARCHITECTURE.md` | Maintainer |
| 2026-11-20 | Closed GOV-04: added a monthly-partitioned `audit_events` table whose database trigger owns the `sha256(prev_hash + '|' + payload)` chain and rejects update/delete/truncate; added a fail-open audit writer with recursive redaction, `AUDIT_RETENTION_DAYS`, audited whole-partition drops with chain anchors, admin list/detail/export/verify API and `/admin/audit` UI. Wired auth, admin, project, membership, settings, repository, branch, pull-request, environment, deployment and Argo CD producers; added audit write/chain/prune metrics with failure alerts and published `docs/AUDIT.md` | Maintainer |
| 2026-11-20 | Closed PLN-01: added `revision` tokens and trigger-owned bumps to the eight planning tables, `If-Match`/`expected_version` conditional mutations with `428`/`409`/`400` responses, explicit retry UX, self-parent and all 73 foreign-key indexes, `schema_test.go` drift guards, a `migration_template.sql` and contract companion, and `docs/DATA-MODEL.md`/`docs/MIGRATION-GUIDE.md`; added `spm_planning_conflicts_total`. Merged as PR #26 (`07ecdae`) after a forward/rollback PGlite rehearsal and all required checks | Maintainer |
| 2026-11-21 | Closed PLN-02: added the work item detail drawer with labels, comments/mentions, watchers, bidirectional links, scanned attachments, activity and traceability; migrated `017_work_item_collaboration` with a contract companion and hardened upload validation (type/magic/size, active markup, EICAR, optional ClamAV with fail-closed scanning); documented fields and endpoints in `docs/WORK-ITEMS.md`. Verified with Go/UI tests, OpenAPI drift, Compose config and a PGlite forward/rollback rehearsal. Merged as PR #28 (`0c7c020`) after all nine required checks passed | Maintainer |

> **Maintenance rule:** when a task moves to `DONE`, update its card status, the totals in
> C.1, and (if a P0) mark it closed in C.3 in the same PR. `TASKS.md` is reviewed at every
> milestone exit and must never describe work that is not backed by evidence.








