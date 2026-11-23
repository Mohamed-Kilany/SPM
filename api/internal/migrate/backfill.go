package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BackfillProgress is the durable checkpoint for a batched backfill.
type BackfillProgress struct {
	Cursor    string
	Completed bool
}

// BackfillStore persists progress between batches. The Postgres implementation
// uses the migration_backfills table created by the migration runner.
type BackfillStore interface {
	Load(ctx context.Context, name string) (BackfillProgress, error)
	Save(ctx context.Context, name string, progress BackfillProgress) error
}

// BackfillFunc processes at most limit rows after cursor and returns the next
// cursor and the number of rows processed. An empty next cursor means the
// backfill is complete. The callback should perform each batch in its own
// transaction.
type BackfillFunc func(ctx context.Context, cursor string, limit int) (next string, processed int, err error)

// BackfillResult summarizes a RunBackfill call.
type BackfillResult struct {
	Batches   int
	Processed int
	Cursor    string
	Completed bool
}

// RunBackfill runs a resumable, bounded backfill. maxBatches <= 0 means no
// batch limit; the function still stops when the callback reports completion.
func RunBackfill(ctx context.Context, store BackfillStore, name string, batchSize, maxBatches int, fn BackfillFunc) (BackfillResult, error) {
	if store == nil {
		return BackfillResult{}, errors.New("migrate: nil backfill store")
	}
	if strings.TrimSpace(name) == "" {
		return BackfillResult{}, errors.New("migrate: backfill name is required")
	}
	if batchSize <= 0 {
		return BackfillResult{}, errors.New("migrate: backfill batch size must be positive")
	}
	if fn == nil {
		return BackfillResult{}, errors.New("migrate: nil backfill function")
	}

	progress, err := store.Load(ctx, name)
	if err != nil {
		return BackfillResult{}, fmt.Errorf("migrate: load backfill %s: %w", name, err)
	}
	result := BackfillResult{Cursor: progress.Cursor, Completed: progress.Completed}
	if progress.Completed {
		return result, nil
	}

	for maxBatches <= 0 || result.Batches < maxBatches {
		next, processed, err := fn(ctx, result.Cursor, batchSize)
		if err != nil {
			return result, fmt.Errorf("migrate: backfill %s: %w", name, err)
		}
		result.Batches++
		result.Processed += processed

		if processed == 0 || next == "" {
			result.Cursor = ""
			result.Completed = true
			if err := store.Save(ctx, name, BackfillProgress{Completed: true}); err != nil {
				return result, fmt.Errorf("migrate: save backfill %s: %w", name, err)
			}
			return result, nil
		}
		if next == result.Cursor {
			return result, fmt.Errorf("migrate: backfill %s cursor did not advance from %q", name, next)
		}
		result.Cursor = next
		if err := store.Save(ctx, name, BackfillProgress{Cursor: next}); err != nil {
			return result, fmt.Errorf("migrate: save backfill %s: %w", name, err)
		}
	}
	return result, nil
}

// PostgresBackfillStore stores checkpoints in migration_backfills.
type PostgresBackfillStore struct {
	pool *pgxpool.Pool
}

// NewPostgresBackfillStore creates a store backed by the API database.
func NewPostgresBackfillStore(pool *pgxpool.Pool) *PostgresBackfillStore {
	return &PostgresBackfillStore{pool: pool}
}

// Load reads the checkpoint for name. Missing rows start at the beginning.
func (s *PostgresBackfillStore) Load(ctx context.Context, name string) (BackfillProgress, error) {
	if s == nil || s.pool == nil {
		return BackfillProgress{}, errors.New("migrate: nil backfill pool")
	}
	var (
		cursor      string
		completedAt *string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT cursor, completed_at::text FROM migration_backfills WHERE name = $1`, name,
	).Scan(&cursor, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return BackfillProgress{}, nil
	}
	if err != nil {
		return BackfillProgress{}, err
	}
	return BackfillProgress{Cursor: cursor, Completed: completedAt != nil}, nil
}

// Save upserts the checkpoint for name.
func (s *PostgresBackfillStore) Save(ctx context.Context, name string, progress BackfillProgress) error {
	if s == nil || s.pool == nil {
		return errors.New("migrate: nil backfill pool")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO migration_backfills (name, cursor, completed_at, updated_at)
		VALUES ($1, $2, CASE WHEN $3 THEN NOW() ELSE NULL END, NOW())
		ON CONFLICT (name) DO UPDATE
		SET cursor = EXCLUDED.cursor,
		    completed_at = EXCLUDED.completed_at,
		    updated_at = NOW()`,
		name, progress.Cursor, progress.Completed)
	return err
}
