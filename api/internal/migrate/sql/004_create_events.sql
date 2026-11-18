-- Migration: 004_create_events
-- Immutable event log mirror of the JetStream stream.

CREATE TABLE IF NOT EXISTS events (
    sequence_id   BIGINT PRIMARY KEY,
    event_type    VARCHAR(255) NOT NULL,
    aggregate_id  UUID NOT NULL,
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS events_aggregate_seq ON events (aggregate_id, sequence_id);
CREATE INDEX IF NOT EXISTS events_type_seq ON events (event_type, sequence_id DESC);
