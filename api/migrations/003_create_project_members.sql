-- Migration: 003_create_project_members
-- Creates the project_members join table with composite PK and FK cascades.
-- Depends on: 001_create_projects, 002_create_users
-- Requirements: 2.1

CREATE TABLE project_members (
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, user_id)
);
