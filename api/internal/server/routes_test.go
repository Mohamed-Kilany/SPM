package server

import (
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
