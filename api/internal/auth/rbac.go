package auth

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/models"
)

// Role constants match the values stored in AuthClaims.Role and the users
// table CHECK constraint.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
	RoleViewer = "viewer"
)

// Project roles. These are stored in project_members.role and are independent
// from the global role in the JWT.
const (
	ProjectRoleOwner      = "owner"
	ProjectRoleMaintainer = "maintainer"
	ProjectRoleDeveloper  = "developer"
	ProjectRoleTester     = "tester"
	ProjectRoleViewer     = "viewer"
)

// ProjectRoles is the canonical ordered list for UI pickers and validation.
var ProjectRoles = []string{
	ProjectRoleOwner,
	ProjectRoleMaintainer,
	ProjectRoleDeveloper,
	ProjectRoleTester,
	ProjectRoleViewer,
}

// ValidProjectRole reports whether role is a supported project role.
func ValidProjectRole(role string) bool {
	for _, candidate := range ProjectRoles {
		if role == candidate {
			return true
		}
	}
	return false
}

// CanWriteProjectRole reports whether a project role may mutate project data.
func CanWriteProjectRole(role string) bool {
	switch role {
	case ProjectRoleOwner, ProjectRoleMaintainer, ProjectRoleDeveloper, ProjectRoleTester:
		return true
	default:
		return false
	}
}

// CanAdminProjectRole reports whether a project role may manage membership,
// settings and destructive project operations.
func CanAdminProjectRole(role string) bool {
	return role == ProjectRoleOwner || role == ProjectRoleMaintainer
}

var forbiddenResponse = errorResponse{
	Error: errorBody{Code: "FORBIDDEN", Message: "Insufficient permissions"},
}

// CanRead returns true if the given claims authorize reading the project.
//   - Admin:   always true.
//   - Member/Viewer: only if projectID appears in claims.ProjectIDs.
func CanRead(claims *models.AuthClaims, projectID string) bool {
	if claims == nil {
		return false
	}
	if claims.Role == RoleAdmin {
		return true
	}
	if claims.Role != RoleMember && claims.Role != RoleViewer {
		return false
	}
	for _, id := range claims.ProjectIDs {
		if id == projectID {
			return true
		}
	}
	return false
}

// CanWrite returns true if the given claims authorize a write operation on the
// specified projectID.
//   - Admin:   always true.
//   - Member:  true only if projectID appears in claims.ProjectIDs.
//   - Viewer:  always false.
func CanWrite(claims *models.AuthClaims, projectID string) bool {
	if claims == nil {
		return false
	}
	switch claims.Role {
	case RoleAdmin:
		return true
	case RoleMember:
		for _, id := range claims.ProjectIDs {
			if id == projectID {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// RequireRole returns a middleware that allows only the listed global roles.
// It is used for project creation, which is not project-scoped.
func RequireRole(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(c *gin.Context) {
		claims, ok := ClaimsFrom(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse{
				Error: errorBody{Code: "UNAUTHORIZED", Message: "Authentication required"},
			})
			return
		}
		if _, ok := allowed[claims.Role]; !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, forbiddenResponse)
			return
		}
		c.Next()
	}
}

// RequireWrite returns a project-scoped write middleware driven by the ":id"
// parameter and the project IDs embedded in the JWT.
func RequireWrite() gin.HandlerFunc {
	return RequireWriteForProject(func(c *gin.Context) string { return c.Param("id") })
}

// RequireWriteForProject is the claims-only project write middleware.
func RequireWriteForProject(getProjectID func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFrom(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse{
				Error: errorBody{Code: "UNAUTHORIZED", Message: "Authentication required"},
			})
			return
		}
		if !CanWrite(claims, getProjectID(c)) {
			c.AbortWithStatusJSON(http.StatusForbidden, forbiddenResponse)
			return
		}
		c.Next()
	}
}

// RequireRead returns a project-scoped read middleware driven by the ":id"
// parameter and the project IDs embedded in the JWT.
func RequireRead() gin.HandlerFunc {
	return RequireReadForProject(func(c *gin.Context) string { return c.Param("id") })
}

// RequireReadForProject is the claims-only project read middleware.
func RequireReadForProject(getProjectID func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFrom(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse{
				Error: errorBody{Code: "UNAUTHORIZED", Message: "Authentication required"},
			})
			return
		}
		if !CanRead(claims, getProjectID(c)) {
			c.AbortWithStatusJSON(http.StatusForbidden, forbiddenResponse)
			return
		}
		c.Next()
	}
}

// MembershipLookup resolves a user's project membership directly from
// PostgreSQL. It is used as an authoritative fallback because JWT project
// lists are a snapshot taken at login and can be stale after project creation
// or membership changes.
func MembershipLookup(ctx context.Context, pool *pgxpool.Pool, projectID, userID string) (role string, member bool, err error) {
	if pool == nil {
		return "", false, nil
	}
	const q = `SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`
	if err := pool.QueryRow(ctx, q, projectID, userID).Scan(&role); err != nil {
		return "", false, nil
	}
	return role, true, nil
}

// RequireProjectReadDB enforces read access using claims first and the
// project_members table as the source of truth. Admins bypass both checks.
func RequireProjectReadDB(pool *pgxpool.Pool) gin.HandlerFunc {
	return requireProjectAccessDB(pool, false)
}

// RequireProjectWriteDB enforces write access using claims first and the
// project_members table as the source of truth. Viewers are always denied.
func RequireProjectWriteDB(pool *pgxpool.Pool) gin.HandlerFunc {
	return requireProjectAccessDB(pool, true)
}

func requireProjectAccessDB(pool *pgxpool.Pool, write bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFrom(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse{
				Error: errorBody{Code: "UNAUTHORIZED", Message: "Authentication required"},
			})
			return
		}
		projectID := c.Param("id")
		if claims.Role == RoleAdmin {
			c.Next()
			return
		}

		allowedByClaims := CanRead(claims, projectID)
		if write {
			allowedByClaims = false
		}
		if allowedByClaims {
			c.Next()
			return
		}

		role, member, err := MembershipLookup(c.Request.Context(), pool, projectID, claims.CanonicalUserID())
		if err == nil && member {
			if !write || CanWriteProjectRole(role) {
				// Keep the claims fresh for downstream handlers.
				claims.ProjectIDs = appendUnique(claims.ProjectIDs, projectID)
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, forbiddenResponse)
	}
}

// RequireProjectAdminDB allows global admins and project owners/maintainers.
// It is used for membership and project-settings mutations.
func RequireProjectAdminDB(pool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFrom(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse{
				Error: errorBody{Code: "UNAUTHORIZED", Message: "Authentication required"},
			})
			return
		}
		if claims.Role == RoleAdmin {
			c.Next()
			return
		}
		projectID := c.Param("id")
		role, member, err := MembershipLookup(c.Request.Context(), pool, projectID, claims.CanonicalUserID())
		if err == nil && member && CanAdminProjectRole(role) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, forbiddenResponse)
	}
}

func appendUnique(values []string, v string) []string {
	for _, existing := range values {
		if existing == v {
			return values
		}
	}
	return append(values, v)
}
