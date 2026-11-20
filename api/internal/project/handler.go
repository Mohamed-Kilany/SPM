// Package project implements the HTTP handlers for project routes.
package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/events"
	"spm-platform/api/internal/gitengine"
	"spm-platform/api/internal/models"
	"spm-platform/api/internal/snapshot"
)

const maxInlineDeltas = 500

// Handler handles project-related HTTP routes.
type Handler struct {
	db           *pgxpool.Pool
	snapRepo     *snapshot.Repo
	eventsClient *events.Client
	gitEngine    gitengine.Provisioner
}

// NewHandler constructs a project Handler.
func NewHandler(db *pgxpool.Pool, snapRepo *snapshot.Repo, eventsClient *events.Client, gitEngine gitengine.Provisioner) *Handler {
	return &Handler{db: db, snapRepo: snapRepo, eventsClient: eventsClient, gitEngine: gitEngine}
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
// GET /projects
// ---------------------------------------------------------------------------

type projectSummary struct {
	ID                uuid.UUID       `json:"id"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	OwnerID           uuid.UUID       `json:"owner_id"`
	RepoName          string          `json:"repo_name"`
	DefaultBranch     string          `json:"default_branch"`
	AppName           string          `json:"app_name"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	Tickets           []models.Ticket `json:"tickets"`
	TicketCount       int             `json:"ticket_count"`
	WorkItemCount     int             `json:"work_item_count"`
	DoneWorkItemCount int             `json:"done_work_item_count"`
	TestCaseCount     int             `json:"test_case_count"`
	OpenBugCount      int             `json:"open_bug_count"`
	LastActivityAt    *time.Time      `json:"last_activity_at"`
}

// List handles GET /projects. Admins see all projects; everyone else sees
// only projects they are a member of.
func (h *Handler) List(c *gin.Context) {
	claims, ok := auth.ClaimsFrom(c)
	if !ok {
		apiError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Missing authentication claims")
		return
	}

	ctx := c.Request.Context()
	const baseSelect = `
		SELECT p.id, p.name, COALESCE(p.description, ''), p.owner_id, p.repo_name,
		       p.default_branch, p.app_name, p.created_at, p.updated_at,
		       COALESCE(s.data, '\x'::bytea), COALESCE(s.last_sequence_id, 0),
		       COALESCE((SELECT COUNT(*)::int FROM work_items wi WHERE wi.project_id = p.id), 0),
		       COALESCE((SELECT COUNT(*)::int FROM work_items wi WHERE wi.project_id = p.id AND wi.status = 'done'), 0),
		       COALESCE((SELECT COUNT(*)::int FROM test_cases tc WHERE tc.project_id = p.id), 0),
		       COALESCE((SELECT COUNT(*)::int FROM work_items wi
		                 WHERE wi.project_id = p.id AND wi.type = 'bug'
		                   AND wi.status NOT IN ('verified', 'closed', 'cancelled')), 0)
		FROM projects p
		LEFT JOIN snapshots s ON s.project_id = p.id`

	var (
		rows pgx.Rows
		err  error
	)
	if claims.Role == auth.RoleAdmin {
		rows, err = h.db.Query(ctx, baseSelect+` ORDER BY p.created_at DESC`)
	} else {
		userID, parseErr := uuid.Parse(claims.CanonicalUserID())
		if parseErr != nil {
			apiError(c, http.StatusUnauthorized, "INVALID_TOKEN", "User ID in claims is not a valid UUID")
			return
		}
		rows, err = h.db.Query(ctx, baseSelect+`
			JOIN project_members pm ON pm.project_id = p.id
			WHERE pm.user_id = $1 AND p.status = 'active'
			ORDER BY p.created_at DESC`, userID)
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to query projects")
		return
	}
	defer rows.Close()

	projects := make([]projectSummary, 0)
	for rows.Next() {
		var (
			p             projectSummary
			compressed    []byte
			lastSeq       int64
			workItems     int
			doneWorkItems int
			testCases     int
			openBugs      int
		)
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.OwnerID, &p.RepoName,
			&p.DefaultBranch, &p.AppName, &p.CreatedAt, &p.UpdatedAt, &compressed, &lastSeq,
			&workItems, &doneWorkItems, &testCases, &openBugs); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to scan project row")
			return
		}
		p.WorkItemCount = workItems
		p.DoneWorkItemCount = doneWorkItems
		p.TestCaseCount = testCases
		p.OpenBugCount = openBugs
		p.Tickets = []models.Ticket{}
		p.TicketCount = 0
		p.LastActivityAt = nil
		if len(compressed) > 0 {
			state, decodeErr := snapshot.DecodeCompressed(compressed)
			if decodeErr == nil {
				if state.Name != "" {
					p.Name = state.Name
				}
				if state.RepoName != "" {
					p.RepoName = state.RepoName
				}
				if state.DefaultBranch != "" {
					p.DefaultBranch = state.DefaultBranch
				}
				if state.AppName != "" {
					p.AppName = state.AppName
				}
				if state.Description != "" {
					p.Description = state.Description
				}
				p.Tickets = state.Tickets
				p.TicketCount = len(state.Tickets)
				if len(state.Tickets) > 0 {
					latest := state.Tickets[0].UpdatedAt
					for _, ticket := range state.Tickets[1:] {
						if ticket.UpdatedAt.After(latest) {
							latest = ticket.UpdatedAt
						}
					}
					if latest.IsZero() {
						latest = state.UpdatedAt
					}
					if !latest.IsZero() {
						p.LastActivityAt = &latest
					}
				}
				if !state.UpdatedAt.IsZero() &&
					(p.LastActivityAt == nil || state.UpdatedAt.After(*p.LastActivityAt)) {
					latest := state.UpdatedAt
					p.LastActivityAt = &latest
				}
			}
		}
		if p.LastActivityAt == nil && !p.CreatedAt.IsZero() {
			created := p.CreatedAt
			p.LastActivityAt = &created
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Error iterating project rows")
		return
	}
	c.JSON(http.StatusOK, projects)
}

// ---------------------------------------------------------------------------
// POST /projects
// ---------------------------------------------------------------------------

type createRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

type createResponse struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	RepoName string    `json:"repo_name"`
	Warnings []string  `json:"warnings"`
}

var (
	nonSlugRE    = regexp.MustCompile(`[^a-z0-9-]+`)
	whitespaceRE = regexp.MustCompile(`\s+`)
)

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, "_", "-")
	s = whitespaceRE.ReplaceAllString(s, "-")
	s = nonSlugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		return "project"
	}
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return s
}

// Create handles POST /projects.
func (h *Handler) Create(c *gin.Context) {
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Project name must not be empty")
		return
	}
	if utf8.RuneCountInString(req.Name) > 255 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Project name must not exceed 255 characters")
		return
	}
	if req.Description != nil && utf8.RuneCountInString(*req.Description) > 4000 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Description must not exceed 4000 characters")
		return
	}

	claims, ok := auth.ClaimsFrom(c)
	if !ok {
		apiError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Missing authentication claims")
		return
	}
	ownerID, err := uuid.Parse(claims.CanonicalUserID())
	if err != nil {
		apiError(c, http.StatusUnauthorized, "INVALID_TOKEN", "User ID in claims is not a valid UUID")
		return
	}

	projectID := models.NewID()
	now := time.Now().UTC()
	slug := slugify(req.Name)
	repoName := slug + "-" + projectID.String()[:8]
	appName := slug
	description := ""
	if req.Description != nil {
		description = *req.Description
	}

	columns := models.DefaultBoardColumns()
	state := models.ProjectState{
		ProjectID:     projectID,
		Name:          req.Name,
		Description:   description,
		OwnerID:       ownerID,
		RepoName:      repoName,
		DefaultBranch: "main",
		AppName:       appName,
		Columns:       columns,
		Tickets:       []models.Ticket{},
		Members:       []models.ProjectMember{{UserID: ownerID, Role: auth.ProjectRoleOwner}},
		Pipelines:     []models.PipelineStatus{},
		Deployments:   []models.DeploymentStatus{},
		Notifications: []models.Notification{},
		UpdatedAt:     now,
	}
	payload, err := json.Marshal(map[string]interface{}{
		"project_id":     projectID,
		"name":           state.Name,
		"description":    state.Description,
		"owner_id":       ownerID,
		"repo_name":      repoName,
		"default_branch": state.DefaultBranch,
		"app_name":       appName,
		"members":        state.Members,
		"columns":        columns,
		"created_at":     now,
	})
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to marshal project state")
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start project transaction")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertProject = `
		INSERT INTO projects (id, name, description, owner_id, repo_name, default_branch, app_name, key_prefix, created_at, updated_at)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7,
		        COALESCE(NULLIF(UPPER(LEFT(REGEXP_REPLACE($2::varchar, '[^a-zA-Z0-9]', '', 'g'), 4)), ''), 'PRJ'),
		        $8, $8)`
	if _, err := tx.Exec(ctx, insertProject, projectID, req.Name, description, ownerID,
		repoName, "main", appName, now); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create project")
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, $3)`,
		projectID, ownerID, auth.ProjectRoleOwner,
	); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to add project owner")
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO environments (project_id, name, type, position)
		VALUES ($1, 'Development', 'development', 1),
		       ($1, 'Staging', 'staging', 2),
		       ($1, 'Production', 'production', 3)
		ON CONFLICT (project_id, name) DO NOTHING`, projectID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create default environments")
		return
	}

	webhookBase := strings.TrimRight(os.Getenv("SPM_WEBHOOK_BASE_URL"), "/")
	if webhookBase == "" {
		webhookBase = "http://localhost:8080"
	}
	webhookToken := projectWebhookToken()
	repoURL := strings.TrimRight(os.Getenv("GITEA_PUBLIC_URL"), "/")
	if repoURL == "" {
		repoURL = strings.TrimRight(os.Getenv("GITEA_ROOT_URL"), "/")
	}
	repoURL += "/" + envOrProject("GITEA_ORG", "spm") + "/" + repoName
	if _, err := tx.Exec(ctx, `
		INSERT INTO git_repositories (project_id, provider, name, url, default_branch, external_id, webhook_token, webhook_url)
		VALUES ($1, 'gitea', $2, $3, 'main', $2, $4, $5)
		ON CONFLICT (project_id, name) DO NOTHING`,
		projectID, repoName, repoURL, webhookToken, webhookBase+"/api/v1/webhooks/gitea"); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to register the source repository")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit project")
		return
	}

	// Publish only after the relational row is committed. Publishing inside the
	// transaction would let the worker observe (and reject) an event for a
	// project that is not yet visible. A transient publish failure is retried;
	// the first subsequent event reconstructs the snapshot from project
	// metadata, so the project is never lost.
	warnings := make([]string, 0, 2)
	envelope := models.EventEnvelope{
		EventType:   "project.created",
		AggregateID: projectID,
		Payload:     json.RawMessage(payload),
		CreatedAt:   now,
	}
	var publishErr error
	for attempt := 0; attempt < 3; attempt++ {
		if _, publishErr = h.eventsClient.Publish(ctx, envelope, 0); publishErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			attempt = 3
		case <-time.After(time.Duration(attempt+1) * 150 * time.Millisecond):
		}
	}
	if publishErr != nil {
		warnings = append(warnings, "Project created but its event could not be published; the board will recover on the next change")
	}

	// Provisioning is deliberately after commit: a Git outage must not make
	// project creation fail. The failure is recorded as an event for the UI.
	provCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if h.gitEngine != nil {
		if err := h.gitEngine.ProvisionRepo(provCtx, projectID.String(), repoName); err != nil {
			warnings = append(warnings, "Git repository provisioning failed; it will be retried automatically")
			h.publishBestEffort(ctx, "git.repo.provision_failed", projectID, map[string]interface{}{
				"reason": err.Error(),
			})
		} else {
			h.publishBestEffort(ctx, "git.repo.provisioned", projectID, map[string]interface{}{
				"repo_name": repoName,
			})
			if registrar, ok := h.gitEngine.(gitengine.WebhookRegistrar); ok {
				webhookURL := strings.TrimSpace(os.Getenv("SPM_INTERNAL_WEBHOOK_URL"))
				if webhookURL == "" {
					webhookBase := strings.TrimRight(os.Getenv("SPM_WEBHOOK_BASE_URL"), "/")
					if webhookBase == "" {
						webhookBase = "http://localhost:8080"
					}
					webhookURL = webhookBase + "/api/v1/webhooks/gitea"
				}
				if err := registrar.EnsureRepoWebhook(provCtx, repoName, webhookURL, webhookToken); err != nil {
					warnings = append(warnings, "Git repository created but its webhook could not be registered")
				}
			}
		}
	}

	c.JSON(http.StatusCreated, createResponse{
		ID: projectID, Name: req.Name, RepoName: repoName, Warnings: warnings,
	})
}

// ---------------------------------------------------------------------------
// GET /projects/:id
// ---------------------------------------------------------------------------

// Get handles GET /projects/:id using the snapshot read path plus delta replay.
func (h *Handler) Get(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_ID", "Project ID is not a valid UUID")
		return
	}
	if !auth.HasProjectCapability(c, auth.CapProjectRead) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "This action requires the project.read capability")
		return
	}

	ctx := c.Request.Context()
	meta, metaErr := h.snapRepo.GetProjectMeta(ctx, projectID)
	if metaErr != nil {
		if errors.Is(metaErr, snapshot.ErrNotFound) {
			apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load project")
		return
	}

	snap, err := h.snapRepo.Get(ctx, projectID)
	if err != nil && !errors.Is(err, snapshot.ErrNotFound) {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read snapshot")
		return
	}

	if errors.Is(err, snapshot.ErrNotFound) {
		state, latestSeq, replayErr := h.replayAll(ctx, projectID, meta)
		if replayErr != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to replay project events")
			return
		}
		c.Header("X-Snapshot-Sequence", strconv.FormatInt(latestSeq, 10))
		c.JSON(http.StatusOK, state)
		return
	}

	deltaCount, err := h.snapRepo.CountDeltaEvents(ctx, projectID, snap.LastSequenceID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to count delta events")
		return
	}
	if deltaCount > maxInlineDeltas {
		state, latestSeq, replayErr := h.replayAll(ctx, projectID, meta)
		if replayErr != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to rebuild project state")
			return
		}
		c.Header("X-Snapshot-Sequence", strconv.FormatInt(latestSeq, 10))
		c.Header("X-Snapshot-Rebuilt", "true")
		c.JSON(http.StatusOK, state)
		return
	}

	var state models.ProjectState
	if err := json.Unmarshal(snap.Data, &state); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to deserialize snapshot")
		return
	}
	normalizeState(&state, projectID, meta)
	latestSeq := snap.LastSequenceID

	deltaEvents, err := h.snapRepo.GetDeltaEvents(ctx, projectID, snap.LastSequenceID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch delta events")
		return
	}
	for _, evt := range deltaEvents {
		next, applyErr := snapshot.Apply(state, evt)
		if applyErr != nil {
			if errors.Is(applyErr, snapshot.ErrUnknownEventType) {
				latestSeq = evt.SequenceID
				continue
			}
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to apply delta event")
			return
		}
		state = next
		latestSeq = evt.SequenceID
	}

	c.Header("X-Snapshot-Sequence", strconv.FormatInt(latestSeq, 10))
	c.JSON(http.StatusOK, state)
}

// replayAll rebuilds state from the PostgreSQL event mirror.
func (h *Handler) replayAll(ctx context.Context, projectID uuid.UUID, meta snapshot.ProjectMeta) (models.ProjectState, int64, error) {
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
	allEvents, err := h.snapRepo.GetDeltaEvents(ctx, projectID, 0)
	if err != nil {
		return models.ProjectState{}, 0, err
	}
	latestSeq := int64(0)
	for _, evt := range allEvents {
		next, applyErr := snapshot.Apply(state, evt)
		if applyErr != nil {
			if errors.Is(applyErr, snapshot.ErrUnknownEventType) {
				latestSeq = evt.SequenceID
				continue
			}
			return models.ProjectState{}, 0, applyErr
		}
		state = next
		latestSeq = evt.SequenceID
	}
	return state, latestSeq, nil
}

// normalizeState fills fields that may be absent from older snapshots.
func normalizeState(state *models.ProjectState, projectID uuid.UUID, meta snapshot.ProjectMeta) {
	if state.ProjectID == uuid.Nil {
		state.ProjectID = projectID
	}
	if state.Name == "" {
		state.Name = meta.Name
	}
	if state.Description == "" {
		state.Description = meta.Description
	}
	if state.RepoName == "" {
		state.RepoName = meta.RepoName
	}
	if state.DefaultBranch == "" {
		state.DefaultBranch = meta.DefaultBranch
	}
	if state.AppName == "" {
		state.AppName = meta.AppName
	}
	if len(state.Columns) == 0 {
		state.Columns = models.DefaultBoardColumns()
	}
	if state.Tickets == nil {
		state.Tickets = []models.Ticket{}
	}
	if state.Members == nil {
		state.Members = []models.ProjectMember{}
	}
	if state.Pipelines == nil {
		state.Pipelines = []models.PipelineStatus{}
	}
	if state.Deployments == nil {
		state.Deployments = []models.DeploymentStatus{}
	}
	if state.Notifications == nil {
		state.Notifications = []models.Notification{}
	}
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

func envOrProject(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func projectWebhookToken() string {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(raw[:])
}

func (h *Handler) publishBestEffort(ctx context.Context, eventType string, projectID uuid.UUID, payload map[string]interface{}) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = h.eventsClient.Publish(ctx, models.EventEnvelope{
		EventType: eventType, AggregateID: projectID, Payload: body, CreatedAt: time.Now().UTC(),
	}, 0)
}
