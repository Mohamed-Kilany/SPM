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
81 method/path pairs. `TestOpenAPIContractMatchesRouter` proves the document and the metadata
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

## Not yet covered

- `schemathesis` or another property-based smoke run against an ephemeral stack is not part of the
  gate. It depends on the integration environment and is tracked as `QAT-04` in
  [`TASKS.md`](../TASKS.md).
- The UI client is hand-written and checked for path parity; generated client types are tracked as
  future work, not claimed here.
