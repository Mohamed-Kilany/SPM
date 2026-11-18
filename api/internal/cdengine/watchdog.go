package cdengine

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"

	"spm-platform/api/internal/events"
	"spm-platform/api/internal/models"
)

const (
	watchdogTimeout  = 10 * time.Minute
	watchdogInterval = 30 * time.Second
)

// pendingDeployment tracks a ManifestRepo commit waiting for Argo CD confirmation.
type pendingDeployment struct {
	ProjectID  string
	CommitSHA  string
	AppName    string
	CommitTime time.Time
}

// Watchdog tracks pending deployments and fires a timeout event if Argo CD
// does not confirm within watchdogTimeout.
type Watchdog struct {
	mu           sync.Mutex
	pending      map[string]pendingDeployment // keyed by commitSHA
	eventsClient *events.Client
}

// NewWatchdog creates a Watchdog. Does not start the background goroutine.
func NewWatchdog(eventsClient *events.Client) *Watchdog {
	return &Watchdog{
		pending:      make(map[string]pendingDeployment),
		eventsClient: eventsClient,
	}
}

// Start launches the background check goroutine. ctx cancellation stops it.
func (w *Watchdog) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(watchdogInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				w.checkTimeouts(ctx, now)
			}
		}
	}()
}

// Track registers a new pending deployment to be confirmed.
// Called immediately after CommitManifest succeeds.
func (w *Watchdog) Track(projectID, commitSHA, appName string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.pending[commitSHA] = pendingDeployment{
		ProjectID:  projectID,
		CommitSHA:  commitSHA,
		AppName:    appName,
		CommitTime: time.Now().UTC(),
	}
}

// Confirm removes a pending deployment when the Argo CD webhook is received.
// commitSHA is used as the lookup key.
func (w *Watchdog) Confirm(commitSHA string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	delete(w.pending, commitSHA)
}

// checkTimeouts iterates the pending map and fires a deployment.status_changed
// event for any entry that has exceeded watchdogTimeout.
func (w *Watchdog) checkTimeouts(ctx context.Context, now time.Time) {
	w.mu.Lock()
	// Collect timed-out entries under the lock, then release before publishing.
	var timedOut []pendingDeployment
	for sha, dep := range w.pending {
		if now.Sub(dep.CommitTime) >= watchdogTimeout {
			timedOut = append(timedOut, dep)
			delete(w.pending, sha)
		}
	}
	w.mu.Unlock()

	for _, dep := range timedOut {
		w.publishUnknown(ctx, dep, now)
	}
}

// publishUnknown appends a deployment.status_changed event with sync_status
// "Unknown" for a deployment that timed out without Argo CD confirmation.
func (w *Watchdog) publishUnknown(ctx context.Context, dep pendingDeployment, now time.Time) {
	projectUUID, err := uuid.Parse(dep.ProjectID)
	if err != nil {
		// Unparseable project UUID — nothing we can do; skip silently.
		return
	}

	payload, err := json.Marshal(map[string]string{
		"sync_status":   "Unknown",
		"health_status": "Unknown",
		"app_name":      dep.AppName,
		"project_id":    dep.ProjectID,
		"commit_sha":    dep.CommitSHA,
		"updated_at":    now.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}

	envelope := models.EventEnvelope{
		EventType:   "deployment.status_changed",
		AggregateID: projectUUID,
		Payload:     payload,
		CreatedAt:   now.UTC(),
	}

	// Publish with no optimistic concurrency check (expectedSeq=0) — this is an
	// async timeout notification and sequence ordering is not critical here.
	_, _ = w.eventsClient.Publish(ctx, envelope, 0)
}
