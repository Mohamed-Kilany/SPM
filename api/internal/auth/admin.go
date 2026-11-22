package auth

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"spm-platform/api/internal/audit"
	"spm-platform/api/internal/models"
)

// AdminUser is the administrative view of a user.
type AdminUser struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	AvatarURL   string   `json:"avatar_url"`
	Role        string   `json:"role"`
	IsActive    bool     `json:"is_active"`
	ProjectIDs  []string `json:"project_ids"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// DirectoryUser is the minimal user directory projection used by member
// pickers. It intentionally excludes roles and account state.
type DirectoryUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

const adminUserSelect = `
	SELECT u.id::text, u.username, u.email, COALESCE(u.display_name, ''),
	       COALESCE(u.avatar_url, ''), u.role, u.is_active,
	       COALESCE((SELECT array_agg(pm.project_id::text ORDER BY pm.project_id)
	                 FROM project_members pm WHERE pm.user_id = u.id), '{}'),
	       u.created_at::text, u.updated_at::text
	FROM users u`

// AdminListUsers handles GET /admin/users.
func (h *Handler) AdminListUsers(c *gin.Context) {
	rows, err := h.db.Query(c.Request.Context(), adminUserSelect+` ORDER BY u.created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to list users"}})
		return
	}
	defer rows.Close()

	users := make([]AdminUser, 0)
	for rows.Next() {
		var u AdminUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.AvatarURL,
			&u.Role, &u.IsActive, &u.ProjectIDs, &u.CreatedAt, &u.UpdatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to scan user"}})
			return
		}
		users = append(users, u)
	}
	c.JSON(http.StatusOK, users)
}

type adminCreateUserRequest struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Password    string `json:"password"`
}

// AdminCreateUser handles POST /admin/users.
func (h *Handler) AdminCreateUser(c *gin.Context) {
	var req adminCreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "BAD_REQUEST", Message: "Request body is not valid JSON"}})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.Username == "" || len(req.Username) > 100 {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "Username is required and must be at most 100 characters"}})
		return
	}
	if req.Email == "" || !strings.Contains(req.Email, "@") || len(req.Email) > 255 {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "A valid email is required"}})
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = req.Username
	}
	if len(req.DisplayName) > 255 {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "Display name is too long"}})
		return
	}
	if !validGlobalRole(req.Role) {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "Role must be admin, member or viewer"}})
		return
	}
	if len(req.Password) < 10 || len(req.Password) > 128 {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "Password must be between 10 and 128 characters"}})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to hash password"}})
		return
	}
	var id uuid.UUID
	err = h.db.QueryRow(c.Request.Context(), `
		INSERT INTO users (username, email, display_name, password_hash, role)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		req.Username, req.Email, req.DisplayName, string(hash), req.Role).Scan(&id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			c.JSON(http.StatusConflict, errorResponse{Error: errorBody{Code: "USER_EXISTS", Message: "Username or email already exists"}})
			return
		}
		c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to create user"}})
		return
	}
	actorID, actorRole := "", ""
	if claims, ok := ClaimsFrom(c); ok && claims != nil {
		actorID, actorRole = claims.CanonicalUserID(), claims.Role
	}
	h.recordAuth(c, audit.Entry{
		ActorID: actorID, ActorRole: actorRole, Action: "admin.user.create",
		TargetType: "user", TargetID: id.String(), Result: "success",
		After: map[string]any{
			"username": req.Username, "email": req.Email,
			"display_name": req.DisplayName, "role": req.Role,
		},
	})
	c.JSON(http.StatusCreated, gin.H{"id": id.String()})
}

type adminUpdateUserRequest struct {
	Email       *string `json:"email"`
	DisplayName *string `json:"display_name"`
	AvatarURL   *string `json:"avatar_url"`
	Role        *string `json:"role"`
	IsActive    *bool   `json:"is_active"`
	Password    *string `json:"password"`
}

// AdminUpdateUser handles PATCH /admin/users/:userId.
func (h *Handler) AdminUpdateUser(c *gin.Context) {
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "INVALID_ID", Message: "User ID is not a valid UUID"}})
		return
	}
	claims, _ := ClaimsFrom(c)
	actorID := ""
	if claims != nil {
		actorID = claims.CanonicalUserID()
	}

	var req adminUpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "BAD_REQUEST", Message: "Request body is not valid JSON"}})
		return
	}

	var currentRole string
	var currentActive bool
	var currentEmail, currentDisplayName, currentAvatar string
	if err := h.db.QueryRow(c.Request.Context(),
		`SELECT role, is_active, email, COALESCE(display_name, ''), COALESCE(avatar_url, '')
		 FROM users WHERE id = $1`, targetID,
	).Scan(&currentRole, &currentActive, &currentEmail, &currentDisplayName, &currentAvatar); err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, errorResponse{Error: errorBody{Code: "NOT_FOUND", Message: "User not found"}})
			return
		}
		c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to load user"}})
		return
	}

	nextRole := currentRole
	nextActive := currentActive
	if req.Role != nil {
		if !validGlobalRole(*req.Role) {
			c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "Role must be admin, member or viewer"}})
			return
		}
		nextRole = *req.Role
	}
	if req.IsActive != nil {
		nextActive = *req.IsActive
	}
	if targetID.String() == actorID && (!nextActive || nextRole != RoleAdmin) {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "SELF_LOCKOUT", Message: "You cannot deactivate or demote your own admin account"}})
		return
	}
	if currentRole == RoleAdmin && (!nextActive || nextRole != RoleAdmin) {
		var remaining int
		if err := h.db.QueryRow(c.Request.Context(),
			`SELECT COUNT(*) FROM users WHERE role = 'admin' AND is_active = TRUE AND id <> $1`, targetID).Scan(&remaining); err != nil {
			c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to check admin count"}})
			return
		}
		if remaining == 0 {
			c.JSON(http.StatusConflict, errorResponse{Error: errorBody{Code: "LAST_ADMIN", Message: "At least one active admin must remain"}})
			return
		}
	}

	sets := make([]string, 0, 8)
	args := make([]interface{}, 0, 8)
	arg := func(v interface{}) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if req.Email != nil {
		email := strings.TrimSpace(strings.ToLower(*req.Email))
		if email == "" || !strings.Contains(email, "@") || len(email) > 255 {
			c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "A valid email is required"}})
			return
		}
		sets = append(sets, "email = "+arg(email))
	}
	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if name == "" || len(name) > 255 {
			c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "Display name is required and must be at most 255 characters"}})
			return
		}
		sets = append(sets, "display_name = "+arg(name))
	}
	if req.AvatarURL != nil {
		avatar := strings.TrimSpace(*req.AvatarURL)
		if len(avatar) > 2048 {
			c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "Avatar URL is too long"}})
			return
		}
		sets = append(sets, "avatar_url = "+arg(avatar))
	}
	if req.Role != nil {
		sets = append(sets, "role = "+arg(nextRole))
	}
	if req.IsActive != nil {
		sets = append(sets, "is_active = "+arg(nextActive))
	}
	if req.Password != nil {
		if len(*req.Password) < 10 || len(*req.Password) > 128 {
			c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "Password must be between 10 and 128 characters"}})
			return
		}
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if hashErr != nil {
			c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to hash password"}})
			return
		}
		sets = append(sets, "password_hash = "+arg(string(hash)), "invalidated_at = NOW()")
	}
	if len(sets) == 0 {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "No user fields supplied"}})
		return
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, targetID)
	query := "UPDATE users SET " + strings.Join(sets, ", ") + " WHERE id = $" + strconv.Itoa(len(args))
	if _, err := h.db.Exec(c.Request.Context(), query, args...); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			c.JSON(http.StatusConflict, errorResponse{Error: errorBody{Code: "EMAIL_IN_USE", Message: "That email address is already in use"}})
			return
		}
		c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to update user"}})
		return
	}
	if (req.Password != nil || req.Role != nil || req.IsActive != nil) && h.invalidator != nil {
		_ = h.invalidator.InvalidateUser(c.Request.Context(), targetID.String())
	}
	if req.Password != nil || req.Role != nil || req.IsActive != nil {
		if h.sessions != nil {
			if revoked, revokeErr := h.sessions.RevokeOthers(c.Request.Context(), targetID.String(), "", "admin_update"); revokeErr == nil {
				for _, revokedID := range revoked {
					h.invalidateSession(c.Request.Context(), revokedID, targetID.String(), "admin_update")
				}
			}
		}
	}
	var nextEmail, nextDisplayName, nextAvatar string
	if req.Email != nil || req.DisplayName != nil || req.AvatarURL != nil {
		_ = h.db.QueryRow(c.Request.Context(),
			`SELECT email, COALESCE(display_name, ''), COALESCE(avatar_url, '') FROM users WHERE id = $1`,
			targetID).Scan(&nextEmail, &nextDisplayName, &nextAvatar)
	} else {
		nextEmail, nextDisplayName, nextAvatar = currentEmail, currentDisplayName, currentAvatar
	}
	before := map[string]any{
		"email": currentEmail, "display_name": currentDisplayName,
		"avatar_url": currentAvatar, "role": currentRole, "is_active": currentActive,
	}
	after := map[string]any{
		"email": nextEmail, "display_name": nextDisplayName,
		"avatar_url": nextAvatar, "role": nextRole, "is_active": nextActive,
	}
	if req.Password != nil {
		// Record that the password rotated without ever storing its value.
		after["password_changed"] = true
	}
	h.recordAuth(c, audit.Entry{
		ActorID: actorID, ActorRole: claimsRole(claims), Action: "admin.user.update",
		TargetType: "user", TargetID: targetID.String(), Result: "success",
		Before: before, After: after,
	})
	c.JSON(http.StatusOK, gin.H{"id": targetID.String()})
}

// claimsRole returns the global role from optional claims for audit metadata.
func claimsRole(claims *models.AuthClaims) string {
	if claims == nil {
		return ""
	}
	return claims.Role
}

// Directory handles GET /users?q= for authenticated member pickers.
func (h *Handler) Directory(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	rows, err := h.db.Query(c.Request.Context(), `
		SELECT id::text, username, email, COALESCE(display_name, ''), COALESCE(avatar_url, '')
		FROM users
		WHERE is_active = TRUE
		  AND ($1 = '' OR username ILIKE '%' || $1 || '%' OR email ILIKE '%' || $1 || '%'
		       OR COALESCE(display_name, '') ILIKE '%' || $1 || '%')
		ORDER BY COALESCE(NULLIF(display_name, ''), username)
		LIMIT $2`, query, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to search users"}})
		return
	}
	defer rows.Close()
	users := make([]DirectoryUser, 0)
	for rows.Next() {
		var u DirectoryUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.AvatarURL); err != nil {
			c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to scan user"}})
			return
		}
		users = append(users, u)
	}
	c.JSON(http.StatusOK, users)
}

func validGlobalRole(role string) bool {
	return role == RoleAdmin || role == RoleMember || role == RoleViewer
}

// LoadAdminUser is used by tests and future admin detail endpoints.
func (h *Handler) LoadAdminUser(ctx context.Context, id string) (AdminUser, error) {
	var u AdminUser
	err := h.db.QueryRow(ctx, adminUserSelect+` WHERE u.id = $1`, id).Scan(
		&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.AvatarURL, &u.Role,
		&u.IsActive, &u.ProjectIDs, &u.CreatedAt, &u.UpdatedAt,
	)
	return u, err
}
