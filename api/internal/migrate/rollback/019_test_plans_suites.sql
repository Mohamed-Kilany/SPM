-- Contract companion for 019_test_plans_suites.sql.
--
-- Do not apply this during a routine rollback. It removes columns and tables
-- that the previous API version may still read. It exists so a planned
-- contract step has a reviewed, complete script.

DROP INDEX IF EXISTS test_runs_suite_idx;
DROP INDEX IF EXISTS test_runs_plan_idx;

ALTER TABLE test_results
    DROP COLUMN IF EXISTS case_revision;

ALTER TABLE test_runs
    DROP COLUMN IF EXISTS suite_name,
    DROP COLUMN IF EXISTS plan_name,
    DROP COLUMN IF EXISTS suite_id,
    DROP COLUMN IF EXISTS plan_id;

DROP INDEX IF EXISTS test_suite_cases_adder_idx;
DROP INDEX IF EXISTS test_suite_cases_case_idx;
DROP TABLE IF EXISTS test_suite_cases;

DROP INDEX IF EXISTS test_suites_assignee_idx;
DROP INDEX IF EXISTS test_suites_plan_idx;
DROP TRIGGER IF EXISTS planning_revision ON test_suites;
DROP TABLE IF EXISTS test_suites;

DROP INDEX IF EXISTS test_plans_creator_idx;
DROP INDEX IF EXISTS test_plans_owner_idx;
DROP INDEX IF EXISTS test_plans_project_idx;
DROP TRIGGER IF EXISTS planning_revision ON test_plans;
DROP TABLE IF EXISTS test_plans;
