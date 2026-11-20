package audit

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned when an audit row does not exist.
var ErrNotFound = errors.New("audit: event not found")

const (
	defaultPageSize = 50
	maxPageSize     = 200
	maxExportRows   = 50000
)

// Filter selects audit rows. Action is a prefix match; the remaining fields
// are exact. From and To are inclusive lower and exclusive upper bounds.
type Filter struct {
	Action    string
	ActorID   string
	ProjectID string
	Result    string
	From      time.Time
	To        time.Time
	Cursor    string
	Limit     int
}

// Page is one keyset-paginated page of audit events.
type Page struct {
	Events     []Event `json:"events"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

const eventSelect = `
	SELECT e.id::text, e.occurred_at, e.actor_id::text, COALESCE(u.username, ''),
	       e.actor_role, e.action, e.target_type, e.target_id, e.project_id::text,
	       COALESCE(p.name, ''), e.result, e.reason, COALESCE(host(e.ip), ''),
	       e.user_agent, e.request_id, e.before_state, e.after_state, e.prev_hash, e.hash
	FROM audit_events e
	LEFT JOIN users u ON u.id = e.actor_id
	LEFT JOIN projects p ON p.id = e.project_id`

// List returns the newest matching audit events with a keyset cursor.
func (l *Logger) List(ctx context.Context, filter Filter) (Page, error) {
	page := Page{Events: []Event{}}
	if !l.Enabled() {
		return page, nil
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}

	where := make([]string, 0, 7)
	args := make([]any, 0, 10)
	arg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if action := strings.TrimSpace(filter.Action); action != "" {
		where = append(where, "e.action LIKE "+arg(action+"%"))
	}
	if actor := strings.TrimSpace(filter.ActorID); actor != "" {
		where = append(where, "e.actor_id = "+arg(actor)+"::uuid")
	}
	if project := strings.TrimSpace(filter.ProjectID); project != "" {
		where = append(where, "e.project_id = "+arg(project)+"::uuid")
	}
	if result := strings.TrimSpace(filter.Result); result != "" {
		where = append(where, "e.result = "+arg(result))
	}
	if !filter.From.IsZero() {
		where = append(where, "e.occurred_at >= "+arg(filter.From))
	}
	if !filter.To.IsZero() {
		where = append(where, "e.occurred_at < "+arg(filter.To))
	}
	if raw := strings.TrimSpace(filter.Cursor); raw != "" {
		cursor, err := decodeCursor(raw)
		if err != nil {
			return page, err
		}
		where = append(where, "(e.occurred_at, e.id) < ("+arg(cursor.occurredAt)+", "+arg(cursor.id)+"::uuid)")
	}
	query := eventSelect
	if len(where) > 0 {
		query += "\n\tWHERE " + strings.Join(where, " AND ")
	}
	query += "\n\tORDER BY e.occurred_at DESC, e.id DESC\n\tLIMIT " + arg(limit+1)

	rows, err := l.pool.Query(ctx, query, args...)
	if err != nil {
		return page, fmt.Errorf("audit: list: %w", err)
	}
	defer rows.Close()

	events, err := scanEvents(rows)
	if err != nil {
		return page, err
	}
	if len(events) > limit {
		last := events[limit-1]
		page.NextCursor = encodeCursor(last.OccurredAt, last.ID)
		events = events[:limit]
	}
	page.Events = events
	return page, nil
}

// Get returns one audit event by ID.
func (l *Logger) Get(ctx context.Context, id string) (Event, error) {
	if !l.Enabled() {
		return Event{}, ErrNotFound
	}
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return Event{}, ErrNotFound
	}
	rows, err := l.pool.Query(ctx, eventSelect+` WHERE e.id = $1`, parsed)
	if err != nil {
		return Event{}, fmt.Errorf("audit: get: %w", err)
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil {
		return Event{}, err
	}
	if len(events) == 0 {
		return Event{}, ErrNotFound
	}
	return events[0], nil
}

// Export returns at most maxExportRows events rendered as CSV or JSON.
func (l *Logger) Export(ctx context.Context, filter Filter, format string) ([]byte, string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		return nil, "", errors.New("audit: export format must be csv or json")
	}

	all := make([]Event, 0, 256)
	cursor := filter.Cursor
	for len(all) < maxExportRows {
		pageFilter := filter
		pageFilter.Cursor = cursor
		pageFilter.Limit = maxPageSize
		page, err := l.List(ctx, pageFilter)
		if err != nil {
			return nil, "", err
		}
		all = append(all, page.Events...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(all) > maxExportRows {
		all = all[:maxExportRows]
	}

	if format == "json" {
		body, err := json.MarshalIndent(all, "", "  ")
		if err != nil {
			return nil, "", fmt.Errorf("audit: render JSON export: %w", err)
		}
		return body, "application/json", nil
	}
	body, err := RenderCSV(all)
	if err != nil {
		return nil, "", err
	}
	return body, "text/csv", nil
}

// RenderCSV renders audit events as CSV. Cells that a spreadsheet would treat
// as a formula are prefixed with an apostrophe to prevent CSV injection.
func RenderCSV(events []Event) ([]byte, error) {
	var builder strings.Builder
	writer := csv.NewWriter(&builder)
	header := []string{
		"id", "occurred_at", "actor_id", "actor_username", "actor_role", "action",
		"target_type", "target_id", "project_id", "project_name", "result", "reason",
		"ip", "user_agent", "request_id", "before", "after", "prev_hash", "hash",
	}
	if err := writer.Write(header); err != nil {
		return nil, err
	}
	for _, event := range events {
		row := []string{
			event.ID,
			event.OccurredAt.UTC().Format(time.RFC3339Nano),
			valueOrEmpty(event.ActorID),
			event.ActorUsername,
			event.ActorRole,
			event.Action,
			event.TargetType,
			event.TargetID,
			valueOrEmpty(event.ProjectID),
			event.ProjectName,
			event.Result,
			event.Reason,
			event.IP,
			event.UserAgent,
			event.RequestID,
			string(event.Before),
			string(event.After),
			event.PrevHash,
			event.Hash,
		}
		for i := range row {
			row[i] = csvSafe(row[i])
		}
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return []byte(builder.String()), nil
}

func csvSafe(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	}
	return value
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func scanEvents(rows pgx.Rows) ([]Event, error) {
	events := make([]Event, 0)
	for rows.Next() {
		var (
			event     Event
			actorID   *string
			projectID *string
			before    []byte
			after     []byte
		)
		if err := rows.Scan(
			&event.ID, &event.OccurredAt, &actorID, &event.ActorUsername,
			&event.ActorRole, &event.Action, &event.TargetType, &event.TargetID,
			&projectID, &event.ProjectName, &event.Result, &event.Reason, &event.IP,
			&event.UserAgent, &event.RequestID, &before, &after,
			&event.PrevHash, &event.Hash,
		); err != nil {
			return nil, fmt.Errorf("audit: scan: %w", err)
		}
		event.ActorID = actorID
		event.ProjectID = projectID
		if len(before) > 0 {
			event.Before = json.RawMessage(before)
		}
		if len(after) > 0 {
			event.After = json.RawMessage(after)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: rows: %w", err)
	}
	return events, nil
}

type eventCursor struct {
	occurredAt time.Time
	id         string
}

func encodeCursor(occurredAt time.Time, id string) string {
	raw := occurredAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(raw string) (eventCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return eventCursor{}, errors.New("audit: invalid cursor")
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return eventCursor{}, errors.New("audit: invalid cursor")
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return eventCursor{}, errors.New("audit: invalid cursor")
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return eventCursor{}, errors.New("audit: invalid cursor")
	}
	return eventCursor{occurredAt: occurredAt, id: id.String()}, nil
}
