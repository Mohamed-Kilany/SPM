//go:build integration

package integration

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test: SSE patch delivery within 500ms after ticket creation
// Requirement 4.4
// ---------------------------------------------------------------------------

func TestSSE_PatchDelivery_Within500ms(t *testing.T) {
	skipIfNoStack(t)

	user, pass := adminCreds()
	token := authToken(t, user, pass)

	// Create a project to stream against.
	projectID := createProject(t, token, "SSE Integration Test Project")

	// Open SSE connection BEFORE the mutation so we don't miss the patch.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	eventCh := readSSEEvents(ctx, t, projectID, token)

	// Wait briefly for the connection to be established.
	time.Sleep(200 * time.Millisecond)

	// Record the time before the mutation.
	start := time.Now()

	// Create a ticket — this triggers an SSE patch.
	resp := do(t, "POST", "/api/v1/projects/"+projectID+"/tickets", token, map[string]any{
		"title":    "SSE test ticket",
		"priority": "medium",
	})
	defer ignoredBody(resp)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// Wait for a data line on the SSE channel within 500ms.
	received := waitFor(t, 500*time.Millisecond, func() bool {
		select {
		case line, ok := <-eventCh:
			if !ok {
				return false
			}
			// SSE data lines start with "data: "
			return strings.HasPrefix(line, "data:")
		default:
			return false
		}
	})

	elapsed := time.Since(start)
	assert.True(t, received, "SSE patch should be received within 500ms (elapsed: %s)", elapsed)
}

// ---------------------------------------------------------------------------
// Test: SSE reconnect with a recent Last-Event-ID replays only missed events
// Requirement 4.7
// ---------------------------------------------------------------------------

func TestSSE_Reconnect_RecentCursor_ReplaysMissedEvent(t *testing.T) {
	skipIfNoStack(t)

	user, pass := adminCreds()
	token := authToken(t, user, pass)
	projectID := createProject(t, token, "SSE Cursor Replay Test Project")

	base := do(t, "POST", "/api/v1/projects/"+projectID+"/tickets", token, map[string]any{
		"title": "cursor baseline", "priority": "medium",
	})
	ignoredBody(base)

	var cursor int64
	require.True(t, waitFor(t, 5*time.Second, func() bool {
		resp := do(t, "GET", "/api/v1/projects/"+projectID, token, nil)
		defer ignoredBody(resp)
		if resp.StatusCode != http.StatusOK {
			return false
		}
		value, err := strconv.ParseInt(resp.Header.Get("X-Snapshot-Sequence"), 10, 64)
		if err != nil {
			return false
		}
		cursor = value
		return cursor > 0
	}), "snapshot cursor should advance after the baseline ticket")
	require.Greater(t, cursor, int64(0))

	missedTitle := "cursor replay missed event"
	created := do(t, "POST", "/api/v1/projects/"+projectID+"/tickets", token, map[string]any{
		"title": missedTitle, "priority": "high",
	})
	ignoredBody(created)

	require.True(t, waitFor(t, 5*time.Second, func() bool {
		resp := do(t, "GET", "/api/v1/projects/"+projectID, token, nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK && strings.Contains(string(body), missedTitle)
	}), "missed ticket should be applied to the snapshot before reconnect")
	// Let the worker publish the corresponding patch into the in-memory ring.
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL()+"/api/v1/projects/"+projectID+"/stream", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Last-Event-ID", strconv.FormatInt(cursor, 10))

	resp, err := (&http.Client{Timeout: 0}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	scanner := bufio.NewScanner(resp.Body)
	var eventID int64
	var eventType, data string
	replayed := false
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			eventID, _ = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
			if eventType == "ticket.created" && strings.Contains(data, missedTitle) {
				replayed = true
			}
		case line == "":
			if replayed {
				break
			}
		}
		if replayed {
			break
		}
	}
	require.True(t, replayed, "reconnect should replay the missed ticket.created patch")
	assert.Greater(t, eventID, cursor, "replayed patch must be newer than the client cursor")
}

// ---------------------------------------------------------------------------
// Test: SSE resync signal when Last-Event-ID is outside buffer
// Requirement 4.8
// ---------------------------------------------------------------------------

func TestSSE_ResyncSignal_WhenLastEventIDOutOfRange(t *testing.T) {
	skipIfNoStack(t)

	user, pass := adminCreds()
	token := authToken(t, user, pass)
	projectID := createProject(t, token, "SSE Resync Test Project")

	// Connect with an artificially high Last-Event-ID that exceeds any buffer.
	req, err := http.NewRequest("GET",
		baseURL()+"/api/v1/projects/"+projectID+"/stream", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Last-Event-ID", "999999999") // way outside any buffer

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := (&http.Client{Timeout: 0}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	scanner := bufio.NewScanner(resp.Body)
	resync := false
	for scanner.Scan() {
		if scanner.Text() == "event: resync" {
			resync = true
			break
		}
	}
	assert.True(t, resync, "out-of-range cursor must produce an explicit resync event")
}

// ---------------------------------------------------------------------------
// Test: Unauthenticated SSE connection → 401
// Requirement 4.1
// ---------------------------------------------------------------------------

func TestSSE_Unauthenticated_Returns401(t *testing.T) {
	skipIfNoStack(t)

	user, pass := adminCreds()
	token := authToken(t, user, pass)
	projectID := createProject(t, token, "SSE Auth Test Project")

	// Connect without auth token.
	resp, err := http.Get(baseURL() + "/api/v1/projects/" + projectID + "/stream")
	require.NoError(t, err)
	defer ignoredBody(resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
