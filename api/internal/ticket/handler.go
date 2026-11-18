// Package ticket implements the HTTP handlers for ticket routes.
package ticket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/events"
	"spm-platform/api/internal/gitengine"
	"spm-platform/api/internal/models"
	"spm-platform/api/internal/snapshot"
)

// Handler handles ticket-related HTTP routes.
type Handler struct {
	snapRepo     *snapshot.Repo
	eventsClient *events.Client
	gitEngine    gitengine.BranchCreator
	db           *pgxpool.Pool
}

// NewHandler constructs a ticket Handler.
func NewHandler(snapRepo *snapshot.Repo, eventsClient *events.Client, gitEngine gitengine.BranchCreator, db *pgxpool.Pool) *Handler {
	return &Handler{snapRepo: snapRepo, eventsClient: eventsClient, gitEngine: gitEngine, db: db}
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

func apiError(c *gin.Context, status int, code, message string) {
	c.JSON(status, errorResponse{Error: errorBody{Code: code, Message: message}})
}

// ---------------------------------------------------------------------------
// POST /projects/:id/tickets
// ---------------------------------------------------------------------------

type createRequest struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	AssigneeID  *uuid.UUID `json:"assignee_id"`
	Priority    string     `json:"priority"`
}

type createResponse struct {
	ID uuid.UUID `json:"id"`
}

func (h *Handler) Create(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Ticket title must not be empty")
		return
	}
	if utf8.RuneCountInString(req.Title) > 255 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Ticket title must not exceed 255 characters")
		return
	}
	if utf8.RuneCountInString(req.Description) > 10000 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Description must not exceed 10000 characters")
		return
	}
	priority, ok := normalizePriority(req.Priority)
	if !ok {
		apiError(c, http.StatusBadRequest, "INVALID_PRIORITY", "Priority must be low, medium, high or critical")
		return
	}

	ctx := c.Request.Context()
	state, err := h.loadProjectState(ctx, projectID)
	if err != nil {
		if errors.Is(err, snapshot.ErrNotFound) {
			apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load project state")
		return
	}
	ticketState := req.State
	if ticketState == "" {
		ticketState = "Backlog"
	}
	if !isValidState(ticketState, state.Columns) {
		apiError(c, http.StatusBadRequest, "INVALID_STATE", "State is not a valid column on this board")
		return
	}
	if req.AssigneeID != nil {
		if err := h.validateAssignee(ctx, projectID, *req.AssigneeID); err != nil {
			apiError(c, http.StatusBadRequest, "INVALID_ASSIGNEE", "Assignee is not a member of this project")
			return
		}
	}

	ticketID := models.NewID()
	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]interface{}{
		"id":          ticketID,
		"title":       req.Title,
		"description": req.Description,
		"state":       ticketState,
		"assignee_id": req.AssigneeID,
		"priority":    priority,
		"created_at":  now,
	})
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to marshal event payload")
		return
	}

	if _, err := h.eventsClient.Publish(ctx, models.EventEnvelope{
		EventType: "ticket.created", AggregateID: projectID, Payload: payload, CreatedAt: now,
	}, 0); err != nil {
		handlePublishError(c, err, "ticket.created")
		return
	}
	c.JSON(http.StatusCreated, createResponse{ID: ticketID})
}

// ---------------------------------------------------------------------------
// PUT /projects/:id/tickets/:tid
// ---------------------------------------------------------------------------

type updateRequest struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	AssigneeID  *uuid.UUID `json:"assignee_id"`
	Priority    string     `json:"priority"`
}

type updateResponse struct {
	ID uuid.UUID `json:"id"`
}

func (h *Handler) Update(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	ticketID, ok := parseTicketID(c)
	if !ok {
		return
	}
	var req updateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Ticket title must not be empty")
		return
	}
	if utf8.RuneCountInString(req.Title) > 255 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Ticket title must not exceed 255 characters")
		return
	}
	if utf8.RuneCountInString(req.Description) > 10000 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Description must not exceed 10000 characters")
		return
	}
	priority, ok := normalizePriority(req.Priority)
	if !ok {
		apiError(c, http.StatusBadRequest, "INVALID_PRIORITY", "Priority must be low, medium, high or critical")
		return
	}

	ctx := c.Request.Context()
	if err := h.ensureTicket(ctx, projectID, ticketID); err != nil {
		h.respondTicketLookupError(c, err)
		return
	}
	if req.AssigneeID != nil {
		if err := h.validateAssignee(ctx, projectID, *req.AssigneeID); err != nil {
			apiError(c, http.StatusBadRequest, "INVALID_ASSIGNEE", "Assignee is not a member of this project")
			return
		}
	}

	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]interface{}{
		"id":          ticketID,
		"title":       req.Title,
		"description": req.Description,
		"assignee_id": req.AssigneeID,
		"priority":    priority,
		"updated_at":  now,
	})
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to marshal event payload")
		return
	}
	if _, err := h.eventsClient.Publish(ctx, models.EventEnvelope{
		EventType: "ticket.updated", AggregateID: projectID, Payload: payload, CreatedAt: now,
	}, 0); err != nil {
		handlePublishError(c, err, "ticket.updated")
		return
	}
	c.JSON(http.StatusOK, updateResponse{ID: ticketID})
}

// ---------------------------------------------------------------------------
// PATCH /projects/:id/tickets/:tid/state
// ---------------------------------------------------------------------------

type changeStateRequest struct {
	State string `json:"state"`
}

type changeStateResponse struct {
	ID uuid.UUID `json:"id"`
}

func (h *Handler) ChangeState(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	ticketID, ok := parseTicketID(c)
	if !ok {
		return
	}
	var req changeStateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.State = strings.TrimSpace(req.State)
	if req.State == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Target state must not be empty")
		return
	}

	ctx := c.Request.Context()
	state, current, err := h.waitForTicket(ctx, projectID, ticketID)
	if err != nil {
		h.respondTicketLookupError(c, err)
		return
	}
	if !isValidState(req.State, state.Columns) {
		apiError(c, http.StatusBadRequest, "INVALID_STATE", "Target state is not a valid column on this board")
		return
	}

	now := time.Now().UTC()
	if current.State == req.State {
		c.JSON(http.StatusOK, changeStateResponse{ID: ticketID})
		return
	}

	payload, err := json.Marshal(map[string]interface{}{
		"id": ticketID, "new_state": req.State, "updated_at": now,
	})
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to marshal event payload")
		return
	}
	if _, err := h.eventsClient.Publish(ctx, models.EventEnvelope{
		EventType: "ticket.state_changed", AggregateID: projectID, Payload: payload, CreatedAt: now,
	}, 0); err != nil {
		handlePublishError(c, err, "ticket.state_changed")
		return
	}

	if req.State == "In Progress" {
		assigneeID := current.AssigneeID
		branchName := current.BranchName
		go h.triggerBranchCreation(projectID, ticketID, branchName, assigneeID)
	}
	c.JSON(http.StatusOK, changeStateResponse{ID: ticketID})
}

// ---------------------------------------------------------------------------
// DELETE /projects/:id/tickets/:tid
// ---------------------------------------------------------------------------

type deleteResponse struct {
	ID uuid.UUID `json:"id"`
}

func (h *Handler) Delete(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	ticketID, ok := parseTicketID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if err := h.ensureTicket(ctx, projectID, ticketID); err != nil {
		h.respondTicketLookupError(c, err)
		return
	}

	now := time.Now().UTC()
	payload, _ := json.Marshal(map[string]interface{}{"id": ticketID})
	if _, err := h.eventsClient.Publish(ctx, models.EventEnvelope{
		EventType: "ticket.deleted", AggregateID: projectID, Payload: payload, CreatedAt: now,
	}, 0); err != nil {
		handlePublishError(c, err, "ticket.deleted")
		return
	}
	c.JSON(http.StatusOK, deleteResponse{ID: ticketID})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func parseProjectID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_ID", "Project ID is not a valid UUID")
		return uuid.UUID{}, false
	}
	return id, true
}

func parseTicketID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("tid"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_ID", "Ticket ID is not a valid UUID")
		return uuid.UUID{}, false
	}
	return id, true
}

func (h *Handler) loadProjectState(ctx context.Context, projectID uuid.UUID) (models.ProjectState, error) {
	state, lastSeq, err := h.loadSnapshotOrEmpty(ctx, projectID)
	if err != nil {
		return models.ProjectState{}, err
	}
	deltaEvents, err := h.snapRepo.GetDeltaEvents(ctx, projectID, lastSeq)
	if err != nil {
		return models.ProjectState{}, err
	}
	for _, evt := range deltaEvents {
		next, applyErr := snapshot.Apply(state, evt)
		if applyErr != nil {
			if errors.Is(applyErr, snapshot.ErrUnknownEventType) {
				continue
			}
			return models.ProjectState{}, applyErr
		}
		state = next
	}
	return state, nil
}

func (h *Handler) loadSnapshotOrEmpty(ctx context.Context, projectID uuid.UUID) (models.ProjectState, int64, error) {
	snap, err := h.snapRepo.Get(ctx, projectID)
	if err == nil {
		var state models.ProjectState
		if err := json.Unmarshal(snap.Data, &state); err != nil {
			return models.ProjectState{}, 0, err
		}
		if state.Tickets == nil {
			state.Tickets = []models.Ticket{}
		}
		if len(state.Columns) == 0 {
			state.Columns = models.DefaultBoardColumns()
		}
		return state, snap.LastSequenceID, nil
	}
	if !errors.Is(err, snapshot.ErrNotFound) {
		return models.ProjectState{}, 0, err
	}

	meta, metaErr := h.snapRepo.GetProjectMeta(ctx, projectID)
	if metaErr != nil {
		return models.ProjectState{}, 0, metaErr
	}
	state := models.ProjectState{
		ProjectID:     projectID,
		Name:          meta.Name,
		Description:   meta.Description,
		OwnerID:       meta.OwnerID,
		RepoName:      meta.RepoName,
		DefaultBranch: meta.DefaultBranch,
		AppName:       meta.AppName,
		Columns:       models.DefaultBoardColumns(),
		Tickets:       []models.Ticket{},
		Members:       []models.ProjectMember{},
		Pipelines:     []models.PipelineStatus{},
		Deployments:   []models.DeploymentStatus{},
		Notifications: []models.Notification{},
		UpdatedAt:     meta.CreatedAt,
	}
	return state, 0, nil
}

// waitForTicket waits briefly for the asynchronous projection to include a
// ticket that was just created. Event-sourced writes return before the worker
// has applied them; without this read-after-write barrier an immediate update
// can observe a stale snapshot and incorrectly return 404.
func (h *Handler) waitForTicket(ctx context.Context, projectID, ticketID uuid.UUID) (models.ProjectState, *models.Ticket, error) {
	const attempts = 40 // up to ~2s
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		state, err := h.loadProjectState(ctx, projectID)
		if err != nil {
			lastErr = err
			if errors.Is(err, snapshot.ErrNotFound) {
				return models.ProjectState{}, nil, err
			}
		} else {
			for i := range state.Tickets {
				if state.Tickets[i].ID == ticketID {
					return state, &state.Tickets[i], nil
				}
			}
			lastErr = snapshot.ErrNotFound
		}

		select {
		case <-ctx.Done():
			return models.ProjectState{}, nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return models.ProjectState{}, nil, lastErr
}

func (h *Handler) ensureTicket(ctx context.Context, projectID, ticketID uuid.UUID) error {
	_, _, err := h.waitForTicket(ctx, projectID, ticketID)
	return err
}

func (h *Handler) respondTicketLookupError(c *gin.Context, err error) {
	if errors.Is(err, snapshot.ErrNotFound) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Project or ticket not found")
		return
	}
	apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load project state")
}

func (h *Handler) validateAssignee(ctx context.Context, projectID, userID uuid.UUID) error {
	var exists bool
	err := h.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM project_members WHERE project_id = $1 AND user_id = $2)`,
		projectID, userID,
	).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return pgx.ErrNoRows
	}
	return nil
}

func normalizePriority(p string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "", models.PriorityMedium:
		return models.PriorityMedium, true
	case models.PriorityLow:
		return models.PriorityLow, true
	case models.PriorityHigh:
		return models.PriorityHigh, true
	case models.PriorityCritical:
		return models.PriorityCritical, true
	default:
		return "", false
	}
}

func isValidState(state string, columns []models.BoardColumn) bool {
	for _, col := range columns {
		if col.Name == state {
			return true
		}
	}
	return false
}

// triggerBranchCreation runs asynchronously after the state-change response.
// It uses a background context because the HTTP request may already be done.
func (h *Handler) triggerBranchCreation(projectID, ticketID uuid.UUID, existingBranch string, assigneeID *uuid.UUID) {
	if existingBranch != "" {
		h.publishBranchEvent(projectID, "git.branch.already_exists", map[string]interface{}{
			"ticket_id": ticketID, "branch_name": existingBranch,
		})
		return
	}
	if h.gitEngine == nil {
		return
	}

	meta, err := h.snapRepo.GetProjectMeta(context.Background(), projectID)
	if err != nil || meta.RepoName == "" {
		h.publishBranchEvent(projectID, "git.branch.creation_failed", map[string]interface{}{
			"ticket_id": ticketID, "branch_name": featureBranch(ticketID),
			"reason": "project repository metadata unavailable", "assignee_id": assigneeID,
		})
		return
	}
	branchName := featureBranch(ticketID)
	sourceBranch := meta.DefaultBranch
	if sourceBranch == "" {
		sourceBranch = "main"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	err = h.gitEngine.CreateBranch(ctx, meta.RepoName, branchName, sourceBranch)

	switch {
	case err == nil:
		h.publishBranchEvent(projectID, "git.branch.created", map[string]interface{}{
			"ticket_id": ticketID, "branch_name": branchName, "updated_at": time.Now().UTC(),
		})
	case errors.Is(err, gitengine.ErrBranchExists):
		h.publishBranchEvent(projectID, "git.branch.already_exists", map[string]interface{}{
			"ticket_id": ticketID, "branch_name": branchName, "updated_at": time.Now().UTC(),
		})
	case errors.Is(err, gitengine.ErrSourceBranchNotFound):
		h.publishBranchFailure(projectID, ticketID, branchName, assigneeID, "source_branch_not_found")
	default:
		h.publishBranchFailure(projectID, ticketID, branchName, assigneeID, err.Error())
	}
}

func (h *Handler) publishBranchFailure(projectID, ticketID uuid.UUID, branchName string, assigneeID *uuid.UUID, reason string) {
	h.publishBranchEvent(projectID, "git.branch.creation_failed", map[string]interface{}{
		"ticket_id": ticketID, "branch_name": branchName, "reason": reason, "assignee_id": assigneeID,
	})
	h.publishBranchEvent(projectID, "notification.branch_creation_failed", map[string]interface{}{
		"ticket_id": ticketID, "assignee_id": assigneeID, "reason": reason,
	})
}

func (h *Handler) publishBranchEvent(projectID uuid.UUID, eventType string, payload map[string]interface{}) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = h.eventsClient.Publish(context.Background(), models.EventEnvelope{
		EventType: eventType, AggregateID: projectID, Payload: body, CreatedAt: time.Now().UTC(),
	}, 0)
}

func featureBranch(ticketID uuid.UUID) string {
	return fmt.Sprintf("feature/%s", ticketID.String())
}

func handlePublishError(c *gin.Context, err error, eventType string) {
	if errors.Is(err, events.ErrTimeout) {
		apiError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Event store is unavailable; please retry")
		return
	}
	if errors.Is(err, events.ErrConflict) {
		apiError(c, http.StatusConflict, "SEQUENCE_CONFLICT", "Concurrent write detected; please retry")
		return
	}
	apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to publish "+eventType+" event")
}
