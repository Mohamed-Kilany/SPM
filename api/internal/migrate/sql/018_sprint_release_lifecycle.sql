-- Migration: 018_sprint_release_lifecycle
-- Explicit sprint and release lifecycle state, release freeze and sign-off.
--
-- Expand-only: every column is nullable or has a non-volatile default, and the
-- exception table is new. The matching contract script lives in
-- migrate/rollback/018_sprint_release_lifecycle.sql and is reserved for a
-- planned removal, not a routine rollback.

-- ---------------------------------------------------------------------------
-- Sprint lifecycle
-- ---------------------------------------------------------------------------

ALTER TABLE sprints
    ADD COLUMN IF NOT EXISTS activated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS completed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS close_policy VARCHAR(20),
    ADD COLUMN IF NOT EXISTS carry_over_count INTEGER NOT NULL DEFAULT 0;

-- Exceptions are keyed by release and entity. A NULL entity_id records a
-- type-wide exception, which is how a release manager authorizes creating new
-- scope while the release is frozen.
CREATE TABLE IF NOT EXISTS release_scope_exceptions (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    release_id  UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    entity_type VARCHAR(20) NOT NULL CHECK (entity_type IN ('work_item', 'feature', 'epic')),
    entity_id   UUID,
    reason      TEXT NOT NULL,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS release_scope_exceptions_release_idx
    ON release_scope_exceptions (release_id, created_at DESC);
CREATE INDEX IF NOT EXISTS release_scope_exceptions_project_idx
    ON release_scope_exceptions (project_id);
CREATE INDEX IF NOT EXISTS release_scope_exceptions_created_by_idx
    ON release_scope_exceptions (created_by)
    WHERE created_by IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS release_scope_exceptions_entity_idx
    ON release_scope_exceptions (release_id, entity_type, entity_id)
    WHERE entity_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS release_scope_exceptions_type_idx
    ON release_scope_exceptions (release_id, entity_type)
    WHERE entity_id IS NULL;

-- ---------------------------------------------------------------------------
-- Release lifecycle
-- ---------------------------------------------------------------------------

ALTER TABLE releases
    ADD COLUMN IF NOT EXISTS milestone_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS frozen_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS frozen_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS freeze_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sign_off_evidence TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS signed_off_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS signed_off_by UUID REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS releases_frozen_idx
    ON releases (project_id, frozen_at)
    WHERE frozen_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS sprints_completed_idx
    ON sprints (project_id, completed_at DESC)
    WHERE completed_at IS NOT NULL;
