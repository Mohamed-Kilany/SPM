package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/metrics"
)

const (
	defaultAccessTTL        = 15 * time.Minute
	defaultRefreshTTL       = 30 * 24 * time.Hour
	defaultIdleTimeout      = 24 * time.Hour
	defaultSessionLimit     = 20
	defaultSessionRetention = 30 * 24 * time.Hour

	// Last-seen writes are throttled: a busy client must not turn every request
	// into a session-row update.
	sessionTouchInterval = 30 * time.Second
	maxUserAgentLength   = 1024
)

// SessionConfig is the session policy resolved from the environment.
type SessionConfig struct {
	// AccessTTL is the lifetime of one access token.
	AccessTTL time.Duration
	// RefreshTTL is the absolute lifetime of a session family.
	RefreshTTL time.Duration
	// IdleTimeout is the inactivity window. Each validated request extends it,
	// capped by RefreshTTL.
	IdleTimeout time.Duration
	// LimitPerUser caps concurrent active sessions. Zero disables the cap.
	LimitPerUser int
	// Retention is how long revoked or expired sessions are kept for audit and
	// reuse detection before cleanup.
	Retention time.Duration
}

// SessionConfigFromEnv reads the session policy. ACCESS_TOKEN_TTL_MINUTES wins
// over the legacy JWT_EXPIRY_HOURS; when neither is set the access token lives
// for 15 minutes.
func SessionConfigFromEnv() SessionConfig {
	return sessionConfigFromLookup(os.Getenv)
}

func sessionConfigFromLookup(get func(string) string) SessionConfig {
	cfg := SessionConfig{
		AccessTTL:    defaultAccessTTL,
		RefreshTTL:   defaultRefreshTTL,
		IdleTimeout:  defaultIdleTimeout,
		LimitPerUser: defaultSessionLimit,
		Retention:    defaultSessionRetention,
	}
	if minutes, ok := positiveInt(get("ACCESS_TOKEN_TTL_MINUTES")); ok {
		cfg.AccessTTL = time.Duration(minutes) * time.Minute
	} else if hours, ok := positiveInt(get("JWT_EXPIRY_HOURS")); ok {
		// Backwards compatibility: deployments that still set the old variable
		// keep their configured lifetime until they migrate.
		cfg.AccessTTL = time.Duration(hours) * time.Hour
	}
	if days, ok := positiveInt(get("REFRESH_TOKEN_TTL_DAYS")); ok {
		cfg.RefreshTTL = time.Duration(days) * 24 * time.Hour
	}
	if minutes, ok := positiveInt(get("SESSION_IDLE_TIMEOUT_MINUTES")); ok {
		cfg.IdleTimeout = time.Duration(minutes) * time.Minute
	}
	if raw := strings.TrimSpace(get("SESSION_LIMIT_PER_USER")); raw != "" {
		if limit, err := strconv.Atoi(raw); err == nil && limit >= 0 {
			cfg.LimitPerUser = limit
		}
	}
	if days, ok := positiveInt(get("SESSION_RETENTION_DAYS")); ok {
		cfg.Retention = time.Duration(days) * 24 * time.Hour
	}
	if cfg.IdleTimeout > cfg.RefreshTTL {
		cfg.IdleTimeout = cfg.RefreshTTL
	}
	return cfg
}

func positiveInt(raw string) (int, bool) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

// Sentinel errors returned by SessionStore.
var (
	ErrSessionNotFound     = errors.New("auth: session not found")
	ErrSessionRevoked      = errors.New("auth: session has been revoked")
	ErrSessionExpired      = errors.New("auth: session has expired")
	ErrRefreshTokenInvalid = errors.New("auth: refresh token is invalid")
	ErrRefreshTokenReuse   = errors.New("auth: refresh token reuse detected")
)

// RefreshReuseError identifies the session family that must be revoked after a
// rotated refresh token is presented again.
type RefreshReuseError struct {
	SessionID string
	UserID    string
}

func (e *RefreshReuseError) Error() string {
	return fmt.Sprintf("auth: refresh token reuse detected for session %s", e.SessionID)
}

func (e *RefreshReuseError) Unwrap() error { return ErrRefreshTokenReuse }

// SessionTokens is the result of creating or rotating a session.
type SessionTokens struct {
	SessionID     string
	UserID        string
	JTI           string
	RefreshToken  string
	IdleExpiresAt time.Time
	ExpiresAt     time.Time
}

// SessionInfo is the owner-visible projection of an active session.
type SessionInfo struct {
	ID            string
	IP            string
	UserAgent     string
	Device        string
	CreatedAt     time.Time
	LastSeenAt    time.Time
	IdleExpiresAt time.Time
	ExpiresAt     time.Time
}

// SessionStore owns persistent sessions and refresh-token rotation.
type SessionStore struct {
	db  *pgxpool.Pool
	cfg SessionConfig
	now func() time.Time
}

// NewSessionStore constructs a SessionStore. A nil pool is tolerated so tests
// and tools can build the handler without a database; every method that needs
// persistence returns an error in that case.
func NewSessionStore(db *pgxpool.Pool, cfg SessionConfig) *SessionStore {
	return &SessionStore{db: db, cfg: cfg, now: func() time.Time { return time.Now().UTC() }}
}

// Config returns the resolved policy.
func (s *SessionStore) Config() SessionConfig {
	if s == nil {
		return SessionConfig{}
	}
	return s.cfg
}

func (s *SessionStore) available() bool { return s != nil && s.db != nil }

func (s *SessionStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}

// Create inserts a new session and returns its initial access/refresh pair.
// evicted contains session IDs revoked because the user exceeded the configured
// concurrent-session limit.
func (s *SessionStore) Create(ctx context.Context, userID, ip, userAgent string) (SessionTokens, []string, error) {
	if !s.available() {
		return SessionTokens{}, nil, errors.New("auth: session store has no database")
	}
	if strings.TrimSpace(userID) == "" {
		return SessionTokens{}, nil, errors.New("auth: user ID must not be empty")
	}
	refreshToken, err := newRefreshToken()
	if err != nil {
		return SessionTokens{}, nil, err
	}
	sessionID := uuid.New()
	jti := uuid.New()
	now := s.clock()
	idleExpires := now.Add(s.cfg.IdleTimeout)
	expires := now.Add(s.cfg.RefreshTTL)
	if idleExpires.After(expires) {
		idleExpires = expires
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return SessionTokens{}, nil, fmt.Errorf("auth: begin session: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_sessions
			(id, user_id, current_jti, current_refresh_hash, ip, user_agent,
			 created_at, last_seen_at, idle_expires_at, expires_at)
		VALUES ($1, $2, $3, $4, $5::inet, $6, $7, $7, $8, $9)`,
		sessionID, userID, jti, hashRefreshToken(refreshToken),
		nullableIP(ip), truncateUserAgent(userAgent), now, idleExpires, expires,
	); err != nil {
		return SessionTokens{}, nil, fmt.Errorf("auth: insert session: %w", err)
	}

	evicted, err := s.enforceLimit(ctx, tx, userID, now)
	if err != nil {
		return SessionTokens{}, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionTokens{}, nil, fmt.Errorf("auth: commit session: %w", err)
	}
	metrics.Default().ObserveSessionEvent("created")
	return SessionTokens{
		SessionID:     sessionID.String(),
		UserID:        userID,
		JTI:           jti.String(),
		RefreshToken:  refreshToken,
		IdleExpiresAt: idleExpires,
		ExpiresAt:     expires,
	}, evicted, nil
}

func (s *SessionStore) enforceLimit(ctx context.Context, tx pgx.Tx, userID string, now time.Time) ([]string, error) {
	if s.cfg.LimitPerUser <= 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		UPDATE auth_sessions
		SET revoked_at = $3, revoke_reason = 'session_limit'
		WHERE id IN (
			SELECT id FROM auth_sessions
			WHERE user_id = $1
			  AND revoked_at IS NULL
			  AND idle_expires_at > $3
			  AND expires_at > $3
			ORDER BY last_seen_at DESC, created_at DESC
			OFFSET $2
		)
		RETURNING id`, userID, s.cfg.LimitPerUser, now)
	if err != nil {
		return nil, fmt.Errorf("auth: enforce session limit: %w", err)
	}
	defer rows.Close()
	var evicted []string
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("auth: scan evicted session: %w", err)
		}
		evicted = append(evicted, id.String())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: iterate evicted sessions: %w", err)
	}
	if len(evicted) > 0 {
		metrics.Default().ObserveSessionEvent("session_limit")
	}
	return evicted, nil
}

// Validate checks that a session is active and extends its idle window. It is
// called by the JWT middleware for every authenticated request.
func (s *SessionStore) Validate(ctx context.Context, sessionID string) error {
	if !s.available() {
		return errors.New("auth: session store has no database")
	}
	id, err := uuid.Parse(strings.TrimSpace(sessionID))
	if err != nil {
		return ErrSessionNotFound
	}
	var (
		revokedAt  *time.Time
		lastSeen   time.Time
		idleExpire time.Time
		expires    time.Time
	)
	err = s.db.QueryRow(ctx, `
		SELECT revoked_at, last_seen_at, idle_expires_at, expires_at
		FROM auth_sessions WHERE id = $1`, id,
	).Scan(&revokedAt, &lastSeen, &idleExpire, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("auth: load session: %w", err)
	}
	if revokedAt != nil {
		return ErrSessionRevoked
	}
	now := s.clock()
	if !now.Before(idleExpire) || !now.Before(expires) {
		return ErrSessionExpired
	}
	if now.Sub(lastSeen) >= sessionTouchInterval {
		nextIdle := now.Add(s.cfg.IdleTimeout)
		if nextIdle.After(expires) {
			nextIdle = expires
		}
		if _, err := s.db.Exec(ctx, `
			UPDATE auth_sessions
			SET last_seen_at = $2, idle_expires_at = $3
			WHERE id = $1 AND revoked_at IS NULL`, id, now, nextIdle); err != nil {
			// Activity tracking must not fail the request; the session was
			// valid when it was read.
			slog.Warn("session_touch_failed", "session_id", id.String(), "error", err)
		}
	}
	return nil
}

// Rotate consumes a refresh token and issues the next generation. Presenting a
// token that was already rotated revokes the whole session family and returns
// *RefreshReuseError.
func (s *SessionStore) Rotate(ctx context.Context, refreshToken, ip, userAgent string) (SessionTokens, error) {
	if !s.available() {
		return SessionTokens{}, errors.New("auth: session store has no database")
	}
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return SessionTokens{}, ErrRefreshTokenInvalid
	}
	hash := hashRefreshToken(refreshToken)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return SessionTokens{}, fmt.Errorf("auth: begin refresh: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		sessionID  uuid.UUID
		userID     uuid.UUID
		currentJTI uuid.UUID
		idleExpire time.Time
		expires    time.Time
		revokedAt  *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, current_jti, idle_expires_at, expires_at, revoked_at
		FROM auth_sessions
		WHERE current_refresh_hash = $1
		FOR UPDATE`, hash,
	).Scan(&sessionID, &userID, &currentJTI, &idleExpire, &expires, &revokedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return s.handleRefreshReuse(ctx, tx, hash)
	}
	if err != nil {
		return SessionTokens{}, fmt.Errorf("auth: load refresh session: %w", err)
	}
	if revokedAt != nil {
		return SessionTokens{}, ErrSessionRevoked
	}
	now := s.clock()
	if !now.Before(idleExpire) || !now.Before(expires) {
		_, _ = tx.Exec(ctx, `
			UPDATE auth_sessions
			SET revoked_at = $2, revoke_reason = 'expired'
			WHERE id = $1 AND revoked_at IS NULL`, sessionID, now)
		if err := tx.Commit(ctx); err != nil {
			return SessionTokens{}, fmt.Errorf("auth: commit expired session: %w", err)
		}
		metrics.Default().ObserveSessionEvent("expired")
		return SessionTokens{}, ErrSessionExpired
	}

	nextRefresh, err := newRefreshToken()
	if err != nil {
		return SessionTokens{}, err
	}
	nextJTI := uuid.New()
	nextIdle := now.Add(s.cfg.IdleTimeout)
	if nextIdle.After(expires) {
		nextIdle = expires
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_session_rotations (id, session_id, refresh_hash, jti, created_at, rotated_at)
		VALUES ($1, $2, $3, $4, $5, $5)`,
		uuid.New(), sessionID, hash, currentJTI, now,
	); err != nil {
		return SessionTokens{}, fmt.Errorf("auth: record refresh rotation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET current_jti = $2,
		    current_refresh_hash = $3,
		    last_seen_at = $4,
		    idle_expires_at = $5,
		    ip = COALESCE($6::inet, ip),
		    user_agent = CASE WHEN $7 <> '' THEN $7 ELSE user_agent END
		WHERE id = $1`,
		sessionID, nextJTI, hashRefreshToken(nextRefresh), now, nextIdle,
		nullableIP(ip), truncateUserAgent(userAgent),
	); err != nil {
		return SessionTokens{}, fmt.Errorf("auth: rotate session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionTokens{}, fmt.Errorf("auth: commit refresh: %w", err)
	}
	metrics.Default().ObserveSessionEvent("refreshed")
	return SessionTokens{
		SessionID:     sessionID.String(),
		UserID:        userID.String(),
		JTI:           nextJTI.String(),
		RefreshToken:  nextRefresh,
		IdleExpiresAt: nextIdle,
		ExpiresAt:     expires,
	}, nil
}

func (s *SessionStore) handleRefreshReuse(ctx context.Context, tx pgx.Tx, hash string) (SessionTokens, error) {
	var (
		sessionID uuid.UUID
		userID    uuid.UUID
	)
	err := tx.QueryRow(ctx, `
		SELECT s.id, s.user_id
		FROM auth_session_rotations r
		JOIN auth_sessions s ON s.id = r.session_id
		WHERE r.refresh_hash = $1
		ORDER BY r.created_at DESC
		LIMIT 1`, hash,
	).Scan(&sessionID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionTokens{}, ErrRefreshTokenInvalid
	}
	if err != nil {
		return SessionTokens{}, fmt.Errorf("auth: lookup rotated refresh token: %w", err)
	}
	now := s.clock()
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = $2, revoke_reason = 'refresh_reuse'
		WHERE id = $1 AND revoked_at IS NULL`, sessionID, now); err != nil {
		return SessionTokens{}, fmt.Errorf("auth: revoke reused session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_session_rotations
		SET reused_at = $2
		WHERE refresh_hash = $1 AND reused_at IS NULL`, hash, now); err != nil {
		return SessionTokens{}, fmt.Errorf("auth: mark refresh reuse: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionTokens{}, fmt.Errorf("auth: commit refresh reuse: %w", err)
	}
	metrics.Default().ObserveSessionEvent("reuse_detected")
	return SessionTokens{}, &RefreshReuseError{SessionID: sessionID.String(), UserID: userID.String()}
}

// List returns the user's active sessions, most recently used first. The
// current session is not special-cased here; the handler marks it.
func (s *SessionStore) List(ctx context.Context, userID string) ([]SessionInfo, error) {
	if !s.available() {
		return nil, errors.New("auth: session store has no database")
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, COALESCE(host(ip), ''), user_agent,
		       created_at, last_seen_at, idle_expires_at, expires_at
		FROM auth_sessions
		WHERE user_id = $1
		  AND revoked_at IS NULL
		  AND idle_expires_at > NOW()
		  AND expires_at > NOW()
		ORDER BY last_seen_at DESC, created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: list sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]SessionInfo, 0)
	for rows.Next() {
		var (
			info SessionInfo
			id   uuid.UUID
		)
		if err := rows.Scan(&id, &info.IP, &info.UserAgent, &info.CreatedAt,
			&info.LastSeenAt, &info.IdleExpiresAt, &info.ExpiresAt); err != nil {
			return nil, fmt.Errorf("auth: scan session: %w", err)
		}
		info.ID = id.String()
		info.Device = DeviceLabel(info.UserAgent)
		sessions = append(sessions, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: iterate sessions: %w", err)
	}
	return sessions, nil
}

// Revoke revokes one owned session. It returns false when the session does not
// belong to the user. Revoking an already revoked session is idempotent and
// returns true.
func (s *SessionStore) Revoke(ctx context.Context, userID, sessionID, reason string) (bool, error) {
	if !s.available() {
		return false, errors.New("auth: session store has no database")
	}
	id, err := uuid.Parse(strings.TrimSpace(sessionID))
	if err != nil {
		return false, nil
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = NOW(), revoke_reason = $3
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		id, userID, reason)
	if err != nil {
		return false, fmt.Errorf("auth: revoke session: %w", err)
	}
	if tag.RowsAffected() > 0 {
		metrics.Default().ObserveSessionEvent("revoked")
		return true, nil
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM auth_sessions WHERE id = $1 AND user_id = $2)`,
		id, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("auth: check session ownership: %w", err)
	}
	return exists, nil
}

// RevokeOthers revokes every active session for the user except keepSessionID.
// An empty keepSessionID revokes all sessions (used by password change and
// account deactivation).
func (s *SessionStore) RevokeOthers(ctx context.Context, userID, keepSessionID, reason string) ([]string, error) {
	if !s.available() {
		return nil, errors.New("auth: session store has no database")
	}
	keep := uuid.Nil
	if strings.TrimSpace(keepSessionID) != "" {
		parsed, err := uuid.Parse(keepSessionID)
		if err != nil {
			return nil, fmt.Errorf("auth: parse current session: %w", err)
		}
		keep = parsed
	}
	rows, err := s.db.Query(ctx, `
		UPDATE auth_sessions
		SET revoked_at = NOW(), revoke_reason = $3
		WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL
		RETURNING id`, userID, keep, reason)
	if err != nil {
		return nil, fmt.Errorf("auth: revoke other sessions: %w", err)
	}
	defer rows.Close()
	var revoked []string
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("auth: scan revoked session: %w", err)
		}
		revoked = append(revoked, id.String())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: iterate revoked sessions: %w", err)
	}
	if len(revoked) > 0 {
		metrics.Default().ObserveSessionEvent("revoke_all")
	}
	return revoked, nil
}

// Cleanup deletes sessions that have been revoked or expired longer than the
// configured retention. Rotation rows cascade with their session.
func (s *SessionStore) Cleanup(ctx context.Context) (int64, error) {
	if !s.available() || s.cfg.Retention <= 0 {
		return 0, nil
	}
	tag, err := s.db.Exec(ctx, `
		DELETE FROM auth_sessions
		WHERE (revoked_at IS NOT NULL AND revoked_at < NOW() - ($1 * INTERVAL '1 second'))
		   OR (expires_at < NOW() - ($1 * INTERVAL '1 second'))`,
		s.cfg.Retention.Seconds())
	if err != nil {
		return 0, fmt.Errorf("auth: clean sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RunMaintenance runs one cleanup pass at startup and then daily until the
// context is cancelled.
func (s *SessionStore) RunMaintenance(ctx context.Context) {
	if !s.available() || s.cfg.Retention <= 0 {
		return
	}
	pass := func() {
		deleted, err := s.Cleanup(ctx)
		if err != nil {
			slog.Warn("session_cleanup_failed", "error", err)
			return
		}
		if deleted > 0 {
			slog.Info("sessions_cleaned", "deleted", deleted)
		}
	}
	pass()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pass()
		}
	}
}

// ActiveCount returns the number of sessions that can currently authenticate.
func (s *SessionStore) ActiveCount(ctx context.Context) (int64, error) {
	if !s.available() {
		return 0, nil
	}
	var count int64
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM auth_sessions
		WHERE revoked_at IS NULL
		  AND idle_expires_at > NOW()
		  AND expires_at > NOW()`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("auth: count active sessions: %w", err)
	}
	return count, nil
}

func newRefreshToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: generate refresh token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func nullableIP(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if net.ParseIP(trimmed) == nil {
		return nil
	}
	return trimmed
}

func truncateUserAgent(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= maxUserAgentLength {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxUserAgentLength])
}

// DeviceLabel turns a user agent into a short, human-readable label for the
// session list. It is deliberately conservative: unknown clients still get a
// stable label instead of an empty cell.
func DeviceLabel(userAgent string) string {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	if ua == "" {
		return "Unknown device"
	}

	browser := "Unknown browser"
	switch {
	case strings.Contains(ua, "edg/"):
		browser = "Edge"
	case strings.Contains(ua, "opr/") || strings.Contains(ua, "opera"):
		browser = "Opera"
	case strings.Contains(ua, "chrome/") || strings.Contains(ua, "crios/"):
		browser = "Chrome"
	case strings.Contains(ua, "firefox/") || strings.Contains(ua, "fxios/"):
		browser = "Firefox"
	case strings.Contains(ua, "safari/") && !strings.Contains(ua, "chrome"):
		browser = "Safari"
	case strings.Contains(ua, "curl/"):
		return "curl client"
	case strings.Contains(ua, "postman"):
		return "Postman client"
	case strings.Contains(ua, "playwright"):
		return "Playwright client"
	}

	osName := "Unknown OS"
	switch {
	case strings.Contains(ua, "windows"):
		osName = "Windows"
	case strings.Contains(ua, "android"):
		osName = "Android"
	case strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad") || strings.Contains(ua, "ios"):
		osName = "iOS"
	case strings.Contains(ua, "mac os x") || strings.Contains(ua, "macintosh"):
		osName = "macOS"
	case strings.Contains(ua, "linux"):
		osName = "Linux"
	}
	return browser + " on " + osName
}
