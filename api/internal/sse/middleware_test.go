package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/models"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newStreamRouter(t *testing.T, store *auth.TicketStore) *gin.Engine {
	t.Helper()
	r := gin.New()
	r.GET("/stream", StreamAuthMiddleware(store, nil), func(c *gin.Context) {
		claims, ok := auth.ClaimsFrom(c)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "claims missing"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"sub":  claims.CanonicalUserID(),
			"role": claims.Role,
		})
	})
	return r
}

func ticketStore(t *testing.T) *auth.TicketStore {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return auth.NewTicketStore(ctx, time.Hour)
}

func issueTicket(t *testing.T, store *auth.TicketStore) string {
	t.Helper()
	ticket, err := store.IssueStreamTicket(models.AuthClaims{
		Role: "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "user-1",
		},
	})
	require.NoError(t, err)
	return ticket
}

func TestStreamAuth_TicketIsAcceptedAndSingleUse(t *testing.T) {
	store := ticketStore(t)
	r := newStreamRouter(t, store)
	ticket := issueTicket(t, store)

	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream?ticket="+ticket, nil)
	r.ServeHTTP(first, req)
	assert.Equal(t, http.StatusOK, first.Code)
	assert.Contains(t, first.Body.String(), "user-1")

	// A second request with the same ticket must be rejected; tokens are
	// single-use even when the first connection succeeds.
	second := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/stream?ticket="+ticket, nil)
	r.ServeHTTP(second, req)
	assert.Equal(t, http.StatusUnauthorized, second.Code)
	assert.Contains(t, second.Body.String(), "TICKET_INVALID")
}

func TestStreamAuth_MalformedTicketRejected(t *testing.T) {
	store := ticketStore(t)
	r := newStreamRouter(t, store)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream?ticket=not-a-uuid", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "TICKET_INVALID")
}

func TestStreamAuth_RejectsLegacyTokenQueryParameter(t *testing.T) {
	store := ticketStore(t)
	r := newStreamRouter(t, store)
	ticket := issueTicket(t, store)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream?token=header.payload.signature&ticket="+ticket, nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "UNAUTHORIZED")

	// The ticket must not have been consumed by the rejected request.
	claims, err := store.RedeemStreamTicket(ticket)
	require.NoError(t, err)
	assert.Equal(t, "user-1", claims.CanonicalUserID())
}

func TestStreamAuth_BearerFallback(t *testing.T) {
	store := ticketStore(t)
	r := newStreamRouter(t, store)
	t.Setenv("JWT_SECRET", "test-secret")
	token, err := auth.IssueToken("user-2", "member", []string{"project-1"}, "test-secret", 1)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "user-2")
}

func TestStreamAuth_MissingCredentials(t *testing.T) {
	store := ticketStore(t)
	r := newStreamRouter(t, store)
	t.Setenv("JWT_SECRET", "test-secret")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "UNAUTHORIZED")
}
