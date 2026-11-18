// Package worker consumes SPM_EVENTS and maintains the project snapshot read
// model in PostgreSQL, then publishes JSON Patches for SSE fan-out.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/pierrec/lz4/v4"

	"spm-platform/api/internal/events"
	"spm-platform/api/internal/models"
	"spm-platform/api/internal/snapshot"
)

const (
	consumerName  = "event-worker"
	streamName    = "SPM_EVENTS"
	filterSubject = "spm.events.>"
	workerID      = "default"
	ackWait       = 30 * time.Second
)

// Worker maintains the project snapshot read model.
type Worker struct {
	client   *events.Client
	pool     *pgxpool.Pool
	snapRepo *snapshot.Repo
}

// New creates a Worker.
func New(client *events.Client, pool *pgxpool.Pool, snapRepo *snapshot.Repo) *Worker {
	return &Worker{client: client, pool: pool, snapRepo: snapRepo}
}

// Start creates the durable pull consumer and starts the processing loop.
func (w *Worker) Start(ctx context.Context) error {
	resumeSeq, err := w.readResumeSequence(ctx)
	if err != nil {
		return fmt.Errorf("worker: failed to read resume sequence: %w", err)
	}

	cfg := &nats.ConsumerConfig{
		Durable:       consumerName,
		AckPolicy:     nats.AckExplicitPolicy,
		MaxAckPending: 1,
		AckWait:       ackWait,
		FilterSubject: filterSubject,
		DeliverPolicy: nats.DeliverAllPolicy,
	}
	if resumeSeq > 0 {
		cfg.DeliverPolicy = nats.DeliverByStartSequencePolicy
		cfg.OptStartSeq = uint64(resumeSeq)
	}

	js := w.client.JetStream()
	_ = js.DeleteConsumer(streamName, consumerName)
	if _, err := js.AddConsumer(streamName, cfg); err != nil {
		return fmt.Errorf("worker: failed to create pull consumer: %w", err)
	}

	sub, err := js.PullSubscribe(filterSubject, consumerName, nats.Bind(streamName, consumerName))
	if err != nil {
		return fmt.Errorf("worker: failed to subscribe to %s: %w", filterSubject, err)
	}

	go w.runPull(ctx, sub)
	return nil
}

func (w *Worker) runPull(ctx context.Context, sub *nats.Subscription) {
	defer func() {
		if err := sub.Drain(); err != nil {
			log.Printf("worker: subscription drain error: %v", err)
		}
	}()

	for {
		if ctx.Err() != nil {
			return
		}

		msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				continue
			}
			if errors.Is(err, nats.ErrConnectionClosed) || errors.Is(err, nats.ErrBadSubscription) {
				log.Printf("worker: subscription closed, stopping: %v", err)
				return
			}
			log.Printf("worker: fetch error: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if len(msgs) == 0 {
			continue
		}
		w.processMessage(ctx, msgs[0])
	}
}

func (w *Worker) processMessage(ctx context.Context, msg *nats.Msg) {
	meta, err := msg.Metadata()
	if err != nil || meta == nil {
		log.Printf("worker: message without JetStream metadata (subject=%s): %v — terminating", msg.Subject, err)
		_ = msg.Term()
		return
	}
	seq := int64(meta.Sequence.Stream)

	var env models.EventEnvelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		log.Printf("worker: invalid envelope seq=%d subject=%s: %v — terminating", seq, msg.Subject, err)
		_ = msg.Term()
		return
	}
	env.SequenceID = seq
	if env.CreatedAt.IsZero() {
		env.CreatedAt = meta.Timestamp
	}

	newState, oldState, changed, err := w.applyInTransaction(ctx, env)
	if err != nil {
		if meta.NumDelivered >= 5 {
			log.Printf("worker: transaction failed seq=%d event=%s after %d deliveries: %v — terminating poison message",
				seq, env.EventType, meta.NumDelivered, err)
			_ = msg.Term()
			return
		}
		log.Printf("worker: transaction failed seq=%d event=%s: %v — redelivery scheduled", seq, env.EventType, err)
		_ = msg.Nak()
		return
	}

	if changed {
		if err := w.publishPatch(env, oldState, newState); err != nil {
			log.Printf("worker: publish update seq=%d event=%s: %v", seq, env.EventType, err)
		}
	}

	if err := msg.Ack(); err != nil {
		log.Printf("worker: ACK failed seq=%d: %v", seq, err)
	}
}

// applyInTransaction returns (newState, oldState, changed, error).
// A duplicate sequence (already applied) returns changed=false and no error.
func (w *Worker) applyInTransaction(ctx context.Context, env models.EventEnvelope) (models.ProjectState, models.ProjectState, bool, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return models.ProjectState{}, models.ProjectState{}, false, fmt.Errorf("worker: begin transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				log.Printf("worker: rollback error: %v", rbErr)
			}
		}
	}()

	var lastSeq int64
	err = tx.QueryRow(ctx,
		`SELECT last_sequence_id FROM worker_state WHERE worker_id = $1 FOR UPDATE`, workerID,
	).Scan(&lastSeq)
	if errors.Is(err, pgx.ErrNoRows) {
		lastSeq = 0
		_, _ = tx.Exec(ctx,
			`INSERT INTO worker_state (worker_id, last_sequence_id, updated_at) VALUES ($1, 0, NOW()) ON CONFLICT DO NOTHING`,
			workerID)
	} else if err != nil {
		return models.ProjectState{}, models.ProjectState{}, false, fmt.Errorf("worker: lock worker_state: %w", err)
	}

	if env.SequenceID <= lastSeq {
		if err := tx.Commit(ctx); err != nil {
			return models.ProjectState{}, models.ProjectState{}, false, fmt.Errorf("worker: commit duplicate: %w", err)
		}
		committed = true
		return models.ProjectState{}, models.ProjectState{}, false, nil
	}

	// Events are project-scoped. A project can be deleted after its event was
	// published; such an event is still mirrored for audit, but it must not
	// poison the queue by failing the snapshots foreign key forever.
	var projectExists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1)`, env.AggregateID,
	).Scan(&projectExists); err != nil {
		return models.ProjectState{}, models.ProjectState{}, false, fmt.Errorf("worker: check project: %w", err)
	}
	if !projectExists {
		if err := w.snapRepo.InsertEvent(ctx, tx, env); err != nil {
			return models.ProjectState{}, models.ProjectState{}, false, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE worker_state SET last_sequence_id = $1, updated_at = NOW() WHERE worker_id = $2`,
			env.SequenceID, workerID,
		); err != nil {
			return models.ProjectState{}, models.ProjectState{}, false, fmt.Errorf("worker: update worker_state: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return models.ProjectState{}, models.ProjectState{}, false, fmt.Errorf("worker: commit orphan event: %w", err)
		}
		committed = true
		log.Printf("worker: skipped snapshot for unknown project %s seq=%d event=%s", env.AggregateID, env.SequenceID, env.EventType)
		return models.ProjectState{}, models.ProjectState{}, false, nil
	}

	oldState, err := w.loadStateForUpdate(ctx, tx, env.AggregateID)
	if err != nil {
		return models.ProjectState{}, models.ProjectState{}, false, err
	}

	if err := w.snapRepo.InsertEvent(ctx, tx, env); err != nil {
		return models.ProjectState{}, models.ProjectState{}, false, err
	}

	newState := oldState
	changed := false
	applied, applyErr := snapshot.Apply(oldState, env)
	switch {
	case applyErr == nil:
		newState = applied
		changed = !sameState(oldState, newState)
	case errors.Is(applyErr, snapshot.ErrUnknownEventType):
		log.Printf("worker: unknown event type %q seq=%d — recorded without state change", env.EventType, env.SequenceID)
	default:
		return models.ProjectState{}, models.ProjectState{}, false, fmt.Errorf("worker: apply event: %w", applyErr)
	}

	stateJSON, err := json.Marshal(newState)
	if err != nil {
		return models.ProjectState{}, models.ProjectState{}, false, fmt.Errorf("worker: marshal state: %w", err)
	}
	snap := models.Snapshot{ProjectID: env.AggregateID, Data: stateJSON, LastSequenceID: env.SequenceID}
	if err := w.snapRepo.Upsert(ctx, tx, snap); err != nil {
		return models.ProjectState{}, models.ProjectState{}, false, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE worker_state SET last_sequence_id = $1, updated_at = NOW() WHERE worker_id = $2`,
		env.SequenceID, workerID,
	); err != nil {
		return models.ProjectState{}, models.ProjectState{}, false, fmt.Errorf("worker: update worker_state: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return models.ProjectState{}, models.ProjectState{}, false, fmt.Errorf("worker: commit: %w", err)
	}
	committed = true
	return newState, oldState, changed, nil
}

func (w *Worker) loadStateForUpdate(ctx context.Context, tx pgx.Tx, projectID uuid.UUID) (models.ProjectState, error) {
	const q = `SELECT data FROM snapshots WHERE project_id = $1 FOR UPDATE`
	var compressed []byte
	err := tx.QueryRow(ctx, q, projectID).Scan(&compressed)
	if errors.Is(err, pgx.ErrNoRows) {
		return loadInitialState(ctx, tx, projectID)
	}
	if err != nil {
		return models.ProjectState{}, fmt.Errorf("worker: select snapshot for update: %w", err)
	}

	raw, err := decompress(compressed)
	if err != nil {
		return models.ProjectState{}, fmt.Errorf("worker: decompress snapshot: %w", err)
	}
	var state models.ProjectState
	if err := json.Unmarshal(raw, &state); err != nil {
		return models.ProjectState{}, fmt.Errorf("worker: unmarshal snapshot: %w", err)
	}
	if state.ProjectID == uuid.Nil {
		state.ProjectID = projectID
	}
	return state, nil
}

// loadInitialState builds a new snapshot from the relational project metadata.
// This keeps the read model correct even if the project.created event was lost
// or was consumed before the project row became visible.
func loadInitialState(ctx context.Context, tx pgx.Tx, projectID uuid.UUID) (models.ProjectState, error) {
	state := models.ProjectState{
		ProjectID:     projectID,
		Columns:       models.DefaultBoardColumns(),
		Tickets:       []models.Ticket{},
		Members:       []models.ProjectMember{},
		Pipelines:     []models.PipelineStatus{},
		Deployments:   []models.DeploymentStatus{},
		Notifications: []models.Notification{},
	}
	const q = `
		SELECT name, COALESCE(description, ''), owner_id, repo_name, default_branch, app_name, created_at
		FROM projects WHERE id = $1`
	err := tx.QueryRow(ctx, q, projectID).Scan(
		&state.Name, &state.Description, &state.OwnerID, &state.RepoName,
		&state.DefaultBranch, &state.AppName, &state.UpdatedAt,
	)
	if err != nil {
		return models.ProjectState{}, fmt.Errorf("worker: load project metadata: %w", err)
	}
	rows, err := tx.Query(ctx,
		`SELECT user_id, role FROM project_members WHERE project_id = $1 ORDER BY user_id`, projectID)
	if err != nil {
		return models.ProjectState{}, fmt.Errorf("worker: load project members: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var member models.ProjectMember
		if err := rows.Scan(&member.UserID, &member.Role); err != nil {
			return models.ProjectState{}, fmt.Errorf("worker: scan project member: %w", err)
		}
		state.Members = append(state.Members, member)
	}
	if err := rows.Err(); err != nil {
		return models.ProjectState{}, fmt.Errorf("worker: iterate project members: %w", err)
	}
	return state, nil
}

func (w *Worker) publishPatch(env models.EventEnvelope, oldState, newState models.ProjectState) error {
	oldJSON, err := json.Marshal(oldState)
	if err != nil {
		return fmt.Errorf("marshal old state: %w", err)
	}
	newJSON, err := json.Marshal(newState)
	if err != nil {
		return fmt.Errorf("marshal new state: %w", err)
	}

	patch, err := snapshot.Diff(oldJSON, newJSON)
	if err != nil {
		return fmt.Errorf("diff state: %w", err)
	}
	if len(patch) == 0 {
		return nil
	}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}

	return w.client.PublishUpdate(events.Update{
		ID:         env.SequenceID,
		ProjectID:  env.AggregateID,
		EventType:  env.EventType,
		Patch:      patchJSON,
		OccurredAt: env.CreatedAt,
	})
}

func (w *Worker) readResumeSequence(ctx context.Context) (int64, error) {
	const query = `SELECT last_sequence_id FROM worker_state WHERE worker_id = $1`
	var lastSeq int64
	err := w.pool.QueryRow(ctx, query, workerID).Scan(&lastSeq)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("worker: query worker_state: %w", err)
	}
	if lastSeq <= 0 {
		return 0, nil
	}
	return lastSeq + 1, nil
}

func sameState(a, b models.ProjectState) bool {
	aj, errA := json.Marshal(a)
	bj, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(aj, bj)
}

func decompress(src []byte) ([]byte, error) {
	r := lz4.NewReader(bytes.NewReader(src))
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, fmt.Errorf("lz4 decompress: %w", err)
	}
	return buf.Bytes(), nil
}
