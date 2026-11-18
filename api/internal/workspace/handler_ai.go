package workspace

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"spm-platform/api/internal/ai"
)

type askRequest struct {
	Question string `json:"question"`
	TopK     int    `json:"top_k"`
}

// AISearch handles GET /projects/:id/ai/search.
func (h *Handler) AISearch(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A search query is required")
		return
	}
	if h.ai == nil || !h.ai.Enabled() {
		apiError(c, http.StatusServiceUnavailable, "AI_UNAVAILABLE", "AI service is not configured")
		return
	}
	workspace, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load project context")
		return
	}
	response, err := h.ai.Search(c.Request.Context(), query, artifactDocuments(workspace), 12)
	if err != nil {
		apiError(c, http.StatusBadGateway, "AI_UNAVAILABLE", err.Error())
		return
	}
	c.JSON(http.StatusOK, response)
}

// AIAsk handles POST /projects/:id/ai/ask.
func (h *Handler) AIAsk(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req askRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Question) == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A question is required")
		return
	}
	if h.ai == nil || !h.ai.Enabled() {
		apiError(c, http.StatusServiceUnavailable, "AI_UNAVAILABLE", "AI service is not configured")
		return
	}
	workspace, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load project context")
		return
	}
	topK := req.TopK
	if topK <= 0 || topK > 20 {
		topK = 6
	}
	response, err := h.ai.Ask(c.Request.Context(), req.Question, artifactDocuments(workspace), topK)
	if err != nil {
		apiError(c, http.StatusBadGateway, "AI_UNAVAILABLE", err.Error())
		return
	}
	c.JSON(http.StatusOK, response)
}

// AIInsights handles GET /projects/:id/ai/insights.
func (h *Handler) AIInsights(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	if h.ai == nil || !h.ai.Enabled() {
		apiError(c, http.StatusServiceUnavailable, "AI_UNAVAILABLE", "AI service is not configured")
		return
	}
	workspace, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load project context")
		return
	}
	response, err := h.ai.Insights(c.Request.Context(), artifactDocuments(workspace), 10)
	if err != nil {
		apiError(c, http.StatusBadGateway, "AI_UNAVAILABLE", err.Error())
		return
	}
	c.JSON(http.StatusOK, response)
}

func artifactDocuments(workspace Workspace) []ai.Document {
	project := workspace.Project
	base := fmt.Sprintf("/projects/%s", project.ID)
	docs := make([]ai.Document, 0, 512)
	add := func(doc ai.Document) {
		doc.ProjectID = project.ID.String()
		doc.ProjectKey = project.KeyPrefix
		docs = append(docs, doc)
	}

	add(ai.Document{
		ID: project.ID.String(), Type: "project", Title: project.Name,
		Text: fmt.Sprintf("%s\n%s\nStatus: %s\nRepository: %s\nDefault branch: %s",
			project.Description, project.KeyPrefix, project.Status, project.RepoName, project.DefaultBranch),
		Status: project.Status, URL: base,
		Metadata: map[string]interface{}{"repo_name": project.RepoName},
	})

	for _, prd := range workspace.PRDs {
		add(ai.Document{
			ID: prd.ID.String(), Type: "prd", Title: prd.Title,
			Text: strings.TrimSpace(prd.Summary + "\n" + prd.Content), Status: prd.Status,
			URL:      base + "?tab=planning&prd=" + prd.ID.String(),
			Metadata: map[string]interface{}{"version": prd.Version},
		})
	}
	for _, epic := range workspace.Epics {
		add(ai.Document{
			ID: epic.ID.String(), Type: "epic", Title: epic.Title,
			Text: epic.Description, Status: epic.Status,
			URL:      base + "?tab=planning&epic=" + epic.ID.String(),
			Metadata: map[string]interface{}{"priority": epic.Priority},
		})
	}
	for _, feature := range workspace.Features {
		add(ai.Document{
			ID: feature.ID.String(), Type: "feature", Title: feature.Title,
			Text:   strings.TrimSpace(feature.Description + "\nAcceptance criteria: " + feature.AcceptanceCriteria),
			Status: feature.Status, URL: base + "?tab=planning&feature=" + feature.ID.String(),
			Metadata: map[string]interface{}{"priority": feature.Priority, "epic_id": feature.EpicID.String()},
		})
	}
	for _, item := range workspace.WorkItems {
		text := strings.TrimSpace(strings.Join([]string{
			item.Description, "Acceptance criteria: " + item.AcceptanceCriteria,
			"Reproduction: " + item.ReproductionSteps, "Expected: " + item.ExpectedResult,
			"Actual: " + item.ActualResult,
		}, "\n"))
		add(ai.Document{
			ID: item.ID.String(), Type: "work_item", Title: item.Key + " " + item.Title,
			Text: text, Status: item.Status, URL: base + "?tab=board&item=" + item.ID.String(),
			Metadata: map[string]interface{}{
				"item_type": item.Type, "priority": item.Priority,
				"stale_days": int(time.Since(item.UpdatedAt).Hours() / 24),
				"feature_id": uuidString(item.FeatureID), "parent_id": uuidString(item.ParentID),
			},
		})
	}
	for _, testCase := range workspace.TestCases {
		steps := make([]string, 0, len(testCase.Steps))
		for _, step := range testCase.Steps {
			steps = append(steps, step.Action+" -> "+step.ExpectedResult)
		}
		add(ai.Document{
			ID: testCase.ID.String(), Type: "test_case", Title: testCase.Title,
			Text:   strings.Join(append([]string{testCase.Description, testCase.Preconditions}, steps...), "\n"),
			Status: testCase.Status, URL: base + "?tab=tests&case=" + testCase.ID.String(),
			Metadata: map[string]interface{}{
				"feature_id": uuidString(testCase.FeatureID), "priority": testCase.Priority,
				"test_type": testCase.Type, "automation_ref": testCase.AutomationRef,
			},
		})
	}
	for _, run := range workspace.TestRuns {
		add(ai.Document{
			ID: run.ID.String(), Type: "test_run", Title: run.Name,
			Text:   fmt.Sprintf("Test run status: %s. Import source: %s", run.Status, run.ImportSource),
			Status: run.Status, URL: base + "?tab=tests&run=" + run.ID.String(),
			Metadata: map[string]interface{}{"build_id": uuidString(run.BuildID)},
		})
	}
	for _, repo := range workspace.Repositories {
		add(ai.Document{
			ID: repo.ID.String(), Type: "repository", Title: repo.Name,
			Text:   fmt.Sprintf("Provider: %s. URL: %s. Default branch: %s.", repo.Provider, repo.URL, repo.DefaultBranch),
			Status: "connected", URL: repo.URL,
			Metadata: map[string]interface{}{"provider": repo.Provider},
		})
	}
	for _, pr := range workspace.PullRequests {
		add(ai.Document{
			ID: pr.ID.String(), Type: "pull_request", Title: fmt.Sprintf("#%d %s", pr.Number, pr.Title),
			Text: pr.Description, Status: pr.Status,
			URL:      base + "?tab=code&pr=" + pr.ID.String(),
			Metadata: map[string]interface{}{"review_status": pr.ReviewStatus, "work_item_id": uuidString(pr.WorkItemID)},
		})
	}
	for _, build := range workspace.Builds {
		add(ai.Document{
			ID: build.ID.String(), Type: "build", Title: build.Provider + " " + build.Number,
			Text:   fmt.Sprintf("Build %s on %s for commit %s", build.Status, build.Branch, build.CommitSHA),
			Status: build.Status, URL: build.LogsURL,
			Metadata: map[string]interface{}{"provider": build.Provider, "branch": build.Branch},
		})
	}
	for _, deployment := range workspace.Deployments {
		add(ai.Document{
			ID: deployment.ID.String(), Type: "deployment", Title: deployment.Version,
			Text: fmt.Sprintf("Deployment %s, health %s, Argo app %s, revision %s",
				deployment.Status, deployment.HealthStatus, deployment.ArgoApp, deployment.ArgoRevision),
			Status: deployment.Status, URL: base + "?tab=delivery&deployment=" + deployment.ID.String(),
			Metadata: map[string]interface{}{"health_status": deployment.HealthStatus, "argo_app": deployment.ArgoApp},
		})
	}
	for _, check := range workspace.HealthChecks {
		add(ai.Document{
			ID: check.ID.String(), Type: "health_check", Title: check.URL,
			Text:   fmt.Sprintf("Health check %s HTTP %v", check.Status, check.HTTPStatus),
			Status: check.Status, URL: check.URL,
			Metadata: map[string]interface{}{"deployment_id": check.DeploymentID.String()},
		})
	}
	return docs
}

func uuidString(value *uuid.UUID) string {
	if value == nil {
		return ""
	}
	return value.String()
}
