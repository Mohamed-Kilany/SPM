package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/ai"
	"spm-platform/api/internal/audit"
	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/events"
	"spm-platform/api/internal/gitengine"
	"spm-platform/api/internal/models"
)

// Handler serves the normalized workspace API.
type Handler struct {
	store  *Store
	events *events.Client
	ai     *ai.Client
	git    *gitengine.Client
	audit  *audit.Logger
}

// NewHandler creates a workspace handler.
func NewHandler(db *pgxpool.Pool, eventsClient *events.Client) *Handler {
	return &Handler{store: NewStore(db), events: eventsClient}
}

// SetAIClient enables AI-assisted search, Q&A and insights for this handler.
func (h *Handler) SetAIClient(client *ai.Client) {
	h.ai = client
}

// SetGitClient enables real branch, pull request and merge actions in Gitea.
func (h *Handler) SetGitClient(client *gitengine.Client) {
	h.git = client
}

// SetAuditLogger attaches the audit log to security-relevant workspace actions.
func (h *Handler) SetAuditLogger(logger *audit.Logger) {
	h.audit = logger
}

// recordAudit writes one audit event for the supplied project when a logger is
// configured. Audit failures never fail the business request.
func (h *Handler) recordAudit(c *gin.Context, projectID uuid.UUID, entry audit.Entry) {
	if h.audit == nil {
		return
	}
	if entry.ActorID == "" {
		if claims, ok := auth.ClaimsFrom(c); ok && claims != nil {
			entry.ActorID = claims.CanonicalUserID()
			entry.ActorRole = claims.Role
		}
	}
	if projectID != uuid.Nil {
		entry.ProjectID = projectID.String()
	}
	h.audit.RecordRequest(c, entry)
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

func parseProjectID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_ID", "Project ID is not a valid UUID")
		return uuid.UUID{}, false
	}
	return id, true
}

func parseParamID(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_ID", strings.Title(name)+" is not a valid UUID")
		return uuid.UUID{}, false
	}
	return id, true
}

func actorID(c *gin.Context) *uuid.UUID {
	claims, ok := auth.ClaimsFrom(c)
	if !ok {
		return nil
	}
	id, err := uuid.Parse(claims.CanonicalUserID())
	if err != nil {
		return nil
	}
	return &id
}

func (h *Handler) recordActivity(ctx context.Context, projectID uuid.UUID, actor *uuid.UUID, entityType string, entityID *uuid.UUID, action, summary string, metadata map[string]interface{}) {
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	body, _ := json.Marshal(metadata)
	var id uuid.UUID
	err := h.store.DB().QueryRow(ctx, `
		INSERT INTO activity_events (project_id, actor_id, entity_type, entity_id, action, summary, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`, projectID, actor, entityType, entityID, action, summary, body).Scan(&id)
	if err != nil {
		return
	}
	if h.events == nil {
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"entity_type": entityType,
		"entity_id":   entityID,
		"action":      action,
		"summary":     summary,
	})
	_, _ = h.events.Publish(ctx, models.EventEnvelope{
		EventType:   "workspace.changed",
		AggregateID: projectID,
		Payload:     payload,
		CreatedAt:   time.Now().UTC(),
	}, 0)
}

// GetWorkspace handles GET /projects/:id/workspace.
func (h *Handler) GetWorkspace(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	w, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load workspace")
		return
	}
	c.JSON(http.StatusOK, w)
}

// GetActivity handles GET /projects/:id/activity.
func (h *Handler) GetActivity(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	w, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load activity")
		return
	}
	c.JSON(http.StatusOK, w.Activity)
}

// GetTraceability handles GET /projects/:id/traceability.
func (h *Handler) GetTraceability(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	w, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load traceability")
		return
	}
	c.JSON(http.StatusOK, buildTraceability(w))
}

// ---------------------------------------------------------------------------
// PRDs
// ---------------------------------------------------------------------------

type prdRequest struct {
	Title           string     `json:"title"`
	Version         string     `json:"version"`
	Status          string     `json:"status"`
	Summary         string     `json:"summary"`
	Content         string     `json:"content"`
	OwnerID         *uuid.UUID `json:"owner_id"`
	ExpectedVersion *int64     `json:"expected_version"`
}

func (h *Handler) CreatePRD(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req prdRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || len(req.Title) > 255 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "PRD title is required and must be at most 255 characters")
		return
	}
	if req.Version == "" {
		req.Version = "1.0"
	}
	if req.Status == "" {
		req.Status = "draft"
	}
	if !validPRDStatus(req.Status) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid PRD status")
		return
	}
	var id uuid.UUID
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO prds (project_id, title, version, status, summary, content, owner_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, revision`,
		projectID, req.Title, req.Version, req.Status, req.Summary, req.Content, req.OwnerID).Scan(&id, &revision)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create PRD")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "prd", &id, "created", "Created PRD "+req.Title, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusCreated, gin.H{"id": id, "revision": revision})
}

func (h *Handler) UpdatePRD(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	prdID, ok := parseParamID(c, "prdId")
	if !ok {
		return
	}
	var req prdRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "PRD title is required")
		return
	}
	if req.Status == "" {
		req.Status = "draft"
	}
	if !validPRDStatus(req.Status) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid PRD status")
		return
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		UPDATE prds SET title=$3, version=$4, status=$5, summary=$6, content=$7, owner_id=$8, updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(9)+`
		RETURNING revision`,
		prdID, projectID, req.Title, req.Version, req.Status, req.Summary, req.Content, req.OwnerID,
		revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "PRD", "prds", prdID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update PRD")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "prd", &prdID, "updated", "Updated PRD "+req.Title, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": prdID, "revision": revision})
}

func (h *Handler) DeletePRD(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	prdID, ok := parseParamID(c, "prdId")
	if !ok {
		return
	}
	expected, ok := revisionFromRequest(c, nil)
	if !ok {
		return
	}
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(),
		`DELETE FROM prds WHERE id=$1 AND project_id=$2`+revisionClause(3)+` RETURNING revision`,
		prdID, projectID, revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "PRD", "prds", prdID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete PRD")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "prd", &prdID, "deleted", "Deleted PRD", nil)
	c.JSON(http.StatusOK, gin.H{"id": prdID, "revision": revision, "deleted": true})
}

func validPRDStatus(s string) bool {
	switch s {
	case "draft", "in_review", "approved", "superseded":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Epics
// ---------------------------------------------------------------------------

type epicRequest struct {
	PRDID           *uuid.UUID `json:"prd_id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	Priority        string     `json:"priority"`
	OwnerID         *uuid.UUID `json:"owner_id"`
	TargetReleaseID *uuid.UUID `json:"target_release_id"`
	StartDate       *string    `json:"start_date"`
	DueDate         *string    `json:"due_date"`
	ExpectedVersion *int64     `json:"expected_version"`
}

func (h *Handler) CreateEpic(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req epicRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || len(req.Title) > 255 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Epic title is required and must be at most 255 characters")
		return
	}
	if req.Status == "" {
		req.Status = "draft"
	}
	if !validEpicStatus(req.Status) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid epic status")
		return
	}
	if req.Priority == "" {
		req.Priority = PriorityMedium
	}
	if !validPriority(req.Priority) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid priority")
		return
	}
	start, err := parseTimePtr(req.StartDate)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	due, err := parseTimePtr(req.DueDate)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	var id uuid.UUID
	var revision int64
	err = h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO epics (project_id, prd_id, title, description, status, priority, owner_id, target_release_id, start_date, due_date)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id, revision`,
		projectID, req.PRDID, req.Title, req.Description, req.Status, req.Priority, req.OwnerID, req.TargetReleaseID, start, due).Scan(&id, &revision)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create epic")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "epic", &id, "created", "Created epic "+req.Title, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusCreated, gin.H{"id": id, "revision": revision})
}

func (h *Handler) UpdateEpic(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	epicID, ok := parseParamID(c, "epicId")
	if !ok {
		return
	}
	var req epicRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if req.Status == "" {
		req.Status = "draft"
	}
	if !validEpicStatus(req.Status) || !validPriority(req.Priority) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid epic status or priority")
		return
	}
	start, _ := parseTimePtr(req.StartDate)
	due, _ := parseTimePtr(req.DueDate)
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		UPDATE epics SET prd_id=$3, title=$4, description=$5, status=$6, priority=$7, owner_id=$8,
		target_release_id=$9, start_date=$10, due_date=$11, updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(12)+`
		RETURNING revision`,
		epicID, projectID, req.PRDID, req.Title, req.Description, req.Status, req.Priority, req.OwnerID,
		req.TargetReleaseID, start, due, revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "epic", "epics", epicID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update epic")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "epic", &epicID, "updated", "Updated epic "+req.Title, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": epicID, "revision": revision})
}

func (h *Handler) DeleteEpic(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	epicID, ok := parseParamID(c, "epicId")
	if !ok {
		return
	}
	expected, ok := revisionFromRequest(c, nil)
	if !ok {
		return
	}
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(),
		`DELETE FROM epics WHERE id=$1 AND project_id=$2`+revisionClause(3)+` RETURNING revision`,
		epicID, projectID, revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "epic", "epics", epicID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete epic")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "epic", &epicID, "deleted", "Deleted epic", nil)
	c.JSON(http.StatusOK, gin.H{"id": epicID, "revision": revision, "deleted": true})
}

func validEpicStatus(s string) bool {
	switch s {
	case "draft", "planned", "in_progress", "done", "cancelled":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Features
// ---------------------------------------------------------------------------

type featureRequest struct {
	EpicID             uuid.UUID  `json:"epic_id"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	AcceptanceCriteria string     `json:"acceptance_criteria"`
	Status             string     `json:"status"`
	Priority           string     `json:"priority"`
	OwnerID            *uuid.UUID `json:"owner_id"`
	EstimatePoints     *float64   `json:"estimate_points"`
	TargetReleaseID    *uuid.UUID `json:"target_release_id"`
	ExpectedVersion    *int64     `json:"expected_version"`
}

func (h *Handler) CreateFeature(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req featureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || len(req.Title) > 255 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Feature title is required and must be at most 255 characters")
		return
	}
	if req.EpicID == uuid.Nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Feature must belong to an epic")
		return
	}
	if req.Status == "" {
		req.Status = "draft"
	}
	if !validFeatureStatus(req.Status) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid feature status")
		return
	}
	if req.Priority == "" {
		req.Priority = PriorityMedium
	}
	if !validPriority(req.Priority) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid priority")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "epics", projectID, req.EpicID); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Epic does not belong to this project")
		return
	}
	var id uuid.UUID
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO features (project_id, epic_id, title, description, acceptance_criteria, status, priority, owner_id, estimate_points, target_release_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id, revision`,
		projectID, req.EpicID, req.Title, req.Description, req.AcceptanceCriteria, req.Status, req.Priority, req.OwnerID, req.EstimatePoints, req.TargetReleaseID).Scan(&id, &revision)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create feature")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "feature", &id, "created", "Created feature "+req.Title, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusCreated, gin.H{"id": id, "revision": revision})
}

func (h *Handler) UpdateFeature(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	featureID, ok := parseParamID(c, "featureId")
	if !ok {
		return
	}
	var req featureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if req.Status == "" {
		req.Status = "draft"
	}
	if !validFeatureStatus(req.Status) || !validPriority(req.Priority) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid feature status or priority")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "epics", projectID, req.EpicID); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Epic does not belong to this project")
		return
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		UPDATE features SET epic_id=$3, title=$4, description=$5, acceptance_criteria=$6, status=$7,
		priority=$8, owner_id=$9, estimate_points=$10, target_release_id=$11, updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(12)+`
		RETURNING revision`,
		featureID, projectID, req.EpicID, req.Title, req.Description, req.AcceptanceCriteria,
		req.Status, req.Priority, req.OwnerID, req.EstimatePoints, req.TargetReleaseID,
		revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "feature", "features", featureID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update feature")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "feature", &featureID, "updated", "Updated feature "+req.Title, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": featureID, "revision": revision})
}

func (h *Handler) DeleteFeature(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	featureID, ok := parseParamID(c, "featureId")
	if !ok {
		return
	}
	expected, ok := revisionFromRequest(c, nil)
	if !ok {
		return
	}
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(),
		`DELETE FROM features WHERE id=$1 AND project_id=$2`+revisionClause(3)+` RETURNING revision`,
		featureID, projectID, revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "feature", "features", featureID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete feature")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "feature", &featureID, "deleted", "Deleted feature", nil)
	c.JSON(http.StatusOK, gin.H{"id": featureID, "revision": revision, "deleted": true})
}

func validFeatureStatus(s string) bool {
	switch s {
	case "draft", "ready", "in_progress", "in_review", "in_qa", "done", "released", "cancelled":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Work items
// ---------------------------------------------------------------------------

type workItemRequest struct {
	FeatureID          *uuid.UUID `json:"feature_id"`
	ParentID           *uuid.UUID `json:"parent_id"`
	Type               string     `json:"type"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	AcceptanceCriteria string     `json:"acceptance_criteria"`
	Status             string     `json:"status"`
	Priority           string     `json:"priority"`
	Severity           *string    `json:"severity"`
	AssigneeID         *uuid.UUID `json:"assignee_id"`
	ReporterID         *uuid.UUID `json:"reporter_id"`
	EstimatePoints     *float64   `json:"estimate_points"`
	SprintID           *uuid.UUID `json:"sprint_id"`
	ReleaseID          *uuid.UUID `json:"release_id"`
	EnvironmentID      *uuid.UUID `json:"environment_id"`
	DueDate            *string    `json:"due_date"`
	ReproductionSteps  string     `json:"reproduction_steps"`
	ExpectedResult     string     `json:"expected_result"`
	ActualResult       string     `json:"actual_result"`
	ExpectedVersion    *int64     `json:"expected_version"`
}

func (h *Handler) CreateWorkItem(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req workItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || len(req.Title) > 255 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Work item title is required and must be at most 255 characters")
		return
	}
	if req.Type == "" {
		req.Type = TypeTask
	}
	if !validWorkItemType(req.Type) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid work item type")
		return
	}
	if auth.ProjectRoleFrom(c) == auth.ProjectRoleTester && req.Type != TypeBug {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Testers can create defects and change status; they cannot create other work items")
		return
	}
	if req.Status == "" {
		if req.Type == TypeBug {
			req.Status = StatusNew
		} else {
			req.Status = StatusBacklog
		}
	}
	if !validWorkItemStatus(req.Type, req.Status) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid status for this work item type")
		return
	}
	if req.Priority == "" {
		req.Priority = PriorityMedium
	}
	if !validPriority(req.Priority) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid priority")
		return
	}
	due, err := parseTimePtr(req.DueDate)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	if err := h.validateWorkItem(c.Request.Context(), projectID, uuid.Nil, &req); err != nil {
		apiError(c, http.StatusUnprocessableEntity, "HIERARCHY_ERROR", err.Error())
		return
	}
	var id uuid.UUID
	var number int64
	var revision int64
	err = h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO work_items (project_id, feature_id, parent_id, type, title, description, acceptance_criteria,
		status, priority, severity, assignee_id, reporter_id, estimate_points, sprint_id, release_id, environment_id,
		due_date, reproduction_steps, expected_result, actual_result)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		RETURNING id, item_number, revision`,
		projectID, req.FeatureID, req.ParentID, req.Type, req.Title, req.Description, req.AcceptanceCriteria,
		req.Status, req.Priority, req.Severity, req.AssigneeID, req.ReporterID, req.EstimatePoints,
		req.SprintID, req.ReleaseID, req.EnvironmentID, due, req.ReproductionSteps, req.ExpectedResult,
		req.ActualResult).Scan(&id, &number, &revision)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create work item")
		return
	}
	var prefix string
	_ = h.store.DB().QueryRow(c.Request.Context(), `SELECT key_prefix FROM projects WHERE id=$1`, projectID).Scan(&prefix)
	key := prefix + "-" + strconv.FormatInt(number, 10)
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "work_item", &id, "created", "Created "+req.Type+" "+key+": "+req.Title, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusCreated, gin.H{"id": id, "key": key, "item_number": number, "revision": revision})
}

func (h *Handler) UpdateWorkItem(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	if auth.ProjectRoleFrom(c) == auth.ProjectRoleTester {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Testers can change work item status but cannot edit work item details")
		return
	}
	itemID, ok := parseParamID(c, "itemId")
	if !ok {
		return
	}
	var req workItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if req.Type == "" {
		req.Type = TypeTask
	}
	if !validWorkItemType(req.Type) || !validWorkItemStatus(req.Type, req.Status) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid work item type or status")
		return
	}
	if err := h.validateWorkItem(c.Request.Context(), projectID, itemID, &req); err != nil {
		apiError(c, http.StatusUnprocessableEntity, "HIERARCHY_ERROR", err.Error())
		return
	}
	due, _ := parseTimePtr(req.DueDate)
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		UPDATE work_items SET feature_id=$3, parent_id=$4, type=$5, title=$6, description=$7,
		acceptance_criteria=$8, status=$9, priority=$10, severity=$11, assignee_id=$12, reporter_id=$13,
		estimate_points=$14, sprint_id=$15, release_id=$16, environment_id=$17, due_date=$18,
		reproduction_steps=$19, expected_result=$20, actual_result=$21,
		closed_at = CASE WHEN $9::varchar IN ('done','closed','verified','cancelled') THEN COALESCE(closed_at, NOW()) ELSE NULL END,
		updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(22)+`
		RETURNING revision`,
		itemID, projectID, req.FeatureID, req.ParentID, req.Type, req.Title, req.Description,
		req.AcceptanceCriteria, req.Status, req.Priority, req.Severity, req.AssigneeID, req.ReporterID,
		req.EstimatePoints, req.SprintID, req.ReleaseID, req.EnvironmentID, due, req.ReproductionSteps,
		req.ExpectedResult, req.ActualResult, revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "work item", "work_items", itemID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update work item")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "work_item", &itemID, "updated", "Updated work item "+req.Title, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": itemID, "revision": revision})
}

type statusRequest struct {
	Status          string `json:"status"`
	ExpectedVersion *int64 `json:"expected_version"`
}

func (h *Handler) ChangeWorkItemStatus(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	itemID, ok := parseParamID(c, "itemId")
	if !ok {
		return
	}
	var req statusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	var itemType, currentStatus string
	err := h.store.DB().QueryRow(c.Request.Context(), `SELECT type, status FROM work_items WHERE id=$1 AND project_id=$2`, itemID, projectID).Scan(&itemType, &currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Work item not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load work item")
		return
	}
	if !validWorkItemStatus(itemType, req.Status) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid status for this work item type")
		return
	}
	if !validStatusTransition(itemType, currentStatus, req.Status) {
		apiError(c, http.StatusUnprocessableEntity, "INVALID_TRANSITION", "Cannot move from "+currentStatus+" to "+req.Status)
		return
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	var revision int64
	err = h.store.DB().QueryRow(c.Request.Context(), `
		UPDATE work_items SET status=$3,
		closed_at = CASE WHEN $3::varchar IN ('done','closed','verified','cancelled') THEN COALESCE(closed_at, NOW()) ELSE NULL END,
		updated_at=NOW() WHERE id=$1 AND project_id=$2`+revisionClause(4)+`
		RETURNING revision`, itemID, projectID, req.Status, revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "work item", "work_items", itemID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update status")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "work_item", &itemID, "status_changed",
		"Moved work item from "+currentStatus+" to "+req.Status, map[string]interface{}{"from": currentStatus, "to": req.Status})
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": itemID, "status": req.Status, "revision": revision})
}

func (h *Handler) DeleteWorkItem(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	if auth.ProjectRoleFrom(c) == auth.ProjectRoleTester {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Testers cannot delete work items")
		return
	}
	itemID, ok := parseParamID(c, "itemId")
	if !ok {
		return
	}
	expected, ok := revisionFromRequest(c, nil)
	if !ok {
		return
	}
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(),
		`DELETE FROM work_items WHERE id=$1 AND project_id=$2`+revisionClause(3)+` RETURNING revision`,
		itemID, projectID, revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "work item", "work_items", itemID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete work item")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "work_item", &itemID, "deleted", "Deleted work item", nil)
	c.JSON(http.StatusOK, gin.H{"id": itemID, "revision": revision, "deleted": true})
}

func (h *Handler) validateWorkItem(ctx context.Context, projectID, selfID uuid.UUID, req *workItemRequest) error {
	if req.FeatureID != nil {
		if err := h.ensureBelongs(ctx, "features", projectID, *req.FeatureID); err != nil {
			return fmt.Errorf("feature does not belong to this project")
		}
	}
	var parentType string
	if req.ParentID != nil {
		if *req.ParentID == selfID {
			return fmt.Errorf("a work item cannot be its own parent")
		}
		err := h.store.DB().QueryRow(ctx, `SELECT type FROM work_items WHERE id=$1 AND project_id=$2`, *req.ParentID, projectID).Scan(&parentType)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("parent work item does not belong to this project")
		}
		if err != nil {
			return err
		}
		// Walk the ancestor chain to reject cycles.
		seen := map[uuid.UUID]bool{*req.ParentID: true}
		cursor := *req.ParentID
		for i := 0; i < 50; i++ {
			var next *uuid.UUID
			if err := h.store.DB().QueryRow(ctx, `SELECT parent_id FROM work_items WHERE id=$1`, cursor).Scan(&next); err != nil || next == nil {
				break
			}
			if *next == selfID || seen[*next] {
				return fmt.Errorf("parent hierarchy would create a cycle")
			}
			seen[*next] = true
			cursor = *next
		}
	}
	switch req.Type {
	case TypeStory:
		if req.ParentID != nil {
			return fmt.Errorf("a story belongs to a feature, not another work item")
		}
		if req.FeatureID == nil {
			return fmt.Errorf("a story must belong to a feature")
		}
	case TypeTask:
		if req.FeatureID == nil {
			return fmt.Errorf("a task must belong to a feature")
		}
		if req.ParentID != nil && parentType != TypeStory {
			return fmt.Errorf("a task may only be parented by a story")
		}
	case TypeSubtask:
		if req.FeatureID == nil || req.ParentID == nil {
			return fmt.Errorf("a subtask must belong to a feature and a parent task/story")
		}
		if parentType != TypeTask && parentType != TypeStory {
			return fmt.Errorf("a subtask may only be parented by a task or story")
		}
	case TypeBug:
		if req.FeatureID == nil && req.ParentID == nil {
			return fmt.Errorf("a bug must link to a feature or a parent work item")
		}
	case TypeSpike, TypeChore:
		// Optional feature/parent.
	default:
		return fmt.Errorf("invalid work item type")
	}
	return nil
}

func validWorkItemType(t string) bool {
	switch t {
	case TypeStory, TypeTask, TypeSubtask, TypeBug, TypeSpike, TypeChore:
		return true
	}
	return false
}

func validWorkItemStatus(t, s string) bool {
	if !validWorkItemType(t) {
		return false
	}
	if t == TypeBug {
		switch s {
		case StatusNew, StatusTriaged, StatusInProgress, StatusFixed, StatusVerified, StatusClosed, StatusReopened, StatusBlocked, StatusCancelled:
			return true
		}
		return false
	}
	switch s {
	case StatusBacklog, StatusReady, StatusInProgress, StatusInReview, StatusInQA, StatusDone, StatusBlocked, StatusCancelled:
		return true
	}
	return false
}

func validStatusTransition(t, from, to string) bool {
	if from == to {
		return true
	}
	if t == TypeBug {
		switch from {
		case StatusNew:
			return to == StatusTriaged || to == StatusCancelled
		case StatusTriaged:
			return to == StatusInProgress || to == StatusCancelled
		case StatusInProgress:
			return to == StatusFixed || to == StatusBlocked || to == StatusCancelled
		case StatusFixed:
			return to == StatusVerified || to == StatusReopened
		case StatusVerified:
			return to == StatusClosed || to == StatusReopened
		case StatusClosed:
			return to == StatusReopened
		case StatusReopened:
			return to == StatusTriaged || to == StatusInProgress
		case StatusBlocked:
			return to == StatusInProgress || to == StatusCancelled
		}
		return false
	}
	switch from {
	case StatusBacklog:
		return to == StatusReady || to == StatusInProgress || to == StatusBlocked || to == StatusCancelled
	case StatusReady:
		return to == StatusBacklog || to == StatusInProgress || to == StatusBlocked || to == StatusCancelled
	case StatusInProgress:
		return to == StatusInReview || to == StatusInQA || to == StatusDone || to == StatusBlocked || to == StatusCancelled
	case StatusInReview:
		return to == StatusInProgress || to == StatusInQA || to == StatusDone || to == StatusBlocked
	case StatusInQA:
		return to == StatusInProgress || to == StatusInReview || to == StatusDone || to == StatusBlocked
	case StatusDone:
		return to == StatusReopened || to == StatusInProgress
	case StatusBlocked:
		return to == StatusReady || to == StatusInProgress || to == StatusInReview || to == StatusInQA || to == StatusCancelled
	case StatusCancelled:
		return to == StatusBacklog || to == StatusReady
	}
	return false
}

func validPriority(p string) bool {
	switch p {
	case PriorityLow, PriorityMedium, PriorityHigh, PriorityCritical:
		return true
	}
	return false
}

func (h *Handler) ensureBelongs(ctx context.Context, table string, projectID, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("missing id")
	}
	var exists bool
	var q string
	if table == "git_branches" {
		q = `SELECT EXISTS (
			SELECT 1 FROM git_branches b
			JOIN git_repositories r ON r.id = b.repository_id
			WHERE b.id=$1 AND r.project_id=$2)`
	} else {
		q = fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE id=$1 AND project_id=$2)`, table)
	}
	if err := h.store.DB().QueryRow(ctx, q, id, projectID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return pgx.ErrNoRows
	}
	return nil
}

// ---------------------------------------------------------------------------
// Sprints and releases
// ---------------------------------------------------------------------------

type sprintRequest struct {
	Name            string   `json:"name"`
	Goal            string   `json:"goal"`
	Status          string   `json:"status"`
	StartDate       *string  `json:"start_date"`
	EndDate         *string  `json:"end_date"`
	Capacity        *float64 `json:"capacity"`
	ExpectedVersion *int64   `json:"expected_version"`
}

func (h *Handler) CreateSprint(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req sprintRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Sprint name is required")
		return
	}
	if req.Status == "" {
		req.Status = "planned"
	}
	start, _ := parseTimePtr(req.StartDate)
	end, _ := parseTimePtr(req.EndDate)
	var id uuid.UUID
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO sprints (project_id, name, goal, status, start_date, end_date, capacity)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, revision`,
		projectID, req.Name, req.Goal, req.Status, start, end, req.Capacity).Scan(&id, &revision)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create sprint")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "sprint", &id, "created", "Created sprint "+req.Name, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusCreated, gin.H{"id": id, "revision": revision})
}

func (h *Handler) UpdateSprint(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	sprintID, ok := parseParamID(c, "sprintId")
	if !ok {
		return
	}
	var req sprintRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	start, _ := parseTimePtr(req.StartDate)
	end, _ := parseTimePtr(req.EndDate)
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		UPDATE sprints SET name=$3, goal=$4, status=$5, start_date=$6, end_date=$7, capacity=$8, updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(9)+`
		RETURNING revision`, sprintID, projectID, req.Name, req.Goal, req.Status, start, end, req.Capacity,
		revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "sprint", "sprints", sprintID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update sprint")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "sprint", &sprintID, "updated", "Updated sprint "+req.Name, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": sprintID, "revision": revision})
}

type releaseRequest struct {
	Name            string  `json:"name"`
	Version         string  `json:"version"`
	Status          string  `json:"status"`
	PlannedAt       *string `json:"planned_at"`
	Notes           string  `json:"notes"`
	ExpectedVersion *int64  `json:"expected_version"`
}

func (h *Handler) CreateRelease(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req releaseRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Version) == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Release name and version are required")
		return
	}
	if req.Status == "" {
		req.Status = "planned"
	}
	planned, _ := parseTimePtr(req.PlannedAt)
	var id uuid.UUID
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO releases (project_id, name, version, status, planned_at, notes)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, revision`,
		projectID, req.Name, req.Version, req.Status, planned, req.Notes).Scan(&id, &revision)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create release")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "release", &id, "created", "Created release "+req.Version, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusCreated, gin.H{"id": id, "revision": revision})
}

func (h *Handler) UpdateRelease(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	releaseID, ok := parseParamID(c, "releaseId")
	if !ok {
		return
	}
	var req releaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	planned, _ := parseTimePtr(req.PlannedAt)
	var releasedAt interface{}
	if req.Status == "released" {
		releasedAt = time.Now().UTC()
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		UPDATE releases SET name=$3, version=$4, status=$5, planned_at=$6, released_at=COALESCE($7, released_at),
		notes=$8, updated_at=NOW() WHERE id=$1 AND project_id=$2`+revisionClause(9)+`
		RETURNING revision`,
		releaseID, projectID, req.Name, req.Version, req.Status, planned, releasedAt, req.Notes,
		revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "release", "releases", releaseID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update release")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "release", &releaseID, "updated", "Updated release "+req.Version, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": releaseID, "revision": revision})
}

// ---------------------------------------------------------------------------
// Traceability
// ---------------------------------------------------------------------------

type traceabilityFeature struct {
	FeatureID       uuid.UUID   `json:"feature_id"`
	FeatureTitle    string      `json:"feature_title"`
	EpicID          uuid.UUID   `json:"epic_id"`
	PRDID           *uuid.UUID  `json:"prd_id"`
	Stories         int         `json:"stories"`
	Tasks           int         `json:"tasks"`
	TestCaseIDs     []uuid.UUID `json:"test_case_ids"`
	LatestTestRunID *uuid.UUID  `json:"latest_test_run_id"`
	TestCases       int         `json:"test_cases"`
	LastRunStatus   string      `json:"last_run_status"`
	OpenBugs        int         `json:"open_bugs"`
	BranchIDs       []uuid.UUID `json:"branch_ids"`
	BranchCount     int         `json:"branch_count"`
	PullRequestIDs  []uuid.UUID `json:"pull_request_ids"`
	PRCount         int         `json:"pr_count"`
	BuildIDs        []uuid.UUID `json:"build_ids"`
	BuildCount      int         `json:"build_count"`
	DeploymentIDs   []uuid.UUID `json:"deployment_ids"`
	Deployments     int         `json:"deployments"`
	HealthStatus    string      `json:"health_status"`
	Gaps            []string    `json:"gaps"`
}

type traceabilityResponse struct {
	Features []traceabilityFeature `json:"features"`
	Gaps     []string              `json:"gaps"`
}

func buildTraceability(w Workspace) traceabilityResponse {
	resp := traceabilityResponse{Features: []traceabilityFeature{}, Gaps: []string{}}
	epicByID := map[uuid.UUID]Epic{}
	for _, e := range w.Epics {
		epicByID[e.ID] = e
	}
	itemsByFeature := map[uuid.UUID][]WorkItem{}
	for _, it := range w.WorkItems {
		if it.FeatureID != nil {
			itemsByFeature[*it.FeatureID] = append(itemsByFeature[*it.FeatureID], it)
		}
	}
	casesByFeature := map[uuid.UUID][]uuid.UUID{}
	for _, tc := range w.TestCases {
		if tc.FeatureID != nil {
			casesByFeature[*tc.FeatureID] = append(casesByFeature[*tc.FeatureID], tc.ID)
		}
	}
	latestResultByCase := map[uuid.UUID]TestResult{}
	for _, result := range w.TestResults {
		current, ok := latestResultByCase[result.TestCaseID]
		if !ok || result.UpdatedAt.After(current.UpdatedAt) {
			latestResultByCase[result.TestCaseID] = result
		}
	}
	branchesByItem := map[uuid.UUID][]uuid.UUID{}
	for _, b := range w.Branches {
		if b.WorkItemID != nil {
			branchesByItem[*b.WorkItemID] = append(branchesByItem[*b.WorkItemID], b.ID)
		}
	}
	prsByItem := map[uuid.UUID][]PullRequest{}
	for _, pr := range w.PullRequests {
		if pr.WorkItemID != nil {
			prsByItem[*pr.WorkItemID] = append(prsByItem[*pr.WorkItemID], pr)
		}
	}

	for _, f := range w.Features {
		row := traceabilityFeature{
			FeatureID: f.ID, FeatureTitle: f.Title, EpicID: f.EpicID,
			TestCaseIDs: append([]uuid.UUID(nil), casesByFeature[f.ID]...),
			TestCases:   len(casesByFeature[f.ID]),
			Gaps:        []string{},
		}
		if e, ok := epicByID[f.EpicID]; ok {
			row.PRDID = e.PRDID
		}

		itemIDs := map[uuid.UUID]bool{}
		releaseIDs := map[uuid.UUID]bool{}
		branchIDs := map[uuid.UUID]bool{}
		mergeSHAs := map[string]bool{}
		buildIDs := map[uuid.UUID]bool{}
		if f.TargetReleaseID != nil {
			releaseIDs[*f.TargetReleaseID] = true
		}
		for _, it := range itemsByFeature[f.ID] {
			itemIDs[it.ID] = true
			if it.ReleaseID != nil {
				releaseIDs[*it.ReleaseID] = true
			}
			switch it.Type {
			case TypeStory:
				row.Stories++
			case TypeTask, TypeSubtask:
				row.Tasks++
			case TypeBug:
				if it.Status != StatusClosed && it.Status != StatusVerified && it.Status != StatusCancelled {
					row.OpenBugs++
				}
			}
		}
		for itemID := range itemIDs {
			for _, branchID := range branchesByItem[itemID] {
				branchIDs[branchID] = true
			}
			for _, pr := range prsByItem[itemID] {
				row.PullRequestIDs = append(row.PullRequestIDs, pr.ID)
				row.PRCount++
				if pr.MergeCommitSHA != "" {
					mergeSHAs[pr.MergeCommitSHA] = true
				}
			}
		}
		for _, branch := range w.Branches {
			if branchIDs[branch.ID] {
				row.BranchIDs = append(row.BranchIDs, branch.ID)
			}
		}
		row.BranchCount = len(branchIDs)
		for _, build := range w.Builds {
			if mergeSHAs[build.CommitSHA] {
				buildIDs[build.ID] = true
				row.BuildIDs = append(row.BuildIDs, build.ID)
			}
		}

		// Deployments are linked through the feature/work-item release or through
		// a build produced by a merged pull request for this feature.
		deployments := []Deployment{}
		for _, d := range w.Deployments {
			linked := false
			if d.BuildID != nil && buildIDs[*d.BuildID] {
				linked = true
			}
			if d.ReleaseID != nil && releaseIDs[*d.ReleaseID] {
				linked = true
			}
			if linked {
				deployments = append(deployments, d)
				if d.BuildID != nil {
					buildIDs[*d.BuildID] = true
				}
			}
		}
		row.BuildCount = len(buildIDs)
		row.Deployments = len(deployments)
		for _, deployment := range deployments {
			row.DeploymentIDs = append(row.DeploymentIDs, deployment.ID)
		}
		// Deployments are ordered newest first. Feature health reflects the
		// most recent deployment, not a healthier historical one.
		if len(deployments) > 0 {
			row.HealthStatus = deployments[0].HealthStatus
		}

		// The latest result across every test case attached to this feature.
		var latest *TestResult
		for _, caseID := range casesByFeature[f.ID] {
			result, ok := latestResultByCase[caseID]
			if !ok {
				continue
			}
			if latest == nil || result.UpdatedAt.After(latest.UpdatedAt) {
				copied := result
				latest = &copied
			}
		}
		if latest != nil {
			row.LastRunStatus = latest.Status
			row.LatestTestRunID = &latest.TestRunID
		}

		if row.PRDID == nil {
			row.Gaps = append(row.Gaps, "No PRD linked through epic")
		}
		if row.Stories == 0 {
			row.Gaps = append(row.Gaps, "No story")
		}
		if row.TestCases == 0 {
			row.Gaps = append(row.Gaps, "No test case")
		} else if row.LastRunStatus == "" {
			row.Gaps = append(row.Gaps, "Tests not run")
		}
		if row.BranchCount == 0 {
			row.Gaps = append(row.Gaps, "No branch")
		}
		if row.PRCount == 0 {
			row.Gaps = append(row.Gaps, "No pull request")
		}
		if row.HealthStatus != "healthy" {
			row.Gaps = append(row.Gaps, "No healthy deployment")
		}
		resp.Features = append(resp.Features, row)
		for _, gap := range row.Gaps {
			resp.Gaps = append(resp.Gaps, f.Title+": "+gap)
		}
	}
	return resp
}
