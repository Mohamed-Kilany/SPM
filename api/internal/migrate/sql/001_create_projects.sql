-- Migration: 001_create_projects
-- Creates the projects table and relational project metadata.

CREATE TABLE IF NOT EXISTS projects (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    name           VARCHAR(255) NOT NULL,
    description    TEXT,
    owner_id       UUID NOT NULL,
    repo_name      VARCHAR(255) NOT NULL DEFAULT '',
    default_branch VARCHAR(255) NOT NULL DEFAULT 'main',
    app_name       VARCHAR(255) NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
