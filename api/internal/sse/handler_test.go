package sse

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/models"
)

func TestSubscribe_ReplaySafety(t *testing.T) {
	manager := &Manager{
		bufs: make(map[string]*RingBuffer),
		subs: make(map[string]map[int64]*subscriber),
	}

	_, _, _, ok := manager.subscribe("project", "user-1", "", 10, true, 5)
	assert.False(t, ok, "a cursor ahead of the durable sequence must request a resync")

	_, _, _, ok = manager.subscribe("project", "user-1", "", 4, true, 5)
	assert.False(t, ok, "a missing gap must request a resync")

	replay, _, unsubscribe, ok := manager.subscribe("project", "user-1", "", 5, true, 5)
	require.True(t, ok)
	assert.Empty(t, replay, "a cursor equal to the snapshot needs no replay")
	unsubscribe()

	manager.bufs["project"].Append(SSEEvent{ID: 6, EventType: "ticket.created", Data: []byte(`[]`)})
	replay, _, unsubscribe, ok = manager.subscribe("project", "user-1", "", 5, true, 6)
	require.True(t, ok)
	require.Len(t, replay, 1)
	assert.Equal(t, int64(6), replay[0].ID)
	unsubscribe()
}

func TestStreamFlushesInitialComment(t *testing.T) {
	manager := &Manager{
		bufs: make(map[string]*RingBuffer),
		subs: make(map[string]map[int64]*subscriber),
	}

	router := gin.New()
	router.GET("/api/v1/projects/:id/stream", func(c *gin.Context) {
		c.Set(auth.ClaimsKey, &models.AuthClaims{UserID: "user-1", Role: auth.RoleAdmin})
		c.Next()
	}, manager.Stream)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		server.URL+"/api/v1/projects/0192f3a4-2222-7000-8000-000000000001/stream", nil)
	require.NoError(t, err)

	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()

	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	assert.Equal(t, "no", response.Header.Get("X-Accel-Buffering"))

	line, err := bufio.NewReader(response.Body).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, ": connected\n", line,
		"EventSource needs an immediate body byte so onopen fires on quiet projects")
}

func TestActiveConnectionsTracksSubscribeAndUnsubscribe(t *testing.T) {
	manager := &Manager{
		bufs: make(map[string]*RingBuffer),
		subs: make(map[string]map[int64]*subscriber),
	}

	assert.Equal(t, 0, manager.ActiveConnections())

	_, _, unsubscribe, ok := manager.subscribe("project", "user-1", "", 0, false, 0)
	require.True(t, ok)
	assert.Equal(t, 1, manager.ActiveConnections())

	unsubscribe()
	assert.Equal(t, 0, manager.ActiveConnections())
}

func TestEvictUserClosesOnlyThatUsersStreams(t *testing.T) {
	manager := &Manager{
		bufs: make(map[string]*RingBuffer),
		subs: make(map[string]map[int64]*subscriber),
	}

	_, removedCh, _, ok := manager.subscribe("project-a", "removed-user", "", 0, false, 0)
	require.True(t, ok)
	_, keptCh, _, ok := manager.subscribe("project-a", "other-user", "", 0, false, 0)
	require.True(t, ok)
	_, otherProjectCh, _, ok := manager.subscribe("project-b", "removed-user", "", 0, false, 0)
	require.True(t, ok)

	assert.Equal(t, 2, manager.evictUser("removed-user"))

	_, open := <-removedCh
	assert.False(t, open, "the removed member's project-a stream must close")
	_, open = <-otherProjectCh
	assert.False(t, open, "the removed member's project-b stream must close")
	select {
	case _, open := <-keptCh:
		assert.True(t, open, "another member's stream must stay open")
	default:
	}
	assert.Equal(t, 1, manager.ActiveConnections())
}

func TestHandleInvalidationEvictsByScope(t *testing.T) {
	manager := &Manager{
		bufs: make(map[string]*RingBuffer),
		subs: make(map[string]map[int64]*subscriber),
	}
	_, userCh, _, ok := manager.subscribe("project-a", "removed-user", "", 0, false, 0)
	require.True(t, ok)
	_, otherCh, _, ok := manager.subscribe("project-a", "other-user", "", 0, false, 0)
	require.True(t, ok)

	manager.handleInvalidation([]byte(`{"scope":"user","user_id":"removed-user","project_id":"project-a","reason":"member_removed"}`))
	_, open := <-userCh
	assert.False(t, open, "a user-scope invalidation closes only that user's streams")
	select {
	case _, open := <-otherCh:
		assert.True(t, open, "other members on the same project stay connected")
	default:
	}

	_, projectCh, _, ok := manager.subscribe("project-a", "other-user", "", 0, false, 0)
	require.True(t, ok)
	manager.handleInvalidation([]byte(`{"scope":"project","project_id":"project-a","reason":"project_archived"}`))
	_, open = <-projectCh
	assert.False(t, open, "a project-scope invalidation closes every stream")
}

func TestHandleInvalidationEvictsSession(t *testing.T) {
	manager := &Manager{
		bufs: make(map[string]*RingBuffer),
		subs: make(map[string]map[int64]*subscriber),
	}
	_, revokedCh, _, ok := manager.subscribe("project-a", "user-1", "session-a", 0, false, 0)
	require.True(t, ok)
	_, sameUserCh, _, ok := manager.subscribe("project-b", "user-1", "session-b", 0, false, 0)
	require.True(t, ok)

	manager.handleInvalidation([]byte(`{"scope":"session","session_id":"session-a","user_id":"user-1","reason":"user_revoked"}`))
	_, open := <-revokedCh
	assert.False(t, open, "a session-scope invalidation closes only that session's stream")
	select {
	case _, open := <-sameUserCh:
		assert.True(t, open, "another session of the same user stays connected")
	default:
	}
}

func TestEvictProjectClosesEveryStream(t *testing.T) {
	manager := &Manager{
		bufs: make(map[string]*RingBuffer),
		subs: make(map[string]map[int64]*subscriber),
	}

	_, firstCh, _, ok := manager.subscribe("archived-project", "user-1", "", 0, false, 0)
	require.True(t, ok)
	_, secondCh, _, ok := manager.subscribe("archived-project", "user-2", "", 0, false, 0)
	require.True(t, ok)
	_, otherCh, _, ok := manager.subscribe("active-project", "user-1", "", 0, false, 0)
	require.True(t, ok)

	assert.Equal(t, 2, manager.evictProject("archived-project"))
	_, open := <-firstCh
	assert.False(t, open)
	_, open = <-secondCh
	assert.False(t, open)
	select {
	case _, open := <-otherCh:
		assert.True(t, open, "streams for other projects must stay open")
	default:
	}
	assert.Equal(t, 1, manager.ActiveConnections())
}
