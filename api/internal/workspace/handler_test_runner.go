package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"spm-platform/api/internal/attachments"
	"spm-platform/api/internal/audit"
	"spm-platform/api/internal/metrics"
)

// testRunCounts is the per-status tally used by the finish summary and by the
// background completion transition in refreshRunStatus.
type testRunCounts struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Blocked int `json:"blocked"`
	Skipped int `json:"skipped"`
	NotRun  int `json:"not_run"`
}

type testRunFinishRequest struct {
	Force bool `json:"force"`
}

// testRunFinishResult is the deterministic summary returned by the finish
// endpoint. Outcome is passed when at least one case ran and none failed,
// failed when any case failed, and incomplete when not_run cases remain after
// a forced finish.
type testRunFinishResult struct {
	ID               uuid.UUID  `json:"id"`
	Status           string     `json:"status"`
	Outcome          string     `json:"outcome"`
	Total            int        `json:"total"`
	Passed           int        `json:"passed"`
	Failed           int        `json:"failed"`
	Blocked          int        `json:"blocked"`
	Skipped          int        `json:"skipped"`
	NotRun           int        `json:"not_run"`
	CompletedAt      *time.Time `json:"completed_at"`
	AlreadyCompleted bool       `json:"already_completed"`
}

// testRunOutcome maps counts to the bounded metric outcome. Blocked and
// skipped cases do not count as failures; only a remaining not_run case makes
// a run incomplete.
func testRunOutcome(counts testRunCounts) string {
	switch {
	case counts.NotRun > 0:
		return "incomplete"
	case counts.Failed > 0:
		return "failed"
	default:
		return "passed"
	}
}

func (h *Handler) loadTestRunCounts(ctx context.Context, runID uuid.UUID) (testRunCounts, error) {
	var counts testRunCounts
	err := h.store.DB().QueryRow(ctx, `
		SELECT COUNT(*),
			COUNT(*) FILTER (WHERE status='passed'),
			COUNT(*) FILTER (WHERE status='failed'),
			COUNT(*) FILTER (WHERE status='blocked'),
			COUNT(*) FILTER (WHERE status='skipped'),
			COUNT(*) FILTER (WHERE status='not_run')
		FROM test_results WHERE test_run_id=$1`, runID).Scan(
		&counts.Total, &counts.Passed, &counts.Failed, &counts.Blocked, &counts.Skipped, &counts.NotRun)
	return counts, err
}

// completeTestRun transitions a run to completed exactly once and records the
// completion metric on that transition. It reports whether the run changed, so
// a concurrent or repeat call never double counts.
func (h *Handler) completeTestRun(ctx context.Context, runID uuid.UUID, counts testRunCounts) (bool, error) {
	outcome := testRunOutcome(counts)
	tag, err := h.store.DB().Exec(ctx, `
		UPDATE test_runs
		SET status='completed', completed_at=COALESCE(completed_at, NOW()),
			started_at=COALESCE(started_at, NOW()), updated_at=NOW()
		WHERE id=$1 AND status <> 'completed'`, runID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	metrics.Default().ObserveTestRunCompleted(outcome)
	return true, nil
}

// FinishTestRun handles POST /projects/:id/test-runs/:runId/finish. A run with
// not_run cases is rejected with 409 RUN_INCOMPLETE unless force is set;
// completed runs return the same summary idempotently.
func (h *Handler) FinishTestRun(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	runID, ok := parseParamID(c, "runId")
	if !ok {
		return
	}
	var req testRunFinishRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	ctx := c.Request.Context()
	var status string
	var completedAt *time.Time
	err := h.store.DB().QueryRow(ctx,
		`SELECT status, completed_at FROM test_runs WHERE id=$1 AND project_id=$2`,
		runID, projectID).Scan(&status, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test run not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test run")
		return
	}
	counts, err := h.loadTestRunCounts(ctx, runID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to summarise test run")
		return
	}
	result := testRunFinishResult{
		ID: runID, Status: status, Outcome: testRunOutcome(counts),
		Total: counts.Total, Passed: counts.Passed, Failed: counts.Failed,
		Blocked: counts.Blocked, Skipped: counts.Skipped, NotRun: counts.NotRun,
		CompletedAt: completedAt,
	}
	if status == "completed" {
		result.AlreadyCompleted = true
		c.JSON(http.StatusOK, result)
		return
	}
	if counts.Total == 0 {
		apiError(c, http.StatusConflict, "RUN_EMPTY", "A test run with no results cannot be completed")
		return
	}
	if counts.NotRun > 0 && !req.Force {
		apiError(c, http.StatusConflict, "RUN_INCOMPLETE",
			fmt.Sprintf("%d of %d cases are still not run. Confirm to finish anyway.", counts.NotRun, counts.Total))
		return
	}
	transitioned, err := h.completeTestRun(ctx, runID, counts)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to finish test run")
		return
	}
	if !transitioned {
		// Another request completed the run between the read and the update.
		result.Status = "completed"
		result.AlreadyCompleted = true
		_ = h.store.DB().QueryRow(ctx, `SELECT completed_at FROM test_runs WHERE id=$1`, runID).Scan(&result.CompletedAt)
		c.JSON(http.StatusOK, result)
		return
	}
	result.Status = "completed"
	_ = h.store.DB().QueryRow(ctx, `SELECT completed_at FROM test_runs WHERE id=$1`, runID).Scan(&result.CompletedAt)
	h.recordActivity(ctx, projectID, actorID(c), "test_run", &runID, "completed",
		"Finished test run with outcome "+result.Outcome,
		map[string]interface{}{"outcome": result.Outcome, "not_run": counts.NotRun, "forced": req.Force && counts.NotRun > 0})
	c.JSON(http.StatusOK, result)
}

// testStepEvidence is the flat evidence record returned after a successful
// upload. The attachment is linked to the step result through evidence_url.
type testStepEvidence struct {
	ID             uuid.UUID  `json:"id"`
	TestStepResult uuid.UUID  `json:"test_step_result_id"`
	Filename       string     `json:"filename"`
	MIMEType       string     `json:"mime_type"`
	SizeBytes      int64      `json:"size_bytes"`
	ChecksumSHA256 string     `json:"checksum_sha256"`
	ScanStatus     string     `json:"scan_status"`
	ScanEngine     string     `json:"scan_engine"`
	ScanDetail     string     `json:"scan_detail,omitempty"`
	UploadedBy     *uuid.UUID `json:"uploaded_by"`
	EvidenceURL    string     `json:"evidence_url"`
	CreatedAt      time.Time  `json:"created_at"`
}

// UploadTestStepEvidence handles
// POST /projects/:id/test-runs/:runId/results/:resultId/steps/:stepResultId/evidence.
// The upload reuses the shared attachment store, so extension, magic-byte,
// size and malware checks are identical to work item attachments.
func (h *Handler) UploadTestStepEvidence(c *gin.Context) {
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
	if h.attachments == nil {
		apiError(c, http.StatusServiceUnavailable, "ATTACHMENT_STORAGE_UNAVAILABLE", "Attachment storage is not configured")
		return
	}
	var found bool
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT TRUE
		FROM test_step_results sr
		JOIN test_results r ON r.id = sr.test_result_id
		JOIN test_runs tr ON tr.id = r.test_run_id
		WHERE sr.id=$1 AND sr.test_result_id=$2 AND r.test_run_id=$3 AND tr.project_id=$4`,
		stepResultID, resultID, runID, projectID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Test step result not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load test step result")
		return
	}
	limit := h.attachments.MaxBytes()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit+1<<20)
	header, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			apiError(c, http.StatusBadRequest, "ATTACHMENT_TOO_LARGE", fmt.Sprintf("Evidence files must be %d bytes or smaller", limit))
			return
		}
		apiError(c, http.StatusBadRequest, "ATTACHMENT_REQUIRED", "A file part named \"file\" is required")
		return
	}
	file, err := header.Open()
	if err != nil {
		apiError(c, http.StatusBadRequest, "ATTACHMENT_REQUIRED", "The uploaded file could not be read")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		apiError(c, http.StatusBadRequest, "ATTACHMENT_REQUIRED", "The uploaded file could not be read")
		return
	}
	attachmentID := uuid.New()
	stored, err := h.attachments.Save(c.Request.Context(), projectID, attachmentID, header.Filename, data)
	if err != nil {
		var rejection *attachments.Rejection
		var unavailable *attachments.Unavailable
		switch {
		case errors.As(err, &rejection):
			h.recordAudit(c, projectID, audit.Entry{
				Action: "test.step.evidence.upload", TargetType: "attachment", TargetID: attachmentID.String(),
				Result: "failure", Reason: rejection.Message,
				After: map[string]any{"test_step_result_id": stepResultID.String(), "filename": header.Filename},
			})
			apiError(c, http.StatusUnprocessableEntity, rejection.Code, rejection.Message)
		case errors.As(err, &unavailable):
			apiError(c, http.StatusServiceUnavailable, "SCAN_UNAVAILABLE", unavailable.Message)
		default:
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to store evidence")
		}
		return
	}
	downloadURL := "/api/v1/projects/" + projectID.String() + "/attachments/" + attachmentID.String() + "/download"
	actor := actorID(c)
	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		_ = h.attachments.Delete(stored.StoragePath)
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start transaction")
		return
	}
	defer tx.Rollback(c.Request.Context())
	var createdAt time.Time
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO attachments (id, project_id, entity_type, entity_id, filename, mime_type, size_bytes,
		                         storage_path, uploaded_by, checksum_sha256, scan_status, scan_engine, scan_detail)
		VALUES ($1, $2, 'test_step_result', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING created_at`,
		attachmentID, projectID, stepResultID, stored.Filename, stored.MIMEType, stored.SizeBytes,
		stored.StoragePath, actor, stored.ChecksumSHA256, stored.ScanStatus, stored.ScanEngine,
		stored.ScanDetail).Scan(&createdAt)
	if err != nil {
		_ = h.attachments.Delete(stored.StoragePath)
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to record evidence")
		return
	}
	tag, err := tx.Exec(c.Request.Context(), `
		UPDATE test_step_results SET evidence_url=$2, updated_at=NOW()
		WHERE id=$1 AND test_result_id=$3`, stepResultID, downloadURL, resultID)
	if err != nil || tag.RowsAffected() == 0 {
		_ = h.attachments.Delete(stored.StoragePath)
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to link evidence to the step")
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		_ = h.attachments.Delete(stored.StoragePath)
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit evidence")
		return
	}
	evidence := testStepEvidence{
		ID: attachmentID, TestStepResult: stepResultID, Filename: stored.Filename,
		MIMEType: stored.MIMEType, SizeBytes: stored.SizeBytes,
		ChecksumSHA256: stored.ChecksumSHA256, ScanStatus: stored.ScanStatus,
		ScanEngine: stored.ScanEngine, ScanDetail: stored.ScanDetail,
		UploadedBy: actor, EvidenceURL: downloadURL, CreatedAt: createdAt,
	}
	h.recordActivity(c.Request.Context(), projectID, actor, "test_step_result", &stepResultID, "evidence_added",
		"Attached evidence "+stored.Filename, map[string]interface{}{"attachment_id": attachmentID.String()})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "test.step.evidence.upload", TargetType: "attachment", TargetID: attachmentID.String(), Result: "success",
		After: map[string]any{"test_step_result_id": stepResultID.String(), "filename": stored.Filename,
			"size_bytes": stored.SizeBytes, "checksum_sha256": stored.ChecksumSHA256, "scan_engine": stored.ScanEngine},
	})
	c.JSON(http.StatusCreated, evidence)
}
