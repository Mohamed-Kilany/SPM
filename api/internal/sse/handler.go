package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/events"
)

const (
	subscriberBuffer = 128
	heartbeatEvery   = 20 * time.Second
)

// Manager is the central SSE hub. A single core-NATS subscription consumes all
// UI updates; per-project ring buffers exist independently of connections, and
// active connections receive fan-out copies.
type Manager struct {
	nc     *nats.Conn
	pool   *pgxpool.Pool
	sub    *nats.Subscription
	mu     sync.Mutex
	bufs   map[string]*RingBuffer
	subs   map[string]map[int64]chan SSEEvent
	nextID int64
}

// NewManager creates an SSE Manager. Start must be called to consume updates.
// The PostgreSQL pool is used as the authoritative source of a project's
// latest event sequence when a client's replay cursor is ahead of the
// in-memory buffer (for example after an API restart).
func NewManager(nc *nats.Conn, pool *pgxpool.Pool) *Manager {
	return &Manager{
		nc:   nc,
		pool: pool,
		bufs: make(map[string]*RingBuffer),
		subs: make(map[string]map[int64]chan SSEEvent),
	}
}

// Start subscribes the hub to the UI update subject space.
func (m *Manager) Start(ctxDone <-chan struct{}) error {
	ch := make(chan *nats.Msg, 1024)
	sub, err := m.nc.ChanSubscribe(events.UpdateSubjectPrefix, ch)
	if err != nil {
		return fmt.Errorf("sse: subscribe %s: %w", events.UpdateSubjectPrefix, err)
	}
	m.sub = sub

	go func() {
		for {
			select {
			case <-ctxDone:
				_ = sub.Drain()
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				m.handleUpdate(msg.Data)
			}
		}
	}()
	return nil
}

// Close unsubscribes the hub.
func (m *Manager) Close() {
	if m.sub != nil {
		_ = m.sub.Drain()
	}
}

// ActiveConnections returns the number of live SSE subscribers across every
// project. It is used by the metrics endpoint and is safe for concurrent use.
func (m *Manager) ActiveConnections() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	for _, subs := range m.subs {
		total += len(subs)
	}
	return total
}

func (m *Manager) handleUpdate(data []byte) {
	var update events.Update
	if err := json.Unmarshal(data, &update); err != nil {
		log.Printf("sse: invalid update envelope: %v", err)
		return
	}
	if update.ProjectID == uuid.Nil || update.EventType == "" {
		return
	}
	if update.Patch == nil {
		update.Patch = json.RawMessage("[]")
	}

	ev := SSEEvent{ID: update.ID, EventType: update.EventType, Data: update.Patch}
	projectID := update.ProjectID.String()

	m.mu.Lock()
	buf := m.bufs[projectID]
	if buf == nil {
		buf = NewRingBuffer()
		m.bufs[projectID] = buf
	}
	buf.Append(ev)

	for id, ch := range m.subs[projectID] {
		select {
		case ch <- ev:
		default:
			// Disconnect slow clients rather than silently losing a patch.
			close(ch)
			delete(m.subs[projectID], id)
		}
	}
	m.mu.Unlock()
}

// Stream handles GET /api/v1/projects/:id/stream.
func (m *Manager) Stream(c *gin.Context) {
	if _, ok := auth.ClaimsFrom(c); !ok {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_ID", "Project ID is not a valid UUID")
		return
	}
	key := projectID.String()

	fromID, hasFromID := lastEventID(c)
	var latestSeq int64
	if hasFromID && m.pool != nil {
		if seq, queryErr := m.latestProjectSequence(c.Request.Context(), projectID); queryErr == nil {
			latestSeq = seq
		}
	}
	replay, ch, unsubscribe, ok := m.subscribe(key, fromID, hasFromID, latestSeq)
	if !ok {
		writeSSEHeaders(c)
		c.Status(http.StatusOK)
		fmt.Fprint(c.Writer, "event: resync\ndata: {}\n\n")
		c.Writer.Flush()
		return
	}
	defer unsubscribe()

	writeSSEHeaders(c)
	c.Status(http.StatusOK)

	for _, ev := range replay {
		_ = writeSSEEvent(c.Writer, ev)
	}
	// Flush headers and an initial comment immediately. EventSource fires
	// `onopen` only after the first body bytes arrive; without this, a quiet
	// project would look disconnected until the first heartbeat or event.
	_, _ = fmt.Fprint(c.Writer, ": connected\n\n")
	c.Writer.Flush()

	ticker := time.NewTicker(heartbeatEvery)
	defer ticker.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(c.Writer, ": ping\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			if err := writeSSEEvent(c.Writer, ev); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

func (m *Manager) subscribe(projectID string, fromID int64, replay bool, latestSeq int64) ([]SSEEvent, chan SSEEvent, func(), bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	buf := m.bufs[projectID]
	if buf == nil {
		buf = NewRingBuffer()
		m.bufs[projectID] = buf
	}

	var eventsToSend []SSEEvent
	if replay {
		var inRange bool
		eventsToSend, inRange = buf.Replay(fromID)
		if !inRange {
			if fromID == latestSeq {
				// The cursor exactly matches the durable snapshot. Every event
				// through this sequence is already present in the client's
				// snapshot, so there is nothing to replay. This also handles the
				// restart case where the in-memory buffer is empty.
				eventsToSend = []SSEEvent{}
			} else {
				// Either the client is genuinely behind the durable log, or it
				// presents a cursor ahead of the server (for example after a
				// restore/rollback). Both are unsafe to patch and must resync.
				return nil, nil, nil, false
			}
		}
	}

	ch := make(chan SSEEvent, subscriberBuffer)
	if m.subs[projectID] == nil {
		m.subs[projectID] = make(map[int64]chan SSEEvent)
	}
	m.nextID++
	subID := m.nextID
	m.subs[projectID][subID] = ch

	unsubscribe := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if subs := m.subs[projectID]; subs != nil {
			if existing, ok := subs[subID]; ok && existing == ch {
				delete(subs, subID)
			}
			if len(subs) == 0 {
				delete(m.subs, projectID)
			}
		}
	}
	return eventsToSend, ch, unsubscribe, true
}

func (m *Manager) latestProjectSequence(ctx context.Context, projectID uuid.UUID) (int64, error) {
	var seq int64
	err := m.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(sequence_id), 0) FROM events WHERE aggregate_id = $1`, projectID,
	).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("sse: query latest sequence: %w", err)
	}
	return seq, nil
}

func lastEventID(c *gin.Context) (int64, bool) {
	raw := strings.TrimSpace(c.GetHeader("Last-Event-ID"))
	if raw == "" {
		raw = strings.TrimSpace(c.Query("lastEventId"))
	}
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		return 0, false
	}
	return id, true
}

func writeSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
}

func writeSSEEvent(w interface{ Write([]byte) (int, error) }, ev SSEEvent) error {
	data := ev.Data
	if len(data) == 0 {
		data = []byte("[]")
	}
	_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\nretry: 3000\n\n", ev.ID, ev.EventType, data)
	return err
}

func writeError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
