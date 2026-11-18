-- Migration: 005_create_snapshots
-- Creates the snapshots table (one row per project, LZ4-compressed JSONB in BYTEA).
-- Depends on: 001_create_projects
-- Requirements: 3.2, 3.7

CREATE TABLE snapshots (
    project_id        UUID PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    data              BYTEA NOT NULL,            -- LZ4-compressed JSONB
    last_sequence_id  BIGINT NOT NULL DEFAULT 0,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
