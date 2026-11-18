-- Migration: 009_webhook_deliveries
-- Exactly-once webhook processing. A delivery is claimed before side effects
-- and marked complete afterwards; failed deliveries are released for retry.

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    source        VARCHAR(32) NOT NULL,
    delivery_id   VARCHAR(255) NOT NULL,
    status        VARCHAR(20) NOT NULL DEFAULT 'processing'
                  CHECK (status IN ('processing', 'completed', 'failed')),
    received_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at  TIMESTAMPTZ,
    error         TEXT,
    PRIMARY KEY (source, delivery_id)
);

CREATE INDEX IF NOT EXISTS webhook_deliveries_received_idx
    ON webhook_deliveries (received_at DESC);
