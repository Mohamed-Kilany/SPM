-- Contract / rollback companion for 016_planning_revisions.
--
-- The API applies migrations only from ../sql. This file is executed by the
-- migration-safety rehearsal (OPS-09) and by local forward/backward tests; it
-- is intentionally not embedded in the running API.
--
-- Production follows expand/contract: deploy code that no longer reads or
-- writes `revision` before running the DROP statements below.

DROP TRIGGER IF EXISTS planning_revision ON prds;
DROP TRIGGER IF EXISTS planning_revision ON epics;
DROP TRIGGER IF EXISTS planning_revision ON features;
DROP TRIGGER IF EXISTS planning_revision ON work_items;
DROP TRIGGER IF EXISTS planning_revision ON test_cases;
DROP TRIGGER IF EXISTS planning_revision ON test_runs;
DROP TRIGGER IF EXISTS planning_revision ON sprints;
DROP TRIGGER IF EXISTS planning_revision ON releases;

DROP FUNCTION IF EXISTS planning_bump_revision();

ALTER TABLE work_items DROP CONSTRAINT IF EXISTS work_items_no_self_parent;

DROP INDEX IF EXISTS project_members_user_idx;
DROP INDEX IF EXISTS prds_owner_idx;
DROP INDEX IF EXISTS prds_approver_idx;
DROP INDEX IF EXISTS epics_owner_idx;
DROP INDEX IF EXISTS epics_target_release_idx;
DROP INDEX IF EXISTS features_owner_idx;
DROP INDEX IF EXISTS features_target_release_idx;
DROP INDEX IF EXISTS work_items_assignee_user_idx;
DROP INDEX IF EXISTS work_items_reporter_idx;
DROP INDEX IF EXISTS work_items_environment_idx;
DROP INDEX IF EXISTS test_cases_owner_idx;
DROP INDEX IF EXISTS test_runs_sprint_idx;
DROP INDEX IF EXISTS test_runs_environment_idx;
DROP INDEX IF EXISTS test_runs_build_idx;
DROP INDEX IF EXISTS test_runs_executor_idx;
DROP INDEX IF EXISTS test_results_executor_idx;
DROP INDEX IF EXISTS test_results_bug_idx;
DROP INDEX IF EXISTS test_step_results_step_idx;
DROP INDEX IF EXISTS test_step_results_bug_idx;
DROP INDEX IF EXISTS git_branches_creator_idx;
DROP INDEX IF EXISTS pull_requests_source_branch_idx;
DROP INDEX IF EXISTS pr_reviews_reviewer_idx;
DROP INDEX IF EXISTS builds_triggered_by_idx;
DROP INDEX IF EXISTS deployments_build_idx;
DROP INDEX IF EXISTS deployments_release_idx;
DROP INDEX IF EXISTS deployments_deployer_idx;
DROP INDEX IF EXISTS deployments_previous_idx;
DROP INDEX IF EXISTS deployments_rollback_idx;
DROP INDEX IF EXISTS deployment_approvals_approver_idx;
DROP INDEX IF EXISTS artifact_links_creator_idx;
DROP INDEX IF EXISTS activity_events_actor_idx;
DROP INDEX IF EXISTS comments_author_idx;
DROP INDEX IF EXISTS attachments_uploader_idx;

ALTER TABLE prds DROP COLUMN IF EXISTS revision;
ALTER TABLE epics DROP COLUMN IF EXISTS revision;
ALTER TABLE features DROP COLUMN IF EXISTS revision;
ALTER TABLE work_items DROP COLUMN IF EXISTS revision;
ALTER TABLE test_cases DROP COLUMN IF EXISTS revision;
ALTER TABLE test_runs DROP COLUMN IF EXISTS revision;
ALTER TABLE sprints DROP COLUMN IF EXISTS revision;
ALTER TABLE releases DROP COLUMN IF EXISTS revision;

-- Do not delete the schema_migrations row here. A rolled-back migration must
-- be re-applied forward, not silently forgotten.
