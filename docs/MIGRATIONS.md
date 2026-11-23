# Migration safety and operations

This document is the runtime reference for the migration runner. The
change-authoring procedure, expand/contract model and rollback policy live in
[`MIGRATION-GUIDE.md`](MIGRATION-GUIDE.md). Release sequencing lives in
[`RELEASING.md`](RELEASING.md).

## 1. What the runner does

The API embeds `api/internal/migrate/sql/*.sql` and applies every file that is
not recorded in `schema_migrations`, in lexical order, before the HTTP server
starts. `000_create_gitea_db.sql` is a psql/initdb bootstrap and is skipped.

For each migration the runner:

1. Reads the embedded file.
2. Opens a transaction, or a dedicated session when the file starts with
   `-- migrate:no-transaction`.
3. Sets `lock_timeout` and `statement_timeout`.
4. Executes the file with the simple query protocol.
5. Records the filename in `schema_migrations` and commits.
6. Emits `spm_migration_duration_seconds{version,status}` and, on success,
   updates `spm_migration_last_success_timestamp_seconds`.

Before the first migration it creates the infrastructure tables:

```sql
schema_migrations (version varchar primary key, applied_at timestamptz)
migration_backfills (name text primary key, cursor text, completed_at timestamptz, updated_at timestamptz)
```

These are metadata, not versioned migrations. Do not add them to
`schema_migrations` by hand.

If a transactional migration fails, its transaction rolls back and the API
process exits; it does not serve traffic against a partially migrated schema.
A `-- migrate:no-transaction` migration can leave partial objects behind, so its
failure runbook is different (section 6).

## 2. Configuration

| Variable | Default | Purpose |
|---|---|---|
| `MIGRATION_LOCK_TIMEOUT` | `5s` | Maximum wait for a table or index lock. Must be a positive Go duration. |
| `MIGRATION_STATEMENT_TIMEOUT` | `2m` | Maximum execution time for one migration statement. Must be a positive Go duration. |
| `MIGRATION_REQUIRE_BACKUP` | `false` | When `true`, startup fails unless a non-empty backup with a verified SHA-256 is configured. Production must set this. |
| `MIGRATION_BACKUP_PATH` | empty | Path to the pre-migration dump, visible inside the API container. |
| `MIGRATION_BACKUP_SHA256` | empty | Expected digest, with or without a `sha256:` prefix and in any case. |

The runner also accepts a sidecar file at `<MIGRATION_BACKUP_PATH>.sha256`; the
first whitespace-delimited field is used as the expected digest. An inline
`MIGRATION_BACKUP_SHA256` takes precedence over the sidecar.

Invalid durations, a non-boolean `MIGRATION_REQUIRE_BACKUP`, a malformed digest
or a malformed sidecar fail startup with a configuration error. Empty values
select the default or mean "not configured".

`docker-compose.yaml` forwards these variables to `api_backend` and defaults
local development to `MIGRATION_REQUIRE_BACKUP=false`. A production deployment
must mount the dump into the container and set:

```text
MIGRATION_REQUIRE_BACKUP=true
MIGRATION_BACKUP_PATH=/backups/spm-before-<release>.dump
MIGRATION_BACKUP_SHA256=<64 hex characters>
```

The digest is compared in constant time. A missing file, a directory, a
zero-length file, a missing required digest or a mismatch aborts startup before
any migration runs.

## 3. Backup verification

1. Take the dump with the existing backup tooling (`scripts/backup.ps1` or the
   platform equivalent).
2. Compute the SHA-256 on the host that produced the dump.
3. Copy both the dump and the digest to the location the API will read.
4. Deploy with `MIGRATION_REQUIRE_BACKUP=true`.
5. Confirm the API log line `migrate: verified backup ...` before the first
   `migrate: applied ...` line.

The runner verifies integrity, not restorability. Restore drills and RPO/RTO
evidence belong to `OPS-01`; an unverified backup is not a restore point.

## 4. CI linter

The Go job runs:

```bash
go run ./cmd/migrate-lint
```

The linter reads the embedded forward migrations and fails on:

| Rule | Rejected pattern | Why |
|---|---|---|
| `destructive-drop-table` | `DROP TABLE` | Contract step; old code may still read the table. |
| `destructive-drop-column` | `DROP COLUMN` | Contract step; old code may still read the column. |
| `destructive-truncate` | `TRUNCATE <table>` | Data loss in an expand migration. `BEFORE TRUNCATE` trigger syntax is allowed. |
| `destructive-delete` | `DELETE FROM` | Unbounded data change; use a resumable backfill. |
| `destructive-type-change` | `ALTER COLUMN ... TYPE` | Can rewrite the table and break the previous release. |
| `destructive-rename` | `RENAME TO` / `RENAME COLUMN` | Breaking change; add the new name and dual-write first. |
| `concurrent-index-transaction` | `CREATE INDEX CONCURRENTLY` without the directive | PostgreSQL rejects it inside a transaction block. |

The linter blanks comments, quoted strings and dollar-quoted function bodies
before matching, so trigger functions and documentation strings do not produce
false positives. `DROP INDEX`, `DROP CONSTRAINT`, `DROP TRIGGER` and
`DROP FUNCTION IF EXISTS` remain allowed because they are normal expand/cleanup
operations; reviewers still check that a dropped object is no longer used.

Rollback companions under `rollback/` are intentionally destructive and are not
linted or embedded.

## 5. Non-transactional migrations

Use `-- migrate:no-transaction` only when a statement cannot run in a
transaction, currently `CREATE INDEX CONCURRENTLY`:

```sql
-- migrate:no-transaction
-- One statement per file. On failure, drop the invalid index before retrying.
CREATE INDEX CONCURRENTLY IF NOT EXISTS work_items_reporter_idx
  ON work_items (reporter_id);
```

Rules:

- Put the directive on its own line before the statement.
- Keep exactly one non-transactional statement per file. PostgreSQL wraps
  multiple simple-query statements in an implicit transaction, which defeats
  `CONCURRENTLY`.
- There is no atomic rollback. If the statement fails, the index may exist in an
  invalid state. Run `DROP INDEX CONCURRENTLY IF EXISTS <name>` as an operator
  step, then redeploy.
- The statement timeout still applies. Raise
  `MIGRATION_STATEMENT_TIMEOUT` for a large index build rather than removing the
  timeout.
- The runner resets the session timeouts when the connection is released, so a
  later pool checkout does not inherit them.

`VACUUM`, `REINDEX CONCURRENTLY` and other maintenance commands are not startup
migrations. Run them as an explicit operator step.

## 6. Failure and rollback runbook

### Transactional migration fails

1. The API exits and the transaction is rolled back.
2. Read the `migrate: apply <file>` error; the failing statement is named.
3. Fix forward with a new migration or correct the unapplied file in a new
   commit. Never edit an applied file or delete its `schema_migrations` row.
4. Redeploy. Already applied files are skipped.

### Non-transactional migration fails

1. The API exits. Inspect `pg_index` for an invalid index:

   ```sql
   SELECT indexrelid::regclass, indisvalid
   FROM pg_index
   WHERE NOT indisvalid;
   ```

2. Drop the invalid index with `DROP INDEX CONCURRENTLY IF EXISTS`.
3. Fix the statement, raise the statement timeout if needed, and redeploy.
4. Do not insert the version into `schema_migrations` until the object is
   correct.

### Application rollback

1. Redeploy the previous tag. Because the migration was additive, the previous
   code must still work.
2. Leave the expanded schema in place. Do not run a contract script to force a
   rollback.
3. If the previous tag cannot run, fix forward. Restoring the pre-migration
   backup is the last resort and loses writes made after the backup.

### Contract step

Contract scripts are operator actions, not startup migrations. Run them only
after the replacement code has been live for a full release cycle, rollback to
the previous tag is no longer expected and the pre-contract backup is verified.
Record the restore point and the executed script in the release pull request.

## 7. Batched, resumable backfills

Large data changes do not belong in a migration. Use
`migrate.RunBackfill` with a `BackfillStore`:

```go
store := migrate.NewPostgresBackfillStore(pool)
result, err := migrate.RunBackfill(ctx, store, "024-owner-normalization", 500, 20,
    func(ctx context.Context, cursor string, limit int) (string, int, error) {
        // Process at most limit rows after cursor in one transaction.
        // Return the next cursor and the number of rows processed.
        // Return ("", n, nil) when the last batch is done.
    })
```

Properties:

- `migration_backfills` stores the cursor and `completed_at`, so a restart
  resumes after the last committed batch.
- `maxBatches` bounds one invocation; `0` means run until completion. A worker
  can call the same backfill repeatedly with a small limit.
- A callback that returns a cursor equal to the current cursor is rejected, so a
  bug cannot loop forever.
- Completion is recorded when the callback returns zero rows or an empty next
  cursor. Re-running a completed backfill is a no-op.
- Each callback batch must be idempotent and should use its own transaction.
  The framework does not wrap the callback in a transaction.

The framework is not a scheduler. Run it from a one-off job, an admin command or
the release procedure, and record the result in the release evidence.

## 8. Metrics and alerts

| Metric | Type | Labels | Use |
|---|---|---|---|
| `spm_migration_duration_seconds` | histogram | `version`, `status` (`success`, `failure`) | Spot slow migrations and failed attempts. |
| `spm_migration_last_success_timestamp_seconds` | gauge | none | Alert when a deploy has not recorded a successful migration within the expected window. |

Suggested alerts:

```promql
# A migration failed during the last 15 minutes.
increase(spm_migration_duration_seconds_count{status="failure"}[15m]) > 0

# No successful migration for 30 days after a release that should have one.
time() - spm_migration_last_success_timestamp_seconds > 30 * 24 * 3600
```

The second alert only makes sense when a release is expected to apply a
migration; a release with no pending files does not update the gauge. Treat a
missing gauge as "no migration applied yet", not as a failure by itself.

## 9. Evidence and limits

The PR gate proves the linter, configuration parsing, backup verification,
backfill checkpoints, metrics registration and the static schema invariants with
Go tests. It does **not** start PostgreSQL. Real-database up/down, lock-time and
from-zero migration coverage is owned by `QAT-02` and runs in CI with real
services. A local PGlite rehearsal can validate SQL shape but is not a substitute
for the CI evidence.

Before marking a schema-changing release ready:

- [ ] `go run ./cmd/migrate-lint` passes.
- [ ] `go test ./internal/migrate -count=1` passes.
- [ ] The staging rehearsal in [`MIGRATION-GUIDE.md`](MIGRATION-GUIDE.md#53-production-rehearsal)
      is recorded with tags, duration and lock waits.
- [ ] The pre-migration backup digest is verified by the runner.
- [ ] The previous tag runs against the expanded schema.
- [ ] Any pending contract script has an owner, a date and a restore point.
