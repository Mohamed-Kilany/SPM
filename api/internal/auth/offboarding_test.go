package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeOffboardingReason(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "rejects empty", input: "", wantErr: true},
		{name: "rejects whitespace only", input: "   ", wantErr: true},
		{name: "rejects two characters", input: "ab", wantErr: true},
		{name: "accepts three characters", input: "why", want: "why"},
		{name: "trims surrounding whitespace", input: "  left the company  ", want: "left the company"},
		{name: "accepts the maximum", input: strings.Repeat("a", 500), want: strings.Repeat("a", 500)},
		{name: "rejects longer than the maximum", input: strings.Repeat("a", 501), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeOffboardingReason(tc.input)
			if tc.wantErr {
				if !errors.Is(err, errOffboardingReason) {
					t.Fatalf("err = %v, want errOffboardingReason", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseOffboardingSuccessor(t *testing.T) {
	id := uuid.New()
	got, err := parseOffboardingSuccessor("  " + id.String() + "  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != id {
		t.Fatalf("successor = %s, want %s", got, id)
	}

	if got, err := parseOffboardingSuccessor(""); err != nil || got != uuid.Nil {
		t.Fatalf("empty successor = %s, %v; want nil UUID, nil error", got, err)
	}

	if _, err := parseOffboardingSuccessor("not-a-uuid"); !errors.Is(err, errOffboardingID) {
		t.Fatalf("err = %v, want errOffboardingID", err)
	}
}

func TestParseProjectOwnerOverrides(t *testing.T) {
	projectA, projectB := uuid.New(), uuid.New()
	userA, userB := uuid.New(), uuid.New()

	overrides, err := parseProjectOwnerOverrides(map[string]string{
		projectA.String():             userA.String(),
		" " + projectB.String() + " ": " " + userB.String(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(overrides) != 2 {
		t.Fatalf("overrides = %d, want 2", len(overrides))
	}
	if overrides[projectA] != userA {
		t.Fatalf("project A successor = %s, want %s", overrides[projectA], userA)
	}
	if overrides[projectB] != userB {
		t.Fatalf("project B successor = %s, want %s", overrides[projectB], userB)
	}

	if _, err := parseProjectOwnerOverrides(map[string]string{"bad": userA.String()}); !errors.Is(err, errOffboardingID) {
		t.Fatalf("bad project key err = %v, want errOffboardingID", err)
	}
	if _, err := parseProjectOwnerOverrides(map[string]string{projectA.String(): "bad"}); !errors.Is(err, errOffboardingID) {
		t.Fatalf("bad user value err = %v, want errOffboardingID", err)
	}
}

func TestImpactRequiresReassignment(t *testing.T) {
	cases := []struct {
		name   string
		impact OffboardingImpact
		want   bool
	}{
		{name: "clean impact", impact: OffboardingImpact{OwnedArtifacts: map[string]int{"prds": 0}}},
		{name: "owned project", impact: OffboardingImpact{OwnedProjects: []OffboardingProject{{ID: "p"}}}, want: true},
		{name: "open assignment", impact: OffboardingImpact{OpenAssignments: []OffboardingAssignment{{ID: "w"}}}, want: true},
		{name: "pending approval", impact: OffboardingImpact{PendingApprovals: []OffboardingApproval{{ID: "a"}}}, want: true},
		{name: "owned artifact", impact: OffboardingImpact{OwnedArtifacts: map[string]int{"prds": 1}}, want: true},
		{name: "sessions alone do not require reassignment", impact: OffboardingImpact{ActiveSessions: 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := impactRequiresReassignment(tc.impact); got != tc.want {
				t.Fatalf("impactRequiresReassignment() = %v, want %v", got, tc.want)
			}
		})
	}
}
