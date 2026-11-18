package workspace

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReleaseReadinessWarningsDoNotBlock(t *testing.T) {
	releaseID := uuid.New()
	featureID := uuid.New()
	caseID := uuid.New()
	bugID := uuid.New()

	release := Release{ID: releaseID, Name: "Release", Version: "1.0.0", Status: "in_progress"}
	feature := Feature{ID: featureID, Title: "Feature", TargetReleaseID: &releaseID}
	testCase := TestCase{ID: caseID, FeatureID: &featureID, Title: "Case"}
	bug := WorkItem{
		ID: bugID, Type: TypeBug, Status: StatusNew, Priority: PriorityMedium,
		Title: "Non-critical defect", FeatureID: &featureID,
	}
	w := Workspace{Features: []Feature{feature}}
	trace := map[uuid.UUID]traceabilityFeature{
		featureID: {FeatureID: featureID, BranchCount: 1, PRCount: 1, BuildCount: 1, HealthStatus: "healthy"},
	}
	latest := map[uuid.UUID]TestResult{caseID: {TestCaseID: caseID, Status: TestPassed}}
	cases := map[uuid.UUID][]TestCase{featureID: {testCase}}
	bugs := map[uuid.UUID][]WorkItem{featureID: {bug}}

	row := computeReleaseReadiness(w, release, trace, latest, cases, bugs)
	if !row.Ready {
		t.Fatalf("warning gate should not block release readiness: %+v", row)
	}
	if row.Score != 66 {
		t.Fatalf("score = %d, want 66 (two passing gates out of three)", row.Score)
	}
	var bugGate *reportGate
	for i := range row.Gates {
		if row.Gates[i].Key == "bugs" {
			bugGate = &row.Gates[i]
		}
	}
	if bugGate == nil || bugGate.Status != "warn" {
		t.Fatalf("bug gate = %+v, want warn", bugGate)
	}
}

func TestValidWorkItemStatus(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		status string
		want   bool
	}{
		{"story backlog", TypeStory, StatusBacklog, true},
		{"story review", TypeStory, StatusInReview, true},
		{"story cannot be fixed", TypeStory, StatusFixed, false},
		{"bug new", TypeBug, StatusNew, true},
		{"bug fixed", TypeBug, StatusFixed, true},
		{"bug verified", TypeBug, StatusVerified, true},
		{"bug cannot be done", TypeBug, StatusDone, false},
		{"unknown type", "epic", StatusReady, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validWorkItemStatus(tt.kind, tt.status); got != tt.want {
				t.Fatalf("validWorkItemStatus(%q, %q) = %v, want %v", tt.kind, tt.status, got, tt.want)
			}
		})
	}
}

func TestValidStatusTransition(t *testing.T) {
	tests := []struct {
		name string
		kind string
		from string
		to   string
		want bool
	}{
		{"same status is idempotent", TypeStory, StatusReady, StatusReady, true},
		{"story backlog to ready", TypeStory, StatusBacklog, StatusReady, true},
		{"story ready to in progress", TypeStory, StatusReady, StatusInProgress, true},
		{"story in progress to review", TypeStory, StatusInProgress, StatusInReview, true},
		{"story review to done", TypeStory, StatusInReview, StatusDone, true},
		{"story done can reopen", TypeStory, StatusDone, StatusReopened, true},
		{"story done cannot go back to backlog", TypeStory, StatusDone, StatusBacklog, false},
		{"cancelled can return to ready", TypeStory, StatusCancelled, StatusReady, true},
		{"bug new to triaged", TypeBug, StatusNew, StatusTriaged, true},
		{"bug new cannot jump to fixed", TypeBug, StatusNew, StatusFixed, false},
		{"bug in progress to fixed", TypeBug, StatusInProgress, StatusFixed, true},
		{"bug fixed to verified", TypeBug, StatusFixed, StatusVerified, true},
		{"bug verified to closed", TypeBug, StatusVerified, StatusClosed, true},
		{"bug closed can reopen", TypeBug, StatusClosed, StatusReopened, true},
		{"unknown transition", TypeStory, "unknown", StatusReady, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validStatusTransition(tt.kind, tt.from, tt.to); got != tt.want {
				t.Fatalf("validStatusTransition(%q, %q, %q) = %v, want %v", tt.kind, tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func completeWorkspace() Workspace {
	now := time.Now().UTC()
	projectID := uuid.New()
	prdID := uuid.New()
	epicID := uuid.New()
	featureID := uuid.New()
	storyID := uuid.New()
	bugID := uuid.New()
	caseID := uuid.New()
	branchID := uuid.New()
	repositoryID := uuid.New()
	buildID := uuid.New()

	return Workspace{
		Project: Project{ID: projectID, Name: "Atlas Commerce"},
		PRDs:    []PRD{{ID: prdID, ProjectID: projectID, Title: "Checkout v2", Status: "approved"}},
		Epics: []Epic{{
			ID: epicID, ProjectID: projectID, PRDID: &prdID,
			Title: "One-tap checkout", Status: "in_progress",
		}},
		Features: []Feature{{
			ID: featureID, ProjectID: projectID, EpicID: epicID,
			Title: "Saved card payment", Status: "in_progress",
		}},
		WorkItems: []WorkItem{
			{ID: storyID, ProjectID: projectID, FeatureID: &featureID, Type: TypeStory, Title: "Pay with saved card", Status: StatusInReview},
			{ID: bugID, ProjectID: projectID, FeatureID: &featureID, Type: TypeBug, Title: "Saved card payment fails", Status: StatusNew},
		},
		TestCases: []TestCase{{
			ID: caseID, ProjectID: projectID, FeatureID: &featureID,
			Title: "Saved card happy path", Status: "ready",
		}},
		TestResults: []TestResult{{
			ID: uuid.New(), TestCaseID: caseID, Status: TestPassed,
			UpdatedAt: now, CreatedAt: now,
		}},
		Branches: []GitBranch{{
			ID: branchID, RepositoryID: repositoryID, WorkItemID: &storyID,
			Name: "feature/atla-1-saved-card", Status: "merged",
		}},
		PullRequests: []PullRequest{{
			ID: uuid.New(), RepositoryID: repositoryID, WorkItemID: &storyID,
			Number: 1, Title: "Saved card payment", Status: "merged",
			ReviewStatus: "approved", MergeCommitSHA: "merge-sha",
		}},
		Builds: []Build{{
			ID: buildID, ProjectID: projectID, CommitSHA: "merge-sha",
			Branch: "main", Status: "success",
		}},
		Deployments: []Deployment{{
			ID: uuid.New(), ProjectID: projectID, BuildID: &buildID,
			Status: "succeeded", HealthStatus: "healthy", Version: "1.0.0",
		}},
	}
}

func TestBuildTraceabilityCompleteChain(t *testing.T) {
	resp := buildTraceability(completeWorkspace())

	if len(resp.Features) != 1 {
		t.Fatalf("features = %d, want 1", len(resp.Features))
	}
	row := resp.Features[0]
	if row.Stories != 1 || row.Tasks != 0 || row.OpenBugs != 1 {
		t.Fatalf("work item counts = stories:%d tasks:%d bugs:%d", row.Stories, row.Tasks, row.OpenBugs)
	}
	if row.TestCases != 1 || row.LastRunStatus != TestPassed {
		t.Fatalf("test summary = cases:%d status:%q", row.TestCases, row.LastRunStatus)
	}
	if row.BranchCount != 1 || row.PRCount != 1 || row.BuildCount != 1 || row.Deployments != 1 {
		t.Fatalf("delivery counts = branches:%d prs:%d builds:%d deployments:%d",
			row.BranchCount, row.PRCount, row.BuildCount, row.Deployments)
	}
	if row.HealthStatus != "healthy" {
		t.Fatalf("health = %q, want healthy", row.HealthStatus)
	}
	if row.Gaps == nil || len(row.Gaps) != 0 {
		t.Fatalf("feature gaps = %#v, want empty slice", row.Gaps)
	}
	if resp.Gaps == nil || len(resp.Gaps) != 0 {
		t.Fatalf("top-level gaps = %#v, want empty slice", resp.Gaps)
	}
}

func TestBuildTraceabilityReportsGaps(t *testing.T) {
	projectID := uuid.New()
	epicID := uuid.New()
	featureID := uuid.New()
	resp := buildTraceability(Workspace{
		Project:  Project{ID: projectID, Name: "Empty project"},
		Epics:    []Epic{{ID: epicID, ProjectID: projectID, Title: "Empty epic"}},
		Features: []Feature{{ID: featureID, ProjectID: projectID, EpicID: epicID, Title: "Unlinked feature"}},
	})

	if len(resp.Features) != 1 {
		t.Fatalf("features = %d, want 1", len(resp.Features))
	}
	row := resp.Features[0]
	want := []string{
		"No PRD linked through epic",
		"No story",
		"No test case",
		"No branch",
		"No pull request",
		"No healthy deployment",
	}
	if len(row.Gaps) != len(want) {
		t.Fatalf("gaps = %#v, want %d entries", row.Gaps, len(want))
	}
	for i, gap := range want {
		if row.Gaps[i] != gap {
			t.Fatalf("gap[%d] = %q, want %q", i, row.Gaps[i], gap)
		}
	}
	for _, gap := range want {
		found := false
		for _, top := range resp.Gaps {
			if strings.Contains(top, gap) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("top-level gaps missing %q: %#v", gap, resp.Gaps)
		}
	}
}

func TestBuildTraceabilityUsesLatestDeploymentHealth(t *testing.T) {
	w := completeWorkspace()
	projectID := w.Project.ID
	buildID := w.Builds[0].ID
	w.Deployments = []Deployment{
		{ID: uuid.New(), ProjectID: projectID, BuildID: &buildID, Status: "succeeded", HealthStatus: "degraded"},
		{ID: uuid.New(), ProjectID: projectID, BuildID: &buildID, Status: "succeeded", HealthStatus: "healthy"},
	}

	resp := buildTraceability(w)
	row := resp.Features[0]
	if row.HealthStatus != "degraded" {
		t.Fatalf("health = %q, want latest deployment health degraded", row.HealthStatus)
	}
	if len(row.Gaps) != 1 || row.Gaps[0] != "No healthy deployment" {
		t.Fatalf("gaps = %#v, want unhealthy deployment gap", row.Gaps)
	}
}

func TestComputeMetrics(t *testing.T) {
	w := completeWorkspace()
	w.TestResults = append(w.TestResults, TestResult{
		ID: uuid.New(), TestCaseID: w.TestCases[0].ID, Status: TestFailed,
	})
	w.Deployments = append(w.Deployments, Deployment{
		ID: uuid.New(), ProjectID: w.Project.ID, Status: "succeeded", HealthStatus: "degraded",
	})

	metrics := computeMetrics(w)
	if metrics.FeatureCount != 1 || metrics.WorkItemCount != 2 || metrics.OpenBugCount != 1 {
		t.Fatalf("entity metrics = %#v", metrics)
	}
	if metrics.PassRate != 50 {
		t.Fatalf("pass rate = %v, want 50", metrics.PassRate)
	}
	if metrics.CoveragePercent != 100 || metrics.UntestedFeatures != 0 || metrics.UnlinkedFeatures != 0 {
		t.Fatalf("coverage metrics = %#v", metrics)
	}
	if metrics.DeploymentCount != 2 || metrics.HealthyDeployments != 1 || metrics.DegradedDeployments != 1 {
		t.Fatalf("deployment metrics = %#v", metrics)
	}
}
