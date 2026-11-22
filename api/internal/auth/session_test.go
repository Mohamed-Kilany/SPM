package auth

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionConfigDefaults(t *testing.T) {
	cfg := sessionConfigFromLookup(func(string) string { return "" })

	assert.Equal(t, 15*time.Minute, cfg.AccessTTL)
	assert.Equal(t, 30*24*time.Hour, cfg.RefreshTTL)
	assert.Equal(t, 24*time.Hour, cfg.IdleTimeout)
	assert.Equal(t, 20, cfg.LimitPerUser)
	assert.Equal(t, 30*24*time.Hour, cfg.Retention)
}

func TestSessionConfigFromEnvironment(t *testing.T) {
	values := map[string]string{
		"ACCESS_TOKEN_TTL_MINUTES":     "20",
		"JWT_EXPIRY_HOURS":             "9",
		"REFRESH_TOKEN_TTL_DAYS":       "14",
		"SESSION_IDLE_TIMEOUT_MINUTES": "90",
		"SESSION_LIMIT_PER_USER":       "5",
		"SESSION_RETENTION_DAYS":       "7",
	}
	cfg := sessionConfigFromLookup(func(key string) string { return values[key] })

	assert.Equal(t, 20*time.Minute, cfg.AccessTTL, "ACCESS_TOKEN_TTL_MINUTES wins over the legacy variable")
	assert.Equal(t, 14*24*time.Hour, cfg.RefreshTTL)
	assert.Equal(t, 90*time.Minute, cfg.IdleTimeout)
	assert.Equal(t, 5, cfg.LimitPerUser)
	assert.Equal(t, 7*24*time.Hour, cfg.Retention)
}

func TestSessionConfigLegacyAccessTTL(t *testing.T) {
	cfg := sessionConfigFromLookup(func(key string) string {
		if key == "JWT_EXPIRY_HOURS" {
			return "8"
		}
		return ""
	})
	assert.Equal(t, 8*time.Hour, cfg.AccessTTL)
}

func TestSessionConfigIgnoresInvalidValues(t *testing.T) {
	values := map[string]string{
		"ACCESS_TOKEN_TTL_MINUTES":     "-5",
		"REFRESH_TOKEN_TTL_DAYS":       "zero",
		"SESSION_IDLE_TIMEOUT_MINUTES": "0",
		"SESSION_LIMIT_PER_USER":       "-1",
		"SESSION_RETENTION_DAYS":       "abc",
	}
	cfg := sessionConfigFromLookup(func(key string) string { return values[key] })

	assert.Equal(t, defaultAccessTTL, cfg.AccessTTL)
	assert.Equal(t, defaultRefreshTTL, cfg.RefreshTTL)
	assert.Equal(t, defaultIdleTimeout, cfg.IdleTimeout)
	assert.Equal(t, defaultSessionLimit, cfg.LimitPerUser)
	assert.Equal(t, defaultSessionRetention, cfg.Retention)
}

func TestSessionConfigAllowsZeroSessionLimit(t *testing.T) {
	cfg := sessionConfigFromLookup(func(key string) string {
		if key == "SESSION_LIMIT_PER_USER" {
			return "0"
		}
		return ""
	})
	assert.Equal(t, 0, cfg.LimitPerUser, "zero disables the concurrent-session cap")
}

func TestSessionConfigIdleTimeoutIsCappedByRefreshTTL(t *testing.T) {
	values := map[string]string{
		"REFRESH_TOKEN_TTL_DAYS":       "1",
		"SESSION_IDLE_TIMEOUT_MINUTES": "2880",
	}
	cfg := sessionConfigFromLookup(func(key string) string { return values[key] })
	assert.Equal(t, 24*time.Hour, cfg.IdleTimeout)
}

func TestDeviceLabel(t *testing.T) {
	cases := map[string]string{
		"": "Unknown device",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0 Safari/537.36":                      "Chrome on Windows",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 13_4) AppleWebKit/605.1.15 Version/17.0 Safari/605.1.15":               "Safari on macOS",
		"Mozilla/5.0 (X11; Linux x86_64; rv:120.0) Gecko/20100101 Firefox/120.0":                                       "Firefox on Linux",
		"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/120.0 Mobile Safari/537.36":                         "Chrome on Android",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile Safari/604.1": "Safari on iOS",
		"Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/120.0 Safari/537.36 Edg/120.0":                        "Edge on Windows",
		"curl/8.4.0":            "curl client",
		"PostmanRuntime/7.36.0": "Postman client",
		"Playwright/1.40":       "Playwright client",
		"something-weird":       "Unknown browser on Unknown OS",
	}
	for userAgent, want := range cases {
		assert.Equal(t, want, DeviceLabel(userAgent), "user agent %q", userAgent)
	}
}

func TestRefreshTokenHashing(t *testing.T) {
	first, err := newRefreshToken()
	require.NoError(t, err)
	second, err := newRefreshToken()
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
	assert.Len(t, first, 43, "32 random bytes encode to 43 base64url characters")
	assert.NotContains(t, first, "=", "raw base64url must not be padded")

	hash := hashRefreshToken(first)
	assert.Len(t, hash, 64)
	_, err = hex.DecodeString(hash)
	require.NoError(t, err)
	assert.Equal(t, hash, hashRefreshToken(first))
	assert.NotEqual(t, hash, hashRefreshToken(second))
}

func TestTruncateUserAgent(t *testing.T) {
	assert.Equal(t, "", truncateUserAgent("   "))
	assert.Equal(t, "plain", truncateUserAgent("plain"))
	assert.Equal(t, "ab", truncateUserAgent("a\x00b"))

	long := strings.Repeat("x", maxUserAgentLength+50)
	assert.Len(t, truncateUserAgent(long), maxUserAgentLength)

	multibyte := strings.Repeat("é", maxUserAgentLength+10)
	assert.Equal(t, maxUserAgentLength, len([]rune(truncateUserAgent(multibyte))))
}

func TestRefreshReuseErrorUnwraps(t *testing.T) {
	err := &RefreshReuseError{SessionID: "session-1", UserID: "user-1"}
	assert.True(t, errors.Is(err, ErrRefreshTokenReuse))
	assert.Contains(t, err.Error(), "session-1")
}
