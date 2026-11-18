//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test: Create project → create ticket → move to In Progress
// Validates the core board management write path end-to-end.
// Requirements: 1.2, 1.3, 6.1
// ---------------------------------------------------------------------------

func TestE2E_CreateProject_CreateTicket_MoveToInProgress(t *testing.T) {
	skipIfNoStack(t)

	user, pass := adminCreds()
	token := authToken(t, user, pass)

	// Create project.
	projectID := createProject(t, token, "E2E Test Project")
	require.NotEmpty(t, projectID)

	// Create ticket and read its ID.
	resp := do(t, "POST", "/api/v1/projects/"+projectID+"/tickets", token, map[string]any{
		"title":    "E2E ticket",
		"priority": "high",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ticketResult struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ticketResult))
	resp.Body.Close()
	require.NotEmpty(t, ticketResult.ID)

	// Move ticket to "In Progress".
	resp2 := do(t, "PATCH", "/api/v1/projects/"+projectID+"/tickets/"+ticketResult.ID+"/state",
		token, map[string]string{"state": "In Progress"})
	defer ignoredBody(resp2)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
}

// ---------------------------------------------------------------------------
// Test: Ticket validation — empty title → 400
// Requirement 1.6, 1.7
// ---------------------------------------------------------------------------

func TestE2E_TicketCreate_EmptyTitle_Returns400(t *testing.T) {
	skipIfNoStack(t)

	user, pass := adminCreds()
	token := authToken(t, user, pass)
	projectID := createProject(t, token, "Validation Test Project")

	resp := do(t, "POST", "/api/v1/projects/"+projectID+"/tickets", token, map[string]any{
		"title": "",
	})
	defer ignoredBody(resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestE2E_TicketCreate_WhitespaceTitle_Returns400(t *testing.T) {
	skipIfNoStack(t)

	user, pass := adminCreds()
	token := authToken(t, user, pass)
	projectID := createProject(t, token, "Whitespace Validation Project")

	resp := do(t, "POST", "/api/v1/projects/"+projectID+"/tickets", token, map[string]any{
		"title": "   ",
	})
	defer ignoredBody(resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Test: Invalid state transition → 400
// Requirement 1.3
// ---------------------------------------------------------------------------

func TestE2E_TicketStateChange_InvalidState_Returns400(t *testing.T) {
	skipIfNoStack(t)

	user, pass := adminCreds()
	token := authToken(t, user, pass)
	projectID := createProject(t, token, "State Validation Project")

	// Create ticket.
	cr := do(t, "POST", "/api/v1/projects/"+projectID+"/tickets", token, map[string]any{
		"title": "state test ticket",
	})
	require.Equal(t, http.StatusCreated, cr.StatusCode)
	var ticketResult struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(cr.Body).Decode(&ticketResult))
	cr.Body.Close()
	require.NotEmpty(t, ticketResult.ID)

	// Attempt to move to a nonexistent state.
	resp := do(t, "PATCH", "/api/v1/projects/"+projectID+"/tickets/"+ticketResult.ID+"/state",
		token, map[string]string{"state": "NonExistentColumn"})
	defer ignoredBody(resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Test: Snapshot read path — GET /projects/:id returns project state
// Requirement 3.3, 3.5
// ---------------------------------------------------------------------------

func TestE2E_ProjectGet_ReturnsSnapshot(t *testing.T) {
	skipIfNoStack(t)

	user, pass := adminCreds()
	token := authToken(t, user, pass)
	projectID := createProject(t, token, "Snapshot Read Test")

	resp := do(t, "GET", "/api/v1/projects/"+projectID, token, nil)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Test: JWT auth — missing token → 401
// Requirement 9.2
// ---------------------------------------------------------------------------

func TestE2E_MissingJWT_Returns401(t *testing.T) {
	skipIfNoStack(t)

	resp := do(t, "GET", "/api/v1/projects", "", nil)
	defer ignoredBody(resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Test: Ticket CRUD lifecycle
// ---------------------------------------------------------------------------

func TestE2E_TicketCRUD_Lifecycle(t *testing.T) {
	skipIfNoStack(t)

	user, pass := adminCreds()
	token := authToken(t, user, pass)
	projectID := createProject(t, token, "CRUD Lifecycle Project")

	// Create
	cr := do(t, "POST", "/api/v1/projects/"+projectID+"/tickets", token, map[string]any{
		"title":    "lifecycle ticket",
		"priority": "low",
	})
	require.Equal(t, http.StatusCreated, cr.StatusCode)
	var ticketResult struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(cr.Body).Decode(&ticketResult))
	cr.Body.Close()
	ticketID := ticketResult.ID
	require.NotEmpty(t, ticketID)

	// Update
	ur := do(t, "PUT", "/api/v1/projects/"+projectID+"/tickets/"+ticketID, token, map[string]any{
		"title":    "updated ticket title",
		"priority": "high",
	})
	defer ignoredBody(ur)
	assert.Equal(t, http.StatusOK, ur.StatusCode)

	// Delete
	dr := do(t, "DELETE", "/api/v1/projects/"+projectID+"/tickets/"+ticketID, token, nil)
	defer ignoredBody(dr)
	assert.Equal(t, http.StatusOK, dr.StatusCode)
}
