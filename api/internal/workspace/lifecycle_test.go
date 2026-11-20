package workspace

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func mustUUID(t *testing.T, raw string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(raw)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", raw, err)
	}
	return id
}

func TestSprintUpdateTransitionAllowsMetadataAndCancellation(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"planned", "planned", true},
		{"active", "active", true},
		{"planned", "cancelled", true},
		{"active", "cancelled", true},
		{"planned", "active", false},
		{"active", "completed", false},
		{"completed", "cancelled", false},
		{"cancelled", "planned", false},
	}
	for _, tc := range cases {
		if got := validSprintUpdateTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("validSprintUpdateTransition(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestReleaseTransitionRequiresLifecycleEndpoints(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"planned", "planned", true},
		{"in_progress", "in_progress", true},
		{"planned", "in_progress", true},
		{"planned", "cancelled", true},
		{"in_progress", "released", true},
		{"in_progress", "cancelled", true},
		{"planned", "released", false},
		{"released", "in_progress", false},
		{"cancelled", "planned", false},
		{"in_progress", "planned", false},
	}
	for _, tc := range cases {
		if got := validReleaseTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("validReleaseTransition(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestSprintPolicy(t *testing.T) {
	cases := map[string]struct {
		want string
		ok   bool
	}{
		"":              {"backlog", true},
		"backlog":       {"backlog", true},
		"next_sprint":   {"next_sprint", true},
		" next_sprint ": {"next_sprint", true},
		"archive":       {"", false},
	}
	for input, want := range cases {
		got, ok := sprintPolicy(input)
		if got != want.want || ok != want.ok {
			t.Errorf("sprintPolicy(%q) = (%q, %v), want (%q, %v)", input, got, ok, want.want, want.ok)
		}
	}
}

func TestSprintDayCountsInTimeZone(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)

	total, elapsed, remaining := sprintDayCounts(&start, &end, time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC), time.UTC)
	if total != 10 || elapsed != 5 || remaining != 5 {
		t.Fatalf("UTC counts = (%d, %d, %d), want (10, 5, 5)", total, elapsed, remaining)
	}

	// 20:00 UTC is the next calendar day in Tokyo, so one more day elapsed.
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load Asia/Tokyo: %v", err)
	}
	total, elapsed, remaining = sprintDayCounts(&start, &end, time.Date(2026, 1, 5, 20, 0, 0, 0, time.UTC), tokyo)
	if total != 10 || elapsed != 6 || remaining != 4 {
		t.Fatalf("Tokyo counts = (%d, %d, %d), want (10, 6, 4)", total, elapsed, remaining)
	}

	// Before and after the sprint window clamp elapsed days.
	if _, elapsed, _ = sprintDayCounts(&start, &end, time.Date(2025, 12, 20, 0, 0, 0, 0, time.UTC), time.UTC); elapsed != 0 {
		t.Fatalf("elapsed before start = %d, want 0", elapsed)
	}
	if _, elapsed, remaining = sprintDayCounts(&start, &end, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), time.UTC); elapsed != 10 || remaining != 0 {
		t.Fatalf("after end = (%d, %d), want (10, 0)", elapsed, remaining)
	}

	if total, elapsed, remaining = sprintDayCounts(nil, &end, time.Now(), time.UTC); total != 0 || elapsed != 0 || remaining != 0 {
		t.Fatalf("missing dates = (%d, %d, %d), want zeros", total, elapsed, remaining)
	}
}

func TestReleaseScopeTable(t *testing.T) {
	cases := map[string]string{
		"work_item": "work_items",
		"feature":   "features",
		"epic":      "epics",
	}
	for entity, table := range cases {
		got, ok := releaseScopeTable(entity)
		if !ok || got != table {
			t.Errorf("releaseScopeTable(%q) = (%q, %v), want (%q, true)", entity, got, ok, table)
		}
	}
	if _, ok := releaseScopeTable("sprint"); ok {
		t.Error("releaseScopeTable accepted an unsupported entity type")
	}
}

func TestSameRelease(t *testing.T) {
	a := mustUUID(t, "b4f0c3de-6a2f-4e6f-9f0a-15a4f4f2a9d1")
	b := mustUUID(t, "6f5f0f5a-95d5-4aa2-8d8b-5a0fcf1e6e21")
	if !sameRelease(nil, nil) || !sameRelease(&a, &a) {
		t.Error("sameRelease should treat identical pointers as equal")
	}
	if sameRelease(nil, &a) || sameRelease(&a, nil) || sameRelease(&a, &b) {
		t.Error("sameRelease should treat different or missing releases as different")
	}
}

func TestTerminalWorkItemStatus(t *testing.T) {
	for _, status := range []string{StatusDone, StatusClosed, StatusVerified, StatusCancelled} {
		if !isTerminalWorkItemStatus(status) {
			t.Errorf("%q should be terminal", status)
		}
	}
	for _, status := range []string{"backlog", "ready", "in_progress", "in_review", "in_qa", "blocked"} {
		if isTerminalWorkItemStatus(status) {
			t.Errorf("%q should not be terminal", status)
		}
	}
}

func TestCompletedWorkItemStatusExcludesCancelled(t *testing.T) {
	for _, status := range []string{StatusDone, StatusClosed, StatusVerified} {
		if !isCompletedWorkItemStatus(status) {
			t.Errorf("%q should count as completed", status)
		}
	}
	for _, status := range []string{StatusCancelled, "backlog", "in_progress", "blocked"} {
		if isCompletedWorkItemStatus(status) {
			t.Errorf("%q should not count as completed", status)
		}
	}
}
