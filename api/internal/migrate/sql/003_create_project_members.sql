-- Migration: 003_create_project_members

CREATE TABLE IF NOT EXISTS project_members (
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role        VARCHAR(20) NOT NULL DEFAULT 'member'
                CHECK (role IN ('admin', 'member', 'viewer')),
    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX IF NOT EXISTS project_members_user_idx ON project_members (user_id);
