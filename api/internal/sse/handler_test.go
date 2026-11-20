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
		subs: make(map[string]map[int64]chan SSEEvent),
	}

	_, _, _, ok := manager.subscribe("project", 10, true, 5)
	assert.False(t, ok, "a cursor ahead of the durable sequence must request a resync")

	_, _, _, ok = manager.subscribe("project", 4, true, 5)
	assert.False(t, ok, "a missing gap must request a resync")

	replay, _, unsubscribe, ok := manager.subscribe("project", 5, true, 5)
	require.True(t, ok)
	assert.Empty(t, replay, "a cursor equal to the snapshot needs no replay")
	unsubscribe()

	manager.bufs["project"].Append(SSEEvent{ID: 6, EventType: "ticket.created", Data: []byte(`[]`)})
	replay, _, unsubscribe, ok = manager.subscribe("project", 5, true, 6)
	require.True(t, ok)
	require.Len(t, replay, 1)
	assert.Equal(t, int64(6), replay[0].ID)
	unsubscribe()
}

func TestStreamFlushesInitialComment(t *testing.T) {
	manager := &Manager{
		bufs: make(map[string]*RingBuffer),
		subs: make(map[string]map[int64]chan SSEEvent),
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
		subs: make(map[string]map[int64]chan SSEEvent),
	}

	assert.Equal(t, 0, manager.ActiveConnections())

	_, _, unsubscribe, ok := manager.subscribe("project", 0, false, 0)
	require.True(t, ok)
	assert.Equal(t, 1, manager.ActiveConnections())

	unsubscribe()
	assert.Equal(t, 0, manager.ActiveConnections())
}
