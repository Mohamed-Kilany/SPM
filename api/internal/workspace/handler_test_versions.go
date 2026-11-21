package workspace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Case versions
// ---------------------------------------------------------------------------

// snapshotTestCaseVersion writes an immutable snapshot of the case as it is
// currently stored, together with its ordered steps. It runs inside the same
// transaction as the case mutation so a version can never describe a state that
// was rolled back.
func snapshotTestCaseVersion(ctx context.Context, tx pgx.Tx, projectID, caseID uuid.UUID, actor *uuid.UUID) error {
	var actorName string
	if actor != nil {
		// The audit trail is more useful with a readable name, but a missing
		// user row must not block the edit; changed_by still identifies them.
		_ = tx.QueryRow(ctx,
			`SELECT COALESCE(NULLIF(display_name, ''), username) FROM users WHERE id=$1`,
			*actor).Scan(&actorName)
	}
	var versionID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO test_case_versions (
			test_case_id, revision, feature_id, feature_title, title, description,
			preconditions, type, priority, status, automation_ref, owner_id, tags,
			changed_by, changed_by_name
		)
		SELECT c.id, c.revision, c.feature_id, COALESCE(f.title, ''), c.title, c.description,
		       c.preconditions, c.type, c.priority, c.status, c.automation_ref, c.owner_id,
		       c.tags, $3, $4
		FROM test_cases c
		LEFT JOIN features f ON f.id = c.feature_id
		WHERE c.id=$1 AND c.project_id=$2
		RETURNING id`, caseID, projectID, actor, actorName).Scan(&versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("workspace: test case %s not found while snapshotting", caseID)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO test_case_version_steps (version_id, position, action, expected_result, test_data)
		SELECT $1, position, action, expected_result, COALESCE(test_data, '')
		FROM test_steps WHERE test_case_id=$2 ORDER BY position`, versionID, caseID)
	return err
}

// ListTestCaseVersions handles GET /projects/:id/test-cases/:caseId/versions.
// Versions are returned newest first with their full step snapshots so the UI
// can render history without a second request per revision.
func (h *Handler) ListTestCaseVersions(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	caseID, ok := parseParamID(c, "caseId")
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	ctx := c.Request.Context()
	var exists bool
	if err := h.store.DB().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM test_cases WHERE id=$1 AND project_id=$2)`,
		caseID, projectID).Scan(&exists); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test case")
		return
	}
	if !exists {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test case not found")
		return
	}

	query := `SELECT id, revision, COALESCE(feature_id::text, ''), feature_title, title, description,
	                 preconditions, type, priority, status, automation_ref, COALESCE(owner_id::text, ''),
	                 tags, COALESCE(changed_by::text, ''), changed_by_name, created_at
	          FROM test_case_versions WHERE test_case_id=$1`
	args := []interface{}{caseID}
	if raw := strings.TrimSpace(c.Query("before")); raw != "" {
		before, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 1 {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "before must be a positive revision")
			return
		}
		args = append(args, before)
		query += fmt.Sprintf(" AND revision < $%d", len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY revision DESC LIMIT $%d", len(args))

	rows, err := h.store.DB().Query(ctx, query, args...)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test case versions")
		return
	}
	defer rows.Close()
	versions := []TestCaseVersion{}
	byVersion := map[uuid.UUID]int{}
	for rows.Next() {
		var version TestCaseVersion
		var feature, owner, changedBy string
		if err := rows.Scan(&version.ID, &version.Revision, &feature, &version.FeatureTitle,
			&version.Title, &version.Description, &version.Preconditions, &version.Type,
			&version.Priority, &version.Status, &version.AutomationRef, &owner, &version.Tags,
			&changedBy, &version.ChangedByName, &version.CreatedAt); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read test case versions")
			return
		}
		version.TestCaseID = caseID
		version.FeatureID = parseUUIDPtr(feature)
		version.OwnerID = parseUUIDPtr(owner)
		version.ChangedBy = parseUUIDPtr(changedBy)
		version.Steps = []TestCaseVersionStep{}
		versions = append(versions, version)
		byVersion[version.ID] = len(versions) - 1
	}
	if err := rows.Err(); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read test case versions")
		return
	}
	if len(versions) > 0 {
		ids := make([]uuid.UUID, len(versions))
		for i := range versions {
			ids[i] = versions[i].ID
		}
		stepRows, err := h.store.DB().Query(ctx, `
			SELECT version_id, position, action, expected_result, test_data
			FROM test_case_version_steps WHERE version_id = ANY($1)
			ORDER BY version_id, position`, ids)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test case version steps")
			return
		}
		defer stepRows.Close()
		for stepRows.Next() {
			var versionID uuid.UUID
			var step TestCaseVersionStep
			if err := stepRows.Scan(&versionID, &step.Position, &step.Action,
				&step.ExpectedResult, &step.TestData); err != nil {
				apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read test case version steps")
				return
			}
			if index, ok := byVersion[versionID]; ok {
				versions[index].Steps = append(versions[index].Steps, step)
			}
		}
		if err := stepRows.Err(); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read test case version steps")
			return
		}
	}
	var total int
	_ = h.store.DB().QueryRow(ctx,
		`SELECT COUNT(*) FROM test_case_versions WHERE test_case_id=$1`, caseID).Scan(&total)
	c.JSON(http.StatusOK, gin.H{"test_case_id": caseID, "total": total, "versions": versions})
}

// ---------------------------------------------------------------------------
// Test case export and import
// ---------------------------------------------------------------------------

const testCaseExportFormat = "spm.test-cases.v1"

type testCaseExportStep struct {
	Action         string `json:"action"`
	ExpectedResult string `json:"expected_result"`
	TestData       string `json:"test_data"`
}

type testCaseExportItem struct {
	FeatureID     *uuid.UUID           `json:"feature_id"`
	FeatureTitle  string               `json:"feature_title"`
	Title         string               `json:"title"`
	Description   string               `json:"description"`
	Preconditions string               `json:"preconditions"`
	Type          string               `json:"type"`
	Priority      string               `json:"priority"`
	Status        string               `json:"status"`
	AutomationRef string               `json:"automation_ref"`
	OwnerID       *uuid.UUID           `json:"owner_id"`
	Tags          []string             `json:"tags"`
	Steps         []testCaseExportStep `json:"steps"`
}

type testCaseExportDocument struct {
	Format     string               `json:"format"`
	ExportedAt time.Time            `json:"exported_at"`
	ProjectID  uuid.UUID            `json:"project_id"`
	Cases      []testCaseExportItem `json:"cases"`
}

// ExportTestCases handles GET /projects/:id/test-cases/export. The document is
// intentionally self-describing so a round-trip import can validate the format
// before writing anything.
func (h *Handler) ExportTestCases(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	rows, err := h.store.DB().Query(ctx, `
		SELECT c.id, c.feature_id, COALESCE(f.title, ''), c.title, c.description, c.preconditions,
		       c.type, c.priority, c.status, c.automation_ref, c.owner_id, c.tags
		FROM test_cases c
		LEFT JOIN features f ON f.id = c.feature_id
		WHERE c.project_id=$1
		ORDER BY lower(c.title), c.created_at`, projectID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to export test cases")
		return
	}
	defer rows.Close()
	document := testCaseExportDocument{
		Format:     testCaseExportFormat,
		ExportedAt: time.Now().UTC(),
		ProjectID:  projectID,
		Cases:      []testCaseExportItem{},
	}
	byCase := map[uuid.UUID]int{}
	for rows.Next() {
		var item testCaseExportItem
		var caseID uuid.UUID
		var feature, owner string
		if err := rows.Scan(&caseID, &feature, &item.FeatureTitle, &item.Title, &item.Description,
			&item.Preconditions, &item.Type, &item.Priority, &item.Status, &item.AutomationRef,
			&owner, &item.Tags); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read test cases")
			return
		}
		item.FeatureID = parseUUIDPtr(feature)
		item.OwnerID = parseUUIDPtr(owner)
		item.Steps = []testCaseExportStep{}
		document.Cases = append(document.Cases, item)
		byCase[caseID] = len(document.Cases) - 1
	}
	if err := rows.Err(); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read test cases")
		return
	}
	stepRows, err := h.store.DB().Query(ctx, `
		SELECT s.test_case_id, s.action, s.expected_result, COALESCE(s.test_data, '')
		FROM test_steps s
		JOIN test_cases c ON c.id = s.test_case_id
		WHERE c.project_id=$1
		ORDER BY s.test_case_id, s.position`, projectID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to export test steps")
		return
	}
	defer stepRows.Close()
	for stepRows.Next() {
		var caseID uuid.UUID
		var step testCaseExportStep
		if err := stepRows.Scan(&caseID, &step.Action, &step.ExpectedResult, &step.TestData); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read test steps")
			return
		}
		if index, ok := byCase[caseID]; ok {
			document.Cases[index].Steps = append(document.Cases[index].Steps, step)
		}
	}
	if err := stepRows.Err(); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read test steps")
		return
	}
	c.JSON(http.StatusOK, document)
}

type testCaseImportRequest struct {
	Format string               `json:"format"`
	Cases  []testCaseExportItem `json:"cases"`
}

// ImportTestCases handles POST /projects/:id/test-cases/import. Every case is
// validated before the first insert and all writes share one transaction, so a
// single bad row leaves the project unchanged.
func (h *Handler) ImportTestCases(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req testCaseImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if req.Format != "" && req.Format != testCaseExportFormat {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR",
			"format must be "+testCaseExportFormat)
		return
	}
	if len(req.Cases) == 0 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "At least one test case is required")
		return
	}
	ctx := c.Request.Context()
	requests := make([]testCaseRequest, len(req.Cases))
	for i := range req.Cases {
		item := req.Cases[i]
		featureID := item.FeatureID
		if featureID == nil {
			title := strings.TrimSpace(item.FeatureTitle)
			if title == "" {
				apiError(c, http.StatusBadRequest, "VALIDATION_ERROR",
					fmt.Sprintf("Case %d needs feature_id or feature_title", i+1))
				return
			}
			if err := h.store.DB().QueryRow(ctx,
				`SELECT id FROM features WHERE project_id=$1 AND lower(title)=lower($2) ORDER BY created_at LIMIT 1`,
				projectID, title).Scan(&featureID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					apiError(c, http.StatusBadRequest, "VALIDATION_ERROR",
						fmt.Sprintf("Case %d references unknown feature %q", i+1, title))
					return
				}
				apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to resolve exported feature")
				return
			}
		}
		steps := make([]testStepInput, len(item.Steps))
		for j, step := range item.Steps {
			steps[j] = testStepInput{
				Action:         step.Action,
				ExpectedResult: step.ExpectedResult,
				TestData:       step.TestData,
			}
		}
		requests[i] = testCaseRequest{
			FeatureID:     featureID,
			Title:         item.Title,
			Description:   item.Description,
			Preconditions: item.Preconditions,
			Type:          item.Type,
			Priority:      item.Priority,
			Status:        item.Status,
			AutomationRef: item.AutomationRef,
			OwnerID:       item.OwnerID,
			Tags:          item.Tags,
			Steps:         steps,
		}
		if !h.validateTestCaseRequest(c, projectID, &requests[i]) {
			return
		}
	}

	tx, err := h.store.DB().Begin(ctx)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	actor := actorID(c)
	imported := make([]gin.H, 0, len(requests))
	for _, req := range requests {
		var caseID uuid.UUID
		var revision int64
		err := tx.QueryRow(ctx, `
			INSERT INTO test_cases (project_id, feature_id, title, description, preconditions, type, priority, status, automation_ref, owner_id, tags)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id, revision`,
			projectID, req.FeatureID, req.Title, req.Description, req.Preconditions, req.Type,
			req.Priority, req.Status, req.AutomationRef, req.OwnerID, req.Tags).Scan(&caseID, &revision)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to import test case")
			return
		}
		for i, step := range req.Steps {
			if _, err := tx.Exec(ctx, `
				INSERT INTO test_steps (test_case_id, position, action, expected_result, test_data)
				VALUES ($1,$2,$3,$4,$5)`, caseID, i+1, step.Action, step.ExpectedResult, step.TestData); err != nil {
				apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to import test steps")
				return
			}
		}
		if err := snapshotTestCaseVersion(ctx, tx, projectID, caseID, actor); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to snapshot imported test case")
			return
		}
		imported = append(imported, gin.H{"id": caseID, "title": req.Title, "revision": revision})
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit imported test cases")
		return
	}
	h.recordActivity(ctx, projectID, actor, "test_case", nil, "imported",
		fmt.Sprintf("Imported %d test case(s)", len(imported)), nil)
	c.JSON(http.StatusCreated, gin.H{"format": testCaseExportFormat, "imported": len(imported), "cases": imported})
}

// ---------------------------------------------------------------------------
// Shared steps
// ---------------------------------------------------------------------------

type sharedStepRequest struct {
	Name            string `json:"name"`
	Action          string `json:"action"`
	ExpectedResult  string `json:"expected_result"`
	TestData        string `json:"test_data"`
	ExpectedVersion *int64 `json:"expected_version"`
}

func isUniqueViolation(err error) bool {
	return pgErrCode(err) == "23505"
}

func validateSharedStepRequest(c *gin.Context, req *sharedStepRequest) bool {
	req.Name = strings.TrimSpace(req.Name)
	req.Action = strings.TrimSpace(req.Action)
	req.ExpectedResult = strings.TrimSpace(req.ExpectedResult)
	if req.Name == "" || len(req.Name) > 255 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Shared step name is required and must be at most 255 characters")
		return false
	}
	if req.Action == "" || req.ExpectedResult == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A shared step needs an action and an expected result")
		return false
	}
	return true
}

// CreateSharedStep handles POST /projects/:id/test-shared-steps.
func (h *Handler) CreateSharedStep(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req sharedStepRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if !validateSharedStepRequest(c, &req) {
		return
	}
	var id uuid.UUID
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO test_shared_steps (project_id, name, action, expected_result, test_data, created_by)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, revision`,
		projectID, req.Name, req.Action, req.ExpectedResult, req.TestData, actorID(c)).Scan(&id, &revision)
	if err != nil {
		if isUniqueViolation(err) {
			apiError(c, http.StatusConflict, "DUPLICATE_NAME", "A shared step with that name already exists")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create shared step")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_shared_step", &id, "created",
		"Created shared step "+req.Name, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusCreated, gin.H{"id": id, "revision": revision})
}

// UpdateSharedStep handles PUT /projects/:id/test-shared-steps/:sharedStepId.
func (h *Handler) UpdateSharedStep(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	stepID, ok := parseParamID(c, "sharedStepId")
	if !ok {
		return
	}
	var req sharedStepRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if !validateSharedStepRequest(c, &req) {
		return
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		UPDATE test_shared_steps SET name=$3, action=$4, expected_result=$5, test_data=$6, updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(7)+`
		RETURNING revision`,
		stepID, projectID, req.Name, req.Action, req.ExpectedResult, req.TestData,
		revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "shared step", "test_shared_steps", stepID, projectID)
		return
	}
	if err != nil {
		if isUniqueViolation(err) {
			apiError(c, http.StatusConflict, "DUPLICATE_NAME", "A shared step with that name already exists")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update shared step")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_shared_step", &stepID, "updated",
		"Updated shared step "+req.Name, nil)
	setRevisionHeader(c, revision)
	c.JSON(http.StatusOK, gin.H{"id": stepID, "revision": revision})
}

// DeleteSharedStep handles DELETE /projects/:id/test-shared-steps/:sharedStepId.
// Cases keep the text they copied, so removing a template never rewrites a test.
func (h *Handler) DeleteSharedStep(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	stepID, ok := parseParamID(c, "sharedStepId")
	if !ok {
		return
	}
	expected, ok := revisionFromRequest(c, nil)
	if !ok {
		return
	}
	var revision int64
	err := h.store.DB().QueryRow(c.Request.Context(),
		`DELETE FROM test_shared_steps WHERE id=$1 AND project_id=$2`+revisionClause(3)+` RETURNING revision`,
		stepID, projectID, revisionValue(expected)).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "shared step", "test_shared_steps", stepID, projectID)
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete shared step")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_shared_step", &stepID, "deleted",
		"Deleted shared step", nil)
	c.JSON(http.StatusOK, gin.H{"id": stepID, "revision": revision, "deleted": true})
}
