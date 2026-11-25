# Testing and CI evidence

This document describes what the pull-request gate proves, how the live
integration stack is built, and how to reproduce it locally. `TASKS.md` remains
the source of truth for task status; this guide explains the mechanics behind
the `QAT-02` evidence.

## Test layers

| Layer | Command | Runs in the PR gate |
|---|---|---|
| Go unit and package tests | `cd api && go test ./... -race -count=1` | Yes, `Go: fmt, vet, test, build` |
| Go integration suite (build tag `integration`) | `bash scripts/ci-integration.sh` | Yes, `Integration: real dependencies` |
| UI unit tests | `cd ui && npm test` | Yes, `UI: typecheck, test, build` |
| AI service tests | `cd ai && python -m pytest -q` | Yes, `AI: pytest` |
| Browser E2E and axe | `cd ui && npm run test:e2e` | No, tracked by `QAT-03` |

The integration job is path-aware: it runs the live stack when a pull request
changes `api/`, `nats/`, `docker-compose.test.yml`, `Dockerfile.test`,
`scripts/ci-integration.sh` or the PR workflow itself. Other pull requests get a
fast green check so the required context is always present and the median gate
stays within its budget.

## Integration stack

`docker-compose.test.yml` defines an isolated Compose project (`spm-test`) with
its own network and volumes. The CI job starts only the services the Go suite
needs:

| Service | Purpose |
|---|---|
| `test_postgres` | Real PostgreSQL 18, fresh volume on every run |
| `test_nats` | Real NATS JetStream with the repository configuration |
| `test_gitea` | Real Gitea instance for webhook and repository integration |
| `test_api_backend` | API built from the pull request, migrations applied at startup |
| `test_runner` | Compiles and runs `api/tests/integration` with the `integration` tag |

The UI and edge services are intentionally not started by the CI job; they are
covered by the UI job and will be exercised by the Compose-backed E2E job in
`QAT-03`. The full test stack still includes them for manual inspection.

PostgreSQL and NATS bind only to `127.0.0.1` on the runner (`55432` and `54222`
by default) so the upgrade rehearsal can reach them. They are not published by
the production stack.

## Deterministic data

The test database starts from the embedded migrations. `007_seed_data.sql`
creates four accounts with fixed test-only credentials:

| User | Password | Role |
|---|---|---|
| `admin` | `Admin@1234` | admin |
| `demo` | `Demo@1234` | member |
| `developer` | `Demo@1234` | member |
| `tester` | `Demo@1234` | viewer |

The suite creates unique users and projects per run. Volumes are removed after
every run, so no state leaks between pull requests. The credentials are test
fixtures, never production secrets.

## Test isolation and session invalidation

Membership and ownership changes invalidate the affected user's tokens so the
new roles take effect on the next request. Because JWTs carry second-precision
issue times, a sign-in made in the same wall-clock second as the change is also
rejected; the ownership tests wait for the next second before signing in again.
This is product behavior, not a test-only switch. The suite still asserts that
the pre-change token is rejected, then verifies the new role with a fresh
token.

## What the integration gate proves

1. **Migrations from zero.** The API starts against a fresh database and every
   embedded migration is applied and recorded in `schema_migrations`. The script
   compares the recorded count with the migration files and fails on a mismatch.
2. **Upgrade from the previous commit.** The script reads the migration files at
   `BASE_SHA` (the pull request base), applies them to a scratch database,
   records those versions, inserts a sentinel project, and starts the current
   API against that database. The current API must apply only the new
   migrations, become healthy, preserve the sentinel row and end with the
   current migration count. This is the N−1 database rehearsal; it does not
   replace the full release rehearsal in `docs/RELEASING.md`.
3. **Real dependencies.** The suite talks to PostgreSQL, NATS and Gitea over the
   Compose network. No database or message-bus mock is used.
4. **Failure diagnostics.** On failure the workflow uploads
   `integration-diagnostics` containing the Compose logs, the verbose Go test
   log (including failure stack traces) and the upgraded API log. There is no
   separate distributed-trace export yet; tracing is tracked by `OBS-02`.

## Running locally

The integration stack requires a local Docker engine. From the repository root:

```bash
# Full isolated stack: PostgreSQL, NATS, Gitea, API, UI, edge and the suite.
docker compose -f docker-compose.test.yml up --build --exit-code-from test_runner

# Or reproduce the CI path, including migration and upgrade checks.
BASE_SHA="$(git rev-parse HEAD~1)" bash scripts/ci-integration.sh

# Keep the stack after a failed run for inspection.
KEEP_STACK=1 BASE_SHA="$(git rev-parse HEAD~1)" bash scripts/ci-integration.sh
```

The script removes the test volumes on success and on failure unless
`KEEP_STACK=1` is set. The Compose project name keeps it isolated from a local
`spm` stack.

To run only the tagged suite against an already-running stack:

```bash
docker compose -f docker-compose.test.yml run --rm --no-deps test_runner
```

## Known gaps

- `TestResourceLimits_IdleMemoryWithinBudget` needs the Docker CLI inside the
  test runner and skips when it is unavailable. The configured-limit sum is
  still checked, and the Compose definitions are validated separately. A
  dedicated resource-budget job belongs with `OPS-08`/`OPS-14`.
- Browser E2E, axe and trace/video artifacts are `QAT-03`.
- The upgrade rehearsal uses the pull request base commit as N−1. Once releases
  are published, the same script can be pointed at a release tag with
  `BASE_SHA`.
- Distributed tracing and trace export are `OBS-02`; the current failure
  artifact is log- and test-output-based.
