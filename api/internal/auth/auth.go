// Package auth provides JWT issuance, parsing, and Gin middleware for
// the SPM platform. All tokens are signed with HMAC-SHA256.
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"spm-platform/api/internal/models"
)

// IssueToken creates and signs a JWT for the given user. It is kept for
// callers that do not use persistent sessions; new sign-ins should call
// IssueSessionToken so the token can be revoked individually.
func IssueToken(userID, role string, projectIDs []string, secret string, expiryHours int) (string, error) {
	if expiryHours <= 0 {
		return "", errors.New("auth: expiryHours must be greater than zero")
	}
	return IssueSessionToken(userID, role, projectIDs, secret,
		time.Duration(expiryHours)*time.Hour, "", "")
}

// IssueSessionToken creates and signs a short-lived access token bound to a
// persistent session. sessionID becomes the "sid" claim and jti becomes the
// standard "jti" claim; both are checked by the session middleware.
func IssueSessionToken(userID, role string, projectIDs []string, secret string, ttl time.Duration, sessionID, jti string) (string, error) {
	if secret == "" {
		return "", errors.New("auth: signing secret must not be empty")
	}
	if ttl <= 0 {
		return "", errors.New("auth: token TTL must be greater than zero")
	}
	if userID == "" {
		return "", errors.New("auth: user ID must not be empty")
	}

	now := time.Now()
	claims := models.AuthClaims{
		UserID:     userID,
		SessionID:  sessionID,
		Role:       role,
		ProjectIDs: projectIDs,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    "spm-platform",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("auth: failed to sign token: %w", err)
	}
	return signed, nil
}

// ParseToken parses and validates the given JWT string, returning the embedded
// AuthClaims on success. Only HS256 is accepted.
func ParseToken(tokenStr, secret string) (*models.AuthClaims, error) {
	if tokenStr == "" {
		return nil, errors.New("auth: token string must not be empty")
	}
	if secret == "" {
		return nil, errors.New("auth: signing secret must not be empty")
	}

	claims := &models.AuthClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("auth: unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("auth: token is not valid")
	}
	claims.Normalize()
	if claims.CanonicalUserID() == "" {
		return nil, errors.New("auth: token has no subject")
	}
	return claims, nil
}
