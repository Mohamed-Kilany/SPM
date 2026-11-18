-- Migration: 008_project_metadata
-- Idempotent upgrade for databases created before these columns existed.

ALTER TABLE projects ADD COLUMN IF NOT EXISTS repo_name VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS default_branch VARCHAR(255) NOT NULL DEFAULT 'main';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS app_name VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE project_members ADD COLUMN IF NOT EXISTS role VARCHAR(20) NOT NULL DEFAULT 'member';

-- Backfill repository/app names from the human-readable project name and the
-- stable UUID suffix. The API uses this exact convention going forward.
UPDATE projects
SET repo_name = lower(regexp_replace(name, '[^a-zA-Z0-9]+', '-', 'g')) || '-' || left(id::text, 8)
WHERE repo_name = '';

UPDATE projects
SET app_name = lower(regexp_replace(name, '[^a-zA-Z0-9]+', '-', 'g'))
WHERE app_name = '';
