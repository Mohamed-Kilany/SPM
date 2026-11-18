package snapshot

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"spm-platform/api/internal/models"
)

func envelope(t *testing.T, projectID uuid.UUID, eventType string, payload any) models.EventEnvelope {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return models.EventEnvelope{
		EventType:   eventType,
		AggregateID: projectID,
		Payload:     raw,
		CreatedAt:   time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC),
	}
}

func projectCreated(t *testing.T, projectID, ownerID uuid.UUID) models.EventEnvelope {
	t.Helper()
	return envelope(t, projectID, "project.created", map[string]any{
		"project_id":     projectID,
		"name":           "Atlas",
		"description":    "Delivery project",
		"owner_id":       ownerID,
		"repo_name":      "atlas",
		"default_branch": "main",
		"app_name":       "atlas",
		"members":        []models.ProjectMember{{UserID: ownerID, Role: "admin"}},
		"columns":        models.DefaultBoardColumns(),
		"created_at":     "2026-10-30T09:00:00Z",
	})
}

func createdProjectState(t *testing.T) (models.ProjectState, uuid.UUID, uuid.UUID) {
	t.Helper()
	projectID := models.NewID()
	ownerID := models.NewID()
	state, err := Apply(models.ProjectState{}, projectCreated(t, projectID, ownerID))
	require.NoError(t, err)
	return state, projectID, ownerID
}

func TestApplyProjectCreated_BootstrapsReadModel(t *testing.T) {
	state, projectID, ownerID := createdProjectState(t)

	assert.Equal(t, projectID, state.ProjectID)
	assert.Equal(t, ownerID, state.OwnerID)
	assert.Equal(t, "Atlas", state.Name)
	assert.Equal(t, "main", state.DefaultBranch)
	assert.Len(t, state.Columns, 4)
	assert.Len(t, state.Members, 1)
	assert.NotNil(t, state.Tickets)
	assert.NotNil(t, state.Pipelines)
	assert.NotNil(t, state.Deployments)
	assert.NotNil(t, state.Notifications)
	assert.False(t, state.UpdatedAt.IsZero())
}

func TestApply_DoesNotMutateInput(t *testing.T) {
	state, projectID, _ := createdProjectState(t)
	original := state

	next, err := Apply(state, envelope(t, projectID, "ticket.created", map[string]any{
		"id":          models.NewID(),
		"title":       "New work",
		"description": "",
		"state":       "Backlog",
		"priority":    "",
		"created_at":  "2026-10-30T10:05:00Z",
	}))
	require.NoError(t, err)

	assert.Empty(t, original.Tickets, "Apply must not mutate its input state")
	assert.Len(t, next.Tickets, 1)
	assert.Equal(t, models.PriorityMedium, next.Tickets[0].Priority)
}

func TestApplyTicketLifecycleAndPipelineMapping(t *testing.T) {
	state, projectID, _ := createdProjectState(t)
	ticketID := models.NewID()

	state, err := Apply(state, envelope(t, projectID, "ticket.created", map[string]any{
		"id":          ticketID,
		"title":       "Build board",
		"description": "initial",
		"state":       "Backlog",
		"priority":    "high",
		"created_at":  "2026-10-30T10:01:00Z",
	}))
	require.NoError(t, err)

	state, err = Apply(state, envelope(t, projectID, "ticket.updated", map[string]any{
		"id":          ticketID,
		"title":       "Build live board",
		"description": "updated",
		"priority":    "critical",
		"updated_at":  "2026-10-30T10:02:00Z",
	}))
	require.NoError(t, err)
	require.Len(t, state.Tickets, 1)
	assert.Equal(t, "Build live board", state.Tickets[0].Title)
	assert.Equal(t, "critical", state.Tickets[0].Priority)

	state, err = Apply(state, envelope(t, projectID, "ticket.state_changed", map[string]any{
		"id":         ticketID,
		"new_state":  "In Progress",
		"updated_at": "2026-10-30T10:03:00Z",
	}))
	require.NoError(t, err)
	assert.Equal(t, "In Progress", state.Tickets[0].State)

	state, err = Apply(state, envelope(t, projectID, "git.branch.created", map[string]any{
		"ticket_id":   ticketID,
		"branch_name": "feature/" + ticketID.String(),
		"updated_at":  "2026-10-30T10:04:00Z",
	}))
	require.NoError(t, err)
	assert.Equal(t, "feature/"+ticketID.String(), state.Tickets[0].BranchName)

	state, err = Apply(state, envelope(t, projectID, "pipeline.status_changed", map[string]any{
		"branch":     "feature/" + ticketID.String(),
		"commit_sha": "abcdef1",
		"status":     "success",
		"ticket_id":  uuid.Nil,
		"updated_at": "2026-10-30T10:05:00Z",
	}))
	require.NoError(t, err)
	require.Len(t, state.Pipelines, 1)
	assert.Equal(t, "success", state.Pipelines[0].Status)
	assert.Equal(t, "success", state.Tickets[0].PipelineStatus)

	state, err = Apply(state, envelope(t, projectID, "pipeline.status_changed", map[string]any{
		"branch":     "feature/" + ticketID.String(),
		"commit_sha": "abcdef2",
		"status":     "failure",
		"ticket_id":  ticketID,
		"updated_at": "2026-10-30T10:06:00Z",
	}))
	require.NoError(t, err)
	assert.Len(t, state.Pipelines, 1, "pipeline status is upserted by branch")
	assert.Equal(t, "failure", state.Tickets[0].PipelineStatus)

	state, err = Apply(state, envelope(t, projectID, "ticket.deleted", map[string]any{
		"id": ticketID,
	}))
	require.NoError(t, err)
	assert.Empty(t, state.Tickets)
}

func TestApplyDeploymentAndNotifications(t *testing.T) {
	state, projectID, _ := createdProjectState(t)

	state, err := Apply(state, envelope(t, projectID, "deployment.health_degraded", map[string]any{
		"app_name":      "atlas",
		"sync_status":   "Synced",
		"health_status": "Degraded",
		"commit_sha":    "abc1234",
		"updated_at":    "2026-10-30T10:10:00Z",
	}))
	require.NoError(t, err)
	require.Len(t, state.Deployments, 1)
	assert.Equal(t, "Degraded", state.Deployments[0].HealthStatus)
	require.Len(t, state.Notifications, 1)
	assert.Equal(t, "deployment.health_degraded", state.Notifications[0].Type)
	assert.NotEqual(t, uuid.Nil, state.Notifications[0].ID)

	state, err = Apply(state, envelope(t, projectID, "git.branch.creation_failed", map[string]any{
		"ticket_id":   models.NewID(),
		"branch_name": "feature/broken",
		"reason":      "Gitea unavailable",
	}))
	require.NoError(t, err)
	require.Len(t, state.Notifications, 2)
	assert.Contains(t, state.Notifications[1].Message, "Gitea unavailable")
}

func TestApplyCapsNotificationHistory(t *testing.T) {
	state, projectID, _ := createdProjectState(t)
	for i := 0; i < 105; i++ {
		var err error
		state, err = Apply(state, envelope(t, projectID, "git.repo.provision_failed", map[string]any{
			"reason": "attempt",
		}))
		require.NoError(t, err)
	}
	assert.Len(t, state.Notifications, 100)
	assert.True(t, state.GitProvisioningFailed)
}

func TestApplyUnknownEventReturnsOriginalState(t *testing.T) {
	state, projectID, _ := createdProjectState(t)
	next, err := Apply(state, envelope(t, projectID, "unknown.event", map[string]any{}))
	assert.ErrorIs(t, err, ErrUnknownEventType)
	assert.Equal(t, state, next)
}

func TestApplyMalformedPayloadIsRejected(t *testing.T) {
	state, projectID, _ := createdProjectState(t)
	next, err := Apply(state, models.EventEnvelope{
		EventType:   "ticket.created",
		AggregateID: projectID,
		Payload:     json.RawMessage(`{"id":`),
	})
	assert.Error(t, err)
	assert.Equal(t, state, next)
}
