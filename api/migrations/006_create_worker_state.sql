-- Migration: 006_create_worker_state
-- Creates the worker_state table for EventWorker resume tracking.
-- Inserts the default worker row so the worker can always SELECT without checking for NULL.
-- Requirements: 11.1, 11.4

CREATE TABLE worker_state (
    worker_id         VARCHAR(64) PRIMARY KEY DEFAULT 'default',
    last_sequence_id  BIGINT NOT NULL DEFAULT 0,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed the default worker row so EventWorker can always find its state on startup.
INSERT INTO worker_state (worker_id, last_sequence_id, updated_at)
VALUES ('default', 0, NOW());
