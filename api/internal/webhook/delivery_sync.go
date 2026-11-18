package webhook

import (
	"context"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/cdengine"
	"spm-platform/api/internal/events"
	"spm-platform/api/internal/gitengine"
)

// DeliverySyncer reconciles the SPM delivery read model from the systems that
// actually execute CI and CD. It is intentionally read-only: SPM does not
// accept manually-entered build or deployment results.
type DeliverySyncer struct {
	db       *pgxpool.Pool
	events   *events.Client
	git      *gitengine.Client
	argo     *cdengine.ArgoClient
	interval time.Duration
	mu       sync.Mutex
}

// NewDeliverySyncer creates a reconciler. A nil git or argo client disables the
// corresponding source while leaving the other source active.
func NewDeliverySyncer(db *pgxpool.Pool, eventsClient *events.Client, git *gitengine.Client, argo *cdengine.ArgoClient) *DeliverySyncer {
	return &DeliverySyncer{
		db: db, events: eventsClient, git: git, argo: argo,
		interval: 30 * time.Second,
	}
}

// Start runs an immediate reconciliation followed by a periodic loop.
func (s *DeliverySyncer) Start(ctx context.Context) {
	go func() {
		s.reconcile(ctx)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reconcile(ctx)
			}
		}
	}()
}

// ReconcileNow is used by tests and by the health/readiness surface. It never
// accepts status input from an HTTP caller.
func (s *DeliverySyncer) ReconcileNow(ctx context.Context) {
	s.reconcile(ctx)
}

func (s *DeliverySyncer) reconcile(ctx context.Context) {
	if !s.mu.TryLock() {
		return
	}
	defer s.mu.Unlock()

	if s.git != nil {
		s.reconcileActions(ctx)
	}
	if s.argo != nil && s.argo.Enabled() {
		s.reconcileArgo(ctx)
	}
}

func (s *DeliverySyncer) reconcileActions(ctx context.Context) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.project_id, r.name, r.provider, r.default_branch, p.app_name, p.key_prefix
		FROM git_repositories r JOIN projects p ON p.id=r.project_id
		WHERE r.provider='gitea'
		ORDER BY r.created_at`)
	if err != nil {
		log.Printf("delivery sync: list gitea repositories: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var repo workspaceRepo
		if err := rows.Scan(&repo.RepositoryID, &repo.ProjectID, &repo.Name, &repo.Provider,
			&repo.DefaultBranch, &repo.AppName, &repo.KeyPrefix); err != nil {
			log.Printf("delivery sync: scan repository: %v", err)
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		runs, err := s.git.ListWorkflowRuns(runCtx, repo.Name, 50)
		cancel()
		if err != nil {
			log.Printf("delivery sync: Gitea Actions for %s: %v", repo.Name, err)
			continue
		}
		for _, run := range runs {
			status := normalizeBuildStatus(run.Status, run.Conclusion)
			if status == "" {
				status = "queued"
			}
			externalID := strconv.FormatInt(run.ID, 10)
			number := ""
			if run.RunNumber > 0 {
				number = strconv.Itoa(run.RunNumber)
			}
			metadata := map[string]interface{}{
				"source":       "gitea-actions",
				"workflow":     firstNonEmpty(run.Name, run.DisplayTitle),
				"event":        run.Event,
				"run_id":       run.ID,
				"run_number":   run.RunNumber,
				"html_url":     run.URL,
				"head_branch":  run.HeadBranch,
				"observed_via": "gitea-actions-api",
			}
			if _, err := syncBuild(ctx, s.db, s.events, repo, ciSyncInput{
				CommitSHA: run.HeadSHA, Branch: run.HeadBranch, Status: status,
				ExternalID: externalID, Number: number, LogsURL: run.URL,
				Provider: "gitea-actions", Metadata: metadata,
			}); err != nil {
				log.Printf("delivery sync: build run %d for %s: %v", run.ID, repo.Name, err)
			}
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("delivery sync: repositories: %v", err)
	}
}

func (s *DeliverySyncer) reconcileArgo(ctx context.Context) {
	argoCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	apps, err := s.argo.ListApplications(argoCtx)
	cancel()
	if err != nil {
		log.Printf("delivery sync: Argo CD applications: %v", err)
		return
	}

	projectRows, err := s.db.Query(ctx, `SELECT id, app_name FROM projects WHERE COALESCE(app_name,'') <> ''`)
	if err != nil {
		log.Printf("delivery sync: list projects: %v", err)
		return
	}
	defer projectRows.Close()
	projects := map[string]string{}
	for projectRows.Next() {
		var id, appName string
		if err := projectRows.Scan(&id, &appName); err != nil {
			continue
		}
		projects[appName] = id
	}
	if err := projectRows.Err(); err != nil {
		log.Printf("delivery sync: project rows: %v", err)
		return
	}

	for _, app := range apps {
		projectID := projects[app.Metadata.Name]
		if projectID == "" {
			// Support explicit labels without guessing across projects.
			projectID = firstNonEmpty(app.Metadata.Labels["spm.io/project-id"], app.Metadata.Labels["spm.project/id"])
		}
		if projectID == "" {
			continue
		}
		revision := firstNonEmpty(app.Status.Sync.Revision, app.Status.OperationState.SyncResult.Revision)
		environment := firstNonEmpty(
			app.Metadata.Labels["spm.io/environment"],
			app.Metadata.Labels["environment"],
			app.Spec.Destination.Namespace,
		)
		details := map[string]interface{}{
			"source":            "argocd-api",
			"namespace":         app.Spec.Destination.Namespace,
			"project":           app.Spec.Project,
			"images":            app.Status.Summary.Images,
			"operation_phase":   app.Status.OperationState.Phase,
			"operation_message": app.Status.OperationState.Message,
			"health_message":    app.Status.Health.Message,
			"observed_via":      "argocd-api",
		}
		if _, err := syncArgo(ctx, s.db, s.events, argoSyncInput{
			AppName: app.Metadata.Name, Environment: environment,
			SyncStatus: app.Status.Sync.Status, HealthStatus: app.Status.Health.Status,
			Revision: revision, CommitSHA: commitSHAFromRevision(revision),
			Details: details,
		}); err != nil && !strings.Contains(err.Error(), "no environment configured") {
			log.Printf("delivery sync: Argo application %s: %v", app.Metadata.Name, err)
		}
	}
}

func commitSHAFromRevision(revision string) string {
	revision = strings.TrimSpace(revision)
	if len(revision) == 40 || len(revision) == 64 {
		for _, r := range revision {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return ""
			}
		}
		return revision
	}
	return ""
}
