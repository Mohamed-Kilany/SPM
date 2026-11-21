package workspace

import "testing"

func TestTestRunOutcome(t *testing.T) {
	cases := []struct {
		name   string
		counts testRunCounts
		want   string
	}{
		{"all passed", testRunCounts{Total: 3, Passed: 3}, "passed"},
		{"blocked and skipped are not failures", testRunCounts{Total: 2, Blocked: 1, Skipped: 1}, "passed"},
		{"any failure", testRunCounts{Total: 3, Passed: 2, Failed: 1}, "failed"},
		{"failure wins over incomplete", testRunCounts{Total: 3, Failed: 1, NotRun: 2}, "incomplete"},
		{"remaining cases", testRunCounts{Total: 3, Passed: 1, NotRun: 2}, "incomplete"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testRunOutcome(tc.counts); got != tc.want {
				t.Fatalf("testRunOutcome(%+v) = %q, want %q", tc.counts, got, tc.want)
			}
		})
	}
}

func TestTestRunOutcomeIncompletePrecedesFailure(t *testing.T) {
	// A forced finish with both failures and unexecuted cases is incomplete:
	// the run cannot claim a definitive failed outcome when coverage is short.
	counts := testRunCounts{Total: 4, Failed: 2, NotRun: 2}
	if got := testRunOutcome(counts); got != "incomplete" {
		t.Fatalf("outcome = %q, want incomplete", got)
	}
}
