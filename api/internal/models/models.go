// Package models defines the core domain types for spm-platform.
package models

import (
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Package-level constraint constants.
const (
	// MaxEventTypeLen is the maximum number of UTF-8 characters allowed in
	// an EventEnvelope.EventType field (mirrors the VARCHAR(255) DB column).
	MaxEventTypeLen = 255

	// MaxPayloadBytes is the maximum size in bytes allowed for an
	// EventEnvelope.Payload field (mirrors the NATS MaxMsgSize limit).
	MaxPayloadBytes = 1 * 1024 * 1024 // 1 MB
)

// Valid ticket priorities.
const (
	PriorityLow      = "low"
	PriorityMedium   = "medium"
	PriorityHigh     = "high"
	PriorityCritical = "critical"
)

// Valid pipeline statuses.
const (
	PipelinePending    = "pending"
	PipelineInProgress = "in_progress"
	PipelineSuccess    = "success"
	PipelineFailure    = "failure"
	PipelineCancelled  = "cancelled"
	PipelineTimedOut   = "timed_out"
)

// EventEnvelope is the canonical event record persisted to PostgreSQL
// and published to NATS JetStream.
type EventEnvelope struct {
	SequenceID  int64           `json:"sequence_id"  db:"sequence_id"`
	EventType   string          `json:"event_type"   db:"event_type"` // max 255 chars
	AggregateID uuid.UUID       `json:"aggregate_id" db:"aggregate_id"`
	Payload     json.RawMessage `json:"payload"      db:"payload"` // JSONB, max 1 MB
	CreatedAt   time.Time       `json:"created_at"   db:"created_at"`
}

// Validate returns a non-nil error if the envelope violates any
// package-level constraints:
//   - EventType must not be empty or exceed MaxEventTypeLen UTF-8 characters.
//   - Payload must be valid JSON and not exceed MaxPayloadBytes bytes.
//   - AggregateID must be a non-nil UUID.
func (e *EventEnvelope) Validate() error {
	if e.EventType == "" {
		return errors.New("models: EventType must not be empty")
	}
	if utf8.RuneCountInString(e.EventType) > MaxEventTypeLen {
		return errors.New("models: EventType exceeds maximum length of 255 characters")
	}
	if len(e.Payload) > MaxPayloadBytes {
		return errors.New("models: Payload exceeds maximum size of 1 MB")
	}
	if len(e.Payload) > 0 && !json.Valid(e.Payload) {
		return errors.New("models: Payload is not valid JSON")
	}
	if e.AggregateID == uuid.Nil {
		return errors.New("models: AggregateID must not be nil")
	}
	return nil
}

// NewID returns a time-ordered UUID v7. PostgreSQL 18 provides the same
// generator as uuidv7(); using it in Go keeps IDs sortable by creation time
// and index-friendly across the API and database defaults.
func NewID() uuid.UUID {
	if id, err := uuid.NewV7(); err == nil {
		return id
	}
	return uuid.New()
}

// Snapshot is the pre-computed read model for a project stored in
// PostgreSQL. The Data field holds LZ4-compressed JSON.
type Snapshot struct {
	ProjectID      uuid.UUID `db:"project_id"`
	Data           []byte    `db:"data"` // LZ4-compressed JSON
	LastSequenceID int64     `db:"last_sequence_id"`
	UpdatedAt      time.Time `db:"updated_at"`
}

// ProjectState is the in-memory deserialized form of a Snapshot.Data.
// Its JSON shape is the public read model consumed by the UI, so changes here
// are API changes.
type ProjectState struct {
	ProjectID             uuid.UUID          `json:"project_id"`
	Name                  string             `json:"name"`
	Description           string             `json:"description"`
	OwnerID               uuid.UUID          `json:"owner_id"`
	RepoName              string             `json:"repo_name"`
	DefaultBranch         string             `json:"default_branch"`
	AppName               string             `json:"app_name"`
	Columns               []BoardColumn      `json:"columns"`
	Tickets               []Ticket           `json:"tickets"`
	Members               []ProjectMember    `json:"members"`
	Pipelines             []PipelineStatus   `json:"pipelines"`
	Deployments           []DeploymentStatus `json:"deployments"`
	Notifications         []Notification     `json:"notifications"`
	GitProvisioningFailed bool               `json:"git_provisioning_failed"`
	// Revision is the last event sequence applied to this snapshot. It gives
	// clients a cheap change signal for normalized workspace artifacts that do
	// not live inside the legacy snapshot payload.
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Ticket is a work item on the project board.
type Ticket struct {
	ID             uuid.UUID  `json:"id"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	State          string     `json:"state"` // Backlog | In Progress | In Review | Done | custom
	AssigneeID     *uuid.UUID `json:"assignee_id"`
	Priority       string     `json:"priority"`    // low | medium | high | critical
	BranchName     string     `json:"branch_name"` // feature/<ticket-id>
	PipelineStatus string     `json:"pipeline_status"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// BoardColumn represents a workflow state column on the board.
type BoardColumn struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	IsDefault bool      `json:"is_default"` // true for the 4 built-in columns
}

// DefaultBoardColumns returns the four built-in BoardColumn values in
// their canonical order (Backlog → In Progress → In Review → Done).
// Each column is assigned a deterministic UUID derived from its name.
func DefaultBoardColumns() []BoardColumn {
	return []BoardColumn{
		{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Name: "Backlog", Position: 0, IsDefault: true},
		{ID: uuid.MustParse("00000000-0000-0000-0000-000000000002"), Name: "In Progress", Position: 1, IsDefault: true},
		{ID: uuid.MustParse("00000000-0000-0000-0000-000000000003"), Name: "In Review", Position: 2, IsDefault: true},
		{ID: uuid.MustParse("00000000-0000-0000-0000-000000000004"), Name: "Done", Position: 3, IsDefault: true},
	}
}

// ProjectMember links a user to a project with a specific role.
type ProjectMember struct {
	UserID uuid.UUID `json:"user_id" db:"user_id"`
	Role   string    `json:"role"    db:"role"` // "admin" | "member" | "viewer"
}

// PipelineStatus tracks CI run state for a branch.
type PipelineStatus struct {
	Branch    string    `json:"branch"`
	CommitSHA string    `json:"commit_sha"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DeploymentStatus tracks Argo CD sync/health state for a project.
type DeploymentStatus struct {
	AppName      string    `json:"app_name"`
	SyncStatus   string    `json:"sync_status"`   // Synced | OutOfSync | Unknown
	HealthStatus string    `json:"health_status"` // Healthy | Degraded | Progressing | Unknown
	CommitSHA    string    `json:"commit_sha"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Notification is a targeted, persistent in-app alert attached to a project.
type Notification struct {
	ID         uuid.UUID  `json:"id"`
	Type       string     `json:"type"`
	Message    string     `json:"message"`
	TicketID   *uuid.UUID `json:"ticket_id,omitempty"`
	AssigneeID *uuid.UUID `json:"assignee_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// User is the platform user record as stored in PostgreSQL.
type User struct {
	ID            uuid.UUID  `json:"id"             db:"id"`
	Username      string     `json:"username"       db:"username"`
	Email         string     `json:"email"          db:"email"`
	PasswordHash  string     `json:"-"              db:"password_hash"`
	Role          string     `json:"role"           db:"role"` // "admin" | "member" | "viewer"
	InvalidatedAt *time.Time `json:"invalidated_at" db:"invalidated_at"`
	CreatedAt     time.Time  `json:"created_at"     db:"created_at"`
}

// AuthClaims are the JWT claims embedded in every access token.
//
// RegisteredClaims.Subject is the canonical user ID. UserID is kept for
// in-process convenience but is never serialised as "sub", avoiding the
// duplicate JSON tag bug that previously caused the field to be dropped.
// ParseToken and Middleware call Normalize to populate it from Subject.
type AuthClaims struct {
	UserID     string   `json:"-"`
	SessionID  string   `json:"sid,omitempty"` // server-side auth_sessions row
	Role       string   `json:"role"`          // "admin" | "member" | "viewer"
	ProjectIDs []string `json:"project_ids"`   // projects explicitly assigned
	jwt.RegisteredClaims
}

// Normalize synchronises UserID with the canonical RegisteredClaims.Subject.
func (c *AuthClaims) Normalize() {
	if c == nil {
		return
	}
	if c.Subject != "" {
		c.UserID = c.Subject
	} else if c.UserID != "" {
		c.Subject = c.UserID
	}
}

// CanonicalUserID returns the authoritative user ID for the claims.
func (c *AuthClaims) CanonicalUserID() string {
	if c == nil {
		return ""
	}
	if c.Subject != "" {
		return c.Subject
	}
	return c.UserID
}
