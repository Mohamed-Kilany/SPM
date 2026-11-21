-- Contract companion for 020_test_case_versions.sql.
--
-- Do not apply this during a routine rollback. It removes version history and
-- step-result snapshots that the previous API version may still read. It
-- exists so a planned contract step has a reviewed, complete script.

DROP INDEX IF EXISTS test_steps_shared_idx;

ALTER TABLE test_step_results
    DROP COLUMN IF EXISTS test_data,
    DROP COLUMN IF EXISTS expected_result,
    DROP COLUMN IF EXISTS action,
    DROP COLUMN IF EXISTS position,
    DROP COLUMN IF EXISTS case_revision;

ALTER TABLE test_step_results
    DROP CONSTRAINT IF EXISTS test_step_results_test_step_id_fkey;

-- Snapshot-only rows describe a step that no longer exists; the pre-020
-- contract cannot represent them because test_step_id was NOT NULL.
DELETE FROM test_step_results WHERE test_step_id IS NULL;

ALTER TABLE test_step_results
    ADD CONSTRAINT test_step_results_test_step_id_fkey
    FOREIGN KEY (test_step_id) REFERENCES test_steps(id) ON DELETE CASCADE;

ALTER TABLE test_step_results
    ALTER COLUMN test_step_id SET NOT NULL;

ALTER TABLE test_steps
    DROP COLUMN IF EXISTS shared_step_id;

DROP INDEX IF EXISTS test_shared_steps_creator_idx;
DROP INDEX IF EXISTS test_shared_steps_project_idx;
DROP TRIGGER IF EXISTS planning_revision ON test_shared_steps;
DROP TABLE IF EXISTS test_shared_steps;

DROP TABLE IF EXISTS test_case_version_steps;

DROP INDEX IF EXISTS test_case_versions_author_idx;
DROP INDEX IF EXISTS test_case_versions_owner_idx;
DROP INDEX IF EXISTS test_case_versions_feature_idx;
DROP INDEX IF EXISTS test_case_versions_case_idx;
DROP TABLE IF EXISTS test_case_versions;
