-- Migration: 004_create_events
-- Creates the immutable event log table (write-ahead mirror for delta queries).
-- Includes events_aggregate_seq index for efficient aggregate-scoped queries.
-- Requirements: 2.3, 3.2

CREATE TABLE events (
    sequence_id   BIGINT PRIMARY KEY,          -- mirrors NATS JetStream sequence ID
    event_type    VARCHAR(255) NOT NULL,
    aggregate_id  UUID NOT NULL,
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX events_aggregate_seq ON events (aggregate_id, sequence_id);
