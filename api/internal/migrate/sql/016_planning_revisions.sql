-- Migration: 016_planning_revisions
-- Optimistic concurrency for planning artifacts and foreign-key index coverage.
--
-- Part of this migration is the integrity backfill for pre-existing tables:
-- PostgreSQL does not index the referencing side of a foreign key, so deleting
-- a referenced user or project would otherwise scan every dependent table.
--
-- `revision` is the internal concurrency token for a row. It is deliberately
-- separate from the human-facing `prds.version` and `releases.version`
-- strings. Every BEFORE UPDATE trigger overwrites the column with OLD + 1 so
-- a client cannot bypass the bump by supplying its own value.
--
-- This migration is expand-only: it adds columns, indexes and a function. It
-- never rewrites existing rows. The matching contract script is
-- api/internal/migrate/rollback/016_planning_revisions.sql.

-- ---------------------------------------------------------------------------
-- Revision columns
-- ---------------------------------------------------------------------------

ALTER TABLE prds
    ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0);
ALTER TABLE epics
    ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0);
ALTER TABLE features
    ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0);
ALTER TABLE work_items
    ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0);
ALTER TABLE test_cases
    ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0);
ALTER TABLE test_runs
    ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0);
ALTER TABLE sprints
    ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0);
ALTER TABLE releases
    ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0);

-- The trigger owns the token. Calling code only reads it and compares it in
-- the UPDATE/DELETE predicate.
CREATE OR REPLACE FUNCTION planning_bump_revision() RETURNS trigger AS $$
BEGIN
    NEW.revision := OLD.revision + 1;
    RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS planning_revision ON prds;
CREATE TRIGGER planning_revision BEFORE UPDATE ON prds
    FOR EACH ROW EXECUTE FUNCTION planning_bump_revision();

DROP TRIGGER IF EXISTS planning_revision ON epics;
CREATE TRIGGER planning_revision BEFORE UPDATE ON epics
    FOR EACH ROW EXECUTE FUNCTION planning_bump_revision();

DROP TRIGGER IF EXISTS planning_revision ON features;
CREATE TRIGGER planning_revision BEFORE UPDATE ON features
    FOR EACH ROW EXECUTE FUNCTION planning_bump_revision();

DROP TRIGGER IF EXISTS planning_revision ON work_items;
CREATE TRIGGER planning_revision BEFORE UPDATE ON work_items
    FOR EACH ROW EXECUTE FUNCTION planning_bump_revision();

DROP TRIGGER IF EXISTS planning_revision ON test_cases;
CREATE TRIGGER planning_revision BEFORE UPDATE ON test_cases
    FOR EACH ROW EXECUTE FUNCTION planning_bump_revision();

DROP TRIGGER IF EXISTS planning_revision ON test_runs;
CREATE TRIGGER planning_revision BEFORE UPDATE ON test_runs
    FOR EACH ROW EXECUTE FUNCTION planning_bump_revision();

DROP TRIGGER IF EXISTS planning_revision ON sprints;
CREATE TRIGGER planning_revision BEFORE UPDATE ON sprints
    FOR EACH ROW EXECUTE FUNCTION planning_bump_revision();

DROP TRIGGER IF EXISTS planning_revision ON releases;
CREATE TRIGGER planning_revision BEFORE UPDATE ON releases
    FOR EACH ROW EXECUTE FUNCTION planning_bump_revision();

REVOKE ALL ON FUNCTION planning_bump_revision() FROM PUBLIC;

-- A work item cannot be its own parent. The application already rejects this
-- in validateWorkItem; the constraint keeps direct writes honest.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'work_items_no_self_parent'
    ) THEN
        ALTER TABLE work_items
            ADD CONSTRAINT work_items_no_self_parent
            CHECK (parent_id IS NULL OR parent_id <> id);
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- Foreign-key indexes
-- ---------------------------------------------------------------------------
-- PostgreSQL does not index the referencing side of a foreign key. Every
-- column below is used by an ON DELETE action, a project scoped list or a
-- traceability query, so an index is required for both integrity maintenance
-- and predictable plans. Indexes already supplied by 012/013 are not repeated.

CREATE INDEX IF NOT EXISTS project_members_user_idx ON project_members (user_id);

CREATE INDEX IF NOT EXISTS prds_owner_idx ON prds (owner_id);
CREATE INDEX IF NOT EXISTS prds_approver_idx ON prds (approved_by);

CREATE INDEX IF NOT EXISTS epics_owner_idx ON epics (owner_id);
CREATE INDEX IF NOT EXISTS epics_target_release_idx ON epics (target_release_id);

CREATE INDEX IF NOT EXISTS features_owner_idx ON features (owner_id);
CREATE INDEX IF NOT EXISTS features_target_release_idx ON features (target_release_id);

-- work_items_assignee_idx is (project_id, assignee_id); the FK action needs a
-- standalone leading index as well.
CREATE INDEX IF NOT EXISTS work_items_assignee_user_idx ON work_items (assignee_id);
CREATE INDEX IF NOT EXISTS work_items_reporter_idx ON work_items (reporter_id);
CREATE INDEX IF NOT EXISTS work_items_environment_idx ON work_items (environment_id);

CREATE INDEX IF NOT EXISTS test_cases_owner_idx ON test_cases (owner_id);

CREATE INDEX IF NOT EXISTS test_runs_sprint_idx ON test_runs (sprint_id);
CREATE INDEX IF NOT EXISTS test_runs_environment_idx ON test_runs (environment_id);
CREATE INDEX IF NOT EXISTS test_runs_build_idx ON test_runs (build_id);
CREATE INDEX IF NOT EXISTS test_runs_executor_idx ON test_runs (executed_by);

CREATE INDEX IF NOT EXISTS test_results_executor_idx ON test_results (executed_by);
CREATE INDEX IF NOT EXISTS test_results_bug_idx ON test_results (bug_work_item_id);

CREATE INDEX IF NOT EXISTS test_step_results_step_idx ON test_step_results (test_step_id);
CREATE INDEX IF NOT EXISTS test_step_results_bug_idx ON test_step_results (bug_work_item_id);

-- Delivery and collaboration FKs. These tables are not revisioned but share
-- the same integrity requirement.
CREATE INDEX IF NOT EXISTS git_branches_creator_idx ON git_branches (created_by);
CREATE INDEX IF NOT EXISTS pull_requests_source_branch_idx ON pull_requests (source_branch_id);
CREATE INDEX IF NOT EXISTS pr_reviews_reviewer_idx ON pull_request_reviews (reviewer_id);
CREATE INDEX IF NOT EXISTS builds_triggered_by_idx ON builds (triggered_by);

CREATE INDEX IF NOT EXISTS deployments_build_idx ON deployments (build_id);
CREATE INDEX IF NOT EXISTS deployments_release_idx ON deployments (release_id);
CREATE INDEX IF NOT EXISTS deployments_deployer_idx ON deployments (deployed_by);
CREATE INDEX IF NOT EXISTS deployments_previous_idx ON deployments (previous_deployment_id);
CREATE INDEX IF NOT EXISTS deployments_rollback_idx ON deployments (rollback_of_id);

CREATE INDEX IF NOT EXISTS deployment_approvals_approver_idx ON deployment_approvals (approver_id);

CREATE INDEX IF NOT EXISTS artifact_links_creator_idx ON artifact_links (created_by);
CREATE INDEX IF NOT EXISTS activity_events_actor_idx ON activity_events (actor_id);
CREATE INDEX IF NOT EXISTS comments_author_idx ON comments (author_id);
CREATE INDEX IF NOT EXISTS attachments_uploader_idx ON attachments (uploaded_by);
