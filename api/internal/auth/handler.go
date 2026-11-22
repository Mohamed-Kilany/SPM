package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"spm-platform/api/internal/audit"
)

// UserInvalidator revokes existing JWTs for a user. NATS KV is the production
// implementation; tests may pass nil.
type UserInvalidator interface {
	InvalidateUser(ctx context.Context, userID string) error
}

// SessionInvalidator publishes session-scoped evictions so every API replica
// closes the matching SSE stream. The events client implements it; tests may
// pass nil.
type SessionInvalidator interface {
	InvalidateSession(ctx context.Context, sessionID, userID, reason string) error
}

// LoginRequest is the JSON body for POST /auth/login.
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse is the JSON response for a successful login. RefreshToken and
// SessionID are absent only for legacy clients when the server runs without a
// session store.
type LoginResponse struct {
	Token        string       `json:"token"`
	RefreshToken string       `json:"refresh_token,omitempty"`
	SessionID    string       `json:"session_id,omitempty"`
	ExpiresIn    int          `json:"expires_in"` // seconds
	User         UserResponse `json:"user"`
}

// UserResponse is the safe public representation of a user. Projects carries
// the resolved project role and capability set so the UI can gate actions
// without guessing from the global role.
type UserResponse struct {
	ID          string          `json:"id"`
	Username    string          `json:"username"`
	Email       string          `json:"email"`
	DisplayName string          `json:"display_name"`
	AvatarURL   string          `json:"avatar_url"`
	Role        string          `json:"role"`
	ProjectIDs  []string        `json:"project_ids"`
	Projects    []ProjectAccess `json:"projects"`
}

// Handler holds the dependencies for auth HTTP handlers.
type Handler struct {
	db                 *pgxpool.Pool
	jwtSecret          string
	expiryHours        int
	ticketStore        *TicketStore
	invalidator        UserInvalidator
	sessions           *SessionStore
	sessionInvalidator SessionInvalidator
	audit              *audit.Logger
}

// SetSessions attaches the persistent session store and the cross-replica
// invalidation publisher. Without a store the API keeps issuing stateless
// tokens, which is only useful for tests and single-replica tools.
func (h *Handler) SetSessions(store *SessionStore, invalidator SessionInvalidator) {
	h.sessions = store
	h.sessionInvalidator = invalidator
}

// SetAuditLogger attaches the audit log. The API wires this at startup; tests
// and tools may leave it nil, in which case audit writes are no-ops.
func (h *Handler) SetAuditLogger(logger *audit.Logger) {
	h.audit = logger
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
			h.recordAuth(c, audit.Entry{
				ActorRole: "anonymous", Action: "auth.login", TargetType: "user",
				TargetID: req.Username, Result: "failure", Reason: "unknown_user",
			})
			abortUnauthorized(c, "INVALID_CREDENTIALS", "Invalid username or password")
			return
		}
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to look up user"},
		})
		return
	}

	if !active {
		h.recordAuth(c, audit.Entry{
			ActorID: userID, ActorRole: role, Action: "auth.login", TargetType: "user",
			TargetID: userID, Result: "denied", Reason: "account_disabled",
		})
		c.JSON(http.StatusForbidden, errorResponse{
			Error: errorBody{Code: "ACCOUNT_DISABLED", Message: "This account has been disabled"},
		})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		h.recordAuth(c, audit.Entry{
			ActorID: userID, ActorRole: role, Action: "auth.login", TargetType: "user",
			TargetID: userID, Result: "failure", Reason: "invalid_password",
		})
		abortUnauthorized(c, "INVALID_CREDENTIALS", "Invalid username or password")
		return
	}

	_, _ = h.db.Exec(ctx, `UPDATE users SET last_login_at = NOW() WHERE id = $1`, userID)

	projects, projectIDs, err := h.memberships(ctx, userID, role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to load project memberships"},
		})
		return
	}

	var (
		token        string
		refreshToken string
		sessionID    string
		expiresIn    = h.expiryHours * 3600
	)
	if h.sessions != nil {
		tokens, evicted, createErr := h.sessions.Create(ctx, userID, c.ClientIP(), c.Request.UserAgent())
		if createErr != nil {
			c.JSON(http.StatusInternalServerError, errorResponse{
				Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to create session"},
			})
			return
		}
		for _, evictedID := range evicted {
			h.invalidateSession(ctx, evictedID, userID, "session_limit")
		}
		token, err = IssueSessionToken(userID, role, projectIDs, h.jwtSecret,
			h.sessions.Config().AccessTTL, tokens.SessionID, tokens.JTI)
		refreshToken = tokens.RefreshToken
		sessionID = tokens.SessionID
		expiresIn = int(h.sessions.Config().AccessTTL.Seconds())
	} else {
		token, err = IssueToken(userID, role, projectIDs, h.jwtSecret, h.expiryHours)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to issue token"},
		})
		return
	}

	h.recordAuth(c, audit.Entry{
		ActorID: userID, ActorRole: role, Action: "auth.login", TargetType: "user",
		TargetID: userID, Result: "success",
	})

	c.JSON(http.StatusOK, LoginResponse{
		Token:        token,
		RefreshToken: refreshToken,
		SessionID:    sessionID,
		ExpiresIn:    expiresIn,
		User: UserResponse{
			ID:          userID,
			Username:    username,
			Email:       email,
			DisplayName: displayName,
			AvatarURL:   avatarURL,
			Role:        role,
			ProjectIDs:  projectIDs,
			Projects:    projects,
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

	projects, projectIDs, err := h.memberships(c.Request.Context(), userID, role)
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
		Projects:    projects,
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
	if h.sessions != nil {
		revoked, revokeErr := h.sessions.RevokeOthers(c.Request.Context(), userID, "", "password_change")
		if revokeErr != nil {
			// The password is already rotated and the KV invalidation is
			// applied; a cleanup failure must not report the change as failed.
			h.recordAuth(c, audit.Entry{
				ActorID: userID, ActorRole: claims.Role, Action: "auth.session.revoke_others",
				TargetType: "user", TargetID: userID, Result: "failure", Reason: "revoke_failed",
			})
		} else {
			for _, revokedID := range revoked {
				h.invalidateSession(c.Request.Context(), revokedID, userID, "password_change")
			}
		}
	}
	h.recordAuth(c, audit.Entry{
		ActorID: userID, ActorRole: claims.Role, Action: "auth.password.change",
		TargetType: "user", TargetID: userID, Result: "success",
	})
	c.Status(http.StatusNoContent)
}

// RefreshRequest is the JSON body for POST /auth/refresh.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Refresh handles POST /api/v1/auth/refresh. It rotates the refresh token and
// issues a new short-lived access token. Presenting an already rotated token
// revokes the whole session family.
func (h *Handler) Refresh(c *gin.Context) {
	if h.sessions == nil {
		c.JSON(http.StatusServiceUnavailable, errorResponse{
			Error: errorBody{Code: "SESSIONS_UNAVAILABLE", Message: "Session management is not enabled"},
		})
		return
	}
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{
			Error: errorBody{Code: "BAD_REQUEST", Message: "refresh_token is required"},
		})
		return
	}

	ctx := c.Request.Context()
	tokens, err := h.sessions.Rotate(ctx, req.RefreshToken, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		var reuse *RefreshReuseError
		switch {
		case errors.As(err, &reuse):
			h.invalidateSession(ctx, reuse.SessionID, reuse.UserID, "refresh_reuse")
			h.recordAuth(c, audit.Entry{
				ActorID: reuse.UserID, Action: "auth.refresh", TargetType: "session",
				TargetID: reuse.SessionID, Result: "denied", Reason: "refresh_reuse",
			})
			abortUnauthorized(c, "REFRESH_TOKEN_REUSE", "Refresh token reuse detected; sign in again")
		case errors.Is(err, ErrSessionRevoked):
			abortUnauthorized(c, "SESSION_REVOKED", "This session has been revoked")
		case errors.Is(err, ErrSessionExpired):
			abortUnauthorized(c, "SESSION_EXPIRED", "This session has expired")
		case errors.Is(err, ErrRefreshTokenInvalid):
			abortUnauthorized(c, "INVALID_REFRESH_TOKEN", "Refresh token is invalid")
		default:
			c.JSON(http.StatusInternalServerError, errorResponse{
				Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to refresh session"},
			})
		}
		return
	}

	user, active, err := h.userByID(ctx, tokens.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to load user"},
		})
		return
	}
	if !active {
		_, _ = h.sessions.RevokeOthers(ctx, tokens.UserID, "", "account_disabled")
		h.invalidateSession(ctx, tokens.SessionID, tokens.UserID, "account_disabled")
		c.JSON(http.StatusForbidden, errorResponse{
			Error: errorBody{Code: "ACCOUNT_DISABLED", Message: "This account has been disabled"},
		})
		return
	}

	token, err := IssueSessionToken(tokens.UserID, user.Role, user.ProjectIDs, h.jwtSecret,
		h.sessions.Config().AccessTTL, tokens.SessionID, tokens.JTI)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to issue token"},
		})
		return
	}

	h.recordAuth(c, audit.Entry{
		ActorID: tokens.UserID, ActorRole: user.Role, Action: "auth.refresh",
		TargetType: "session", TargetID: tokens.SessionID, Result: "success",
	})
	c.JSON(http.StatusOK, LoginResponse{
		Token:        token,
		RefreshToken: tokens.RefreshToken,
		SessionID:    tokens.SessionID,
		ExpiresIn:    int(h.sessions.Config().AccessTTL.Seconds()),
		User:         user,
	})
}

// Logout handles POST /api/v1/auth/logout. It revokes the current session and
// closes its SSE stream. Legacy stateless tokens fall back to user-wide
// invalidation.
func (h *Handler) Logout(c *gin.Context) {
	claims, ok := ClaimsFrom(c)
	if !ok {
		abortUnauthorized(c, "UNAUTHORIZED", "Authentication required")
		return
	}
	userID := claims.CanonicalUserID()
	ctx := c.Request.Context()

	if h.sessions != nil && claims.SessionID != "" {
		revoked, err := h.sessions.Revoke(ctx, userID, claims.SessionID, "logout")
		if err != nil {
			c.JSON(http.StatusInternalServerError, errorResponse{
				Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to revoke session"},
			})
			return
		}
		if revoked {
			h.invalidateSession(ctx, claims.SessionID, userID, "logout")
		}
	} else if h.invalidator != nil {
		_ = h.invalidator.InvalidateUser(ctx, userID)
	}

	h.recordAuth(c, audit.Entry{
		ActorID: userID, ActorRole: claims.Role, Action: "auth.logout",
		TargetType: "user", TargetID: userID, Result: "success",
	})
	c.Status(http.StatusNoContent)
}

// SessionResponse is the owner-visible projection of one active session.
type SessionResponse struct {
	ID            string    `json:"id"`
	Device        string    `json:"device"`
	IP            string    `json:"ip"`
	UserAgent     string    `json:"user_agent"`
	CreatedAt     time.Time `json:"created_at"`
	LastSeenAt    time.Time `json:"last_seen_at"`
	IdleExpiresAt time.Time `json:"idle_expires_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Current       bool      `json:"current"`
}

// SessionListResponse is the JSON response for GET /auth/sessions.
type SessionListResponse struct {
	Sessions []SessionResponse `json:"sessions"`
}

// ListSessions handles GET /api/v1/auth/sessions.
func (h *Handler) ListSessions(c *gin.Context) {
	claims, ok := ClaimsFrom(c)
	if !ok {
		abortUnauthorized(c, "UNAUTHORIZED", "Authentication required")
		return
	}
	if h.sessions == nil {
		c.JSON(http.StatusServiceUnavailable, errorResponse{
			Error: errorBody{Code: "SESSIONS_UNAVAILABLE", Message: "Session management is not enabled"},
		})
		return
	}
	sessions, err := h.sessions.List(c.Request.Context(), claims.CanonicalUserID())
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to list sessions"},
		})
		return
	}
	response := make([]SessionResponse, 0, len(sessions))
	for _, session := range sessions {
		response = append(response, SessionResponse{
			ID:            session.ID,
			Device:        session.Device,
			IP:            session.IP,
			UserAgent:     session.UserAgent,
			CreatedAt:     session.CreatedAt,
			LastSeenAt:    session.LastSeenAt,
			IdleExpiresAt: session.IdleExpiresAt,
			ExpiresAt:     session.ExpiresAt,
			Current:       session.ID == claims.SessionID,
		})
	}
	c.JSON(http.StatusOK, SessionListResponse{Sessions: response})
}

// RevokeSession handles DELETE /api/v1/auth/sessions/:sessionId.
func (h *Handler) RevokeSession(c *gin.Context) {
	claims, ok := ClaimsFrom(c)
	if !ok {
		abortUnauthorized(c, "UNAUTHORIZED", "Authentication required")
		return
	}
	if h.sessions == nil {
		c.JSON(http.StatusServiceUnavailable, errorResponse{
			Error: errorBody{Code: "SESSIONS_UNAVAILABLE", Message: "Session management is not enabled"},
		})
		return
	}
	userID := claims.CanonicalUserID()
	sessionID := strings.TrimSpace(c.Param("sessionId"))
	revoked, err := h.sessions.Revoke(c.Request.Context(), userID, sessionID, "user_revoked")
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to revoke session"},
		})
		return
	}
	if !revoked {
		c.JSON(http.StatusNotFound, errorResponse{
			Error: errorBody{Code: "SESSION_NOT_FOUND", Message: "Session not found"},
		})
		return
	}
	h.invalidateSession(c.Request.Context(), sessionID, userID, "user_revoked")
	h.recordAuth(c, audit.Entry{
		ActorID: userID, ActorRole: claims.Role, Action: "auth.session.revoke",
		TargetType: "session", TargetID: sessionID, Result: "success",
	})
	c.Status(http.StatusNoContent)
}

// RevokeOtherSessions handles POST /api/v1/auth/sessions/revoke-others. The
// current session is kept so the caller stays signed in.
func (h *Handler) RevokeOtherSessions(c *gin.Context) {
	claims, ok := ClaimsFrom(c)
	if !ok {
		abortUnauthorized(c, "UNAUTHORIZED", "Authentication required")
		return
	}
	if h.sessions == nil {
		c.JSON(http.StatusServiceUnavailable, errorResponse{
			Error: errorBody{Code: "SESSIONS_UNAVAILABLE", Message: "Session management is not enabled"},
		})
		return
	}
	userID := claims.CanonicalUserID()
	revoked, err := h.sessions.RevokeOthers(c.Request.Context(), userID, claims.SessionID, "user_revoked_others")
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to revoke sessions"},
		})
		return
	}
	for _, revokedID := range revoked {
		h.invalidateSession(c.Request.Context(), revokedID, userID, "user_revoked_others")
	}
	h.recordAuth(c, audit.Entry{
		ActorID: userID, ActorRole: claims.Role, Action: "auth.session.revoke_others",
		TargetType: "user", TargetID: userID, Result: "success",
		After: map[string]any{"revoked": len(revoked)},
	})
	c.JSON(http.StatusOK, gin.H{"revoked": len(revoked)})
}

// invalidateSession publishes a best-effort cross-replica eviction. The
// database row is already revoked, so the heartbeat fallback still closes the
// stream when NATS is unavailable.
func (h *Handler) invalidateSession(ctx context.Context, sessionID, userID, reason string) {
	if h.sessionInvalidator == nil || sessionID == "" {
		return
	}
	_ = h.sessionInvalidator.InvalidateSession(ctx, sessionID, userID, reason)
}

// userByID loads the public user projection and reports whether the account is
// active. Refresh uses it to rebuild claims after rotation.
func (h *Handler) userByID(ctx context.Context, userID string) (UserResponse, bool, error) {
	var (
		user   UserResponse
		active bool
	)
	err := h.db.QueryRow(ctx,
		`SELECT id::text, username, email, COALESCE(display_name, ''), COALESCE(avatar_url, ''), role, is_active
		 FROM users WHERE id = $1`, userID,
	).Scan(&user.ID, &user.Username, &user.Email, &user.DisplayName, &user.AvatarURL, &user.Role, &active)
	if err != nil {
		return UserResponse{}, false, err
	}
	projects, projectIDs, err := h.memberships(ctx, userID, user.Role)
	if err != nil {
		return UserResponse{}, false, err
	}
	user.Projects = projects
	user.ProjectIDs = projectIDs
	return user, active, nil
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
	h.recordAuth(c, audit.Entry{
		ActorID: claims.CanonicalUserID(), ActorRole: claims.Role, Action: "auth.stream_ticket.issue",
		TargetType: "user", TargetID: claims.CanonicalUserID(), Result: "success",
	})
	c.JSON(http.StatusOK, gin.H{"ticket": ticketID, "expires_in": 30})
}

// recordAuth writes an audit event without failing the request. The audit
// logger already logs write failures and exposes them as metrics.
func (h *Handler) recordAuth(c *gin.Context, entry audit.Entry) {
	if h.audit == nil {
		return
	}
	h.audit.RecordRequest(c, entry)
}

func (h *Handler) memberships(ctx context.Context, userID, globalRole string) ([]ProjectAccess, []string, error) {
	rows, err := h.db.Query(ctx,
		`SELECT pm.project_id, pm.role
		 FROM project_members pm
		 JOIN projects p ON p.id = pm.project_id
		 WHERE pm.user_id = $1 AND p.status = 'active'
		 ORDER BY pm.project_id`,
		userID,
	)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	projects := make([]ProjectAccess, 0)
	projectIDs := make([]string, 0)
	for rows.Next() {
		var projectID, role string
		if err := rows.Scan(&projectID, &role); err != nil {
			return nil, nil, err
		}
		projects = append(projects, ProjectAccess{
			ProjectID:    projectID,
			Role:         role,
			Capabilities: EffectiveCapabilities(globalRole, role),
		})
		projectIDs = append(projectIDs, projectID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return projects, projectIDs, nil
}
