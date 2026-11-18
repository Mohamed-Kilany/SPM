package webhook

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/events"
)

// GenericHandler normalizes Git, CI and CD deliveries from Gitea, GitHub,
// GitLab, Bitbucket, Jenkins and generic automation. Repository-scoped
// deliveries authenticate with the webhook token shown in the delivery tab.
type GenericHandler struct {
	db     *pgxpool.Pool
	events *events.Client
}

// NewGenericHandler creates a GenericHandler.
func NewGenericHandler(db *pgxpool.Pool, eventsClient *events.Client) *GenericHandler {
	return &GenericHandler{db: db, events: eventsClient}
}

// HandleGit handles POST /webhooks/git/:provider.
func (h *GenericHandler) HandleGit(c *gin.Context) {
	payload, body, ok := readGenericPayload(c)
	if !ok {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(c.Param("provider")))
	token := genericWebhookToken(c)
	repo, err := resolveWorkspaceRepo(c.Request.Context(), h.db, provider, repoNames(payload), token)
	if err != nil {
		apiError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unknown repository or invalid webhook token")
		return
	}
	eventType := strings.ToLower(firstNonEmpty(
		c.GetHeader("X-GitHub-Event"), c.GetHeader("X-Gitlab-Event"), c.GetHeader("X-Gitea-Event"),
		c.GetHeader("X-Event-Key"), getString(payload, "event"), getString(payload, "event_type"),
		getString(payload, "object_kind"),
	))
	if strings.Contains(eventType, "pull") || strings.Contains(eventType, "merge_request") ||
		payload["pull_request"] != nil || payload["merge_request"] != nil || payload["object_attributes"] != nil {
		if err := h.syncGitPullRequest(c, repo, payload); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
	} else {
		if err := h.syncGitPush(c, repo, payload); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "repository_id": repo.RepositoryID, "project_id": repo.ProjectID, "bytes": len(body)})
}

func (h *GenericHandler) syncGitPush(c *gin.Context, repo workspaceRepo, payload map[string]interface{}) error {
	branch := strings.TrimPrefix(firstNonEmpty(
		getString(payload, "ref"),
		getString(payload, "branch"),
		getString(payload, "push", "changes", "0", "new", "name"),
	), "refs/heads/")
	sha := firstNonEmpty(
		getString(payload, "after"),
		getString(payload, "head_commit", "id"),
		getString(payload, "push", "changes", "0", "new", "target", "hash"),
		getString(payload, "commits", "0", "id"),
	)
	message := firstNonEmpty(
		getString(payload, "head_commit", "message"),
		getString(payload, "push", "changes", "0", "new", "target", "message"),
		getString(payload, "commits", "0", "message"),
	)
	author := firstNonEmpty(
		getString(payload, "head_commit", "author", "name"),
		getString(payload, "pusher", "name"),
		getString(payload, "push", "changes", "0", "new", "target", "author", "raw"),
		getString(payload, "sender", "login"),
	)
	email := firstNonEmpty(getString(payload, "head_commit", "author", "email"), getString(payload, "pusher", "email"))
	return syncPush(c.Request.Context(), h.db, h.events, repo, branch, sha, message, author, email, time.Now().UTC())
}

func (h *GenericHandler) syncGitPullRequest(c *gin.Context, repo workspaceRepo, payload map[string]interface{}) error {
	pr := firstMap(payload, "pull_request", "merge_request", "object_attributes", "pullrequest")
	if pr == nil {
		pr = payload
	}
	number := firstInt(pr, "number", "iid", "id")
	if number == 0 {
		number = firstInt(payload, "number", "iid")
	}
	state := firstNonEmpty(getString(pr, "state"), getString(pr, "action"), getString(payload, "action"))
	merged := getBool(pr, "merged") || strings.EqualFold(state, "merged") || strings.EqualFold(getString(payload, "action"), "merged")
	branch := firstNonEmpty(
		getString(pr, "head", "ref"), getString(pr, "source_branch"), getString(pr, "source", "branch", "name"),
		getString(pr, "from_branch", "name"),
	)
	target := firstNonEmpty(
		getString(pr, "base", "ref"), getString(pr, "target_branch"), getString(pr, "destination", "branch", "name"),
		getString(pr, "to_branch", "name"),
	)
	mergeSHA := firstNonEmpty(getString(pr, "merge_commit_sha"), getString(pr, "merge_commit"), getString(pr, "merge_commit_hash"))
	headSHA := firstNonEmpty(getString(pr, "head", "sha"), getString(pr, "last_commit", "id"), getString(pr, "source", "commit", "hash"))
	title := firstNonEmpty(getString(pr, "title"), getString(payload, "title"))
	body := firstNonEmpty(getString(pr, "body"), getString(pr, "description"), getString(payload, "description"))
	author := firstNonEmpty(getString(pr, "user", "login"), getString(pr, "author", "name"), getString(payload, "sender", "login"), getString(payload, "user", "name"))
	review := firstNonEmpty(getString(pr, "review_status"), getString(payload, "review", "state"))
	if review == "" && getBool(payload, "approved") {
		review = "approved"
	}
	_, err := syncPullRequest(c.Request.Context(), h.db, h.events, repo, prSyncInput{
		Number: number, Title: title, Description: body, Branch: branch, TargetBranch: target,
		Author: author, State: state, Merged: merged, MergeSHA: mergeSHA, HeadSHA: headSHA,
		ReviewStatus: review, UpdatedAt: time.Now().UTC(),
	})
	return err
}

// HandleCI handles POST /webhooks/ci/:provider.
func (h *GenericHandler) HandleCI(c *gin.Context) {
	payload, _, ok := readGenericPayload(c)
	if !ok {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(c.Param("provider")))
	repo, err := resolveWorkspaceRepo(c.Request.Context(), h.db, provider, repoNames(payload), genericWebhookToken(c))
	if err != nil {
		apiError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unknown repository or invalid webhook token")
		return
	}
	run := firstMap(payload, "workflow_run", "build", "run", "pipeline")
	if run == nil {
		run = payload
	}
	commitSHA := firstNonEmpty(
		getString(run, "head_sha"), getString(run, "commit_sha"), getString(run, "sha"), getString(run, "revision"),
		getString(payload, "commit_sha"), getString(payload, "sha"), getString(payload, "checkout_sha"),
	)
	branch := strings.TrimPrefix(firstNonEmpty(
		getString(run, "head_branch"), getString(run, "branch"), getString(run, "ref"),
		getString(payload, "branch"), getString(payload, "ref"),
	), "refs/heads/")
	status := firstNonEmpty(getString(run, "status"), getString(run, "state"), getString(run, "result"), getString(payload, "status"), getString(payload, "state"))
	conclusion := firstNonEmpty(getString(run, "conclusion"), getString(payload, "conclusion"))
	externalID := firstNonEmpty(
		getString(run, "id"), getString(run, "run_id"), getString(run, "build_id"),
		getString(run, "number"), getString(payload, "id"), getString(payload, "external_id"),
	)
	number := firstNonEmpty(getString(run, "run_number"), getString(run, "number"), getString(payload, "number"), externalID)
	logsURL := firstNonEmpty(getString(run, "html_url"), getString(run, "url"), getString(payload, "logs_url"), getString(payload, "url"))
	buildID, err := syncBuild(c.Request.Context(), h.db, h.events, repo, ciSyncInput{
		CommitSHA: commitSHA, Branch: branch, Status: status, Conclusion: conclusion,
		ExternalID: externalID, Number: number, LogsURL: logsURL, Provider: provider,
		Metadata: map[string]interface{}{"event": getString(payload, "event"), "name": getString(run, "name")},
	})
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "build_id": buildID, "project_id": repo.ProjectID})
}

// HandleCD handles POST /webhooks/cd/:provider.
func (h *GenericHandler) HandleCD(c *gin.Context) {
	payload, _, ok := readGenericPayload(c)
	if !ok {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(c.Param("provider")))
	appName := firstNonEmpty(
		getString(payload, "app"), getString(payload, "app_name"), getString(payload, "application"),
		getString(payload, "application", "metadata", "name"), getString(payload, "app", "metadata", "name"),
	)
	projectID := uuid.Nil
	if raw := firstNonEmpty(getString(payload, "project_id"), getString(payload, "project")); raw != "" {
		if parsed, err := uuid.Parse(raw); err == nil {
			projectID = parsed
		}
	}
	environment := firstNonEmpty(
		getString(payload, "environment"), getString(payload, "env"),
		getString(payload, "labels", "environment"), getString(payload, "app", "metadata", "labels", "environment"),
		getString(payload, "application", "metadata", "labels", "environment"),
	)
	syncStatus := firstNonEmpty(
		getString(payload, "sync_status"), getString(payload, "sync", "status"),
		getString(payload, "status", "sync", "status"), getString(payload, "syncState"),
	)
	healthStatus := firstNonEmpty(
		getString(payload, "health_status"), getString(payload, "health", "status"),
		getString(payload, "status", "health", "status"), getString(payload, "healthState"),
	)
	revision := firstNonEmpty(
		getString(payload, "revision"), getString(payload, "commit_sha"),
		getString(payload, "target_revision"), getString(payload, "status", "sync", "revision"),
		getString(payload, "app", "status", "sync", "revision"),
	)
	if syncStatus == "" && healthStatus == "" {
		syncStatus = firstNonEmpty(getString(payload, "status"), getString(payload, "state"))
	}
	deploymentID, err := syncArgo(c.Request.Context(), h.db, h.events, argoSyncInput{
		ProjectID: projectID, AppName: appName, Environment: environment, SyncStatus: syncStatus,
		HealthStatus: healthStatus, Revision: revision, CommitSHA: firstNonEmpty(getString(payload, "commit_sha"), revision),
		Message: getString(payload, "message"), Details: map[string]interface{}{"source": provider},
	})
	if err != nil {
		apiError(c, http.StatusUnprocessableEntity, "CD_SYNC_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "deployment_id": deploymentID})
}

func readGenericPayload(c *gin.Context) (map[string]interface{}, []byte, bool) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 4*1024*1024))
	if err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_PAYLOAD", "Could not read webhook payload")
		return nil, nil, false
	}
	payload := map[string]interface{}{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			apiError(c, http.StatusBadRequest, "INVALID_PAYLOAD", "Webhook payload is not valid JSON")
			return nil, nil, false
		}
	}
	return payload, body, true
}

func genericWebhookToken(c *gin.Context) string {
	if token := strings.TrimSpace(c.GetHeader("X-SPM-Webhook-Token")); token != "" {
		return token
	}
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	if strings.HasPrefix(strings.ToLower(auth), "token ") {
		return strings.TrimSpace(auth[6:])
	}
	return strings.TrimSpace(c.Query("token"))
}

func repoNames(payload map[string]interface{}) []string {
	names := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			names = append(names, value)
		}
	}
	add(getString(payload, "repository", "full_name"))
	add(getString(payload, "repository", "name"))
	add(getString(payload, "project", "path_with_namespace"))
	add(getString(payload, "project", "name"))
	add(getString(payload, "repository", "owner", "login") + "/" + getString(payload, "repository", "name"))
	add(getString(payload, "repo", "full_name"))
	add(getString(payload, "repository", "fullName"))
	return names
}

func getValue(payload map[string]interface{}, path ...string) interface{} {
	var current interface{} = payload
	for _, part := range path {
		switch typed := current.(type) {
		case map[string]interface{}:
			current = typed[part]
		case []interface{}:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil
			}
			current = typed[index]
		default:
			return nil
		}
	}
	return current
}

func getString(payload map[string]interface{}, path ...string) string {
	value := getValue(payload, path...)
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return ""
	}
}

func firstMap(payload map[string]interface{}, keys ...string) map[string]interface{} {
	for _, key := range keys {
		if value, ok := payload[key].(map[string]interface{}); ok {
			return value
		}
	}
	return nil
}

func firstInt(payload map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		raw := getString(payload, key)
		if raw == "" {
			continue
		}
		if parsed, err := strconv.Atoi(raw); err == nil {
			return parsed
		}
	}
	return 0
}

func getBool(payload map[string]interface{}, path ...string) bool {
	value := getValue(payload, path...)
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true") || typed == "1"
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
