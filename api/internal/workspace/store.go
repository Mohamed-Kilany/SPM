package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store owns the normalized SDLC/STLC read and write queries.
type Store struct {
	db *pgxpool.Pool
}

// NewStore creates a workspace store.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// DB exposes the pool for handlers that need a transaction.
func (s *Store) DB() *pgxpool.Pool { return s.db }

// Load aggregates the full workspace for a project.
func (s *Store) Load(ctx context.Context, projectID uuid.UUID) (Workspace, error) {
	var w Workspace
	if err := s.loadProject(ctx, projectID, &w); err != nil {
		return Workspace{}, err
	}
	if err := s.loadMembers(ctx, projectID, &w); err != nil {
		return Workspace{}, err
	}
	if err := s.loadPlanning(ctx, projectID, &w); err != nil {
		return Workspace{}, err
	}
	if err := s.loadWorkItems(ctx, projectID, &w); err != nil {
		return Workspace{}, err
	}
	if err := s.loadTests(ctx, projectID, &w); err != nil {
		return Workspace{}, err
	}
	if err := s.loadGit(ctx, projectID, &w); err != nil {
		return Workspace{}, err
	}
	if err := s.loadDelivery(ctx, projectID, &w); err != nil {
		return Workspace{}, err
	}
	if err := s.loadLinksAndActivity(ctx, projectID, &w); err != nil {
		return Workspace{}, err
	}
	w.Metrics = computeMetrics(w)
	w.Revision = time.Now().UnixMilli()
	return w, nil
}

func (s *Store) loadProject(ctx context.Context, projectID uuid.UUID, w *Workspace) error {
	const q = `
		SELECT id, name, COALESCE(description, ''), COALESCE(key_prefix, ''), COALESCE(status, 'active'),
		       owner_id, repo_name, default_branch, app_name, COALESCE(settings, '{}'::jsonb), created_at, updated_at
		FROM projects WHERE id = $1`
	var rawSettings []byte
	err := s.db.QueryRow(ctx, q, projectID).Scan(
		&w.Project.ID, &w.Project.Name, &w.Project.Description, &w.Project.KeyPrefix,
		&w.Project.Status, &w.Project.OwnerID, &w.Project.RepoName, &w.Project.DefaultBranch,
		&w.Project.AppName, &rawSettings, &w.Project.CreatedAt, &w.Project.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return pgx.ErrNoRows
	}
	if err != nil {
		return err
	}
	w.Project.Settings = decodeSettings(rawSettings)
	return nil
}

func (s *Store) loadMembers(ctx context.Context, projectID uuid.UUID, w *Workspace) error {
	const q = `
		SELECT pm.user_id, COALESCE(u.username, ''), COALESCE(u.email, ''), pm.role
		FROM project_members pm
		LEFT JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = $1
		ORDER BY u.username NULLS LAST`
	rows, err := s.db.Query(ctx, q, projectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	w.Members = []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Username, &m.Email, &m.Role); err != nil {
			return err
		}
		w.Members = append(w.Members, m)
	}
	return rows.Err()
}

func (s *Store) loadPlanning(ctx context.Context, projectID uuid.UUID, w *Workspace) error {
	// PRDs
	prdRows, err := s.db.Query(ctx, `
		SELECT id, project_id, title, version, status, COALESCE(summary, ''), COALESCE(content, ''),
		       COALESCE(owner_id::text, ''), COALESCE(approved_by::text, ''), approved_at, created_at, updated_at
		FROM prds WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer prdRows.Close()
	w.PRDs = []PRD{}
	for prdRows.Next() {
		var p PRD
		var owner, approved string
		if err := prdRows.Scan(&p.ID, &p.ProjectID, &p.Title, &p.Version, &p.Status, &p.Summary,
			&p.Content, &owner, &approved, &p.ApprovedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return err
		}
		p.OwnerID = parseUUIDPtr(owner)
		p.ApprovedBy = parseUUIDPtr(approved)
		w.PRDs = append(w.PRDs, p)
	}
	if err := prdRows.Err(); err != nil {
		return err
	}

	// Releases
	relRows, err := s.db.Query(ctx, `
		SELECT id, project_id, name, version, status, planned_at, released_at, COALESCE(notes, ''), created_at, updated_at
		FROM releases WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer relRows.Close()
	w.Releases = []Release{}
	for relRows.Next() {
		var r Release
		if err := relRows.Scan(&r.ID, &r.ProjectID, &r.Name, &r.Version, &r.Status, &r.PlannedAt,
			&r.ReleasedAt, &r.Notes, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return err
		}
		w.Releases = append(w.Releases, r)
	}
	if err := relRows.Err(); err != nil {
		return err
	}

	// Sprints
	sprintRows, err := s.db.Query(ctx, `
		SELECT id, project_id, name, COALESCE(goal, ''), status, start_date, end_date, capacity, created_at, updated_at
		FROM sprints WHERE project_id = $1 ORDER BY start_date DESC NULLS LAST, created_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer sprintRows.Close()
	w.Sprints = []Sprint{}
	for sprintRows.Next() {
		var sp Sprint
		if err := sprintRows.Scan(&sp.ID, &sp.ProjectID, &sp.Name, &sp.Goal, &sp.Status, &sp.StartDate,
			&sp.EndDate, &sp.Capacity, &sp.CreatedAt, &sp.UpdatedAt); err != nil {
			return err
		}
		w.Sprints = append(w.Sprints, sp)
	}
	if err := sprintRows.Err(); err != nil {
		return err
	}

	// Epics
	epicRows, err := s.db.Query(ctx, `
		SELECT id, project_id, COALESCE(prd_id::text, ''), title, COALESCE(description, ''), status, priority,
		       COALESCE(owner_id::text, ''), COALESCE(target_release_id::text, ''), start_date, due_date, created_at, updated_at
		FROM epics WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer epicRows.Close()
	w.Epics = []Epic{}
	for epicRows.Next() {
		var e Epic
		var prd, owner, release string
		if err := epicRows.Scan(&e.ID, &e.ProjectID, &prd, &e.Title, &e.Description, &e.Status, &e.Priority,
			&owner, &release, &e.StartDate, &e.DueDate, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return err
		}
		e.PRDID = parseUUIDPtr(prd)
		e.OwnerID = parseUUIDPtr(owner)
		e.TargetReleaseID = parseUUIDPtr(release)
		w.Epics = append(w.Epics, e)
	}
	if err := epicRows.Err(); err != nil {
		return err
	}

	// Features
	featureRows, err := s.db.Query(ctx, `
		SELECT id, project_id, epic_id, title, COALESCE(description, ''), COALESCE(acceptance_criteria, ''),
		       status, priority, COALESCE(owner_id::text, ''), estimate_points,
		       COALESCE(target_release_id::text, ''), created_at, updated_at
		FROM features WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer featureRows.Close()
	w.Features = []Feature{}
	for featureRows.Next() {
		var f Feature
		var owner, release string
		if err := featureRows.Scan(&f.ID, &f.ProjectID, &f.EpicID, &f.Title, &f.Description,
			&f.AcceptanceCriteria, &f.Status, &f.Priority, &owner, &f.EstimatePoints, &release,
			&f.CreatedAt, &f.UpdatedAt); err != nil {
			return err
		}
		f.OwnerID = parseUUIDPtr(owner)
		f.TargetReleaseID = parseUUIDPtr(release)
		w.Features = append(w.Features, f)
	}
	return featureRows.Err()
}

func (s *Store) loadWorkItems(ctx context.Context, projectID uuid.UUID, w *Workspace) error {
	const q = `
		SELECT id, project_id, item_number, COALESCE(feature_id::text, ''), COALESCE(parent_id::text, ''),
		       type, title, COALESCE(description, ''), COALESCE(acceptance_criteria, ''), status, priority,
		       severity, COALESCE(assignee_id::text, ''), COALESCE(reporter_id::text, ''), estimate_points,
		       COALESCE(sprint_id::text, ''), COALESCE(release_id::text, ''), COALESCE(environment_id::text, ''),
		       due_date, COALESCE(reproduction_steps, ''), COALESCE(expected_result, ''), COALESCE(actual_result, ''),
		       created_at, updated_at, closed_at
		FROM work_items WHERE project_id = $1 ORDER BY item_number DESC`
	rows, err := s.db.Query(ctx, q, projectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	w.WorkItems = []WorkItem{}
	for rows.Next() {
		var it WorkItem
		var feature, parent, assignee, reporter, sprint, release, env string
		if err := rows.Scan(&it.ID, &it.ProjectID, &it.ItemNumber, &feature, &parent, &it.Type,
			&it.Title, &it.Description, &it.AcceptanceCriteria, &it.Status, &it.Priority, &it.Severity,
			&assignee, &reporter, &it.EstimatePoints, &sprint, &release, &env, &it.DueDate,
			&it.ReproductionSteps, &it.ExpectedResult, &it.ActualResult, &it.CreatedAt, &it.UpdatedAt,
			&it.ClosedAt); err != nil {
			return err
		}
		it.FeatureID = parseUUIDPtr(feature)
		it.ParentID = parseUUIDPtr(parent)
		it.AssigneeID = parseUUIDPtr(assignee)
		it.ReporterID = parseUUIDPtr(reporter)
		it.SprintID = parseUUIDPtr(sprint)
		it.ReleaseID = parseUUIDPtr(release)
		it.EnvironmentID = parseUUIDPtr(env)
		it.Key = w.Project.KeyPrefix + "-" + strconv.FormatInt(it.ItemNumber, 10)
		w.WorkItems = append(w.WorkItems, it)
	}
	return rows.Err()
}

func (s *Store) loadTests(ctx context.Context, projectID uuid.UUID, w *Workspace) error {
	// Test cases
	caseRows, err := s.db.Query(ctx, `
		SELECT id, project_id, COALESCE(feature_id::text, ''), title, COALESCE(description, ''),
		       COALESCE(preconditions, ''), type, priority, status, COALESCE(automation_ref, ''),
		       COALESCE(owner_id::text, ''), tags, created_at, updated_at
		FROM test_cases WHERE project_id = $1 ORDER BY updated_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer caseRows.Close()
	w.TestCases = []TestCase{}
	byCase := map[uuid.UUID]int{}
	for caseRows.Next() {
		var tc TestCase
		var feature, owner string
		if err := caseRows.Scan(&tc.ID, &tc.ProjectID, &feature, &tc.Title, &tc.Description,
			&tc.Preconditions, &tc.Type, &tc.Priority, &tc.Status, &tc.AutomationRef, &owner,
			&tc.Tags, &tc.CreatedAt, &tc.UpdatedAt); err != nil {
			return err
		}
		tc.FeatureID = parseUUIDPtr(feature)
		tc.OwnerID = parseUUIDPtr(owner)
		tc.Steps = []TestStep{}
		w.TestCases = append(w.TestCases, tc)
		byCase[tc.ID] = len(w.TestCases) - 1
	}
	if err := caseRows.Err(); err != nil {
		return err
	}

	// Steps for every case in the project
	stepRows, err := s.db.Query(ctx, `
		SELECT s.id, s.test_case_id, s.position, s.action, s.expected_result, COALESCE(s.test_data, ''),
		       s.created_at, s.updated_at
		FROM test_steps s
		JOIN test_cases c ON c.id = s.test_case_id
		WHERE c.project_id = $1
		ORDER BY s.test_case_id, s.position`, projectID)
	if err != nil {
		return err
	}
	defer stepRows.Close()
	for stepRows.Next() {
		var st TestStep
		if err := stepRows.Scan(&st.ID, &st.TestCaseID, &st.Position, &st.Action, &st.ExpectedResult,
			&st.TestData, &st.CreatedAt, &st.UpdatedAt); err != nil {
			return err
		}
		if index, ok := byCase[st.TestCaseID]; ok {
			w.TestCases[index].Steps = append(w.TestCases[index].Steps, st)
		}
	}
	if err := stepRows.Err(); err != nil {
		return err
	}

	// Test runs
	runRows, err := s.db.Query(ctx, `
		SELECT id, project_id, COALESCE(release_id::text, ''), COALESCE(sprint_id::text, ''),
		       COALESCE(environment_id::text, ''), COALESCE(build_id::text, ''), name, status,
		       COALESCE(executed_by::text, ''), started_at, completed_at, COALESCE(import_source, ''),
		       COALESCE(import_ref, ''), COALESCE(metadata, '{}'::jsonb), created_at, updated_at
		FROM test_runs WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer runRows.Close()
	w.TestRuns = []TestRun{}
	byRun := map[uuid.UUID]int{}
	for runRows.Next() {
		var tr TestRun
		var release, sprint, env, build, executed string
		var metadata []byte
		if err := runRows.Scan(&tr.ID, &tr.ProjectID, &release, &sprint, &env, &build, &tr.Name,
			&tr.Status, &executed, &tr.StartedAt, &tr.CompletedAt, &tr.ImportSource, &tr.ImportRef,
			&metadata, &tr.CreatedAt, &tr.UpdatedAt); err != nil {
			return err
		}
		tr.ReleaseID = parseUUIDPtr(release)
		tr.SprintID = parseUUIDPtr(sprint)
		tr.EnvironmentID = parseUUIDPtr(env)
		tr.BuildID = parseUUIDPtr(build)
		tr.ExecutedBy = parseUUIDPtr(executed)
		tr.Metadata = map[string]interface{}{}
		if len(metadata) > 0 {
			_ = json.Unmarshal(metadata, &tr.Metadata)
		}
		tr.Results = []TestResult{}
		w.TestRuns = append(w.TestRuns, tr)
		byRun[tr.ID] = len(w.TestRuns) - 1
	}
	if err := runRows.Err(); err != nil {
		return err
	}

	// Test results
	resultRows, err := s.db.Query(ctx, `
		SELECT r.id, r.test_run_id, r.test_case_id, r.status, COALESCE(r.executed_by::text, ''),
		       r.duration_ms, COALESCE(r.actual_result, ''), COALESCE(r.bug_work_item_id::text, ''),
		       r.started_at, r.completed_at, r.created_at, r.updated_at
		FROM test_results r
		JOIN test_runs tr ON tr.id = r.test_run_id
		WHERE tr.project_id = $1
		ORDER BY r.created_at`, projectID)
	if err != nil {
		return err
	}
	defer resultRows.Close()
	w.TestResults = []TestResult{}
	byResult := map[uuid.UUID]int{}
	runResults := map[uuid.UUID][]int{}
	for resultRows.Next() {
		var tr TestResult
		var executed, bug string
		if err := resultRows.Scan(&tr.ID, &tr.TestRunID, &tr.TestCaseID, &tr.Status, &executed,
			&tr.DurationMS, &tr.ActualResult, &bug, &tr.StartedAt, &tr.CompletedAt,
			&tr.CreatedAt, &tr.UpdatedAt); err != nil {
			return err
		}
		tr.ExecutedBy = parseUUIDPtr(executed)
		tr.BugWorkItemID = parseUUIDPtr(bug)
		tr.StepResults = []TestStepResult{}
		w.TestResults = append(w.TestResults, tr)
		byResult[tr.ID] = len(w.TestResults) - 1
		if _, ok := byRun[tr.TestRunID]; ok {
			runResults[tr.TestRunID] = append(runResults[tr.TestRunID], len(w.TestResults)-1)
		}
	}
	if err := resultRows.Err(); err != nil {
		return err
	}

	// Step results
	stepResultRows, err := s.db.Query(ctx, `
		SELECT sr.id, sr.test_result_id, sr.test_step_id, sr.status, COALESCE(sr.actual_result, ''),
		       COALESCE(sr.evidence_url, ''), COALESCE(sr.bug_work_item_id::text, ''), sr.created_at, sr.updated_at
		FROM test_step_results sr
		JOIN test_results r ON r.id = sr.test_result_id
		JOIN test_runs tr ON tr.id = r.test_run_id
		WHERE tr.project_id = $1
		ORDER BY sr.test_result_id, sr.created_at`, projectID)
	if err != nil {
		return err
	}
	defer stepResultRows.Close()
	w.StepResults = []TestStepResult{}
	for stepResultRows.Next() {
		var sr TestStepResult
		var bug string
		if err := stepResultRows.Scan(&sr.ID, &sr.TestResultID, &sr.TestStepID, &sr.Status,
			&sr.ActualResult, &sr.EvidenceURL, &bug, &sr.CreatedAt, &sr.UpdatedAt); err != nil {
			return err
		}
		sr.BugWorkItemID = parseUUIDPtr(bug)
		w.StepResults = append(w.StepResults, sr)
		if index, ok := byResult[sr.TestResultID]; ok {
			w.TestResults[index].StepResults = append(w.TestResults[index].StepResults, sr)
		}
	}
	if err := stepResultRows.Err(); err != nil {
		return err
	}

	// Nest the fully-populated results under their runs. Indexes are used above
	// because appending to a slice can reallocate and invalidate pointers.
	for runID, indexes := range runResults {
		runIndex, ok := byRun[runID]
		if !ok {
			continue
		}
		w.TestRuns[runIndex].Results = make([]TestResult, 0, len(indexes))
		for _, index := range indexes {
			w.TestRuns[runIndex].Results = append(w.TestRuns[runIndex].Results, w.TestResults[index])
		}
	}
	return nil
}

func (s *Store) loadGit(ctx context.Context, projectID uuid.UUID, w *Workspace) error {
	repoRows, err := s.db.Query(ctx, `
		SELECT id, project_id, provider, name, COALESCE(url, ''), default_branch, COALESCE(external_id, ''),
		       COALESCE(webhook_token, ''), COALESCE(webhook_url, ''), created_at, updated_at
		FROM git_repositories WHERE project_id = $1 ORDER BY name`, projectID)
	if err != nil {
		return err
	}
	defer repoRows.Close()
	w.Repositories = []GitRepository{}
	for repoRows.Next() {
		var r GitRepository
		if err := repoRows.Scan(&r.ID, &r.ProjectID, &r.Provider, &r.Name, &r.URL,
			&r.DefaultBranch, &r.ExternalID, &r.WebhookToken, &r.WebhookURL, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return err
		}
		w.Repositories = append(w.Repositories, r)
	}
	if err := repoRows.Err(); err != nil {
		return err
	}

	branchRows, err := s.db.Query(ctx, `
		SELECT b.id, b.repository_id, COALESCE(b.work_item_id::text, ''), b.name,
		       COALESCE(b.head_commit_sha, ''), b.status, COALESCE(b.created_by::text, ''),
		       b.created_at, b.updated_at, b.merged_at
		FROM git_branches b
		JOIN git_repositories r ON r.id = b.repository_id
		WHERE r.project_id = $1 ORDER BY b.updated_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer branchRows.Close()
	w.Branches = []GitBranch{}
	for branchRows.Next() {
		var b GitBranch
		var item, creator string
		if err := branchRows.Scan(&b.ID, &b.RepositoryID, &item, &b.Name, &b.HeadCommitSHA,
			&b.Status, &creator, &b.CreatedAt, &b.UpdatedAt, &b.MergedAt); err != nil {
			return err
		}
		b.WorkItemID = parseUUIDPtr(item)
		b.CreatedBy = parseUUIDPtr(creator)
		w.Branches = append(w.Branches, b)
	}
	if err := branchRows.Err(); err != nil {
		return err
	}

	commitRows, err := s.db.Query(ctx, `
		SELECT c.id, c.repository_id, c.sha, COALESCE(c.branch_name, ''), COALESCE(c.message, ''),
		       COALESCE(c.author_name, ''), COALESCE(c.author_email, ''), c.committed_at, c.created_at
		FROM git_commits c
		JOIN git_repositories r ON r.id = c.repository_id
		WHERE r.project_id = $1 ORDER BY c.committed_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer commitRows.Close()
	w.Commits = []GitCommit{}
	for commitRows.Next() {
		var c GitCommit
		if err := commitRows.Scan(&c.ID, &c.RepositoryID, &c.SHA, &c.BranchName, &c.Message,
			&c.AuthorName, &c.AuthorEmail, &c.CommittedAt, &c.CreatedAt); err != nil {
			return err
		}
		w.Commits = append(w.Commits, c)
	}
	if err := commitRows.Err(); err != nil {
		return err
	}

	prRows, err := s.db.Query(ctx, `
		SELECT p.id, p.repository_id, COALESCE(p.work_item_id::text, ''), p.number, p.title,
		       COALESCE(p.description, ''), COALESCE(p.source_branch_id::text, ''), p.target_branch,
		       COALESCE(p.author, ''), p.status, p.review_status, COALESCE(p.merge_commit_sha, ''),
		       p.created_at, p.updated_at, p.merged_at
		FROM pull_requests p
		JOIN git_repositories r ON r.id = p.repository_id
		WHERE r.project_id = $1 ORDER BY p.updated_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer prRows.Close()
	w.PullRequests = []PullRequest{}
	for prRows.Next() {
		var pr PullRequest
		var item, source string
		if err := prRows.Scan(&pr.ID, &pr.RepositoryID, &item, &pr.Number, &pr.Title, &pr.Description,
			&source, &pr.TargetBranch, &pr.Author, &pr.Status, &pr.ReviewStatus, &pr.MergeCommitSHA,
			&pr.CreatedAt, &pr.UpdatedAt, &pr.MergedAt); err != nil {
			return err
		}
		pr.WorkItemID = parseUUIDPtr(item)
		pr.SourceBranchID = parseUUIDPtr(source)
		w.PullRequests = append(w.PullRequests, pr)
	}
	return prRows.Err()
}

func (s *Store) loadDelivery(ctx context.Context, projectID uuid.UUID, w *Workspace) error {
	envRows, err := s.db.Query(ctx, `
		SELECT id, project_id, name, type, COALESCE(url, ''), COALESCE(health_check_url, ''),
		       deploy_strategy, requires_approval, position, created_at, updated_at
		FROM environments WHERE project_id = $1 ORDER BY position, name`, projectID)
	if err != nil {
		return err
	}
	defer envRows.Close()
	w.Environments = []Environment{}
	for envRows.Next() {
		var e Environment
		if err := envRows.Scan(&e.ID, &e.ProjectID, &e.Name, &e.Type, &e.URL, &e.HealthCheckURL,
			&e.DeployStrategy, &e.RequiresApproval, &e.Position, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return err
		}
		w.Environments = append(w.Environments, e)
	}
	if err := envRows.Err(); err != nil {
		return err
	}

	buildRows, err := s.db.Query(ctx, `
		SELECT id, project_id, COALESCE(repository_id::text, ''), commit_sha, branch, provider,
		       COALESCE(external_id, ''), COALESCE(number, ''), status, COALESCE(logs_url, ''),
		       COALESCE(triggered_by::text, ''), COALESCE(metadata, '{}'::jsonb), started_at,
		       completed_at, created_at, updated_at
		FROM builds WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer buildRows.Close()
	w.Builds = []Build{}
	for buildRows.Next() {
		var b Build
		var repo, triggered string
		var metadata []byte
		if err := buildRows.Scan(&b.ID, &b.ProjectID, &repo, &b.CommitSHA, &b.Branch, &b.Provider,
			&b.ExternalID, &b.Number, &b.Status, &b.LogsURL, &triggered, &metadata, &b.StartedAt,
			&b.CompletedAt, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return err
		}
		b.RepositoryID = parseUUIDPtr(repo)
		b.TriggeredBy = parseUUIDPtr(triggered)
		b.Metadata = map[string]interface{}{}
		if len(metadata) > 0 {
			_ = json.Unmarshal(metadata, &b.Metadata)
		}
		w.Builds = append(w.Builds, b)
	}
	if err := buildRows.Err(); err != nil {
		return err
	}

	deployRows, err := s.db.Query(ctx, `
		SELECT id, project_id, environment_id, COALESCE(build_id::text, ''), COALESCE(release_id::text, ''),
		       status, health_status, COALESCE(version, ''), COALESCE(argo_app, ''),
		       COALESCE(argo_revision, ''), COALESCE(metadata, '{}'::jsonb), COALESCE(deployed_by::text, ''),
		       COALESCE(previous_deployment_id::text, ''), COALESCE(rollback_of_id::text, ''),
		       started_at, completed_at, created_at, updated_at
		FROM deployments WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer deployRows.Close()
	w.Deployments = []Deployment{}
	for deployRows.Next() {
		var d Deployment
		var build, release, deployedBy, previous, rollback string
		var metadata []byte
		if err := deployRows.Scan(&d.ID, &d.ProjectID, &d.EnvironmentID, &build, &release, &d.Status,
			&d.HealthStatus, &d.Version, &d.ArgoApp, &d.ArgoRevision, &metadata, &deployedBy,
			&previous, &rollback, &d.StartedAt, &d.CompletedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return err
		}
		d.BuildID = parseUUIDPtr(build)
		d.ReleaseID = parseUUIDPtr(release)
		d.DeployedBy = parseUUIDPtr(deployedBy)
		d.PreviousDeploymentID = parseUUIDPtr(previous)
		d.RollbackOfID = parseUUIDPtr(rollback)
		d.Metadata = map[string]interface{}{}
		if len(metadata) > 0 {
			_ = json.Unmarshal(metadata, &d.Metadata)
		}
		w.Deployments = append(w.Deployments, d)
	}
	if err := deployRows.Err(); err != nil {
		return err
	}

	healthRows, err := s.db.Query(ctx, `
		SELECT h.id, h.deployment_id, COALESCE(h.url, ''), h.status, h.http_status, h.latency_ms,
		       COALESCE(h.details, '{}'::jsonb), h.checked_at, h.created_at
		FROM health_checks h
		JOIN deployments d ON d.id = h.deployment_id
		WHERE d.project_id = $1 ORDER BY h.checked_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer healthRows.Close()
	w.HealthChecks = []HealthCheck{}
	for healthRows.Next() {
		var hc HealthCheck
		var details []byte
		if err := healthRows.Scan(&hc.ID, &hc.DeploymentID, &hc.URL, &hc.Status, &hc.HTTPStatus,
			&hc.LatencyMS, &details, &hc.CheckedAt, &hc.CreatedAt); err != nil {
			return err
		}
		if len(details) > 0 {
			_ = json.Unmarshal(details, &hc.Details)
		}
		if hc.Details == nil {
			hc.Details = map[string]interface{}{}
		}
		w.HealthChecks = append(w.HealthChecks, hc)
	}
	return healthRows.Err()
}

func (s *Store) loadLinksAndActivity(ctx context.Context, projectID uuid.UUID, w *Workspace) error {
	linkRows, err := s.db.Query(ctx, `
		SELECT id, project_id, source_type, source_id, target_type, target_id, link_type,
		       COALESCE(created_by::text, ''), created_at
		FROM artifact_links WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer linkRows.Close()
	w.Links = []ArtifactLink{}
	for linkRows.Next() {
		var l ArtifactLink
		var creator string
		if err := linkRows.Scan(&l.ID, &l.ProjectID, &l.SourceType, &l.SourceID, &l.TargetType,
			&l.TargetID, &l.LinkType, &creator, &l.CreatedAt); err != nil {
			return err
		}
		l.CreatedBy = parseUUIDPtr(creator)
		w.Links = append(w.Links, l)
	}
	if err := linkRows.Err(); err != nil {
		return err
	}

	activityRows, err := s.db.Query(ctx, `
		SELECT id, project_id, COALESCE(actor_id::text, ''), entity_type, COALESCE(entity_id::text, ''), action,
		       COALESCE(summary, ''), COALESCE(metadata, '{}'::jsonb), created_at
		FROM activity_events WHERE project_id = $1 ORDER BY created_at DESC LIMIT 200`, projectID)
	if err != nil {
		return err
	}
	defer activityRows.Close()
	w.Activity = []ActivityEvent{}
	for activityRows.Next() {
		var a ActivityEvent
		var actor, entity string
		var metadata []byte
		if err := activityRows.Scan(&a.ID, &a.ProjectID, &actor, &a.EntityType, &entity,
			&a.Action, &a.Summary, &metadata, &a.CreatedAt); err != nil {
			return err
		}
		a.ActorID = parseUUIDPtr(actor)
		a.EntityID = parseUUIDPtr(entity)
		if len(metadata) > 0 {
			_ = json.Unmarshal(metadata, &a.Metadata)
		}
		if a.Metadata == nil {
			a.Metadata = map[string]interface{}{}
		}
		w.Activity = append(w.Activity, a)
	}
	return activityRows.Err()
}

func computeMetrics(w Workspace) Metrics {
	m := Metrics{
		PRDCount:        len(w.PRDs),
		EpicCount:       len(w.Epics),
		FeatureCount:    len(w.Features),
		WorkItemCount:   len(w.WorkItems),
		TestCaseCount:   len(w.TestCases),
		TestRunCount:    len(w.TestRuns),
		DeploymentCount: len(w.Deployments),
		TestTrend:       []TestTrendPoint{},
	}
	for _, it := range w.WorkItems {
		if it.Type == TypeBug && it.Status != StatusClosed && it.Status != StatusCancelled && it.Status != StatusVerified {
			m.OpenBugCount++
		}
	}
	totalResults := 0
	passed := 0
	for _, r := range w.TestResults {
		totalResults++
		if r.Status == TestPassed {
			passed++
		}
	}
	if totalResults > 0 {
		m.PassRate = float64(passed) / float64(totalResults) * 100
	}
	testedFeatures := map[uuid.UUID]bool{}
	for _, tc := range w.TestCases {
		if tc.FeatureID != nil {
			testedFeatures[*tc.FeatureID] = true
		}
	}
	for _, f := range w.Features {
		if !testedFeatures[f.ID] {
			m.UntestedFeatures++
		}
	}
	linkedFeatures := map[uuid.UUID]bool{}
	for _, it := range w.WorkItems {
		if it.FeatureID != nil {
			linkedFeatures[*it.FeatureID] = true
		}
	}
	for _, f := range w.Features {
		if !linkedFeatures[f.ID] {
			m.UnlinkedFeatures++
		}
	}
	if len(w.Features) > 0 {
		m.CoveragePercent = float64(len(w.Features)-m.UntestedFeatures) / float64(len(w.Features)) * 100
	}
	for _, d := range w.Deployments {
		if d.HealthStatus == "healthy" || d.Status == "healthy" {
			m.HealthyDeployments++
		}
		if d.HealthStatus == "degraded" || d.HealthStatus == "unhealthy" || d.Status == "degraded" {
			m.DegradedDeployments++
		}
	}
	m.FlakyTestCount = flakyTestCount(w)
	m.TestTrend = testTrend(w)
	m.CycleTimeHours = cycleTimeHours(w)
	m.DeliveryConfidence = deliveryConfidence(w, m)
	return m
}

// flakyTestCount counts test cases that have both passed and failed across the
// most recent executions. It is intentionally based on case identity rather
// than a single run so a retried case is visible.
func flakyTestCount(w Workspace) int {
	seen := map[uuid.UUID]map[string]bool{}
	for _, r := range w.TestResults {
		if r.Status != TestPassed && r.Status != TestFailed {
			continue
		}
		if seen[r.TestCaseID] == nil {
			seen[r.TestCaseID] = map[string]bool{}
		}
		seen[r.TestCaseID][r.Status] = true
	}
	count := 0
	for _, statuses := range seen {
		if statuses[TestPassed] && statuses[TestFailed] {
			count++
		}
	}
	return count
}

func testTrend(w Workspace) []TestTrendPoint {
	points := make([]TestTrendPoint, 0, len(w.TestRuns))
	// TestRuns are newest-first; produce the last 12 completed runs oldest-first.
	for _, run := range w.TestRuns {
		if run.Status != "completed" && run.Status != "aborted" {
			continue
		}
		total, pass, fail := 0, 0, 0
		for _, result := range run.Results {
			if result.Status == TestNotRun {
				continue
			}
			total++
			switch result.Status {
			case TestPassed:
				pass++
			case TestFailed:
				fail++
			}
		}
		point := TestTrendPoint{
			RunID: run.ID, Name: run.Name, Total: total, Passed: pass, Failed: fail,
			Date: run.CreatedAt.UTC().Format("2006-01-02"),
		}
		if total > 0 {
			point.PassRate = float64(pass) / float64(total) * 100
		}
		points = append(points, point)
		if len(points) == 12 {
			break
		}
	}
	for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
		points[i], points[j] = points[j], points[i]
	}
	return points
}

func cycleTimeHours(w Workspace) float64 {
	var total float64
	var count int
	for _, item := range w.WorkItems {
		if item.ClosedAt == nil || item.CreatedAt.IsZero() || item.ClosedAt.Before(item.CreatedAt) {
			continue
		}
		total += item.ClosedAt.Sub(item.CreatedAt).Hours()
		count++
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func deliveryConfidence(w Workspace, m Metrics) float64 {
	if len(w.Features) == 0 && len(w.WorkItems) == 0 {
		return 0
	}
	score := 0.0
	score += m.PassRate * 0.35
	score += m.CoveragePercent * 0.25
	if len(w.Environments) > 0 {
		score += (float64(m.HealthyDeployments) / float64(len(w.Environments))) * 0.20 * 100
	}
	openCritical := 0
	for _, item := range w.WorkItems {
		if item.Type == TypeBug && item.Status != StatusClosed && item.Status != StatusCancelled && item.Status != StatusVerified &&
			item.Priority == PriorityCritical {
			openCritical++
		}
	}
	bugScore := 100.0
	if openCritical > 0 {
		bugScore = 0
	} else if m.OpenBugCount > 0 {
		bugScore = 70
	}
	score += bugScore * 0.20
	if score > 100 {
		return 100
	}
	if score < 0 {
		return 0
	}
	return score
}

func decodeSettings(raw []byte) ProjectSettings {
	settings := DefaultProjectSettings()
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &settings)
	}
	return settings
}

func parseUUIDPtr(raw string) *uuid.UUID {
	if raw == "" {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

func nullString(raw string) interface{} {
	if raw == "" {
		return nil
	}
	return raw
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func requireProject(ctx context.Context, db *pgxpool.Pool, projectID uuid.UUID) error {
	var exists bool
	if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1)`, projectID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return pgx.ErrNoRows
	}
	return nil
}

func parseTimePtr(raw *string) (*time.Time, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, *raw); err == nil {
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", *raw); err == nil {
		return &t, nil
	}
	return nil, fmt.Errorf("invalid timestamp %q: expected RFC3339 or YYYY-MM-DD", *raw)
}
