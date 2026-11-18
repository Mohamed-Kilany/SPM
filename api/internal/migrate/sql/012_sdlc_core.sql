-- Migration: 012_sdlc_core
-- Normalized SDLC/STLC graph: PRD -> Epic -> Feature -> Work Item,
-- Feature -> Test Case -> Test Run -> Test Result -> Bug,
-- Work Item -> Branch -> PR -> Commit -> Build -> Deployment -> Health Check.
--
-- The legacy event-sourced snapshot remains the real-time transport. New
-- artifacts live in normalized tables and publish workspace.changed events.

-- ---------------------------------------------------------------------------
-- Project metadata
-- ---------------------------------------------------------------------------

ALTER TABLE projects ADD COLUMN IF NOT EXISTS key_prefix VARCHAR(12) NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'active';

UPDATE projects
SET key_prefix = UPPER(LEFT(REGEXP_REPLACE(name, '[^a-zA-Z0-9]', '', 'g'), 4))
WHERE key_prefix = '';

UPDATE projects SET key_prefix = 'PRJ' WHERE key_prefix = '';

-- ---------------------------------------------------------------------------
-- Planning
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS prds (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    title       VARCHAR(255) NOT NULL,
    version     VARCHAR(32) NOT NULL DEFAULT '1.0',
    status      VARCHAR(20) NOT NULL DEFAULT 'draft'
                CHECK (status IN ('draft', 'in_review', 'approved', 'superseded')),
    summary     TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL DEFAULT '',
    owner_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    approved_by UUID REFERENCES users(id) ON DELETE SET NULL,
    approved_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS prds_project_idx ON prds (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS prds_status_idx ON prds (project_id, status);

CREATE TABLE IF NOT EXISTS releases (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        VARCHAR(255) NOT NULL,
    version     VARCHAR(64) NOT NULL,
    status      VARCHAR(20) NOT NULL DEFAULT 'planned'
                CHECK (status IN ('planned', 'in_progress', 'released', 'cancelled')),
    planned_at  TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    notes       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, version)
);

CREATE INDEX IF NOT EXISTS releases_project_idx ON releases (project_id, planned_at DESC);

CREATE TABLE IF NOT EXISTS sprints (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        VARCHAR(255) NOT NULL,
    goal        TEXT NOT NULL DEFAULT '',
    status      VARCHAR(20) NOT NULL DEFAULT 'planned'
                CHECK (status IN ('planned', 'active', 'completed', 'cancelled')),
    start_date  DATE,
    end_date    DATE,
    capacity    NUMERIC(8,2),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, name)
);

CREATE INDEX IF NOT EXISTS sprints_project_idx ON sprints (project_id, start_date DESC);

CREATE TABLE IF NOT EXISTS epics (
    id                UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id        UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    prd_id            UUID REFERENCES prds(id) ON DELETE SET NULL,
    title             VARCHAR(255) NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    status            VARCHAR(20) NOT NULL DEFAULT 'draft'
                      CHECK (status IN ('draft', 'planned', 'in_progress', 'done', 'cancelled')),
    priority          VARCHAR(20) NOT NULL DEFAULT 'medium'
                      CHECK (priority IN ('low', 'medium', 'high', 'critical')),
    owner_id          UUID REFERENCES users(id) ON DELETE SET NULL,
    target_release_id UUID REFERENCES releases(id) ON DELETE SET NULL,
    start_date        DATE,
    due_date          DATE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS epics_project_idx ON epics (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS epics_prd_idx ON epics (prd_id);
CREATE INDEX IF NOT EXISTS epics_status_idx ON epics (project_id, status);

CREATE TABLE IF NOT EXISTS features (
    id                  UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id          UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    epic_id             UUID NOT NULL REFERENCES epics(id) ON DELETE CASCADE,
    title               VARCHAR(255) NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    acceptance_criteria TEXT NOT NULL DEFAULT '',
    status              VARCHAR(20) NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft', 'ready', 'in_progress', 'in_review', 'in_qa', 'done', 'released', 'cancelled')),
    priority            VARCHAR(20) NOT NULL DEFAULT 'medium'
                        CHECK (priority IN ('low', 'medium', 'high', 'critical')),
    owner_id            UUID REFERENCES users(id) ON DELETE SET NULL,
    estimate_points     NUMERIC(6,2),
    target_release_id   UUID REFERENCES releases(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS features_project_idx ON features (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS features_epic_idx ON features (epic_id);
CREATE INDEX IF NOT EXISTS features_status_idx ON features (project_id, status);

CREATE TABLE IF NOT EXISTS environments (
    id                UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id        UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name              VARCHAR(255) NOT NULL,
    type              VARCHAR(20) NOT NULL DEFAULT 'custom'
                      CHECK (type IN ('development', 'staging', 'production', 'custom')),
    url               VARCHAR(2048) NOT NULL DEFAULT '',
    health_check_url  VARCHAR(2048) NOT NULL DEFAULT '',
    deploy_strategy   VARCHAR(32) NOT NULL DEFAULT 'rolling',
    requires_approval BOOLEAN NOT NULL DEFAULT FALSE,
    position          INT NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, name)
);

CREATE INDEX IF NOT EXISTS environments_project_idx ON environments (project_id, position);

-- Default environments for existing projects. New projects get the same rows
-- from the project creation handler.
INSERT INTO environments (project_id, name, type, position)
SELECT p.id, v.name, v.type, v.position
FROM projects p
CROSS JOIN (
    VALUES
        ('Development', 'development', 1),
        ('Staging', 'staging', 2),
        ('Production', 'production', 3)
) AS v(name, type, position)
ON CONFLICT (project_id, name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Work items (Story / Task / Subtask / Bug / Spike / Chore)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS work_items (
    id                  UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id          UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    item_number         BIGINT GENERATED BY DEFAULT AS IDENTITY,
    feature_id          UUID REFERENCES features(id) ON DELETE SET NULL,
    parent_id           UUID REFERENCES work_items(id) ON DELETE SET NULL,
    type                VARCHAR(20) NOT NULL
                        CHECK (type IN ('story', 'task', 'subtask', 'bug', 'spike', 'chore')),
    title               VARCHAR(255) NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    acceptance_criteria TEXT NOT NULL DEFAULT '',
    status              VARCHAR(20) NOT NULL DEFAULT 'backlog'
                        CHECK (status IN ('backlog', 'ready', 'in_progress', 'in_review', 'in_qa', 'done', 'blocked', 'cancelled', 'new', 'triaged', 'fixed', 'verified', 'closed', 'reopened')),
    priority            VARCHAR(20) NOT NULL DEFAULT 'medium'
                        CHECK (priority IN ('low', 'medium', 'high', 'critical')),
    severity            VARCHAR(20)
                        CHECK (severity IS NULL OR severity IN ('trivial', 'minor', 'major', 'critical', 'blocker')),
    assignee_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    reporter_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    estimate_points     NUMERIC(6,2),
    sprint_id           UUID REFERENCES sprints(id) ON DELETE SET NULL,
    release_id          UUID REFERENCES releases(id) ON DELETE SET NULL,
    environment_id      UUID REFERENCES environments(id) ON DELETE SET NULL,
    due_date            DATE,
    reproduction_steps  TEXT NOT NULL DEFAULT '',
    expected_result     TEXT NOT NULL DEFAULT '',
    actual_result       TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at           TIMESTAMPTZ,
    UNIQUE (project_id, item_number)
);

CREATE INDEX IF NOT EXISTS work_items_project_idx ON work_items (project_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS work_items_feature_idx ON work_items (feature_id);
CREATE INDEX IF NOT EXISTS work_items_parent_idx ON work_items (parent_id);
CREATE INDEX IF NOT EXISTS work_items_status_idx ON work_items (project_id, status);
CREATE INDEX IF NOT EXISTS work_items_assignee_idx ON work_items (project_id, assignee_id);
CREATE INDEX IF NOT EXISTS work_items_sprint_idx ON work_items (sprint_id);
CREATE INDEX IF NOT EXISTS work_items_release_idx ON work_items (release_id);
CREATE INDEX IF NOT EXISTS work_items_type_idx ON work_items (project_id, type);

-- ---------------------------------------------------------------------------
-- Test management
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS test_cases (
    id               UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id       UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    feature_id       UUID REFERENCES features(id) ON DELETE SET NULL,
    title            VARCHAR(255) NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    preconditions    TEXT NOT NULL DEFAULT '',
    type             VARCHAR(20) NOT NULL DEFAULT 'manual'
                     CHECK (type IN ('manual', 'automated')),
    priority         VARCHAR(20) NOT NULL DEFAULT 'medium'
                     CHECK (priority IN ('low', 'medium', 'high', 'critical')),
    status           VARCHAR(20) NOT NULL DEFAULT 'draft'
                     CHECK (status IN ('draft', 'ready', 'deprecated')),
    automation_ref   VARCHAR(1024) NOT NULL DEFAULT '',
    owner_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    tags             TEXT[] NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS test_cases_project_idx ON test_cases (project_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS test_cases_feature_idx ON test_cases (feature_id);
CREATE INDEX IF NOT EXISTS test_cases_status_idx ON test_cases (project_id, status);

CREATE TABLE IF NOT EXISTS test_steps (
    id              UUID PRIMARY KEY DEFAULT uuidv7(),
    test_case_id    UUID NOT NULL REFERENCES test_cases(id) ON DELETE CASCADE,
    position        INT NOT NULL,
    action          TEXT NOT NULL,
    expected_result TEXT NOT NULL,
    test_data       TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (test_case_id, position)
);

CREATE INDEX IF NOT EXISTS test_steps_case_idx ON test_steps (test_case_id, position);

CREATE TABLE IF NOT EXISTS git_repositories (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id     UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    provider       VARCHAR(20) NOT NULL DEFAULT 'gitea'
                   CHECK (provider IN ('gitea', 'github', 'gitlab', 'bitbucket', 'generic')),
    name           VARCHAR(255) NOT NULL,
    url            VARCHAR(2048) NOT NULL DEFAULT '',
    default_branch VARCHAR(255) NOT NULL DEFAULT 'main',
    external_id    VARCHAR(255) NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, name)
);

CREATE INDEX IF NOT EXISTS git_repositories_project_idx ON git_repositories (project_id);

CREATE TABLE IF NOT EXISTS git_branches (
    id              UUID PRIMARY KEY DEFAULT uuidv7(),
    repository_id   UUID NOT NULL REFERENCES git_repositories(id) ON DELETE CASCADE,
    work_item_id    UUID REFERENCES work_items(id) ON DELETE SET NULL,
    name            VARCHAR(255) NOT NULL,
    head_commit_sha VARCHAR(64) NOT NULL DEFAULT '',
    status          VARCHAR(20) NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'merged', 'deleted')),
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    merged_at       TIMESTAMPTZ,
    UNIQUE (repository_id, name)
);

CREATE INDEX IF NOT EXISTS git_branches_repo_idx ON git_branches (repository_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS git_branches_item_idx ON git_branches (work_item_id);

CREATE TABLE IF NOT EXISTS git_commits (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    repository_id  UUID NOT NULL REFERENCES git_repositories(id) ON DELETE CASCADE,
    sha            VARCHAR(64) NOT NULL,
    branch_name    VARCHAR(255) NOT NULL DEFAULT '',
    message        TEXT NOT NULL DEFAULT '',
    author_name    VARCHAR(255) NOT NULL DEFAULT '',
    author_email   VARCHAR(255) NOT NULL DEFAULT '',
    committed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repository_id, sha)
);

CREATE INDEX IF NOT EXISTS git_commits_repo_idx ON git_commits (repository_id, committed_at DESC);

CREATE TABLE IF NOT EXISTS pull_requests (
    id                UUID PRIMARY KEY DEFAULT uuidv7(),
    repository_id     UUID NOT NULL REFERENCES git_repositories(id) ON DELETE CASCADE,
    work_item_id      UUID REFERENCES work_items(id) ON DELETE SET NULL,
    number            INT NOT NULL,
    title             VARCHAR(255) NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    source_branch_id  UUID REFERENCES git_branches(id) ON DELETE SET NULL,
    target_branch     VARCHAR(255) NOT NULL DEFAULT 'main',
    author            VARCHAR(255) NOT NULL DEFAULT '',
    status            VARCHAR(20) NOT NULL DEFAULT 'open'
                      CHECK (status IN ('draft', 'open', 'in_review', 'approved', 'merged', 'closed')),
    review_status     VARCHAR(20) NOT NULL DEFAULT 'pending'
                      CHECK (review_status IN ('pending', 'approved', 'changes_requested')),
    merge_commit_sha  VARCHAR(64) NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    merged_at         TIMESTAMPTZ,
    UNIQUE (repository_id, number)
);

CREATE INDEX IF NOT EXISTS pull_requests_repo_idx ON pull_requests (repository_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS pull_requests_item_idx ON pull_requests (work_item_id);

CREATE TABLE IF NOT EXISTS pull_request_reviews (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    pull_request_id UUID NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    reviewer_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    status         VARCHAR(20) NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'approved', 'changes_requested')),
    comment        TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS pr_reviews_pr_idx ON pull_request_reviews (pull_request_id, created_at DESC);

CREATE TABLE IF NOT EXISTS builds (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id     UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    repository_id  UUID REFERENCES git_repositories(id) ON DELETE SET NULL,
    commit_sha     VARCHAR(64) NOT NULL DEFAULT '',
    branch         VARCHAR(255) NOT NULL DEFAULT '',
    provider       VARCHAR(32) NOT NULL DEFAULT 'gitea-actions',
    external_id    VARCHAR(255) NOT NULL DEFAULT '',
    number         VARCHAR(64) NOT NULL DEFAULT '',
    status         VARCHAR(20) NOT NULL DEFAULT 'queued'
                   CHECK (status IN ('queued', 'running', 'success', 'failure', 'cancelled', 'timed_out')),
    logs_url       VARCHAR(2048) NOT NULL DEFAULT '',
    triggered_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    started_at     TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS builds_project_idx ON builds (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS builds_commit_idx ON builds (project_id, commit_sha);
CREATE INDEX IF NOT EXISTS builds_status_idx ON builds (project_id, status);

CREATE TABLE IF NOT EXISTS deployments (
    id                    UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id            UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id        UUID NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    build_id              UUID REFERENCES builds(id) ON DELETE SET NULL,
    release_id            UUID REFERENCES releases(id) ON DELETE SET NULL,
    status                VARCHAR(20) NOT NULL DEFAULT 'queued'
                          CHECK (status IN ('queued', 'deploying', 'verifying', 'healthy', 'degraded', 'failed', 'rolled_back', 'cancelled')),
    health_status         VARCHAR(20) NOT NULL DEFAULT 'unknown'
                          CHECK (health_status IN ('unknown', 'healthy', 'degraded', 'unhealthy')),
    version               VARCHAR(64) NOT NULL DEFAULT '',
    deployed_by           UUID REFERENCES users(id) ON DELETE SET NULL,
    previous_deployment_id UUID REFERENCES deployments(id) ON DELETE SET NULL,
    rollback_of_id        UUID REFERENCES deployments(id) ON DELETE SET NULL,
    started_at            TIMESTAMPTZ,
    completed_at          TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS deployments_project_idx ON deployments (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS deployments_env_idx ON deployments (environment_id, created_at DESC);
CREATE INDEX IF NOT EXISTS deployments_status_idx ON deployments (project_id, status);

CREATE TABLE IF NOT EXISTS deployment_approvals (
    id            UUID PRIMARY KEY DEFAULT uuidv7(),
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    approver_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    status        VARCHAR(20) NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'approved', 'rejected')),
    comment       TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS deployment_approvals_idx ON deployment_approvals (deployment_id);

CREATE TABLE IF NOT EXISTS health_checks (
    id            UUID PRIMARY KEY DEFAULT uuidv7(),
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    url           VARCHAR(2048) NOT NULL DEFAULT '',
    status        VARCHAR(20) NOT NULL DEFAULT 'unknown'
                  CHECK (status IN ('unknown', 'healthy', 'degraded', 'unhealthy')),
    http_status   INT,
    latency_ms    INT,
    details       JSONB NOT NULL DEFAULT '{}'::jsonb,
    checked_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS health_checks_deployment_idx ON health_checks (deployment_id, checked_at DESC);

CREATE TABLE IF NOT EXISTS test_runs (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id     UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    release_id     UUID REFERENCES releases(id) ON DELETE SET NULL,
    sprint_id      UUID REFERENCES sprints(id) ON DELETE SET NULL,
    environment_id UUID REFERENCES environments(id) ON DELETE SET NULL,
    build_id       UUID REFERENCES builds(id) ON DELETE SET NULL,
    name           VARCHAR(255) NOT NULL,
    status         VARCHAR(20) NOT NULL DEFAULT 'planned'
                   CHECK (status IN ('planned', 'in_progress', 'completed', 'aborted')),
    executed_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    started_at     TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS test_runs_project_idx ON test_runs (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS test_runs_release_idx ON test_runs (release_id);

CREATE TABLE IF NOT EXISTS test_results (
    id                UUID PRIMARY KEY DEFAULT uuidv7(),
    test_run_id       UUID NOT NULL REFERENCES test_runs(id) ON DELETE CASCADE,
    test_case_id      UUID NOT NULL REFERENCES test_cases(id) ON DELETE CASCADE,
    status            VARCHAR(20) NOT NULL DEFAULT 'not_run'
                      CHECK (status IN ('not_run', 'passed', 'failed', 'blocked', 'skipped')),
    executed_by       UUID REFERENCES users(id) ON DELETE SET NULL,
    duration_ms       INT,
    actual_result     TEXT NOT NULL DEFAULT '',
    bug_work_item_id  UUID REFERENCES work_items(id) ON DELETE SET NULL,
    started_at        TIMESTAMPTZ,
    completed_at      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (test_run_id, test_case_id)
);

CREATE INDEX IF NOT EXISTS test_results_run_idx ON test_results (test_run_id, created_at);
CREATE INDEX IF NOT EXISTS test_results_case_idx ON test_results (test_case_id, created_at DESC);
CREATE INDEX IF NOT EXISTS test_results_status_idx ON test_results (test_run_id, status);

CREATE TABLE IF NOT EXISTS test_step_results (
    id               UUID PRIMARY KEY DEFAULT uuidv7(),
    test_result_id   UUID NOT NULL REFERENCES test_results(id) ON DELETE CASCADE,
    test_step_id     UUID NOT NULL REFERENCES test_steps(id) ON DELETE CASCADE,
    status           VARCHAR(20) NOT NULL DEFAULT 'not_run'
                     CHECK (status IN ('not_run', 'passed', 'failed', 'blocked', 'skipped')),
    actual_result    TEXT NOT NULL DEFAULT '',
    evidence_url     VARCHAR(2048) NOT NULL DEFAULT '',
    bug_work_item_id UUID REFERENCES work_items(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (test_result_id, test_step_id)
);

CREATE INDEX IF NOT EXISTS test_step_results_result_idx ON test_step_results (test_result_id);

-- ---------------------------------------------------------------------------
-- Cross-cutting traceability and audit
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS artifact_links (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_type VARCHAR(40) NOT NULL,
    source_id   UUID NOT NULL,
    target_type VARCHAR(40) NOT NULL,
    target_id   UUID NOT NULL,
    link_type   VARCHAR(40) NOT NULL DEFAULT 'related',
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (source_id <> target_id),
    UNIQUE (project_id, source_type, source_id, target_type, target_id, link_type)
);

CREATE INDEX IF NOT EXISTS artifact_links_source_idx ON artifact_links (project_id, source_type, source_id);
CREATE INDEX IF NOT EXISTS artifact_links_target_idx ON artifact_links (project_id, target_type, target_id);

CREATE TABLE IF NOT EXISTS activity_events (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    actor_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    entity_type VARCHAR(40) NOT NULL,
    entity_id   UUID,
    action      VARCHAR(40) NOT NULL,
    summary     TEXT NOT NULL DEFAULT '',
    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS activity_project_idx ON activity_events (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS activity_entity_idx ON activity_events (entity_type, entity_id, created_at DESC);

CREATE TABLE IF NOT EXISTS comments (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    entity_type VARCHAR(40) NOT NULL,
    entity_id   UUID NOT NULL,
    author_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    body        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS comments_entity_idx ON comments (project_id, entity_type, entity_id, created_at DESC);

CREATE TABLE IF NOT EXISTS attachments (
    id           UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id   UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    entity_type  VARCHAR(40) NOT NULL,
    entity_id    UUID NOT NULL,
    filename     VARCHAR(255) NOT NULL,
    mime_type    VARCHAR(255) NOT NULL DEFAULT 'application/octet-stream',
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    storage_path VARCHAR(2048) NOT NULL,
    uploaded_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS attachments_entity_idx ON attachments (project_id, entity_type, entity_id);
