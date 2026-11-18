package workspace

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type reportGate struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type releaseReadiness struct {
	ReleaseID uuid.UUID    `json:"release_id"`
	Name      string       `json:"name"`
	Version   string       `json:"version"`
	Status    string       `json:"status"`
	Ready     bool         `json:"ready"`
	Score     int          `json:"score"`
	Gates     []reportGate `json:"gates"`
}

type coverageRow struct {
	FeatureID   uuid.UUID `json:"feature_id"`
	Feature     string    `json:"feature"`
	Epic        string    `json:"epic"`
	PRD         string    `json:"prd"`
	TestCases   int       `json:"test_cases"`
	Passed      int       `json:"passed"`
	Failed      int       `json:"failed"`
	PassRate    float64   `json:"pass_rate"`
	OpenBugs    int       `json:"open_bugs"`
	DeliveryGap int       `json:"delivery_gaps"`
}

type reportResponse struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Metrics     Metrics            `json:"metrics"`
	Coverage    []coverageRow      `json:"coverage"`
	Releases    []releaseReadiness `json:"releases"`
}

func buildReports(w Workspace) reportResponse {
	resp := reportResponse{
		GeneratedAt: time.Now().UTC(),
		Metrics:     w.Metrics,
		Coverage:    []coverageRow{},
		Releases:    []releaseReadiness{},
	}
	epicByID := map[uuid.UUID]Epic{}
	for _, epic := range w.Epics {
		epicByID[epic.ID] = epic
	}
	prdByID := map[uuid.UUID]PRD{}
	for _, prd := range w.PRDs {
		prdByID[prd.ID] = prd
	}
	trace := buildTraceability(w)
	traceByFeature := map[uuid.UUID]traceabilityFeature{}
	for _, row := range trace.Features {
		traceByFeature[row.FeatureID] = row
	}
	latestByCase := map[uuid.UUID]TestResult{}
	for _, result := range w.TestResults {
		current, ok := latestByCase[result.TestCaseID]
		if !ok || result.UpdatedAt.After(current.UpdatedAt) {
			latestByCase[result.TestCaseID] = result
		}
	}
	casesByFeature := map[uuid.UUID][]TestCase{}
	for _, testCase := range w.TestCases {
		if testCase.FeatureID != nil {
			casesByFeature[*testCase.FeatureID] = append(casesByFeature[*testCase.FeatureID], testCase)
		}
	}
	bugsByFeature := map[uuid.UUID][]WorkItem{}
	for _, item := range w.WorkItems {
		if item.Type == TypeBug && item.FeatureID != nil {
			bugsByFeature[*item.FeatureID] = append(bugsByFeature[*item.FeatureID], item)
		}
	}
	for _, feature := range w.Features {
		row := coverageRow{FeatureID: feature.ID, Feature: feature.Title}
		if epic, ok := epicByID[feature.EpicID]; ok {
			row.Epic = epic.Title
			if epic.PRDID != nil {
				row.PRD = prdByID[*epic.PRDID].Title
			}
		}
		row.TestCases = len(casesByFeature[feature.ID])
		for _, testCase := range casesByFeature[feature.ID] {
			result, ok := latestByCase[testCase.ID]
			if !ok {
				continue
			}
			switch result.Status {
			case TestPassed:
				row.Passed++
			case TestFailed:
				row.Failed++
			}
		}
		if executed := row.Passed + row.Failed; executed > 0 {
			row.PassRate = float64(row.Passed) / float64(executed) * 100
		}
		for _, bug := range bugsByFeature[feature.ID] {
			if bug.Status != StatusClosed && bug.Status != StatusVerified && bug.Status != StatusCancelled {
				row.OpenBugs++
			}
		}
		if traceRow, ok := traceByFeature[feature.ID]; ok {
			row.DeliveryGap = len(traceRow.Gaps)
		}
		resp.Coverage = append(resp.Coverage, row)
	}

	for _, release := range w.Releases {
		resp.Releases = append(resp.Releases, computeReleaseReadiness(w, release, traceByFeature, latestByCase, casesByFeature, bugsByFeature))
	}
	return resp
}

func computeReleaseReadiness(w Workspace, release Release, trace map[uuid.UUID]traceabilityFeature, latest map[uuid.UUID]TestResult, cases map[uuid.UUID][]TestCase, bugs map[uuid.UUID][]WorkItem) releaseReadiness {
	row := releaseReadiness{ReleaseID: release.ID, Name: release.Name, Version: release.Version, Status: release.Status, Gates: []reportGate{}}
	features := map[uuid.UUID]Feature{}
	for _, feature := range w.Features {
		if feature.TargetReleaseID != nil && *feature.TargetReleaseID == release.ID {
			features[feature.ID] = feature
			continue
		}
		for _, epic := range w.Epics {
			if epic.ID == feature.EpicID && epic.TargetReleaseID != nil && *epic.TargetReleaseID == release.ID {
				features[feature.ID] = feature
			}
		}
	}
	for _, item := range w.WorkItems {
		if item.ReleaseID != nil && *item.ReleaseID == release.ID && item.FeatureID != nil {
			for _, feature := range w.Features {
				if feature.ID == *item.FeatureID {
					features[feature.ID] = feature
				}
			}
		}
	}
	if len(features) == 0 {
		row.Gates = append(row.Gates, reportGate{Key: "scope", Label: "Release scope", Status: "fail", Detail: "No features are linked to this release"})
	}
	testTotal, testPassed, testFailed, testExecuted := 0, 0, 0, 0
	openCritical, openBugs := 0, 0
	deliveryComplete := 0
	for featureID := range features {
		for _, testCase := range cases[featureID] {
			result, ok := latest[testCase.ID]
			if !ok {
				testTotal++
				continue
			}
			testTotal++
			if result.Status != TestNotRun {
				testExecuted++
			}
			switch result.Status {
			case TestPassed:
				testPassed++
			case TestFailed:
				testFailed++
			}
		}
		for _, bug := range bugs[featureID] {
			if bug.Status == StatusClosed || bug.Status == StatusVerified || bug.Status == StatusCancelled {
				continue
			}
			openBugs++
			if bug.Priority == PriorityCritical {
				openCritical++
			}
		}
		if traceRow, ok := trace[featureID]; ok {
			hasDelivery := traceRow.BranchCount > 0 && traceRow.PRCount > 0 && traceRow.BuildCount > 0 && traceRow.HealthStatus == "healthy"
			if hasDelivery {
				deliveryComplete++
			}
		}
	}
	if testTotal > 0 && testExecuted == testTotal && testFailed == 0 {
		row.Gates = append(row.Gates, reportGate{Key: "tests", Label: "Test evidence", Status: "pass", Detail: fmt.Sprintf("%d/%d latest test results passed", testPassed, testTotal)})
	} else if testTotal == 0 {
		row.Gates = append(row.Gates, reportGate{Key: "tests", Label: "Test evidence", Status: "fail", Detail: "No test cases are linked to the release scope"})
	} else if testExecuted < testTotal {
		row.Gates = append(row.Gates, reportGate{Key: "tests", Label: "Test evidence", Status: "fail", Detail: fmt.Sprintf("%d of %d test case(s) have no executed result", testTotal-testExecuted, testTotal)})
	} else {
		row.Gates = append(row.Gates, reportGate{Key: "tests", Label: "Test evidence", Status: "fail", Detail: fmt.Sprintf("%d failing test result(s); %d/%d passed", testFailed, testPassed, testTotal)})
	}
	if openCritical == 0 && openBugs == 0 {
		row.Gates = append(row.Gates, reportGate{Key: "bugs", Label: "Defect gate", Status: "pass", Detail: "No open defects in the release scope"})
	} else if openCritical > 0 {
		row.Gates = append(row.Gates, reportGate{Key: "bugs", Label: "Defect gate", Status: "fail", Detail: fmt.Sprintf("%d open critical bug(s), %d open in total", openCritical, openBugs)})
	} else {
		row.Gates = append(row.Gates, reportGate{Key: "bugs", Label: "Defect gate", Status: "warn", Detail: fmt.Sprintf("%d open non-critical bug(s)", openBugs)})
	}
	if len(features) == 0 {
		row.Gates = append(row.Gates, reportGate{Key: "delivery", Label: "Delivery evidence", Status: "fail", Detail: "No feature can be verified"})
	} else if deliveryComplete == len(features) {
		row.Gates = append(row.Gates, reportGate{Key: "delivery", Label: "Delivery evidence", Status: "pass", Detail: "Every feature has a branch, merged PR, successful build and healthy deployment"})
	} else {
		row.Gates = append(row.Gates, reportGate{Key: "delivery", Label: "Delivery evidence", Status: "fail", Detail: fmt.Sprintf("%d/%d features have complete delivery evidence", deliveryComplete, len(features))})
	}
	// A release can be signed off only when no gate is failing. Warnings stay
	// visible to the release manager and reduce the evidence score, but they do
	// not block readiness by themselves.
	passed := 0
	blocked := false
	for _, gate := range row.Gates {
		switch gate.Status {
		case "pass":
			passed++
		case "fail":
			blocked = true
		}
	}
	row.Ready = len(row.Gates) > 0 && !blocked
	if len(row.Gates) > 0 {
		row.Score = int(float64(passed) / float64(len(row.Gates)) * 100)
	}
	return row
}

// GetReports handles GET /projects/:id/reports.
func (h *Handler) GetReports(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	w, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
		return
	}
	resp := buildReports(w)
	if raw := strings.TrimSpace(c.Query("release_id")); raw != "" {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "release_id must be a UUID")
			return
		}
		filtered := make([]releaseReadiness, 0, 1)
		for _, release := range resp.Releases {
			if release.ReleaseID == id {
				filtered = append(filtered, release)
			}
		}
		resp.Releases = filtered
	}
	c.JSON(http.StatusOK, resp)
}

// GetReleaseReadiness handles GET /projects/:id/release-readiness.
func (h *Handler) GetReleaseReadiness(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	w, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
		return
	}
	resp := buildReports(w)
	if raw := strings.TrimSpace(c.Query("release_id")); raw != "" {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "release_id must be a UUID")
			return
		}
		for _, release := range resp.Releases {
			if release.ReleaseID == id {
				c.JSON(http.StatusOK, release)
				return
			}
		}
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Release not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"releases": resp.Releases})
}

// ExportReport handles GET /projects/:id/reports/export?format=csv|json&type=...
func (h *Handler) ExportReport(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	w, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
		return
	}
	report := buildReports(w)
	kind := strings.ToLower(strings.TrimSpace(c.DefaultQuery("type", "features")))
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "json")))
	filename := "spm-" + strings.ToLower(w.Project.KeyPrefix) + "-" + kind
	if format == "csv" {
		var buf bytes.Buffer
		writer := csv.NewWriter(&buf)
		rows := [][]string{}
		switch kind {
		case "features", "coverage":
			rows = append(rows, []string{"feature", "epic", "prd", "test_cases", "passed", "failed", "pass_rate", "open_bugs", "delivery_gaps"})
			for _, row := range report.Coverage {
				rows = append(rows, []string{row.Feature, row.Epic, row.PRD, strconv.Itoa(row.TestCases), strconv.Itoa(row.Passed), strconv.Itoa(row.Failed), fmt.Sprintf("%.2f", row.PassRate), strconv.Itoa(row.OpenBugs), strconv.Itoa(row.DeliveryGap)})
			}
		case "test-results":
			rows = append(rows, []string{"run", "test_case", "status", "actual_result", "duration_ms", "completed_at"})
			caseTitle := map[uuid.UUID]string{}
			for _, testCase := range w.TestCases {
				caseTitle[testCase.ID] = testCase.Title
			}
			runName := map[uuid.UUID]string{}
			for _, run := range w.TestRuns {
				runName[run.ID] = run.Name
			}
			for _, result := range w.TestResults {
				completed := ""
				if result.CompletedAt != nil {
					completed = result.CompletedAt.UTC().Format(time.RFC3339)
				}
				duration := ""
				if result.DurationMS != nil {
					duration = strconv.Itoa(*result.DurationMS)
				}
				rows = append(rows, []string{runName[result.TestRunID], caseTitle[result.TestCaseID], result.Status, result.ActualResult, duration, completed})
			}
		case "traceability":
			trace := buildTraceability(w)
			rows = append(rows, []string{"feature", "stories", "tasks", "test_cases", "last_run_status", "open_bugs", "branches", "pull_requests", "builds", "deployments", "health", "gaps"})
			for _, row := range trace.Features {
				rows = append(rows, []string{row.FeatureTitle, strconv.Itoa(row.Stories), strconv.Itoa(row.Tasks), strconv.Itoa(row.TestCases), row.LastRunStatus, strconv.Itoa(row.OpenBugs), strconv.Itoa(row.BranchCount), strconv.Itoa(row.PRCount), strconv.Itoa(row.BuildCount), strconv.Itoa(row.Deployments), row.HealthStatus, strings.Join(row.Gaps, "; ")})
			}
		default:
			rows = append(rows, []string{"metric", "value"})
			rows = append(rows,
				[]string{"pass_rate", fmt.Sprintf("%.2f", report.Metrics.PassRate)},
				[]string{"coverage_percent", fmt.Sprintf("%.2f", report.Metrics.CoveragePercent)},
				[]string{"open_bugs", strconv.Itoa(report.Metrics.OpenBugCount)},
				[]string{"flaky_tests", strconv.Itoa(report.Metrics.FlakyTestCount)},
				[]string{"cycle_time_hours", fmt.Sprintf("%.2f", report.Metrics.CycleTimeHours)},
				[]string{"delivery_confidence", fmt.Sprintf("%.2f", report.Metrics.DeliveryConfidence)},
			)
		}
		_ = writer.WriteAll(rows)
		writer.Flush()
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, filename))
		c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
		return
	}
	var payload interface{} = report
	switch kind {
	case "traceability":
		payload = buildTraceability(w)
	case "releases", "readiness":
		payload = report.Releases
	case "test-results":
		payload = w.TestResults
	case "metrics", "summary":
		payload = report.Metrics
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to encode report")
		return
	}
	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, filename))
	c.Data(http.StatusOK, "application/json", body)
}
