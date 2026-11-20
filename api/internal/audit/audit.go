// Package audit owns the append-only, hash-chained audit log. Handlers write
// security and privileged actions through a Logger; the database trigger owns
// payload canonicalization, prev_hash and hash so callers cannot forge them.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/metrics"
	"spm-platform/api/internal/observability"
)

const (
	maxActionLength  = 100
	maxTargetLength  = 255
	maxReasonLength  = 2000
	maxUserAgent     = 1024
	maxStringValue   = 1024
	maxRequestID     = 128
	defaultRetention = 365
	redactedValue    = "[REDACTED]"
	truncatedSuffix  = "...[truncated]"
)

// Entry is one audit write. ActorID and ProjectID are UUID strings; an empty
// value is stored as NULL. Before and After are redacted by Record before they
// reach PostgreSQL.
type Entry struct {
	ActorID    string
	ActorRole  string
	Action     string
	TargetType string
	TargetID   string
	ProjectID  string
	Result     string
	Reason     string
	IP         string
	UserAgent  string
	RequestID  string
	Before     any
	After      any
}

// Event is the API projection of a stored audit row.
type Event struct {
	ID            string          `json:"id"`
	OccurredAt    time.Time       `json:"occurred_at"`
	ActorID       *string         `json:"actor_id,omitempty"`
	ActorUsername string          `json:"actor_username,omitempty"`
	ActorRole     string          `json:"actor_role,omitempty"`
	Action        string          `json:"action"`
	TargetType    string          `json:"target_type,omitempty"`
	TargetID      string          `json:"target_id,omitempty"`
	ProjectID     *string         `json:"project_id,omitempty"`
	ProjectName   string          `json:"project_name,omitempty"`
	Result        string          `json:"result"`
	Reason        string          `json:"reason,omitempty"`
	IP            string          `json:"ip,omitempty"`
	UserAgent     string          `json:"user_agent,omitempty"`
	RequestID     string          `json:"request_id,omitempty"`
	Before        json.RawMessage `json:"before,omitempty"`
	After         json.RawMessage `json:"after,omitempty"`
	PrevHash      string          `json:"prev_hash,omitempty"`
	Hash          string          `json:"hash,omitempty"`
}

// Logger writes audit rows. A nil Logger is a no-op so handlers stay usable in
// tests and command-line tools without a database.
type Logger struct {
	pool *pgxpool.Pool
}

// NewLogger constructs a Logger over the supplied pool.
func NewLogger(pool *pgxpool.Pool) *Logger {
	return &Logger{pool: pool}
}

// Enabled reports whether writes are persisted.
func (l *Logger) Enabled() bool {
	return l != nil && l.pool != nil
}

// Record persists one audit event. The database assigns the chain position.
func (l *Logger) Record(ctx context.Context, entry Entry) error {
	action := sanitize(entry.Action, maxActionLength)
	if action == "" {
		return errors.New("audit: action is required")
	}
	if !l.Enabled() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result := strings.ToLower(strings.TrimSpace(entry.Result))
	switch result {
	case "":
		result = "success"
	case "success", "failure", "denied":
	default:
		return fmt.Errorf("audit: unsupported result %q", result)
	}

	actorID := parseOptionalUUID(entry.ActorID)
	projectID := parseOptionalUUID(entry.ProjectID)
	ip := parseOptionalIP(entry.IP)

	started := time.Now()
	var id uuid.UUID
	err := l.pool.QueryRow(ctx, `
		INSERT INTO audit_events
			(actor_id, actor_role, action, target_type, target_id, project_id, result,
			 reason, ip, user_agent, request_id, before_state, after_state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id`,
		actorID,
		sanitize(entry.ActorRole, 20),
		action,
		sanitize(entry.TargetType, 40),
		sanitize(entry.TargetID, maxTargetLength),
		projectID,
		result,
		sanitize(entry.Reason, maxReasonLength),
		ip,
		sanitize(entry.UserAgent, maxUserAgent),
		sanitize(entry.RequestID, maxRequestID),
		redactedJSON(entry.Before),
		redactedJSON(entry.After),
	).Scan(&id)
	metrics.Default().ObserveAuditWrite(time.Since(started), err)
	if err != nil {
		return fmt.Errorf("audit: write %s: %w", action, err)
	}
	return nil
}

// RecordRequest enriches an entry with request metadata and writes it. A
// failed audit write never fails the business request; it is logged and
// exported through spm_audit_writes_total{outcome="failure"} so the alert in
// docs/AUDIT.md fires.
func (l *Logger) RecordRequest(c *gin.Context, entry Entry) {
	if !l.Enabled() {
		return
	}
	if entry.IP == "" {
		if ip := net.ParseIP(c.ClientIP()); ip != nil {
			entry.IP = ip.String()
		}
	}
	entry.UserAgent = truncate(c.Request.UserAgent(), maxUserAgent)
	entry.RequestID = observability.RequestIDFrom(c)
	if err := l.Record(c.Request.Context(), entry); err != nil {
		slog.Warn("audit_write_failed",
			"action", sanitizeLog(entry.Action),
			"request_id", sanitizeLog(entry.RequestID),
			"error", err,
		)
	}
}

// Redact converts a value to a JSON-safe copy with sensitive keys masked and
// long strings truncated. It is exported so tests and callers can prove that
// a value is safe before it is attached to an event.
func Redact(value any) any {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "[UNSERIALIZABLE]"
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "[UNSERIALIZABLE]"
	}
	return redactValue(decoded)
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if isSensitiveKey(key) {
				out[key] = redactedValue
				continue
			}
			out[key] = redactValue(nested)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, nested := range typed {
			out[i] = redactValue(nested)
		}
		return out
	case string:
		return truncate(typed, maxStringValue)
	default:
		return value
	}
}

var sensitiveFragments = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"authorization",
	"cookie",
	"credential",
	"privatekey",
	"apikey",
	"sessionid",
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	for _, fragment := range sensitiveFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func redactedJSON(value any) any {
	if value == nil {
		return nil
	}
	body, err := json.Marshal(Redact(value))
	if err != nil {
		return nil
	}
	return body
}

func parseOptionalUUID(value string) *uuid.UUID {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	parsed, err := uuid.Parse(trimmed)
	if err != nil {
		return nil
	}
	return &parsed
}

func parseOptionalIP(value string) *string {
	parsed := net.ParseIP(strings.TrimSpace(value))
	if parsed == nil {
		return nil
	}
	safe := parsed.String()
	return &safe
}

func sanitize(value string, limit int) string {
	value = strings.ReplaceAll(value, "\x00", "")
	value = strings.TrimSpace(value)
	return truncate(value, limit)
}

func truncate(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	if limit <= len(truncatedSuffix) {
		return string(runes[:limit])
	}
	return string(runes[:limit-len(truncatedSuffix)]) + truncatedSuffix
}

func sanitizeLog(value string) string {
	value = strings.ReplaceAll(value, "\n", "")
	return strings.ReplaceAll(value, "\r", "")
}

// DefaultRetentionDays is used when AUDIT_RETENTION_DAYS is unset.
func DefaultRetentionDays() int { return defaultRetention }

// RetentionDaysFromEnv parses AUDIT_RETENTION_DAYS. Zero disables pruning.
func RetentionDaysFromEnv(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultRetention
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 0 {
		return defaultRetention
	}
	return days
}

// EnsurePartitions extends the monthly partition horizon.
func (l *Logger) EnsurePartitions(ctx context.Context, monthsAhead int) error {
	if !l.Enabled() {
		return nil
	}
	if monthsAhead < 1 {
		monthsAhead = 3
	}
	if _, err := l.pool.Exec(ctx, `SELECT audit_ensure_partitions($1)`, monthsAhead); err != nil {
		return fmt.Errorf("audit: ensure partitions: %w", err)
	}
	return nil
}

// Prune drops whole expired partitions. Each drop is itself audited.
func (l *Logger) Prune(ctx context.Context, retentionDays int) (int, error) {
	if !l.Enabled() || retentionDays < 1 {
		return 0, nil
	}
	var dropped int
	if err := l.pool.QueryRow(ctx, `SELECT audit_prune($1)`, retentionDays).Scan(&dropped); err != nil {
		return 0, fmt.Errorf("audit: prune: %w", err)
	}
	if dropped > 0 {
		metrics.Default().ObserveAuditPruned(dropped)
		slog.Info("audit_partitions_pruned", "partitions", dropped, "retention_days", retentionDays)
	}
	return dropped, nil
}

// RunMaintenance extends partitions, applies retention and verifies the chain
// once at startup and then daily until the context is cancelled. Startup
// callers should also extend the partition horizon synchronously so a fresh
// deployment has a current partition before serving traffic.
func (l *Logger) RunMaintenance(ctx context.Context, retentionDays int) {
	if !l.Enabled() {
		return
	}
	l.maintenancePass(ctx, retentionDays)
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.maintenancePass(ctx, retentionDays)
		}
	}
}

func (l *Logger) maintenancePass(ctx context.Context, retentionDays int) {
	if err := l.EnsurePartitions(ctx, 3); err != nil {
		slog.Warn("audit_partition_maintenance_failed", "error", err)
	}
	if _, err := l.Prune(ctx, retentionDays); err != nil {
		slog.Warn("audit_retention_failed", "error", err)
	}
	result, err := l.Verify(ctx)
	if err != nil {
		slog.Warn("audit_verification_failed", "error", err)
		return
	}
	if !result.OK {
		slog.Error("audit_chain_broken",
			"checked", result.Checked,
			"first_bad_id", result.FirstBadID,
			"first_bad_cause", result.FirstBadCause,
		)
	}
}

// WriteStatus is a small helper for handlers that need to report whether audit
// persistence is configured without exposing the pool.
func (l *Logger) WriteStatus() string {
	if l.Enabled() {
		return "enabled"
	}
	return "disabled"
}
