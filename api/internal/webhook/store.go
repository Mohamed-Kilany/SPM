package webhook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store implements the exactly-once webhook delivery state machine.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a webhook delivery store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Claim tries to take ownership of a delivery. It returns false when the
// delivery was already completed. Stale processing claims and failed claims
// may be retried.
func (s *Store) Claim(ctx context.Context, source, deliveryID string) (bool, error) {
	const insert = `
		INSERT INTO webhook_deliveries (source, delivery_id, status, received_at)
		VALUES ($1, $2, 'processing', NOW())
		ON CONFLICT (source, delivery_id) DO NOTHING`
	tag, err := s.pool.Exec(ctx, insert, source, deliveryID)
	if err != nil {
		return false, fmt.Errorf("webhook: claim delivery: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}

	const reclaim = `
		UPDATE webhook_deliveries
		SET status = 'processing', received_at = NOW(), completed_at = NULL, error = NULL
		WHERE source = $1 AND delivery_id = $2
		  AND (status = 'failed' OR (status = 'processing' AND received_at < $3))
		RETURNING delivery_id`
	var id string
	err = s.pool.QueryRow(ctx, reclaim, source, deliveryID, time.Now().Add(-5*time.Minute)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("webhook: reclaim delivery: %w", err)
	}
	return true, nil
}

// Complete marks a delivery as successfully handled.
func (s *Store) Complete(ctx context.Context, source, deliveryID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE webhook_deliveries
		SET status = 'completed', completed_at = NOW(), error = NULL
		WHERE source = $1 AND delivery_id = $2`, source, deliveryID)
	return err
}

// Release marks a delivery failed and retryable.
func (s *Store) Release(ctx context.Context, source, deliveryID string, cause error) error {
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE webhook_deliveries
		SET status = 'failed', error = $3
		WHERE source = $1 AND delivery_id = $2`, source, deliveryID, message)
	return err
}
