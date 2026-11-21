# API contract and versioning

`docs/openapi.yaml` is the published HTTP contract. It is generated from the Go route
inventory, not hand-maintained.

## Source of truth

| Layer | Location |
|---|---|
| Gin registration | `api/internal/server/routes.go` |
| Route metadata (method, path, auth level) | `api/internal/server/metadata.go` |
| Generated contract | `docs/openapi.yaml` |
| Generator and drift check | `api/cmd/openapi` |

`TestRoutesMatchRegistration` proves the metadata table and the real Gin engine serve the same
118 method/path pairs. `TestOpenAPIContractMatchesRouter` proves the document and the metadata
describe the same operations in both directions. `TestUIClientPathsExist` proves every API path
used by `ui/src/api/client.ts` is documented.

## Regenerate and verify

From the repository root:

```powershell
# Rewrite docs/openapi.yaml after adding or changing a route
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/openapi.ps1

# Fail if the committed document is stale
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/openapi.ps1 -Check
```

Equivalent commands from `api/`:

```bash
go run ./cmd/openapi -write
go run ./cmd/openapi -check
```

The generator is deterministic: routes are sorted, operations are regenerated as a whole, and
unreachable components are removed. Hand-maintained schemas under `components` are preserved and
refreshed from `api/cmd/openapi/main.go`.

The pull-request gate runs `go run ./cmd/openapi -check` in the Go job and
`npx @redocly/cli lint docs/openapi.yaml` in the UI job. A contract change that is not committed,
or a document that fails OpenAPI validation, fails the gate.

## Versioning policy

- The major version lives in the URL: `/api/v1`. The OpenAPI `info.version` (`2.0.0`) versions the
  document itself, not the URL contract.
- Additive changes (new routes, new optional request fields, new response fields) are allowed
  within `/api/v1`.
- A breaking change (removing or renaming a route, field or enum value; changing a status code;
  tightening validation) requires a new URL major version. Both versions may run side by side
  during a migration window.
- An operation marked `deprecated: true` in the contract must remain functional for at least one
  documented release before removal in the next major version.
- Deprecation notices belong in the operation `description` and in the release notes.

## Authentication

- Bearer JWT: `Authorization: Bearer <token>` on all `/api/v1` routes except login, webhooks and
  the health probes.
- Server-sent events: `POST /api/v1/auth/stream-ticket` issues a single-use ticket; the stream
  accepts it through the `ticket` query parameter. Legacy `?token=` URLs are rejected.
- Webhooks: Gitea and Argo CD authenticate with their configured signature or token; generic
  delivery webhooks use the repository webhook token.

## Optimistic concurrency

Planning artifacts are revisioned: PRDs, epics, features, work items, test
plans, test suites, test cases, test runs, test shared steps, sprints and
releases. `revision` is an internal integer that the database increments on
every update; it is not the human-facing `version` string on a PRD or release.
The model is described in [`DATA-MODEL.md`](DATA-MODEL.md).

A mutating request must send the revision it read:

- `If-Match: "7"` (also accepts `7`, `W/"7"` and `*`), or
- `?expected_version=7`, or
- `expected_version: 7` in the JSON body.

Precedence is header, then query, then body. `If-Match: *` requires the row to
exist but accepts any revision.

| Result | Status | Error code |
|---|---|---|
| No token supplied | `428` | `REVISION_REQUIRED` |
| Token is stale (someone else wrote first) | `409` | `REVISION_CONFLICT` |
| Token is malformed or not a positive integer | `400` | `INVALID_REVISION` |

A `409` body carries `current_version` with the revision to retry from, and the
response `ETag` is that revision. Successful create and update responses return
`{"id": "...", "revision": N}` with the new `ETag`; a revisioned delete returns
`200 {"id": "...", "revision": N, "deleted": true}`. Rejected stale writes
increment `spm_planning_conflicts_total{resource}`.

Test runs are the one exception: they are revisioned and return the token on
create, but have no update endpoint. Their status refresh bumps the revision
internally, and test result/step patches do not require a token.

Work item fields and the collaboration endpoints (comments, mentions, watchers,
links and attachments) are described in [`WORK-ITEMS.md`](WORK-ITEMS.md).
Sprint start/close, carry-over, velocity, release freeze, scope exceptions and
sign-off are described in [`PLANNING-LIFECYCLE.md`](PLANNING-LIFECYCLE.md).
Test plans, ordered suites, filter/CSV membership and run snapshots are
described in [`TEST-MANAGEMENT.md`](TEST-MANAGEMENT.md). The same document covers
case version history (`GET /test-cases/{caseId}/versions`), shared step
templates (`/test-shared-steps`) and the `spm.test-cases.v1` JSON
export/import endpoints.

The UI sends `If-Match` from the freshest workspace snapshot. On `409` it
refreshes the project, keeps the edit dialog open and asks the user to review
and retry; it never silently overwrites.

## Not yet covered

- `schemathesis` or another property-based smoke run against an ephemeral stack is not part of the
  gate. It depends on the integration environment and is tracked as `QAT-04` in
  [`TASKS.md`](../TASKS.md).
- The UI client is hand-written and checked for path parity; generated client types are tracked as
  future work, not claimed here.
