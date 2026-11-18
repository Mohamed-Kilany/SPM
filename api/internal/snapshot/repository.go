// Package snapshot provides snapshot read/write operations backed by PostgreSQL.
package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pierrec/lz4/v4"

	"spm-platform/api/internal/models"
)

// ErrNotFound is returned by Get when no snapshot exists for the given project.
var ErrNotFound = errors.New("snapshot: not found")

// Repo provides snapshot read/write operations backed by PostgreSQL.
type Repo struct {
	pool *pgxpool.Pool
}

// New creates a Repo with the given connection pool.
func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// Pool exposes the underlying pool for read handlers that need project
// metadata alongside snapshot data.
func (r *Repo) Pool() *pgxpool.Pool { return r.pool }

// Get retrieves the snapshot for the given project. The returned Snapshot.Data
// contains raw (decompressed) bytes.
func (r *Repo) Get(ctx context.Context, projectID uuid.UUID) (*models.Snapshot, error) {
	const query = `SELECT data, last_sequence_id, updated_at FROM snapshots WHERE project_id = $1`

	var snap models.Snapshot
	snap.ProjectID = projectID

	var compressed []byte
	err := r.pool.QueryRow(ctx, query, projectID).Scan(&compressed, &snap.LastSequenceID, &snap.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("snapshot: get query failed: %w", err)
	}

	raw, err := decompress(compressed)
	if err != nil {
		return nil, fmt.Errorf("snapshot: decompression failed for project %s: %w", projectID, err)
	}
	snap.Data = raw
	return &snap, nil
}

// GetState loads and decodes the current project state.
func (r *Repo) GetState(ctx context.Context, projectID uuid.UUID) (models.ProjectState, error) {
	snap, err := r.Get(ctx, projectID)
	if err != nil {
		return models.ProjectState{}, err
	}
	var state models.ProjectState
	if err := decodeState(snap.Data, &state); err != nil {
		return models.ProjectState{}, err
	}
	return state, nil
}

// Upsert writes the snapshot within an existing transaction. Data must be raw
// (uncompressed) JSON.
func (r *Repo) Upsert(ctx context.Context, tx pgx.Tx, snap models.Snapshot) error {
	const query = `
		INSERT INTO snapshots (project_id, data, last_sequence_id, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (project_id) DO UPDATE
			SET data = EXCLUDED.data,
			    last_sequence_id = EXCLUDED.last_sequence_id,
			    updated_at = EXCLUDED.updated_at`

	compressed, err := compress(snap.Data)
	if err != nil {
		return fmt.Errorf("snapshot: compression failed for project %s: %w", snap.ProjectID, err)
	}
	if _, err := tx.Exec(ctx, query, snap.ProjectID, compressed, snap.LastSequenceID); err != nil {
		return fmt.Errorf("snapshot: upsert failed for project %s: %w", snap.ProjectID, err)
	}
	return nil
}

// GetDeltaEvents returns all events for a project with sequence_id > afterSeq.
func (r *Repo) GetDeltaEvents(ctx context.Context, projectID uuid.UUID, afterSeq int64) ([]models.EventEnvelope, error) {
	const query = `
		SELECT sequence_id, event_type, aggregate_id, payload, created_at
		FROM events
		WHERE aggregate_id = $1 AND sequence_id > $2
		ORDER BY sequence_id ASC`
	return r.queryEvents(ctx, query, projectID, afterSeq)
}

// GetEventsRange returns up to limit events after afterSeq in stream order.
func (r *Repo) GetEventsRange(ctx context.Context, projectID uuid.UUID, afterSeq int64, limit int) ([]models.EventEnvelope, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const query = `
		SELECT sequence_id, event_type, aggregate_id, payload, created_at
		FROM events
		WHERE aggregate_id = $1 AND sequence_id > $2
		ORDER BY sequence_id ASC
		LIMIT $3`
	return r.queryEvents(ctx, query, projectID, afterSeq, limit)
}

func (r *Repo) queryEvents(ctx context.Context, query string, args ...interface{}) ([]models.EventEnvelope, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("snapshot: events query failed: %w", err)
	}
	defer rows.Close()

	var events []models.EventEnvelope
	for rows.Next() {
		var e models.EventEnvelope
		if err := rows.Scan(&e.SequenceID, &e.EventType, &e.AggregateID, &e.Payload, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("snapshot: scanning event row: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("snapshot: iterating event rows: %w", err)
	}
	return events, nil
}

// CountDeltaEvents returns the count of events with sequence_id > afterSeq.
func (r *Repo) CountDeltaEvents(ctx context.Context, projectID uuid.UUID, afterSeq int64) (int64, error) {
	const query = `SELECT COUNT(*) FROM events WHERE aggregate_id = $1 AND sequence_id > $2`
	var count int64
	if err := r.pool.QueryRow(ctx, query, projectID, afterSeq).Scan(&count); err != nil {
		return 0, fmt.Errorf("snapshot: count delta events query failed: %w", err)
	}
	return count, nil
}

// LatestSequenceForProject returns the newest event sequence for a project.
func (r *Repo) LatestSequenceForProject(ctx context.Context, projectID uuid.UUID) (int64, error) {
	const query = `SELECT COALESCE(MAX(sequence_id), 0) FROM events WHERE aggregate_id = $1`
	var seq int64
	if err := r.pool.QueryRow(ctx, query, projectID).Scan(&seq); err != nil {
		return 0, fmt.Errorf("snapshot: latest sequence query failed: %w", err)
	}
	return seq, nil
}

// InsertEvent mirrors an envelope into the events table within a transaction.
func (r *Repo) InsertEvent(ctx context.Context, tx pgx.Tx, env models.EventEnvelope) error {
	const query = `
		INSERT INTO events (sequence_id, event_type, aggregate_id, payload, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (sequence_id) DO NOTHING`

	if _, err := tx.Exec(ctx, query, env.SequenceID, env.EventType, env.AggregateID, env.Payload, env.CreatedAt); err != nil {
		return fmt.Errorf("snapshot: insert event seq=%d: %w", env.SequenceID, err)
	}
	return nil
}

// ProjectMeta is the relational metadata required by write handlers.
type ProjectMeta struct {
	ID            uuid.UUID
	Name          string
	Description   string
	OwnerID       uuid.UUID
	RepoName      string
	DefaultBranch string
	AppName       string
	CreatedAt     time.Time
}

// GetProjectMeta reads project metadata from the relational projection.
func (r *Repo) GetProjectMeta(ctx context.Context, projectID uuid.UUID) (ProjectMeta, error) {
	const query = `
		SELECT id, name, COALESCE(description, ''), owner_id, repo_name, default_branch, app_name, created_at
		FROM projects WHERE id = $1`
	var m ProjectMeta
	err := r.pool.QueryRow(ctx, query, projectID).Scan(
		&m.ID, &m.Name, &m.Description, &m.OwnerID, &m.RepoName, &m.DefaultBranch, &m.AppName, &m.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectMeta{}, ErrNotFound
		}
		return ProjectMeta{}, err
	}
	return m, nil
}

func decodeState(data []byte, state *models.ProjectState) error {
	if err := json.Unmarshal(data, state); err != nil {
		return fmt.Errorf("snapshot: decode state: %w", err)
	}
	// Defensive normalisation for snapshots created by older versions.
	if state.Tickets == nil {
		state.Tickets = []models.Ticket{}
	}
	if state.Columns == nil || len(state.Columns) == 0 {
		state.Columns = models.DefaultBoardColumns()
	}
	if state.Members == nil {
		state.Members = []models.ProjectMember{}
	}
	if state.Pipelines == nil {
		state.Pipelines = []models.PipelineStatus{}
	}
	if state.Deployments == nil {
		state.Deployments = []models.DeploymentStatus{}
	}
	if state.Notifications == nil {
		state.Notifications = []models.Notification{}
	}
	return nil
}

// DecodeCompressed decodes an LZ4-compressed snapshot blob into state.
func DecodeCompressed(src []byte) (models.ProjectState, error) {
	raw, err := decompress(src)
	if err != nil {
		return models.ProjectState{}, err
	}
	var state models.ProjectState
	if err := decodeState(raw, &state); err != nil {
		return models.ProjectState{}, err
	}
	return state, nil
}

func compress(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := lz4.NewWriter(&buf)
	if _, err := w.Write(src); err != nil {
		return nil, fmt.Errorf("lz4 write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("lz4 close: %w", err)
	}
	return buf.Bytes(), nil
}

func decompress(src []byte) ([]byte, error) {
	r := lz4.NewReader(bytes.NewReader(src))
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, fmt.Errorf("lz4 decompress: %w", err)
	}
	return buf.Bytes(), nil
}
