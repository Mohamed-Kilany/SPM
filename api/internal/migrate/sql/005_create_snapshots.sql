-- Migration: 005_create_snapshots

CREATE TABLE IF NOT EXISTS snapshots (
    project_id        UUID PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    data              BYTEA NOT NULL,
    last_sequence_id  BIGINT NOT NULL DEFAULT 0,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
