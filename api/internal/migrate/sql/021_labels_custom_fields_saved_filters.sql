-- Migration: 021_labels_custom_fields_saved_filters
-- Project labels, typed custom fields and reusable saved filters.
--
-- Expand-only: new tables only. Existing work item labels are backfilled into
-- the reusable label registry so old rows gain colors without a rewrite of
-- work_items. Custom-field values are stored in typed side tables, so adding a
-- field never changes the schema. The matching contract script lives in
-- migrate/rollback/021_labels_custom_fields_saved_filters.sql.

-- ---------------------------------------------------------------------------
-- Reusable project labels
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS project_labels (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        VARCHAR(24) NOT NULL,
    color       VARCHAR(16) NOT NULL DEFAULT 'slate'
                CHECK (color IN ('slate', 'blue', 'green', 'amber', 'red', 'purple', 'pink', 'teal')),
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT project_labels_name_normalized CHECK (name = lower(btrim(name)) AND length(name) > 0),
    UNIQUE (project_id, name)
);

CREATE INDEX IF NOT EXISTS project_labels_project_idx ON project_labels (project_id, name);
CREATE INDEX IF NOT EXISTS project_labels_creator_idx ON project_labels (created_by);

-- Existing work item labels are already normalized by the application. Skip
-- anything that predates the current length/character rules instead of failing
-- the migration.
INSERT INTO project_labels (project_id, name)
SELECT DISTINCT wi.project_id, label
FROM work_items wi
CROSS JOIN LATERAL unnest(wi.labels) AS label
WHERE length(btrim(label)) BETWEEN 1 AND 24
  AND label = lower(btrim(label))
  AND label ~ '^[a-z0-9 _-]+$'
ON CONFLICT (project_id, name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Typed custom fields
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS custom_fields (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    key         VARCHAR(40) NOT NULL,
    name        VARCHAR(80) NOT NULL,
    type        VARCHAR(16) NOT NULL
                CHECK (type IN ('text', 'number', 'select', 'date', 'user')),
    description TEXT NOT NULL DEFAULT '',
    options     JSONB NOT NULL DEFAULT '[]'::jsonb,
    config      JSONB NOT NULL DEFAULT '{}'::jsonb,
    required    BOOLEAN NOT NULL DEFAULT FALSE,
    position    INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
    archived_at TIMESTAMPTZ,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT custom_fields_key_normalized CHECK (key = lower(btrim(key)) AND key ~ '^[a-z][a-z0-9_]{0,39}$'),
    UNIQUE (project_id, key)
);

CREATE INDEX IF NOT EXISTS custom_fields_project_idx ON custom_fields (project_id, position, name);
CREATE INDEX IF NOT EXISTS custom_fields_creator_idx ON custom_fields (created_by);

-- One row per populated value. Empty values are deleted instead of stored, so
-- a missing row means "not set" even for a required field.
CREATE TABLE IF NOT EXISTS custom_field_values (
    id            UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id    UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    field_id      UUID NOT NULL REFERENCES custom_fields(id) ON DELETE RESTRICT,
    entity_type   VARCHAR(16) NOT NULL
                  CHECK (entity_type IN ('work_item', 'test_case')),
    entity_id     UUID NOT NULL,
    value_text    TEXT NOT NULL DEFAULT '',
    value_number  DOUBLE PRECISION,
    value_date    DATE,
    value_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (field_id, entity_type, entity_id)
);

CREATE INDEX IF NOT EXISTS custom_field_values_project_idx ON custom_field_values (project_id, entity_type, entity_id);
CREATE INDEX IF NOT EXISTS custom_field_values_entity_idx ON custom_field_values (entity_type, entity_id);
CREATE INDEX IF NOT EXISTS custom_field_values_user_idx ON custom_field_values (value_user_id);

-- ---------------------------------------------------------------------------
-- Saved filters
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS saved_filters (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        VARCHAR(80) NOT NULL,
    scope       VARCHAR(16) NOT NULL
                CHECK (scope IN ('planning', 'board', 'test')),
    visibility  VARCHAR(16) NOT NULL DEFAULT 'private'
                CHECK (visibility IN ('private', 'shared')),
    owner_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    filters     JSONB NOT NULL DEFAULT '{}'::jsonb,
    sort        JSONB NOT NULL DEFAULT '{}'::jsonb,
    columns     JSONB NOT NULL DEFAULT '[]'::jsonb,
    pinned      BOOLEAN NOT NULL DEFAULT FALSE,
    is_default  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revision    BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    CONSTRAINT saved_filters_name_present CHECK (length(btrim(name)) > 0),
    UNIQUE (project_id, owner_id, scope, name)
);

CREATE INDEX IF NOT EXISTS saved_filters_project_idx ON saved_filters (project_id, scope, visibility);
CREATE INDEX IF NOT EXISTS saved_filters_owner_idx ON saved_filters (owner_id, scope);
CREATE UNIQUE INDEX IF NOT EXISTS saved_filters_default_idx
    ON saved_filters (project_id, owner_id, scope)
    WHERE is_default;

DROP TRIGGER IF EXISTS planning_revision ON saved_filters;
CREATE TRIGGER planning_revision BEFORE UPDATE ON saved_filters
    FOR EACH ROW EXECUTE FUNCTION planning_bump_revision();
