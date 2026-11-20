// Package observability provides request correlation, structured access logs,
// security headers and a small in-process rate limiter for the API edge.
package observability

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const requestIDKey = "spm.request_id"

// RequestID accepts a caller-supplied correlation ID or generates one. The ID
// is echoed on the response and included in every structured access log line.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if !validRequestID(id) {
			var raw [12]byte
			if _, err := rand.Read(raw[:]); err == nil {
				id = hex.EncodeToString(raw[:])
			} else {
				id = time.Now().UTC().Format("20060102150405.000000000")
			}
		}
		c.Set(requestIDKey, id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

// RequestIDFrom returns the correlation ID stored by RequestID.
func RequestIDFrom(c *gin.Context) string {
	if value, ok := c.Get(requestIDKey); ok {
		if id, ok := value.(string); ok {
			return id
		}
	}
	return ""
}

// AccessLog emits one structured line per request without logging bodies or
// authorization headers.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		status := c.Writer.Status()
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}
		// Paths and forwarded client addresses are caller-controlled. Strip CR
		// and LF so they cannot forge log entries (CWE-117).
		safePath := strings.ReplaceAll(c.Request.URL.Path, "\n", "")
		safePath = strings.ReplaceAll(safePath, "\r", "")
		safeClientIP := strings.ReplaceAll(c.ClientIP(), "\n", "")
		safeClientIP = strings.ReplaceAll(safeClientIP, "\r", "")
		slog.Log(c.Request.Context(), level, "http_request",
			"request_id", RequestIDFrom(c),
			"method", c.Request.Method,
			"path", safePath,
			"status", status,
			"latency_ms", time.Since(started).Milliseconds(),
			"client_ip", safeClientIP,
			"bytes", c.Writer.Size(),
		)
	}
}

// Recovery converts panics into a correlated JSON 500 response.
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		slog.Error("panic_recovered", "request_id", RequestIDFrom(c), "error", recovered)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "INTERNAL_ERROR", "message": "An unexpected error occurred"},
		})
	})
}

// SecurityHeaders applies conservative API response headers. The SPA edge has
// its own CSP in nginx; these headers protect direct API access.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Header("Cross-Origin-Resource-Policy", "same-origin")
		c.Next()
	}
}

type rateWindow struct {
	start time.Time
	count int
}

// RateLimit limits requests per client IP in a fixed window. It is deliberately
// in-process: the local/offline deployment has one API replica. A distributed
// deployment should replace it with a shared limiter at the edge.
func RateLimit(limit int, window time.Duration) gin.HandlerFunc {
	var mu sync.Mutex
	windows := map[string]rateWindow{}
	lastSweep := time.Now()
	return func(c *gin.Context) {
		now := time.Now()
		key := c.ClientIP()
		mu.Lock()
		if now.Sub(lastSweep) > window*4 {
			for entryKey, entry := range windows {
				if now.Sub(entry.start) > window {
					delete(windows, entryKey)
				}
			}
			lastSweep = now
		}
		entry := windows[key]
		if entry.start.IsZero() || now.Sub(entry.start) >= window {
			entry = rateWindow{start: now, count: 0}
		}
		entry.count++
		windows[key] = entry
		allowed := entry.count <= limit
		retryAfter := window - now.Sub(entry.start)
		mu.Unlock()
		if !allowed {
			c.Header("Retry-After", strings.TrimSuffix(retryAfter.Round(time.Second).String(), "0s"))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{"code": "RATE_LIMITED", "message": "Too many requests. Please retry later."},
			})
			return
		}
		c.Next()
	}
}
