-- Migration: 014_enterprise_identity
-- Enterprise identity, profile and project-role foundation.
--
-- Global roles remain admin/member/viewer. Project roles are expanded from the
-- v1 admin/member/viewer set to owner/maintainer/developer/tester/viewer.
-- Existing project "admin" memberships become owners.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS display_name VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS avatar_url VARCHAR(2048) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

UPDATE users
SET display_name = username
WHERE display_name = '';

CREATE INDEX IF NOT EXISTS users_active_idx ON users (is_active);
CREATE INDEX IF NOT EXISTS users_display_name_idx ON users (LOWER(display_name));

ALTER TABLE project_members
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE project_members
    DROP CONSTRAINT IF EXISTS project_members_role_check;

UPDATE project_members
SET role = 'owner'
WHERE role = 'admin';

ALTER TABLE project_members
    ADD CONSTRAINT project_members_role_check
    CHECK (role IN ('owner', 'maintainer', 'developer', 'tester', 'viewer'));

ALTER TABLE project_members
    ALTER COLUMN role SET DEFAULT 'developer';

CREATE INDEX IF NOT EXISTS project_members_project_role_idx
    ON project_members (project_id, role);
