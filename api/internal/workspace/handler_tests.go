package workspace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Test cases
// ---------------------------------------------------------------------------

type testStepInput struct {
	Action         string `json:"action"`
	ExpectedResult string `json:"expected_result"`
	TestData       string `json:"test_data"`
}

type testCaseRequest struct {
	FeatureID       *uuid.UUID      `json:"feature_id"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	Preconditions   string          `json:"preconditions"`
	Type            string          `json:"type"`
	Priority        string          `json:"priority"`
	Status          string          `json:"status"`
	AutomationRef   string          `json:"automation_ref"`
	OwnerID         *uuid.UUID      `json:"owner_id"`
	Tags            []string        `json:"tags"`
	Steps           []testStepInput `json:"steps"`
	ExpectedVersion *int64          `json:"expected_version"`
}

func (h *Handler) CreateTestCase(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req testCaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || len(req.Title) > 255 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Test case title is required and must be at most 255 characters")
		return
	}
	if req.Type == "" {
		req.Type = "manual"
	}
	if req.Type != "manual" && req.Type != "automated" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Test case type must be manual or automated")
		return
	}
	if req.Status == "" {
		req.Status = "draft"
	}
	if req.Priority == "" {
		req.Priority = PriorityMedium
	}
	if req.FeatureID == nil {
		apiError(c, http.StatusUnprocessableEntity, "TRACEABILITY_ERROR", "A test case must link to a feature")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "features", projectID, *req.FeatureID); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Feature does not belong to this project")
		return
	}
	if len(req.Steps) == 0 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A test case must have at least one step")
		return
	}
	for _, step := range req.Steps {
		if strings.TrimSpace(step.Action) == "" || strings.TrimSpace(step.ExpectedResult) == "" {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Every test step needs an action and an expected result")
			return
		}
	}

	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(c.Request.Context())

	var id uuid.UUID
	var revision int64
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO test_cases (project_id, feature_id, title, description, preconditions, type, priority, status, automation_ref, owner_id, tags)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id, revision`,
		projectID, req.FeatureID, req.Title, req.Description, req.Preconditions, req.Type,
		req.Priority, req.Status, req.AutomationRef, req.OwnerID, req.Tags).Scan(&id, &revision)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create test case")
		return
	}
	for i, step := range req.Steps {
		_, err = tx.Exec(c.Request.Context(), `
			INSERT INTO test_steps (test_case_id, position, action, expected_result, test_data)
			VALUES ($1,$2,$3,$4,$5)`, id, i+1, step.Action, step.ExpectedResult, step.TestData)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create test steps")
			return
		}
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit test case")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_case", &id, "created", "Created test case "+req.Title, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusCreated, gin.H{"id": id, "revision": revision})
}

func (h *Handler) UpdateTestCase(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	caseID, ok := parseParamID(c, "caseId")
	if !ok {
		return
	}
	var req testCaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Test case title is required")
		return
	}
	if req.FeatureID == nil {
		apiError(c, http.StatusUnprocessableEntity, "TRACEABILITY_ERROR", "A test case must link to a feature")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "features", projectID, *req.FeatureID); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Feature does not belong to this project")
		return
	}
	if len(req.Steps) == 0 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A test case must have at least one step")
		return
	}

	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}

	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(c.Request.Context())

	var revision int64
	err = tx.QueryRow(c.Request.Context(), `
		UPDATE test_cases SET feature_id=$3, title=$4, description=$5, preconditions=$6, type=$7,
		priority=$8, status=$9, automation_ref=$10, owner_id=$11, tags=$12, updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(13)+`
		RETURNING revision`,
		caseID, projectID, req.FeatureID, req.Title, req.Description, req.Preconditions, req.Type,
		req.Priority, req.Status, req.AutomationRef, req.OwnerID, req.Tags,
		revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "test case", "test_cases", caseID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update test case")
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `DELETE FROM test_steps WHERE test_case_id=$1`, caseID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to replace test steps")
		return
	}
	for i, step := range req.Steps {
		_, err = tx.Exec(c.Request.Context(), `
			INSERT INTO test_steps (test_case_id, position, action, expected_result, test_data)
			VALUES ($1,$2,$3,$4,$5)`, caseID, i+1, step.Action, step.ExpectedResult, step.TestData)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create test steps")
			return
		}
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit test case")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_case", &caseID, "updated", "Updated test case "+req.Title, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": caseID, "revision": revision})
}

func (h *Handler) DeleteTestCase(c *gin.Context) {
	projectID, ok := parseProjectID(c)
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
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(),
		`DELETE FROM test_cases WHERE id=$1 AND project_id=$2`+revisionClause(3)+` RETURNING revision`,
		caseID, projectID, revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "test case", "test_cases", caseID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete test case")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_case", &caseID, "deleted", "Deleted test case", nil)
	c.JSON(http.StatusOK, gin.H{"id": caseID, "revision": revision, "deleted": true})
}

// ---------------------------------------------------------------------------
// Test runs and results
// ---------------------------------------------------------------------------

type testRunRequest struct {
	ReleaseID     *uuid.UUID  `json:"release_id"`
	SprintID      *uuid.UUID  `json:"sprint_id"`
	EnvironmentID *uuid.UUID  `json:"environment_id"`
	BuildID       *uuid.UUID  `json:"build_id"`
	Name          string      `json:"name"`
	Status        string      `json:"status"`
	ExecutedBy    *uuid.UUID  `json:"executed_by"`
	TestCaseIDs   []uuid.UUID `json:"test_case_ids"`
}

func (h *Handler) CreateTestRun(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req testRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "Test run " + time.Now().UTC().Format("2006-01-02 15:04")
	}
	if req.Status == "" {
		req.Status = "planned"
	}
	if req.EnvironmentID == nil {
		apiError(c, http.StatusUnprocessableEntity, "TRACEABILITY_ERROR", "A test run must link to an environment")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "environments", projectID, *req.EnvironmentID); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Environment does not belong to this project")
		return
	}
	if len(req.TestCaseIDs) == 0 {
		rows, err := h.store.DB().Query(c.Request.Context(), `SELECT id FROM test_cases WHERE project_id=$1 ORDER BY title`, projectID)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test cases")
			return
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test cases")
				return
			}
			req.TestCaseIDs = append(req.TestCaseIDs, id)
		}
		rows.Close()
	}
	if len(req.TestCaseIDs) == 0 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "At least one test case is required")
		return
	}

	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(c.Request.Context())

	var runID uuid.UUID
	var revision int64
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO test_runs (project_id, release_id, sprint_id, environment_id, build_id, name, status, executed_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, revision`,
		projectID, req.ReleaseID, req.SprintID, req.EnvironmentID, req.BuildID, req.Name, req.Status, req.ExecutedBy).Scan(&runID, &revision)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create test run")
		return
	}
	for _, caseID := range req.TestCaseIDs {
		var exists bool
		if err := tx.QueryRow(c.Request.Context(), `SELECT EXISTS (SELECT 1 FROM test_cases WHERE id=$1 AND project_id=$2)`, caseID, projectID).Scan(&exists); err != nil || !exists {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Test case does not belong to this project")
			return
		}
		var resultID uuid.UUID
		err = tx.QueryRow(c.Request.Context(), `
			INSERT INTO test_results (test_run_id, test_case_id, status)
			VALUES ($1,$2,'not_run') RETURNING id`, runID, caseID).Scan(&resultID)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create test result")
			return
		}
		_, err = tx.Exec(c.Request.Context(), `
			INSERT INTO test_step_results (test_result_id, test_step_id, status)
			SELECT $1, id, 'not_run' FROM test_steps WHERE test_case_id=$2 ORDER BY position`, resultID, caseID)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create step results")
			return
		}
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit test run")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_run", &runID, "created", "Created test run "+req.Name, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusCreated, gin.H{"id": runID, "revision": revision})
}

type testResultPatch struct {
	Status        string     `json:"status"`
	ActualResult  string     `json:"actual_result"`
	DurationMS    *int       `json:"duration_ms"`
	BugWorkItemID *uuid.UUID `json:"bug_work_item_id"`
}

func (h *Handler) PatchTestResult(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	runID, ok := parseParamID(c, "runId")
	if !ok {
		return
	}
	resultID, ok := parseParamID(c, "resultId")
	if !ok {
		return
	}
	var req testResultPatch
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if !validTestStatus(req.Status) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid test result status")
		return
	}
	if req.BugWorkItemID != nil {
		if err := h.ensureBelongs(c.Request.Context(), "work_items", projectID, *req.BugWorkItemID); err != nil {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Bug does not belong to this project")
			return
		}
	}
	var completedAt interface{}
	if req.Status != TestNotRun {
		completedAt = time.Now().UTC()
	}
	tag, err := h.store.DB().Exec(c.Request.Context(), `
		UPDATE test_results r SET status=$4, actual_result=$5, duration_ms=$6, bug_work_item_id=$7,
		completed_at=COALESCE($8, r.completed_at), updated_at=NOW()
		FROM test_runs tr
		WHERE r.id=$1 AND r.test_run_id=$2 AND tr.id=r.test_run_id AND tr.project_id=$3`,
		resultID, runID, projectID, req.Status, req.ActualResult, req.DurationMS, req.BugWorkItemID, completedAt)
	if err != nil || tag.RowsAffected() == 0 {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test result not found")
		return
	}
	h.refreshRunStatus(c.Request.Context(), runID)
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_result", &resultID, "result_recorded",
		"Recorded test result: "+req.Status, map[string]interface{}{"status": req.Status})
	c.JSON(http.StatusOK, gin.H{"id": resultID, "status": req.Status})
}

type testStepResultPatch struct {
	Status        string     `json:"status"`
	ActualResult  string     `json:"actual_result"`
	EvidenceURL   string     `json:"evidence_url"`
	BugWorkItemID *uuid.UUID `json:"bug_work_item_id"`
}

func (h *Handler) PatchTestStepResult(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	runID, ok := parseParamID(c, "runId")
	if !ok {
		return
	}
	resultID, ok := parseParamID(c, "resultId")
	if !ok {
		return
	}
	stepResultID, ok := parseParamID(c, "stepResultId")
	if !ok {
		return
	}
	var req testStepResultPatch
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if !validTestStatus(req.Status) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid step result status")
		return
	}
	tag, err := h.store.DB().Exec(c.Request.Context(), `
		UPDATE test_step_results sr SET status=$5, actual_result=$6, evidence_url=$7, bug_work_item_id=$8, updated_at=NOW()
		FROM test_results r
		JOIN test_runs tr ON tr.id = r.test_run_id
		WHERE sr.id=$1 AND sr.test_result_id=$2 AND r.id=$2 AND r.test_run_id=$3 AND tr.project_id=$4`,
		stepResultID, resultID, runID, projectID, req.Status, req.ActualResult, req.EvidenceURL, req.BugWorkItemID)
	if err != nil || tag.RowsAffected() == 0 {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test step result not found")
		return
	}
	h.recomputeResultStatus(c.Request.Context(), resultID)
	h.refreshRunStatus(c.Request.Context(), runID)
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_step_result", &stepResultID, "result_recorded",
		"Recorded step result: "+req.Status, map[string]interface{}{"status": req.Status})
	c.JSON(http.StatusOK, gin.H{"id": stepResultID, "status": req.Status})
}

type createBugRequest struct {
	Title             string     `json:"title"`
	Description       string     `json:"description"`
	Severity          *string    `json:"severity"`
	Priority          string     `json:"priority"`
	AssigneeID        *uuid.UUID `json:"assignee_id"`
	ReproductionSteps string     `json:"reproduction_steps"`
	ExpectedResult    string     `json:"expected_result"`
	ActualResult      string     `json:"actual_result"`
	StepResultID      *uuid.UUID `json:"step_result_id"`
}

func (h *Handler) CreateBugFromResult(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	runID, ok := parseParamID(c, "runId")
	if !ok {
		return
	}
	resultID, ok := parseParamID(c, "resultId")
	if !ok {
		return
	}
	var req createBugRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Bug title is required")
		return
	}
	if req.Priority == "" {
		req.Priority = PriorityHigh
	}

	// Resolve the test result and its case/feature so the bug is fully linked.
	var caseID uuid.UUID
	var featureID *uuid.UUID
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT r.test_case_id, c.feature_id
		FROM test_results r
		JOIN test_runs tr ON tr.id = r.test_run_id
		JOIN test_cases c ON c.id = r.test_case_id
		WHERE r.id=$1 AND r.test_run_id=$2 AND tr.project_id=$3`, resultID, runID, projectID).Scan(&caseID, &featureID)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test result not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test result")
		return
	}

	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(c.Request.Context())

	var bugID uuid.UUID
	var number int64
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO work_items (project_id, feature_id, type, title, description, status, priority, severity,
		assignee_id, reporter_id, reproduction_steps, expected_result, actual_result)
		VALUES ($1,$2,'bug',$3,$4,'new',$5,$6,$7,$8,$9,$10,$11)
		RETURNING id, item_number`,
		projectID, featureID, req.Title, req.Description, req.Priority, req.Severity,
		req.AssigneeID, actorID(c), req.ReproductionSteps, req.ExpectedResult, req.ActualResult).Scan(&bugID, &number)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create bug")
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `UPDATE test_results SET bug_work_item_id=$2, updated_at=NOW() WHERE id=$1`, resultID, bugID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to link bug to result")
		return
	}
	if req.StepResultID != nil {
		_, _ = tx.Exec(c.Request.Context(), `UPDATE test_step_results SET bug_work_item_id=$2, updated_at=NOW() WHERE id=$1 AND test_result_id=$3`, *req.StepResultID, bugID, resultID)
	}
	_, _ = tx.Exec(c.Request.Context(), `
		INSERT INTO artifact_links (project_id, source_type, source_id, target_type, target_id, link_type, created_by)
		VALUES ($1,'test_result',$2,'work_item',$3,'found_bug',$4)
		ON CONFLICT DO NOTHING`, projectID, resultID, bugID, actorID(c))
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit bug")
		return
	}
	var prefix string
	_ = h.store.DB().QueryRow(c.Request.Context(), `SELECT key_prefix FROM projects WHERE id=$1`, projectID).Scan(&prefix)
	key := prefix + "-" + fmt.Sprintf("%d", number)
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "work_item", &bugID, "created_from_test",
		"Created bug "+key+" from failed test result", map[string]interface{}{"test_result_id": resultID})
	c.JSON(http.StatusCreated, gin.H{"id": bugID, "key": key})
}

func validTestStatus(s string) bool {
	switch s {
	case TestNotRun, TestPassed, TestFailed, TestBlocked, TestSkipped:
		return true
	}
	return false
}

func (h *Handler) recomputeResultStatus(ctx context.Context, resultID uuid.UUID) {
	var failed, passed, blocked, skipped, notRun int
	err := h.store.DB().QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status='failed'),
			COUNT(*) FILTER (WHERE status='passed'),
			COUNT(*) FILTER (WHERE status='blocked'),
			COUNT(*) FILTER (WHERE status='skipped'),
			COUNT(*) FILTER (WHERE status='not_run')
		FROM test_step_results WHERE test_result_id=$1`, resultID).Scan(&failed, &passed, &blocked, &skipped, &notRun)
	if err != nil {
		return
	}
	status := TestNotRun
	switch {
	case failed > 0:
		status = TestFailed
	case notRun > 0:
		status = TestNotRun
	case blocked > 0:
		status = TestBlocked
	case passed > 0 && skipped == 0:
		status = TestPassed
	case passed > 0:
		status = TestPassed
	default:
		status = TestSkipped
	}
	_, _ = h.store.DB().Exec(ctx, `UPDATE test_results SET status=$2, updated_at=NOW() WHERE id=$1`, resultID, status)
}

func (h *Handler) refreshRunStatus(ctx context.Context, runID uuid.UUID) {
	var remaining int
	_ = h.store.DB().QueryRow(ctx, `SELECT COUNT(*) FROM test_results WHERE test_run_id=$1 AND status='not_run'`, runID).Scan(&remaining)
	if remaining == 0 {
		_, _ = h.store.DB().Exec(ctx, `UPDATE test_runs SET status='completed', completed_at=COALESCE(completed_at, NOW()), updated_at=NOW() WHERE id=$1`, runID)
	} else {
		_, _ = h.store.DB().Exec(ctx, `UPDATE test_runs SET status='in_progress', started_at=COALESCE(started_at, NOW()), updated_at=NOW() WHERE id=$1 AND status='planned'`, runID)
	}
}
