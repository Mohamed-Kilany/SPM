-- Migration: 006_create_worker_state

CREATE TABLE IF NOT EXISTS worker_state (
    worker_id         VARCHAR(64) PRIMARY KEY DEFAULT 'default',
    last_sequence_id  BIGINT NOT NULL DEFAULT 0,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO worker_state (worker_id, last_sequence_id, updated_at)
VALUES ('default', 0, NOW())
ON CONFLICT (worker_id) DO NOTHING;
