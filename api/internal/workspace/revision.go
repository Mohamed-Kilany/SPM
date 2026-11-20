package workspace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"spm-platform/api/internal/metrics"
)

// revisionPrecondition is a parsed optimistic-concurrency token. Expected is
// zero when the caller sent If-Match: * (the row must exist but any revision
// is acceptable).
type revisionPrecondition struct {
	Expected int64
	Any      bool
}

// revisionConflictResponse is the 409 body. current_version is the revision a
// caller must use on retry; the field name is part of the public contract.
type revisionConflictResponse struct {
	Error          errorBody `json:"error"`
	CurrentVersion int64     `json:"current_version"`
}

// revisionTables is an allowlist for the table identifier used by
// currentRevision. Table names are never taken from a request.
var revisionTables = map[string]bool{
	"prds":       true,
	"epics":      true,
	"features":   true,
	"work_items": true,
	"test_cases": true,
	"test_runs":  true,
	"sprints":    true,
	"releases":   true,
}

// revisionFromRequest resolves the expected revision from, in order:
//
//  1. the If-Match request header (`"7"`, `7`, `W/"7"`, or `*`),
//  2. the expected_version query parameter,
//  3. an expected_version field on the parsed JSON body.
//
// Every mutation of a revisioned resource requires a token. A missing token
// is a 428 so a caller cannot accidentally fall back to last-write-wins.
func revisionFromRequest(c *gin.Context, bodyValue *int64) (revisionPrecondition, bool) {
	if raw := strings.TrimSpace(c.GetHeader("If-Match")); raw != "" {
		if raw == "*" {
			return revisionPrecondition{Any: true}, true
		}
		if strings.Contains(raw, ",") {
			apiError(c, http.StatusBadRequest, "INVALID_REVISION", "If-Match must contain exactly one revision")
			return revisionPrecondition{}, false
		}
		value := strings.TrimPrefix(raw, "W/")
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		revision, err := strconv.ParseInt(value, 10, 64)
		if err != nil || revision < 1 {
			apiError(c, http.StatusBadRequest, "INVALID_REVISION", "If-Match must be a positive revision number or *")
			return revisionPrecondition{}, false
		}
		return revisionPrecondition{Expected: revision}, true
	}

	if raw := strings.TrimSpace(c.Query("expected_version")); raw != "" {
		revision, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || revision < 1 {
			apiError(c, http.StatusBadRequest, "INVALID_REVISION", "expected_version must be a positive integer")
			return revisionPrecondition{}, false
		}
		return revisionPrecondition{Expected: revision}, true
	}

	if bodyValue != nil {
		if *bodyValue < 1 {
			apiError(c, http.StatusBadRequest, "INVALID_REVISION", "expected_version must be a positive integer")
			return revisionPrecondition{}, false
		}
		return revisionPrecondition{Expected: *bodyValue}, true
	}

	apiError(c, http.StatusPreconditionRequired, "REVISION_REQUIRED",
		"Provide the current revision in If-Match or expected_version before changing this resource")
	return revisionPrecondition{}, false
}

// revisionClause returns the predicate appended to an UPDATE or DELETE.
// argument is the 1-based positional parameter holding the expected revision.
// A zero value means If-Match: * and therefore an existence check only.
func revisionClause(argument int) string {
	return fmt.Sprintf(" AND ($%d::bigint = 0 OR revision = $%d::bigint)", argument, argument)
}

// revisionValue converts a precondition to the positional argument value.
func revisionValue(pre revisionPrecondition) int64 {
	if pre.Any {
		return 0
	}
	return pre.Expected
}

// setRevisionHeader publishes the new revision as a strong ETag so a client
// can chain a follow-up write without a full reload.
func setRevisionHeader(c *gin.Context, revision int64) {
	c.Header("ETag", `"`+strconv.FormatInt(revision, 10)+`"`)
}

type revisionFailureKind int

const (
	revisionFailureNotFound revisionFailureKind = iota
	revisionFailureConflict
	revisionFailureLookup
)

// classifyRevisionFailure distinguishes a concurrent edit from a deleted row
// once the revisioned UPDATE/DELETE matched nothing. It is separated from the
// HTTP layer so the branch logic is testable without a database.
func classifyRevisionFailure(lookup func() (int64, error)) (revisionFailureKind, int64) {
	current, err := lookup()
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return revisionFailureNotFound, 0
	case err != nil:
		return revisionFailureLookup, 0
	default:
		return revisionFailureConflict, current
	}
}

// revisionFailure writes 404 when the row no longer exists and 409 with the
// current revision when another writer got there first.
func (h *Handler) revisionFailure(c *gin.Context, resource, table string, id, projectID uuid.UUID) {
	kind, current := classifyRevisionFailure(func() (int64, error) {
		return h.currentRevision(c.Request.Context(), table, id, projectID)
	})
	switch kind {
	case revisionFailureNotFound:
		apiError(c, http.StatusNotFound, "NOT_FOUND", resource+" not found")
	case revisionFailureLookup:
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read the current "+resource+" revision")
	default:
		writeRevisionConflict(c, resource, current)
	}
}

// writeRevisionConflict emits the contract 409 body and records the metric.
func writeRevisionConflict(c *gin.Context, resource string, current int64) {
	metrics.Default().ObservePlanningConflict(strings.ToLower(resource))
	setRevisionHeader(c, current)
	c.JSON(http.StatusConflict, revisionConflictResponse{
		Error: errorBody{
			Code:    "REVISION_CONFLICT",
			Message: "The " + resource + " changed since it was loaded. Reload it and retry.",
		},
		CurrentVersion: current,
	})
}

func (h *Handler) currentRevision(ctx context.Context, table string, id, projectID uuid.UUID) (int64, error) {
	if !revisionTables[table] {
		return 0, fmt.Errorf("workspace: %q is not a revisioned table", table)
	}
	var revision int64
	err := h.store.DB().QueryRow(ctx,
		fmt.Sprintf(`SELECT revision FROM %s WHERE id=$1 AND project_id=$2`, table),
		id, projectID).Scan(&revision)
	return revision, err
}
