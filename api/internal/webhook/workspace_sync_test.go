package webhook

import "testing"

func TestNormalizeBuildStatus(t *testing.T) {
	tests := []struct {
		status     string
		conclusion string
		want       string
	}{
		{"queued", "", "queued"},
		{"in_progress", "", "running"},
		{"completed", "success", "success"},
		{"completed", "failure", "failure"},
		{"completed", "timed_out", "timed_out"},
		{"completed", "cancelled", "cancelled"},
		{"FAILED", "", "failure"},
		{"unknown", "", "unknown"},
	}
	for _, test := range tests {
		if got := normalizeBuildStatus(test.status, test.conclusion); got != test.want {
			t.Errorf("normalizeBuildStatus(%q, %q) = %q, want %q", test.status, test.conclusion, got, test.want)
		}
	}
}

func TestMapArgoStatus(t *testing.T) {
	tests := []struct {
		sync, health           string
		wantStatus, wantHealth string
	}{
		{"Synced", "Healthy", "healthy", "healthy"},
		{"Synced", "Degraded", "degraded", "degraded"},
		{"Synced", "Progressing", "deploying", "unknown"},
		{"OutOfSync", "Healthy", "degraded", "degraded"},
		{"OutOfSync", "Unknown", "degraded", "degraded"},
		{"Synced", "Unknown", "verifying", "unknown"},
		{"", "Missing", "deploying", "unknown"},
	}
	for _, test := range tests {
		status, health := mapArgoStatus(test.sync, test.health)
		if status != test.wantStatus || health != test.wantHealth {
			t.Errorf("mapArgoStatus(%q, %q) = (%q, %q), want (%q, %q)",
				test.sync, test.health, status, health, test.wantStatus, test.wantHealth)
		}
	}
}

func TestPayloadHelpers(t *testing.T) {
	payload := map[string]interface{}{
		"repository": map[string]interface{}{"full_name": "spm/atlas"},
		"pull_request": map[string]interface{}{
			"number": float64(42),
			"head":   map[string]interface{}{"ref": "feature/ATL-7-checkout"},
		},
	}
	if got := getString(payload, "repository", "full_name"); got != "spm/atlas" {
		t.Fatalf("getString() = %q", got)
	}
	pullRequest, _ := payload["pull_request"].(map[string]interface{})
	if got := firstInt(pullRequest, "number"); got != 42 {
		t.Fatalf("firstInt() = %d", got)
	}
	if got := getString(payload, "pull_request", "head", "ref"); got != "feature/ATL-7-checkout" {
		t.Fatalf("nested getString() = %q", got)
	}
	if got := getString(payload, "missing", "value"); got != "" {
		t.Fatalf("missing getString() = %q", got)
	}
}
