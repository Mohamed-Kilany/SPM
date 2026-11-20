-- Contract companion for 018_sprint_release_lifecycle.sql.
--
-- Do not apply this during a routine rollback. It removes columns and a table
-- that the previous API version may still read. It exists so a planned
-- contract step has a reviewed, complete script.

DROP INDEX IF EXISTS sprints_completed_idx;
DROP INDEX IF EXISTS releases_frozen_idx;

DROP INDEX IF EXISTS release_scope_exceptions_type_idx;
DROP INDEX IF EXISTS release_scope_exceptions_entity_idx;
DROP INDEX IF EXISTS release_scope_exceptions_created_by_idx;
DROP INDEX IF EXISTS release_scope_exceptions_project_idx;
DROP INDEX IF EXISTS release_scope_exceptions_release_idx;
DROP TABLE IF EXISTS release_scope_exceptions;

ALTER TABLE releases
    DROP COLUMN IF EXISTS signed_off_by,
    DROP COLUMN IF EXISTS signed_off_at,
    DROP COLUMN IF EXISTS sign_off_evidence,
    DROP COLUMN IF EXISTS freeze_reason,
    DROP COLUMN IF EXISTS frozen_by,
    DROP COLUMN IF EXISTS frozen_at,
    DROP COLUMN IF EXISTS milestone_at;

ALTER TABLE sprints
    DROP COLUMN IF EXISTS carry_over_count,
    DROP COLUMN IF EXISTS close_policy,
    DROP COLUMN IF EXISTS completed_by,
    DROP COLUMN IF EXISTS completed_at,
    DROP COLUMN IF EXISTS activated_at;
