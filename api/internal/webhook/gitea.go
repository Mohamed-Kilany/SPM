// Package webhook implements inbound webhook processors for external services.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/cdengine"
	"spm-platform/api/internal/events"
	"spm-platform/api/internal/models"
)

var branchFeatureRe = regexp.MustCompile(`^feature/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

var ErrProjectNotFound = errors.New("webhook: project not found")

type projectRef struct {
	ID            uuid.UUID
	RepoName      string
	AppName       string
	DefaultBranch string
}

// ---------------------------------------------------------------------------
// Gitea
// ---------------------------------------------------------------------------

// GiteaHandler processes inbound webhooks from Gitea.
type GiteaHandler struct {
	db       *pgxpool.Pool
	store    *Store
	events   *events.Client
	cd       *cdengine.Manager
	watchdog *cdengine.Watchdog
	secret   string
}

// NewGiteaHandler creates a GiteaHandler.
func NewGiteaHandler(db *pgxpool.Pool, store *Store, eventsClient *events.Client, cd *cdengine.Manager, watchdog *cdengine.Watchdog) *GiteaHandler {
	return &GiteaHandler{
		db: db, store: store, events: eventsClient, cd: cd, watchdog: watchdog,
		secret: os.Getenv("GITEA_WEBHOOK_SECRET"),
	}
}

// Handle handles POST /webhooks/gitea.
func (h *GiteaHandler) Handle(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 2*1024*1024))
	if err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_PAYLOAD", "Failed to read request body")
		return
	}
	if !h.verifySignature(c.Request.Context(), body, c.GetHeader("X-Hub-Signature-256")) {
		apiError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid webhook signature")
		return
	}

	eventType := c.GetHeader("X-Gitea-Event")
	deliveryID := firstHeader(c, "X-Gitea-Delivery", "X-GitHub-Delivery")
	if deliveryID == "" {
		deliveryID = bodyHash(eventType, body)
	}

	ctx := c.Request.Context()
	claimed, err := h.store.Claim(ctx, "gitea", deliveryID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to claim webhook delivery")
		return
	}
	if !claimed {
		c.JSON(http.StatusOK, gin.H{"status": "duplicate"})
		return
	}

	processErr := h.dispatch(ctx, eventType, body)
	if processErr != nil {
		if errors.Is(processErr, ErrProjectNotFound) {
			_ = h.store.Complete(ctx, "gitea", deliveryID)
			c.JSON(http.StatusOK, gin.H{"status": "ignored", "reason": "unknown repository"})
			return
		}
		_ = h.store.Release(ctx, "gitea", deliveryID, processErr)
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to process webhook")
		return
	}
	if err := h.store.Complete(ctx, "gitea", deliveryID); err != nil {
		log.Printf("webhook: complete gitea delivery %s: %v", deliveryID, err)
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *GiteaHandler) dispatch(ctx context.Context, eventType string, body []byte) error {
	switch eventType {
	case "push":
		return h.handlePush(ctx, body)
	case "pull_request":
		return h.handlePullRequest(ctx, body)
	case "pull_request_review":
		return h.handlePullRequestReview(ctx, body)
	case "workflow_run":
		return h.handleWorkflowRun(ctx, body)
	default:
		return nil
	}
}

func (h *GiteaHandler) verifySignature(ctx context.Context, body []byte, header string) bool {
	if header == "" {
		return false
	}
	// Prefer the per-repository token stored when the hook was registered. This
	// keeps one project's webhook secret from authenticating another project.
	// The shared GITEA_WEBHOOK_SECRET remains a compatibility fallback for hooks
	// created by older SPM versions.
	var payload struct {
		Repository struct {
			Name     string `json:"name"`
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		repo, repoErr := resolveWorkspaceRepo(ctx, h.db, "gitea",
			[]string{payload.Repository.FullName, payload.Repository.Name}, "")
		if repoErr == nil && repo.WebhookToken != "" && signatureMatches(body, repo.WebhookToken, header) {
			return true
		}
	}
	return h.secret != "" && signatureMatches(body, h.secret, header)
}

func signatureMatches(body []byte, secret, header string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(header), []byte(expected))
}

// ---------------------------------------------------------------------------
// Push
// ---------------------------------------------------------------------------

type PushPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Owner    struct {
			Username string `json:"username"`
		} `json:"owner"`
	} `json:"repository"`
	HeadCommit struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		Author  struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"author"`
		Timestamp time.Time `json:"timestamp"`
	} `json:"head_commit"`
	Commits []struct {
		ID string `json:"id"`
	} `json:"commits"`
}

func (h *GiteaHandler) handlePush(ctx context.Context, body []byte) error {
	var payload PushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("parse push payload: %w", err)
	}
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
	if repo, repoErr := resolveWorkspaceRepo(ctx, h.db, "gitea", []string{payload.Repository.FullName, payload.Repository.Name}, ""); repoErr == nil {
		committedAt := payload.HeadCommit.Timestamp
		if committedAt.IsZero() {
			committedAt = time.Now().UTC()
		}
		if err := syncPush(ctx, h.db, h.events, repo, branch, payload.HeadCommit.ID,
			payload.HeadCommit.Message, payload.HeadCommit.Author.Name, payload.HeadCommit.Author.Email, committedAt); err != nil {
			return err
		}
	}
	matches := branchFeatureRe.FindStringSubmatch(branch)
	if matches == nil {
		return nil
	}
	ticketID, err := uuid.Parse(matches[1])
	if err != nil {
		return nil
	}
	project, err := h.resolveProjectByRepo(ctx, payload.Repository.Name)
	if err != nil {
		return err
	}

	eventPayload, err := json.Marshal(map[string]interface{}{
		"ticket_id":  ticketID,
		"branch":     branch,
		"commit_sha": payload.HeadCommit.ID,
		"author":     payload.HeadCommit.Author.Name,
		"pushed_at":  time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	return h.publish(ctx, project.ID, "git.code.pushed", eventPayload)
}

// ---------------------------------------------------------------------------
// Pull request
// ---------------------------------------------------------------------------

type PullRequestPayload struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		Number         int       `json:"number"`
		Title          string    `json:"title"`
		Body           string    `json:"body"`
		State          string    `json:"state"`
		Merged         bool      `json:"merged"`
		MergeCommitSHA string    `json:"merge_commit_sha"`
		CreatedAt      time.Time `json:"created_at"`
		UpdatedAt      time.Time `json:"updated_at"`
		MergedAt       time.Time `json:"merged_at"`
		Head           struct {
			Ref string `json:"ref"`
			Sha string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"pull_request"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func (h *GiteaHandler) handlePullRequest(ctx context.Context, body []byte) error {
	var payload PullRequestPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("parse pull_request payload: %w", err)
	}
	number := payload.PullRequest.Number
	if number == 0 {
		number = payload.Number
	}
	updatedAt := payload.PullRequest.UpdatedAt
	if payload.PullRequest.MergedAt.After(updatedAt) {
		updatedAt = payload.PullRequest.MergedAt
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	if repo, repoErr := resolveWorkspaceRepo(ctx, h.db, "gitea", []string{payload.Repository.FullName, payload.Repository.Name}, ""); repoErr == nil {
		if _, err := syncPullRequest(ctx, h.db, h.events, repo, prSyncInput{
			Number: number, Title: payload.PullRequest.Title, Description: payload.PullRequest.Body,
			Branch: payload.PullRequest.Head.Ref, TargetBranch: payload.PullRequest.Base.Ref,
			Author: payload.PullRequest.User.Login, State: payload.PullRequest.State,
			Merged: payload.PullRequest.Merged, MergeSHA: payload.PullRequest.MergeCommitSHA,
			HeadSHA: payload.PullRequest.Head.Sha, UpdatedAt: updatedAt,
		}); err != nil {
			return err
		}
	}
	if payload.Action != "closed" || !payload.PullRequest.Merged {
		return nil
	}
	project, err := h.resolveProjectByRepo(ctx, payload.Repository.Name)
	if err != nil {
		return err
	}

	branch := payload.PullRequest.Head.Ref
	var ticketID uuid.UUID
	if matches := branchFeatureRe.FindStringSubmatch(branch); matches != nil {
		if parsed, err := uuid.Parse(matches[1]); err == nil {
			ticketID = parsed
		}
	}

	eventPayload, err := json.Marshal(map[string]interface{}{
		"repository": payload.Repository.Name,
		"branch":     branch,
		"ticket_id":  ticketID,
		"commit_sha": payload.PullRequest.Head.Sha,
		"base_ref":   payload.PullRequest.Base.Ref,
		"merged_at":  time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if err := h.publish(ctx, project.ID, "git.pr.merged", eventPayload); err != nil {
		return err
	}

	// CD: only promote code whose feature branch passed CI.
	if h.cd != nil && payload.PullRequest.Head.Sha != "" && branch != "" {
		ok, checkErr := h.pipelineSucceeded(ctx, project.ID, branch)
		if checkErr != nil {
			log.Printf("webhook: pipeline lookup failed for %s: %v", branch, checkErr)
		} else if ok {
			commitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			if commitErr := h.cd.CommitManifest(commitCtx, project.ID.String(), payload.PullRequest.Head.Sha, project.RepoName); commitErr != nil {
				log.Printf("webhook: manifest commit failed for project %s: %v", project.ID, commitErr)
			} else if h.watchdog != nil {
				h.watchdog.Track(project.ID.String(), payload.PullRequest.Head.Sha, project.AppName)
			}
		}
	}
	return nil
}

func (h *GiteaHandler) handlePullRequestReview(ctx context.Context, body []byte) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("parse pull_request_review payload: %w", err)
	}
	repoNames := []string{getString(payload, "repository", "full_name"), getString(payload, "repository", "name")}
	repo, err := resolveWorkspaceRepo(ctx, h.db, "gitea", repoNames, "")
	if err != nil {
		if errors.Is(err, ErrProjectNotFound) {
			return nil
		}
		return err
	}
	state := strings.ToLower(getString(payload, "review", "state"))
	review := "pending"
	if state == "approved" {
		review = "approved"
	} else if state == "rejected" || state == "requested_changes" {
		review = "changes_requested"
	}
	_, err = syncPullRequest(ctx, h.db, h.events, repo, prSyncInput{
		Number:       firstInt(payload, "pull_request", "number"),
		Title:        getString(payload, "pull_request", "title"),
		Description:  getString(payload, "pull_request", "body"),
		Branch:       getString(payload, "pull_request", "head", "ref"),
		TargetBranch: getString(payload, "pull_request", "base", "ref"),
		Author:       getString(payload, "pull_request", "user", "login"),
		State:        getString(payload, "pull_request", "state"),
		HeadSHA:      getString(payload, "pull_request", "head", "sha"),
		ReviewStatus: review, UpdatedAt: time.Now().UTC(),
	})
	return err
}

// ---------------------------------------------------------------------------
// Workflow run
// ---------------------------------------------------------------------------

type WorkflowRunPayload struct {
	Action      string `json:"action"`
	WorkflowRun struct {
		ID         int64  `json:"id"`
		RunNumber  int    `json:"run_number"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HeadSHA    string `json:"head_sha"`
		HeadBranch string `json:"head_branch"`
		HTMLURL    string `json:"html_url"`
	} `json:"workflow_run"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func mapPipelineStatus(status, conclusion string) string {
	if status == "in_progress" || status == "queued" || status == "requested" || status == "waiting" {
		return models.PipelineInProgress
	}
	switch conclusion {
	case "success":
		return models.PipelineSuccess
	case "failure":
		return models.PipelineFailure
	case "cancelled":
		return models.PipelineCancelled
	case "timed_out":
		return models.PipelineTimedOut
	default:
		if status == "completed" {
			return models.PipelineFailure
		}
		return models.PipelineInProgress
	}
}

func (h *GiteaHandler) handleWorkflowRun(ctx context.Context, body []byte) error {
	var payload WorkflowRunPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("parse workflow_run payload: %w", err)
	}
	if repo, repoErr := resolveWorkspaceRepo(ctx, h.db, "gitea", []string{payload.Repository.FullName, payload.Repository.Name}, ""); repoErr == nil {
		if _, err := syncBuild(ctx, h.db, h.events, repo, ciSyncInput{
			CommitSHA: payload.WorkflowRun.HeadSHA, Branch: payload.WorkflowRun.HeadBranch,
			Status: payload.WorkflowRun.Status, Conclusion: payload.WorkflowRun.Conclusion,
			ExternalID: fmt.Sprintf("%d", payload.WorkflowRun.ID), Number: fmt.Sprintf("%d", payload.WorkflowRun.RunNumber),
			LogsURL: payload.WorkflowRun.HTMLURL, Provider: "gitea-actions",
			Metadata: map[string]interface{}{"workflow": payload.WorkflowRun.Name, "action": payload.Action},
		}); err != nil {
			return err
		}
	}
	project, err := h.resolveProjectByRepo(ctx, payload.Repository.Name)
	if err != nil {
		return err
	}

	branch := payload.WorkflowRun.HeadBranch
	var ticketID uuid.UUID
	if matches := branchFeatureRe.FindStringSubmatch(branch); matches != nil {
		if parsed, err := uuid.Parse(matches[1]); err == nil {
			ticketID = parsed
		}
	}
	status := mapPipelineStatus(payload.WorkflowRun.Status, payload.WorkflowRun.Conclusion)
	eventPayload, err := json.Marshal(map[string]interface{}{
		"repository":      payload.Repository.Name,
		"branch":          branch,
		"commit_sha":      payload.WorkflowRun.HeadSHA,
		"status":          status,
		"pipeline_status": status,
		"ticket_id":       ticketID,
		"updated_at":      time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	return h.publish(ctx, project.ID, "pipeline.status_changed", eventPayload)
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

func (h *GiteaHandler) publish(ctx context.Context, projectID uuid.UUID, eventType string, payload []byte) error {
	_, err := h.events.Publish(ctx, models.EventEnvelope{
		EventType: eventType, AggregateID: projectID, Payload: payload, CreatedAt: time.Now().UTC(),
	}, 0)
	return err
}

func (h *GiteaHandler) resolveProjectByRepo(ctx context.Context, repoName string) (projectRef, error) {
	var p projectRef
	err := h.db.QueryRow(ctx, `
		SELECT id, repo_name, app_name, default_branch
		FROM projects
		WHERE repo_name = $1 OR name = $1
		ORDER BY (repo_name = $1) DESC
		LIMIT 1`, repoName).Scan(&p.ID, &p.RepoName, &p.AppName, &p.DefaultBranch)
	if errors.Is(err, pgx.ErrNoRows) {
		return projectRef{}, ErrProjectNotFound
	}
	if err != nil {
		return projectRef{}, fmt.Errorf("resolve project by repo %q: %w", repoName, err)
	}
	return p, nil
}

func (h *GiteaHandler) pipelineSucceeded(ctx context.Context, projectID uuid.UUID, branch string) (bool, error) {
	var status string
	err := h.db.QueryRow(ctx, `
		SELECT COALESCE(payload->>'status', payload->>'pipeline_status', '')
		FROM events
		WHERE aggregate_id = $1
		  AND event_type = 'pipeline.status_changed'
		  AND payload->>'branch' = $2
		ORDER BY sequence_id DESC
		LIMIT 1`, projectID, branch).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == models.PipelineSuccess, nil
}

func firstHeader(c *gin.Context, names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(c.GetHeader(name)); v != "" {
			return v
		}
	}
	return ""
}

func bodyHash(prefix string, body []byte) string {
	sum := sha256.Sum256(append([]byte(prefix), body...))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type apiErrorResponse struct {
	Error apiErrorBody `json:"error"`
}

func apiError(c *gin.Context, status int, code, message string) {
	c.JSON(status, apiErrorResponse{Error: apiErrorBody{Code: code, Message: message}})
}

// computeHMAC is exported for property-based tests.
func computeHMAC(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return fmt.Sprintf("sha256=%s", hex.EncodeToString(mac.Sum(nil)))
}
