// Package workspace implements the normalized SDLC/STLC domain: PRDs, epics,
// features, work items, test management, source-control links and delivery
// evidence. The legacy event-sourced board remains available for compatibility;
// new screens use the /workspace aggregate.
package workspace

import (
	"time"

	"github.com/google/uuid"
)

// Canonical work item types.
const (
	TypeStory   = "story"
	TypeTask    = "task"
	TypeSubtask = "subtask"
	TypeBug     = "bug"
	TypeSpike   = "spike"
	TypeChore   = "chore"
)

// Canonical work item statuses.
const (
	StatusBacklog    = "backlog"
	StatusReady      = "ready"
	StatusInProgress = "in_progress"
	StatusInReview   = "in_review"
	StatusInQA       = "in_qa"
	StatusDone       = "done"
	StatusBlocked    = "blocked"
	StatusCancelled  = "cancelled"
	StatusNew        = "new"
	StatusTriaged    = "triaged"
	StatusFixed      = "fixed"
	StatusVerified   = "verified"
	StatusClosed     = "closed"
	StatusReopened   = "reopened"
)

// Canonical priorities.
const (
	PriorityLow      = "low"
	PriorityMedium   = "medium"
	PriorityHigh     = "high"
	PriorityCritical = "critical"
)

// Canonical test statuses.
const (
	TestNotRun  = "not_run"
	TestPassed  = "passed"
	TestFailed  = "failed"
	TestBlocked = "blocked"
	TestSkipped = "skipped"
)

// MergePolicy controls the evidence required before a pull request can be
// merged from the delivery console. Defaults are deliberately conservative but
// can be relaxed per project for early-stage repositories.
type MergePolicy struct {
	RequireLinkedWorkItem bool `json:"require_linked_work_item"`
	RequireApprovedReview bool `json:"require_approved_review"`
	RequirePassingChecks  bool `json:"require_passing_checks"`
}

// ProjectSettings stores configurable delivery and automation policy.
type ProjectSettings struct {
	MergePolicy        MergePolicy `json:"merge_policy"`
	AutoRollback       bool        `json:"auto_rollback"`
	AutoDeployOnBuild  bool        `json:"auto_deploy_on_build"`
	AutoDeployEnv      string      `json:"auto_deploy_environment"`
	RequireHealthCheck bool        `json:"require_health_check"`
}

// DefaultProjectSettings returns the policy used when a project has no explicit
// settings. Requiring a linked work item and passing checks keeps delivery
// traceable without forcing every small repository to use approvals.
func DefaultProjectSettings() ProjectSettings {
	return ProjectSettings{
		MergePolicy: MergePolicy{
			RequireLinkedWorkItem: true,
			RequireApprovedReview: false,
			RequirePassingChecks:  true,
		},
		AutoRollback:       false,
		AutoDeployOnBuild:  false,
		AutoDeployEnv:      "staging",
		RequireHealthCheck: false,
	}
}

// Project is the workspace-level project view.
type Project struct {
	ID            uuid.UUID       `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	KeyPrefix     string          `json:"key_prefix"`
	Status        string          `json:"status"`
	OwnerID       uuid.UUID       `json:"owner_id"`
	RepoName      string          `json:"repo_name"`
	DefaultBranch string          `json:"default_branch"`
	AppName       string          `json:"app_name"`
	Settings      ProjectSettings `json:"settings"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// Member is a project member with a display name where available.
type Member struct {
	UserID   uuid.UUID `json:"user_id"`
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Role     string    `json:"role"`
}

// PRD is a versioned product requirements document.
type PRD struct {
	ID         uuid.UUID  `json:"id"`
	ProjectID  uuid.UUID  `json:"project_id"`
	Title      string     `json:"title"`
	Version    string     `json:"version"`
	Status     string     `json:"status"`
	Summary    string     `json:"summary"`
	Content    string     `json:"content"`
	OwnerID    *uuid.UUID `json:"owner_id"`
	ApprovedBy *uuid.UUID `json:"approved_by"`
	ApprovedAt *time.Time `json:"approved_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Revision   int64      `json:"revision"`
}

// Epic groups features under a product outcome.
type Epic struct {
	ID              uuid.UUID  `json:"id"`
	ProjectID       uuid.UUID  `json:"project_id"`
	PRDID           *uuid.UUID `json:"prd_id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	Priority        string     `json:"priority"`
	OwnerID         *uuid.UUID `json:"owner_id"`
	TargetReleaseID *uuid.UUID `json:"target_release_id"`
	StartDate       *time.Time `json:"start_date"`
	DueDate         *time.Time `json:"due_date"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Revision        int64      `json:"revision"`
}

// Feature is a user-facing capability under an epic.
type Feature struct {
	ID                 uuid.UUID  `json:"id"`
	ProjectID          uuid.UUID  `json:"project_id"`
	EpicID             uuid.UUID  `json:"epic_id"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	AcceptanceCriteria string     `json:"acceptance_criteria"`
	Status             string     `json:"status"`
	Priority           string     `json:"priority"`
	OwnerID            *uuid.UUID `json:"owner_id"`
	EstimatePoints     *float64   `json:"estimate_points"`
	TargetReleaseID    *uuid.UUID `json:"target_release_id"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	Revision           int64      `json:"revision"`
}

// WorkItem is a story, task, subtask, bug, spike or chore.
type WorkItem struct {
	ID                 uuid.UUID  `json:"id"`
	ProjectID          uuid.UUID  `json:"project_id"`
	ItemNumber         int64      `json:"item_number"`
	Key                string     `json:"key"`
	FeatureID          *uuid.UUID `json:"feature_id"`
	ParentID           *uuid.UUID `json:"parent_id"`
	Type               string     `json:"type"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	AcceptanceCriteria string     `json:"acceptance_criteria"`
	Status             string     `json:"status"`
	Priority           string     `json:"priority"`
	Severity           *string    `json:"severity"`
	AssigneeID         *uuid.UUID `json:"assignee_id"`
	ReporterID         *uuid.UUID `json:"reporter_id"`
	EstimatePoints     *float64   `json:"estimate_points"`
	SprintID           *uuid.UUID `json:"sprint_id"`
	ReleaseID          *uuid.UUID `json:"release_id"`
	EnvironmentID      *uuid.UUID `json:"environment_id"`
	DueDate            *time.Time `json:"due_date"`
	ReproductionSteps  string     `json:"reproduction_steps"`
	ExpectedResult     string     `json:"expected_result"`
	ActualResult       string     `json:"actual_result"`
	Labels             []string   `json:"labels"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ClosedAt           *time.Time `json:"closed_at"`
	Revision           int64      `json:"revision"`
}

// Sprint is a time-boxed planning interval.
type Sprint struct {
	ID             uuid.UUID  `json:"id"`
	ProjectID      uuid.UUID  `json:"project_id"`
	Name           string     `json:"name"`
	Goal           string     `json:"goal"`
	Status         string     `json:"status"`
	StartDate      *time.Time `json:"start_date"`
	EndDate        *time.Time `json:"end_date"`
	Capacity       *float64   `json:"capacity"`
	ActivatedAt    *time.Time `json:"activated_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	CompletedBy    *uuid.UUID `json:"completed_by"`
	ClosePolicy    string     `json:"close_policy"`
	CarryOverCount int        `json:"carry_over_count"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Revision       int64      `json:"revision"`
}

// Release is a shippable version.
type Release struct {
	ID              uuid.UUID  `json:"id"`
	ProjectID       uuid.UUID  `json:"project_id"`
	Name            string     `json:"name"`
	Version         string     `json:"version"`
	Status          string     `json:"status"`
	PlannedAt       *time.Time `json:"planned_at"`
	ReleasedAt      *time.Time `json:"released_at"`
	MilestoneAt     *time.Time `json:"milestone_at"`
	FrozenAt        *time.Time `json:"frozen_at"`
	FrozenBy        *uuid.UUID `json:"frozen_by"`
	FreezeReason    string     `json:"freeze_reason"`
	SignOffEvidence string     `json:"sign_off_evidence"`
	SignedOffAt     *time.Time `json:"signed_off_at"`
	SignedOffBy     *uuid.UUID `json:"signed_off_by"`
	Notes           string     `json:"notes"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Revision        int64      `json:"revision"`
}

// Environment is a deployment target.
type Environment struct {
	ID               uuid.UUID `json:"id"`
	ProjectID        uuid.UUID `json:"project_id"`
	Name             string    `json:"name"`
	Type             string    `json:"type"`
	URL              string    `json:"url"`
	HealthCheckURL   string    `json:"health_check_url"`
	DeployStrategy   string    `json:"deploy_strategy"`
	RequiresApproval bool      `json:"requires_approval"`
	Position         int       `json:"position"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// TestCase is a reusable test definition.
type TestCase struct {
	ID            uuid.UUID  `json:"id"`
	ProjectID     uuid.UUID  `json:"project_id"`
	FeatureID     *uuid.UUID `json:"feature_id"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Preconditions string     `json:"preconditions"`
	Type          string     `json:"type"`
	Priority      string     `json:"priority"`
	Status        string     `json:"status"`
	AutomationRef string     `json:"automation_ref"`
	OwnerID       *uuid.UUID `json:"owner_id"`
	Tags          []string   `json:"tags"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Revision      int64      `json:"revision"`
	Steps         []TestStep `json:"steps"`
}

// TestStep is one ordered action in a test case.
type TestStep struct {
	ID             uuid.UUID `json:"id"`
	TestCaseID     uuid.UUID `json:"test_case_id"`
	Position       int       `json:"position"`
	Action         string    `json:"action"`
	ExpectedResult string    `json:"expected_result"`
	TestData       string    `json:"test_data"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TestPlan groups suites and test cases for a test cycle.
type TestPlan struct {
	ID          uuid.UUID  `json:"id"`
	ProjectID   uuid.UUID  `json:"project_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	OwnerID     *uuid.UUID `json:"owner_id"`
	StartDate   *time.Time `json:"start_date"`
	EndDate     *time.Time `json:"end_date"`
	CreatedBy   *uuid.UUID `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Revision    int64      `json:"revision"`
}

// TestSuite is an ordered group of test cases inside a plan.
type TestSuite struct {
	ID          uuid.UUID  `json:"id"`
	PlanID      uuid.UUID  `json:"plan_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	AssigneeID  *uuid.UUID `json:"assignee_id"`
	Position    int        `json:"position"`
	ArchivedAt  *time.Time `json:"archived_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Revision    int64      `json:"revision"`
}

// TestSuiteCase links a test case to a suite in a stable order.
type TestSuiteCase struct {
	SuiteID    uuid.UUID  `json:"suite_id"`
	TestCaseID uuid.UUID  `json:"test_case_id"`
	Position   int        `json:"position"`
	AddedBy    *uuid.UUID `json:"added_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TestRun is an execution of test cases against a build/environment.
type TestRun struct {
	ID            uuid.UUID              `json:"id"`
	ProjectID     uuid.UUID              `json:"project_id"`
	ReleaseID     *uuid.UUID             `json:"release_id"`
	SprintID      *uuid.UUID             `json:"sprint_id"`
	EnvironmentID *uuid.UUID             `json:"environment_id"`
	BuildID       *uuid.UUID             `json:"build_id"`
	PlanID        *uuid.UUID             `json:"plan_id"`
	SuiteID       *uuid.UUID             `json:"suite_id"`
	PlanName      string                 `json:"plan_name"`
	SuiteName     string                 `json:"suite_name"`
	Name          string                 `json:"name"`
	Status        string                 `json:"status"`
	ExecutedBy    *uuid.UUID             `json:"executed_by"`
	StartedAt     *time.Time             `json:"started_at"`
	CompletedAt   *time.Time             `json:"completed_at"`
	ImportSource  string                 `json:"import_source"`
	ImportRef     string                 `json:"import_ref"`
	Metadata      map[string]interface{} `json:"metadata"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
	Revision      int64                  `json:"revision"`
	Results       []TestResult           `json:"results"`
}

// TestResult is the outcome of one test case in a run.
type TestResult struct {
	ID            uuid.UUID        `json:"id"`
	TestRunID     uuid.UUID        `json:"test_run_id"`
	TestCaseID    uuid.UUID        `json:"test_case_id"`
	CaseRevision  int64            `json:"case_revision"`
	Status        string           `json:"status"`
	ExecutedBy    *uuid.UUID       `json:"executed_by"`
	DurationMS    *int             `json:"duration_ms"`
	ActualResult  string           `json:"actual_result"`
	BugWorkItemID *uuid.UUID       `json:"bug_work_item_id"`
	StartedAt     *time.Time       `json:"started_at"`
	CompletedAt   *time.Time       `json:"completed_at"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	StepResults   []TestStepResult `json:"step_results"`
}

// TestStepResult is the outcome of one step in a result.
type TestStepResult struct {
	ID            uuid.UUID  `json:"id"`
	TestResultID  uuid.UUID  `json:"test_result_id"`
	TestStepID    uuid.UUID  `json:"test_step_id"`
	Status        string     `json:"status"`
	ActualResult  string     `json:"actual_result"`
	EvidenceURL   string     `json:"evidence_url"`
	BugWorkItemID *uuid.UUID `json:"bug_work_item_id"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// GitRepository is a linked source repository.
type GitRepository struct {
	ID            uuid.UUID `json:"id"`
	ProjectID     uuid.UUID `json:"project_id"`
	Provider      string    `json:"provider"`
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	DefaultBranch string    `json:"default_branch"`
	ExternalID    string    `json:"external_id"`
	WebhookToken  string    `json:"webhook_token"`
	WebhookURL    string    `json:"webhook_url"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// GitBranch is a branch linked to a work item.
type GitBranch struct {
	ID            uuid.UUID  `json:"id"`
	RepositoryID  uuid.UUID  `json:"repository_id"`
	WorkItemID    *uuid.UUID `json:"work_item_id"`
	Name          string     `json:"name"`
	HeadCommitSHA string     `json:"head_commit_sha"`
	Status        string     `json:"status"`
	CreatedBy     *uuid.UUID `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	MergedAt      *time.Time `json:"merged_at"`
}

// GitCommit is a commit linked to a repository.
type GitCommit struct {
	ID           uuid.UUID `json:"id"`
	RepositoryID uuid.UUID `json:"repository_id"`
	SHA          string    `json:"sha"`
	BranchName   string    `json:"branch_name"`
	Message      string    `json:"message"`
	AuthorName   string    `json:"author_name"`
	AuthorEmail  string    `json:"author_email"`
	CommittedAt  time.Time `json:"committed_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// PullRequest is a reviewable code change.
type PullRequest struct {
	ID             uuid.UUID  `json:"id"`
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
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	MergedAt       *time.Time `json:"merged_at"`
}

// Build is a CI execution.
type Build struct {
	ID           uuid.UUID              `json:"id"`
	ProjectID    uuid.UUID              `json:"project_id"`
	RepositoryID *uuid.UUID             `json:"repository_id"`
	CommitSHA    string                 `json:"commit_sha"`
	Branch       string                 `json:"branch"`
	Provider     string                 `json:"provider"`
	ExternalID   string                 `json:"external_id"`
	Number       string                 `json:"number"`
	Status       string                 `json:"status"`
	LogsURL      string                 `json:"logs_url"`
	TriggeredBy  *uuid.UUID             `json:"triggered_by"`
	Metadata     map[string]interface{} `json:"metadata"`
	StartedAt    *time.Time             `json:"started_at"`
	CompletedAt  *time.Time             `json:"completed_at"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

// Deployment is a CD execution against an environment.
type Deployment struct {
	ID                   uuid.UUID              `json:"id"`
	ProjectID            uuid.UUID              `json:"project_id"`
	EnvironmentID        uuid.UUID              `json:"environment_id"`
	BuildID              *uuid.UUID             `json:"build_id"`
	ReleaseID            *uuid.UUID             `json:"release_id"`
	Status               string                 `json:"status"`
	HealthStatus         string                 `json:"health_status"`
	Version              string                 `json:"version"`
	ArgoApp              string                 `json:"argo_app"`
	ArgoRevision         string                 `json:"argo_revision"`
	Metadata             map[string]interface{} `json:"metadata"`
	DeployedBy           *uuid.UUID             `json:"deployed_by"`
	PreviousDeploymentID *uuid.UUID             `json:"previous_deployment_id"`
	RollbackOfID         *uuid.UUID             `json:"rollback_of_id"`
	StartedAt            *time.Time             `json:"started_at"`
	CompletedAt          *time.Time             `json:"completed_at"`
	CreatedAt            time.Time              `json:"created_at"`
	UpdatedAt            time.Time              `json:"updated_at"`
}

// HealthCheck is a point-in-time health observation for a deployment.
type HealthCheck struct {
	ID           uuid.UUID              `json:"id"`
	DeploymentID uuid.UUID              `json:"deployment_id"`
	URL          string                 `json:"url"`
	Status       string                 `json:"status"`
	HTTPStatus   *int                   `json:"http_status"`
	LatencyMS    *int                   `json:"latency_ms"`
	Details      map[string]interface{} `json:"details"`
	CheckedAt    time.Time              `json:"checked_at"`
	CreatedAt    time.Time              `json:"created_at"`
}

// ActivityEvent is the append-only audit timeline entry.
type ActivityEvent struct {
	ID         uuid.UUID              `json:"id"`
	ProjectID  uuid.UUID              `json:"project_id"`
	ActorID    *uuid.UUID             `json:"actor_id"`
	EntityType string                 `json:"entity_type"`
	EntityID   *uuid.UUID             `json:"entity_id"`
	Action     string                 `json:"action"`
	Summary    string                 `json:"summary"`
	Metadata   map[string]interface{} `json:"metadata"`
	CreatedAt  time.Time              `json:"created_at"`
}

// ArtifactLink is a typed many-to-many traceability link.
type ArtifactLink struct {
	ID         uuid.UUID  `json:"id"`
	ProjectID  uuid.UUID  `json:"project_id"`
	SourceType string     `json:"source_type"`
	SourceID   uuid.UUID  `json:"source_id"`
	TargetType string     `json:"target_type"`
	TargetID   uuid.UUID  `json:"target_id"`
	LinkType   string     `json:"link_type"`
	CreatedBy  *uuid.UUID `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Mention is a project member referenced from a comment body.
type Mention struct {
	UserID      uuid.UUID `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
}

// Comment is one sanitized, plain-text discussion entry on an artifact.
type Comment struct {
	ID         uuid.UUID  `json:"id"`
	ProjectID  uuid.UUID  `json:"project_id"`
	EntityType string     `json:"entity_type"`
	EntityID   uuid.UUID  `json:"entity_id"`
	AuthorID   *uuid.UUID `json:"author_id"`
	AuthorName string     `json:"author_name"`
	Body       string     `json:"body"`
	Mentions   []Mention  `json:"mentions"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	EditedAt   *time.Time `json:"edited_at"`
}

// Attachment is uploaded evidence. ScanStatus must be clean before download.
type Attachment struct {
	ID             uuid.UUID  `json:"id"`
	ProjectID      uuid.UUID  `json:"project_id"`
	EntityType     string     `json:"entity_type"`
	EntityID       uuid.UUID  `json:"entity_id"`
	Filename       string     `json:"filename"`
	MIMEType       string     `json:"mime_type"`
	SizeBytes      int64      `json:"size_bytes"`
	ChecksumSHA256 string     `json:"checksum_sha256"`
	ScanStatus     string     `json:"scan_status"`
	ScanEngine     string     `json:"scan_engine"`
	ScanDetail     string     `json:"scan_detail,omitempty"`
	UploadedBy     *uuid.UUID `json:"uploaded_by"`
	UploaderName   string     `json:"uploader_name"`
	CreatedAt      time.Time  `json:"created_at"`
	DownloadURL    string     `json:"download_url"`
}

// Watcher is a project member subscribed to a work item.
type Watcher struct {
	UserID      uuid.UUID `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// LinkedArtifact is one bidirectional link rendered from the perspective of
// the artifact being viewed.
type LinkedArtifact struct {
	LinkID       uuid.UUID `json:"link_id"`
	LinkType     string    `json:"link_type"`
	Direction    string    `json:"direction"`
	ArtifactType string    `json:"artifact_type"`
	ArtifactID   uuid.UUID `json:"artifact_id"`
	ArtifactKey  string    `json:"artifact_key,omitempty"`
	Title        string    `json:"title,omitempty"`
	Status       string    `json:"status,omitempty"`
}

// WorkItemDetail is the drawer aggregate: the item, its children, and the
// collaboration surfaces that are too large for the workspace snapshot.
type WorkItemDetail struct {
	WorkItem
	Children        []WorkItem       `json:"children"`
	Comments        []Comment        `json:"comments"`
	Attachments     []Attachment     `json:"attachments"`
	Watchers        []Watcher        `json:"watchers"`
	Watching        bool             `json:"watching"`
	Links           []LinkedArtifact `json:"links"`
	Activity        []ActivityEvent  `json:"activity"`
	Branches        []GitBranch      `json:"branches"`
	PullRequests    []PullRequest    `json:"pull_requests"`
	ChildCount      int              `json:"child_count"`
	CommentCount    int              `json:"comment_count"`
	AttachmentCount int              `json:"attachment_count"`
	WatcherCount    int              `json:"watcher_count"`
}

// TestTrendPoint is one completed test run's pass/fail summary.
type TestTrendPoint struct {
	RunID    uuid.UUID `json:"run_id"`
	Name     string    `json:"name"`
	Date     string    `json:"date"`
	Total    int       `json:"total"`
	Passed   int       `json:"passed"`
	Failed   int       `json:"failed"`
	PassRate float64   `json:"pass_rate"`
}

// Metrics are derived workspace health indicators.
type Metrics struct {
	PRDCount            int              `json:"prd_count"`
	EpicCount           int              `json:"epic_count"`
	FeatureCount        int              `json:"feature_count"`
	WorkItemCount       int              `json:"work_item_count"`
	OpenBugCount        int              `json:"open_bug_count"`
	TestCaseCount       int              `json:"test_case_count"`
	TestRunCount        int              `json:"test_run_count"`
	PassRate            float64          `json:"pass_rate"`
	CoveragePercent     float64          `json:"coverage_percent"`
	DeploymentCount     int              `json:"deployment_count"`
	HealthyDeployments  int              `json:"healthy_deployments"`
	DegradedDeployments int              `json:"degraded_deployments"`
	UnlinkedFeatures    int              `json:"unlinked_features"`
	UntestedFeatures    int              `json:"untested_features"`
	FlakyTestCount      int              `json:"flaky_test_count"`
	CycleTimeHours      float64          `json:"cycle_time_hours"`
	DeliveryConfidence  float64          `json:"delivery_confidence"`
	TestTrend           []TestTrendPoint `json:"test_trend"`
}

// Workspace is the aggregated read model for the new UI.
type Workspace struct {
	Project      Project          `json:"project"`
	Members      []Member         `json:"members"`
	PRDs         []PRD            `json:"prds"`
	Epics        []Epic           `json:"epics"`
	Features     []Feature        `json:"features"`
	WorkItems    []WorkItem       `json:"work_items"`
	Sprints      []Sprint         `json:"sprints"`
	Releases     []Release        `json:"releases"`
	Environments []Environment    `json:"environments"`
	TestCases    []TestCase       `json:"test_cases"`
	TestPlans    []TestPlan       `json:"test_plans"`
	TestSuites   []TestSuite      `json:"test_suites"`
	SuiteCases   []TestSuiteCase  `json:"test_suite_cases"`
	TestRuns     []TestRun        `json:"test_runs"`
	TestResults  []TestResult     `json:"test_results"`
	StepResults  []TestStepResult `json:"test_step_results"`
	Repositories []GitRepository  `json:"repositories"`
	Branches     []GitBranch      `json:"branches"`
	Commits      []GitCommit      `json:"commits"`
	PullRequests []PullRequest    `json:"pull_requests"`
	Builds       []Build          `json:"builds"`
	Deployments  []Deployment     `json:"deployments"`
	HealthChecks []HealthCheck    `json:"health_checks"`
	Links        []ArtifactLink   `json:"links"`
	Activity     []ActivityEvent  `json:"activity"`
	Metrics      Metrics          `json:"metrics"`
	Revision     int64            `json:"revision"`
}
