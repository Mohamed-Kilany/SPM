package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
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

var forbiddenResponse = errorResponse{
	Error: errorBody{Code: "FORBIDDEN", Message: "Insufficient permissions"},
}

// RequireRole returns a middleware that allows only the listed global roles.
// It is used for project creation and global administration, which are not
// project-scoped.
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
