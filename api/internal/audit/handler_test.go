package audit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newAuditRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(NewLogger(nil))
	router := gin.New()
	router.GET("/api/v1/admin/audit/events", handler.List)
	router.GET("/api/v1/admin/audit/events/:auditId", handler.Get)
	router.GET("/api/v1/admin/audit/export", handler.Export)
	router.GET("/api/v1/admin/audit/verify", handler.Verify)
	return router
}

func TestHandlerListRejectsInvalidFilter(t *testing.T) {
	response := httptest.NewRecorder()
	newAuditRouter().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/events?result=maybe", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	var body apiErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("error code = %q, want VALIDATION_ERROR", body.Error.Code)
	}
}

func TestHandlerListReturnsEmptyPageWhenDisabled(t *testing.T) {
	response := httptest.NewRecorder()
	newAuditRouter().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/events", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"events":[]`) {
		t.Fatalf("body = %s, want an empty events array", response.Body.String())
	}
}

func TestHandlerGetReturnsNotFound(t *testing.T) {
	response := httptest.NewRecorder()
	newAuditRouter().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/events/0199b0c4-7d2a-7c31-9e1a-2b3c4d5e6f70", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestHandlerVerifyReturnsOKWhenDisabled(t *testing.T) {
	response := httptest.NewRecorder()
	newAuditRouter().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/verify", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body Verification
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode verification: %v", err)
	}
	if !body.OK {
		t.Fatalf("verification = %+v, want OK", body)
	}
}

func TestHandlerExportSetsDownloadHeaders(t *testing.T) {
	response := httptest.NewRecorder()
	newAuditRouter().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/export?format=csv", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", contentType)
	}
	if disposition := response.Header().Get("Content-Disposition"); !strings.Contains(disposition, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", disposition)
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options is not nosniff")
	}
	if !strings.HasPrefix(response.Body.String(), "id,occurred_at") {
		t.Errorf("CSV body does not start with the header: %s", response.Body.String())
	}
}

func TestHandlerExportRejectsUnknownFormat(t *testing.T) {
	response := httptest.NewRecorder()
	newAuditRouter().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/export?format=xml", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}
