package workspace

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type importStepInput struct {
	Position     int    `json:"position"`
	Status       string `json:"status"`
	ActualResult string `json:"actual_result"`
	EvidenceURL  string `json:"evidence_url"`
}

type importResultInput struct {
	TestCaseID    *uuid.UUID        `json:"test_case_id"`
	Title         string            `json:"title"`
	AutomationRef string            `json:"automation_ref"`
	Status        string            `json:"status"`
	DurationMS    *int              `json:"duration_ms"`
	ActualResult  string            `json:"actual_result"`
	Steps         []importStepInput `json:"steps"`
}

type testImportRequest struct {
	Format        string              `json:"format"`
	Name          string              `json:"name"`
	EnvironmentID *uuid.UUID          `json:"environment_id"`
	ReleaseID     *uuid.UUID          `json:"release_id"`
	SprintID      *uuid.UUID          `json:"sprint_id"`
	BuildID       *uuid.UUID          `json:"build_id"`
	FeatureID     *uuid.UUID          `json:"feature_id"`
	Source        string              `json:"source"`
	Ref           string              `json:"ref"`
	Results       []importResultInput `json:"results"`
}

type importResponse struct {
	ID        uuid.UUID `json:"id"`
	Total     int       `json:"total"`
	Matched   int       `json:"matched"`
	Created   int       `json:"created"`
	Unmatched int       `json:"unmatched"`
	RunStatus string    `json:"run_status"`
}

type junitDoc struct {
	XMLName xml.Name     `xml:"testsuites"`
	Suites  []junitSuite `xml:"testsuite"`
	Cases   []junitCase  `xml:"testcase"`
}

type junitSuite struct {
	Name  string      `xml:"name,attr"`
	Cases []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string     `xml:"name,attr"`
	Classname string     `xml:"classname,attr"`
	Time      float64    `xml:"time,attr"`
	Failure   *junitText `xml:"failure"`
	Error     *junitText `xml:"error"`
	Skipped   *junitText `xml:"skipped"`
}

type junitText struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

// ImportTestRun handles POST /projects/:id/test-runs/import. It accepts either
// a normalized SPM JSON payload or JUnit XML. Existing cases are matched by
// automation_ref first and title second; when a feature is supplied, unmatched
// automated cases are created as linked test cases.
func (h *Handler) ImportTestRun(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 8*1024*1024))
	if err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_PAYLOAD", "Could not read import payload")
		return
	}
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "")))
	if format == "" {
		format = strings.ToLower(strings.TrimSpace(c.GetHeader("X-SPM-Import-Format")))
	}
	if format == "" {
		if strings.HasPrefix(strings.TrimSpace(string(body)), "<") {
			format = "junit"
		} else {
			format = "json"
		}
	}
	var req testImportRequest
	if format == "junit" || format == "xml" {
		parsed, parseErr := parseJUnit(body)
		if parseErr != nil {
			apiError(c, http.StatusBadRequest, "INVALID_PAYLOAD", "Invalid JUnit XML: "+parseErr.Error())
			return
		}
		req = parsed
		req.Source = "junit"
	} else {
		if err := json.Unmarshal(body, &req); err != nil {
			apiError(c, http.StatusBadRequest, "INVALID_PAYLOAD", "Invalid JSON import payload")
			return
		}
		if req.Source == "" {
			req.Source = "json"
		}
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "Imported run " + time.Now().UTC().Format("2006-01-02 15:04")
	}
	environmentRaw := strings.TrimSpace(c.Query("environment_id"))
	if environmentRaw != "" {
		if id, parseErr := uuid.Parse(environmentRaw); parseErr == nil {
			req.EnvironmentID = &id
		}
	}
	featureRaw := strings.TrimSpace(c.Query("feature_id"))
	if featureRaw != "" {
		if id, parseErr := uuid.Parse(featureRaw); parseErr == nil {
			req.FeatureID = &id
		}
	}
	if req.EnvironmentID == nil {
		apiError(c, http.StatusUnprocessableEntity, "TRACEABILITY_ERROR", "An imported test run must link to an environment")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "environments", projectID, *req.EnvironmentID); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Environment does not belong to this project")
		return
	}
	if len(req.Results) == 0 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "The import payload contains no test results")
		return
	}
	if req.FeatureID != nil {
		if err := h.ensureBelongs(c.Request.Context(), "features", projectID, *req.FeatureID); err != nil {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Feature does not belong to this project")
			return
		}
	}

	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start import transaction")
		return
	}
	defer tx.Rollback(c.Request.Context())

	var runID uuid.UUID
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO test_runs (project_id, release_id, sprint_id, environment_id, build_id, name, status,
		import_source, import_ref, metadata, started_at)
		VALUES ($1,$2,$3,$4,$5,$6,'in_progress',$7,$8,$9,NOW()) RETURNING id`,
		projectID, req.ReleaseID, req.SprintID, req.EnvironmentID, req.BuildID, req.Name, req.Source, req.Ref,
		map[string]interface{}{"imported": true, "source": req.Source, "ref": req.Ref}).Scan(&runID); err != nil {
		log.Printf("workspace import: create run: %v", err)
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create imported test run")
		return
	}

	seenCases := map[uuid.UUID]bool{}
	response := importResponse{ID: runID}
	for _, input := range req.Results {
		status := normalizeImportStatus(input.Status)
		if status == "" {
			status = TestNotRun
		}
		caseID, created, resolveErr := resolveImportCase(c.Request.Context(), tx, projectID, req.FeatureID, input, actorID(c))
		if resolveErr != nil {
			log.Printf("workspace import: resolve case %q: %v", input.Title, resolveErr)
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to resolve imported test case")
			return
		}
		response.Total++
		if caseID == uuid.Nil {
			response.Unmatched++
			continue
		}
		if seenCases[caseID] {
			continue
		}
		seenCases[caseID] = true
		if created {
			response.Created++
		} else {
			response.Matched++
		}
		var completed interface{}
		if status != TestNotRun {
			completed = time.Now().UTC()
		}
		var resultID uuid.UUID
		if err := tx.QueryRow(c.Request.Context(), `
			INSERT INTO test_results (test_run_id, test_case_id, case_revision, status, duration_ms, actual_result, completed_at)
			VALUES ($1,$2,(SELECT revision FROM test_cases WHERE id=$2),$3,$4,$5,$6) RETURNING id`,
			runID, caseID, status, input.DurationMS, input.ActualResult, completed).Scan(&resultID); err != nil {
			log.Printf("workspace import: create result for case %s: %v", caseID, err)
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to import test result")
			return
		}
		for _, step := range input.Steps {
			stepStatus := normalizeImportStatus(step.Status)
			if stepStatus == "" {
				stepStatus = TestNotRun
			}
			_, _ = tx.Exec(c.Request.Context(), `
				INSERT INTO test_step_results (test_result_id, test_step_id, case_revision, position, action, expected_result, test_data, status, actual_result, evidence_url)
				SELECT $1, id, (SELECT revision FROM test_cases WHERE id=$5), position, action, expected_result,
				       COALESCE(test_data, ''), $2, $3, $4
				FROM test_steps
				WHERE test_case_id=$5 AND position=$6
				ON CONFLICT (test_result_id, test_step_id) DO UPDATE
				SET status=EXCLUDED.status, actual_result=EXCLUDED.actual_result,
				    evidence_url=EXCLUDED.evidence_url, updated_at=NOW()`,
				resultID, stepStatus, step.ActualResult, step.EvidenceURL, caseID, step.Position)
		}
	}
	runStatus := "completed"
	var notRun int
	_ = tx.QueryRow(c.Request.Context(), `SELECT COUNT(*) FROM test_results WHERE test_run_id=$1 AND status='not_run'`, runID).Scan(&notRun)
	if notRun > 0 {
		runStatus = "in_progress"
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE test_runs SET status=$2::varchar,
		completed_at=CASE WHEN $2::varchar='completed' THEN NOW() ELSE NULL END,
		updated_at=NOW() WHERE id=$1`, runID, runStatus); err != nil {
		log.Printf("workspace import: finalize run %s: %v", runID, err)
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to finalize imported test run")
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		log.Printf("workspace import: commit run %s: %v", runID, err)
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit imported test run")
		return
	}
	response.RunStatus = runStatus
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "test_run", &runID, "imported",
		fmt.Sprintf("Imported %d test result(s) from %s", response.Total, req.Source),
		map[string]interface{}{"matched": response.Matched, "created": response.Created, "unmatched": response.Unmatched})
	c.JSON(http.StatusCreated, response)
}

func parseJUnit(body []byte) (testImportRequest, error) {
	var doc junitDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return testImportRequest{}, err
	}
	req := testImportRequest{Format: "junit", Name: "JUnit import " + time.Now().UTC().Format("2006-01-02 15:04")}
	add := func(testCase junitCase) {
		status := TestPassed
		actual := ""
		if testCase.Failure != nil {
			status = TestFailed
			actual = strings.TrimSpace(testCase.Failure.Message + " " + testCase.Failure.Body)
		} else if testCase.Error != nil {
			status = TestFailed
			actual = strings.TrimSpace(testCase.Error.Message + " " + testCase.Error.Body)
		} else if testCase.Skipped != nil {
			status = TestSkipped
			actual = strings.TrimSpace(testCase.Skipped.Message)
		}
		duration := int(testCase.Time * 1000)
		ref := testCase.Classname
		if ref != "" && testCase.Name != "" {
			ref += "." + testCase.Name
		}
		req.Results = append(req.Results, importResultInput{
			Title: testCase.Name, AutomationRef: ref, Status: status, DurationMS: &duration, ActualResult: actual,
		})
	}
	for _, suite := range doc.Suites {
		for _, testCase := range suite.Cases {
			add(testCase)
		}
	}
	for _, testCase := range doc.Cases {
		add(testCase)
	}
	return req, nil
}

func normalizeImportStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "passed", "pass", "success", "succeeded", "ok":
		return TestPassed
	case "failed", "fail", "failure", "error", "errored":
		return TestFailed
	case "blocked":
		return TestBlocked
	case "skipped", "skip", "pending":
		return TestSkipped
	case "not_run", "not-run", "not run":
		return TestNotRun
	default:
		return ""
	}
}

func resolveImportCase(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, featureID *uuid.UUID, input importResultInput, actor *uuid.UUID) (uuid.UUID, bool, error) {
	title := strings.TrimSpace(input.Title)
	ref := strings.TrimSpace(input.AutomationRef)
	if input.TestCaseID != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM test_cases WHERE id=$1 AND project_id=$2)`, *input.TestCaseID, projectID).Scan(&exists); err != nil {
			return uuid.Nil, false, err
		}
		if exists {
			return *input.TestCaseID, false, nil
		}
	}
	var id uuid.UUID
	if ref != "" {
		err := tx.QueryRow(ctx, `SELECT id FROM test_cases WHERE project_id=$1 AND automation_ref=$2 ORDER BY updated_at DESC LIMIT 1`, projectID, ref).Scan(&id)
		if err == nil {
			return id, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, err
		}
	}
	if title != "" {
		err := tx.QueryRow(ctx, `SELECT id FROM test_cases WHERE project_id=$1 AND title=$2 ORDER BY updated_at DESC LIMIT 1`, projectID, title).Scan(&id)
		if err == nil {
			return id, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, err
		}
	}
	if featureID == nil {
		return uuid.Nil, false, nil
	}
	if title == "" {
		title = ref
	}
	if title == "" {
		title = "Imported automated check"
	}
	if len(title) > 255 {
		title = title[:255]
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO test_cases (project_id, feature_id, title, type, priority, status, automation_ref)
		VALUES ($1,$2,$3,'automated','medium','ready',$4) RETURNING id`,
		projectID, *featureID, title, ref).Scan(&id); err != nil {
		return uuid.Nil, false, err
	}
	if err := snapshotTestCaseVersion(ctx, tx, projectID, id, actor); err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}
