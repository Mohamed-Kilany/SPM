package metrics

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	dto "github.com/prometheus/client_model/go"
)

func newTestRegistry() *Registry {
	return New(BuildInfo{Version: "1.2.3", Commit: "abc123", BuiltAt: "2026-11-20T00:00:00Z"})
}

func family(t *testing.T, registry *Registry, name string) *dto.MetricFamily {
	t.Helper()
	families, err := registry.Gatherer().Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, candidate := range families {
		if candidate.GetName() == name {
			return candidate
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

func TestHTTPMiddlewareUsesRouteTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := newTestRegistry()
	router := gin.New()
	router.Use(registry.HTTPMiddleware())
	router.GET("/api/v1/projects/:id/items/:itemId", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/6c1f9b2e-4a55-4d7a-9f11-2b5f7d3c8a10/items/raw-secret-id", nil)
	router.ServeHTTP(httptest.NewRecorder(), request)

	found := false
	for _, metric := range family(t, registry, "spm_http_requests_total").GetMetric() {
		route := labelValue(metric, "route")
		if strings.Contains(route, "6c1f9b2e") || strings.Contains(route, "raw-secret-id") {
			t.Fatalf("route label leaked a raw identifier: %q", route)
		}
		if route == "/api/v1/projects/:id/items/:itemId" {
			found = true
			if got := labelValue(metric, "method"); got != http.MethodGet {
				t.Errorf("method label = %q, want GET", got)
			}
			if got := labelValue(metric, "status"); got != "204" {
				t.Errorf("status label = %q, want 204", got)
			}
		}
	}
	if !found {
		t.Fatal("route template was not recorded")
	}
}

func TestHTTPMiddlewareMarksUnmatchedRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := newTestRegistry()
	router := gin.New()
	router.Use(registry.HTTPMiddleware())

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/does-not-exist/42", nil))

	found := false
	for _, metric := range family(t, registry, "spm_http_requests_total").GetMetric() {
		if labelValue(metric, "route") == "unmatched" && labelValue(metric, "status") == "404" {
			found = true
		}
	}
	if !found {
		t.Fatal("unmatched route was not recorded with a bounded label")
	}
}

func TestScrapeReturnsPrometheusTextAndBuildInfo(t *testing.T) {
	registry := newTestRegistry()
	response := httptest.NewRecorder()
	registry.Handler("").ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", contentType)
	}
	body := response.Body.String()
	for _, expected := range []string{
		"spm_build_info",
		`version="1.2.3"`,
		`commit="abc123"`,
		`component="api"`,
		"go_goroutines",
		"process_start_time_seconds",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("scrape output is missing %q", expected)
		}
	}
}

func TestScrapeTokenIsRequiredWhenConfigured(t *testing.T) {
	registry := newTestRegistry()
	handler := registry.Handler("s3cret")

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /metrics = %d, want 401", unauthorized.Code)
	}
	if unauthorized.Header().Get("WWW-Authenticate") == "" {
		t.Error("401 response is missing WWW-Authenticate")
	}

	authorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer s3cret")
	handler.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authenticated GET /metrics = %d, want 200", authorized.Code)
	}
}

func TestNilRegistryIsSafe(t *testing.T) {
	var registry *Registry
	registry.ObserveDependency("gitea", http.MethodGet, "200", time.Millisecond)
	registry.ObserveAI("/ask", time.Millisecond, nil)
	registry.ObserveReconcile("argocd", time.Millisecond, errors.New("boom"))
	registry.RegisterPool(nil)
	registry.RegisterNATS(nil)
	registry.RegisterSSE(nil)

	response := httptest.NewRecorder()
	registry.Handler("").ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil registry handler = %d, want 503", response.Code)
	}
}

func TestSSEGaugeReadsCurrentCount(t *testing.T) {
	registry := newTestRegistry()
	active := 3
	registry.RegisterSSE(func() int { return active })

	found := family(t, registry, "spm_sse_connections_active").GetMetric()
	if len(found) != 1 || found[0].GetGauge().GetValue() != 3 {
		t.Fatalf("sse gauge = %+v, want 3", found)
	}
}

func TestReconcileAndAIOutcomes(t *testing.T) {
	registry := newTestRegistry()
	registry.ObserveReconcile("gitea-actions", 250*time.Millisecond, nil)
	registry.ObserveReconcile("argocd", 100*time.Millisecond, errors.New("unavailable"))
	registry.ObserveAI("/ask", 300*time.Millisecond, nil)
	registry.ObserveAI("/ask", 50*time.Millisecond, errors.New("timeout"))

	reconcile := family(t, registry, "spm_delivery_reconcile_runs_total")
	if len(reconcile.GetMetric()) != 2 {
		t.Fatalf("reconcile runs = %d, want 2", len(reconcile.GetMetric()))
	}
	ai := family(t, registry, "spm_ai_requests_total")
	if len(ai.GetMetric()) != 2 {
		t.Fatalf("AI requests = %d, want 2", len(ai.GetMetric()))
	}
	lastSuccess := family(t, registry, "spm_delivery_reconcile_last_success_timestamp_seconds")
	if len(lastSuccess.GetMetric()) != 1 {
		t.Fatalf("last success = %d series, want only the successful source", len(lastSuccess.GetMetric()))
	}
	if labelValue(lastSuccess.GetMetric()[0], "source") != "gitea-actions" {
		t.Errorf("last success source = %q, want gitea-actions", labelValue(lastSuccess.GetMetric()[0], "source"))
	}
}

func TestInstrumentTransportRecordsDependency(t *testing.T) {
	registry := newTestRegistry()
	previous := Default()
	SetDefault(registry)
	defer SetDefault(previous)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := &http.Client{Transport: InstrumentTransport("gitea", nil)}
	response, err := client.Get(server.URL + "/api/v1/repos/spm/demo")
	if err != nil {
		t.Fatalf("GET fake dependency: %v", err)
	}
	response.Body.Close()

	found := false
	for _, metric := range family(t, registry, "spm_dependency_requests_total").GetMetric() {
		if labelValue(metric, "dependency") == "gitea" &&
			labelValue(metric, "method") == http.MethodGet &&
			labelValue(metric, "status") == "202" {
			found = true
		}
	}
	if !found {
		t.Fatal("dependency request was not recorded")
	}
}
