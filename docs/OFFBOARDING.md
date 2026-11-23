# Ownership transfer and offboarding

This document describes the two governance flows that move responsibility for a
project or a departing account: explicit ownership transfer and transactional
offboarding. Both are enforced by the API, audited and surfaced in the UI.

## Ownership transfer

`projects.owner_id` is a projection. The authoritative owner is the
`project_members` row with `role = 'owner'`. The transfer helpers keep the two
in sync and repair historical drift when a transfer is requested.

### Lifecycle

```text
owner/admin requests ──► pending ──► target accepts ──► accepted
                              │
                              ├──► target declines ──► declined
                              ├──► admin confirms  ──► confirmed
                              └──► requester/owner/admin cancels ──► cancelled
```

- A request is stored in `ownership_transfers` (migration `024`).
- Only one request may be pending per project; a partial unique index enforces
  the rule, and a second request returns `409 TRANSFER_PENDING`.
- The target must be an active project member. Self-transfer returns
  `409 ALREADY_OWNER`.
- Accepting or confirming demotes the previous owner to `maintainer`, promotes
  the target to `owner`, updates `projects.owner_id`, and resolves the request in
  one transaction.
- If ownership changed after the request was created, resolution returns
  `409 OWNERSHIP_CHANGED`. Archived projects return `409 PROJECT_ARCHIVED`.
- A resolved request cannot be resolved again (`409 TRANSFER_RESOLVED`).

### Permission matrix

| Action | Who may call it |
|---|---|
| Request | Project owner or global admin |
| Accept / decline | The transfer target |
| Confirm on the target's behalf | Global admin |
| Cancel | Requester, current owner or global admin |

### Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/projects/{id}/ownership-transfers` | Pending request and recent history |
| `POST` | `/api/v1/projects/{id}/ownership-transfers` | Request a transfer |
| `POST` | `/api/v1/projects/{id}/ownership-transfers/{transferId}/accept` | Target accepts |
| `POST` | `/api/v1/projects/{id}/ownership-transfers/{transferId}/decline` | Target declines |
| `POST` | `/api/v1/projects/{id}/ownership-transfers/{transferId}/confirm` | Admin confirms |
| `DELETE` | `/api/v1/projects/{id}/ownership-transfers/{transferId}` | Cancel a pending request |
| `GET` | `/api/v1/auth/ownership-transfers` | Caller's incoming and outgoing pending requests |

The profile inbox is available without project access so a target can act on a
request before visiting the project.

## Offboarding

Deactivating an account through the generic `PATCH /admin/users/{userId}` is
blocked with `409 REASSIGNMENT_REQUIRED` whenever the user still owns work.
Use the offboarding endpoints instead.

### Preview

`GET /api/v1/admin/users/{userId}/offboarding` returns:

- the user's global role, active state and last-admin status;
- owned projects, with owner count and a sole-owner flag;
- open work items assigned to the user (backlog, ready, in progress, in review,
  in QA, blocked, new, triaged, fixed and reopened);
- pending deployment approvals held by the user;
- owned planning artifacts (`prds`, `epics`, `features`, `test_cases`,
  `test_plans`);
- active session count;
- `requires_reassignment` and human-readable warnings.

`requires_reassignment` is true when any owned project, open assignment,
pending approval or owned artifact exists. Sessions alone do not require a
successor; they are revoked regardless.

### Transaction

`POST /api/v1/admin/users/{userId}/offboarding` accepts:

```json
{
  "reason": "left the company",
  "reassign_to": "00000000-0000-0000-0000-000000000000",
  "project_owners": {
    "project-uuid": "successor-user-uuid"
  }
}
```

- `reason` is required and must be 3–500 characters.
- `reassign_to` is required when the preview reports reassignment is needed.
- `project_owners` optionally overrides the successor per owned project; values
  must be active users other than the departing user.
- The transaction locks the user row, rechecks the last-admin rule and the
  preview, then:
  1. adds the successor as a developer on projects where they receive open
     items, approvals or artifacts;
  2. reassigns open work items and pending deployment approvals;
  3. reassigns owned artifacts;
  4. moves every owned project to the chosen successor and updates
     `projects.owner_id` and the owner membership;
  5. deletes the departing user's remaining memberships;
  6. deactivates the user, sets `invalidated_at`, and revokes every open
     `auth_sessions` row;
  7. commits.
- SSE streams for revoked sessions are evicted immediately, and the user's
  token cache is invalidated.
- A deactivated user cannot log in, refresh, stream or use an existing access
  token.

### Protections

| Condition | Result |
|---|---|
| Deactivating yourself | `400 SELF_LOCKOUT` |
| User already inactive | `409 ALREADY_INACTIVE` |
| Last active global admin | `409 LAST_ADMIN` |
| Impact exists without `reassign_to` | `409 REASSIGNMENT_REQUIRED` |
| Successor is the departing user or inactive | `400 INVALID_SUCCESSOR` |

### Audit events

| Action | Target |
|---|---|
| `project.ownership.request` | `ownership_transfer` |
| `project.ownership.accept` | `ownership_transfer` |
| `project.ownership.decline` | `ownership_transfer` |
| `project.ownership.confirm` | `ownership_transfer` |
| `project.ownership.cancel` | `ownership_transfer` |
| `project.ownership.transfer` | `project` (also emitted per project during offboarding) |
| `admin.user.offboard` | `user` |

Each offboarding audit entry records the reason, before/after state, reassigned
counts and the revoked session count.

## UI

- **Project settings → Project ownership** shows the current owner, the pending
  request, recent history and the request form. Every disabled action displays
  the permission it needs (`Requires project owner or global admin`,
  `Only the transfer target can accept…`, and so on).
- **Profile → Ownership transfer requests** lists incoming requests with
  Accept/Decline and outgoing pending requests with Cancel.
- **Administration → Users & roles** opens the offboarding preview before any
  deactivation. The modal lists projects, open items, approvals and artifacts,
  lets the admin choose a default successor and per-project overrides, requires
  a reason and blocks the last active admin. Self-deactivation is disabled with
  an accessible reason.

## Rollout and rollback

Migration `024_ownership_transfers.sql` is additive. The rollback companion
drops only the new table; `projects.owner_id` and memberships are unaffected.
Deployments may run the previous API version against the new schema because it
ignores the table.

## Verification

- `api/internal/workspace/handler_ownership_test.go` covers the transfer
  permission matrix and reason validation.
- `api/internal/auth/offboarding_test.go` covers reason, successor and override
  parsing plus the reassignment predicate.
- `api/internal/migrate/schema_test.go` pins the table, constraints and indexes.
- `api/tests/integration/ownership_test.go` exercises request/accept, admin
  confirm and the full offboarding reassignment and session-revocation flow
  against a running stack.
- `ui/src/lib/permissions.test.ts` covers the UI permission reasons.
