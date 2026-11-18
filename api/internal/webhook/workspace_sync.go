package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/events"
	"spm-platform/api/internal/models"
)

// workspaceRepo is the normalized repository/project pair resolved from an
// inbound delivery. Legacy project snapshots are not used by these updates.
type workspaceRepo struct {
	RepositoryID  uuid.UUID
	ProjectID     uuid.UUID
	Name          string
	Provider      string
	DefaultBranch string
	AppName       string
	KeyPrefix     string
	WebhookToken  string
}

var workItemKeyRe = regexp.MustCompile(`(?i)([a-z][a-z0-9]{1,11})-(\d+)`)

func resolveWorkspaceRepo(ctx context.Context, db *pgxpool.Pool, provider string, names []string, token string) (workspaceRepo, error) {
	var repo workspaceRepo
	if token != "" {
		err := db.QueryRow(ctx, `
			SELECT r.id, r.project_id, r.name, r.provider, r.default_branch, p.app_name, p.key_prefix,
			       COALESCE(r.webhook_token, '')
			FROM git_repositories r JOIN projects p ON p.id=r.project_id
			WHERE r.webhook_token=$1 LIMIT 1`, token).
			Scan(&repo.RepositoryID, &repo.ProjectID, &repo.Name, &repo.Provider, &repo.DefaultBranch, &repo.AppName, &repo.KeyPrefix, &repo.WebhookToken)
		if err == nil {
			return repo, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return workspaceRepo{}, err
		}
		return workspaceRepo{}, ErrProjectNotFound
	}
	clean := make([]string, 0, len(names)*2)
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		clean = append(clean, name)
		if slash := strings.LastIndex(name, "/"); slash >= 0 && slash < len(name)-1 {
			clean = append(clean, name[slash+1:])
		}
	}
	if len(clean) == 0 {
		return workspaceRepo{}, ErrProjectNotFound
	}
	err := db.QueryRow(ctx, `
		SELECT r.id, r.project_id, r.name, r.provider, r.default_branch, p.app_name, p.key_prefix,
		       COALESCE(r.webhook_token, '')
		FROM git_repositories r JOIN projects p ON p.id=r.project_id
		WHERE (r.provider=$1 OR $1='') AND (r.name = ANY($2::text[]) OR r.external_id = ANY($2::text[]))
		ORDER BY (r.provider=$1) DESC, r.created_at ASC LIMIT 1`, provider, clean).
		Scan(&repo.RepositoryID, &repo.ProjectID, &repo.Name, &repo.Provider, &repo.DefaultBranch, &repo.AppName, &repo.KeyPrefix, &repo.WebhookToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return workspaceRepo{}, ErrProjectNotFound
	}
	if err != nil {
		return workspaceRepo{}, err
	}
	return repo, nil
}

func inferWorkItem(ctx context.Context, db *pgxpool.Pool, projectID uuid.UUID, text string) *uuid.UUID {
	text = strings.TrimPrefix(text, "refs/heads/")
	if text == "" {
		return nil
	}
	// UUID branch convention: feature/<work-item-uuid>.
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == '/' || r == ' ' || r == ':' || r == '#' }) {
		if id, err := uuid.Parse(part); err == nil {
			var exists bool
			if db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM work_items WHERE id=$1 AND project_id=$2)`, id, projectID).Scan(&exists) == nil && exists {
				return &id
			}
		}
	}
	// Human key convention: feature/ATL-12-checkout or PR title "ATL-12".
	for _, match := range workItemKeyRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 {
			continue
		}
		number, err := strconv.ParseInt(match[2], 10, 64)
		if err != nil {
			continue
		}
		var id uuid.UUID
		err = db.QueryRow(ctx, `SELECT id FROM work_items WHERE project_id=$1 AND item_number=$2 LIMIT 1`, projectID, number).Scan(&id)
		if err == nil {
			return &id
		}
	}
	return nil
}

func syncPush(ctx context.Context, db *pgxpool.Pool, eventsClient *events.Client, repo workspaceRepo, branch, sha, message, authorName, authorEmail string, committedAt time.Time) error {
	branch = strings.TrimPrefix(branch, "refs/heads/")
	if branch == "" {
		return nil
	}
	itemID := inferWorkItem(ctx, db, repo.ProjectID, branch+" "+message)
	var branchID uuid.UUID
	var linked *uuid.UUID
	err := db.QueryRow(ctx, `
		INSERT INTO git_branches (repository_id, work_item_id, name, head_commit_sha)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (repository_id, name) DO UPDATE SET
			head_commit_sha=CASE WHEN EXCLUDED.head_commit_sha<>'' THEN EXCLUDED.head_commit_sha ELSE git_branches.head_commit_sha END,
			work_item_id=COALESCE(EXCLUDED.work_item_id, git_branches.work_item_id),
			updated_at=NOW()
		RETURNING id, work_item_id`, repo.RepositoryID, itemID, branch, sha).Scan(&branchID, &linked)
	if err != nil {
		return err
	}
	if linked != nil {
		_, _ = db.Exec(ctx, `UPDATE work_items SET status='in_progress', updated_at=NOW()
			WHERE id=$1 AND status IN ('backlog','ready')`, *linked)
	}
	if sha != "" {
		_, _ = db.Exec(ctx, `
			INSERT INTO git_commits (repository_id, sha, branch_name, message, author_name, author_email, committed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (repository_id, sha) DO UPDATE SET message=EXCLUDED.message,
				branch_name=EXCLUDED.branch_name, committed_at=EXCLUDED.committed_at`,
			repo.RepositoryID, sha, branch, message, authorName, authorEmail, committedAt)
	}
	workspaceNotify(ctx, db, eventsClient, repo.ProjectID, "branch", &branchID, "code_pushed",
		"Code pushed to "+branch, map[string]interface{}{"branch": branch, "commit_sha": sha, "work_item_id": linked})
	return nil
}

type prSyncInput struct {
	Number       int
	Title        string
	Description  string
	Branch       string
	TargetBranch string
	Author       string
	State        string
	Merged       bool
	MergeSHA     string
	HeadSHA      string
	ReviewStatus string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func syncPullRequest(ctx context.Context, db *pgxpool.Pool, eventsClient *events.Client, repo workspaceRepo, in prSyncInput) (uuid.UUID, error) {
	if in.Number <= 0 {
		return uuid.Nil, nil
	}
	if in.TargetBranch == "" {
		in.TargetBranch = repo.DefaultBranch
	}
	itemID := inferWorkItem(ctx, db, repo.ProjectID, in.Branch+" "+in.Title)
	branchID, linked := uuid.Nil, itemID
	if in.Branch != "" {
		var linkedPtr *uuid.UUID
		if err := db.QueryRow(ctx, `
			INSERT INTO git_branches (repository_id, work_item_id, name, head_commit_sha)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (repository_id, name) DO UPDATE SET
				head_commit_sha=CASE WHEN EXCLUDED.head_commit_sha<>'' THEN EXCLUDED.head_commit_sha ELSE git_branches.head_commit_sha END,
				work_item_id=COALESCE(EXCLUDED.work_item_id, git_branches.work_item_id), updated_at=NOW()
			RETURNING id, work_item_id`, repo.RepositoryID, itemID, in.Branch, in.HeadSHA).Scan(&branchID, &linkedPtr); err != nil {
			return uuid.Nil, err
		}
		linked = linkedPtr
	}
	status := "open"
	switch {
	case in.Merged:
		status = "merged"
	case strings.EqualFold(in.State, "closed"):
		status = "closed"
	case strings.EqualFold(in.State, "draft"):
		status = "draft"
	case strings.TrimSpace(in.State) == "":
		status = ""
	}
	review := strings.ToLower(strings.TrimSpace(in.ReviewStatus))
	if review != "approved" && review != "changes_requested" {
		review = "pending"
	}
	var prID uuid.UUID
	var mergedAt interface{}
	if in.Merged {
		mergedAt = in.UpdatedAt.UTC()
		if mergedAt == (time.Time{}) {
			mergedAt = time.Now().UTC()
		}
	}
	err := db.QueryRow(ctx, `
		INSERT INTO pull_requests (repository_id, work_item_id, number, title, description, source_branch_id,
			target_branch, author, status, review_status, merge_commit_sha, merged_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (repository_id, number) DO UPDATE SET
			work_item_id=COALESCE(EXCLUDED.work_item_id, pull_requests.work_item_id),
			title=CASE WHEN EXCLUDED.title<>'' THEN EXCLUDED.title ELSE pull_requests.title END,
			description=CASE WHEN EXCLUDED.description<>'' THEN EXCLUDED.description ELSE pull_requests.description END,
			source_branch_id=COALESCE(EXCLUDED.source_branch_id, pull_requests.source_branch_id),
			target_branch=CASE WHEN EXCLUDED.target_branch<>'' THEN EXCLUDED.target_branch ELSE pull_requests.target_branch END,
			author=CASE WHEN EXCLUDED.author<>'' THEN EXCLUDED.author ELSE pull_requests.author END,
			status=CASE WHEN EXCLUDED.status<>'' THEN EXCLUDED.status ELSE pull_requests.status END,
			review_status=CASE WHEN EXCLUDED.review_status<>'pending' THEN EXCLUDED.review_status ELSE pull_requests.review_status END,
			merge_commit_sha=CASE WHEN EXCLUDED.merge_commit_sha<>'' THEN EXCLUDED.merge_commit_sha ELSE pull_requests.merge_commit_sha END,
			merged_at=COALESCE(EXCLUDED.merged_at, pull_requests.merged_at), updated_at=NOW()
		RETURNING id`,
		repo.RepositoryID, linked, in.Number, in.Title, in.Description, nullUUID(branchID),
		in.TargetBranch, in.Author, status, review, in.MergeSHA, mergedAt).Scan(&prID)
	if err != nil {
		return uuid.Nil, err
	}
	if in.Merged {
		if branchID != uuid.Nil {
			_, _ = db.Exec(ctx, `UPDATE git_branches SET status='merged', merged_at=COALESCE(merged_at,NOW()), updated_at=NOW() WHERE id=$1`, branchID)
		}
		if linked != nil {
			_, _ = db.Exec(ctx, `UPDATE work_items SET status=CASE WHEN type='bug' THEN 'fixed' ELSE 'in_review' END,
				updated_at=NOW() WHERE id=$1 AND status NOT IN ('done','closed','cancelled','verified','fixed')`, *linked)
		}
		// CI results are created only by the Gitea Actions reconciliation
		// path. A merge is not a build and must not fabricate a queued status.
	}
	workspaceNotify(ctx, db, eventsClient, repo.ProjectID, "pull_request", &prID, "synced",
		fmt.Sprintf("PR #%d is %s", in.Number, status), map[string]interface{}{
			"number": in.Number, "status": status, "review_status": review,
			"work_item_id": linked, "merge_commit_sha": in.MergeSHA,
		})
	return prID, nil
}

func syncBuild(ctx context.Context, db *pgxpool.Pool, eventsClient *events.Client, repo workspaceRepo, in ciSyncInput) (uuid.UUID, error) {
	status := normalizeBuildStatus(in.Status, in.Conclusion)
	if status == "" {
		status = "queued"
	}
	var buildID uuid.UUID
	var found bool
	if in.ExternalID != "" {
		err := db.QueryRow(ctx, `SELECT id FROM builds WHERE project_id=$1 AND external_id=$2 ORDER BY created_at DESC LIMIT 1`, repo.ProjectID, in.ExternalID).Scan(&buildID)
		found = err == nil
	}
	if !found && in.CommitSHA != "" {
		err := db.QueryRow(ctx, `SELECT id FROM builds WHERE project_id=$1 AND commit_sha=$2 ORDER BY created_at DESC LIMIT 1`, repo.ProjectID, in.CommitSHA).Scan(&buildID)
		found = err == nil
	}
	metadata, _ := json.Marshal(in.Metadata)
	if found {
		_, err := db.Exec(ctx, `
			UPDATE builds SET status=$2::varchar, logs_url=COALESCE(NULLIF($3,''), logs_url),
				external_id=COALESCE(NULLIF($4,''), external_id), number=COALESCE(NULLIF($5,''), number),
				provider=COALESCE(NULLIF($6,''), provider), metadata=CASE WHEN $7::jsonb='{}'::jsonb THEN metadata ELSE $7 END,
				started_at=COALESCE(started_at, CASE WHEN $2::varchar IN ('running','success','failure') THEN NOW() END),
				completed_at=CASE WHEN $2::varchar IN ('success','failure','cancelled','timed_out') THEN COALESCE(completed_at,NOW()) ELSE completed_at END,
				updated_at=NOW()
			WHERE id=$1`, buildID, status, in.LogsURL, in.ExternalID, in.Number, in.Provider, metadata)
		if err != nil {
			return uuid.Nil, err
		}
	} else {
		provider := in.Provider
		if provider == "" {
			provider = "generic-ci"
		}
		err := db.QueryRow(ctx, `
			INSERT INTO builds (project_id, repository_id, commit_sha, branch, provider, external_id, number,
				status, logs_url, metadata, started_at, completed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8::varchar,$9,$10,
				CASE WHEN $8::varchar IN ('running','success','failure') THEN NOW() END,
				CASE WHEN $8::varchar IN ('success','failure','cancelled','timed_out') THEN NOW() END)
			RETURNING id`, repo.ProjectID, repo.RepositoryID, in.CommitSHA, in.Branch, provider,
			in.ExternalID, in.Number, status, in.LogsURL, metadata).Scan(&buildID)
		if err != nil {
			return uuid.Nil, err
		}
	}
	workspaceNotify(ctx, db, eventsClient, repo.ProjectID, "build", &buildID, "status_changed",
		"Build "+status, map[string]interface{}{"status": status, "commit_sha": in.CommitSHA, "branch": in.Branch})
	// Deployment records are written only by the Argo CD reconciliation path.
	return buildID, nil
}

type ciSyncInput struct {
	CommitSHA  string
	Branch     string
	Status     string
	Conclusion string
	ExternalID string
	Number     string
	LogsURL    string
	Provider   string
	Metadata   map[string]interface{}
}

type argoSyncInput struct {
	ProjectID    uuid.UUID
	AppName      string
	Environment  string
	SyncStatus   string
	HealthStatus string
	Revision     string
	CommitSHA    string
	Message      string
	Details      map[string]interface{}
}

func syncArgo(ctx context.Context, db *pgxpool.Pool, eventsClient *events.Client, in argoSyncInput) (uuid.UUID, error) {
	var projectID uuid.UUID
	if in.ProjectID != uuid.Nil {
		projectID = in.ProjectID
	} else if in.AppName != "" {
		_ = db.QueryRow(ctx, `SELECT id FROM projects WHERE app_name=$1 LIMIT 1`, in.AppName).Scan(&projectID)
	}
	if projectID == uuid.Nil {
		return uuid.Nil, ErrProjectNotFound
	}
	appName := in.AppName
	if appName == "" {
		_ = db.QueryRow(ctx, `SELECT app_name FROM projects WHERE id=$1`, projectID).Scan(&appName)
	}
	envName := strings.TrimSpace(in.Environment)
	var envID uuid.UUID
	if envName != "" {
		_ = db.QueryRow(ctx, `SELECT id FROM environments WHERE project_id=$1 AND (lower(name)=lower($2) OR type=lower($2)) ORDER BY position DESC LIMIT 1`, projectID, envName).Scan(&envID)
	}
	if envID == uuid.Nil {
		_ = db.QueryRow(ctx, `SELECT id FROM environments WHERE project_id=$1 ORDER BY position DESC LIMIT 1`, projectID).Scan(&envID)
	}
	if envID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("no environment configured for project %s", projectID)
	}
	var buildID *uuid.UUID
	if in.CommitSHA != "" {
		var id uuid.UUID
		if err := db.QueryRow(ctx, `SELECT id FROM builds WHERE project_id=$1 AND commit_sha=$2 ORDER BY created_at DESC LIMIT 1`, projectID, in.CommitSHA).Scan(&id); err == nil {
			buildID = &id
		}
	}
	status, health := mapArgoStatus(in.SyncStatus, in.HealthStatus)
	revision := in.Revision
	if revision == "" {
		revision = in.CommitSHA
	}
	var deploymentID uuid.UUID
	err := db.QueryRow(ctx, `
		SELECT id FROM deployments WHERE project_id=$1 AND (
			($2::text <> '' AND argo_app=$2::text AND argo_revision=$3::text) OR
			($4::uuid IS NOT NULL AND build_id=$4::uuid)
		) ORDER BY created_at DESC LIMIT 1`, projectID, appName, revision, buildID).Scan(&deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		version := revision
		if len(version) > 12 {
			version = version[:12]
		}
		details, _ := json.Marshal(in.Details)
		err = db.QueryRow(ctx, `
			INSERT INTO deployments (project_id, environment_id, build_id, status, health_status, version,
				argo_app, argo_revision, metadata, started_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW()) RETURNING id`,
			projectID, envID, buildID, status, health, version, appName, revision, details).Scan(&deploymentID)
	} else if err == nil {
		details, _ := json.Marshal(in.Details)
		_, err = db.Exec(ctx, `
			UPDATE deployments SET status=$2::varchar, health_status=$3::varchar, argo_app=$4, argo_revision=$5,
				build_id=COALESCE($6::uuid, build_id),
				metadata=CASE WHEN $7::jsonb='{}'::jsonb THEN metadata ELSE $7 END,
				completed_at=CASE WHEN $2::varchar IN ('healthy','degraded','failed','rolled_back','cancelled') THEN COALESCE(completed_at,NOW()) ELSE completed_at END,
				updated_at=NOW() WHERE id=$1`, deploymentID, status, health, appName, revision, buildID, details)
	}
	if err != nil {
		return uuid.Nil, err
	}
	if in.HealthStatus != "" {
		var latestStatus string
		_ = db.QueryRow(ctx, `
			SELECT status FROM health_checks WHERE deployment_id=$1
			ORDER BY checked_at DESC LIMIT 1`, deploymentID).Scan(&latestStatus)
		if latestStatus != health {
			httpStatus := 200
			if strings.EqualFold(in.HealthStatus, "Degraded") || strings.EqualFold(in.HealthStatus, "Missing") {
				httpStatus = 503
			}
			details, _ := json.Marshal(in.Details)
			_, _ = db.Exec(ctx, `
				INSERT INTO health_checks (deployment_id, url, status, http_status, details)
				VALUES ($1,$2,$3,$4,$5)`, deploymentID, "", health, httpStatus, details)
		}
	}
	workspaceNotify(ctx, db, eventsClient, projectID, "deployment", &deploymentID, "argo_synced",
		"Argo CD "+appName+": "+in.SyncStatus+"/"+in.HealthStatus, map[string]interface{}{
			"app_name": appName, "sync_status": in.SyncStatus, "health_status": in.HealthStatus,
			"revision": revision, "commit_sha": in.CommitSHA,
		})
	// Argo CD owns rollback decisions and execution. SPM only records the
	// observed health and never creates a synthetic rollback deployment.
	return deploymentID, nil
}

func normalizeBuildStatus(status, conclusion string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	conclusion = strings.ToLower(strings.TrimSpace(conclusion))
	switch status {
	case "queued", "pending", "requested", "waiting":
		return "queued"
	case "in_progress", "running", "started":
		return "running"
	case "success", "succeeded", "passed":
		return "success"
	case "failure", "failed", "error", "errored":
		return "failure"
	case "cancelled", "canceled", "aborted":
		return "cancelled"
	case "timed_out", "timeout":
		return "timed_out"
	}
	switch conclusion {
	case "success":
		return "success"
	case "failure", "timed_out":
		if conclusion == "timed_out" {
			return "timed_out"
		}
		return "failure"
	case "cancelled":
		return "cancelled"
	}
	return status
}

func mapArgoStatus(syncStatus, healthStatus string) (string, string) {
	health := strings.ToLower(strings.TrimSpace(healthStatus))
	sync := strings.ToLower(strings.TrimSpace(syncStatus))
	if sync == "outofsync" || sync == "out_of_sync" {
		return "degraded", "degraded"
	}
	switch health {
	case "healthy":
		return "healthy", "healthy"
	case "degraded":
		return "degraded", "degraded"
	case "missing", "unknown":
		if sync == "synced" {
			return "verifying", "unknown"
		}
		return "deploying", "unknown"
	case "progressing", "suspended":
		return "deploying", "unknown"
	}
	if sync == "synced" {
		return "verifying", "unknown"
	}
	return "deploying", "unknown"
}

func workspaceNotify(ctx context.Context, db *pgxpool.Pool, eventsClient *events.Client, projectID uuid.UUID, entityType string, entityID *uuid.UUID, action, summary string, metadata map[string]interface{}) {
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	body, _ := json.Marshal(metadata)
	var id uuid.UUID
	if err := db.QueryRow(ctx, `
		INSERT INTO activity_events (project_id, actor_id, entity_type, entity_id, action, summary, metadata)
		VALUES ($1,NULL,$2,$3,$4,$5,$6) RETURNING id`,
		projectID, entityType, entityID, action, summary, body).Scan(&id); err != nil {
		return
	}
	if eventsClient == nil {
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"entity_type": entityType, "entity_id": entityID, "action": action, "summary": summary,
	})
	_, _ = eventsClient.Publish(ctx, models.EventEnvelope{
		EventType: "workspace.changed", AggregateID: projectID, Payload: payload, CreatedAt: time.Now().UTC(),
	}, 0)
}

func nullUUID(id uuid.UUID) interface{} {
	if id == uuid.Nil {
		return nil
	}
	return id
}
