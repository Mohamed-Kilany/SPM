package workspace

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidTestPlanStatus(t *testing.T) {
	for _, status := range []string{"draft", "active", "archived"} {
		if !validTestPlanStatus(status) {
			t.Errorf("validTestPlanStatus(%q) = false, want true", status)
		}
	}
	for _, status := range []string{"", "planned", "completed", "ACTIVE"} {
		if validTestPlanStatus(status) {
			t.Errorf("validTestPlanStatus(%q) = true, want false", status)
		}
	}
}

func TestValidTestPlanTransition(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"draft", "draft", true},
		{"draft", "active", true},
		{"draft", "archived", true},
		{"active", "draft", true},
		{"active", "active", true},
		{"active", "archived", true},
		{"archived", "archived", true},
		{"archived", "draft", false},
		{"archived", "active", false},
	}
	for _, tc := range cases {
		if got := validTestPlanTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("validTestPlanTransition(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestParseOptionalDate(t *testing.T) {
	if value, err := parseOptionalDate(nil); err != nil || value != nil {
		t.Fatalf("nil date = (%v, %v), want (nil, nil)", value, err)
	}
	blank := "  "
	if value, err := parseOptionalDate(&blank); err != nil || value != nil {
		t.Fatalf("blank date = (%v, %v), want (nil, nil)", value, err)
	}
	date := "2026-11-21"
	value, err := parseOptionalDate(&date)
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}
	if value == nil || !value.Equal(time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("parsed date = %v, want 2026-11-21", value)
	}
	bad := "21/11/2026"
	if _, err := parseOptionalDate(&bad); err == nil {
		t.Fatal("expected an error for a non-ISO date")
	}
}

func TestDedupeCaseIDsPreservesOrder(t *testing.T) {
	first, second, third := uuid.New(), uuid.New(), uuid.New()
	got := dedupeCaseIDs([]uuid.UUID{first, second, first, third, second})
	want := []uuid.UUID{first, second, third}
	if len(got) != len(want) {
		t.Fatalf("len(dedupe) = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("dedupe[%d] = %s, want %s", index, got[index], want[index])
		}
	}
}
