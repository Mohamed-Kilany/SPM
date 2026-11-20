package workspace

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"spm-platform/api/internal/audit"
)

// GetSettings returns the effective project settings. Projects created before
// settings existed receive the documented defaults without a data migration.
func (h *Handler) GetSettings(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	w, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
		return
	}
	c.JSON(http.StatusOK, w.Project.Settings)
}

// UpdateSettings updates merge and delivery automation policy.
func (h *Handler) UpdateSettings(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req ProjectSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if strings.TrimSpace(req.AutoDeployEnv) == "" {
		req.AutoDeployEnv = "staging"
	}
	var previous []byte
	if err := h.store.DB().QueryRow(c.Request.Context(),
		`SELECT settings FROM projects WHERE id = $1`, projectID).Scan(&previous); err != nil {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
		return
	}
	body, err := json.Marshal(req)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Settings could not be serialized")
		return
	}
	tag, err := h.store.DB().Exec(c.Request.Context(), `
		UPDATE projects SET settings=$2, updated_at=NOW() WHERE id=$1`, projectID, body)
	if err != nil || tag.RowsAffected() == 0 {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update settings")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "project_settings", nil, "updated",
		"Updated merge and delivery policy", map[string]interface{}{
			"require_linked_work_item": req.MergePolicy.RequireLinkedWorkItem,
			"require_approved_review":  req.MergePolicy.RequireApprovedReview,
			"require_passing_checks":   req.MergePolicy.RequirePassingChecks,
		})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "project.settings.update", TargetType: "project", TargetID: projectID.String(),
		Result: "success", Before: json.RawMessage(previous), After: req,
	})
	c.JSON(http.StatusOK, req)
}
