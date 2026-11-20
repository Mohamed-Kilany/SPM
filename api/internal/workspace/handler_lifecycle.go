package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"spm-platform/api/internal/audit"
)

// ---------------------------------------------------------------------------
// State machines
// ---------------------------------------------------------------------------

// validSprintUpdateTransition allows metadata edits and cancellation. Starting
// and closing have dedicated endpoints because both require additional work
// (validation and carry-over respectively).
func validSprintUpdateTransition(from, to string) bool {
	if from == to {
		return true
	}
	if to != "cancelled" {
		return false
	}
	return from == "planned" || from == "active"
}

// validReleaseTransition is the release state machine. A release must be
// signed off before it can be marked released.
func validReleaseTransition(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case "planned":
		return to == "in_progress" || to == "cancelled"
	case "in_progress":
		return to == "released" || to == "cancelled"
	default:
		return false
	}
}

// terminalWorkItemStatuses are the states that count as complete for sprint
// velocity. Cancelled work remains in its sprint but is not carried over.
var terminalWorkItemStatuses = map[string]bool{
	StatusDone:      true,
	StatusClosed:    true,
	StatusVerified:  true,
	StatusCancelled: true,
}

func isTerminalWorkItemStatus(status string) bool {
	return terminalWorkItemStatuses[status]
}

// isCompletedWorkItemStatus is terminal work that counts toward completion and
// velocity. Cancelled work stays in its sprint but never counts as completed.
func isCompletedWorkItemStatus(status string) bool {
	switch status {
	case StatusDone, StatusClosed, StatusVerified:
		return true
	default:
		return false
	}
}

const incompleteWorkItemPredicate = `status NOT IN ('done', 'closed', 'verified', 'cancelled')`

// ---------------------------------------------------------------------------
// Sprint lifecycle
// ---------------------------------------------------------------------------

type sprintStartRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
}

// StartSprint handles POST /projects/:id/sprints/:sprintId/start.
func (h *Handler) StartSprint(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	sprintID, ok := parseParamID(c, "sprintId")
	if !ok {
		return
	}
	var req sprintStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	tx, err := h.store.DB().Begin(ctx)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start sprint")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize concurrent starts in the same project so the overlap check is
	// authoritative without a schema-level exclusion constraint.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM projects WHERE id=$1 FOR UPDATE`, projectID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to lock project")
		return
	}
	var name, goal, status string
	var start, end *time.Time
	err = tx.QueryRow(ctx, `
		SELECT name, COALESCE(goal, ''), status, start_date, end_date
		FROM sprints WHERE id=$1 AND project_id=$2 FOR UPDATE`, sprintID, projectID).
		Scan(&name, &goal, &status, &start, &end)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Sprint not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint")
		return
	}
	if status != "planned" {
		apiError(c, http.StatusConflict, "SPRINT_NOT_PLANNED", "Only a planned sprint can be started")
		return
	}
	if strings.TrimSpace(goal) == "" {
		apiError(c, http.StatusUnprocessableEntity, "SPRINT_GOAL_REQUIRED", "A sprint requires a goal before it can start")
		return
	}
	if start == nil || end == nil {
		apiError(c, http.StatusUnprocessableEntity, "SPRINT_DATES_REQUIRED", "A sprint requires start and end dates before it can start")
		return
	}
	if end.Before(*start) {
		apiError(c, http.StatusUnprocessableEntity, "SPRINT_DATES_INVALID", "Sprint end date cannot be before its start date")
		return
	}
	overlap, err := sprintOverlapsTx(ctx, tx, projectID, sprintID, *start, *end)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to check sprint overlap")
		return
	}
	if overlap {
		apiError(c, http.StatusConflict, "SPRINT_OVERLAP", "Another active sprint overlaps these dates")
		return
	}

	var revision int64
	var activatedAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE sprints
		SET status='active', activated_at=NOW(), updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(3)+`
		RETURNING revision, activated_at`,
		sprintID, projectID, revisionValue(expected)).Scan(&revision, &activatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(ctx)
		h.revisionFailure(c, "sprint", "sprints", sprintID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start sprint")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start sprint")
		return
	}

	h.recordActivity(ctx, projectID, actorID(c), "sprint", &sprintID, "started", "Started sprint "+name, map[string]interface{}{"start_date": start, "end_date": end})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "sprint.start", TargetType: "sprint", TargetID: sprintID.String(), Result: "success",
		After: map[string]any{"name": name, "goal": goal, "start_date": start, "end_date": end, "revision": revision},
	})
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": sprintID, "revision": revision, "status": "active", "activated_at": activatedAt})
}

type sprintCarryItem struct {
	ID             uuid.UUID `json:"id"`
	Key            string    `json:"key"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	EstimatePoints *float64  `json:"estimate_points"`
}

type sprintClosePreview struct {
	SprintID         uuid.UUID         `json:"sprint_id"`
	SprintName       string            `json:"sprint_name"`
	Status           string            `json:"status"`
	Policy           string            `json:"policy"`
	NextSprintID     *uuid.UUID        `json:"next_sprint_id"`
	NextSprintName   string            `json:"next_sprint_name"`
	IncompleteItems  []sprintCarryItem `json:"incomplete_items"`
	IncompleteCount  int               `json:"incomplete_count"`
	IncompletePoints float64           `json:"incomplete_points"`
	CompletedCount   int               `json:"completed_count"`
	CompletedPoints  float64           `json:"completed_points"`
}

func sprintPolicy(raw string) (string, bool) {
	switch strings.TrimSpace(raw) {
	case "", "backlog":
		return "backlog", true
	case "next_sprint":
		return "next_sprint", true
	}
	return "", false
}

// PreviewSprintClose handles GET /projects/:id/sprints/:sprintId/close-preview.
func (h *Handler) PreviewSprintClose(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	sprintID, ok := parseParamID(c, "sprintId")
	if !ok {
		return
	}
	policy, ok := sprintPolicy(c.Query("policy"))
	if !ok {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "policy must be backlog or next_sprint")
		return
	}
	var nextSprintID *uuid.UUID
	if raw := strings.TrimSpace(c.Query("next_sprint_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "next_sprint_id must be a UUID")
			return
		}
		nextSprintID = &id
	}
	if policy == "next_sprint" && nextSprintID == nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "next_sprint_id is required for the next_sprint policy")
		return
	}

	ctx := c.Request.Context()
	var preview sprintClosePreview
	preview.SprintID = sprintID
	preview.Policy = policy
	err := h.store.DB().QueryRow(ctx, `
		SELECT name, status FROM sprints WHERE id=$1 AND project_id=$2`, sprintID, projectID).
		Scan(&preview.SprintName, &preview.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Sprint not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint")
		return
	}
	if preview.Status != "active" {
		apiError(c, http.StatusConflict, "SPRINT_NOT_ACTIVE", "Only an active sprint can be closed")
		return
	}
	if nextSprintID != nil {
		var nextStatus string
		err = h.store.DB().QueryRow(ctx, `
			SELECT name, status FROM sprints WHERE id=$1 AND project_id=$2`, *nextSprintID, projectID).
			Scan(&preview.NextSprintName, &nextStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			apiError(c, http.StatusNotFound, "NOT_FOUND", "Next sprint not found")
			return
		}
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load next sprint")
			return
		}
		if *nextSprintID == sprintID {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A sprint cannot carry work into itself")
			return
		}
		if nextStatus != "planned" && nextStatus != "active" {
			apiError(c, http.StatusUnprocessableEntity, "SPRINT_NOT_OPEN", "Carry-over target must be planned or active")
			return
		}
		preview.NextSprintID = nextSprintID
	}

	rows, err := h.store.DB().Query(ctx, `
		SELECT w.id, p.key_prefix || '-' || w.item_number, w.title, w.status, w.estimate_points
		FROM work_items w
		JOIN projects p ON p.id = w.project_id
		WHERE w.project_id=$1 AND w.sprint_id=$2
		ORDER BY w.item_number`, projectID, sprintID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint work")
		return
	}
	defer rows.Close()
	preview.IncompleteItems = []sprintCarryItem{}
	for rows.Next() {
		var item sprintCarryItem
		if err := rows.Scan(&item.ID, &item.Key, &item.Title, &item.Status, &item.EstimatePoints); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint work")
			return
		}
		if isCompletedWorkItemStatus(item.Status) {
			preview.CompletedCount++
			if item.EstimatePoints != nil {
				preview.CompletedPoints += *item.EstimatePoints
			}
			continue
		}
		if item.Status == StatusCancelled {
			// Cancelled work stays attached to the closed sprint but is neither
			// carried over nor counted as completed.
			continue
		}
		preview.IncompleteItems = append(preview.IncompleteItems, item)
		preview.IncompleteCount++
		if item.EstimatePoints != nil {
			preview.IncompletePoints += *item.EstimatePoints
		}
	}
	if err := rows.Err(); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint work")
		return
	}
	preview.IncompletePoints = round2(preview.IncompletePoints)
	preview.CompletedPoints = round2(preview.CompletedPoints)
	c.JSON(http.StatusOK, preview)
}

type sprintCloseRequest struct {
	Policy          string     `json:"policy"`
	NextSprintID    *uuid.UUID `json:"next_sprint_id"`
	ExpectedVersion *int64     `json:"expected_version"`
	Note            string     `json:"note"`
}

// CloseSprint handles POST /projects/:id/sprints/:sprintId/close.
func (h *Handler) CloseSprint(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	sprintID, ok := parseParamID(c, "sprintId")
	if !ok {
		return
	}
	var req sprintCloseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	policy, ok := sprintPolicy(req.Policy)
	if !ok {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "policy must be backlog or next_sprint")
		return
	}
	if policy == "next_sprint" && req.NextSprintID == nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "next_sprint_id is required for the next_sprint policy")
		return
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	tx, err := h.store.DB().Begin(ctx)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to close sprint")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var name, status string
	err = tx.QueryRow(ctx, `
		SELECT name, status FROM sprints WHERE id=$1 AND project_id=$2 FOR UPDATE`, sprintID, projectID).
		Scan(&name, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Sprint not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint")
		return
	}
	if status != "active" {
		apiError(c, http.StatusConflict, "SPRINT_NOT_ACTIVE", "Only an active sprint can be closed")
		return
	}

	var nextSprintID *uuid.UUID
	if policy == "next_sprint" {
		if *req.NextSprintID == sprintID {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A sprint cannot carry work into itself")
			return
		}
		var nextStatus string
		err = tx.QueryRow(ctx, `
			SELECT status FROM sprints WHERE id=$1 AND project_id=$2 FOR UPDATE`, *req.NextSprintID, projectID).
			Scan(&nextStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			apiError(c, http.StatusNotFound, "NOT_FOUND", "Next sprint not found")
			return
		}
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load next sprint")
			return
		}
		if nextStatus != "planned" && nextStatus != "active" {
			apiError(c, http.StatusUnprocessableEntity, "SPRINT_NOT_OPEN", "Carry-over target must be planned or active")
			return
		}
		nextSprintID = req.NextSprintID
	}

	var movedItems int
	var movedPoints float64
	rows, err := tx.Query(ctx, `
		UPDATE work_items
		SET sprint_id=$3, updated_at=NOW()
		WHERE project_id=$1 AND sprint_id=$2 AND `+incompleteWorkItemPredicate+`
		RETURNING COALESCE(estimate_points, 0)`, projectID, sprintID, nextSprintID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to carry over work items")
		return
	}
	for rows.Next() {
		var points float64
		if err := rows.Scan(&points); err != nil {
			rows.Close()
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to carry over work items")
			return
		}
		movedItems++
		movedPoints += points
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to carry over work items")
		return
	}
	rows.Close()

	var revision int64
	var completedAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE sprints
		SET status='completed', completed_at=NOW(), completed_by=$3, close_policy=$4,
		    carry_over_count=$5, updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(6)+`
		RETURNING revision, completed_at`,
		sprintID, projectID, actorID(c), policy, movedItems, revisionValue(expected)).
		Scan(&revision, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(ctx)
		h.revisionFailure(c, "sprint", "sprints", sprintID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to close sprint")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to close sprint")
		return
	}

	movedPoints = round2(movedPoints)
	summary := fmt.Sprintf("Closed sprint %s and moved %d item(s) to %s", name, movedItems, closePolicyLabel(policy))
	h.recordActivity(ctx, projectID, actorID(c), "sprint", &sprintID, "closed", summary, map[string]interface{}{
		"policy": policy, "moved_items": movedItems, "moved_points": movedPoints, "next_sprint_id": nextSprintID,
	})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "sprint.close", TargetType: "sprint", TargetID: sprintID.String(), Result: "success",
		Reason: strings.TrimSpace(req.Note),
		After: map[string]any{"name": name, "policy": policy, "moved_items": movedItems,
			"moved_points": movedPoints, "next_sprint_id": nextSprintID, "revision": revision},
	})
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{
		"id": sprintID, "revision": revision, "status": "completed",
		"policy": policy, "moved_items": movedItems, "moved_points": movedPoints,
		"next_sprint_id": nextSprintID, "completed_at": completedAt,
	})
}

func closePolicyLabel(policy string) string {
	if policy == "next_sprint" {
		return "the next sprint"
	}
	return "the backlog"
}

// ---------------------------------------------------------------------------
// Sprint metrics
// ---------------------------------------------------------------------------

type sprintMetrics struct {
	SprintID           uuid.UUID  `json:"sprint_id"`
	Name               string     `json:"name"`
	Status             string     `json:"status"`
	TimeZone           string     `json:"time_zone"`
	StartDate          *time.Time `json:"start_date"`
	EndDate            *time.Time `json:"end_date"`
	Capacity           *float64   `json:"capacity"`
	CommittedPoints    float64    `json:"committed_points"`
	CompletedPoints    float64    `json:"completed_points"`
	RemainingPoints    float64    `json:"remaining_points"`
	CompletedItems     int        `json:"completed_items"`
	TotalItems         int        `json:"total_items"`
	RemainingItems     int        `json:"remaining_items"`
	VelocityPoints     *float64   `json:"velocity_points"`
	VelocitySampleSize int        `json:"velocity_sample_size"`
	CapacityUsed       *float64   `json:"capacity_used_percent"`
	DaysTotal          int        `json:"days_total"`
	DaysElapsed        int        `json:"days_elapsed"`
	DaysRemaining      int        `json:"days_remaining"`
}

// GetSprintMetrics handles GET /projects/:id/sprints/:sprintId/metrics.
//
// Definitions:
//   - committed points: estimate_points of every work item currently assigned
//     to the sprint, regardless of status.
//   - completed points: estimate_points of done, closed or verified work items.
//   - velocity: mean completed points of the most recent three completed
//     sprints in the project, including this one when it is completed.
//   - dates are calendar days in the requested IANA time zone; UTC is the
//     default.
func (h *Handler) GetSprintMetrics(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	sprintID, ok := parseParamID(c, "sprintId")
	if !ok {
		return
	}
	zoneName := strings.TrimSpace(c.Query("time_zone"))
	if zoneName == "" {
		zoneName = "UTC"
	}
	location, err := time.LoadLocation(zoneName)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "time_zone must be a valid IANA time zone")
		return
	}

	ctx := c.Request.Context()
	metrics := sprintMetrics{SprintID: sprintID, TimeZone: zoneName}
	err = h.store.DB().QueryRow(ctx, `
		SELECT name, status, start_date, end_date, capacity
		FROM sprints WHERE id=$1 AND project_id=$2`, sprintID, projectID).
		Scan(&metrics.Name, &metrics.Status, &metrics.StartDate, &metrics.EndDate, &metrics.Capacity)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Sprint not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint")
		return
	}

	rows, err := h.store.DB().Query(ctx, `
		SELECT status, estimate_points FROM work_items
		WHERE project_id=$1 AND sprint_id=$2`, projectID, sprintID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint work")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var points *float64
		if err := rows.Scan(&status, &points); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint work")
			return
		}
		metrics.TotalItems++
		if points != nil {
			metrics.CommittedPoints += *points
		}
		if isCompletedWorkItemStatus(status) {
			metrics.CompletedItems++
			if points != nil {
				metrics.CompletedPoints += *points
			}
		}
	}
	if err := rows.Err(); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint work")
		return
	}
	metrics.RemainingItems = metrics.TotalItems - metrics.CompletedItems
	metrics.CommittedPoints = round2(metrics.CommittedPoints)
	metrics.CompletedPoints = round2(metrics.CompletedPoints)
	metrics.RemainingPoints = round2(metrics.CommittedPoints - metrics.CompletedPoints)
	if metrics.Capacity != nil && *metrics.Capacity > 0 {
		used := round2(metrics.CompletedPoints / *metrics.Capacity * 100)
		metrics.CapacityUsed = &used
	}

	// Velocity from the three most recent completed sprints, including this
	// sprint when it has completed.
	velocityRows, err := h.store.DB().Query(ctx, `
		SELECT COALESCE(SUM(w.estimate_points), 0)
		FROM sprints s
		LEFT JOIN work_items w ON w.sprint_id = s.id
			AND w.status IN ('done', 'closed', 'verified')
		WHERE s.project_id=$1 AND s.status='completed' AND s.id<>$2
		GROUP BY s.id, s.completed_at
		ORDER BY s.completed_at DESC NULLS LAST
		LIMIT 3`, projectID, sprintID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint velocity")
		return
	}
	samples := []float64{}
	if metrics.Status == "completed" {
		samples = append(samples, metrics.CompletedPoints)
	}
	for velocityRows.Next() {
		var points float64
		if err := velocityRows.Scan(&points); err != nil {
			velocityRows.Close()
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint velocity")
			return
		}
		if len(samples) < 3 {
			samples = append(samples, round2(points))
		}
	}
	if err := velocityRows.Err(); err != nil {
		velocityRows.Close()
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load sprint velocity")
		return
	}
	velocityRows.Close()
	metrics.VelocitySampleSize = len(samples)
	if len(samples) > 0 {
		total := 0.0
		for _, points := range samples {
			total += points
		}
		velocity := round2(total / float64(len(samples)))
		metrics.VelocityPoints = &velocity
	}
	metrics.DaysTotal, metrics.DaysElapsed, metrics.DaysRemaining = sprintDayCounts(metrics.StartDate, metrics.EndDate, time.Now(), location)
	c.JSON(http.StatusOK, metrics)
}

func sprintDayCounts(start, end *time.Time, now time.Time, location *time.Location) (total, elapsed, remaining int) {
	if start == nil || end == nil || end.Before(*start) {
		return 0, 0, 0
	}
	// Sprint dates are calendar dates (PostgreSQL DATE), so compare their UTC
	// date components. "Today" is the current calendar date in the requested
	// IANA zone.
	startDate := dateOnly(*start)
	endDate := dateOnly(*end)
	total = int(endDate.Sub(startDate).Hours()/24) + 1
	today := dateOnly(now.In(location))
	switch {
	case today.Before(startDate):
		elapsed = 0
	case today.After(endDate):
		elapsed = total
	default:
		elapsed = int(today.Sub(startDate).Hours()/24) + 1
	}
	remaining = total - elapsed
	if remaining < 0 {
		remaining = 0
	}
	return total, elapsed, remaining
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}

func sprintOverlapsTx(ctx context.Context, tx pgx.Tx, projectID, exclude uuid.UUID, start, end time.Time) (bool, error) {
	var overlap bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sprints
			WHERE project_id=$1 AND status='active' AND id<>$2
			  AND daterange(start_date, end_date, '[]') && daterange($3::date, $4::date, '[]')
		)`, projectID, exclude, start, end).Scan(&overlap)
	return overlap, err
}

func (h *Handler) sprintOverlaps(ctx context.Context, projectID, exclude uuid.UUID, start, end time.Time) (bool, error) {
	var overlap bool
	err := h.store.DB().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sprints
			WHERE project_id=$1 AND status='active' AND id<>$2
			  AND daterange(start_date, end_date, '[]') && daterange($3::date, $4::date, '[]')
		)`, projectID, exclude, start, end).Scan(&overlap)
	return overlap, err
}

// ---------------------------------------------------------------------------
// Release freeze, exceptions and sign-off
// ---------------------------------------------------------------------------

type releaseFreezeRequest struct {
	Reason          string `json:"reason"`
	ExpectedVersion *int64 `json:"expected_version"`
}

// FreezeRelease handles POST /projects/:id/releases/:releaseId/freeze.
func (h *Handler) FreezeRelease(c *gin.Context) {
	if !canModerate(c) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Only a project owner or maintainer can freeze a release")
		return
	}
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	releaseID, ok := parseParamID(c, "releaseId")
	if !ok {
		return
	}
	var req releaseFreezeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" || len(req.Reason) > 1000 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A freeze reason of at most 1000 characters is required")
		return
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var version, status string
	var frozenAt *time.Time
	err := h.store.DB().QueryRow(ctx, `
		SELECT version, status, frozen_at FROM releases WHERE id=$1 AND project_id=$2`, releaseID, projectID).
		Scan(&version, &status, &frozenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Release not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load release")
		return
	}
	if status == "released" || status == "cancelled" {
		apiError(c, http.StatusConflict, "RELEASE_STATE", "A released or cancelled release cannot be frozen")
		return
	}
	if frozenAt != nil {
		apiError(c, http.StatusConflict, "RELEASE_ALREADY_FROZEN", "Release is already frozen")
		return
	}
	var revision int64
	var frozen time.Time
	err = h.store.DB().QueryRow(ctx, `
		UPDATE releases
		SET frozen_at=NOW(), frozen_by=$3, freeze_reason=$4, updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(5)+`
		RETURNING revision, frozen_at`,
		releaseID, projectID, actorID(c), req.Reason, revisionValue(expected)).Scan(&revision, &frozen)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "release", "releases", releaseID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to freeze release")
		return
	}
	h.recordActivity(ctx, projectID, actorID(c), "release", &releaseID, "frozen", "Froze release "+version, map[string]interface{}{"reason": req.Reason})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "release.freeze", TargetType: "release", TargetID: releaseID.String(), Result: "success",
		Reason: req.Reason, After: map[string]any{"version": version, "revision": revision},
	})
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": releaseID, "revision": revision, "frozen_at": frozen, "freeze_reason": req.Reason})
}

// UnfreezeRelease handles DELETE /projects/:id/releases/:releaseId/freeze.
func (h *Handler) UnfreezeRelease(c *gin.Context) {
	if !canModerate(c) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Only a project owner or maintainer can unfreeze a release")
		return
	}
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	releaseID, ok := parseParamID(c, "releaseId")
	if !ok {
		return
	}
	expected, ok := revisionFromRequest(c, nil)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var revision int64
	var version string
	err := h.store.DB().QueryRow(ctx, `
		UPDATE releases
		SET frozen_at=NULL, frozen_by=NULL, freeze_reason='', updated_at=NOW()
		WHERE id=$1 AND project_id=$2 AND frozen_at IS NOT NULL`+revisionClause(3)+`
		RETURNING revision, version`,
		releaseID, projectID, revisionValue(expected)).Scan(&revision, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		var frozenAt *time.Time
		checkErr := h.store.DB().QueryRow(ctx, `SELECT frozen_at FROM releases WHERE id=$1 AND project_id=$2`, releaseID, projectID).Scan(&frozenAt)
		if checkErr == nil && frozenAt == nil {
			apiError(c, http.StatusConflict, "RELEASE_NOT_FROZEN", "Release is not frozen")
			return
		}
		h.revisionFailure(c, "release", "releases", releaseID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to unfreeze release")
		return
	}
	h.recordActivity(ctx, projectID, actorID(c), "release", &releaseID, "unfrozen", "Unfroze release "+version, nil)
	h.recordAudit(c, projectID, audit.Entry{
		Action: "release.unfreeze", TargetType: "release", TargetID: releaseID.String(), Result: "success",
		After: map[string]any{"version": version, "revision": revision},
	})
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": releaseID, "revision": revision, "frozen_at": nil})
}

type releaseExceptionRequest struct {
	EntityType string     `json:"entity_type"`
	EntityID   *uuid.UUID `json:"entity_id"`
	Reason     string     `json:"reason"`
}

type releaseScopeException struct {
	ID         uuid.UUID  `json:"id"`
	ReleaseID  uuid.UUID  `json:"release_id"`
	EntityType string     `json:"entity_type"`
	EntityID   *uuid.UUID `json:"entity_id"`
	Reason     string     `json:"reason"`
	CreatedBy  *uuid.UUID `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

func releaseScopeTable(entityType string) (string, bool) {
	switch entityType {
	case "work_item":
		return "work_items", true
	case "feature":
		return "features", true
	case "epic":
		return "epics", true
	}
	return "", false
}

// CreateReleaseException handles POST /projects/:id/releases/:releaseId/exceptions.
func (h *Handler) CreateReleaseException(c *gin.Context) {
	if !canModerate(c) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Only a project owner or maintainer can record a scope exception")
		return
	}
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	releaseID, ok := parseParamID(c, "releaseId")
	if !ok {
		return
	}
	var req releaseExceptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.EntityType = strings.TrimSpace(req.EntityType)
	if _, ok := releaseScopeTable(req.EntityType); !ok {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "entity_type must be work_item, feature or epic")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" || len(req.Reason) > 2000 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "An exception reason of at most 2000 characters is required")
		return
	}
	ctx := c.Request.Context()
	var frozenAt *time.Time
	var version string
	err := h.store.DB().QueryRow(ctx, `
		SELECT version, frozen_at FROM releases WHERE id=$1 AND project_id=$2`, releaseID, projectID).
		Scan(&version, &frozenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Release not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load release")
		return
	}
	if frozenAt == nil {
		apiError(c, http.StatusConflict, "RELEASE_NOT_FROZEN", "Scope exceptions apply only while a release is frozen")
		return
	}
	if req.EntityID != nil {
		table, _ := releaseScopeTable(req.EntityType)
		if err := h.ensureBelongs(ctx, table, projectID, *req.EntityID); err != nil {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Scope entity does not belong to this project")
			return
		}
	}
	var exception releaseScopeException
	exception.ReleaseID = releaseID
	exception.EntityType = req.EntityType
	exception.EntityID = req.EntityID
	exception.Reason = req.Reason
	err = h.store.DB().QueryRow(ctx, `
		INSERT INTO release_scope_exceptions (project_id, release_id, entity_type, entity_id, reason, created_by)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, created_by, created_at`,
		projectID, releaseID, req.EntityType, req.EntityID, req.Reason, actorID(c)).
		Scan(&exception.ID, &exception.CreatedBy, &exception.CreatedAt)
	if err != nil {
		if pgErrCode(err) == "23505" {
			apiError(c, http.StatusConflict, "RELEASE_EXCEPTION_EXISTS", "An exception for this scope already exists")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to record scope exception")
		return
	}
	h.recordActivity(ctx, projectID, actorID(c), "release", &releaseID, "scope_exception", "Recorded a scope exception for release "+version, map[string]interface{}{"entity_type": req.EntityType, "entity_id": req.EntityID, "reason": req.Reason})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "release.scope_exception", TargetType: "release", TargetID: releaseID.String(), Result: "success",
		Reason: req.Reason, After: map[string]any{"version": version, "entity_type": req.EntityType, "entity_id": req.EntityID},
	})
	c.JSON(http.StatusCreated, exception)
}

// ListReleaseExceptions handles GET /projects/:id/releases/:releaseId/exceptions.
func (h *Handler) ListReleaseExceptions(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	releaseID, ok := parseParamID(c, "releaseId")
	if !ok {
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "releases", projectID, releaseID); err != nil {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Release not found")
		return
	}
	rows, err := h.store.DB().Query(c.Request.Context(), `
		SELECT id, release_id, entity_type, entity_id, reason, created_by, created_at
		FROM release_scope_exceptions
		WHERE project_id=$1 AND release_id=$2
		ORDER BY created_at DESC`, projectID, releaseID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list scope exceptions")
		return
	}
	defer rows.Close()
	exceptions := []releaseScopeException{}
	for rows.Next() {
		var exception releaseScopeException
		if err := rows.Scan(&exception.ID, &exception.ReleaseID, &exception.EntityType, &exception.EntityID,
			&exception.Reason, &exception.CreatedBy, &exception.CreatedAt); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list scope exceptions")
			return
		}
		exceptions = append(exceptions, exception)
	}
	if err := rows.Err(); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list scope exceptions")
		return
	}
	c.JSON(http.StatusOK, gin.H{"exceptions": exceptions})
}

// releaseExceptionExists checks an entity-specific exception first, then a
// type-wide exception recorded with a NULL entity_id.
func (h *Handler) releaseExceptionExists(ctx context.Context, releaseID uuid.UUID, entityType string, entityID *uuid.UUID) (bool, error) {
	var exists bool
	err := h.store.DB().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM release_scope_exceptions
			WHERE release_id=$1 AND entity_type=$2
			  AND (entity_id IS NULL OR ($3::uuid IS NOT NULL AND entity_id=$3))
		)`, releaseID, entityType, entityID).Scan(&exists)
	return exists, err
}

// enforceReleaseFreeze blocks a scope change when either the source or target
// release is frozen without an authorized exception.
func (h *Handler) enforceReleaseFreeze(c *gin.Context, projectID uuid.UUID, entityType string, entityID *uuid.UUID, current, next *uuid.UUID) bool {
	if sameRelease(current, next) {
		return true
	}
	for _, releaseID := range []*uuid.UUID{current, next} {
		if releaseID == nil {
			continue
		}
		var name, version string
		var frozenAt *time.Time
		err := h.store.DB().QueryRow(c.Request.Context(), `
			SELECT name, version, frozen_at FROM releases WHERE id=$1 AND project_id=$2`, *releaseID, projectID).
			Scan(&name, &version, &frozenAt)
		if errors.Is(err, pgx.ErrNoRows) {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Release does not belong to this project")
			return false
		}
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load release")
			return false
		}
		if frozenAt == nil {
			continue
		}
		exempt, err := h.releaseExceptionExists(c.Request.Context(), *releaseID, entityType, entityID)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to check release scope exception")
			return false
		}
		if !exempt {
			apiError(c, http.StatusConflict, "RELEASE_FROZEN",
				fmt.Sprintf("Release %s %s is frozen; record a scope exception before changing its scope", name, version))
			return false
		}
	}
	return true
}

func sameRelease(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// currentScopeRelease loads the release currently attached to an entity.
func (h *Handler) currentScopeRelease(ctx context.Context, table, column string, projectID, entityID uuid.UUID) (*uuid.UUID, error) {
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE id=$1 AND project_id=$2`, column, table)
	var releaseID *uuid.UUID
	err := h.store.DB().QueryRow(ctx, query, entityID, projectID).Scan(&releaseID)
	return releaseID, err
}

type releaseSignOffRequest struct {
	Evidence        string `json:"evidence"`
	ExpectedVersion *int64 `json:"expected_version"`
}

// SignOffRelease handles POST /projects/:id/releases/:releaseId/sign-off.
func (h *Handler) SignOffRelease(c *gin.Context) {
	if !canModerate(c) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Only a project owner or maintainer can sign off a release")
		return
	}
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	releaseID, ok := parseParamID(c, "releaseId")
	if !ok {
		return
	}
	var req releaseSignOffRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Evidence = strings.TrimSpace(req.Evidence)
	if len(req.Evidence) < 10 || len(req.Evidence) > 5000 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Sign-off evidence must be between 10 and 5000 characters")
		return
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var status, version string
	err := h.store.DB().QueryRow(ctx, `
		SELECT status, version FROM releases WHERE id=$1 AND project_id=$2`, releaseID, projectID).
		Scan(&status, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Release not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load release")
		return
	}
	if status == "released" || status == "cancelled" {
		apiError(c, http.StatusConflict, "RELEASE_STATE", "A released or cancelled release cannot be signed off")
		return
	}
	workspace, err := h.store.Load(ctx, projectID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to evaluate release readiness")
		return
	}
	report := buildReports(workspace)
	var readiness *releaseReadiness
	for i := range report.Releases {
		if report.Releases[i].ReleaseID == releaseID {
			readiness = &report.Releases[i]
			break
		}
	}
	if readiness == nil {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Release not found")
		return
	}
	if !readiness.Ready {
		c.JSON(http.StatusConflict, gin.H{
			"error":     errorBody{Code: "RELEASE_NOT_READY", Message: "Release readiness gates are not satisfied"},
			"readiness": readiness,
		})
		return
	}
	var revision int64
	var signedAt time.Time
	err = h.store.DB().QueryRow(ctx, `
		UPDATE releases
		SET sign_off_evidence=$3, signed_off_at=NOW(), signed_off_by=$4, updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(5)+`
		RETURNING revision, signed_off_at`,
		releaseID, projectID, req.Evidence, actorID(c), revisionValue(expected)).Scan(&revision, &signedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "release", "releases", releaseID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to sign off release")
		return
	}
	digest := sha256.Sum256([]byte(req.Evidence))
	h.recordActivity(ctx, projectID, actorID(c), "release", &releaseID, "signed_off", "Signed off release "+version, map[string]interface{}{"evidence_sha256": hex.EncodeToString(digest[:])})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "release.sign_off", TargetType: "release", TargetID: releaseID.String(), Result: "success",
		After: map[string]any{"version": version, "evidence_sha256": hex.EncodeToString(digest[:]), "revision": revision},
	})
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{
		"id": releaseID, "revision": revision, "signed_off_at": signedAt,
		"signed_off_by": actorID(c), "sign_off_evidence": req.Evidence,
	})
}

// pgErrCode extracts a PostgreSQL SQLSTATE from a pgx error.
func pgErrCode(err error) string {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState()
	}
	return ""
}
