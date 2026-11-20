package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"spm-platform/api/internal/metrics"
)

func testRegistry(t *testing.T) *metrics.Registry {
	t.Helper()
	registry := metrics.New(metrics.BuildInfo{Version: "test", Commit: "test"})
	previous := metrics.Default()
	metrics.SetDefault(registry)
	t.Cleanup(func() { metrics.SetDefault(previous) })
	return registry
}

func aiMetric(t *testing.T, registry *metrics.Registry, name string) []*dto.Metric {
	t.Helper()
	families, err := registry.Gatherer().Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return family.GetMetric()
		}
	}
	t.Fatalf("metric family %q not found", name)
	return nil
}

func labelValue(metric *dto.Metric, name string) string {
	for _, label := range metric.GetLabel() {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}

func TestSearchRecordsBoundedAIMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/semantic-search" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SearchResponse{Query: "checkout", Engine: "test"})
	}))
	defer server.Close()
	t.Setenv("AI_SERVICE_URL", server.URL)

	registry := testRegistry(t)
	client := New()
	if _, err := client.Search(context.Background(), "checkout", nil, 5); err != nil {
		t.Fatalf("Search: %v", err)
	}

	found := false
	for _, metric := range aiMetric(t, registry, "spm_ai_requests_total") {
		if labelValue(metric, "operation") == "/semantic-search" && labelValue(metric, "outcome") == "success" {
			found = true
		}
	}
	if !found {
		t.Fatal("successful AI request was not recorded with a bounded operation label")
	}
}

func TestAIErrorRecordsErrorOutcome(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("AI_SERVICE_URL", server.URL)

	registry := testRegistry(t)
	client := New()
	if _, err := client.Ask(context.Background(), "why?", nil, 5); err == nil {
		t.Fatal("Ask succeeded against a failing server")
	}

	found := false
	for _, metric := range aiMetric(t, registry, "spm_ai_requests_total") {
		if labelValue(metric, "operation") == "/ask" && labelValue(metric, "outcome") == "error" {
			found = true
		}
	}
	if !found {
		t.Fatal("failed AI request was not recorded as an error outcome")
	}
}
