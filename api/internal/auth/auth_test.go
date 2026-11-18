package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/models"
)

const testSecret = "super-secret-key-for-tests"

// --- IssueToken ---

func TestIssueToken_RoundTrip(t *testing.T) {
	projectIDs := []string{"proj-1", "proj-2"}
	tokenStr, err := auth.IssueToken("user-42", "member", projectIDs, testSecret, 1)
	require.NoError(t, err)
	require.NotEmpty(t, tokenStr)

	claims, err := auth.ParseToken(tokenStr, testSecret)
	require.NoError(t, err)
	assert.Equal(t, "user-42", claims.UserID)
	assert.Equal(t, "member", claims.Role)
	assert.Equal(t, projectIDs, claims.ProjectIDs)
	// Note: jwt/v5 populates UserID (json:"sub") from the "sub" claim;
	// RegisteredClaims.Subject is only set when IssueToken writes it, not on parse
	// because AuthClaims.UserID takes the json:"sub" slot.
}

func TestIssueToken_EmptySecret(t *testing.T) {
	_, err := auth.IssueToken("u1", "admin", nil, "", 1)
	assert.Error(t, err)
}

func TestIssueToken_ZeroExpiry(t *testing.T) {
	_, err := auth.IssueToken("u1", "admin", nil, testSecret, 0)
	assert.Error(t, err)
}

func TestIssueToken_NegativeExpiry(t *testing.T) {
	_, err := auth.IssueToken("u1", "admin", nil, testSecret, -5)
	assert.Error(t, err)
}

// --- ParseToken ---

func TestParseToken_EmptyToken(t *testing.T) {
	_, err := auth.ParseToken("", testSecret)
	assert.Error(t, err)
}

func TestParseToken_EmptySecret(t *testing.T) {
	tokenStr, _ := auth.IssueToken("u1", "admin", nil, testSecret, 1)
	_, err := auth.ParseToken(tokenStr, "")
	assert.Error(t, err)
}

func TestParseToken_WrongSecret(t *testing.T) {
	tokenStr, _ := auth.IssueToken("u1", "admin", nil, testSecret, 1)
	_, err := auth.ParseToken(tokenStr, "wrong-secret")
	assert.Error(t, err)
}

func TestParseToken_ExpiredToken(t *testing.T) {
	// Build a token that expired 1 hour ago using the jwt library directly.
	claims := models.AuthClaims{
		UserID: "u1",
		Role:   "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "u1",
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(testSecret))
	require.NoError(t, err)

	_, err = auth.ParseToken(tokenStr, testSecret)
	assert.Error(t, err)
}

func TestParseToken_TamperedSignature(t *testing.T) {
	// A token whose signature segment has been corrupted must fail verification.
	tokenStr, err := auth.IssueToken("u1", "admin", nil, testSecret, 1)
	require.NoError(t, err)

	// JWT format: <header>.<payload>.<signature>
	// Replace the entire signature segment with a fixed invalid value so the
	// HMAC check always fails regardless of base64 padding coincidences.
	parts := strings.SplitN(tokenStr, ".", 3)
	require.Len(t, parts, 3, "JWT must have 3 parts")
	parts[2] = "aW52YWxpZHNpZ25hdHVyZWhlcmU" // base64url for "invalidsignaturehere"
	tampered := strings.Join(parts, ".")

	_, err = auth.ParseToken(tampered, testSecret)
	assert.Error(t, err)
}

func TestParseToken_MalformedToken(t *testing.T) {
	_, err := auth.ParseToken("not.a.jwt", testSecret)
	assert.Error(t, err)
}
