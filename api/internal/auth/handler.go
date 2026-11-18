package auth

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// UserInvalidator revokes existing JWTs for a user. NATS KV is the production
// implementation; tests may pass nil.
type UserInvalidator interface {
	InvalidateUser(ctx context.Context, userID string) error
}

// LoginRequest is the JSON body for POST /auth/login.
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse is the JSON response for a successful login.
type LoginResponse struct {
	Token     string       `json:"token"`
	ExpiresIn int          `json:"expires_in"` // seconds
	User      UserResponse `json:"user"`
}

// UserResponse is the safe public representation of a user.
type UserResponse struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	AvatarURL   string   `json:"avatar_url"`
	Role        string   `json:"role"`
	ProjectIDs  []string `json:"project_ids"`
}

// Handler holds the dependencies for auth HTTP handlers.
type Handler struct {
	db          *pgxpool.Pool
	jwtSecret   string
	expiryHours int
	ticketStore *TicketStore
	invalidator UserInvalidator
}

// NewHandler creates a Handler. Reads JWT_SECRET (required) and
// JWT_EXPIRY_HOURS (default 8) from environment variables.
func NewHandler(db *pgxpool.Pool, ticketStore *TicketStore, invalidator UserInvalidator) *Handler {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		panic("auth: JWT_SECRET environment variable must be set")
	}

	expiryHours := 8
	if raw := os.Getenv("JWT_EXPIRY_HOURS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			expiryHours = n
		}
	}

	return &Handler{
		db:          db,
		jwtSecret:   secret,
		expiryHours: expiryHours,
		ticketStore: ticketStore,
		invalidator: invalidator,
	}
}

// Login handles POST /auth/login.
func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{
			Error: errorBody{Code: "BAD_REQUEST", Message: "username and password are required"},
		})
		return
	}

	ctx := c.Request.Context()

	var (
		userID       string
		username     string
		email        string
		displayName  string
		avatarURL    string
		passwordHash string
		role         string
		active       bool
	)
	err := h.db.QueryRow(ctx,
		`SELECT id, username, email, COALESCE(display_name, ''), COALESCE(avatar_url, ''),
		        password_hash, role, is_active
		 FROM users WHERE username = $1`,
		req.Username,
	).Scan(&userID, &username, &email, &displayName, &avatarURL, &passwordHash, &role, &active)

	if err != nil {
		if err == pgx.ErrNoRows {
			abortUnauthorized(c, "INVALID_CREDENTIALS", "Invalid username or password")
			return
		}
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to look up user"},
		})
		return
	}

	if !active {
		c.JSON(http.StatusForbidden, errorResponse{
			Error: errorBody{Code: "ACCOUNT_DISABLED", Message: "This account has been disabled"},
		})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		abortUnauthorized(c, "INVALID_CREDENTIALS", "Invalid username or password")
		return
	}

	_, _ = h.db.Exec(ctx, `UPDATE users SET last_login_at = NOW() WHERE id = $1`, userID)

	projectIDs, err := h.projectIDs(ctx, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to load project memberships"},
		})
		return
	}

	token, err := IssueToken(userID, role, projectIDs, h.jwtSecret, h.expiryHours)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to issue token"},
		})
		return
	}

	c.JSON(http.StatusOK, LoginResponse{
		Token:     token,
		ExpiresIn: h.expiryHours * 3600,
		User: UserResponse{
			ID:          userID,
			Username:    username,
			Email:       email,
			DisplayName: displayName,
			AvatarURL:   avatarURL,
			Role:        role,
			ProjectIDs:  projectIDs,
		},
	})
}

// Me returns the current authenticated principal and fresh memberships.
func (h *Handler) Me(c *gin.Context) {
	claims, ok := ClaimsFrom(c)
	if !ok {
		abortUnauthorized(c, "UNAUTHORIZED", "Authentication required")
		return
	}
	userID := claims.CanonicalUserID()

	var (
		username    string
		email       string
		displayName string
		avatarURL   string
		role        string
		active      bool
	)
	if err := h.db.QueryRow(c.Request.Context(),
		`SELECT username, email, COALESCE(display_name, ''), COALESCE(avatar_url, ''), role, is_active
		 FROM users WHERE id = $1`, userID,
	).Scan(&username, &email, &displayName, &avatarURL, &role, &active); err != nil {
		if err == pgx.ErrNoRows {
			abortUnauthorized(c, "UNAUTHORIZED", "User no longer exists")
			return
		}
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to load user"},
		})
		return
	}
	if !active {
		abortUnauthorized(c, "ACCOUNT_DISABLED", "This account has been disabled")
		return
	}

	projectIDs, err := h.projectIDs(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to load project memberships"},
		})
		return
	}

	c.JSON(http.StatusOK, UserResponse{
		ID:          userID,
		Username:    username,
		Email:       email,
		DisplayName: displayName,
		AvatarURL:   avatarURL,
		Role:        role,
		ProjectIDs:  projectIDs,
	})
}

type updateProfileRequest struct {
	DisplayName *string `json:"display_name"`
	Email       *string `json:"email"`
	AvatarURL   *string `json:"avatar_url"`
}

// UpdateMe updates the authenticated user's profile.
func (h *Handler) UpdateMe(c *gin.Context) {
	claims, ok := ClaimsFrom(c)
	if !ok {
		abortUnauthorized(c, "UNAUTHORIZED", "Authentication required")
		return
	}
	userID := claims.CanonicalUserID()

	var req updateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "BAD_REQUEST", Message: "Request body is not valid JSON"}})
		return
	}

	sets := make([]string, 0, 4)
	args := make([]interface{}, 0, 5)
	arg := func(value interface{}) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}

	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if name == "" || len(name) > 255 {
			c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "Display name is required and must be at most 255 characters"}})
			return
		}
		sets = append(sets, "display_name = "+arg(name))
	}
	if req.Email != nil {
		email := strings.TrimSpace(strings.ToLower(*req.Email))
		if email == "" || len(email) > 255 || !strings.Contains(email, "@") {
			c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "A valid email is required"}})
			return
		}
		sets = append(sets, "email = "+arg(email))
	}
	if req.AvatarURL != nil {
		avatar := strings.TrimSpace(*req.AvatarURL)
		if len(avatar) > 2048 {
			c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "Avatar URL is too long"}})
			return
		}
		sets = append(sets, "avatar_url = "+arg(avatar))
	}
	if len(sets) == 0 {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "No profile fields supplied"}})
		return
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, userID)
	query := "UPDATE users SET " + strings.Join(sets, ", ") + " WHERE id = $" + strconv.Itoa(len(args))
	if _, err := h.db.Exec(c.Request.Context(), query, args...); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			c.JSON(http.StatusConflict, errorResponse{Error: errorBody{Code: "EMAIL_IN_USE", Message: "That email address is already in use"}})
			return
		}
		c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to update profile"}})
		return
	}

	h.Me(c)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword verifies the current password, rotates the hash and revokes
// existing sessions through the invalidation KV.
func (h *Handler) ChangePassword(c *gin.Context) {
	claims, ok := ClaimsFrom(c)
	if !ok {
		abortUnauthorized(c, "UNAUTHORIZED", "Authentication required")
		return
	}
	userID := claims.CanonicalUserID()

	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "BAD_REQUEST", Message: "Current and new passwords are required"}})
		return
	}
	if len(req.NewPassword) < 10 || len(req.NewPassword) > 128 {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "New password must be between 10 and 128 characters"}})
		return
	}

	var hash string
	if err := h.db.QueryRow(c.Request.Context(), `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash); err != nil {
		abortUnauthorized(c, "UNAUTHORIZED", "User no longer exists")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.CurrentPassword)); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "INVALID_PASSWORD", Message: "Current password is incorrect"}})
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to hash password"}})
		return
	}
	if _, err := h.db.Exec(c.Request.Context(),
		`UPDATE users SET password_hash = $1, invalidated_at = NOW(), updated_at = NOW() WHERE id = $2`,
		string(newHash), userID); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to change password"}})
		return
	}
	if h.invalidator != nil {
		_ = h.invalidator.InvalidateUser(c.Request.Context(), userID)
	}
	c.Status(http.StatusNoContent)
}

// StreamTicket handles POST /api/v1/auth/stream-ticket. JWT middleware runs
// upstream, so claims are already in the Gin context.
func (h *Handler) StreamTicket(c *gin.Context) {
	claims, ok := ClaimsFrom(c)
	if !ok {
		abortUnauthorized(c, "UNAUTHORIZED", "Authentication required")
		return
	}
	ticketID, err := h.ticketStore.IssueStreamTicket(*claims)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to issue stream ticket"},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ticket": ticketID, "expires_in": 30})
}

func (h *Handler) projectIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := h.db.Query(ctx,
		`SELECT project_id FROM project_members WHERE user_id = $1 ORDER BY project_id`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	projectIDs := make([]string, 0)
	for rows.Next() {
		var pid string
		if err := rows.Scan(&pid); err != nil {
			return nil, err
		}
		projectIDs = append(projectIDs, pid)
	}
	return projectIDs, rows.Err()
}
