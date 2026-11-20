package webhook

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/audit"
	"spm-platform/api/internal/cdengine"
	"spm-platform/api/internal/events"
	"spm-platform/api/internal/models"
)

// ArgoCDHandler processes inbound webhooks from Argo CD.
type ArgoCDHandler struct {
	db       *pgxpool.Pool
	store    *Store
	events   *events.Client
	watchdog *cdengine.Watchdog
	token    string
	audit    *audit.Logger
}

// NewArgoCDHandler creates an ArgoCDHandler.
func NewArgoCDHandler(db *pgxpool.Pool, store *Store, eventsClient *events.Client, watchdog *cdengine.Watchdog) *ArgoCDHandler {
	return &ArgoCDHandler{
		db: db, store: store, events: eventsClient, watchdog: watchdog,
		token: os.Getenv("ARGOCD_WEBHOOK_TOKEN"),
	}
}

// SetAuditLogger attaches the audit log for deployment observations.
func (h *ArgoCDHandler) SetAuditLogger(logger *audit.Logger) {
	h.audit = logger
}

// Handle handles POST /webhooks/argocd.
func (h *ArgoCDHandler) Handle(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") || h.token == "" {
		apiError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid or missing authorization token")
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if subtle.ConstantTimeCompare([]byte(token), []byte(h.token)) != 1 {
		apiError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid or missing authorization token")
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 2*1024*1024))
	if err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_PAYLOAD", "Failed to read Argo CD notification payload")
		return
	}

	deliveryID := firstHeader(c, "X-ArgoCD-Delivery", "X-Webhook-Delivery", "X-Gitea-Delivery")
	if deliveryID == "" {
		deliveryID = bodyHash("argocd", body)
	}

	ctx := c.Request.Context()
	claimed, err := h.store.Claim(ctx, "argocd", deliveryID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to claim webhook delivery")
		return
	}
	if !claimed {
		c.JSON(http.StatusOK, gin.H{"status": "duplicate"})
		return
	}

	if err := h.process(ctx, body); err != nil {
		if errors.Is(err, ErrProjectNotFound) {
			_ = h.store.Complete(ctx, "argocd", deliveryID)
			c.JSON(http.StatusOK, gin.H{"status": "ignored", "reason": "unknown application"})
			return
		}
		_ = h.store.Release(ctx, "argocd", deliveryID, err)
		log.Printf("webhook: Argo CD notification failed for delivery %s: %v", deliveryID, err)
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to process Argo CD notification")
		return
	}
	if err := h.store.Complete(ctx, "argocd", deliveryID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to record webhook completion")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *ArgoCDHandler) process(ctx context.Context, body []byte) error {
	var notification ArgoCDNotification
	if err := json.Unmarshal(body, &notification); err != nil {
		return err
	}

	appName := notification.App.Metadata.Name
	syncStatus := notification.App.Status.Sync.Status
	healthStatus := notification.App.Status.Health.Status
	if syncStatus == "" {
		syncStatus = "Unknown"
	}
	if healthStatus == "" {
		healthStatus = "Unknown"
	}

	project, err := h.resolveProject(ctx, notification.ProjectID, appName)
	if err != nil {
		return err
	}
	if appName == "" {
		appName = project.AppName
	}
	// The app name is caller-controlled through the webhook payload. Strip CR
	// and LF before logging so a forged event cannot create log entries
	// (CWE-117).
	safeAppName := strings.ReplaceAll(appName, "\n", "")
	safeAppName = strings.ReplaceAll(safeAppName, "\r", "")

	now := time.Now().UTC()
	payloadMap := map[string]interface{}{
		"app_name":      appName,
		"sync_status":   syncStatus,
		"health_status": healthStatus,
		"commit_sha":    notification.CommitSHA,
		"project_id":    project.ID.String(),
		"updated_at":    now,
	}
	eventPayload, err := json.Marshal(payloadMap)
	if err != nil {
		return err
	}

	if _, err := h.events.Publish(ctx, models.EventEnvelope{
		EventType: "deployment.status_changed", AggregateID: project.ID,
		Payload: eventPayload, CreatedAt: now,
	}, 0); err != nil {
		return err
	}

	if strings.EqualFold(healthStatus, "Degraded") {
		if _, err := h.events.Publish(ctx, models.EventEnvelope{
			EventType: "deployment.health_degraded", AggregateID: project.ID,
			Payload: eventPayload, CreatedAt: now,
		}, 0); err != nil {
			return err
		}
	}

	if h.watchdog != nil && notification.CommitSHA != "" {
		h.watchdog.Confirm(notification.CommitSHA)
	}

	normalizedProjectID := uuid.Nil
	if parsed, parseErr := uuid.Parse(notification.ProjectID); parseErr == nil {
		normalizedProjectID = parsed
	}
	if _, syncErr := syncArgo(ctx, h.db, h.events, argoSyncInput{
		ProjectID: normalizedProjectID, AppName: appName, Environment: notification.Environment,
		SyncStatus: syncStatus, HealthStatus: healthStatus, Revision: notification.Revision,
		CommitSHA: notification.CommitSHA, Message: notification.Message,
		Details: map[string]interface{}{"source": "argocd", "namespace": notification.App.Metadata.Namespace},
	}); syncErr != nil && !errors.Is(syncErr, ErrProjectNotFound) {
		log.Printf("webhook: normalized Argo CD sync failed for %s: %v", safeAppName, syncErr)
	}
	if h.audit != nil {
		entry := audit.Entry{
			ActorRole: "system", Action: "deployment.observed", TargetType: "project",
			TargetID: project.ID.String(), ProjectID: project.ID.String(), Result: "success",
			After: map[string]any{
				"app_name": appName, "environment": notification.Environment,
				"sync_status": syncStatus, "health_status": healthStatus,
				"revision": notification.Revision, "commit_sha": notification.CommitSHA,
			},
		}
		if strings.EqualFold(healthStatus, "Degraded") {
			entry.Result = "failure"
			entry.Reason = "argo_health_degraded"
		}
		if err := h.audit.Record(ctx, entry); err != nil {
			log.Printf("webhook: audit deployment observation failed for %s: %v", safeAppName, err)
		}
	}
	return nil
}

func (h *ArgoCDHandler) resolveProject(ctx context.Context, rawProjectID, appName string) (projectRef, error) {
	var p projectRef
	if rawProjectID != "" {
		if id, err := uuid.Parse(rawProjectID); err == nil {
			err = h.db.QueryRow(ctx, `
				SELECT id, repo_name, app_name, default_branch FROM projects WHERE id = $1`, id,
			).Scan(&p.ID, &p.RepoName, &p.AppName, &p.DefaultBranch)
			if err == nil {
				return p, nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return projectRef{}, err
			}
		}
	}
	if appName != "" {
		err := h.db.QueryRow(ctx, `
			SELECT id, repo_name, app_name, default_branch FROM projects WHERE app_name = $1 LIMIT 1`, appName,
		).Scan(&p.ID, &p.RepoName, &p.AppName, &p.DefaultBranch)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return projectRef{}, err
		}
	}
	return projectRef{}, ErrProjectNotFound
}

// ArgoCDNotification is the JSON body posted by Argo CD Notifications.
type ArgoCDNotification struct {
	App struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Status struct {
			Sync struct {
				Status string `json:"status"`
			} `json:"sync"`
			Health struct {
				Status string `json:"status"`
			} `json:"health"`
		} `json:"status"`
	} `json:"app"`
	CommitSHA   string `json:"commit_sha"`
	ProjectID   string `json:"project_id"`
	Environment string `json:"environment"`
	Revision    string `json:"revision"`
	Message     string `json:"message"`
}
