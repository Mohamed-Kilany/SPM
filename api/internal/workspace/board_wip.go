package workspace

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// boardColumnStatuses mirrors ui/src/lib/workspace.ts boardColumns. A WIP limit
// is keyed by column, so every status that renders in the column counts toward
// the same limit. Keeping the map server-side lets a hard block agree with the
// badge the board shows.
var boardColumnStatuses = map[string][]string{
	"backlog":     {"backlog", "new", "reopened", "cancelled"},
	"ready":       {"ready", "triaged"},
	"in_progress": {"in_progress"},
	"review":      {"in_review", "fixed"},
	"qa":          {"in_qa", "verified"},
	"done":        {"done", "closed"},
	"blocked":     {"blocked"},
}

// boardColumnForStatus returns the column that renders a status. Unknown
// statuses fall back to the backlog column, matching the board's fallback.
func boardColumnForStatus(status string) string {
	for column, statuses := range boardColumnStatuses {
		for _, candidate := range statuses {
			if candidate == status {
				return column
			}
		}
	}
	return "backlog"
}

// validateBoardSettings normalizes the WIP policy before it is stored. Unknown
// columns and out-of-range limits are rejected instead of silently ignored, so
// an operator never believes a limit is active when it is not.
func validateBoardSettings(board *BoardSettings) error {
	normalized := make(map[string]int, len(board.WIPLimits))
	for key, limit := range board.WIPLimits {
		column := strings.ToLower(strings.TrimSpace(key))
		if _, ok := boardColumnStatuses[column]; !ok {
			return fmt.Errorf("Unknown board column %q", key)
		}
		if limit < 0 || limit > 999 {
			return fmt.Errorf("WIP limit for %s must be between 0 and 999", column)
		}
		if limit > 0 {
			normalized[column] = limit
		}
	}
	board.WIPLimits = normalized
	return nil
}

// wipDetails is the structured part of a 409 WIP_LIMIT response. The board can
// explain which column is full without re-reading project settings.
type wipDetails struct {
	Column    string `json:"column"`
	Status    string `json:"status"`
	Current   int    `json:"current"`
	Limit     int    `json:"limit"`
	HardBlock bool   `json:"hard_block"`
}

// enforceWIPLimit applies the project's optional hard WIP block. It returns an
// empty string when the move is allowed (including every project that has not
// opted in), or a bounded failure reason after writing the response. A move
// inside the same column is always allowed because it does not change the
// column count.
func (h *Handler) enforceWIPLimit(c *gin.Context, projectID uuid.UUID, currentStatus, targetStatus string) string {
	if currentStatus != "" && boardColumnForStatus(currentStatus) == boardColumnForStatus(targetStatus) {
		return ""
	}
	var raw []byte
	if err := h.store.DB().QueryRow(c.Request.Context(),
		`SELECT settings FROM projects WHERE id=$1`, projectID).Scan(&raw); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load board policy")
		return "error"
	}
	board := decodeSettings(raw).Board
	if !board.WIPHardBlock {
		return ""
	}
	column := boardColumnForStatus(targetStatus)
	limit := board.WIPLimits[column]
	if limit <= 0 {
		return ""
	}
	var current int
	if err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT count(*) FROM work_items
		WHERE project_id=$1 AND status = ANY($2)`,
		projectID, boardColumnStatuses[column]).Scan(&current); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to check WIP limit")
		return "error"
	}
	if current >= limit {
		c.JSON(http.StatusConflict, gin.H{
			"error": errorBody{
				Code:    "WIP_LIMIT",
				Message: fmt.Sprintf("%s is at its WIP limit of %d", column, limit),
			},
			"wip": wipDetails{
				Column: column, Status: targetStatus, Current: current, Limit: limit, HardBlock: true,
			},
		})
		return "wip"
	}
	return ""
}
