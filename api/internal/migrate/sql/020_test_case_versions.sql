-- Migration: 020_test_case_versions
-- Immutable test case versions, shared step templates and snapshot step results.
--
-- Expand-only: existing test cases are backfilled with their current revision,
-- existing step results gain a content snapshot, and every new column has a
-- default or is nullable. The matching contract script lives in
-- migrate/rollback/020_test_case_versions.sql and is reserved for a planned
-- removal, not a routine rollback.

CREATE TABLE IF NOT EXISTS test_case_versions (
    id               UUID PRIMARY KEY DEFAULT uuidv7(),
    test_case_id     UUID NOT NULL REFERENCES test_cases(id) ON DELETE CASCADE,
    revision         BIGINT NOT NULL CHECK (revision > 0),
    feature_id       UUID REFERENCES features(id) ON DELETE SET NULL,
    feature_title    VARCHAR(255) NOT NULL DEFAULT '',
    title            VARCHAR(255) NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    preconditions    TEXT NOT NULL DEFAULT '',
    type             VARCHAR(20) NOT NULL
                     CHECK (type IN ('manual', 'automated')),
    priority         VARCHAR(20) NOT NULL
                     CHECK (priority IN ('low', 'medium', 'high', 'critical')),
    status           VARCHAR(20) NOT NULL
                     CHECK (status IN ('draft', 'ready', 'deprecated')),
    automation_ref   VARCHAR(1024) NOT NULL DEFAULT '',
    owner_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    tags             TEXT[] NOT NULL DEFAULT '{}',
    changed_by       UUID REFERENCES users(id) ON DELETE SET NULL,
    changed_by_name  VARCHAR(255) NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (test_case_id, revision)
);

CREATE INDEX IF NOT EXISTS test_case_versions_case_idx ON test_case_versions (test_case_id, revision DESC);
CREATE INDEX IF NOT EXISTS test_case_versions_feature_idx ON test_case_versions (feature_id);
CREATE INDEX IF NOT EXISTS test_case_versions_owner_idx ON test_case_versions (owner_id);
CREATE INDEX IF NOT EXISTS test_case_versions_author_idx ON test_case_versions (changed_by);

CREATE TABLE IF NOT EXISTS test_case_version_steps (
    version_id      UUID NOT NULL REFERENCES test_case_versions(id) ON DELETE CASCADE,
    position        INTEGER NOT NULL CHECK (position > 0),
    action          TEXT NOT NULL,
    expected_result TEXT NOT NULL,
    test_data       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (version_id, position)
);

-- Reusable step templates are copied into a case when used. Editing a template
-- never rewrites a case, and deleting one keeps the copied text in place.
CREATE TABLE IF NOT EXISTS test_shared_steps (
    id              UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    action          TEXT NOT NULL,
    expected_result TEXT NOT NULL,
    test_data       TEXT NOT NULL DEFAULT '',
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revision        BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    UNIQUE (project_id, name)
);

CREATE INDEX IF NOT EXISTS test_shared_steps_project_idx ON test_shared_steps (project_id, name);
CREATE INDEX IF NOT EXISTS test_shared_steps_creator_idx ON test_shared_steps (created_by);

DROP TRIGGER IF EXISTS planning_revision ON test_shared_steps;
CREATE TRIGGER planning_revision BEFORE UPDATE ON test_shared_steps
    FOR EACH ROW EXECUTE FUNCTION planning_bump_revision();

-- A case step may originate from a shared template. The text is still copied
-- into test_steps, so an updated or deleted template cannot alter the case.
ALTER TABLE test_steps
    ADD COLUMN IF NOT EXISTS shared_step_id UUID REFERENCES test_shared_steps(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS test_steps_shared_idx ON test_steps (shared_step_id);

-- Step results must survive a later case edit. They keep the action, expected
-- result, data and revision they executed against instead of joining live
-- test_steps rows that an edit replaces.
ALTER TABLE test_step_results
    ALTER COLUMN test_step_id DROP NOT NULL;

ALTER TABLE test_step_results
    DROP CONSTRAINT IF EXISTS test_step_results_test_step_id_fkey;

ALTER TABLE test_step_results
    ADD CONSTRAINT test_step_results_test_step_id_fkey
    FOREIGN KEY (test_step_id) REFERENCES test_steps(id) ON DELETE SET NULL;

ALTER TABLE test_step_results
    ADD COLUMN IF NOT EXISTS case_revision BIGINT NOT NULL DEFAULT 1 CHECK (case_revision > 0),
    ADD COLUMN IF NOT EXISTS position INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
    ADD COLUMN IF NOT EXISTS action TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS expected_result TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS test_data TEXT NOT NULL DEFAULT '';

-- Backfill the current case as revision one for every existing case. Older
-- revisions cannot be reconstructed; from this baseline onward every edit is
-- captured and every new run references an immutable version.
INSERT INTO test_case_versions (
    test_case_id, revision, feature_id, feature_title, title, description,
    preconditions, type, priority, status, automation_ref, owner_id, tags,
    changed_by, changed_by_name, created_at
)
SELECT c.id, c.revision, c.feature_id, COALESCE(f.title, ''), c.title, c.description,
       c.preconditions, c.type, c.priority, c.status, c.automation_ref, c.owner_id,
       c.tags, NULL, '', c.updated_at
FROM test_cases c
LEFT JOIN features f ON f.id = c.feature_id
ON CONFLICT (test_case_id, revision) DO NOTHING;

INSERT INTO test_case_version_steps (version_id, position, action, expected_result, test_data)
SELECT v.id, s.position, s.action, s.expected_result, s.test_data
FROM test_case_versions v
JOIN test_cases c ON c.id = v.test_case_id AND c.revision = v.revision
JOIN test_steps s ON s.test_case_id = v.test_case_id
ON CONFLICT (version_id, position) DO NOTHING;

-- Backfill step-result snapshots from the steps that still exist, then the
-- case revision from the owning result. Blank snapshots only, so reapplying
-- this expand migration cannot overwrite a recorded snapshot.
UPDATE test_step_results sr
SET action = s.action,
    expected_result = s.expected_result,
    test_data = s.test_data,
    position = s.position
FROM test_steps s
WHERE s.id = sr.test_step_id
  AND sr.action = ''
  AND sr.expected_result = '';

UPDATE test_step_results sr
SET case_revision = r.case_revision
FROM test_results r
WHERE r.id = sr.test_result_id
  AND sr.case_revision = 1;
