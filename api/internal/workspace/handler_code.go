package workspace

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The Code workspace is deliberately served through SPM. Gitea remains the
// internal Git engine, but every read is authorized against the SPM project
// membership and executed with the server-side service token.

func (h *Handler) requireGit(c *gin.Context) bool {
	if h.git == nil {
		apiError(c, http.StatusServiceUnavailable, "GIT_UNAVAILABLE", "Git integration is not configured")
		return false
	}
	return true
}

func (h *Handler) resolveRepository(c *gin.Context) (uuid.UUID, string, string, string, bool) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return uuid.Nil, "", "", "", false
	}
	repoID, ok := parseParamID(c, "repoId")
	if !ok {
		return uuid.Nil, "", "", "", false
	}
	name, defaultBranch, provider, err := h.repositoryRemote(c.Request.Context(), projectID, repoID)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Repository not found")
		return uuid.Nil, "", "", "", false
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load repository")
		return uuid.Nil, "", "", "", false
	}
	return repoID, name, defaultBranch, provider, true
}

func (h *Handler) requireGiteaRepository(c *gin.Context, provider string) bool {
	if provider != "gitea" {
		apiError(c, http.StatusUnprocessableEntity, "GIT_PROVIDER_UNSUPPORTED", "Repository browsing is available for Gitea repositories")
		return false
	}
	if !h.requireGit(c) {
		return false
	}
	return true
}

func gitReadError(c *gin.Context, err error) {
	message := err.Error()
	if strings.Contains(message, "status 404") {
		apiError(c, http.StatusNotFound, "REPOSITORY_NOT_FOUND", "The repository or ref does not exist in the Git engine. Repair the repository and try again.")
		return
	}
	if strings.Contains(message, "GITEA_API_TOKEN") {
		apiError(c, http.StatusBadGateway, "GIT_AUTH_FAILED", message)
		return
	}
	apiError(c, http.StatusBadGateway, "GIT_READ_FAILED", message)
}

// GetRepositoryTree handles GET /projects/:id/repositories/:repoId/tree.
func (h *Handler) GetRepositoryTree(c *gin.Context) {
	_, name, defaultBranch, provider, ok := h.resolveRepository(c)
	if !ok || !h.requireGiteaRepository(c, provider) {
		return
	}
	ref := strings.TrimSpace(c.Query("ref"))
	if ref == "" {
		ref = defaultBranch
	}
	path := strings.Trim(c.Query("path"), "/")
	entries, err := h.git.ListRepositoryContents(c.Request.Context(), name, ref, path)
	if err != nil {
		gitReadError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ref": ref, "path": path, "entries": entries})
}

// GetRepositoryFile handles GET /projects/:id/repositories/:repoId/blob.
func (h *Handler) GetRepositoryFile(c *gin.Context) {
	_, name, defaultBranch, provider, ok := h.resolveRepository(c)
	if !ok || !h.requireGiteaRepository(c, provider) {
		return
	}
	path := strings.Trim(c.Query("path"), "/")
	if path == "" {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A file path is required")
		return
	}
	ref := strings.TrimSpace(c.Query("ref"))
	if ref == "" {
		ref = defaultBranch
	}
	file, err := h.git.GetRepositoryFile(c.Request.Context(), name, ref, path)
	if err != nil {
		gitReadError(c, err)
		return
	}
	c.JSON(http.StatusOK, file)
}

// GetRepositoryCommits handles GET /projects/:id/repositories/:repoId/commits.
func (h *Handler) GetRepositoryCommits(c *gin.Context) {
	_, name, defaultBranch, provider, ok := h.resolveRepository(c)
	if !ok || !h.requireGiteaRepository(c, provider) {
		return
	}
	ref := strings.TrimSpace(c.Query("ref"))
	if ref == "" {
		ref = defaultBranch
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	commits, err := h.git.ListRepositoryCommits(c.Request.Context(), name, ref, page, limit)
	if err != nil {
		gitReadError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ref": ref, "commits": commits})
}

// GetRepositoryBranches handles GET /projects/:id/repositories/:repoId/branches.
func (h *Handler) GetRepositoryBranches(c *gin.Context) {
	_, name, _, provider, ok := h.resolveRepository(c)
	if !ok || !h.requireGiteaRepository(c, provider) {
		return
	}
	branches, err := h.git.ListRepositoryBranches(c.Request.Context(), name)
	if err != nil {
		gitReadError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"branches": branches})
}

// GetPullRequestFiles handles GET /projects/:id/pull-requests/:prId/files.
func (h *Handler) GetPullRequestFiles(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	prID, ok := parseParamID(c, "prId")
	if !ok {
		return
	}
	var number int
	var provider, repoName string
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT p.number, r.provider, r.name
		FROM pull_requests p JOIN git_repositories r ON r.id = p.repository_id
		WHERE p.id=$1 AND r.project_id=$2`, prID, projectID).Scan(&number, &provider, &repoName)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Pull request not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load pull request")
		return
	}
	if !h.requireGiteaRepository(c, provider) {
		return
	}
	files, err := h.git.GetPullRequestFiles(c.Request.Context(), repoName, number)
	if err != nil {
		gitReadError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"number": number, "files": files})
}

// ProvisionRepository handles POST /projects/:id/repositories/:repoId/provision.
// It repairs a missing remote repository and re-registers the SPM webhook. The
// operation is idempotent and is useful when a local Gitea volume was reset.
func (h *Handler) ProvisionRepository(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	repoID, ok := parseParamID(c, "repoId")
	if !ok {
		return
	}
	var name, provider, webhookURL, webhookToken string
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT name, provider, COALESCE(webhook_url, ''), COALESCE(webhook_token, '')
		FROM git_repositories WHERE id=$1 AND project_id=$2`, repoID, projectID).
		Scan(&name, &provider, &webhookURL, &webhookToken)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Repository not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load repository")
		return
	}
	if !h.requireGiteaRepository(c, provider) {
		return
	}
	if err := h.git.ProvisionRepo(c.Request.Context(), projectID.String(), name); err != nil {
		gitReadError(c, err)
		return
	}
	webhookTarget := webhookURL
	if internal := strings.TrimSpace(os.Getenv("SPM_INTERNAL_WEBHOOK_URL")); internal != "" {
		// Gitea must call the API over the Compose network, not the host's
		// localhost alias. Prefer the explicitly configured internal URL.
		webhookTarget = internal
	}
	if webhookTarget != "" {
		// Each repository has its own signing token. Using it here keeps
		// projects isolated and matches the signature verifier.
		if webhookToken == "" {
			apiError(c, http.StatusInternalServerError, "CONFIGURATION_ERROR", "Repository webhook token is not configured")
			return
		}
		if err := h.git.EnsureRepoWebhook(c.Request.Context(), name, webhookTarget, webhookToken); err != nil {
			gitReadError(c, err)
			return
		}
		if _, err := h.store.DB().Exec(c.Request.Context(), `
			UPDATE git_repositories SET webhook_url=$1, updated_at=NOW() WHERE id=$2`, webhookTarget, repoID); err != nil {
			gitReadError(c, err)
			return
		}
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "repository", &repoID, "provisioned", "Provisioned Git repository "+name, nil)
	c.JSON(http.StatusOK, gin.H{"id": repoID, "name": name, "status": "ready"})
}
