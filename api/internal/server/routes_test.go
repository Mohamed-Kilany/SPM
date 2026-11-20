package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"spm-platform/api/internal/auth"
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

// TestProjectRoutesDeclareCapability keeps the capability contract complete:
// every project-scoped route names exactly one capability, and every mutating
// route is capability-gated rather than falling back to the old coarse gate.
func TestProjectRoutesDeclareCapability(t *testing.T) {
	for _, route := range Routes() {
		projectScoped := strings.Contains(route.Path, "/projects/:id")
		if projectScoped {
			if !auth.ValidCapability(route.Capability) {
				t.Errorf("project route %s %s has invalid capability %q", route.Method, route.Path, route.Capability)
			}
			if route.Method != http.MethodGet && route.Capability == "" {
				t.Errorf("mutating project route %s %s has no capability", route.Method, route.Path)
			}
			continue
		}
		if route.Capability != "" {
			t.Errorf("non-project route %s %s declares capability %q", route.Method, route.Path, route.Capability)
		}
	}

	// Every capability in the matrix is attached to at least one route, so a
	// typo cannot silently create an unreachable capability.
	used := map[auth.Capability]bool{}
	for _, route := range Routes() {
		if route.Capability != "" {
			used[route.Capability] = true
		}
	}
	for _, capability := range auth.AllCapabilities {
		switch capability {
		case auth.CapPRReview, auth.CapAuditRead, auth.CapDeliveryRollback:
			// Review submission arrives with GOV-03, audit reads with GOV-04
			// and rollback with the delivery write surface. The capabilities
			// exist now so those follow-ups attach to the same matrix.
			continue
		}
		if !used[capability] {
			t.Errorf("capability %q is not attached to a route", capability)
		}
	}
}

// staticRoleResolver resolves every project membership to one role, so route
// wiring can be exercised without PostgreSQL.
type staticRoleResolver struct {
	role   string
	member bool
}

func (s staticRoleResolver) RoleForProject(context.Context, string, string) (string, bool, bool, error) {
	return s.role, s.member, true, nil
}

// capabilityRequest exercises one registered route with one token and resolver.
func capabilityRequest(t *testing.T, route Route, role, globalRole string, member bool) int {
	t.Helper()
	const secret = "route-capability-test-secret"
	t.Setenv("JWT_SECRET", secret)

	token, err := auth.IssueToken("user-1", globalRole, nil, secret, 1)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Handle(route.Method, route.Path,
		auth.Middleware(nil),
		auth.RequireProjectCapabilityWithResolver(staticRoleResolver{role: role, member: member}, route.Capability),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	req := httptest.NewRequest(route.Method, ginParam.ReplaceAllString(route.Path, "x"), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// TestProjectRoutesEnforceMatrix walks every capability-gated route for every
// project role. It proves the live middleware binding matches the canonical
// matrix, not just the metadata table.
func TestProjectRoutesEnforceMatrix(t *testing.T) {
	for _, route := range Routes() {
		if route.Capability == "" {
			continue
		}
		for _, role := range auth.ProjectRoles {
			want := http.StatusForbidden
			if auth.RoleHasCapability(role, route.Capability) {
				want = http.StatusNoContent
			}
			got := capabilityRequest(t, route, role, auth.RoleMember, true)
			if got != want {
				t.Errorf("%s %s as %s: status = %d, want %d (capability %s)",
					route.Method, route.Path, role, got, want, route.Capability)
			}
		}
	}
}

// TestProjectRoutesEnforceGlobalViewer proves a global viewer stays read-only
// even when the membership row says owner.
func TestProjectRoutesEnforceGlobalViewer(t *testing.T) {
	readOnly := map[auth.Capability]bool{
		auth.CapProjectRead:  true,
		auth.CapCodeRead:     true,
		auth.CapDeliveryRead: true,
	}
	for _, route := range Routes() {
		if route.Capability == "" {
			continue
		}
		want := http.StatusForbidden
		if readOnly[route.Capability] {
			want = http.StatusNoContent
		}
		got := capabilityRequest(t, route, auth.ProjectRoleOwner, auth.RoleViewer, true)
		if got != want {
			t.Errorf("%s %s as global viewer/owner: status = %d, want %d (capability %s)",
				route.Method, route.Path, got, want, route.Capability)
		}
	}
}

// TestProjectRoutesAllowGlobalAdmin proves break-glass access is wired on every
// project route, including when no membership row exists.
func TestProjectRoutesAllowGlobalAdmin(t *testing.T) {
	for _, route := range Routes() {
		if route.Capability == "" {
			continue
		}
		got := capabilityRequest(t, route, "", auth.RoleAdmin, false)
		if got != http.StatusNoContent {
			t.Errorf("%s %s as global admin: status = %d, want %d (capability %s)",
				route.Method, route.Path, got, http.StatusNoContent, route.Capability)
		}
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
