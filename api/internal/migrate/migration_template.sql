-- Expand-only migration template.
--
-- Copy this file to sql/NNN_<name>.sql. Never edit an applied migration.
--
-- Rules enforced by review and api/internal/migrate/schema_test.go:
--   1. Additive only: new columns, tables, indexes, functions, triggers.
--   2. New columns are nullable or have a safe DEFAULT.
--   3. Every foreign-key column gets an index on its leading column.
--   4. Backfills are idempotent and bounded; run them outside the request path.
--   5. Destructive changes ship later, in a separate contract migration under
--      migrate/rollback, after the old code path is gone.
--   6. Provide the matching rollback script and rehearse forward/backward.

BEGIN;

ALTER TABLE example_table
    ADD COLUMN IF NOT EXISTS example_column VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS example_table_example_column_idx
    ON example_table (example_column);

COMMIT;
