package workspace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"spm-platform/api/internal/audit"
)

// createBugRequest is the body for POST /test-runs/{runId}/results/{resultId}/bug.
// Force is only consulted when open duplicates exist; it creates the bug anyway
// and relinks the result to the newest bug.
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
	Force             bool       `json:"force"`
}

// bugDuplicate is one open bug that already covers the same title or result.
type bugDuplicate struct {
	ID      uuid.UUID `json:"id"`
	Key     string    `json:"key"`
	Title   string    `json:"title"`
	Status  string    `json:"status"`
	MatchOn string    `json:"match_on"`
}

// bugContext is the traceability snapshot captured before a defect is written.
type bugContext struct {
	RunID       uuid.UUID
	RunName     string
	RunStatus   string
	PlanName    string
	SuiteName   string
	Environment *uuid.UUID
	Release     *uuid.UUID
	Sprint      *uuid.UUID
	Build       *uuid.UUID
	BuildNumber string
	BuildCommit string

	Deployment            *uuid.UUID
	DeploymentEnvironment string
	DeploymentStatus      string
	DeploymentHealth      string

	ResultID        uuid.UUID
	ResultStatus    string
	ResultActual    string
	ResultBug       *uuid.UUID
	CaseID          uuid.UUID
	CaseTitle       string
	CaseRevision    int64
	FeatureID       *uuid.UUID
	EnvironmentName string
	ReleaseName     string
	SprintName      string
	Steps           []TestStepResult
}

// bugCreateResult is returned by the single-create endpoint.
type bugCreateResult struct {
	ID           uuid.UUID      `json:"id"`
	Key          string         `json:"key"`
	Revision     int64          `json:"revision"`
	StepResultID *uuid.UUID     `json:"step_result_id,omitempty"`
	Links        []ArtifactLink `json:"links"`
}

type bulkCreateBugsRequest struct {
	ResultIDs []uuid.UUID `json:"result_ids"`
	Force     bool        `json:"force"`
}

// bulkBugItem reports what happened for one selected failed result.
type bulkBugItem struct {
	ResultID     uuid.UUID      `json:"result_id"`
	Status       string         `json:"status"`
	Reason       string         `json:"reason,omitempty"`
	BugID        *uuid.UUID     `json:"bug_id,omitempty"`
	Key          string         `json:"key,omitempty"`
	StepResultID *uuid.UUID     `json:"step_result_id,omitempty"`
	Duplicates   []bugDuplicate `json:"duplicates,omitempty"`
}

type bulkBugResult struct {
	RunID      uuid.UUID     `json:"run_id"`
	Requested  int           `json:"requested"`
	Created    int           `json:"created"`
	Duplicates int           `json:"duplicates"`
	Skipped    int           `json:"skipped"`
	Failed     int           `json:"failed"`
	Items      []bulkBugItem `json:"items"`
}

var errBugStepNotFound = errors.New("step result does not belong to the test result")

func normalizeBugTitle(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(title)), " "))
}

func autoBugTitle(bc *bugContext, step *TestStepResult) string {
	if step == nil {
		return "Defect: " + bc.CaseTitle
	}
	title := fmt.Sprintf("Defect: %s — step %d", bc.CaseTitle, step.Position)
	if action := strings.TrimSpace(step.Action); action != "" {
		title += ": " + action
	}
	return title
}

func autoStepReproduction(steps []TestStepResult) string {
	lines := make([]string, 0, len(steps))
	for index, step := range steps {
		position := step.Position
		if position <= 0 {
			position = index + 1
		}
		lines = append(lines, fmt.Sprintf("%d. %s", position, step.Action))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func selectBugStep(bc *bugContext, stepResultID *uuid.UUID) (*TestStepResult, error) {
	if stepResultID != nil {
		for index := range bc.Steps {
			if bc.Steps[index].ID == *stepResultID {
				return &bc.Steps[index], nil
			}
		}
		return nil, errBugStepNotFound
	}
	for index := range bc.Steps {
		if bc.Steps[index].Status == TestFailed {
			return &bc.Steps[index], nil
		}
	}
	return nil, nil
}

// composeBugDescription appends the immutable execution context to the
// reporter's own description. IDs are included so the defect can be traced even
// if the test run is later archived.
func composeBugDescription(base string, bc *bugContext, step *TestStepResult) string {
	lines := []string{
		"Test context",
		fmt.Sprintf("- Test run: %s (%s) [%s]", bc.RunName, bc.RunStatus, bc.RunID),
		fmt.Sprintf("- Test case: %s (revision %d) [%s]", bc.CaseTitle, bc.CaseRevision, bc.CaseID),
		fmt.Sprintf("- Test result: %s [%s]", bc.ResultStatus, bc.ResultID),
	}
	if bc.PlanName != "" {
		lines = append(lines, "- Plan: "+bc.PlanName)
	}
	if bc.SuiteName != "" {
		lines = append(lines, "- Suite: "+bc.SuiteName)
	}
	if step != nil {
		lines = append(lines, fmt.Sprintf("- Step: %d — %s (%s) [%s]", step.Position, step.Action, step.Status, step.ID))
	}
	if bc.EnvironmentName != "" {
		lines = append(lines, "- Environment: "+bc.EnvironmentName)
	}
	if bc.ReleaseName != "" {
		lines = append(lines, "- Release: "+bc.ReleaseName)
	}
	if bc.SprintName != "" {
		lines = append(lines, "- Sprint: "+bc.SprintName)
	}
	if bc.Build != nil {
		build := "Build: #" + bc.BuildNumber
		if bc.BuildCommit != "" {
			commit := bc.BuildCommit
			if len(commit) > 12 {
				commit = commit[:12]
			}
			build += " (" + commit + ")"
		}
		lines = append(lines, "- "+build)
	}
	if bc.Deployment != nil {
		deployment := fmt.Sprintf("- Deployment: %s/%s", bc.DeploymentStatus, bc.DeploymentHealth)
		if bc.DeploymentEnvironment != "" {
			deployment += " on " + bc.DeploymentEnvironment
		}
		deployment += " [" + bc.Deployment.String() + "]"
		lines = append(lines, deployment)
	}

	if len(bc.Steps) > 0 {
		lines = append(lines, "", "Executed steps")
		for index, current := range bc.Steps {
			position := current.Position
			if position <= 0 {
				position = index + 1
			}
			lines = append(lines, fmt.Sprintf("%d. [%s] %s", position, current.Status, current.Action))
			if current.ExpectedResult != "" {
				lines = append(lines, "   expected: "+current.ExpectedResult)
			}
			if current.ActualResult != "" {
				lines = append(lines, "   actual: "+current.ActualResult)
			}
			if current.EvidenceURL != "" {
				lines = append(lines, "   evidence: "+current.EvidenceURL)
			}
		}
	}

	contextBlock := strings.Join(lines, "\n")
	if base = strings.TrimSpace(base); base == "" {
		return contextBlock
	}
	return base + "\n\n" + contextBlock
}

func bugLinkTargets(bc *bugContext, bugID uuid.UUID) []ArtifactLink {
	links := []ArtifactLink{
		{SourceType: "test_result", SourceID: bc.ResultID, TargetType: "work_item", TargetID: bugID, LinkType: "found_bug"},
		{SourceType: "test_run", SourceID: bc.RunID, TargetType: "work_item", TargetID: bugID, LinkType: "found_bug"},
		{SourceType: "test_case", SourceID: bc.CaseID, TargetType: "work_item", TargetID: bugID, LinkType: "found_bug"},
	}
	if bc.Build != nil {
		links = append(links, ArtifactLink{SourceType: "build", SourceID: *bc.Build, TargetType: "work_item", TargetID: bugID, LinkType: "found_bug"})
	}
	if bc.Deployment != nil {
		links = append(links, ArtifactLink{SourceType: "deployment", SourceID: *bc.Deployment, TargetType: "work_item", TargetID: bugID, LinkType: "found_bug"})
	}
	return links
}

// bulkSkipReason explains why a result cannot produce a new defect without
// touching the database. not_failed is a skip rather than an error so stale
// selections report clearly.
func bulkSkipReason(bc *bugContext) string {
	if bc.ResultBug != nil {
		return "already_linked"
	}
	for _, step := range bc.Steps {
		if step.BugWorkItemID != nil {
			return "already_linked"
		}
		if step.Status == TestFailed {
			return ""
		}
	}
	if bc.ResultStatus != TestFailed {
		return "not_failed"
	}
	return ""
}

func firstFailedStep(steps []TestStepResult) *TestStepResult {
	for index := range steps {
		if steps[index].Status == TestFailed {
			return &steps[index]
		}
	}
	return nil
}

func summarizeBulkBugs(runID uuid.UUID, items []bulkBugItem) bulkBugResult {
	summary := bulkBugResult{RunID: runID, Requested: len(items), Items: items}
	if summary.Items == nil {
		summary.Items = []bulkBugItem{}
	}
	for _, item := range items {
		switch item.Status {
		case "created":
			summary.Created++
		case "duplicate":
			summary.Duplicates++
		case "skipped":
			summary.Skipped++
		default:
			summary.Failed++
		}
	}
	return summary
}

func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func validBugSeverity(severity string) bool {
	switch severity {
	case "blocker", "critical", "major", "minor", "trivial":
		return true
	}
	return false
}

func (h *Handler) loadBugContext(ctx context.Context, projectID, runID, resultID uuid.UUID) (*bugContext, error) {
	bc := &bugContext{}
	err := h.store.DB().QueryRow(ctx, `
		SELECT tr.id, tr.name, tr.status, COALESCE(tr.plan_name, ''), COALESCE(tr.suite_name, ''),
		       tr.environment_id, COALESCE(e.name, ''), tr.release_id, COALESCE(rel.name, ''),
		       tr.sprint_id, COALESCE(s.name, ''), tr.build_id, COALESCE(b.number, ''), COALESCE(b.commit_sha, ''),
		       r.id, r.status, r.actual_result, r.bug_work_item_id,
		       c.id, c.title, r.case_revision, c.feature_id
		FROM test_results r
		JOIN test_runs tr ON tr.id = r.test_run_id
		JOIN test_cases c ON c.id = r.test_case_id
		LEFT JOIN environments e ON e.id = tr.environment_id
		LEFT JOIN releases rel ON rel.id = tr.release_id
		LEFT JOIN sprints s ON s.id = tr.sprint_id
		LEFT JOIN builds b ON b.id = tr.build_id
		WHERE r.id=$1 AND r.test_run_id=$2 AND tr.project_id=$3`,
		resultID, runID, projectID).Scan(
		&bc.RunID, &bc.RunName, &bc.RunStatus, &bc.PlanName, &bc.SuiteName,
		&bc.Environment, &bc.EnvironmentName, &bc.Release, &bc.ReleaseName,
		&bc.Sprint, &bc.SprintName, &bc.Build, &bc.BuildNumber, &bc.BuildCommit,
		&bc.ResultID, &bc.ResultStatus, &bc.ResultActual, &bc.ResultBug,
		&bc.CaseID, &bc.CaseTitle, &bc.CaseRevision, &bc.FeatureID)
	if err != nil {
		return nil, err
	}

	rows, err := h.store.DB().Query(ctx, `
		SELECT id, test_result_id, test_step_id, case_revision, position, action, expected_result, test_data,
		       status, actual_result, evidence_url, bug_work_item_id, created_at, updated_at
		FROM test_step_results WHERE test_result_id=$1 ORDER BY position, id`, resultID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bc.Steps = []TestStepResult{}
	for rows.Next() {
		var step TestStepResult
		if err := rows.Scan(&step.ID, &step.TestResultID, &step.TestStepID, &step.CaseRevision, &step.Position,
			&step.Action, &step.ExpectedResult, &step.TestData, &step.Status, &step.ActualResult,
			&step.EvidenceURL, &step.BugWorkItemID, &step.CreatedAt, &step.UpdatedAt); err != nil {
			return nil, err
		}
		bc.Steps = append(bc.Steps, step)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if bc.Environment != nil || bc.Build != nil {
		var deploymentID uuid.UUID
		err := h.store.DB().QueryRow(ctx, `
			SELECT d.id, d.status, d.health_status, COALESCE(e.name, '')
			FROM deployments d
			JOIN environments e ON e.id = d.environment_id
			WHERE d.project_id=$1
			  AND ($2::uuid IS NULL OR d.environment_id=$2)
			  AND ($3::uuid IS NULL OR d.build_id=$3)
			ORDER BY (d.build_id IS NOT DISTINCT FROM $3::uuid) DESC, d.created_at DESC
			LIMIT 1`, projectID, bc.Environment, bc.Build).Scan(
			&deploymentID, &bc.DeploymentStatus, &bc.DeploymentHealth, &bc.DeploymentEnvironment)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			bc.Deployment = &deploymentID
		}
	}
	return bc, nil
}

func (h *Handler) findDuplicateBugs(ctx context.Context, projectID, resultID uuid.UUID, title string) ([]bugDuplicate, error) {
	rows, err := h.store.DB().Query(ctx, `
		SELECT wi.id, p.key_prefix || '-' || wi.item_number::text, wi.title, wi.status,
		       CASE WHEN wi.id = r.bug_work_item_id OR EXISTS (
		            SELECT 1 FROM artifact_links al
		            WHERE al.project_id = wi.project_id
		              AND al.source_type = 'test_result' AND al.source_id = $2
		              AND al.target_type = 'work_item' AND al.target_id = wi.id
		       ) THEN 'test_result' ELSE 'title' END
		FROM work_items wi
		JOIN projects p ON p.id = wi.project_id
		LEFT JOIN test_results r ON r.id = $2
		WHERE wi.project_id=$1 AND wi.type='bug'
		  AND wi.status NOT IN ('done', 'cancelled', 'closed', 'verified')
		  AND (
		    lower(btrim(regexp_replace(wi.title, '[[:space:]]+', ' ', 'g'))) = lower($3)
		    OR wi.id = r.bug_work_item_id
		    OR EXISTS (
		      SELECT 1 FROM artifact_links al
		      WHERE al.project_id = wi.project_id
		        AND al.source_type = 'test_result' AND al.source_id = $2
		        AND al.target_type = 'work_item' AND al.target_id = wi.id
		    )
		  )
		ORDER BY wi.updated_at DESC
		LIMIT 10`, projectID, resultID, normalizeBugTitle(title))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	duplicates := []bugDuplicate{}
	for rows.Next() {
		var duplicate bugDuplicate
		if err := rows.Scan(&duplicate.ID, &duplicate.Key, &duplicate.Title, &duplicate.Status, &duplicate.MatchOn); err != nil {
			return nil, err
		}
		duplicates = append(duplicates, duplicate)
	}
	return duplicates, rows.Err()
}

func (h *Handler) workItemKey(ctx context.Context, projectID uuid.UUID, number int64) string {
	var prefix string
	_ = h.store.DB().QueryRow(ctx, `SELECT key_prefix FROM projects WHERE id=$1`, projectID).Scan(&prefix)
	return prefix + "-" + fmt.Sprintf("%d", number)
}

// insertBug writes the defect, relinks the executed result/step and records
// artifact links for every traceability artifact in one transaction.
func (h *Handler) insertBug(ctx context.Context, projectID uuid.UUID, actor *uuid.UUID, bc *bugContext, req createBugRequest, step *TestStepResult) (uuid.UUID, int64, int64, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = autoBugTitle(bc, step)
	}
	description := composeBugDescription(req.Description, bc, step)
	priority := req.Priority
	if priority == "" {
		priority = PriorityHigh
	}
	labels := []string{"test-defect"}

	tx, err := h.store.DB().Begin(ctx)
	if err != nil {
		return uuid.Nil, 0, 0, err
	}
	defer tx.Rollback(ctx)

	var bugID uuid.UUID
	var number, revision int64
	err = tx.QueryRow(ctx, `
		INSERT INTO work_items (project_id, feature_id, type, title, description, status, priority, severity,
		assignee_id, reporter_id, sprint_id, release_id, environment_id, reproduction_steps, expected_result,
		actual_result, labels)
		VALUES ($1,$2,'bug',$3,$4,'new',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id, item_number, revision`,
		projectID, bc.FeatureID, title, description, priority, req.Severity, req.AssigneeID, actor,
		bc.Sprint, bc.Release, bc.Environment, req.ReproductionSteps, req.ExpectedResult,
		req.ActualResult, labels).Scan(&bugID, &number, &revision)
	if err != nil {
		return uuid.Nil, 0, 0, err
	}

	if _, err := tx.Exec(ctx, `UPDATE test_results SET bug_work_item_id=$2, updated_at=NOW() WHERE id=$1`, bc.ResultID, bugID); err != nil {
		return uuid.Nil, 0, 0, err
	}
	if step != nil {
		if _, err := tx.Exec(ctx, `UPDATE test_step_results SET bug_work_item_id=$2, updated_at=NOW() WHERE id=$1 AND test_result_id=$3`, step.ID, bugID, bc.ResultID); err != nil {
			return uuid.Nil, 0, 0, err
		}
	}
	for _, link := range bugLinkTargets(bc, bugID) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO artifact_links (project_id, source_type, source_id, target_type, target_id, link_type, created_by)
			VALUES ($1,$2,$3,'work_item',$4,'found_bug',$5)
			ON CONFLICT DO NOTHING`,
			projectID, link.SourceType, link.SourceID, bugID, actor); err != nil {
			return uuid.Nil, 0, 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, 0, 0, err
	}
	return bugID, number, revision, nil
}

// CreateBugFromResult handles POST /projects/{id}/test-runs/{runId}/results/{resultId}/bug.
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
	if req.Priority == "" {
		req.Priority = PriorityHigh
	}
	if !validPriority(req.Priority) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid priority")
		return
	}
	if req.Severity != nil {
		severity := strings.TrimSpace(*req.Severity)
		if severity == "" {
			req.Severity = nil
		} else if !validBugSeverity(severity) {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid severity")
			return
		} else {
			req.Severity = &severity
		}
	}

	bc, err := h.loadBugContext(c.Request.Context(), projectID, runID, resultID)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test result not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test result")
		return
	}
	step, err := selectBugStep(bc, req.StepResultID)
	if errors.Is(err, errBugStepNotFound) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Step result does not belong to the test result")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test steps")
		return
	}
	if req.Title == "" {
		req.Title = autoBugTitle(bc, step)
	}
	if req.ExpectedResult == "" && step != nil {
		req.ExpectedResult = step.ExpectedResult
	}
	if req.ActualResult == "" {
		if step != nil {
			req.ActualResult = step.ActualResult
		}
		if req.ActualResult == "" {
			req.ActualResult = bc.ResultActual
		}
	}
	if req.ReproductionSteps == "" {
		req.ReproductionSteps = autoStepReproduction(bc.Steps)
	}

	duplicates, err := h.findDuplicateBugs(c.Request.Context(), projectID, resultID, req.Title)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to check for duplicate bugs")
		return
	}
	if len(duplicates) > 0 && !req.Force {
		h.recordAudit(c, projectID, audit.Entry{
			Action: "test.bug.create", TargetType: "test_result", TargetID: resultID.String(), Result: "failure",
			Reason: "duplicate_bug",
			After:  map[string]any{"title": req.Title, "duplicates": len(duplicates)},
		})
		c.JSON(http.StatusConflict, gin.H{
			"error":      errorBody{Code: "DUPLICATE_BUG", Message: "A similar open bug already exists"},
			"duplicates": duplicates,
		})
		return
	}

	bugID, number, revision, err := h.insertBug(c.Request.Context(), projectID, actorID(c), bc, req, step)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create bug")
		return
	}
	key := h.workItemKey(c.Request.Context(), projectID, number)
	var stepID *uuid.UUID
	if step != nil {
		stepID = &step.ID
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "work_item", &bugID, "created_from_test",
		"Created bug "+key+" from failed test result", map[string]interface{}{
			"test_run_id": runID, "test_result_id": resultID, "test_case_id": bc.CaseID,
		})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "test.bug.create", TargetType: "work_item", TargetID: bugID.String(), Result: "success",
		After: map[string]any{
			"key": key, "title": req.Title, "test_run_id": runID.String(), "test_result_id": resultID.String(),
			"test_case_id": bc.CaseID.String(), "step_result_id": uuidOrEmpty(stepID),
			"environment_id": uuidOrEmpty(bc.Environment), "build_id": uuidOrEmpty(bc.Build),
			"deployment_id": uuidOrEmpty(bc.Deployment), "forced": req.Force,
		},
	})
	c.JSON(http.StatusCreated, bugCreateResult{
		ID: bugID, Key: key, Revision: revision, StepResultID: stepID, Links: bugLinkTargets(bc, bugID),
	})
}

// BulkCreateBugsFromRun handles POST /projects/{id}/test-runs/{runId}/bugs.
// Results that are already linked or no longer failed are skipped; duplicates
// are reported per item unless force is set.
func (h *Handler) BulkCreateBugsFromRun(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	runID, ok := parseParamID(c, "runId")
	if !ok {
		return
	}
	var req bulkCreateBugsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}

	var runExists bool
	if err := h.store.DB().QueryRow(c.Request.Context(),
		`SELECT EXISTS (SELECT 1 FROM test_runs WHERE id=$1 AND project_id=$2)`, runID, projectID).Scan(&runExists); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test run")
		return
	}
	if !runExists {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test run not found")
		return
	}

	resultIDs := uniqueIDs(req.ResultIDs)
	if len(resultIDs) > 0 {
		var found int
		if err := h.store.DB().QueryRow(c.Request.Context(),
			`SELECT COUNT(*) FROM test_results WHERE test_run_id=$1 AND id = ANY($2)`, runID, resultIDs).Scan(&found); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test results")
			return
		}
		if found != len(resultIDs) {
			apiError(c, http.StatusBadRequest, "INVALID_RESULT_IDS", "Every result_id must belong to this test run")
			return
		}
	} else {
		rows, err := h.store.DB().Query(c.Request.Context(), `
			SELECT r.id FROM test_results r
			WHERE r.test_run_id=$1
			  AND (r.status='failed' OR EXISTS (
			    SELECT 1 FROM test_step_results sr WHERE sr.test_result_id=r.id AND sr.status='failed'
			  ))
			ORDER BY r.created_at`, runID)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load failed test results")
			return
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load failed test results")
				return
			}
			resultIDs = append(resultIDs, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load failed test results")
			return
		}
		rows.Close()
	}

	items := make([]bulkBugItem, 0, len(resultIDs))
	for _, resultID := range resultIDs {
		item := bulkBugItem{ResultID: resultID}
		bc, err := h.loadBugContext(c.Request.Context(), projectID, runID, resultID)
		if err != nil {
			item.Status = "failed"
			item.Reason = "load_failed"
			items = append(items, item)
			continue
		}
		if reason := bulkSkipReason(bc); reason != "" {
			item.Status = "skipped"
			item.Reason = reason
			items = append(items, item)
			continue
		}
		step := firstFailedStep(bc.Steps)
		request := createBugRequest{
			Title:             autoBugTitle(bc, step),
			Priority:          PriorityHigh,
			ReproductionSteps: autoStepReproduction(bc.Steps),
		}
		if step != nil {
			request.ExpectedResult = step.ExpectedResult
			request.ActualResult = step.ActualResult
			if request.ActualResult == "" {
				request.ActualResult = bc.ResultActual
			}
		} else {
			request.ActualResult = bc.ResultActual
		}
		duplicates, err := h.findDuplicateBugs(c.Request.Context(), projectID, resultID, request.Title)
		if err != nil {
			item.Status = "failed"
			item.Reason = "duplicate_check_failed"
			items = append(items, item)
			continue
		}
		if len(duplicates) > 0 && !req.Force {
			item.Status = "duplicate"
			item.Reason = "open_bug_exists"
			item.Duplicates = duplicates
			items = append(items, item)
			continue
		}
		bugID, number, _, err := h.insertBug(c.Request.Context(), projectID, actorID(c), bc, request, step)
		if err != nil {
			item.Status = "failed"
			item.Reason = "create_failed"
			items = append(items, item)
			continue
		}
		key := h.workItemKey(c.Request.Context(), projectID, number)
		item.Status = "created"
		item.BugID = &bugID
		item.Key = key
		if step != nil {
			item.StepResultID = &step.ID
		}
		h.recordActivity(c.Request.Context(), projectID, actorID(c), "work_item", &bugID, "created_from_test",
			"Created bug "+key+" from failed test result", map[string]interface{}{
				"test_run_id": runID, "test_result_id": resultID, "test_case_id": bc.CaseID, "bulk": true,
			})
		h.recordAudit(c, projectID, audit.Entry{
			Action: "test.bug.create", TargetType: "work_item", TargetID: bugID.String(), Result: "success",
			After: map[string]any{
				"key": key, "test_run_id": runID.String(), "test_result_id": resultID.String(),
				"test_case_id": bc.CaseID.String(), "bulk": true, "forced": req.Force,
			},
		})
		items = append(items, item)
	}

	summary := summarizeBulkBugs(runID, items)
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_run", &runID, "bugs_created_from_failures",
		fmt.Sprintf("Created %d bugs from failed test results", summary.Created), map[string]interface{}{
			"requested": summary.Requested, "created": summary.Created, "duplicates": summary.Duplicates,
			"skipped": summary.Skipped, "failed": summary.Failed,
		})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "test.bug.bulk_create", TargetType: "test_run", TargetID: runID.String(), Result: "success",
		After: map[string]any{
			"requested": summary.Requested, "created": summary.Created, "duplicates": summary.Duplicates,
			"skipped": summary.Skipped, "failed": summary.Failed, "forced": req.Force,
		},
	})
	c.JSON(http.StatusOK, summary)
}
