-- Migration: 013_integrations_reports
-- Phase 3-6 additions: configurable delivery policy, inbound integration
-- metadata, report/import provenance and query indexes. The platform is not
-- live yet, so this migration only creates the fresh normalized schema; no
-- legacy ticket data is backfilled.

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS settings JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE git_repositories
    ADD COLUMN IF NOT EXISTS webhook_token VARCHAR(128) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS webhook_url VARCHAR(2048) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS git_repositories_webhook_token_idx
    ON git_repositories (webhook_token)
    WHERE webhook_token <> '';

ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS builds_repo_branch_idx
    ON builds (repository_id, branch, created_at DESC);

ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS argo_app VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS argo_revision VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS deployments_env_latest_idx
    ON deployments (project_id, environment_id, created_at DESC);

ALTER TABLE test_runs
    ADD COLUMN IF NOT EXISTS import_source VARCHAR(32) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS import_ref VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS test_runs_project_created_idx
    ON test_runs (project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS work_items_project_sprint_status_idx
    ON work_items (project_id, sprint_id, status);

CREATE INDEX IF NOT EXISTS activity_project_action_idx
    ON activity_events (project_id, action, created_at DESC);

-- A deterministic default repository token is not generated here. Application
-- code creates a cryptographically random token when a project repository is
-- registered, so an operator can rotate it without a schema change.
