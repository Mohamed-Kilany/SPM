package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/models"
)

// ---- CanWrite unit tests ----------------------------------------------------

func TestCanWrite_Admin_AlwaysTrue(t *testing.T) {
	claims := &models.AuthClaims{
		Role:       auth.RoleAdmin,
		ProjectIDs: []string{}, // no explicit assignments needed
	}
	assert.True(t, auth.CanWrite(claims, "any-project-id"))
	assert.True(t, auth.CanWrite(claims, "another-project"))
	assert.True(t, auth.CanWrite(claims, ""))
}

func TestCanWrite_Member_AssignedProject_True(t *testing.T) {
	claims := &models.AuthClaims{
		Role:       auth.RoleMember,
		ProjectIDs: []string{"proj-1", "proj-2"},
	}
	assert.True(t, auth.CanWrite(claims, "proj-1"))
	assert.True(t, auth.CanWrite(claims, "proj-2"))
}

func TestCanWrite_Member_UnassignedProject_False(t *testing.T) {
	claims := &models.AuthClaims{
		Role:       auth.RoleMember,
		ProjectIDs: []string{"proj-1"},
	}
	assert.False(t, auth.CanWrite(claims, "proj-99"))
	assert.False(t, auth.CanWrite(claims, ""))
}

func TestCanWrite_Member_NoProjects_False(t *testing.T) {
	claims := &models.AuthClaims{
		Role:       auth.RoleMember,
		ProjectIDs: []string{},
	}
	assert.False(t, auth.CanWrite(claims, "proj-1"))
}

func TestCanWrite_Viewer_AlwaysFalse(t *testing.T) {
	claims := &models.AuthClaims{
		Role:       auth.RoleViewer,
		ProjectIDs: []string{"proj-1", "proj-2"}, // even if listed, viewer cannot write
	}
	assert.False(t, auth.CanWrite(claims, "proj-1"))
	assert.False(t, auth.CanWrite(claims, "proj-2"))
	assert.False(t, auth.CanWrite(claims, "any-project"))
}

func TestCanWrite_UnknownRole_False(t *testing.T) {
	claims := &models.AuthClaims{
		Role:       "superuser",
		ProjectIDs: []string{"proj-1"},
	}
	assert.False(t, auth.CanWrite(claims, "proj-1"))
	assert.False(t, auth.CanWrite(claims, "any-project"))
}

func TestCanWrite_NilClaims_False(t *testing.T) {
	assert.False(t, auth.CanWrite(nil, "proj-1"))
}

// ---- RequireWrite middleware tests ------------------------------------------

// buildRBACRouter creates a Gin router where:
//  1. JWT Middleware authenticates the request (reads JWT_SECRET from env).
//  2. RequireWrite() checks write permission on :id.
//  3. A simple 200 handler confirms the request reached the endpoint.
func buildRBACRouter(t *testing.T) *gin.Engine {
	t.Helper()
	t.Setenv("JWT_SECRET", testSecret)

	r := gin.New()
	r.POST("/projects/:id/tickets",
		auth.Middleware(nil),
		auth.RequireWrite(),
		func(c *gin.Context) {
			c.Status(http.StatusOK)
		},
	)
	return r
}

func TestRequireWrite_Admin_Returns200(t *testing.T) {
	tokenStr, err := auth.IssueToken("admin-user", auth.RoleAdmin, nil, testSecret, 1)
	require.NoError(t, err)

	r := buildRBACRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/projects/proj-1/tickets", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireWrite_Viewer_Returns403(t *testing.T) {
	tokenStr, err := auth.IssueToken("viewer-user", auth.RoleViewer, []string{"proj-1"}, testSecret, 1)
	require.NoError(t, err)

	r := buildRBACRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/projects/proj-1/tickets", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "FORBIDDEN")
}

func TestRequireWrite_Member_AssignedProject_Returns200(t *testing.T) {
	tokenStr, err := auth.IssueToken("member-user", auth.RoleMember, []string{"proj-1"}, testSecret, 1)
	require.NoError(t, err)

	r := buildRBACRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/projects/proj-1/tickets", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireWrite_Member_UnassignedProject_Returns403(t *testing.T) {
	tokenStr, err := auth.IssueToken("member-user", auth.RoleMember, []string{"proj-1"}, testSecret, 1)
	require.NoError(t, err)

	r := buildRBACRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/projects/proj-99/tickets", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "FORBIDDEN")
}
