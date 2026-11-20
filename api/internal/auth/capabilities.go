package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/observability"
)

// Capability is one explicit authorization verb attached to project routes.
// Capabilities are additive and are always derived from the project role
// stored in PostgreSQL; they are never embedded in the JWT.
type Capability string

const (
	CapProjectRead      Capability = "project.read"
	CapPlanningWrite    Capability = "planning.write"
	CapWorkItemWrite    Capability = "work_item.write"
	CapTestWrite        Capability = "test.write"
	CapCodeRead         Capability = "code.read"
	CapCodeWrite        Capability = "code.write"
	CapPRReview         Capability = "pr.review"
	CapPRMerge          Capability = "pr.merge"
	CapDeliveryRead     Capability = "delivery.read"
	CapDeliveryDeploy   Capability = "delivery.deploy"
	CapDeliveryRollback Capability = "delivery.rollback"
	CapSettingsWrite    Capability = "settings.write"
	CapMemberWrite      Capability = "member.write"
	CapAuditRead        Capability = "audit.read"
)

// AllCapabilities is the canonical capability order used by /auth/me, tests
// and documentation.
var AllCapabilities = []Capability{
	CapProjectRead,
	CapPlanningWrite,
	CapWorkItemWrite,
	CapTestWrite,
	CapCodeRead,
	CapCodeWrite,
	CapPRReview,
	CapPRMerge,
	CapDeliveryRead,
	CapDeliveryDeploy,
	CapDeliveryRollback,
	CapSettingsWrite,
	CapMemberWrite,
	CapAuditRead,
}

var readOnlyCapabilities = map[Capability]bool{
	CapProjectRead:  true,
	CapCodeRead:     true,
	CapDeliveryRead: true,
}

// roleCapabilities is the canonical authorization matrix. Keep it in sync with
// docs/AUTHZ.md; the table-driven tests fail if a role is missing.
var roleCapabilities = map[string]map[Capability]bool{
	ProjectRoleOwner:      capabilitySet(AllCapabilities...),
	ProjectRoleMaintainer: capabilitySet(AllCapabilities...),
	ProjectRoleDeveloper: capabilitySet(
		CapProjectRead,
		CapPlanningWrite,
		CapWorkItemWrite,
		CapTestWrite,
		CapCodeRead,
		CapCodeWrite,
		CapPRReview,
		CapDeliveryRead,
		CapDeliveryDeploy,
	),
	ProjectRoleTester: capabilitySet(
		CapProjectRead,
		CapWorkItemWrite,
		CapTestWrite,
		CapCodeRead,
		CapDeliveryRead,
	),
	ProjectRoleViewer: capabilitySet(
		CapProjectRead,
		CapCodeRead,
		CapDeliveryRead,
	),
}

func capabilitySet(capabilities ...Capability) map[Capability]bool {
	set := make(map[Capability]bool, len(capabilities))
	for _, capability := range capabilities {
		set[capability] = true
	}
	return set
}

// ValidCapability reports whether capability is part of the canonical set.
func ValidCapability(capability Capability) bool {
	for _, candidate := range AllCapabilities {
		if capability == candidate {
			return true
		}
	}
	return false
}

// CapabilitiesForRole returns the capabilities granted by one project role in
// canonical order. Unknown roles receive no capabilities.
func CapabilitiesForRole(role string) []Capability {
	granted := roleCapabilities[role]
	capabilities := make([]Capability, 0, len(granted))
	for _, capability := range AllCapabilities {
		if granted[capability] {
			capabilities = append(capabilities, capability)
		}
	}
	return capabilities
}

// RoleHasCapability reports whether a project role grants capability.
func RoleHasCapability(role string, capability Capability) bool {
	return roleCapabilities[role][capability]
}

// EffectiveCapabilities applies the global role on top of the project role.
// Global admins keep break-glass access to every capability. Global viewers
// are read-only even if a project membership would otherwise grant writes.
func EffectiveCapabilities(globalRole, projectRole string) []Capability {
	if globalRole == RoleAdmin {
		return append([]Capability(nil), AllCapabilities...)
	}
	if globalRole != RoleMember && globalRole != RoleViewer {
		return nil
	}
	granted := roleCapabilities[projectRole]
	if len(granted) == 0 {
		return nil
	}
	capabilities := make([]Capability, 0, len(granted))
	for _, capability := range AllCapabilities {
		if !granted[capability] {
			continue
		}
		if globalRole == RoleViewer && !readOnlyCapabilities[capability] {
			continue
		}
		capabilities = append(capabilities, capability)
	}
	return capabilities
}

// ProjectAccess is the resolved authorization context for one project. It is
// attached to the Gin context by RequireProjectCapability and returned per
// project by /auth/me.
type ProjectAccess struct {
	ProjectID    string       `json:"project_id"`
	Role         string       `json:"role"`
	Capabilities []Capability `json:"capabilities"`
}

// Allows reports whether the resolved access grants capability.
func (a ProjectAccess) Allows(capability Capability) bool {
	for _, granted := range a.Capabilities {
		if granted == capability {
			return true
		}
	}
	return false
}

const projectAccessKey = "spm.project_access"

// ProjectAccessFrom returns the authorization context attached by
// RequireProjectCapability.
func ProjectAccessFrom(c *gin.Context) (ProjectAccess, bool) {
	raw, exists := c.Get(projectAccessKey)
	if !exists {
		return ProjectAccess{}, false
	}
	access, ok := raw.(ProjectAccess)
	return access, ok
}

// ProjectRoleFrom returns the resolved project role for the current route. A
// global admin is reported as "admin" because it is break-glass, not a
// membership role.
func ProjectRoleFrom(c *gin.Context) string {
	access, ok := ProjectAccessFrom(c)
	if !ok {
		return ""
	}
	return access.Role
}

// HasProjectCapability reports whether the current route's resolved access
// grants capability. Handlers use it for conditional rules that a route-level
// middleware cannot express, such as tester defect-only work item creation.
func HasProjectCapability(c *gin.Context, capability Capability) bool {
	access, ok := ProjectAccessFrom(c)
	return ok && access.Allows(capability)
}

// MembershipResolver resolves the authoritative project role for a user. The
// PostgreSQL implementation is PoolMembershipResolver; tests may supply a
// deterministic fake.
type MembershipResolver interface {
	RoleForProject(ctx context.Context, projectID, userID string) (role string, member bool, err error)
}

// PoolMembershipResolver reads project_members on every request.
type PoolMembershipResolver struct {
	Pool *pgxpool.Pool
}

// RoleForProject implements MembershipResolver.
func (r PoolMembershipResolver) RoleForProject(ctx context.Context, projectID, userID string) (string, bool, error) {
	return MembershipLookup(ctx, r.Pool, projectID, userID)
}

// RequireProjectCapability resolves the caller's project membership from
// PostgreSQL on every request and enforces one capability. The resolved
// capability set is stored on the request for downstream handlers and for
// /auth/me. Global admins bypass the membership lookup and are logged so their
// break-glass use is auditable once GOV-04 lands.
func RequireProjectCapability(pool *pgxpool.Pool, capability Capability) gin.HandlerFunc {
	return RequireProjectCapabilityWithResolver(PoolMembershipResolver{Pool: pool}, capability)
}

// RequireProjectCapabilityWithResolver is the testable form of
// RequireProjectCapability.
func RequireProjectCapabilityWithResolver(resolver MembershipResolver, capability Capability) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFrom(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse{
				Error: errorBody{Code: "UNAUTHORIZED", Message: "Authentication required"},
			})
			return
		}
		projectID := c.Param("id")
		if projectID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, errorResponse{
				Error: errorBody{Code: "INVALID_PROJECT", Message: "Project ID is required"},
			})
			return
		}

		if claims.Role == RoleAdmin {
			access := ProjectAccess{
				ProjectID:    projectID,
				Role:         RoleAdmin,
				Capabilities: append([]Capability(nil), AllCapabilities...),
			}
			// The project ID and request path are caller-controlled. Strip CR
			// and LF before logging so a request cannot forge log entries
			// (CWE-117).
			safeProjectID := strings.ReplaceAll(projectID, "\n", "")
			safeProjectID = strings.ReplaceAll(safeProjectID, "\r", "")
			safePath := strings.ReplaceAll(c.Request.URL.Path, "\n", "")
			safePath = strings.ReplaceAll(safePath, "\r", "")
			slog.Info("authz_break_glass",
				"request_id", observability.RequestIDFrom(c),
				"user_id", claims.CanonicalUserID(),
				"project_id", safeProjectID,
				"capability", string(capability),
				"method", c.Request.Method,
				"path", safePath,
			)
			c.Set(projectAccessKey, access)
			c.Next()
			return
		}

		if resolver == nil {
			denyCapability(c, claims.CanonicalUserID(), projectID, "", capability, "membership resolver unavailable")
			return
		}
		role, member, err := resolver.RoleForProject(c.Request.Context(), projectID, claims.CanonicalUserID())
		if err != nil {
			safeProjectID := strings.ReplaceAll(projectID, "\n", "")
			safeProjectID = strings.ReplaceAll(safeProjectID, "\r", "")
			slog.Error("authz_membership_lookup_failed",
				"request_id", observability.RequestIDFrom(c),
				"user_id", claims.CanonicalUserID(),
				"project_id", safeProjectID,
				"error", err,
			)
			denyCapability(c, claims.CanonicalUserID(), projectID, "", capability, "membership lookup failed")
			return
		}
		if !member {
			denyCapability(c, claims.CanonicalUserID(), projectID, "", capability, "not a project member")
			return
		}

		capabilities := EffectiveCapabilities(claims.Role, role)
		access := ProjectAccess{ProjectID: projectID, Role: role, Capabilities: capabilities}
		if !access.Allows(capability) {
			denyCapability(c, claims.CanonicalUserID(), projectID, role, capability, "role does not grant capability")
			return
		}

		c.Set(projectAccessKey, access)
		c.Next()
	}
}

func denyCapability(c *gin.Context, userID, projectID, role string, capability Capability, reason string) {
	// Project IDs and paths come from the request. Strip CR and LF so a caller
	// cannot forge log entries (CWE-117).
	safeProjectID := strings.ReplaceAll(projectID, "\n", "")
	safeProjectID = strings.ReplaceAll(safeProjectID, "\r", "")
	safePath := strings.ReplaceAll(c.Request.URL.Path, "\n", "")
	safePath = strings.ReplaceAll(safePath, "\r", "")
	slog.Warn("authz_denied",
		"request_id", observability.RequestIDFrom(c),
		"user_id", userID,
		"project_id", safeProjectID,
		"project_role", role,
		"capability", string(capability),
		"reason", reason,
		"method", c.Request.Method,
		"path", safePath,
	)
	c.AbortWithStatusJSON(http.StatusForbidden, errorResponse{
		Error: errorBody{
			Code:    "FORBIDDEN",
			Message: "This action requires the " + string(capability) + " capability",
		},
	})
}

// MembershipLookup resolves a user's project membership directly from
// PostgreSQL. A missing row is not an error; a database failure is, so the
// caller can fail closed and log the cause.
func MembershipLookup(ctx context.Context, pool *pgxpool.Pool, projectID, userID string) (role string, member bool, err error) {
	if pool == nil {
		return "", false, nil
	}
	const q = `SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`
	if err := pool.QueryRow(ctx, q, projectID, userID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	if !ValidProjectRole(role) {
		return "", false, errors.New("project member has an unsupported role")
	}
	return role, true, nil
}
