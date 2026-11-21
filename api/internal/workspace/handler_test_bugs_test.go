package workspace

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func bugContextFixture() (*bugContext, *TestStepResult) {
	runID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	resultID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	caseID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	environmentID := uuid.MustParse("00000000-0000-0000-0000-000000000004")
	releaseID := uuid.MustParse("00000000-0000-0000-0000-000000000005")
	sprintID := uuid.MustParse("00000000-0000-0000-0000-000000000006")
	buildID := uuid.MustParse("00000000-0000-0000-0000-000000000007")
	deploymentID := uuid.MustParse("00000000-0000-0000-0000-000000000008")
	stepID := uuid.MustParse("00000000-0000-0000-0000-000000000009")

	step := TestStepResult{
		ID: stepID, TestResultID: resultID, CaseRevision: 4, Position: 2,
		Action: "Confirm payment", ExpectedResult: "Receipt appears", Status: TestFailed,
		ActualResult: "Spinner hung", EvidenceURL: "/evidence/checkout.png",
	}
	bc := &bugContext{
		RunID: runID, RunName: "Checkout regression", RunStatus: "in_progress",
		PlanName: "Release plan", SuiteName: "Smoke",
		Environment: &environmentID, EnvironmentName: "staging",
		Release: &releaseID, ReleaseName: "2026.11",
		Sprint: &sprintID, SprintName: "Sprint 12",
		Build: &buildID, BuildNumber: "1841", BuildCommit: "0123456789abcdef",
		Deployment: &deploymentID, DeploymentEnvironment: "staging",
		DeploymentStatus: "healthy", DeploymentHealth: "healthy",
		ResultID: resultID, ResultStatus: TestFailed, ResultActual: "Checkout failed",
		CaseID: caseID, CaseTitle: "Checkout", CaseRevision: 4,
		Steps: []TestStepResult{step},
	}
	return bc, &bc.Steps[0]
}

func TestNormalizeBugTitle(t *testing.T) {
	cases := map[string]string{
		"  Defect:   Checkout\tfailed  ": "defect: checkout failed",
		"Already normal":                 "already normal",
		"":                               "",
	}
	for input, want := range cases {
		if got := normalizeBugTitle(input); got != want {
			t.Fatalf("normalizeBugTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAutoBugTitleUsesFailedStep(t *testing.T) {
	bc, step := bugContextFixture()
	if got, want := autoBugTitle(bc, step), "Defect: Checkout — step 2: Confirm payment"; got != want {
		t.Fatalf("autoBugTitle() = %q, want %q", got, want)
	}
	if got, want := autoBugTitle(bc, nil), "Defect: Checkout"; got != want {
		t.Fatalf("autoBugTitle(nil) = %q, want %q", got, want)
	}
	step.Action = "   "
	if got, want := autoBugTitle(bc, step), "Defect: Checkout — step 2"; got != want {
		t.Fatalf("autoBugTitle(blank action) = %q, want %q", got, want)
	}
}

func TestAutoStepReproductionFallsBackToIndex(t *testing.T) {
	steps := []TestStepResult{
		{Position: 0, Action: "Open checkout"},
		{Position: 3, Action: "Pay"},
	}
	want := "1. Open checkout\n3. Pay"
	if got := autoStepReproduction(steps); got != want {
		t.Fatalf("autoStepReproduction() = %q, want %q", got, want)
	}
	if got := autoStepReproduction(nil); got != "" {
		t.Fatalf("autoStepReproduction(nil) = %q, want empty", got)
	}
}

func TestSelectBugStep(t *testing.T) {
	bc, step := bugContextFixture()
	bc.Steps = append(bc.Steps, TestStepResult{ID: uuid.New(), Position: 1, Action: "Open", Status: TestPassed})

	selected, err := selectBugStep(bc, &step.ID)
	if err != nil || selected == nil || selected.ID != step.ID {
		t.Fatalf("selectBugStep(explicit) = %v, %v", selected, err)
	}
	selected, err = selectBugStep(bc, nil)
	if err != nil || selected == nil || selected.ID != step.ID {
		t.Fatalf("selectBugStep(failed) = %v, %v", selected, err)
	}
	unknown := uuid.New()
	if _, err := selectBugStep(bc, &unknown); err != errBugStepNotFound {
		t.Fatalf("selectBugStep(unknown) error = %v, want %v", err, errBugStepNotFound)
	}

	bc.Steps = []TestStepResult{{Status: TestPassed}}
	if selected, err := selectBugStep(bc, nil); err != nil || selected != nil {
		t.Fatalf("selectBugStep(no failure) = %v, %v; want nil, nil", selected, err)
	}
}

func TestComposeBugDescriptionCarriesTraceability(t *testing.T) {
	bc, step := bugContextFixture()
	description := composeBugDescription("Checkout hangs after confirmation.", bc, step)
	for _, want := range []string{
		"Checkout hangs after confirmation.",
		"Test run: Checkout regression (in_progress)",
		"Test case: Checkout (revision 4)",
		"Step: 2 — Confirm payment (failed)",
		"Environment: staging",
		"Release: 2026.11",
		"Sprint: Sprint 12",
		"Build: #1841 (0123456789ab)",
		"Deployment: healthy/healthy on staging",
		"expected: Receipt appears",
		"actual: Spinner hung",
		"evidence: /evidence/checkout.png",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("composeBugDescription() missing %q in:\n%s", want, description)
		}
	}
	if !strings.HasPrefix(description, "Checkout hangs after confirmation.\n\nTest context") {
		t.Fatalf("composeBugDescription() did not keep the reporter text first:\n%s", description)
	}
}

func TestComposeBugDescriptionWithoutBase(t *testing.T) {
	bc, _ := bugContextFixture()
	bc.Build = nil
	bc.Deployment = nil
	bc.Environment = nil
	bc.EnvironmentName = ""
	description := composeBugDescription("   ", bc, nil)
	if strings.HasPrefix(description, "\n") || !strings.HasPrefix(description, "Test context") {
		t.Fatalf("composeBugDescription(empty base) = %q", description)
	}
	if strings.Contains(description, "Build:") || strings.Contains(description, "Deployment:") {
		t.Fatalf("composeBugDescription() included absent artifacts:\n%s", description)
	}
}

func TestBugLinkTargets(t *testing.T) {
	bc, _ := bugContextFixture()
	bugID := uuid.New()
	links := bugLinkTargets(bc, bugID)
	if len(links) != 5 {
		t.Fatalf("bugLinkTargets() returned %d links, want 5", len(links))
	}
	types := map[string]bool{}
	for _, link := range links {
		types[link.SourceType] = true
		if link.TargetID != bugID || link.TargetType != "work_item" || link.LinkType != "found_bug" {
			t.Fatalf("unexpected link: %+v", link)
		}
	}
	for _, want := range []string{"test_result", "test_run", "test_case", "build", "deployment"} {
		if !types[want] {
			t.Fatalf("bugLinkTargets() missing source %q", want)
		}
	}

	bc.Build = nil
	bc.Deployment = nil
	if got := len(bugLinkTargets(bc, bugID)); got != 3 {
		t.Fatalf("bugLinkTargets(no build/deployment) returned %d links, want 3", got)
	}
}

func TestBulkSkipReason(t *testing.T) {
	bc, _ := bugContextFixture()
	bc.ResultStatus = TestFailed
	if got := bulkSkipReason(bc); got != "" {
		t.Fatalf("bulkSkipReason(failed) = %q, want empty", got)
	}

	linked := uuid.New()
	bc.ResultBug = &linked
	if got := bulkSkipReason(bc); got != "already_linked" {
		t.Fatalf("bulkSkipReason(linked result) = %q, want already_linked", got)
	}
	bc.ResultBug = nil
	bc.Steps[0].BugWorkItemID = &linked
	if got := bulkSkipReason(bc); got != "already_linked" {
		t.Fatalf("bulkSkipReason(linked step) = %q, want already_linked", got)
	}

	bc.Steps[0].BugWorkItemID = nil
	bc.Steps[0].Status = TestPassed
	bc.ResultStatus = TestPassed
	if got := bulkSkipReason(bc); got != "not_failed" {
		t.Fatalf("bulkSkipReason(passed) = %q, want not_failed", got)
	}

	bc.ResultStatus = TestNotRun
	bc.Steps[0].Status = TestFailed
	if got := bulkSkipReason(bc); got != "" {
		t.Fatalf("bulkSkipReason(failed step) = %q, want empty", got)
	}
}

func TestSummarizeBulkBugs(t *testing.T) {
	runID := uuid.New()
	summary := summarizeBulkBugs(runID, []bulkBugItem{
		{Status: "created"}, {Status: "created"}, {Status: "duplicate"},
		{Status: "skipped"}, {Status: "failed"}, {Status: "anything-else"},
	})
	if summary.RunID != runID || summary.Requested != 6 || summary.Created != 2 ||
		summary.Duplicates != 1 || summary.Skipped != 1 || summary.Failed != 2 {
		t.Fatalf("summarizeBulkBugs() = %+v", summary)
	}
	empty := summarizeBulkBugs(runID, nil)
	if empty.Items == nil || empty.Requested != 0 {
		t.Fatalf("summarizeBulkBugs(nil) = %+v", empty)
	}
}

func TestUniqueIDs(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	got := uniqueIDs([]uuid.UUID{first, second, first})
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("uniqueIDs() = %v", got)
	}
}

func TestValidBugSeverity(t *testing.T) {
	for _, severity := range []string{"blocker", "critical", "major", "minor", "trivial"} {
		if !validBugSeverity(severity) {
			t.Fatalf("validBugSeverity(%q) = false", severity)
		}
	}
	for _, severity := range []string{"", "severe", "high"} {
		if validBugSeverity(severity) {
			t.Fatalf("validBugSeverity(%q) = true", severity)
		}
	}
}
