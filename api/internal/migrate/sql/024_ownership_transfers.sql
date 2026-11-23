-- Migration: 024_ownership_transfers
-- Explicit project ownership transfer requests.
--
-- A transfer is requested by a project owner or a global admin and stays
-- pending until the target member accepts it, the target declines it, an admin
-- confirms it on the target's behalf, or the requester cancels it. Only one
-- request may be pending per project so two admins cannot race two successors.

CREATE TABLE IF NOT EXISTS ownership_transfers (
    id           UUID PRIMARY KEY DEFAULT uuidv7(),
    project_id   UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    from_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    to_user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status       VARCHAR(20) NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'accepted', 'declined', 'confirmed', 'cancelled')),
    reason       VARCHAR(500) NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at  TIMESTAMPTZ,
    resolved_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    CONSTRAINT ownership_transfers_distinct_users CHECK (from_user_id <> to_user_id),
    CONSTRAINT ownership_transfers_resolution_consistent CHECK (
        (status = 'pending' AND resolved_at IS NULL AND resolved_by IS NULL)
        OR (status <> 'pending' AND resolved_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS ownership_transfers_one_pending_idx
    ON ownership_transfers (project_id)
    WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS ownership_transfers_target_idx
    ON ownership_transfers (to_user_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS ownership_transfers_project_idx
    ON ownership_transfers (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ownership_transfers_from_idx
    ON ownership_transfers (from_user_id, status);
CREATE INDEX IF NOT EXISTS ownership_transfers_requested_by_idx
    ON ownership_transfers (requested_by);
CREATE INDEX IF NOT EXISTS ownership_transfers_resolved_by_idx
    ON ownership_transfers (resolved_by);

COMMENT ON TABLE ownership_transfers IS
    'Pending and historical project ownership transfers. Acceptance or admin confirmation moves projects.owner_id and the owner membership together.';
