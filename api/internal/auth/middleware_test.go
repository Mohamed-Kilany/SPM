package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/models"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---- helpers ----------------------------------------------------------------

// buildExpiredToken creates a JWT that expired 1 hour ago, signed with secret.
func buildExpiredToken(secret string) (string, error) {
	claims := models.AuthClaims{
		UserID: "expired-user",
		Role:   "member",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "expired-user",
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ---- fakeKVEntry satisfies nats.KeyValueEntry ------------------------------

type fakeKVEntry struct {
	value []byte
}

func (e *fakeKVEntry) Bucket() string             { return "user_invalidation" }
func (e *fakeKVEntry) Key() string                { return "" }
func (e *fakeKVEntry) Value() []byte              { return e.value }
func (e *fakeKVEntry) Revision() uint64           { return 1 }
func (e *fakeKVEntry) Created() time.Time         { return time.Time{} }
func (e *fakeKVEntry) Delta() uint64              { return 0 }
func (e *fakeKVEntry) Operation() nats.KeyValueOp { return nats.KeyValuePut }

// ---- fakeKV satisfies nats.KeyValue ----------------------------------------

// fakeKV is an in-memory stub that only needs Get() to work.
// All other methods panic — they are never called by Middleware.
type fakeKV struct {
	entries map[string][]byte
}

func newFakeKV(entries map[string][]byte) *fakeKV {
	return &fakeKV{entries: entries}
}

func (kv *fakeKV) Get(key string) (nats.KeyValueEntry, error) {
	if v, ok := kv.entries[key]; ok {
		return &fakeKVEntry{value: v}, nil
	}
	return nil, nats.ErrKeyNotFound
}

func (kv *fakeKV) GetRevision(key string, revision uint64) (nats.KeyValueEntry, error) {
	panic("not implemented")
}
func (kv *fakeKV) Put(key string, value []byte) (uint64, error)       { panic("not implemented") }
func (kv *fakeKV) PutString(key string, value string) (uint64, error) { panic("not implemented") }
func (kv *fakeKV) Create(key string, value []byte) (uint64, error)    { panic("not implemented") }
func (kv *fakeKV) Update(key string, value []byte, last uint64) (uint64, error) {
	panic("not implemented")
}
func (kv *fakeKV) Delete(key string, opts ...nats.DeleteOpt) error { panic("not implemented") }
func (kv *fakeKV) Purge(key string, opts ...nats.DeleteOpt) error  { panic("not implemented") }
func (kv *fakeKV) Watch(keys string, opts ...nats.WatchOpt) (nats.KeyWatcher, error) {
	panic("not implemented")
}
func (kv *fakeKV) WatchAll(opts ...nats.WatchOpt) (nats.KeyWatcher, error) {
	panic("not implemented")
}
func (kv *fakeKV) WatchFiltered(keys []string, opts ...nats.WatchOpt) (nats.KeyWatcher, error) {
	panic("not implemented")
}
func (kv *fakeKV) Keys(opts ...nats.WatchOpt) ([]string, error)           { panic("not implemented") }
func (kv *fakeKV) ListKeys(opts ...nats.WatchOpt) (nats.KeyLister, error) { panic("not implemented") }
func (kv *fakeKV) History(key string, opts ...nats.WatchOpt) ([]nats.KeyValueEntry, error) {
	panic("not implemented")
}
func (kv *fakeKV) Bucket() string                           { return "user_invalidation" }
func (kv *fakeKV) PurgeDeletes(opts ...nats.PurgeOpt) error { panic("not implemented") }
func (kv *fakeKV) Status() (nats.KeyValueStatus, error)     { panic("not implemented") }

// ---- router builder --------------------------------------------------------

// newTestRouter builds a Gin router with the JWT middleware and a trivial
// protected handler. Pass nil for kv to skip invalidation checks.
func newTestRouter(t *testing.T, secret string, kv nats.KeyValue) *gin.Engine {
	t.Helper()
	t.Setenv("JWT_SECRET", secret)

	r := gin.New()
	r.Use(auth.Middleware(kv))
	r.GET("/protected", func(c *gin.Context) {
		claims, ok := auth.ClaimsFrom(c)
		require.True(t, ok)
		c.JSON(http.StatusOK, gin.H{"user_id": claims.UserID, "role": claims.Role})
	})
	return r
}

// ---- tests: happy path -----------------------------------------------------

func TestMiddleware_ValidToken(t *testing.T) {
	tokenStr, err := auth.IssueToken("user-1", "admin", []string{"p1"}, testSecret, 1)
	require.NoError(t, err)

	r := newTestRouter(t, testSecret, nil)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ---- tests: missing / malformed header -------------------------------------

func TestMiddleware_MissingAuthHeader(t *testing.T) {
	r := newTestRouter(t, testSecret, nil)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "UNAUTHORIZED")
}

func TestMiddleware_WrongScheme(t *testing.T) {
	r := newTestRouter(t, testSecret, nil)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestMiddleware_EmptyBearerToken(t *testing.T) {
	r := newTestRouter(t, testSecret, nil)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ---- tests: invalid / wrong-secret token -----------------------------------

func TestMiddleware_WrongSecret(t *testing.T) {
	tokenStr, _ := auth.IssueToken("u1", "admin", nil, testSecret, 1)

	r := newTestRouter(t, "different-secret", nil)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestMiddleware_MalformedToken(t *testing.T) {
	r := newTestRouter(t, testSecret, nil)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer garbage.token.here")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ---- tests: expired token --------------------------------------------------

func TestMiddleware_ExpiredToken_Returns401WithTokenExpiredCode(t *testing.T) {
	tokenStr, err := buildExpiredToken(testSecret)
	require.NoError(t, err)

	r := newTestRouter(t, testSecret, nil)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "TOKEN_EXPIRED")
}

// ---- tests: missing JWT_SECRET env var -------------------------------------

func TestMiddleware_MissingJWTSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "")

	r := gin.New()
	r.Use(auth.Middleware(nil))
	r.GET("/protected", func(c *gin.Context) { c.Status(http.StatusOK) })

	tokenStr, _ := auth.IssueToken("u1", "admin", nil, testSecret, 1)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ---- tests: ClaimsFrom helper ----------------------------------------------

func TestClaimsFrom_AbsentClaims(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	claims, ok := auth.ClaimsFrom(c)
	assert.False(t, ok)
	assert.Nil(t, claims)
}

// ---- tests: KV invalidation check ------------------------------------------

func TestMiddleware_TokenInvalidated_Returns401(t *testing.T) {
	tokenStr, err := auth.IssueToken("user-inv", "member", nil, testSecret, 1)
	require.NoError(t, err)

	// Invalidation timestamp is "now + 60s" → strictly after iat → token rejected.
	futureTs := strconv.FormatInt(time.Now().Add(60*time.Second).Unix(), 10)
	kv := newFakeKV(map[string][]byte{"user-inv": []byte(futureTs)})

	r := newTestRouter(t, testSecret, kv)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "TOKEN_INVALIDATED")
}

func TestMiddleware_TokenNotInvalidated_Passes(t *testing.T) {
	tokenStr, err := auth.IssueToken("user-ok", "member", nil, testSecret, 1)
	require.NoError(t, err)

	// Invalidation timestamp is "now - 60s" → older than iat → token is still valid.
	pastTs := strconv.FormatInt(time.Now().Add(-60*time.Second).Unix(), 10)
	kv := newFakeKV(map[string][]byte{"user-ok": []byte(pastTs)})

	r := newTestRouter(t, testSecret, kv)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMiddleware_NoKVEntry_Passes(t *testing.T) {
	tokenStr, err := auth.IssueToken("user-new", "member", nil, testSecret, 1)
	require.NoError(t, err)

	// KV has no entry for this user — token should pass.
	kv := newFakeKV(map[string][]byte{})

	r := newTestRouter(t, testSecret, kv)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
