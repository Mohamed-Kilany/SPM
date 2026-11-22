package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/nats-io/nats.go"

	"spm-platform/api/internal/models"
)

// ClaimsKey is the Gin context key under which the validated AuthClaims are stored.
const ClaimsKey = "auth_claims"

// errorResponse is the canonical API error envelope used by this package.
type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func abortUnauthorized(c *gin.Context, code, message string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse{
		Error: errorBody{Code: code, Message: message},
	})
}

// SessionValidator checks that a JWT's session is still active. SessionStore
// implements it; tests may pass a fake.
type SessionValidator interface {
	Validate(ctx context.Context, sessionID string) error
}

// Middleware returns a Gin middleware that validates the JWT present in the
// Authorization: Bearer <token> header and injects the parsed AuthClaims into
// the Gin context under ClaimsKey. An optional SessionValidator rejects tokens
// whose persistent session has been revoked or has expired.
//
// JWTs are never accepted from query parameters. EventSource clients must use
// the short-lived stream ticket flow instead.
func Middleware(invalidationKV nats.KeyValue, validators ...SessionValidator) gin.HandlerFunc {
	var sessionValidator SessionValidator
	if len(validators) > 0 {
		sessionValidator = validators[0]
	}
	return func(c *gin.Context) {
		secret := os.Getenv("JWT_SECRET")
		if secret == "" {
			abortUnauthorized(c, "UNAUTHORIZED", "Server JWT secret is not configured")
			return
		}

		var tokenStr string
		if authHeader := c.GetHeader("Authorization"); authHeader != "" {
			const prefix = "Bearer "
			if !strings.HasPrefix(authHeader, prefix) {
				abortUnauthorized(c, "UNAUTHORIZED", "Authorization header must use Bearer scheme")
				return
			}
			tokenStr = strings.TrimSpace(authHeader[len(prefix):])
		}

		if tokenStr == "" {
			abortUnauthorized(c, "UNAUTHORIZED", "Authorization header is required")
			return
		}

		claims := &models.AuthClaims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(secret), nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))

		if err != nil {
			if errors.Is(err, jwt.ErrTokenExpired) {
				abortUnauthorized(c, "TOKEN_EXPIRED", "Token has expired")
				return
			}
			abortUnauthorized(c, "UNAUTHORIZED", "Invalid or malformed token")
			return
		}

		if !token.Valid {
			abortUnauthorized(c, "UNAUTHORIZED", "Token is not valid")
			return
		}

		claims.Normalize()
		userID := claims.CanonicalUserID()
		if userID == "" {
			abortUnauthorized(c, "UNAUTHORIZED", "Token has no subject")
			return
		}

		if invalidationKV != nil && claims.IssuedAt != nil {
			entry, kvErr := invalidationKV.Get(userID)
			if kvErr == nil {
				invalidationTs, parseErr := strconv.ParseInt(strings.TrimSpace(string(entry.Value())), 10, 64)
				if parseErr == nil && claims.IssuedAt.Unix() < invalidationTs {
					abortUnauthorized(c, "TOKEN_INVALIDATED", "Token has been invalidated")
					return
				}
			}
			// Any other KV error is treated permissively (fail-open) so a KV
			// outage does not block every authenticated request.
		}

		if sessionValidator != nil && claims.SessionID != "" {
			switch err := sessionValidator.Validate(c.Request.Context(), claims.SessionID); {
			case err == nil:
			case errors.Is(err, ErrSessionRevoked):
				abortUnauthorized(c, "SESSION_REVOKED", "This session has been revoked")
				return
			case errors.Is(err, ErrSessionExpired), errors.Is(err, ErrSessionNotFound):
				abortUnauthorized(c, "SESSION_EXPIRED", "This session has expired")
				return
			default:
				// Session state is security-critical. A database failure must
				// not silently accept a token that may have been revoked.
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, errorResponse{
					Error: errorBody{Code: "SESSION_CHECK_FAILED", Message: "Session state is temporarily unavailable"},
				})
				return
			}
		}

		c.Set(ClaimsKey, claims)
		c.Next()
	}
}

// ClaimsFrom extracts the AuthClaims that Middleware injected into the Gin
// context. Returns (claims, true) on success, or (zero-value, false) if the
// claims are absent or have an unexpected type.
func ClaimsFrom(c *gin.Context) (*models.AuthClaims, bool) {
	raw, exists := c.Get(ClaimsKey)
	if !exists {
		return nil, false
	}
	claims, ok := raw.(*models.AuthClaims)
	return claims, ok
}

// RequireAuth is a convenience middleware for routes that only need a valid
// JWT and not project-scoped RBAC.
func RequireAuth(invalidationKV nats.KeyValue, validators ...SessionValidator) gin.HandlerFunc {
	return Middleware(invalidationKV, validators...)
}

// tokenExpiryFromEnv reads JWT_EXPIRY_HOURS with a safe default.
func tokenExpiryFromEnv() int {
	expiryHours := 8
	if raw := os.Getenv("JWT_EXPIRY_HOURS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			expiryHours = n
		}
	}
	return expiryHours
}

// nowUTC is a test seam.
var nowUTC = func() time.Time { return time.Now().UTC() }
