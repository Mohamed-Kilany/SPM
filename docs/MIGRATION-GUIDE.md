# Migration guide

This guide is the working procedure for changing the SPM PostgreSQL schema. The
release-level compatibility rules live in [`RELEASING.md`](RELEASING.md); the
model invariants live in [`DATA-MODEL.md`](DATA-MODEL.md).

## 1. How migrations run

The API embeds `api/internal/migrate/sql/*.sql` and applies unapplied files in
lexical order at startup, before the server accepts traffic. Each file is
executed inside its own transaction and recorded in `schema_migrations`.

Consequences:

- A migration must be valid PostgreSQL that can run inside a transaction.
  `CREATE INDEX CONCURRENTLY`, `VACUUM` and other non-transactional statements do
  not work here. Use a regular `CREATE INDEX` during a maintenance window, or
  add a separate non-transactional runner first.
- There is no down migration at runtime. Forward files are the only scripts the
  API applies.
- `000_create_gitea_db.sql` is a psql/initdb bootstrap and is skipped by the
  runner.
- Never edit an applied migration. Its version is recorded; an edit silently
  diverges between environments. Add `NNN_*.sql` instead.

## 2. Expand / contract

Every schema change ships in two steps, across releases:

1. **Expand** (`vN`): additive DDL only. New nullable columns or columns with a
   safe default, new tables, new indexes, new functions/triggers. Old and new
   application code must both work against the expanded schema.
2. **Backfill** (`vN`): bounded, idempotent batches outside the request path.
   Record progress; a retry must not duplicate work.
3. **Switch** (`vN`): new code reads and writes the new shape. Keep the old shape
   writable until the contract release.
4. **Contract** (`vN+1` or later): drop the old column, table, index or trigger
   only after `vN` has been stable for a full release cycle and rollback to
   `vN-1` is no longer expected.

A destructive statement never ships in the same release as the code that stops
using the old shape. If a destructive change has already run, roll forward with
a fix; restoring a backup is the last resort.

## 3. Adding a migration

1. Branch from `master` using a `feature/`, `fix/`, `change/` or `chore/`
   branch.
2. Copy `api/internal/migrate/migration_template.sql` to the next free version:

   ```text
   api/internal/migrate/sql/017_<snake_case_name>.sql
   ```

3. Keep the forward file idempotent. The runner applies each file once, but
   local harnesses and rehearsals re-run files, so use `IF NOT EXISTS`,
   `CREATE OR REPLACE`, guarded `DO $$` blocks and `DROP TRIGGER IF EXISTS`
   before `CREATE TRIGGER`.
4. Add the matching rollback companion:

   ```text
   api/internal/migrate/rollback/017_<snake_case_name>.sql
   ```

   The companion is the contract step. It is intentionally **not** embedded in
   the API binary and is never applied automatically.
5. Add or extend tests. Every new invariant belongs in
   `api/internal/migrate/schema_test.go`. If the change adds a foreign key, the
   test's leading-index check must keep passing.
6. Update [`DATA-MODEL.md`](DATA-MODEL.md) when the entity graph, cascade rules
   or concurrency contract changes.
7. Validate forward and backward semantics locally (section 5), then open the
   PR. The PR gate runs `go test ./...`, including the schema tests, but it does
   **not** run PostgreSQL migrations.

### Naming rules

| Object | Pattern | Example |
|---|---|---|
| Migration | `NNN_snake_case.sql` | `016_planning_revisions.sql` |
| Rollback | same filename under `rollback/` | `rollback/016_planning_revisions.sql` |
| Index | `<table>_<columns>_idx` | `work_items_reporter_idx` |
| Trigger | `<domain>_<action>` | `planning_revision` |
| Function | `<domain>_<verb>` | `planning_bump_revision` |

## 4. Worked example: 016_planning_revisions

`016` is the reference expand migration. It:

- adds `revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0)` to the eight
  planning tables;
- installs `planning_bump_revision()` and a `BEFORE UPDATE` trigger on each
  table, so callers cannot set the token;
- adds `work_items_no_self_parent`, guarded by `pg_constraint`, without
  rewriting rows;
- adds the missing foreign-key indexes and is re-runnable.

The companion
[`rollback/016_planning_revisions.sql`](../api/internal/migrate/rollback/016_planning_revisions.sql)
drops the triggers, function, constraint, indexes and columns in dependency
order. It is a contract script: run it only after the code that reads `revision`
is gone and rollback to the previous release is no longer required.

Because the forward migration is additive with defaults, the previous API tag
keeps working against the expanded schema: it ignores `revision`, and the
trigger keeps incrementing it for writes made through the old code.

## 5. Verifying forward and backward

### 5.1 Static schema tests (required, in the PR gate)

From `api/`:

```powershell
go test ./internal/migrate -count=1 -v
```

This checks migration version uniqueness and order, revision coverage, rollback
completeness, that rollback scripts are not embedded, and that every one of the
foreign keys has a leading index.

### 5.2 PostgreSQL semantics (local or staging)

The PR gate has no PostgreSQL service (`QAT-02` and `OPS-09` own the
real-database path). Locally, apply all forward files to an empty database, then
exercise the migration's contract:

1. Apply `001` through the new version, skipping `000`.
2. Re-apply the new forward file and confirm it is a no-op.
3. Check the new columns, defaults, constraints, triggers, functions and
   indexes.
4. Exercise the behavior the migration promises. For `016`: create one row per
   revisioned table, update it and confirm `revision` increments; try to force
   `revision` and confirm it is ignored; update/delete with a stale token and
   confirm zero rows; verify `If-Match: *` uses the `0` sentinel.
5. Run the rollback companion and confirm every object is gone.
6. Re-apply the forward file and confirm the schema expands again.

A throwaway [PGlite](https://pglite.dev/) database is sufficient for steps 1–6
when Docker or a PostgreSQL service is unavailable. `016` was validated this way:
all eight tables, 33 indexes, stale/wildcard updates and deletes, forced-revision
rejection, self-parent rejection, rollback and re-expand passed.

### 5.3 Production rehearsal (`OPS-09`)

Before a release that changes the schema, rehearse in staging:

1. Deploy `vN`; wait for `/health/ready`.
2. Run the smoke check and one write path.
3. Deploy `vN-1` over the expanded schema without restoring the database.
4. Confirm the previous tag still serves reads and writes.
5. Record the date, tags and result in the release pull request.

Do not run a contract script during the rehearsal unless the release explicitly
removes the old shape.

## 6. Rolling back a migration

Preferred order:

1. **Roll back the application.** Redeploy the previous tag. Because the
   forward migration is additive, the old code must still work. If it does not,
   the expansion was not backward compatible; fix forward.
2. **Keep the schema expanded.** Leaving new columns or indexes in place is
   safe and avoids data loss.
3. **Run the contract script only when planned.** It requires the replacement
   code to be live, the rollback window to be closed and a backup to exist.
   Destructive scripts are not applied by the API.

If a forward migration fails at startup, the transaction rolls back and the API
does not start. Fix the SQL in a new commit; do not hand-edit `schema_migrations`.

## 7. Data safety rules

- No table rewrite in a request-path migration. Add columns with defaults
  (metadata-only on PostgreSQL 11+) and backfill in batches.
- No `DROP`, `TRUNCATE` or type narrowing in an expand migration.
- No long locks on hot tables. Add indexes during a maintenance window; the
  current runner cannot use `CONCURRENTLY`.
- Every foreign key gets a leading index. The test enforces this; do not disable
  it.
- Hash-chained audit data is append-only (`015_audit_log.sql`). Retention uses
  the audited partition-drop procedure in [`AUDIT.md`](AUDIT.md); never delete
  audit rows directly.
- Back up before a destructive contract step and record the restore point in
  the release pull request.

## 8. Checklist for reviewers

- [ ] Forward migration is additive and idempotent.
- [ ] Rollback companion exists, is not embedded, and drops everything the
      forward file creates.
- [ ] New foreign keys have a leading index.
- [ ] New columns are nullable or have a safe default.
- [ ] Backfills are bounded and idempotent.
- [ ] `go test ./internal/migrate -count=1` passes.
- [ ] Forward/backward semantics have been exercised against PostgreSQL or
      PGlite.
- [ ] `docs/DATA-MODEL.md` and the OpenAPI contract are updated if the public
      shape changed.
- [ ] The release notes state the expand/contract step and whether a contract
      script is pending.
