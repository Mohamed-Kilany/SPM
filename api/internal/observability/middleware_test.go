package observability

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRequestIDAndSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID(), SecurityHeaders())
	router.GET("/probe", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"request_id": RequestIDFrom(c)})
	})

	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	request.Header.Set("X-Request-ID", "client-correlation-123")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("X-Request-ID"); got != "client-correlation-123" {
		t.Fatalf("X-Request-ID = %q", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := recorder.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
}

func TestRequestIDRejectsUnsafeValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID())
	router.GET("/probe", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	request.Header.Set("X-Request-ID", "bad value\r\nInjected: yes")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	got := recorder.Header().Get("X-Request-ID")
	if got == "" || got == "bad value\r\nInjected: yes" {
		t.Fatalf("unsafe request ID was not replaced: %q", got)
	}
}

func TestRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RateLimit(1, time.Minute))
	router.GET("/probe", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if first.Code != http.StatusNoContent {
		t.Fatalf("first request status = %d", first.Code)
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d", second.Code)
	}
}
