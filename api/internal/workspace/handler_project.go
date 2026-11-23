package workspace

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"spm-platform/api/internal/audit"
	"spm-platform/api/internal/auth"
)

var keyPrefixRE = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,11}$`)

type updateProjectRequest struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	KeyPrefix     *string `json:"key_prefix"`
	DefaultBranch *string `json:"default_branch"`
	Status        *string `json:"status"`
}

// UpdateProject handles PATCH /projects/:id. Project owners, maintainers and
// global admins may edit metadata; the change is auditable.
func (h *Handler) UpdateProject(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req updateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if req.Name == nil && req.Description == nil && req.KeyPrefix == nil && req.DefaultBranch == nil && req.Status == nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "No project fields supplied")
		return
	}

	sets := make([]string, 0, 6)
	args := make([]interface{}, 0, 7)
	arg := func(v interface{}) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 255 {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Project name is required and must be at most 255 characters")
			return
		}
		sets = append(sets, "name = "+arg(name))
	}
	if req.Description != nil {
		if len(*req.Description) > 4000 {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Description must be at most 4000 characters")
			return
		}
		sets = append(sets, "description = NULLIF("+arg(strings.TrimSpace(*req.Description))+", '')")
	}
	if req.KeyPrefix != nil {
		prefix := strings.ToUpper(strings.TrimSpace(*req.KeyPrefix))
		if !keyPrefixRE.MatchString(prefix) {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Key prefix must be 2-12 uppercase letters or digits and start with a letter")
			return
		}
		sets = append(sets, "key_prefix = "+arg(prefix))
	}
	if req.DefaultBranch != nil {
		branch := strings.TrimSpace(*req.DefaultBranch)
		if branch == "" || len(branch) > 255 {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Default branch is required")
			return
		}
		sets = append(sets, "default_branch = "+arg(branch))
	}
	statusArchived := false
	if req.Status != nil {
		status := strings.ToLower(strings.TrimSpace(*req.Status))
		if status != "active" && status != "archived" {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Status must be active or archived")
			return
		}
		statusArchived = status == "archived"
		sets = append(sets, "status = "+arg(status))
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, projectID)
	query := "UPDATE projects SET " + strings.Join(sets, ", ") + " WHERE id = $" + strconv.Itoa(len(args))
	tag, err := h.store.DB().Exec(c.Request.Context(), query, args...)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			apiError(c, http.StatusConflict, "CONFLICT", "That project key prefix is already in use")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update project")
		return
	}
	if tag.RowsAffected() == 0 {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
		return
	}
	if statusArchived {
		h.invalidateProjectSessions(c.Request.Context(), projectID, "project_archived")
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "project", &projectID, "updated", "Updated project settings", nil)
	fields := map[string]any{}
	if req.Name != nil {
		fields["name"] = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		fields["description"] = strings.TrimSpace(*req.Description)
	}
	if req.KeyPrefix != nil {
		fields["key_prefix"] = strings.ToUpper(strings.TrimSpace(*req.KeyPrefix))
	}
	if req.DefaultBranch != nil {
		fields["default_branch"] = strings.TrimSpace(*req.DefaultBranch)
	}
	if req.Status != nil {
		fields["status"] = strings.ToLower(strings.TrimSpace(*req.Status))
	}
	h.recordAudit(c, projectID, audit.Entry{
		Action: "project.update", TargetType: "project", TargetID: projectID.String(),
		Result: "success", After: fields,
	})
	c.JSON(http.StatusOK, gin.H{"id": projectID})
}

type memberRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// ListMembers handles GET /projects/:id/members.
func (h *Handler) ListMembers(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	w, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
		return
	}
	c.JSON(http.StatusOK, w.Members)
}

// AddMember handles POST /projects/:id/members.
func (h *Handler) AddMember(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req memberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	userID, err := uuid.Parse(strings.TrimSpace(req.UserID))
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "user_id must be a UUID")
		return
	}
	role := strings.ToLower(strings.TrimSpace(req.Role))
	if !auth.ValidProjectRole(role) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid project role")
		return
	}
	if role == auth.ProjectRoleOwner && !canGrantOwnership(c) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Only an owner or a global admin can grant ownership")
		return
	}
	var active bool
	if err := h.store.DB().QueryRow(c.Request.Context(), `SELECT is_active FROM users WHERE id = $1`, userID).Scan(&active); err != nil {
		if err == pgx.ErrNoRows {
			apiError(c, http.StatusNotFound, "NOT_FOUND", "User not found")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load user")
		return
	}
	if !active {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Cannot add a disabled user")
		return
	}

	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start membership transaction")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	demotedOwners := make([]uuid.UUID, 0, 1)
	if role == auth.ProjectRoleOwner {
		rows, qerr := tx.Query(c.Request.Context(),
			`UPDATE project_members SET role = 'maintainer', updated_at = NOW()
			 WHERE project_id = $1 AND role = 'owner' AND user_id <> $2
			 RETURNING user_id`, projectID, userID)
		if qerr != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to transfer ownership")
			return
		}
		for rows.Next() {
			var demoted uuid.UUID
			if err := rows.Scan(&demoted); err != nil {
				rows.Close()
				apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to transfer ownership")
				return
			}
			demotedOwners = append(demotedOwners, demoted)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to transfer ownership")
			return
		}
	}
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO project_members (project_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role, updated_at = NOW()`,
		projectID, userID, role); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to add project member")
		return
	}
	if role == auth.ProjectRoleOwner {
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE projects SET owner_id = $1, updated_at = NOW() WHERE id = $2`, userID, projectID); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to transfer ownership")
			return
		}
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit membership")
		return
	}
	h.invalidateMemberSessions(c.Request.Context(), userID, projectID, "member_added")
	for _, demoted := range demotedOwners {
		h.invalidateMemberSessions(c.Request.Context(), demoted, projectID, "ownership_transferred")
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "project_member", &userID, "added", "Added project member", map[string]interface{}{"role": role})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "project.member.add", TargetType: "user", TargetID: userID.String(),
		Result: "success", After: map[string]any{"role": role, "ownership_transferred": len(demotedOwners) > 0},
	})
	c.JSON(http.StatusCreated, gin.H{"user_id": userID, "role": role})
}

// UpdateMember handles PATCH /projects/:id/members/:userId.
func (h *Handler) UpdateMember(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	userID, ok := parseParamID(c, "userId")
	if !ok {
		return
	}
	var req memberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	role := strings.ToLower(strings.TrimSpace(req.Role))
	if !auth.ValidProjectRole(role) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid project role")
		return
	}

	var currentRole string
	if err := h.store.DB().QueryRow(c.Request.Context(),
		`SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID).Scan(&currentRole); err != nil {
		if err == pgx.ErrNoRows {
			apiError(c, http.StatusNotFound, "NOT_FOUND", "Project member not found")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load member")
		return
	}
	if (currentRole == auth.ProjectRoleOwner || role == auth.ProjectRoleOwner) && !canGrantOwnership(c) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Only an owner or a global admin can change ownership")
		return
	}
	if currentRole == auth.ProjectRoleOwner && role != auth.ProjectRoleOwner {
		var otherOwners int
		if err := h.store.DB().QueryRow(c.Request.Context(),
			`SELECT COUNT(*) FROM project_members WHERE project_id = $1 AND role = 'owner' AND user_id <> $2`,
			projectID, userID).Scan(&otherOwners); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to check ownership")
			return
		}
		if otherOwners == 0 {
			apiError(c, http.StatusConflict, "LAST_OWNER", "A project must retain an owner")
			return
		}
	}
	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start membership transaction")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()
	demotedOwners := make([]uuid.UUID, 0, 1)
	if role == auth.ProjectRoleOwner {
		rows, qerr := tx.Query(c.Request.Context(),
			`UPDATE project_members SET role = 'maintainer', updated_at = NOW()
			 WHERE project_id = $1 AND role = 'owner' AND user_id <> $2
			 RETURNING user_id`, projectID, userID)
		if qerr != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to transfer ownership")
			return
		}
		for rows.Next() {
			var demoted uuid.UUID
			if err := rows.Scan(&demoted); err != nil {
				rows.Close()
				apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to transfer ownership")
				return
			}
			demotedOwners = append(demotedOwners, demoted)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to transfer ownership")
			return
		}
	}
	if _, err := tx.Exec(c.Request.Context(),
		`UPDATE project_members SET role = $3, updated_at = NOW() WHERE project_id = $1 AND user_id = $2`,
		projectID, userID, role); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update member")
		return
	}
	if role == auth.ProjectRoleOwner {
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE projects SET owner_id = $1, updated_at = NOW() WHERE id = $2`, userID, projectID); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to transfer ownership")
			return
		}
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to commit membership")
		return
	}
	h.invalidateMemberSessions(c.Request.Context(), userID, projectID, "member_role_changed")
	for _, demoted := range demotedOwners {
		h.invalidateMemberSessions(c.Request.Context(), demoted, projectID, "ownership_transferred")
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "project_member", &userID, "updated", "Updated project member role", map[string]interface{}{"role": role})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "project.member.update", TargetType: "user", TargetID: userID.String(),
		Result: "success", Before: map[string]any{"role": currentRole},
		After: map[string]any{"role": role, "ownership_transferred": len(demotedOwners) > 0},
	})
	c.JSON(http.StatusOK, gin.H{"user_id": userID, "role": role})
}

// RemoveMember handles DELETE /projects/:id/members/:userId.
func (h *Handler) RemoveMember(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	userID, ok := parseParamID(c, "userId")
	if !ok {
		return
	}
	var role string
	if err := h.store.DB().QueryRow(c.Request.Context(),
		`SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID).Scan(&role); err != nil {
		if err == pgx.ErrNoRows {
			apiError(c, http.StatusNotFound, "NOT_FOUND", "Project member not found")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load member")
		return
	}
	if role == auth.ProjectRoleOwner && !canGrantOwnership(c) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Only an owner or a global admin can remove an owner")
		return
	}
	if role == auth.ProjectRoleOwner {
		var owners int
		if err := h.store.DB().QueryRow(c.Request.Context(),
			`SELECT COUNT(*) FROM project_members WHERE project_id = $1 AND role = 'owner'`, projectID).Scan(&owners); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to check ownership")
			return
		}
		if owners <= 1 {
			apiError(c, http.StatusConflict, "LAST_OWNER", "A project must retain an owner")
			return
		}
	}
	tag, err := h.store.DB().Exec(c.Request.Context(),
		`DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to remove member")
		return
	}
	if tag.RowsAffected() == 0 {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Project member not found")
		return
	}
	h.invalidateMemberSessions(c.Request.Context(), userID, projectID, "member_removed")
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "project_member", &userID, "removed", "Removed project member", nil)
	h.recordAudit(c, projectID, audit.Entry{
		Action: "project.member.remove", TargetType: "user", TargetID: userID.String(),
		Result: "success", Before: map[string]any{"role": role},
	})
	c.Status(http.StatusNoContent)
}

// invalidateMemberSessions makes a membership change effective beyond the
// database lookup: the target's JWTs are rejected on the next request and every
// replica evicts their live SSE streams. Failures are logged but do not fail the
// committed membership change; the per-request database lookup still enforces
// the new role.
func (h *Handler) invalidateMemberSessions(ctx context.Context, userID, projectID uuid.UUID, reason string) {
	if h.events == nil {
		return
	}
	if err := h.events.InvalidateMembership(ctx, userID.String(), projectID.String(), reason); err != nil {
		slog.Warn("authz_invalidation_failed",
			"scope", "membership",
			"reason", reason,
			"user_id", userID.String(),
			"project_id", projectID.String(),
			"error", err,
		)
	}
}

// invalidateProjectSessions closes every replica's SSE streams after a project
// is archived. Access itself is denied by the membership resolver, which reads
// the project status on every request.
func (h *Handler) invalidateProjectSessions(ctx context.Context, projectID uuid.UUID, reason string) {
	if h.events == nil {
		return
	}
	if err := h.events.InvalidateProject(ctx, projectID.String(), reason); err != nil {
		slog.Warn("authz_invalidation_failed",
			"scope", "project",
			"reason", reason,
			"project_id", projectID.String(),
			"error", err,
		)
	}
}

// canGrantOwnership reports whether the actor may grant, change or revoke the
// owner role. Maintainers hold member.write but must not be able to promote
// themselves or anyone else to owner.
func canGrantOwnership(c *gin.Context) bool {
	role := auth.ProjectRoleFrom(c)
	return role == auth.ProjectRoleOwner || role == auth.RoleAdmin
}
