// Package events provides the NATS JetStream client for the SPM platform.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"spm-platform/api/internal/metrics"
	"spm-platform/api/internal/models"
)

// Sentinel errors returned by Publish.
var (
	ErrConflict = errors.New("events: optimistic concurrency conflict")
	ErrTimeout  = errors.New("events: publish timeout — event store unavailable")
)

const (
	streamName           = "SPM_EVENTS"
	kvUserInvalidation   = "user_invalidation"
	publishTimeout       = 5 * time.Second
	defaultRetentionDays = 90
	minRetentionDays     = 1
	maxRetentionDays     = 3650
)

// InvalidationSubject is the core NATS subject used to evict live resources
// (currently SSE streams) when a token or project loses access. Every API
// replica subscribes to it, so an event raised by one replica reaches all.
const InvalidationSubject = "spm.authz.invalidation"

// AuthzInvalidation is the control message published on InvalidationSubject.
// Scope selects the eviction: "user" closes every stream owned by UserID;
// "project" closes every stream subscribed to ProjectID; "session" closes the
// stream bound to SessionID. Reasons are fixed operational strings, never user
// input, because they become metric labels.
type AuthzInvalidation struct {
	Scope     string `json:"scope"`
	UserID    string `json:"user_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Reason    string `json:"reason"`
}

// Update is the envelope published on core NATS for SSE fan-out.
type Update struct {
	ID         int64           `json:"id"`
	ProjectID  uuid.UUID       `json:"project_id"`
	EventType  string          `json:"event_type"`
	Patch      json.RawMessage `json:"patch"`
	OccurredAt time.Time       `json:"occurred_at"`
}

// Client wraps a NATS connection and JetStream context.
type Client struct {
	conn             *nats.Conn
	js               nats.JetStreamContext
	userInvalidation nats.KeyValue
}

// New connects to NATS, ensures the event stream and invalidation KV exist.
func New(natsURL string, retentionDays int) (*Client, error) {
	if retentionDays < minRetentionDays {
		retentionDays = minRetentionDays
	}
	if retentionDays > maxRetentionDays {
		retentionDays = maxRetentionDays
	}

	nc, err := nats.Connect(natsURL,
		nats.Name("spm-api"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Timeout(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("events: failed to connect to NATS at %q: %w", natsURL, err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("events: failed to obtain JetStream context: %w", err)
	}

	if err := ensureStream(js, retentionDays); err != nil {
		nc.Close()
		return nil, fmt.Errorf("events: failed to bootstrap %s stream: %w", streamName, err)
	}

	invalidationKV, err := ensureKV(js, kvUserInvalidation, nats.MemoryStorage, 0)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("events: failed to bootstrap %s KV bucket: %w", kvUserInvalidation, err)
	}

	return &Client{conn: nc, js: js, userInvalidation: invalidationKV}, nil
}

func ensureStream(js nats.JetStreamContext, retentionDays int) error {
	cfg := &nats.StreamConfig{
		Name:       streamName,
		Subjects:   []string{"spm.events.>"},
		Storage:    nats.FileStorage,
		Retention:  nats.LimitsPolicy,
		MaxAge:     time.Duration(retentionDays) * 24 * time.Hour,
		Replicas:   1,
		MaxMsgSize: models.MaxPayloadBytes,
		Discard:    nats.DiscardOld,
		NoAck:      false,
	}

	info, err := js.StreamInfo(streamName)
	if err != nil {
		if errors.Is(err, nats.ErrStreamNotFound) {
			_, err = js.AddStream(cfg)
			return err
		}
		return err
	}
	if info.Config.MaxAge != cfg.MaxAge || info.Config.MaxMsgSize != cfg.MaxMsgSize {
		_, err = js.UpdateStream(cfg)
	}
	return err
}

func ensureKV(js nats.JetStreamContext, bucket string, storage nats.StorageType, ttl time.Duration) (nats.KeyValue, error) {
	kv, err := js.KeyValue(bucket)
	if err == nil {
		return kv, nil
	}
	if !errors.Is(err, nats.ErrBucketNotFound) {
		return nil, err
	}
	return js.CreateKeyValue(&nats.KeyValueConfig{Bucket: bucket, Storage: storage, TTL: ttl})
}

// Publish publishes an EventEnvelope to SPM_EVENTS and returns the
// authoritative JetStream stream sequence assigned to it.
//
// All domain events are project-scoped, so AggregateID is the project UUID.
// The subject remains spm.events.<aggregate-type>.<project-id>; the aggregate
// type is only a routing prefix inside the stream.
func (c *Client) Publish(ctx context.Context, envelope models.EventEnvelope, expectedSeq int64) (int64, error) {
	if err := envelope.Validate(); err != nil {
		return 0, fmt.Errorf("events: invalid envelope: %w", err)
	}

	if envelope.CreatedAt.IsZero() {
		envelope.CreatedAt = time.Now().UTC()
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		return 0, fmt.Errorf("events: failed to marshal envelope: %w", err)
	}

	aggregateType := aggregateTypeFromEventType(envelope.EventType)
	subject := fmt.Sprintf("spm.events.%s.%s", aggregateType, envelope.AggregateID.String())

	msg := nats.NewMsg(subject)
	msg.Data = body
	if envelope.EventType != "" {
		msg.Header.Set("Spm-Event-Type", envelope.EventType)
	}
	if expectedSeq > 0 {
		msg.Header.Set(nats.ExpectedLastSubjSeqHdr, fmt.Sprintf("%d", expectedSeq))
	}

	publishCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		publishCtx, cancel = context.WithTimeout(ctx, publishTimeout)
		defer cancel()
	}

	paf, err := c.js.PublishMsgAsync(msg)
	if err != nil {
		return 0, mapPublishError(err)
	}

	select {
	case ack := <-paf.Ok():
		return int64(ack.Sequence), nil
	case pubErr := <-paf.Err():
		return 0, mapPublishError(pubErr)
	case <-publishCtx.Done():
		return 0, ErrTimeout
	}
}

// PublishUpdate fans a JSON Patch out to SSE clients. Core NATS is used
// deliberately: snapshot durability covers recovery, while the SSE ring
// buffer covers short reconnects.
func (c *Client) PublishUpdate(update Update) error {
	if update.Patch == nil {
		update.Patch = json.RawMessage("[]")
	}
	body, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("events: marshal update: %w", err)
	}
	subject := fmt.Sprintf("ui.updates.project.%s.%s", update.ProjectID.String(), update.EventType)
	if err := c.conn.Publish(subject, body); err != nil {
		return fmt.Errorf("events: publish update: %w", err)
	}
	return c.conn.FlushTimeout(2 * time.Second)
}

// UpdateSubjectPrefix is the core NATS namespace consumed by the SSE hub.
const UpdateSubjectPrefix = "ui.updates.>"

func aggregateTypeFromEventType(eventType string) string {
	if idx := strings.IndexByte(eventType, '.'); idx >= 0 {
		return eventType[:idx]
	}
	return eventType
}

func mapPublishError(err error) error {
	if err == nil {
		return nil
	}
	errStr := err.Error()
	if strings.Contains(errStr, "wrong last msg sequence") ||
		strings.Contains(errStr, "wrong last sequence") ||
		strings.Contains(errStr, "sequence mismatch") {
		return fmt.Errorf("%w: %s", ErrConflict, errStr)
	}
	if errors.Is(err, nats.ErrTimeout) || strings.Contains(errStr, "timeout") {
		return fmt.Errorf("%w: %s", ErrTimeout, errStr)
	}
	return err
}

// UserInvalidation returns the KV bucket used for JWT revocation lookups.
func (c *Client) UserInvalidation() nats.KeyValue { return c.userInvalidation }

// InvalidateUser records the current Unix timestamp for a user. JWTs issued
// before this timestamp are rejected by the auth middleware. The invalidation
// is also published through NATS so every replica evicts the user's SSE
// streams.
func (c *Client) InvalidateUser(ctx context.Context, userID string) error {
	return c.invalidateUser(ctx, userID, "", "token")
}

// InvalidateMembership invalidates a user's tokens after their project
// membership changes, and publishes the project scope so other replicas can
// close that user's streams.
func (c *Client) InvalidateMembership(ctx context.Context, userID, projectID, reason string) error {
	return c.invalidateUser(ctx, userID, projectID, reason)
}

// InvalidateProject publishes a project-wide invalidation. Every replica closes
// the affected SSE streams; project access itself is denied by the membership
// resolver as soon as the archive commits.
func (c *Client) InvalidateProject(ctx context.Context, projectID, reason string) error {
	if c == nil {
		return nil
	}
	inv := AuthzInvalidation{Scope: "project", ProjectID: projectID, Reason: reason}
	if err := c.publishInvalidation(ctx, inv); err != nil {
		return err
	}
	metrics.Default().ObserveAuthzInvalidation("project", reason)
	return nil
}

// InvalidateSession publishes a session-scoped eviction. The auth_sessions row
// is already revoked when this is called; the message lets every replica close
// the matching SSE stream immediately instead of waiting for its heartbeat.
func (c *Client) InvalidateSession(ctx context.Context, sessionID, userID, reason string) error {
	if c == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	inv := AuthzInvalidation{Scope: "session", SessionID: sessionID, UserID: userID, Reason: reason}
	if err := c.publishInvalidation(ctx, inv); err != nil {
		return err
	}
	metrics.Default().ObserveAuthzInvalidation("session", reason)
	return nil
}

func (c *Client) invalidateUser(ctx context.Context, userID, projectID, reason string) error {
	if c == nil {
		return nil
	}
	var errs []error
	if c.userInvalidation != nil {
		// Add one second so a token issued in the same wall-clock second as
		// the change is also rejected; a token issued after the boundary is
		// accepted. JWTs carry second-precision iat.
		ts := strconv.FormatInt(time.Now().Unix()+1, 10)
		if _, err := c.userInvalidation.Put(userID, []byte(ts)); err != nil {
			errs = append(errs, fmt.Errorf("events: persist user invalidation: %w", err))
		}
	}
	inv := AuthzInvalidation{Scope: "user", UserID: userID, ProjectID: projectID, Reason: reason}
	if err := c.publishInvalidation(ctx, inv); err != nil {
		errs = append(errs, err)
	} else {
		metrics.Default().ObserveAuthzInvalidation("user", reason)
	}
	return errors.Join(errs...)
}

func (c *Client) publishInvalidation(ctx context.Context, inv AuthzInvalidation) error {
	if c == nil || c.conn == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(inv)
	if err != nil {
		return fmt.Errorf("events: marshal authz invalidation: %w", err)
	}
	if err := c.conn.Publish(InvalidationSubject, payload); err != nil {
		return fmt.Errorf("events: publish authz invalidation: %w", err)
	}
	return nil
}

// JetStream returns the JetStream context used by the worker.
func (c *Client) JetStream() nats.JetStreamContext { return c.js }

// Conn returns the underlying NATS connection (SSE hub subscription).
func (c *Client) Conn() *nats.Conn { return c.conn }

// Close drains pending messages and closes the connection.
func (c *Client) Close() {
	if c.conn != nil && !c.conn.IsClosed() {
		_ = c.conn.Drain()
	}
}
