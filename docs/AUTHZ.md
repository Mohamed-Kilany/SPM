# Authorization

SPM authorizes every project-scoped request against the membership row stored in
PostgreSQL. A JWT proves who the caller is; it does **not** grant project
access. This document is the contract for `GOV-01` and the source of truth for
`docs/openapi.yaml` `x-spm-capability` metadata.

## 1. Identity layers

| Layer | Source | Values | Lifetime |
|---|---|---|---|
| Global role | `users.role` and the JWT | `admin`, `member`, `viewer` | Token lifetime |
| Project role | `project_members.role` | `owner`, `maintainer`, `developer`, `tester`, `viewer` | Re-read on every request |
| Project status | `projects.status` | `active`, `archived` | Re-read on every request |

`RequireProjectCapability` parses `:id`, joins `project_members` to `projects`,
derives the effective capability set and attaches it to the request. A removed
member, a changed role or an archived project is effective on the next request
without re-issuing a token. Archived projects disappear from `/projects` and
`/auth/me` for non-admins; a global admin keeps break-glass access.

Global rules:

- **`admin`** keeps break-glass access to every capability. Each break-glass
  request logs `authz_break_glass` with the request ID, user, project and
  capability; the structured log line is the audit trail for this path.
- **`viewer`** is read-only platform-wide. Even a project `owner` membership
  grants only `project.read`, `code.read` and `delivery.read`.
- **`member`** receives the capabilities of the project role.
- An unknown global or project role receives no capabilities and is denied.

## 2. Capability matrix

| Capability | Owner | Maintainer | Developer | Tester | Viewer | Global admin |
|---|---|---|---|---|---|---|
| `project.read` | yes | yes | yes | yes | yes | yes |
| `planning.write` | yes | yes | yes | no | no | yes |
| `work_item.write` | yes | yes | yes | defects/status only | no | yes |
| `test.write` | yes | yes | yes | yes | no | yes |
| `code.read` | yes | yes | yes | yes | yes | yes |
| `code.write` | yes | yes | yes | no | no | yes |
| `pr.review` | yes | yes | yes | no | no | yes |
| `pr.merge` | yes | yes | no | no | no | yes |
| `delivery.read` | yes | yes | yes | yes | yes | yes |
| `delivery.deploy` | yes | yes | non-prod | no | no | yes |
| `delivery.rollback` | yes | yes | no | no | no | yes |
| `settings.write` | yes | yes | no | no | no | yes |
| `member.write` | yes | yes | no | no | no | yes |
| `audit.read` | yes | yes | no | no | no | yes |

`pr.review` is granted to developers but has no HTTP route until `GOV-03`
introduces first-class reviews. `audit.read` and `delivery.rollback` likewise
wait for project-scoped audit routes and the delivery write surface. The
global-admin audit API (`GOV-04`) is a separate, admin-only surface and does not
use project capabilities.

### Tester restriction

Testers hold `work_item.write` so they can file defects and move work. The
handlers enforce the narrower rule:

- Ticket create, edit and delete are denied; ticket **state** changes are
  allowed.
- Work-item create is limited to `type: "bug"`; edit and delete are denied;
  status changes are allowed.
- Defects raised from a test result use `test.write`, which testers hold.

### Developer delivery restriction

Developers hold `delivery.deploy` for non-production environments only.
`POST /projects/:id/environments` rejects `type: "production"` for developers.
Production deployments and rollbacks are not exposed as SPM write routes; they
remain Argo CD projections.

### Ownership

`member.write` lets owners and maintainers manage ordinary members. Only an
owner or a global admin may:

- add a member with the `owner` role,
- change an existing owner's role,
- remove an owner.

Maintainers therefore cannot promote themselves or anyone else to owner. The
last-owner invariant remains enforced by the existing transaction checks.

## 3. Route enforcement

Every project route names exactly one capability in
`api/internal/server/metadata.go` and attaches
`auth.RequireProjectCapability` in `api/internal/server/routes.go`. The
generated OpenAPI document carries the same value as `x-spm-capability`; the
contract test rejects a project route with a missing or invalid capability and
a mutating route without one.

Representative mappings:

| Route family | Capability |
|---|---|
| `GET /projects/:id`, workspace, activity, traceability, reports, AI, settings, members | `project.read` |
| Repository tree/blob/commits/branches, PR files | `code.read` |
| Release readiness | `delivery.read` |
| Tickets and work items | `work_item.write` |
| PRDs, epics, features, sprints, releases, links | `planning.write` |
| Test cases, runs, results, bug-from-result | `test.write` |
| Repositories, branches, pull requests | `code.write` |
| Pull request merge | `pr.merge` |
| Environments | `delivery.deploy` |
| Project update and settings | `settings.write` |
| Members | `member.write` |

Reads resolve membership from PostgreSQL too. The JWT `project_ids` list is
kept for compatibility with older clients but is not used for authorization.

## 4. `/auth/me` contract

`GET /auth/me` and `POST /auth/login` return the effective set per project:

```json
{
  "id": "…",
  "role": "member",
  "project_ids": ["…"],
  "projects": [
    {
      "project_id": "…",
      "role": "developer",
      "capabilities": ["project.read", "planning.write", "work_item.write"]
    }
  ]
}
```

The UI uses `auth.can(projectId, capability)` for visibility and labels the
denied action with the missing capability. UI visibility is convenience only;
the API is the enforcement point.

## 5. Invalidation

Membership changes do more than alter the next database lookup:

- The target user's JWTs are invalidated through the NATS-backed
  `user_invalidation` KV. Every replica reads that bucket, so all API instances
  reject the token on the next request (`TOKEN_INVALIDATED`). The stored
  timestamp is one second ahead of the change so a token issued in the same
  wall-clock second is also rejected.
- An authorization invalidation is published on the core NATS subject
  `spm.authz.invalidation` with `scope: "user"`; every replica closes that
  user's live SSE streams. Removing a member therefore stops both reads and
  stream updates immediately.
- Role changes use the same path. Ownership transfer also invalidates every
  demoted owner.
- Archiving a project publishes `scope: "project"` and closes every stream for
  the project. Subsequent requests are denied because the membership resolver
  reads `projects.status` on every request.
- Add, update and remove operations are best-effort on the NATS side: the
  database mutation has already committed, and the per-request membership
  lookup remains the enforcement point if NATS is unavailable. A publish
  failure logs `authz_invalidation_failed`.
- `spm_authz_invalidations_total{scope,reason}` counts published invalidations.
  `reason` is a fixed operational string such as `member_removed`,
  `member_role_changed`, `ownership_transferred`, `member_added` or
  `project_archived`; it is never user input.

## 6. Denials and observability

- Missing or invalid authentication returns `401`.
- A missing membership, insufficient capability, unknown role or membership
  lookup failure returns `403` with the required capability in the message.
- The middleware logs `authz_denied` with the request ID, user, project,
  project role, capability and path.
- Global admin break-glass requests log `authz_break_glass`.
- Project IDs and paths are caller-controlled, so both fields are stripped of
  CR and LF before logging; a request cannot forge a second log entry.
- Audit persistence for both authorization events remains future work; session
  revocation and SSE teardown are implemented by `GOV-02` (see section 5).

## 7. Verification

`api/internal/auth` tests cover:

- the full role × capability matrix,
- global viewer read-only enforcement,
- global admin break-glass bypass,
- DB membership resolution and non-member denial,
- archived-project denial while break-glass still works,
- immediate effect of role upgrades and downgrades,
- shared invalidation across two API replicas,
- fail-closed behaviour on a lookup error.

`api/internal/sse` tests cover replay safety, active-connection accounting and
eviction by user and project scope.
