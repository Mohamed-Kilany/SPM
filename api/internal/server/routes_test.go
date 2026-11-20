package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

var (
	ginParam  = regexp.MustCompile(`:[A-Za-z0-9_]+`)
	docParam  = regexp.MustCompile(`\{[^}]+\}`)
	routeKeyf = func(method, path string) string { return method + " " + path }
)

var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true,
}

func normalizePath(path string) string {
	return docParam.ReplaceAllString(ginParam.ReplaceAllString(path, "{}"), "{}")
}

func ginToOpenAPI(path string) string {
	return ginParam.ReplaceAllStringFunc(path, func(match string) string {
		return "{" + strings.TrimPrefix(match, ":") + "}"
	})
}

func routeSet(routes []Route) map[string]Route {
	set := make(map[string]Route, len(routes))
	for _, route := range routes {
		key := routeKeyf(route.Method, normalizePath(route.Path))
		set[key] = route
	}
	return set
}

// TestRoutesMatchRegistration proves the machine-readable route inventory is
// the same set the Gin engine actually serves. A route added or removed in one
// place without the other fails here.
func TestRoutesMatchRegistration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r, Handlers{})

	registered := make([]Route, 0, len(r.Routes()))
	for _, route := range r.Routes() {
		registered = append(registered, Route{Method: route.Method, Path: route.Path})
	}

	actual := routeSet(registered)
	documented := routeSet(Routes())

	var missing, extra []string
	for key := range actual {
		if _, ok := documented[key]; !ok {
			missing = append(missing, key)
		}
	}
	for key := range documented {
		if _, ok := actual[key]; !ok {
			extra = append(extra, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("route inventory drift:\nregistered but missing from Routes(): %v\ndocumented but not registered: %v", missing, extra)
	}
}

// TestRoutesHaveSupportedAuth keeps the auth vocabulary closed.
func TestRoutesHaveSupportedAuth(t *testing.T) {
	supported := map[AuthLevel]bool{
		AuthPublic: true, AuthBearer: true, AuthMember: true, AuthAdmin: true,
		AuthProjectRead: true, AuthProjectWrite: true, AuthProjectAdmin: true, AuthSSETicket: true,
	}
	validated := make(map[string]bool)
	for _, route := range Routes() {
		key := routeKeyf(route.Method, normalizePath(route.Path))
		if validated[key] {
			t.Errorf("duplicate route in Routes(): %s", key)
		}
		validated[key] = true
		if !supported[route.Auth] {
			t.Errorf("route %s has unsupported auth level %q", key, route.Auth)
		}
	}
	if len(validated) == 0 {
		t.Fatal("route inventory is empty")
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"/api/v1/projects/:id/members/:userId":          "/api/v1/projects/{}/members/{}",
		"/api/v1/projects/{projectId}/members/{userId}": "/api/v1/projects/{}/members/{}",
		"/health": "/health",
	}
	for input, want := range cases {
		if got := normalizePath(input); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestHealthReportsBuildMetadata proves the version injected at build time is
// visible to operators without reading the binary or the container tag.
func TestHealthReportsBuildMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r, Handlers{BuildInfo: BuildInfo{
		Version: "1.2.3",
		Commit:  "abc123",
		BuiltAt: "2026-11-19T00:00:00Z",
	}})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /health/live = %d, want %d", w.Code, http.StatusOK)
	}

	var body struct {
		Status string `json:"status"`
		Build  struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			BuiltAt string `json:"built_at"`
		} `json:"build"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Build.Version != "1.2.3" || body.Build.Commit != "abc123" || body.Build.BuiltAt != "2026-11-19T00:00:00Z" {
		t.Errorf("build metadata = %+v", body.Build)
	}
}

// TestHealthDefaultsAreHonest prevents a local or test build from claiming a
// release version.
func TestHealthDefaultsAreHonest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r, Handlers{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	var body struct {
		Build struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			BuiltAt string `json:"built_at"`
		} `json:"build"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body.Build.Version != "dev" || body.Build.Commit != "none" || body.Build.BuiltAt != "unknown" {
		t.Errorf("default build metadata = %+v", body.Build)
	}
}
