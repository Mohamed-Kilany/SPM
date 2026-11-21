-- Migration: 019_test_plans_suites
-- Test plans, ordered suites, suite membership and run snapshots.
--
-- Expand-only: both new tables carry a trigger-owned revision, membership is
-- additive, and every test_runs/test_results column is nullable or defaulted.
-- The matching contract script lives in migrate/rollback/019_test_plans_suites.sql
-- and is reserved for a planned removal, not a routine rollback.

CREATE TABLE IF NOT EXISTS test_plans (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      VARCHAR(20) NOT NULL DEFAULT 'draft'
                CHECK (status IN ('draft', 'active', 'archived')),
    owner_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    start_date  DATE,
    end_date    DATE,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revision    BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    CHECK (end_date IS NULL OR start_date IS NULL OR end_date >= start_date)
);

CREATE INDEX IF NOT EXISTS test_plans_project_idx ON test_plans (project_id, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS test_plans_owner_idx ON test_plans (owner_id);
CREATE INDEX IF NOT EXISTS test_plans_creator_idx ON test_plans (created_by);

DROP TRIGGER IF EXISTS planning_revision ON test_plans;
CREATE TRIGGER planning_revision BEFORE UPDATE ON test_plans
    FOR EACH ROW EXECUTE FUNCTION planning_bump_revision();

CREATE TABLE IF NOT EXISTS test_suites (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    plan_id     UUID NOT NULL REFERENCES test_plans(id) ON DELETE CASCADE,
    name        VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    assignee_id UUID REFERENCES users(id) ON DELETE SET NULL,
    position    INTEGER NOT NULL DEFAULT 0,
    archived_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revision    BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0)
);

CREATE INDEX IF NOT EXISTS test_suites_plan_idx ON test_suites (plan_id, position, created_at);
CREATE INDEX IF NOT EXISTS test_suites_assignee_idx ON test_suites (assignee_id);

DROP TRIGGER IF EXISTS planning_revision ON test_suites;
CREATE TRIGGER planning_revision BEFORE UPDATE ON test_suites
    FOR EACH ROW EXECUTE FUNCTION planning_bump_revision();

-- Membership rows are ordered inside a suite. A case can appear in many suites
-- but only once in the same suite, which makes filter/CSV ingestion idempotent.
CREATE TABLE IF NOT EXISTS test_suite_cases (
    suite_id     UUID NOT NULL REFERENCES test_suites(id) ON DELETE CASCADE,
    test_case_id UUID NOT NULL REFERENCES test_cases(id) ON DELETE CASCADE,
    position     INTEGER NOT NULL DEFAULT 0,
    added_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (suite_id, test_case_id)
);

CREATE INDEX IF NOT EXISTS test_suite_cases_case_idx ON test_suite_cases (test_case_id);
CREATE INDEX IF NOT EXISTS test_suite_cases_adder_idx ON test_suite_cases (added_by);

-- A run keeps the plan/suite names it was created from so the historical
-- context survives an archive or a later rename. case_revision records the
-- test_cases.revision each result executed against.
ALTER TABLE test_runs
    ADD COLUMN IF NOT EXISTS plan_id UUID REFERENCES test_plans(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS suite_id UUID REFERENCES test_suites(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS plan_name VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS suite_name VARCHAR(255) NOT NULL DEFAULT '';

ALTER TABLE test_results
    ADD COLUMN IF NOT EXISTS case_revision BIGINT NOT NULL DEFAULT 1 CHECK (case_revision > 0);

CREATE INDEX IF NOT EXISTS test_runs_plan_idx ON test_runs (plan_id);
CREATE INDEX IF NOT EXISTS test_runs_suite_idx ON test_runs (suite_id);
