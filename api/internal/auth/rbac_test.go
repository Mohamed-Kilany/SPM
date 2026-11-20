package auth_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/models"
)

// TestCapabilityMatrix pins the canonical role-to-capability matrix. The
// expectations are written independently of the implementation so a role
// accidentally gaining or losing a capability fails here.
func TestCapabilityMatrix(t *testing.T) {
	expected := map[string]map[auth.Capability]bool{
		auth.ProjectRoleOwner:      capabilitySet(auth.AllCapabilities...),
		auth.ProjectRoleMaintainer: capabilitySet(auth.AllCapabilities...),
		auth.ProjectRoleDeveloper: capabilitySet(
			auth.CapProjectRead,
			auth.CapPlanningWrite,
			auth.CapWorkItemWrite,
			auth.CapTestWrite,
			auth.CapCodeRead,
			auth.CapCodeWrite,
			auth.CapPRReview,
			auth.CapDeliveryRead,
			auth.CapDeliveryDeploy,
		),
		auth.ProjectRoleTester: capabilitySet(
			auth.CapProjectRead,
			auth.CapWorkItemWrite,
			auth.CapTestWrite,
			auth.CapCodeRead,
			auth.CapDeliveryRead,
		),
		auth.ProjectRoleViewer: capabilitySet(
			auth.CapProjectRead,
			auth.CapCodeRead,
			auth.CapDeliveryRead,
		),
	}

	for _, role := range auth.ProjectRoles {
		for _, capability := range auth.AllCapabilities {
			want := expected[role][capability]
			if got := auth.RoleHasCapability(role, capability); got != want {
				t.Errorf("RoleHasCapability(%q, %q) = %v, want %v", role, capability, got, want)
			}
			has := false
			for _, granted := range auth.CapabilitiesForRole(role) {
				if granted == capability {
					has = true
				}
			}
			if has != want {
				t.Errorf("CapabilitiesForRole(%q) contains %q = %v, want %v", role, capability, has, want)
			}
		}
	}

	for _, capability := range auth.AllCapabilities {
		if !auth.ValidCapability(capability) {
			t.Errorf("AllCapabilities contains invalid capability %q", capability)
		}
	}
	if auth.ValidCapability("project.destroy") {
		t.Error("ValidCapability accepted an unknown capability")
	}
}

func TestEffectiveCapabilities(t *testing.T) {
	assert.Equal(t, auth.AllCapabilities, auth.EffectiveCapabilities(auth.RoleAdmin, auth.ProjectRoleViewer),
		"global admins keep break-glass access to every capability")

	viewerOwner := auth.EffectiveCapabilities(auth.RoleViewer, auth.ProjectRoleOwner)
	assert.ElementsMatch(t,
		[]auth.Capability{auth.CapProjectRead, auth.CapCodeRead, auth.CapDeliveryRead},
		viewerOwner,
		"global viewers must stay read-only even when they own a project")
	for _, forbidden := range []auth.Capability{
		auth.CapSettingsWrite, auth.CapMemberWrite, auth.CapPRMerge, auth.CapWorkItemWrite,
	} {
		assert.NotContains(t, viewerOwner, forbidden)
	}

	developer := auth.EffectiveCapabilities(auth.RoleMember, auth.ProjectRoleDeveloper)
	assert.Contains(t, developer, auth.CapCodeWrite)
	assert.NotContains(t, developer, auth.CapPRMerge)
	assert.NotContains(t, developer, auth.CapSettingsWrite)
	assert.NotContains(t, developer, auth.CapMemberWrite)

	tester := auth.EffectiveCapabilities(auth.RoleMember, auth.ProjectRoleTester)
	assert.Contains(t, tester, auth.CapWorkItemWrite)
	assert.NotContains(t, tester, auth.CapPlanningWrite)
	assert.NotContains(t, tester, auth.CapCodeWrite)

	assert.Empty(t, auth.EffectiveCapabilities("superuser", auth.ProjectRoleOwner))
	assert.Empty(t, auth.EffectiveCapabilities(auth.RoleMember, "unknown"))
}

type fakeResolver struct {
	role     string
	member   bool
	archived bool
	err      error
	calls    int
}

func (f *fakeResolver) RoleForProject(_ context.Context, _, _ string) (string, bool, bool, error) {
	f.calls++
	return f.role, f.member, !f.archived, f.err
}

func capabilityRouter(t *testing.T, resolver auth.MembershipResolver, capability auth.Capability) *gin.Engine {
	t.Helper()
	t.Setenv("JWT_SECRET", testSecret)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/projects/:id/check",
		auth.Middleware(nil),
		auth.RequireProjectCapabilityWithResolver(resolver, capability),
		func(c *gin.Context) {
			access, ok := auth.ProjectAccessFrom(c)
			if !ok {
				c.Status(http.StatusInternalServerError)
				return
			}
			c.JSON(http.StatusOK, gin.H{
				"role":         auth.ProjectRoleFrom(c),
				"capabilities": access.Capabilities,
			})
		},
	)
	return r
}

func callCapabilityRouter(t *testing.T, r *gin.Engine, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/projects/project-1/check", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func tokenFor(t *testing.T, userID, role string) string {
	t.Helper()
	token, err := auth.IssueToken(userID, role, nil, testSecret, 1)
	require.NoError(t, err)
	return token
}

func TestRequireProjectCapability(t *testing.T) {
	cases := []struct {
		name       string
		globalRole string
		role       string
		member     bool
		capability auth.Capability
		want       int
	}{
		{"owner settings", auth.RoleMember, auth.ProjectRoleOwner, true, auth.CapSettingsWrite, http.StatusOK},
		{"maintainer members", auth.RoleMember, auth.ProjectRoleMaintainer, true, auth.CapMemberWrite, http.StatusOK},
		{"developer code write", auth.RoleMember, auth.ProjectRoleDeveloper, true, auth.CapCodeWrite, http.StatusOK},
		{"developer cannot merge", auth.RoleMember, auth.ProjectRoleDeveloper, true, auth.CapPRMerge, http.StatusForbidden},
		{"developer cannot manage settings", auth.RoleMember, auth.ProjectRoleDeveloper, true, auth.CapSettingsWrite, http.StatusForbidden},
		{"tester cannot plan", auth.RoleMember, auth.ProjectRoleTester, true, auth.CapPlanningWrite, http.StatusForbidden},
		{"tester can test", auth.RoleMember, auth.ProjectRoleTester, true, auth.CapTestWrite, http.StatusOK},
		{"tester can triage work items", auth.RoleMember, auth.ProjectRoleTester, true, auth.CapWorkItemWrite, http.StatusOK},
		{"viewer can read", auth.RoleMember, auth.ProjectRoleViewer, true, auth.CapProjectRead, http.StatusOK},
		{"viewer cannot write", auth.RoleMember, auth.ProjectRoleViewer, true, auth.CapWorkItemWrite, http.StatusForbidden},
		{"non-member denied", auth.RoleMember, "", false, auth.CapProjectRead, http.StatusForbidden},
		{"global viewer cannot write as owner", auth.RoleViewer, auth.ProjectRoleOwner, true, auth.CapMemberWrite, http.StatusForbidden},
		{"global viewer can read as owner", auth.RoleViewer, auth.ProjectRoleOwner, true, auth.CapProjectRead, http.StatusOK},
		{"unknown project role denied", auth.RoleMember, "superuser", true, auth.CapProjectRead, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &fakeResolver{role: tc.role, member: tc.member}
			r := capabilityRouter(t, resolver, tc.capability)
			w := callCapabilityRouter(t, r, tokenFor(t, "user-1", tc.globalRole))
			assert.Equal(t, tc.want, w.Code, w.Body.String())
		})
	}
}

func TestRequireProjectCapabilityAdminBreakGlass(t *testing.T) {
	resolver := &fakeResolver{member: false}
	r := capabilityRouter(t, resolver, auth.CapSettingsWrite)
	w := callCapabilityRouter(t, r, tokenFor(t, "admin-1", auth.RoleAdmin))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Zero(t, resolver.calls, "global admins must not depend on a membership row")
	assert.Contains(t, w.Body.String(), `"role":"admin"`)
}

func TestRequireProjectCapabilityRoleChangeIsImmediate(t *testing.T) {
	resolver := &fakeResolver{role: auth.ProjectRoleDeveloper, member: true}
	r := capabilityRouter(t, resolver, auth.CapCodeWrite)
	token := tokenFor(t, "user-1", auth.RoleMember)

	assert.Equal(t, http.StatusOK, callCapabilityRouter(t, r, token).Code)

	resolver.role = auth.ProjectRoleViewer
	assert.Equal(t, http.StatusForbidden, callCapabilityRouter(t, r, token).Code,
		"a role downgrade must apply on the next request")

	resolver.role = auth.ProjectRoleMaintainer
	settings := capabilityRouter(t, resolver, auth.CapSettingsWrite)
	assert.Equal(t, http.StatusOK, callCapabilityRouter(t, settings, token).Code,
		"a role upgrade must apply on the next request")
}

func TestRequireProjectCapabilityArchivedProject(t *testing.T) {
	resolver := &fakeResolver{role: auth.ProjectRoleOwner, member: true, archived: true}
	r := capabilityRouter(t, resolver, auth.CapProjectRead)
	w := callCapabilityRouter(t, r, tokenFor(t, "user-1", auth.RoleMember))
	assert.Equal(t, http.StatusForbidden, w.Code, "archived projects stop granting access immediately")
	assert.Contains(t, w.Body.String(), "project.read")

	// Break-glass access is intentionally unaffected by the archive state.
	admin := capabilityRouter(t, &fakeResolver{archived: true}, auth.CapProjectRead)
	assert.Equal(t, http.StatusOK,
		callCapabilityRouter(t, admin, tokenFor(t, "admin-1", auth.RoleAdmin)).Code)
}

func TestRequireProjectCapabilityFailsClosed(t *testing.T) {
	r := capabilityRouter(t, &fakeResolver{err: errors.New("database down")}, auth.CapProjectRead)
	token := tokenFor(t, "user-1", auth.RoleMember)
	assert.Equal(t, http.StatusForbidden, callCapabilityRouter(t, r, token).Code)

	missingAuth := capabilityRouter(t, &fakeResolver{role: auth.ProjectRoleOwner, member: true}, auth.CapProjectRead)
	assert.Equal(t, http.StatusUnauthorized, callCapabilityRouter(t, missingAuth, "").Code)
}

func TestRequireProjectCapabilityExposesResolvedSet(t *testing.T) {
	resolver := &fakeResolver{role: auth.ProjectRoleDeveloper, member: true}
	r := capabilityRouter(t, resolver, auth.CapCodeWrite)
	w := callCapabilityRouter(t, r, tokenFor(t, "user-1", auth.RoleMember))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"role":"developer"`)
	assert.Contains(t, w.Body.String(), `"code.write"`)
	assert.NotContains(t, w.Body.String(), `"member.write"`)
}

// TestAuthzLogsStripLineBreaks proves that caller-controlled project IDs and
// paths cannot forge log entries. CodeQL flags this class as CWE-117; the
// structured handler also quotes values, but the sanitizer keeps the guarantee
// independent of the handler.
func TestAuthzLogsStripLineBreaks(t *testing.T) {
	cases := []struct {
		name   string
		claims *models.AuthClaims
	}{
		{"break glass", &models.AuthClaims{UserID: "admin-1", Role: auth.RoleAdmin}},
		{"denied", &models.AuthClaims{UserID: "user-1", Role: auth.RoleMember}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/projects/x/check", nil)
			c.Request.URL.Path = "/api/v1/projects/x\nforged/check"
			c.Params = gin.Params{{Key: "id", Value: "proj\r\nforged"}}
			c.Set(auth.ClaimsKey, tc.claims)

			handler := auth.RequireProjectCapabilityWithResolver(
				&fakeResolver{member: false}, auth.CapSettingsWrite)
			handler(c)

			out := strings.TrimSpace(buf.String())
			require.NotEmpty(t, out)
			assert.NotContains(t, out, `\n`, "log entry kept an escaped newline")
			assert.NotContains(t, out, `\r`, "log entry kept an escaped carriage return")
			assert.Contains(t, out, `"project_id":"projforged"`)
			assert.Contains(t, out, `"path":"/api/v1/projects/xforged/check"`)
		})
	}
}

func capabilitySet(capabilities ...auth.Capability) map[auth.Capability]bool {
	set := make(map[auth.Capability]bool, len(capabilities))
	for _, capability := range capabilities {
		set[capability] = true
	}
	return set
}
