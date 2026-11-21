package workspace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Test plans
// ---------------------------------------------------------------------------

type testPlanRequest struct {
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	OwnerID         *uuid.UUID `json:"owner_id"`
	StartDate       *string    `json:"start_date"`
	EndDate         *string    `json:"end_date"`
	ExpectedVersion *int64     `json:"expected_version"`
}

var validTestPlanStatuses = map[string]bool{"draft": true, "active": true, "archived": true}

func validTestPlanStatus(status string) bool { return validTestPlanStatuses[status] }

// validTestPlanTransition allows activating a draft, parking an active plan
// back to draft, and archiving either. Archived is terminal so a historical
// plan cannot silently re-enter an active cycle.
func validTestPlanTransition(from, to string) bool {
	if from == to {
		return true
	}
	if from == "archived" {
		return false
	}
	return to == "draft" || to == "active" || to == "archived"
}

func parseOptionalDate(value *string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	return parseTimePtr(&trimmed)
}

// validateTestPlan validates shared create/update fields.
func (h *Handler) validateTestPlan(c *gin.Context, projectID uuid.UUID, req *testPlanRequest) bool {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 255 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Test plan name is required and must be at most 255 characters")
		return false
	}
	if req.Status == "" {
		req.Status = "draft"
	}
	if !validTestPlanStatus(req.Status) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Test plan status must be draft, active or archived")
		return false
	}
	start, err := parseOptionalDate(req.StartDate)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "start_date must be YYYY-MM-DD")
		return false
	}
	end, err := parseOptionalDate(req.EndDate)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "end_date must be YYYY-MM-DD")
		return false
	}
	if start != nil && end != nil && end.Before(*start) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "end_date must not be before start_date")
		return false
	}
	if req.OwnerID != nil {
		var member bool
		if err := h.store.DB().QueryRow(c.Request.Context(),
			`SELECT EXISTS (SELECT 1 FROM project_members WHERE project_id=$1 AND user_id=$2)`,
			projectID, *req.OwnerID).Scan(&member); err != nil || !member {
			apiError(c, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Plan owner must be a project member")
			return false
		}
	}
	return true
}

func (h *Handler) CreateTestPlan(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req testPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if !h.validateTestPlan(c, projectID, &req) {
		return
	}
	if req.Status == "archived" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A new test plan cannot start archived")
		return
	}
	start, _ := parseOptionalDate(req.StartDate)
	end, _ := parseOptionalDate(req.EndDate)
	var id uuid.UUID
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO test_plans (project_id, name, description, status, owner_id, start_date, end_date, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, revision`,
		projectID, req.Name, req.Description, req.Status, req.OwnerID, start, end,
		actorID(c)).Scan(&id, &revision)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create test plan")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_plan", &id, "created", "Created test plan "+req.Name, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusCreated, gin.H{"id": id, "revision": revision, "status": req.Status})
}

func (h *Handler) UpdateTestPlan(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	planID, ok := parseParamID(c, "planId")
	if !ok {
		return
	}
	var req testPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if !h.validateTestPlan(c, projectID, &req) {
		return
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	tx, err := h.store.DB().Begin(ctx)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var currentStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM test_plans WHERE id=$1 AND project_id=$2 FOR UPDATE`,
		planID, projectID).Scan(&currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test plan not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test plan")
		return
	}
	if !validTestPlanTransition(currentStatus, req.Status) {
		apiError(c, http.StatusConflict, "PLAN_STATE", "An archived test plan cannot be reactivated")
		return
	}
	start, _ := parseOptionalDate(req.StartDate)
	end, _ := parseOptionalDate(req.EndDate)
	var revision int64
	err = tx.QueryRow(ctx, `
		UPDATE test_plans SET name=$3, description=$4, status=$5, owner_id=$6,
		start_date=$7, end_date=$8, updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(9)+` RETURNING revision`,
		planID, projectID, req.Name, req.Description, req.Status, req.OwnerID, start, end,
		revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "test plan", "test_plans", planID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update test plan")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit test plan")
		return
	}
	h.recordActivity(ctx, projectID, actorID(c), "test_plan", &planID, "updated",
		"Updated test plan "+req.Name, map[string]interface{}{"status": req.Status})
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": planID, "revision": revision, "status": req.Status})
}

// ---------------------------------------------------------------------------
// Test suites
// ---------------------------------------------------------------------------

type testSuiteRequest struct {
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	AssigneeID      *uuid.UUID `json:"assignee_id"`
	Position        *int       `json:"position"`
	Archived        *bool      `json:"archived"`
	ExpectedVersion *int64     `json:"expected_version"`
}

func (h *Handler) validateSuiteAssignee(c *gin.Context, projectID uuid.UUID, assigneeID *uuid.UUID) bool {
	if assigneeID == nil {
		return true
	}
	var member bool
	if err := h.store.DB().QueryRow(c.Request.Context(),
		`SELECT EXISTS (SELECT 1 FROM project_members WHERE project_id=$1 AND user_id=$2)`,
		projectID, *assigneeID).Scan(&member); err != nil || !member {
		apiError(c, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Suite assignee must be a project member")
		return false
	}
	return true
}

// testSuiteRevisionFailure resolves a failed suite update. Suites are scoped
// through their plan rather than a project_id column, so the generic
// currentRevision lookup cannot serve this table.
func (h *Handler) testSuiteRevisionFailure(c *gin.Context, suiteID, planID, projectID uuid.UUID) {
	var current int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT s.revision FROM test_suites s JOIN test_plans p ON p.id = s.plan_id
		WHERE s.id=$1 AND s.plan_id=$2 AND p.project_id=$3`,
		suiteID, planID, projectID).Scan(&current)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test suite not found")
	case err != nil:
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read the current test suite revision")
	default:
		writeRevisionConflict(c, "test suite", current)
	}
}

func (h *Handler) CreateTestSuite(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	planID, ok := parseParamID(c, "planId")
	if !ok {
		return
	}
	var req testSuiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 255 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Suite name is required and must be at most 255 characters")
		return
	}
	if !h.validateSuiteAssignee(c, projectID, req.AssigneeID) {
		return
	}
	var planStatus string
	err := h.store.DB().QueryRow(c.Request.Context(),
		`SELECT status FROM test_plans WHERE id=$1 AND project_id=$2`, planID, projectID).Scan(&planStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test plan not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test plan")
		return
	}
	if planStatus == "archived" {
		apiError(c, http.StatusConflict, "PLAN_STATE", "An archived test plan cannot accept new suites")
		return
	}
	position := 0
	if req.Position == nil {
		_ = h.store.DB().QueryRow(c.Request.Context(),
			`SELECT COALESCE(MAX(position), -1) + 1 FROM test_suites WHERE plan_id=$1`, planID).Scan(&position)
	} else {
		position = *req.Position
	}
	var id uuid.UUID
	var revision int64
	err = h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO test_suites (plan_id, name, description, assignee_id, position)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, revision`,
		planID, req.Name, req.Description, req.AssigneeID, position).Scan(&id, &revision)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create test suite")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_suite", &id, "created", "Created test suite "+req.Name, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusCreated, gin.H{"id": id, "plan_id": planID, "revision": revision, "position": position})
}

func (h *Handler) UpdateTestSuite(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	planID, ok := parseParamID(c, "planId")
	if !ok {
		return
	}
	suiteID, ok := parseParamID(c, "suiteId")
	if !ok {
		return
	}
	var req testSuiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 255 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Suite name is required and must be at most 255 characters")
		return
	}
	if !h.validateSuiteAssignee(c, projectID, req.AssigneeID) {
		return
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	archived := req.Archived != nil && *req.Archived
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		UPDATE test_suites s SET name=$3, description=$4, assignee_id=$5,
		position=COALESCE($6, s.position),
		archived_at=CASE WHEN $7 THEN COALESCE(s.archived_at, NOW()) ELSE NULL END,
		updated_at=NOW()
		WHERE s.id=$1 AND s.plan_id=$2
		  AND EXISTS (SELECT 1 FROM test_plans p WHERE p.id=s.plan_id AND p.project_id=$8)`+revisionClause(9)+`
		RETURNING s.revision`,
		suiteID, planID, req.Name, req.Description, req.AssigneeID, req.Position, archived,
		projectID, revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.testSuiteRevisionFailure(c, suiteID, planID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update test suite")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_suite", &suiteID, "updated", "Updated test suite "+req.Name, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": suiteID, "plan_id": planID, "revision": revision})
}

type suiteOrderRequest struct {
	SuiteIDs        []uuid.UUID `json:"suite_ids"`
	ExpectedVersion *int64      `json:"expected_version"`
}

// ReorderTestSuites rewrites every non-archived suite position in one
// transaction. The request must list the plan's complete active suite set so a
// stale cart cannot reorder a partial view.
func (h *Handler) ReorderTestSuites(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	planID, ok := parseParamID(c, "planId")
	if !ok {
		return
	}
	var req suiteOrderRequest
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
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var planRevision int64
	err = tx.QueryRow(ctx, `SELECT revision FROM test_plans WHERE id=$1 AND project_id=$2 FOR UPDATE`,
		planID, projectID).Scan(&planRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test plan not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test plan")
		return
	}
	if !expected.Any && expected.Expected != planRevision {
		writeRevisionConflict(c, "test plan", planRevision)
		return
	}

	var planStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM test_plans WHERE id=$1`, planID).Scan(&planStatus); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test plan")
		return
	}
	if planStatus == "archived" {
		apiError(c, http.StatusConflict, "PLAN_STATE", "An archived test plan cannot be reordered")
		return
	}

	rows, err := tx.Query(ctx, `SELECT id FROM test_suites WHERE plan_id=$1 AND archived_at IS NULL`, planID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test suites")
		return
	}
	existing := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test suites")
			return
		}
		existing[id] = true
	}
	rows.Close()
	if len(req.SuiteIDs) != len(existing) {
		apiError(c, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "suite_ids must list every active suite in the plan")
		return
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range req.SuiteIDs {
		if !existing[id] || seen[id] {
			apiError(c, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "suite_ids contains an unknown or duplicate suite")
			return
		}
		seen[id] = true
	}
	for index, id := range req.SuiteIDs {
		if _, err := tx.Exec(ctx, `UPDATE test_suites SET position=$2, updated_at=NOW() WHERE id=$1 AND plan_id=$3`,
			id, index, planID); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to reorder test suites")
			return
		}
	}
	var revision int64
	if err := tx.QueryRow(ctx, `UPDATE test_plans SET updated_at=NOW() WHERE id=$1 RETURNING revision`, planID).Scan(&revision); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to reorder test suites")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit test suite order")
		return
	}
	h.recordActivity(ctx, projectID, actorID(c), "test_plan", &planID, "reordered", "Reordered test suites", nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": planID, "revision": revision})
}

// ---------------------------------------------------------------------------
// Suite membership
// ---------------------------------------------------------------------------

type suiteCasesRequest struct {
	TestCaseIDs     []uuid.UUID     `json:"test_case_ids"`
	Filter          *testCaseFilter `json:"filter"`
	CSV             string          `json:"csv"`
	ExpectedVersion *int64          `json:"expected_version"`
}

// testCaseFilter is shared by suite membership and run creation. Empty fields
// are ignored; every supplied field narrows the project-scoped result set.
type testCaseFilter struct {
	FeatureID *uuid.UUID `json:"feature_id"`
	Type      string     `json:"type"`
	Priority  string     `json:"priority"`
	Status    string     `json:"status"`
}

type unitTestCase struct {
	ID       uuid.UUID
	Title    string
	Revision int64
}

func dedupeCaseIDs(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// validateTestCaseFilter checks the shared filter fields before they reach SQL.
func (h *Handler) validateTestCaseFilter(c *gin.Context, projectID uuid.UUID, filter *testCaseFilter) bool {
	if filter == nil {
		return true
	}
	if filter.Type != "" && filter.Type != "manual" && filter.Type != "automated" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "filter.type must be manual or automated")
		return false
	}
	if filter.Priority != "" && !validPriority(filter.Priority) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "filter.priority is invalid")
		return false
	}
	if filter.Status != "" && filter.Status != "draft" && filter.Status != "ready" && filter.Status != "deprecated" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "filter.status is invalid")
		return false
	}
	if filter.FeatureID != nil {
		if err := h.ensureBelongs(c.Request.Context(), "features", projectID, *filter.FeatureID); err != nil {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "filter.feature_id does not belong to this project")
			return false
		}
	}
	return true
}

// resolveFilteredCases applies the shared test-case filter. Archived plans are
// rejected by the caller; deprecated cases remain selectable so a historical
// suite can still be executed intentionally.
func (h *Handler) resolveFilteredCases(ctx context.Context, projectID uuid.UUID, filter *testCaseFilter) ([]unitTestCase, error) {
	query := `SELECT id, title, revision FROM test_cases WHERE project_id=$1`
	args := []interface{}{projectID}
	if filter != nil {
		if filter.FeatureID != nil {
			args = append(args, *filter.FeatureID)
			query += fmt.Sprintf(" AND feature_id=$%d", len(args))
		}
		if filter.Type != "" {
			args = append(args, filter.Type)
			query += fmt.Sprintf(" AND type=$%d", len(args))
		}
		if filter.Priority != "" {
			args = append(args, filter.Priority)
			query += fmt.Sprintf(" AND priority=$%d", len(args))
		}
		if filter.Status != "" {
			args = append(args, filter.Status)
			query += fmt.Sprintf(" AND status=$%d", len(args))
		}
	}
	query += " ORDER BY title"
	rows, err := h.store.DB().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cases := []unitTestCase{}
	for rows.Next() {
		var tc unitTestCase
		if err := rows.Scan(&tc.ID, &tc.Title, &tc.Revision); err != nil {
			return nil, err
		}
		cases = append(cases, tc)
	}
	return cases, rows.Err()
}

// resolveCSVCaseTitles maps one title per line to project cases. A header row
// named title/case/name is ignored. Any unknown title rejects the whole
// request; duplicates are harmless because membership conflicts are skipped.
func (h *Handler) resolveCSVCaseTitles(ctx context.Context, projectID uuid.UUID, csv string) ([]unitTestCase, []string) {
	lines := strings.Split(strings.ReplaceAll(csv, "\r\n", "\n"), "\n")
	resolved := []unitTestCase{}
	problems := []string{}
	for index, line := range lines {
		title := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), `"`))
		if title == "" {
			continue
		}
		if index == 0 && (strings.EqualFold(title, "title") || strings.EqualFold(title, "case") || strings.EqualFold(title, "name")) {
			continue
		}
		var tc unitTestCase
		err := h.store.DB().QueryRow(ctx, `
			SELECT id, title, revision FROM test_cases
			WHERE project_id=$1 AND LOWER(title)=LOWER($2)
			ORDER BY created_at LIMIT 1`, projectID, title).Scan(&tc.ID, &tc.Title, &tc.Revision)
		if errors.Is(err, pgx.ErrNoRows) {
			problems = append(problems, fmt.Sprintf("line %d: no test case titled %q", index+1, title))
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %d: failed to resolve %q", index+1, title))
			continue
		}
		resolved = append(resolved, tc)
	}
	return resolved, problems
}

func (h *Handler) AddSuiteCases(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	planID, ok := parseParamID(c, "planId")
	if !ok {
		return
	}
	suiteID, ok := parseParamID(c, "suiteId")
	if !ok {
		return
	}
	var req suiteCasesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	var planStatus string
	var suiteArchivedAt *time.Time
	err := h.store.DB().QueryRow(ctx, `
		SELECT p.status, s.archived_at FROM test_suites s JOIN test_plans p ON p.id=s.plan_id
		WHERE s.id=$1 AND s.plan_id=$2 AND p.project_id=$3`, suiteID, planID, projectID).Scan(&planStatus, &suiteArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test suite not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test suite")
		return
	}
	if planStatus == "archived" {
		apiError(c, http.StatusConflict, "PLAN_STATE", "An archived test plan cannot accept cases")
		return
	}
	if suiteArchivedAt != nil {
		apiError(c, http.StatusConflict, "TEST_SUITE_ARCHIVED", "An archived test suite cannot accept cases")
		return
	}

	cases := []unitTestCase{}
	if len(req.TestCaseIDs) > 0 {
		ids := dedupeCaseIDs(req.TestCaseIDs)
		rows, err := h.store.DB().Query(ctx, `SELECT id, title, revision FROM test_cases WHERE project_id=$1 AND id = ANY($2)`,
			projectID, ids)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test cases")
			return
		}
		found := 0
		for rows.Next() {
			var tc unitTestCase
			if err := rows.Scan(&tc.ID, &tc.Title, &tc.Revision); err != nil {
				rows.Close()
				apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test cases")
				return
			}
			cases = append(cases, tc)
			found++
		}
		rows.Close()
		if found != len(ids) {
			apiError(c, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Every test_case_ids entry must belong to this project")
			return
		}
	}
	if req.Filter != nil {
		if !h.validateTestCaseFilter(c, projectID, req.Filter) {
			return
		}
		filtered, err := h.resolveFilteredCases(ctx, projectID, req.Filter)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to resolve test case filter")
			return
		}
		cases = append(cases, filtered...)
	}
	if strings.TrimSpace(req.CSV) != "" {
		resolved, problems := h.resolveCSVCaseTitles(ctx, projectID, req.CSV)
		if len(problems) > 0 {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", strings.Join(problems, "; "))
			return
		}
		cases = append(cases, resolved...)
	}
	if len(cases) == 0 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Select at least one test case, filter match or CSV row")
		return
	}

	seen := map[uuid.UUID]bool{}
	unique := make([]unitTestCase, 0, len(cases))
	for _, tc := range cases {
		if seen[tc.ID] {
			continue
		}
		seen[tc.ID] = true
		unique = append(unique, tc)
	}
	sort.SliceStable(unique, func(i, j int) bool { return unique[i].Title < unique[j].Title })

	tx, err := h.store.DB().Begin(ctx)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	var currentRevision int64
	err = tx.QueryRow(ctx, `SELECT revision FROM test_suites WHERE id=$1 AND plan_id=$2 FOR UPDATE`,
		suiteID, planID).Scan(&currentRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test suite not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test suite")
		return
	}
	if !expected.Any && expected.Expected != currentRevision {
		writeRevisionConflict(c, "test suite", currentRevision)
		return
	}
	var position int
	_ = tx.QueryRow(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM test_suite_cases WHERE suite_id=$1`, suiteID).Scan(&position)
	added := 0
	for _, tc := range unique {
		tag, err := tx.Exec(ctx, `
			INSERT INTO test_suite_cases (suite_id, test_case_id, position, added_by)
			VALUES ($1,$2,$3,$4) ON CONFLICT (suite_id, test_case_id) DO NOTHING`,
			suiteID, tc.ID, position, actorID(c))
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to add test case to suite")
			return
		}
		if tag.RowsAffected() > 0 {
			added++
			position++
		}
	}
	var revision int64
	if err := tx.QueryRow(ctx, `UPDATE test_suites SET updated_at=NOW() WHERE id=$1 RETURNING revision`, suiteID).Scan(&revision); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update test suite")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit suite cases")
		return
	}
	h.recordActivity(ctx, projectID, actorID(c), "test_suite", &suiteID, "cases_added",
		fmt.Sprintf("Added %d test cases to suite", added), map[string]interface{}{"added": added, "requested": len(unique)})
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{
		"id": suiteID, "revision": revision, "added": added,
		"skipped": len(unique) - added, "requested": len(unique),
	})
}

func (h *Handler) RemoveSuiteCase(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	planID, ok := parseParamID(c, "planId")
	if !ok {
		return
	}
	suiteID, ok := parseParamID(c, "suiteId")
	if !ok {
		return
	}
	caseID, ok := parseParamID(c, "caseId")
	if !ok {
		return
	}
	expected, ok := revisionFromRequest(c, nil)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	tx, err := h.store.DB().Begin(ctx)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	var currentRevision int64
	var planStatus string
	var suiteArchivedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT s.revision, p.status, s.archived_at FROM test_suites s JOIN test_plans p ON p.id=s.plan_id
		WHERE s.id=$1 AND s.plan_id=$2 AND p.project_id=$3 FOR UPDATE`,
		suiteID, planID, projectID).Scan(&currentRevision, &planStatus, &suiteArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test suite not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test suite")
		return
	}
	if !expected.Any && expected.Expected != currentRevision {
		writeRevisionConflict(c, "test suite", currentRevision)
		return
	}
	if planStatus == "archived" || suiteArchivedAt != nil {
		apiError(c, http.StatusConflict, "TEST_SUITE_ARCHIVED", "An archived test plan or suite cannot change membership")
		return
	}
	tag, err := tx.Exec(ctx, `DELETE FROM test_suite_cases WHERE suite_id=$1 AND test_case_id=$2`, suiteID, caseID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to remove test case from suite")
		return
	}
	if tag.RowsAffected() == 0 {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test case is not in this suite")
		return
	}
	var revision int64
	if err := tx.QueryRow(ctx, `UPDATE test_suites SET updated_at=NOW() WHERE id=$1 RETURNING revision`, suiteID).Scan(&revision); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update test suite")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit suite case removal")
		return
	}
	h.recordActivity(ctx, projectID, actorID(c), "test_suite", &suiteID, "case_removed", "Removed a test case from suite", nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": suiteID, "revision": revision, "removed": caseID})
}
