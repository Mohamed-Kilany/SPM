package audit

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"spm-platform/api/internal/models"
)

// Handler serves the admin audit API.
type Handler struct {
	logger *Logger
}

// NewHandler constructs the audit API handler.
func NewHandler(logger *Logger) *Handler {
	return &Handler{logger: logger}
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type apiErrorEnvelope struct {
	Error apiErrorBody `json:"error"`
}

func writeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, apiErrorEnvelope{Error: apiErrorBody{Code: code, Message: message}})
}

// List handles GET /admin/audit/events.
func (h *Handler) List(c *gin.Context) {
	filter, err := parseFilter(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	page, err := h.logger.List(c.Request.Context(), filter)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list audit events")
		return
	}
	c.JSON(http.StatusOK, page)
}

// Get handles GET /admin/audit/events/:auditId.
func (h *Handler) Get(c *gin.Context) {
	event, err := h.logger.Get(c.Request.Context(), c.Param("auditId"))
	if errors.Is(err, ErrNotFound) {
		writeError(c, http.StatusNotFound, "NOT_FOUND", "Audit event not found")
		return
	}
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load audit event")
		return
	}
	c.JSON(http.StatusOK, event)
}

// Export handles GET /admin/audit/export?format=csv|json.
func (h *Handler) Export(c *gin.Context) {
	filter, err := parseFilter(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	format := strings.ToLower(strings.TrimSpace(c.Query("format")))
	if format == "" {
		format = "csv"
	}
	body, contentType, err := h.logger.Export(c.Request.Context(), filter, format)
	if err != nil {
		if strings.Contains(err.Error(), "format") {
			writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "format must be csv or json")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to export audit events")
		return
	}

	actorID, actorRole := actorFromContext(c)
	h.logger.RecordRequest(c, Entry{
		ActorID:    actorID,
		ActorRole:  actorRole,
		Action:     "audit.exported",
		TargetType: "audit_log",
		TargetID:   format,
		Result:     "success",
		Reason:     describeFilter(filter),
	})

	filename := "audit-" + time.Now().UTC().Format("20060102-150405") + "." + format
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, contentType, body)
}

// Verify handles GET /admin/audit/verify.
func (h *Handler) Verify(c *gin.Context) {
	result, err := h.logger.Verify(c.Request.Context())
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to verify the audit chain")
		return
	}
	c.JSON(http.StatusOK, result)
}

func parseFilter(c *gin.Context) (Filter, error) {
	var filter Filter
	filter.Action = strings.TrimSpace(c.Query("action"))
	if len(filter.Action) > maxActionLength {
		return filter, errors.New("action is too long")
	}
	filter.ActorID = strings.TrimSpace(c.Query("actor_id"))
	if filter.ActorID != "" {
		if _, err := parseUUID(filter.ActorID); err != nil {
			return filter, errors.New("actor_id must be a UUID")
		}
	}
	filter.ProjectID = strings.TrimSpace(c.Query("project_id"))
	if filter.ProjectID != "" {
		if _, err := parseUUID(filter.ProjectID); err != nil {
			return filter, errors.New("project_id must be a UUID")
		}
	}
	filter.Result = strings.ToLower(strings.TrimSpace(c.Query("result")))
	switch filter.Result {
	case "", "success", "failure", "denied":
	default:
		return filter, errors.New("result must be success, failure or denied")
	}
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		parsed, err := parseTimeBound(raw, false)
		if err != nil {
			return filter, errors.New("from must be RFC3339 or YYYY-MM-DD")
		}
		filter.From = parsed
	}
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		parsed, err := parseTimeBound(raw, true)
		if err != nil {
			return filter, errors.New("to must be RFC3339 or YYYY-MM-DD")
		}
		filter.To = parsed
	}
	filter.Cursor = strings.TrimSpace(c.Query("cursor"))
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			return filter, errors.New("limit must be a positive integer")
		}
		filter.Limit = limit
	}
	return filter, nil
}

func parseTimeBound(raw string, endOfDay bool) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed.UTC(), nil
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, err
	}
	if endOfDay {
		parsed = parsed.Add(24 * time.Hour)
	}
	return parsed.UTC(), nil
}

func describeFilter(filter Filter) string {
	parts := make([]string, 0, 4)
	if filter.Action != "" {
		parts = append(parts, "action="+filter.Action)
	}
	if filter.ActorID != "" {
		parts = append(parts, "actor_id="+filter.ActorID)
	}
	if filter.ProjectID != "" {
		parts = append(parts, "project_id="+filter.ProjectID)
	}
	if filter.Result != "" {
		parts = append(parts, "result="+filter.Result)
	}
	if len(parts) == 0 {
		return "all events"
	}
	return strings.Join(parts, ",")
}

func parseUUID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

// actorFromContext reads the claims injected by auth.Middleware without
// importing the auth package, which imports audit for write helpers.
func actorFromContext(c *gin.Context) (string, string) {
	raw, ok := c.Get("auth_claims")
	if !ok {
		return "", ""
	}
	claims, ok := raw.(*models.AuthClaims)
	if !ok || claims == nil {
		return "", ""
	}
	return claims.CanonicalUserID(), claims.Role
}
