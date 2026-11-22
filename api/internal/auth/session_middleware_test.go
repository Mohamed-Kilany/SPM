package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"spm-platform/api/internal/auth"
)

// fakeSessionValidator records the session ID it was asked to validate and
// returns the configured error.
type fakeSessionValidator struct {
	err       error
	validated []string
}

func (f *fakeSessionValidator) Validate(_ context.Context, sessionID string) error {
	f.validated = append(f.validated, sessionID)
	return f.err
}

func newSessionRouter(t *testing.T, secret string, validator auth.SessionValidator) *gin.Engine {
	t.Helper()
	t.Setenv("JWT_SECRET", secret)

	r := gin.New()
	r.Use(auth.Middleware(nil, validator))
	r.GET("/protected", func(c *gin.Context) {
		claims, ok := auth.ClaimsFrom(c)
		require.True(t, ok)
		c.JSON(http.StatusOK, gin.H{"user_id": claims.UserID, "session_id": claims.SessionID})
	})
	return r
}

func sessionToken(t *testing.T, secret, sessionID string) string {
	t.Helper()
	token, err := auth.IssueSessionToken("user-1", "member", nil, secret, time.Hour, sessionID, "jti-1")
	require.NoError(t, err)
	return token
}

func TestMiddleware_SessionActive(t *testing.T) {
	validator := &fakeSessionValidator{}
	r := newSessionRouter(t, testSecret, validator)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+sessionToken(t, testSecret, "session-1"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"session-1"}, validator.validated)
	assert.Contains(t, w.Body.String(), `"session_id":"session-1"`)
}

func TestMiddleware_SessionRevoked(t *testing.T) {
	validator := &fakeSessionValidator{err: auth.ErrSessionRevoked}
	r := newSessionRouter(t, testSecret, validator)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+sessionToken(t, testSecret, "session-1"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "SESSION_REVOKED")
}

func TestMiddleware_SessionNotFoundOrExpired(t *testing.T) {
	for name, sessionErr := range map[string]error{
		"not found": auth.ErrSessionNotFound,
		"expired":   auth.ErrSessionExpired,
	} {
		t.Run(name, func(t *testing.T) {
			validator := &fakeSessionValidator{err: sessionErr}
			r := newSessionRouter(t, testSecret, validator)

			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", "Bearer "+sessionToken(t, testSecret, "session-1"))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Contains(t, w.Body.String(), "SESSION_EXPIRED")
		})
	}
}

func TestMiddleware_SessionCheckFailureFailsClosed(t *testing.T) {
	validator := &fakeSessionValidator{err: errors.New("database unavailable")}
	r := newSessionRouter(t, testSecret, validator)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+sessionToken(t, testSecret, "session-1"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "SESSION_CHECK_FAILED")
}

func TestMiddleware_LegacyTokenSkipsSessionCheck(t *testing.T) {
	validator := &fakeSessionValidator{err: auth.ErrSessionRevoked}
	r := newSessionRouter(t, testSecret, validator)

	token, err := auth.IssueToken("user-1", "member", nil, testSecret, 1)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, validator.validated)
}
