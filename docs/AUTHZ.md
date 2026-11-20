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

`RequireProjectCapability` parses `:id`, reads `project_members`, derives the
effective capability set and attaches it to the request. A removed member or a
changed role is effective on the next request without re-issuing a token.

Global rules:

- **`admin`** keeps break-glass access to every capability. Each break-glass
  request logs `authz_break_glass` with the request ID, user, project and
  capability; the append-only audit record arrives with `GOV-04`.
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
wait for `GOV-04` and the delivery write surface.

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

## 5. Denials and observability

- Missing or invalid authentication returns `401`.
- A missing membership, insufficient capability, unknown role or membership
  lookup failure returns `403` with the required capability in the message.
- The middleware logs `authz_denied` with the request ID, user, project,
  project role, capability and path.
- Global admin break-glass requests log `authz_break_glass`.
- Audit persistence for both events is `GOV-04`; session revocation and SSE
  teardown on membership removal are `GOV-02`.

## 6. Verification

`api/internal/auth` tests cover:

- the full role × capability matrix,
- global viewer read-only enforcement,
- global admin break-glass bypass,
- DB membership resolution and non-member denial,
- immediate effect of role upgrades and downgrades,
- fail-closed behaviour on a lookup error.
