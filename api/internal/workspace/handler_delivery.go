package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/gitengine"
)

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Repositories, branches and pull requests
// ---------------------------------------------------------------------------

type repositoryRequest struct {
	Provider      string `json:"provider"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	DefaultBranch string `json:"default_branch"`
	ExternalID    string `json:"external_id"`
}

func (h *Handler) CreateRepository(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req repositoryRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Repository name is required")
		return
	}
	if req.Provider == "" {
		req.Provider = "gitea"
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}
	token := randomWebhookToken()
	webhookURL := strings.TrimRight(os.Getenv("SPM_WEBHOOK_BASE_URL"), "/")
	if webhookURL == "" {
		webhookURL = "http://localhost:8080"
	}
	webhookURL += "/api/v1/webhooks/git/" + req.Provider
	var id uuid.UUID
	err := h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO git_repositories (project_id, provider, name, url, default_branch, external_id, webhook_token, webhook_url)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (project_id, name) DO UPDATE SET provider=EXCLUDED.provider, url=EXCLUDED.url,
			default_branch=EXCLUDED.default_branch, external_id=EXCLUDED.external_id,
			webhook_token=CASE WHEN git_repositories.webhook_token='' THEN EXCLUDED.webhook_token ELSE git_repositories.webhook_token END,
			webhook_url=EXCLUDED.webhook_url, updated_at=NOW()
		RETURNING id`,
		projectID, req.Provider, req.Name, req.URL, req.DefaultBranch, req.ExternalID, token, webhookURL).Scan(&id)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create repository")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "repository", &id, "created", "Linked repository "+req.Name, nil)
	c.JSON(http.StatusCreated, gin.H{"id": id, "webhook_token": token, "webhook_url": webhookURL})
}

type branchRequest struct {
	RepositoryID  uuid.UUID  `json:"repository_id"`
	WorkItemID    *uuid.UUID `json:"work_item_id"`
	Name          string     `json:"name"`
	HeadCommitSHA string     `json:"head_commit_sha"`
	CreatedBy     *uuid.UUID `json:"created_by"`
}

func (h *Handler) CreateBranch(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req branchRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Branch name is required")
		return
	}
	if req.WorkItemID == nil {
		apiError(c, http.StatusUnprocessableEntity, "TRACEABILITY_ERROR", "A branch must link to a work item")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "git_repositories", projectID, req.RepositoryID); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Repository does not belong to this project")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "work_items", projectID, *req.WorkItemID); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Work item does not belong to this project")
		return
	}
	repoName, defaultBranch, provider, err := h.repositoryRemote(c.Request.Context(), projectID, req.RepositoryID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load repository")
		return
	}
	if provider == "gitea" && h.git != nil {
		if err := h.git.CreateBranch(c.Request.Context(), repoName, req.Name, defaultBranch); err != nil {
			if !errors.Is(err, gitengine.ErrBranchExists) {
				apiError(c, http.StatusUnprocessableEntity, "GIT_BRANCH_FAILED", err.Error())
				return
			}
		}
		if sha, lookupErr := h.git.GetRepositoryBranch(c.Request.Context(), repoName, req.Name); lookupErr == nil {
			req.HeadCommitSHA = sha
		}
	}
	var id uuid.UUID
	err = h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO git_branches (repository_id, work_item_id, name, head_commit_sha, created_by)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		req.RepositoryID, req.WorkItemID, req.Name, req.HeadCommitSHA, req.CreatedBy).Scan(&id)
	if err != nil {
		apiError(c, http.StatusConflict, "BRANCH_EXISTS", "Branch is already linked to this project")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "branch", &id, "created", "Created branch "+req.Name, nil)
	c.JSON(http.StatusCreated, gin.H{"id": id, "name": req.Name, "head_commit_sha": req.HeadCommitSHA})
}

func (h *Handler) repositoryRemote(ctx context.Context, projectID, repositoryID uuid.UUID) (string, string, string, error) {
	var name, defaultBranch, provider string
	err := h.store.DB().QueryRow(ctx, `
		SELECT name, COALESCE(NULLIF(default_branch, ''), 'main'), provider
		FROM git_repositories WHERE id=$1 AND project_id=$2`, repositoryID, projectID).
		Scan(&name, &defaultBranch, &provider)
	return name, defaultBranch, provider, err
}

type pullRequestRequest struct {
	RepositoryID   uuid.UUID  `json:"repository_id"`
	WorkItemID     *uuid.UUID `json:"work_item_id"`
	Number         int        `json:"number"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	SourceBranchID *uuid.UUID `json:"source_branch_id"`
	TargetBranch   string     `json:"target_branch"`
	Author         string     `json:"author"`
	Status         string     `json:"status"`
	ReviewStatus   string     `json:"review_status"`
	MergeCommitSHA string     `json:"merge_commit_sha"`
}

func (h *Handler) CreatePullRequest(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req pullRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Title) == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Pull request title is required")
		return
	}
	if req.WorkItemID == nil {
		apiError(c, http.StatusUnprocessableEntity, "TRACEABILITY_ERROR", "A pull request must link to a work item")
		return
	}
	if req.TargetBranch == "" {
		req.TargetBranch = "main"
	}
	if req.Status == "" {
		req.Status = "open"
	}
	if req.ReviewStatus == "" {
		req.ReviewStatus = "pending"
	}
	if !oneOf(req.Status, "open", "draft", "merged", "closed") {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Pull request status is not valid")
		return
	}
	if !oneOf(req.ReviewStatus, "pending", "approved", "changes_requested") {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Review status is not valid")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "git_repositories", projectID, req.RepositoryID); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Repository does not belong to this project")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "work_items", projectID, *req.WorkItemID); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Work item does not belong to this project")
		return
	}
	if req.SourceBranchID != nil {
		if err := h.ensureBelongs(c.Request.Context(), "git_branches", projectID, *req.SourceBranchID); err != nil {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Source branch does not belong to this project")
			return
		}
	}

	repoName, _, provider, err := h.repositoryRemote(c.Request.Context(), projectID, req.RepositoryID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load repository")
		return
	}
	sourceBranch, err := h.branchName(c.Request.Context(), projectID, req.RepositoryID, req.SourceBranchID, *req.WorkItemID)
	if err != nil || sourceBranch == "" {
		apiError(c, http.StatusUnprocessableEntity, "TRACEABILITY_ERROR", "Select a source branch created from the linked work item")
		return
	}
	htmlURL := ""
	if provider == "gitea" && h.git != nil {
		result, gitErr := h.git.CreatePullRequest(c.Request.Context(), repoName, req.Title, sourceBranch, req.TargetBranch, req.Description)
		if gitErr != nil {
			apiError(c, http.StatusUnprocessableEntity, "GIT_PR_FAILED", gitErr.Error())
			return
		}
		req.Number = result.Number
		htmlURL = result.HTMLURL
	}
	if req.Number <= 0 {
		apiError(c, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "A pull request number could not be allocated")
		return
	}
	var id uuid.UUID
	err = h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO pull_requests (repository_id, work_item_id, number, title, description, source_branch_id,
		target_branch, author, status, review_status, merge_commit_sha)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
		req.RepositoryID, req.WorkItemID, req.Number, req.Title, req.Description, req.SourceBranchID,
		req.TargetBranch, req.Author, req.Status, req.ReviewStatus, req.MergeCommitSHA).Scan(&id)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create pull request")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "pull_request", &id, "created", "Opened PR #"+itoa(req.Number)+": "+req.Title, nil)
	c.JSON(http.StatusCreated, gin.H{"id": id, "number": req.Number, "html_url": htmlURL, "source_branch": sourceBranch})
}

func (h *Handler) branchName(ctx context.Context, projectID, repositoryID uuid.UUID, branchID *uuid.UUID, workItemID uuid.UUID) (string, error) {
	var name string
	if branchID != nil {
		err := h.store.DB().QueryRow(ctx, `
			SELECT b.name FROM git_branches b JOIN git_repositories r ON r.id=b.repository_id
			WHERE b.id=$1 AND b.repository_id=$2 AND r.project_id=$3`, *branchID, repositoryID, projectID).Scan(&name)
		if err == nil {
			return name, nil
		}
	}
	err := h.store.DB().QueryRow(ctx, `
		SELECT b.name FROM git_branches b JOIN git_repositories r ON r.id=b.repository_id
		WHERE b.work_item_id=$1 AND b.repository_id=$2 AND r.project_id=$3
		ORDER BY b.updated_at DESC LIMIT 1`, workItemID, repositoryID, projectID).Scan(&name)
	return name, err
}

type pullRequestUpdate struct {
	Title          string `json:"title"`
	Description    string `json:"description"`
	Status         string `json:"status"`
	ReviewStatus   string `json:"review_status"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	TargetBranch   string `json:"target_branch"`
}

// MergePullRequest merges the remote Gitea pull request and records the outcome.
func (h *Handler) MergePullRequest(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	prID, ok := parseParamID(c, "prId")
	if !ok {
		return
	}
	var req struct {
		MergeCommitSHA string `json:"merge_commit_sha"`
	}
	_ = c.ShouldBindJSON(&req)

	var status, reviewStatus, provider, repoName, mergeSHA, targetBranch string
	var number int
	var repoID uuid.UUID
	var workItemID, branchID *uuid.UUID
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT p.status, p.review_status, p.number, p.repository_id, p.work_item_id, p.source_branch_id,
		       COALESCE(p.merge_commit_sha,''), r.provider, r.name, COALESCE(NULLIF(p.target_branch,''),'main')
		FROM pull_requests p JOIN git_repositories r ON r.id=p.repository_id
		WHERE p.id=$1 AND r.project_id=$2`, prID, projectID).
		Scan(&status, &reviewStatus, &number, &repoID, &workItemID, &branchID, &mergeSHA, &provider, &repoName, &targetBranch)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Pull request not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load pull request")
		return
	}
	if status == "merged" {
		c.JSON(http.StatusOK, gin.H{"id": prID, "status": "merged", "number": number})
		return
	}
	if req.MergeCommitSHA == "" {
		req.MergeCommitSHA = mergeSHA
	}
	if req.MergeCommitSHA == "" && branchID != nil {
		_ = h.store.DB().QueryRow(c.Request.Context(), `SELECT COALESCE(head_commit_sha,'') FROM git_branches WHERE id=$1`, *branchID).Scan(&req.MergeCommitSHA)
	}
	if blocked := h.checkMergePolicy(c.Request.Context(), projectID, repoID, workItemID, branchID, reviewStatus, req.MergeCommitSHA); len(blocked) > 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":   errorBody{Code: "MERGE_POLICY_BLOCKED", Message: "Merge blocked: " + strings.Join(blocked, "; ")},
			"details": blocked,
		})
		return
	}
	if provider == "gitea" && h.git != nil {
		if err := h.git.MergePullRequest(c.Request.Context(), repoName, number); err != nil {
			apiError(c, http.StatusUnprocessableEntity, "GIT_MERGE_FAILED", err.Error())
			return
		}
		// The merge commit is created by Gitea; use the target branch head as
		// the authoritative SHA so the queued build and traceability evidence
		// point at the commit that is actually on the protected branch.
		if sha, lookupErr := h.git.GetRepositoryBranch(c.Request.Context(), repoName, targetBranch); lookupErr == nil && sha != "" {
			req.MergeCommitSHA = sha
		}
	}
	if _, err := h.store.DB().Exec(c.Request.Context(), `
		UPDATE pull_requests SET status='merged', review_status='approved',
			merge_commit_sha=CASE WHEN $3 <> '' THEN $3 ELSE merge_commit_sha END,
			merged_at=COALESCE(merged_at,NOW()), updated_at=NOW()
		WHERE id=$1 AND repository_id IN (SELECT id FROM git_repositories WHERE project_id=$2)`,
		prID, projectID, req.MergeCommitSHA); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to record merge")
		return
	}
	if branchID != nil {
		_, _ = h.store.DB().Exec(c.Request.Context(), `UPDATE git_branches SET status='merged', merged_at=NOW(), updated_at=NOW() WHERE id=$1`, *branchID)
	}
	if workItemID != nil {
		_, _ = h.store.DB().Exec(c.Request.Context(), `
			UPDATE work_items
			SET status = CASE WHEN type='bug' THEN 'fixed' ELSE 'in_review' END, updated_at = NOW()
			WHERE id=$1 AND status NOT IN ('done','closed','cancelled','verified','fixed')`, *workItemID)
	}
	// Builds are created only by the Gitea Actions reconciliation path.
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "pull_request", &prID, "merged",
		"Merged PR #"+itoa(number), nil)
	c.JSON(http.StatusOK, gin.H{"id": prID, "status": "merged", "number": number})
}

func (h *Handler) UpdatePullRequest(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	prID, ok := parseParamID(c, "prId")
	if !ok {
		return
	}
	var req pullRequestUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	var currentStatus, currentReview string
	var workItemID *uuid.UUID
	var branchID *uuid.UUID
	var repoID uuid.UUID
	var number int
	var mergeSHA, provider, repoName, storedTargetBranch string
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT p.status, p.review_status, p.work_item_id, p.source_branch_id, p.repository_id, p.number,
		       COALESCE(p.merge_commit_sha,''), r.provider, r.name, COALESCE(NULLIF(p.target_branch,''),'main')
		FROM pull_requests p JOIN git_repositories r ON r.id=p.repository_id
		WHERE p.id=$1 AND r.project_id=$2`, prID, projectID).
		Scan(&currentStatus, &currentReview, &workItemID, &branchID, &repoID, &number, &mergeSHA, &provider, &repoName, &storedTargetBranch)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Pull request not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load pull request")
		return
	}
	if req.Status == "" {
		req.Status = currentStatus
	}
	if req.ReviewStatus == "" {
		req.ReviewStatus = currentReview
	}
	if req.MergeCommitSHA == "" {
		req.MergeCommitSHA = mergeSHA
	}
	if req.MergeCommitSHA == "" && branchID != nil {
		_ = h.store.DB().QueryRow(c.Request.Context(), `SELECT COALESCE(head_commit_sha,'') FROM git_branches WHERE id=$1`, *branchID).Scan(&req.MergeCommitSHA)
	}
	if req.TargetBranch == "" {
		req.TargetBranch = storedTargetBranch
	}
	if req.TargetBranch == "" {
		req.TargetBranch = "main"
	}
	if !oneOf(req.Status, "open", "draft", "merged", "closed") {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Pull request status is not valid")
		return
	}
	if !oneOf(req.ReviewStatus, "pending", "approved", "changes_requested") {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Review status is not valid")
		return
	}
	if req.Status == "merged" {
		req.ReviewStatus = "approved"
	}
	if req.Status == "merged" && currentStatus != "merged" {
		if blocked := h.checkMergePolicy(c.Request.Context(), projectID, repoID, workItemID, branchID, req.ReviewStatus, req.MergeCommitSHA); len(blocked) > 0 {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":   errorBody{Code: "MERGE_POLICY_BLOCKED", Message: "Merge blocked: " + strings.Join(blocked, "; ")},
				"details": blocked,
			})
			return
		}
		if provider == "gitea" && h.git != nil {
			if err := h.git.MergePullRequest(c.Request.Context(), repoName, number); err != nil {
				apiError(c, http.StatusUnprocessableEntity, "GIT_MERGE_FAILED", err.Error())
				return
			}
			if sha, lookupErr := h.git.GetRepositoryBranch(c.Request.Context(), repoName, req.TargetBranch); lookupErr == nil && sha != "" {
				req.MergeCommitSHA = sha
			}
		}
	}
	var mergedAt interface{}
	if req.Status == "merged" {
		mergedAt = time.Now().UTC()
	}
	tag, err := h.store.DB().Exec(c.Request.Context(), `
		UPDATE pull_requests SET title=$3, description=$4, status=$5, review_status=$6,
		merge_commit_sha=$7, target_branch=$8, merged_at=COALESCE($9, merged_at), updated_at=NOW()
		WHERE id=$1 AND repository_id=$2`,
		prID, repoID, req.Title, req.Description, req.Status, req.ReviewStatus, req.MergeCommitSHA,
		req.TargetBranch, mergedAt)
	if err != nil || tag.RowsAffected() == 0 {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update pull request")
		return
	}

	if req.Status == "merged" && currentStatus != "merged" {
		if branchID != nil {
			_, _ = h.store.DB().Exec(c.Request.Context(), `UPDATE git_branches SET status='merged', merged_at=NOW(), updated_at=NOW() WHERE id=$1`, *branchID)
		}
		if workItemID != nil {
			// A merge moves a story/task into review, while a bug fix is ready
			// for verification. Terminal states are left untouched.
			_, _ = h.store.DB().Exec(c.Request.Context(), `
				UPDATE work_items
				SET status = CASE WHEN type='bug' THEN 'fixed' ELSE 'in_review' END,
				    updated_at = NOW()
				WHERE id=$1 AND status NOT IN ('done','closed','cancelled','verified','fixed')`, *workItemID)
		}
	}
	// Builds are created only by the Gitea Actions reconciliation path.
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "pull_request", &prID, "updated",
		"PR #"+itoa(number)+" is now "+req.Status, map[string]interface{}{"status": req.Status})
	c.JSON(http.StatusOK, gin.H{"id": prID, "status": req.Status})
}

// ---------------------------------------------------------------------------
// Builds
// ---------------------------------------------------------------------------

type buildRequest struct {
	RepositoryID *uuid.UUID             `json:"repository_id"`
	CommitSHA    string                 `json:"commit_sha"`
	Branch       string                 `json:"branch"`
	Provider     string                 `json:"provider"`
	ExternalID   string                 `json:"external_id"`
	Number       string                 `json:"number"`
	Status       string                 `json:"status"`
	LogsURL      string                 `json:"logs_url"`
	Metadata     map[string]interface{} `json:"metadata"`
	TriggeredBy  *uuid.UUID             `json:"triggered_by"`
}

func (h *Handler) CreateBuild(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req buildRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if req.Status == "" {
		req.Status = "queued"
	}
	if !oneOf(req.Status, "queued", "running", "success", "failure", "cancelled", "timed_out") {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Build status is not valid")
		return
	}
	if req.Provider == "" {
		req.Provider = "generic"
	}
	if req.RepositoryID != nil {
		if err := h.ensureBelongs(c.Request.Context(), "git_repositories", projectID, *req.RepositoryID); err != nil {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Repository does not belong to this project")
			return
		}
	}
	var startedAt interface{}
	if req.Status == "running" || req.Status == "success" || req.Status == "failure" {
		startedAt = time.Now().UTC()
	}
	metadata := req.Metadata
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	var id uuid.UUID
	err := h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO builds (project_id, repository_id, commit_sha, branch, provider, external_id, number,
		status, logs_url, metadata, triggered_by, started_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		projectID, req.RepositoryID, req.CommitSHA, req.Branch, req.Provider, req.ExternalID,
		req.Number, req.Status, req.LogsURL, metadata, req.TriggeredBy, startedAt).Scan(&id)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create build")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "build", &id, "created", "Build "+req.Number+" is "+req.Status, nil)
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

type buildUpdate struct {
	Status     string `json:"status"`
	LogsURL    string `json:"logs_url"`
	ExternalID string `json:"external_id"`
	Number     string `json:"number"`
}

func (h *Handler) UpdateBuild(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	buildID, ok := parseParamID(c, "buildId")
	if !ok {
		return
	}
	var req buildUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if req.Status == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Build status is required")
		return
	}
	if !oneOf(req.Status, "queued", "running", "success", "failure", "cancelled", "timed_out") {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Build status is not valid")
		return
	}
	var completedAt interface{}
	if req.Status == "success" || req.Status == "failure" || req.Status == "cancelled" || req.Status == "timed_out" {
		completedAt = time.Now().UTC()
	}
	tag, err := h.store.DB().Exec(c.Request.Context(), `
		UPDATE builds SET status=$3, logs_url=COALESCE(NULLIF($4,''), logs_url),
		external_id=COALESCE(NULLIF($5,''), external_id), number=COALESCE(NULLIF($6,''), number),
		completed_at=COALESCE($7, completed_at), updated_at=NOW()
		WHERE id=$1 AND project_id=$2`, buildID, projectID, req.Status, req.LogsURL, req.ExternalID, req.Number, completedAt)
	if err != nil || tag.RowsAffected() == 0 {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Build not found")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "build", &buildID, "status_changed", "Build is now "+req.Status, map[string]interface{}{"status": req.Status})
	c.JSON(http.StatusOK, gin.H{"id": buildID, "status": req.Status})
}

// ---------------------------------------------------------------------------
// Environments, deployments and health checks
// ---------------------------------------------------------------------------

type environmentRequest struct {
	Name             string `json:"name"`
	Type             string `json:"type"`
	URL              string `json:"url"`
	HealthCheckURL   string `json:"health_check_url"`
	DeployStrategy   string `json:"deploy_strategy"`
	RequiresApproval bool   `json:"requires_approval"`
	Position         int    `json:"position"`
}

func (h *Handler) CreateEnvironment(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req environmentRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Environment name is required")
		return
	}
	if req.Type == "" {
		req.Type = "custom"
	}
	if !oneOf(req.Type, "development", "staging", "production", "custom") {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Environment type is not valid")
		return
	}
	// Developers hold delivery.deploy for non-production environments only.
	// There is no deployment or rollback write route yet, so environment
	// creation is where production must be refused.
	if req.Type == "production" && auth.ProjectRoleFrom(c) == auth.ProjectRoleDeveloper {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Developers can manage non-production environments only")
		return
	}
	if req.DeployStrategy == "" {
		req.DeployStrategy = "rolling"
	}
	var id uuid.UUID
	err := h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO environments (project_id, name, type, url, health_check_url, deploy_strategy, requires_approval, position)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		projectID, req.Name, req.Type, req.URL, req.HealthCheckURL, req.DeployStrategy, req.RequiresApproval, req.Position).Scan(&id)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create environment")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "environment", &id, "created", "Created environment "+req.Name, nil)
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

type deploymentRequest struct {
	EnvironmentID uuid.UUID              `json:"environment_id"`
	BuildID       *uuid.UUID             `json:"build_id"`
	ReleaseID     *uuid.UUID             `json:"release_id"`
	Status        string                 `json:"status"`
	HealthStatus  string                 `json:"health_status"`
	Version       string                 `json:"version"`
	ArgoApp       string                 `json:"argo_app"`
	ArgoRevision  string                 `json:"argo_revision"`
	Metadata      map[string]interface{} `json:"metadata"`
	DeployedBy    *uuid.UUID             `json:"deployed_by"`
}

func (h *Handler) CreateDeployment(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req deploymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if req.EnvironmentID == uuid.Nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Deployment environment is required")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "environments", projectID, req.EnvironmentID); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Environment does not belong to this project")
		return
	}
	if req.BuildID == nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A deployment must reference a build")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), "builds", projectID, *req.BuildID); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Build does not belong to this project")
		return
	}
	var environmentType string
	if err := h.store.DB().QueryRow(c.Request.Context(), `SELECT type FROM environments WHERE id=$1`, req.EnvironmentID).Scan(&environmentType); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load environment")
		return
	}
	if environmentType == "production" && req.ReleaseID == nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A production deployment must reference a release")
		return
	}
	if req.ReleaseID != nil {
		if err := h.ensureBelongs(c.Request.Context(), "releases", projectID, *req.ReleaseID); err != nil {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Release does not belong to this project")
			return
		}
	}
	if req.Status == "" {
		req.Status = "queued"
	}
	if !oneOf(req.Status, "queued", "deploying", "verifying", "healthy", "degraded", "failed", "rolled_back", "cancelled") {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Deployment status is not valid")
		return
	}
	if req.HealthStatus == "" {
		req.HealthStatus = "unknown"
	}
	if !oneOf(req.HealthStatus, "unknown", "healthy", "degraded", "unhealthy") {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Health status is not valid")
		return
	}
	if req.Status == "healthy" && h.healthCheckRequired(c.Request.Context(), projectID) {
		apiError(c, http.StatusUnprocessableEntity, "HEALTH_CHECK_REQUIRED", "Start the deployment first, then record a healthy health check")
		return
	}
	var previous *uuid.UUID
	var prev uuid.UUID
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT id FROM deployments WHERE project_id=$1 AND environment_id=$2 ORDER BY created_at DESC LIMIT 1`,
		projectID, req.EnvironmentID).Scan(&prev)
	if err == nil {
		previous = &prev
	}
	var startedAt interface{}
	if req.Status != "queued" {
		startedAt = time.Now().UTC()
	}
	metadata := req.Metadata
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	var id uuid.UUID
	if req.ArgoApp == "" {
		req.ArgoApp = ""
	}
	err = h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO deployments (project_id, environment_id, build_id, release_id, status, health_status,
		version, argo_app, argo_revision, metadata, deployed_by, previous_deployment_id, started_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
		projectID, req.EnvironmentID, req.BuildID, req.ReleaseID, req.Status, req.HealthStatus,
		req.Version, req.ArgoApp, req.ArgoRevision, metadata, req.DeployedBy, previous, startedAt).Scan(&id)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create deployment")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "deployment", &id, "created",
		"Deployment "+req.Version+" is "+req.Status, map[string]interface{}{"environment_id": req.EnvironmentID})
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

type deploymentUpdate struct {
	Status       string                 `json:"status"`
	HealthStatus string                 `json:"health_status"`
	Version      string                 `json:"version"`
	ArgoApp      string                 `json:"argo_app"`
	ArgoRevision string                 `json:"argo_revision"`
	Metadata     map[string]interface{} `json:"metadata"`
}

func (h *Handler) healthCheckRequired(ctx context.Context, projectID uuid.UUID) bool {
	var raw []byte
	if err := h.store.DB().QueryRow(ctx, `SELECT COALESCE(settings,'{}'::jsonb) FROM projects WHERE id=$1`, projectID).Scan(&raw); err != nil {
		return false
	}
	var settings struct {
		RequireHealthCheck bool `json:"require_health_check"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return false
	}
	return settings.RequireHealthCheck
}

func (h *Handler) hasHealthyCheck(ctx context.Context, deploymentID uuid.UUID) bool {
	var exists bool
	_ = h.store.DB().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM health_checks WHERE deployment_id=$1 AND status='healthy')`, deploymentID).Scan(&exists)
	return exists
}

func (h *Handler) UpdateDeployment(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	deploymentID, ok := parseParamID(c, "deploymentId")
	if !ok {
		return
	}
	var req deploymentUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if req.Status != "" && !oneOf(req.Status, "queued", "deploying", "verifying", "healthy", "degraded", "failed", "rolled_back", "cancelled") {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Deployment status is not valid")
		return
	}
	if req.HealthStatus != "" && !oneOf(req.HealthStatus, "unknown", "healthy", "degraded", "unhealthy") {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Health status is not valid")
		return
	}
	if req.Status == "healthy" && h.healthCheckRequired(c.Request.Context(), projectID) && !h.hasHealthyCheck(c.Request.Context(), deploymentID) {
		apiError(c, http.StatusUnprocessableEntity, "HEALTH_CHECK_REQUIRED", "A healthy health check must be recorded before this deployment can be completed")
		return
	}
	var completedAt interface{}
	if req.Status == "healthy" || req.Status == "failed" || req.Status == "degraded" || req.Status == "rolled_back" || req.Status == "cancelled" {
		completedAt = time.Now().UTC()
	}
	tag, err := h.store.DB().Exec(c.Request.Context(), `
		UPDATE deployments SET status=COALESCE(NULLIF($3,''), status),
		health_status=COALESCE(NULLIF($4,''), health_status),
		version=COALESCE(NULLIF($5,''), version),
		argo_app=COALESCE(NULLIF($6,''), argo_app),
		argo_revision=COALESCE(NULLIF($7,''), argo_revision),
		metadata=CASE WHEN COALESCE($8::jsonb, '{}'::jsonb) = '{}'::jsonb THEN metadata ELSE $8 END,
		completed_at=COALESCE($9, completed_at), updated_at=NOW()
		WHERE id=$1 AND project_id=$2`, deploymentID, projectID, req.Status, req.HealthStatus, req.Version,
		req.ArgoApp, req.ArgoRevision, req.Metadata, completedAt)
	if err != nil || tag.RowsAffected() == 0 {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Deployment not found")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "deployment", &deploymentID, "status_changed",
		"Deployment is now "+req.Status, map[string]interface{}{"status": req.Status})
	c.JSON(http.StatusOK, gin.H{"id": deploymentID, "status": req.Status})
}

type healthCheckRequest struct {
	URL        string                 `json:"url"`
	Status     string                 `json:"status"`
	HTTPStatus *int                   `json:"http_status"`
	LatencyMS  *int                   `json:"latency_ms"`
	Details    map[string]interface{} `json:"details"`
}

func (h *Handler) CreateHealthCheck(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	deploymentID, ok := parseParamID(c, "deploymentId")
	if !ok {
		return
	}
	var req healthCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if req.Status == "" {
		req.Status = "unknown"
	}
	if !oneOf(req.Status, "unknown", "healthy", "degraded", "unhealthy") {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Health check status is not valid")
		return
	}
	var exists bool
	if err := h.store.DB().QueryRow(c.Request.Context(), `SELECT EXISTS (SELECT 1 FROM deployments WHERE id=$1 AND project_id=$2)`, deploymentID, projectID).Scan(&exists); err != nil || !exists {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Deployment not found")
		return
	}
	details := req.Details
	if details == nil {
		details = map[string]interface{}{}
	}
	var id uuid.UUID
	err := h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO health_checks (deployment_id, url, status, http_status, latency_ms, details)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		deploymentID, req.URL, req.Status, req.HTTPStatus, req.LatencyMS, details).Scan(&id)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create health check")
		return
	}
	deployStatus := ""
	switch req.Status {
	case "healthy":
		deployStatus = "healthy"
	case "degraded", "unhealthy":
		deployStatus = "degraded"
	}
	if deployStatus != "" {
		_, _ = h.store.DB().Exec(c.Request.Context(), `
			UPDATE deployments SET health_status=$2, status=$3,
			completed_at=CASE WHEN $4 THEN NOW() ELSE completed_at END, updated_at=NOW()
			WHERE id=$1`, deploymentID, req.Status, deployStatus, req.Status == "healthy")
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "health_check", &id, "recorded",
		"Health check is "+req.Status, map[string]interface{}{"deployment_id": deploymentID, "status": req.Status})
	c.JSON(http.StatusCreated, gin.H{"id": id, "deployment_status": deployStatus})
}

type rollbackRequest struct {
	Reason string `json:"reason"`
}

func (h *Handler) RollbackDeployment(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	deploymentID, ok := parseParamID(c, "deploymentId")
	if !ok {
		return
	}
	var req rollbackRequest
	_ = c.ShouldBindJSON(&req)

	var envID uuid.UUID
	var previousID *uuid.UUID
	var failedStatus string
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT environment_id, previous_deployment_id, status FROM deployments WHERE id=$1 AND project_id=$2`,
		deploymentID, projectID).Scan(&envID, &previousID, &failedStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Deployment not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load deployment")
		return
	}
	if previousID == nil {
		apiError(c, http.StatusUnprocessableEntity, "NO_ROLLBACK_TARGET", "No previous deployment is available")
		return
	}
	var buildID, releaseID *uuid.UUID
	var version string
	err = h.store.DB().QueryRow(c.Request.Context(), `
		SELECT build_id, release_id, COALESCE(version,'') FROM deployments WHERE id=$1`, *previousID).Scan(&buildID, &releaseID, &version)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load previous deployment")
		return
	}
	var newID uuid.UUID
	err = h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO deployments (project_id, environment_id, build_id, release_id, status, health_status,
		version, previous_deployment_id, rollback_of_id, started_at)
		VALUES ($1,$2,$3,$4,'deploying','unknown',$5,$6,$7,NOW()) RETURNING id`,
		projectID, envID, buildID, releaseID, version, *previousID, deploymentID).Scan(&newID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create rollback deployment")
		return
	}
	_, _ = h.store.DB().Exec(c.Request.Context(), `UPDATE deployments SET status='rolled_back', updated_at=NOW() WHERE id=$1`, deploymentID)
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "deployment", &newID, "rollback_started",
		"Rolled back deployment to "+version+": "+req.Reason, map[string]interface{}{"rollback_of": deploymentID, "previous": *previousID})
	c.JSON(http.StatusCreated, gin.H{"id": newID, "rollback_of": deploymentID})
}

// ---------------------------------------------------------------------------
// Generic links
// ---------------------------------------------------------------------------

type linkRequest struct {
	SourceType string    `json:"source_type"`
	SourceID   uuid.UUID `json:"source_id"`
	TargetType string    `json:"target_type"`
	TargetID   uuid.UUID `json:"target_id"`
	LinkType   string    `json:"link_type"`
}

func (h *Handler) CreateLink(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req linkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if req.SourceType == "" || req.TargetType == "" || req.SourceID == uuid.Nil || req.TargetID == uuid.Nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Source and target type/id are required")
		return
	}
	if req.SourceID == req.TargetID && req.SourceType == req.TargetType {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "An artifact cannot link to itself")
		return
	}
	if req.LinkType == "" {
		req.LinkType = "related"
	}
	var id uuid.UUID
	err := h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO artifact_links (project_id, source_type, source_id, target_type, target_id, link_type, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (project_id, source_type, source_id, target_type, target_id, link_type) DO UPDATE SET link_type=EXCLUDED.link_type
		RETURNING id`,
		projectID, req.SourceType, req.SourceID, req.TargetType, req.TargetID, req.LinkType, actorID(c)).Scan(&id)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create link")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "link", &id, "created",
		"Linked "+req.SourceType+" to "+req.TargetType, map[string]interface{}{"link_type": req.LinkType})
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (h *Handler) DeleteLink(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	linkID, ok := parseParamID(c, "linkId")
	if !ok {
		return
	}
	tag, err := h.store.DB().Exec(c.Request.Context(), `DELETE FROM artifact_links WHERE id=$1 AND project_id=$2`, linkID, projectID)
	if err != nil || tag.RowsAffected() == 0 {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Link not found")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "link", &linkID, "deleted", "Deleted traceability link", nil)
	c.JSON(http.StatusOK, gin.H{"id": linkID})
}

// checkMergePolicy returns human-readable reasons why a merge is blocked.
// The checks are intentionally server-side: a direct API caller cannot bypass
// the project's configured evidence requirements.
func (h *Handler) checkMergePolicy(ctx context.Context, projectID, repoID uuid.UUID, workItemID, branchID *uuid.UUID, reviewStatus, mergeSHA string) []string {
	var raw []byte
	if err := h.store.DB().QueryRow(ctx, `SELECT COALESCE(settings,'{}'::jsonb) FROM projects WHERE id=$1`, projectID).Scan(&raw); err != nil {
		return []string{"Project settings could not be loaded"}
	}
	settings := decodeSettings(raw)
	blocked := []string{}
	if settings.MergePolicy.RequireLinkedWorkItem && workItemID == nil {
		blocked = append(blocked, "A linked work item is required")
	}
	if settings.MergePolicy.RequireApprovedReview && reviewStatus != "approved" {
		blocked = append(blocked, "An approved review is required")
	}
	if settings.MergePolicy.RequirePassingChecks {
		branch := ""
		if branchID != nil {
			_ = h.store.DB().QueryRow(ctx, `SELECT name FROM git_branches WHERE id=$1`, *branchID).Scan(&branch)
		}
		var status string
		err := h.store.DB().QueryRow(ctx, `
			SELECT status FROM builds
			WHERE project_id=$1 AND repository_id=$2
			  AND (branch=$3 OR commit_sha=$4)
			ORDER BY created_at DESC LIMIT 1`, projectID, repoID, branch, mergeSHA).Scan(&status)
		if err != nil || status != "success" {
			blocked = append(blocked, "A successful CI build for the source branch or merge commit is required")
		}
	}
	return blocked
}

func randomWebhookToken() string {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(raw[:])
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
