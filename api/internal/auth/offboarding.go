package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"spm-platform/api/internal/audit"
	"spm-platform/api/internal/metrics"
)

// Open work-item statuses. Terminal statuses are excluded from an offboarding
// reassignment because the work no longer needs an active assignee.
var openWorkItemStatuses = []string{
	"backlog", "ready", "in_progress", "in_review", "in_qa",
	"blocked", "new", "triaged", "fixed", "reopened",
}

// OffboardingProject is a project the departing user owns or co-owns.
type OffboardingProject struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	OwnerCount int    `json:"owner_count"`
	SoleOwner  bool   `json:"sole_owner"`
}

// OffboardingAssignment is an open work item assigned to the departing user.
type OffboardingAssignment struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
}

// OffboardingApproval is a pending deployment approval held by the user.
type OffboardingApproval struct {
	ID           string `json:"id"`
	DeploymentID string `json:"deployment_id"`
	ProjectID    string `json:"project_id"`
	ProjectName  string `json:"project_name"`
}

// OffboardingImpact is the preview an admin must review before deactivating a
// user. Every item that can be reassigned is listed so the decision is explicit.
type OffboardingImpact struct {
	UserID               string                  `json:"user_id"`
	Username             string                  `json:"username"`
	DisplayName          string                  `json:"display_name"`
	Role                 string                  `json:"role"`
	IsActive             bool                    `json:"is_active"`
	IsLastAdmin          bool                    `json:"is_last_admin"`
	OwnedProjects        []OffboardingProject    `json:"owned_projects"`
	OpenAssignments      []OffboardingAssignment `json:"open_assignments"`
	PendingApprovals     []OffboardingApproval   `json:"pending_approvals"`
	OwnedArtifacts       map[string]int          `json:"owned_artifacts"`
	ActiveSessions       int                     `json:"active_sessions"`
	RequiresReassignment bool                    `json:"requires_reassignment"`
	Warnings             []string                `json:"warnings"`
}

type offboardingRequest struct {
	Reason        string            `json:"reason"`
	ReassignTo    string            `json:"reassign_to"`
	ProjectOwners map[string]string `json:"project_owners"`
}

type offboardingResult struct {
	Impact              OffboardingImpact `json:"impact"`
	ReassignedProjects  int               `json:"reassigned_projects"`
	ReassignedItems     int               `json:"reassigned_items"`
	ReassignedApprovals int               `json:"reassigned_approvals"`
	ReassignedArtifacts int               `json:"reassigned_artifacts"`
	RevokedSessions     int               `json:"revoked_sessions"`
}

// OffboardingImpact handles GET /admin/users/:userId/offboarding.
func (h *Handler) OffboardingImpact(c *gin.Context) {
	targetID, ok := h.offboardingTarget(c)
	if !ok {
		return
	}
	impact, err := h.loadOffboardingImpact(c.Request.Context(), targetID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, errorResponse{Error: errorBody{Code: "NOT_FOUND", Message: "User not found"}})
			return
		}
		c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to load offboarding impact"}})
		return
	}
	c.JSON(http.StatusOK, impact)
}

// OffboardUser handles POST /admin/users/:userId/offboarding. It reassigns
// owned projects, open assignments, pending approvals and owned artifacts,
// revokes every session, and deactivates the account in one transaction.
func (h *Handler) OffboardUser(c *gin.Context) {
	targetID, ok := h.offboardingTarget(c)
	if !ok {
		return
	}
	claims, _ := ClaimsFrom(c)
	actorID := ""
	actorRole := ""
	if claims != nil {
		actorID = claims.CanonicalUserID()
		actorRole = claims.Role
	}
	if targetID.String() == actorID {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "SELF_LOCKOUT", Message: "You cannot deactivate your own account"}})
		return
	}

	var req offboardingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "BAD_REQUEST", Message: "Request body is not valid JSON"}})
		return
	}
	reason, err := normalizeOffboardingReason(req.Reason)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "A reason between 3 and 500 characters is required"}})
		return
	}
	reassignTo, err := parseOffboardingSuccessor(req.ReassignTo)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "reassign_to must be a UUID"}})
		return
	}
	overrides, err := parseProjectOwnerOverrides(req.ProjectOwners)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "VALIDATION_ERROR", Message: "project_owners keys and values must be UUIDs"}})
		return
	}

	ctx := c.Request.Context()
	impact, result, err := h.offboardUser(ctx, targetID, reassignTo, overrides)
	if err != nil {
		switch {
		case errors.Is(err, errOffboardingSelf):
			c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "SELF_LOCKOUT", Message: "You cannot deactivate your own account"}})
		case errors.Is(err, errOffboardingInactive):
			c.JSON(http.StatusConflict, errorResponse{Error: errorBody{Code: "ALREADY_INACTIVE", Message: "The user is already inactive"}})
		case errors.Is(err, errOffboardingLastAdmin):
			c.JSON(http.StatusConflict, errorResponse{Error: errorBody{Code: "LAST_ADMIN", Message: "At least one active admin must remain"}})
		case errors.Is(err, errOffboardingReassignment):
			c.JSON(http.StatusConflict, errorResponse{Error: errorBody{Code: "REASSIGNMENT_REQUIRED", Message: "Reassign the affected projects, assignments, approvals and artifacts before deactivation"}})
		case errors.Is(err, errOffboardingSuccessor):
			c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "INVALID_SUCCESSOR", Message: "The reassignment target must be an active user other than the departing user"}})
		default:
			c.JSON(http.StatusInternalServerError, errorResponse{Error: errorBody{Code: "INTERNAL_ERROR", Message: "Failed to deactivate the user"}})
		}
		return
	}

	for _, sessionID := range result.revokedSessionIDs {
		h.invalidateSession(ctx, sessionID, targetID.String(), "offboarding")
	}
	if h.invalidator != nil {
		_ = h.invalidator.InvalidateUser(ctx, targetID.String())
	}
	for _, transfer := range result.projectTransfers {
		h.recordAuth(c, audit.Entry{
			ActorID: actorID, ActorRole: actorRole, Action: "project.ownership.transfer",
			ProjectID: transfer.ProjectID, TargetType: "project", TargetID: transfer.ProjectID,
			Result: "success", Reason: reason,
			Before: map[string]any{"owner_id": transfer.From},
			After:  map[string]any{"owner_id": transfer.To, "via": "offboarding"},
		})
	}
	h.recordAuth(c, audit.Entry{
		ActorID: actorID, ActorRole: actorRole, Action: "admin.user.offboard",
		TargetType: "user", TargetID: targetID.String(), Result: "success",
		Reason: reason,
		Before: map[string]any{"is_active": true, "role": impact.Role},
		After: map[string]any{
			"is_active":           false,
			"reassign_to":         reassignTo.String(),
			"owned_projects":      len(impact.OwnedProjects),
			"open_assignments":    len(impact.OpenAssignments),
			"pending_approvals":   len(impact.PendingApprovals),
			"owned_artifacts":     impact.OwnedArtifacts,
			"revoked_sessions":    result.RevokedSessions,
			"reassigned_projects": result.ReassignedProjects,
		},
	})
	c.JSON(http.StatusOK, offboardingResult{
		Impact:              impact,
		ReassignedProjects:  result.ReassignedProjects,
		ReassignedItems:     result.ReassignedItems,
		ReassignedApprovals: result.ReassignedApprovals,
		ReassignedArtifacts: result.ReassignedArtifacts,
		RevokedSessions:     result.RevokedSessions,
	})
}

var (
	errOffboardingSelf         = errors.New("offboarding self")
	errOffboardingInactive     = errors.New("offboarding inactive user")
	errOffboardingLastAdmin    = errors.New("offboarding last admin")
	errOffboardingReassignment = errors.New("offboarding reassignment required")
	errOffboardingSuccessor    = errors.New("offboarding invalid successor")
	errOffboardingReason       = errors.New("offboarding reason invalid")
	errOffboardingID           = errors.New("offboarding ID invalid")
)

// normalizeOffboardingReason trims the mandatory audit reason and enforces the
// 3 to 500 character contract.
func normalizeOffboardingReason(raw string) (string, error) {
	reason := strings.TrimSpace(raw)
	if len(reason) < 3 || len(reason) > 500 {
		return "", errOffboardingReason
	}
	return reason, nil
}

// parseOffboardingSuccessor accepts an empty value or a user UUID.
func parseOffboardingSuccessor(raw string) (uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errOffboardingID
	}
	return id, nil
}

// parseProjectOwnerOverrides converts the optional per-project successors map
// into typed IDs so the transaction never reparses raw request strings.
func parseProjectOwnerOverrides(raw map[string]string) (map[uuid.UUID]uuid.UUID, error) {
	overrides := make(map[uuid.UUID]uuid.UUID, len(raw))
	for rawProject, rawUser := range raw {
		projectID, err := uuid.Parse(strings.TrimSpace(rawProject))
		if err != nil {
			return nil, errOffboardingID
		}
		userID, err := uuid.Parse(strings.TrimSpace(rawUser))
		if err != nil {
			return nil, errOffboardingID
		}
		overrides[projectID] = userID
	}
	return overrides, nil
}

// impactRequiresReassignment is the single predicate behind the preview flag,
// the generic deactivation guard and the transactional check.
func impactRequiresReassignment(impact OffboardingImpact) bool {
	if len(impact.OwnedProjects) > 0 ||
		len(impact.OpenAssignments) > 0 ||
		len(impact.PendingApprovals) > 0 {
		return true
	}
	for _, count := range impact.OwnedArtifacts {
		if count > 0 {
			return true
		}
	}
	return false
}

type offboardingTxResult struct {
	ReassignedProjects  int
	ReassignedItems     int
	ReassignedApprovals int
	ReassignedArtifacts int
	RevokedSessions     int
	revokedSessionIDs   []string
	projectTransfers    []offboardingProjectTransfer
}

type offboardingProjectTransfer struct {
	ProjectID string
	From      string
	To        string
}

func (h *Handler) offboardUser(ctx context.Context, targetID, reassignTo uuid.UUID, overrides map[uuid.UUID]uuid.UUID) (OffboardingImpact, offboardingTxResult, error) {
	var impact OffboardingImpact
	var result offboardingTxResult
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return impact, result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var active bool
	var role string
	if err := tx.QueryRow(ctx,
		`SELECT is_active, role FROM users WHERE id = $1 FOR UPDATE`, targetID).Scan(&active, &role); err != nil {
		return impact, result, err
	}
	if !active {
		return impact, result, errOffboardingInactive
	}
	if role == RoleAdmin {
		var remaining int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE role = 'admin' AND is_active = TRUE AND id <> $1`, targetID).Scan(&remaining); err != nil {
			return impact, result, err
		}
		if remaining == 0 {
			return impact, result, errOffboardingLastAdmin
		}
	}

	impact, err = h.loadOffboardingImpactTx(ctx, tx, targetID)
	if err != nil {
		return impact, result, err
	}
	if impact.RequiresReassignment && reassignTo == uuid.Nil {
		return impact, result, errOffboardingReassignment
	}
	if reassignTo != uuid.Nil {
		if err := validateSuccessor(ctx, tx, reassignTo, targetID); err != nil {
			return impact, result, err
		}
	}
	for projectID, userID := range overrides {
		if err := validateSuccessor(ctx, tx, userID, targetID); err != nil {
			return impact, result, err
		}
		owned := false
		for _, project := range impact.OwnedProjects {
			if project.ID == projectID.String() {
				owned = true
				break
			}
		}
		if !owned {
			return impact, result, errOffboardingSuccessor
		}
	}

	// Reassign assignments, approvals and owned artifacts first. This adds the
	// successor as a developer where needed; owned projects are promoted to
	// owner afterwards.
	if reassignTo != uuid.Nil {
		if err := h.reassignWorkItems(ctx, tx, targetID, reassignTo); err != nil {
			return impact, result, err
		}
		result.ReassignedItems = len(impact.OpenAssignments)
		if err := h.reassignApprovals(ctx, tx, targetID, reassignTo); err != nil {
			return impact, result, err
		}
		result.ReassignedApprovals = len(impact.PendingApprovals)
		count, err := h.reassignArtifacts(ctx, tx, targetID, reassignTo)
		if err != nil {
			return impact, result, err
		}
		result.ReassignedArtifacts = count
	}

	// Move each owned project to its chosen successor (per-project override or
	// the default target).
	for _, project := range impact.OwnedProjects {
		projectID, _ := uuid.Parse(project.ID)
		successor := reassignTo
		if override, ok := overrides[projectID]; ok {
			successor = override
		}
		if successor == uuid.Nil {
			continue
		}
		fromID, err := h.transferProjectInTx(ctx, tx, projectID, targetID, successor)
		if err != nil {
			return impact, result, err
		}
		result.ReassignedProjects++
		result.projectTransfers = append(result.projectTransfers, offboardingProjectTransfer{
			ProjectID: project.ID, From: fromID.String(), To: successor.String(),
		})
	}

	// Remove the departing user's remaining memberships so no stale access row
	// survives. Owned projects were transferred above.
	if _, err := tx.Exec(ctx, `DELETE FROM project_members WHERE user_id = $1`, targetID); err != nil {
		return impact, result, err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET is_active = FALSE, invalidated_at = NOW(), updated_at = NOW() WHERE id = $1`, targetID); err != nil {
		return impact, result, err
	}

	rows, err := tx.Query(ctx, `
		UPDATE auth_sessions SET revoked_at = NOW(), revoke_reason = 'offboarding'
		WHERE user_id = $1 AND revoked_at IS NULL
		RETURNING id::text`, targetID)
	if err != nil {
		return impact, result, err
	}
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			rows.Close()
			return impact, result, err
		}
		result.revokedSessionIDs = append(result.revokedSessionIDs, sessionID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return impact, result, err
	}
	result.RevokedSessions = len(result.revokedSessionIDs)

	if err := tx.Commit(ctx); err != nil {
		return impact, result, err
	}
	if result.RevokedSessions > 0 {
		metrics.Default().ObserveSessionEvent("revoke_all")
	}
	return impact, result, nil
}

func validateSuccessor(ctx context.Context, tx pgx.Tx, successor, target uuid.UUID) error {
	if successor == target {
		return errOffboardingSuccessor
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT is_active FROM users WHERE id = $1`, successor).Scan(&active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errOffboardingSuccessor
		}
		return err
	}
	if !active {
		return errOffboardingSuccessor
	}
	return nil
}

func (h *Handler) transferProjectInTx(ctx context.Context, tx pgx.Tx, projectID, from, to uuid.UUID) (uuid.UUID, error) {
	var ownerID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT user_id FROM project_members
		WHERE project_id = $1 AND role = 'owner'
		ORDER BY updated_at DESC LIMIT 1`, projectID).Scan(&ownerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ownerID = from
		} else {
			return uuid.Nil, err
		}
	}
	if ownerID != from {
		return uuid.Nil, errOffboardingSuccessor
	}
	if _, err := tx.Exec(ctx, `
		UPDATE project_members SET role = 'maintainer', updated_at = NOW()
		WHERE project_id = $1 AND role = 'owner' AND user_id <> $2`, projectID, to); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role)
		VALUES ($1, $2, 'owner')
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = 'owner', updated_at = NOW()`,
		projectID, to); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE projects SET owner_id = $1, updated_at = NOW() WHERE id = $2`, to, projectID); err != nil {
		return uuid.Nil, err
	}
	return ownerID, nil
}

func (h *Handler) reassignWorkItems(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role)
		SELECT DISTINCT project_id, $1::uuid, 'developer'
		FROM work_items
		WHERE assignee_id = $2 AND status = ANY($3::text[])
		ON CONFLICT (project_id, user_id) DO NOTHING`, to, from, openWorkItemStatuses); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE work_items SET assignee_id = $1, updated_at = NOW()
		WHERE assignee_id = $2 AND status = ANY($3::text[])`, to, from, openWorkItemStatuses)
	return err
}

func (h *Handler) reassignApprovals(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role)
		SELECT DISTINCT d.project_id, $1::uuid, 'developer'
		FROM deployment_approvals a
		JOIN deployments d ON d.id = a.deployment_id
		WHERE a.approver_id = $2 AND a.status = 'pending'
		ON CONFLICT (project_id, user_id) DO NOTHING`, to, from); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE deployment_approvals SET approver_id = $1
		WHERE approver_id = $2 AND status = 'pending'`, to, from)
	return err
}

func (h *Handler) reassignArtifacts(ctx context.Context, tx pgx.Tx, from, to uuid.UUID) (int, error) {
	tables := []string{"prds", "epics", "features", "test_cases", "test_plans"}
	total := 0
	for _, table := range tables {
		// Table names are constants from the fixed list above.
		if _, err := tx.Exec(ctx, fmt.Sprintf(`
			INSERT INTO project_members (project_id, user_id, role)
			SELECT DISTINCT project_id, $1::uuid, 'developer'
			FROM %s WHERE owner_id = $2
			ON CONFLICT (project_id, user_id) DO NOTHING`, table), to, from); err != nil {
			return total, err
		}
		tag, err := tx.Exec(ctx, fmt.Sprintf(`
			UPDATE %s SET owner_id = $1, updated_at = NOW() WHERE owner_id = $2`, table), to, from)
		if err != nil {
			return total, err
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}

func (h *Handler) loadOffboardingImpact(ctx context.Context, targetID uuid.UUID) (OffboardingImpact, error) {
	return h.loadOffboardingImpactTx(ctx, h.db, targetID)
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
}

func (h *Handler) loadOffboardingImpactTx(ctx context.Context, q rowQuerier, targetID uuid.UUID) (OffboardingImpact, error) {
	var impact OffboardingImpact
	impact.OwnedArtifacts = map[string]int{}
	if err := q.QueryRow(ctx,
		`SELECT id::text, username, COALESCE(display_name, ''), role, is_active FROM users WHERE id = $1`,
		targetID).Scan(&impact.UserID, &impact.Username, &impact.DisplayName, &impact.Role, &impact.IsActive); err != nil {
		return impact, err
	}
	if impact.Role == RoleAdmin && impact.IsActive {
		var remaining int
		if err := q.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE role = 'admin' AND is_active = TRUE AND id <> $1`, targetID).Scan(&remaining); err != nil {
			return impact, err
		}
		impact.IsLastAdmin = remaining == 0
	}
	if impact.IsLastAdmin {
		impact.Warnings = append(impact.Warnings, "This is the last active admin; deactivation is blocked.")
	}

	rows, err := q.Query(ctx, `
		SELECT p.id::text, p.name, p.status,
		       (SELECT COUNT(*) FROM project_members o WHERE o.project_id = p.id AND o.role = 'owner')
		FROM projects p
		WHERE EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = p.id AND pm.user_id = $1 AND pm.role = 'owner')
		   OR (p.owner_id = $1 AND NOT EXISTS (SELECT 1 FROM project_members o WHERE o.project_id = p.id AND o.role = 'owner'))
		ORDER BY p.name`, targetID)
	if err != nil {
		return impact, err
	}
	for rows.Next() {
		var project OffboardingProject
		if err := rows.Scan(&project.ID, &project.Name, &project.Status, &project.OwnerCount); err != nil {
			rows.Close()
			return impact, err
		}
		project.SoleOwner = project.OwnerCount <= 1
		impact.OwnedProjects = append(impact.OwnedProjects, project)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return impact, err
	}

	rows, err = q.Query(ctx, `
		SELECT w.id::text, p.key_prefix, w.item_number, w.title, w.status,
		       p.id::text, p.name
		FROM work_items w
		JOIN projects p ON p.id = w.project_id
		WHERE w.assignee_id = $1 AND w.status = ANY($2::text[])
		ORDER BY w.updated_at DESC
		LIMIT 200`, targetID, openWorkItemStatuses)
	if err != nil {
		return impact, err
	}
	for rows.Next() {
		var item OffboardingAssignment
		var prefix string
		var number int64
		if err := rows.Scan(&item.ID, &prefix, &number, &item.Title, &item.Status, &item.ProjectID, &item.ProjectName); err != nil {
			rows.Close()
			return impact, err
		}
		item.Key = fmt.Sprintf("%s-%d", prefix, number)
		impact.OpenAssignments = append(impact.OpenAssignments, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return impact, err
	}

	rows, err = q.Query(ctx, `
		SELECT a.id::text, a.deployment_id::text, p.id::text, p.name
		FROM deployment_approvals a
		JOIN deployments d ON d.id = a.deployment_id
		JOIN projects p ON p.id = d.project_id
		WHERE a.approver_id = $1 AND a.status = 'pending'
		ORDER BY a.created_at DESC
		LIMIT 200`, targetID)
	if err != nil {
		return impact, err
	}
	for rows.Next() {
		var approval OffboardingApproval
		if err := rows.Scan(&approval.ID, &approval.DeploymentID, &approval.ProjectID, &approval.ProjectName); err != nil {
			rows.Close()
			return impact, err
		}
		impact.PendingApprovals = append(impact.PendingApprovals, approval)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return impact, err
	}

	for _, table := range []string{"prds", "epics", "features", "test_cases", "test_plans"} {
		var count int
		if err := q.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE owner_id = $1`, table), targetID).Scan(&count); err != nil {
			return impact, err
		}
		impact.OwnedArtifacts[table] = count
	}
	if err := q.QueryRow(ctx, `
		SELECT COUNT(*) FROM auth_sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()`, targetID).Scan(&impact.ActiveSessions); err != nil {
		return impact, err
	}

	impact.RequiresReassignment = impactRequiresReassignment(impact)
	if impact.RequiresReassignment {
		impact.Warnings = append(impact.Warnings, "Choose a successor before deactivation; affected ownership and open work will move to that user.")
	}
	if impact.ActiveSessions > 0 {
		impact.Warnings = append(impact.Warnings, fmt.Sprintf("%d active session(s) will be revoked immediately.", impact.ActiveSessions))
	}
	return impact, nil
}

func (h *Handler) offboardingTarget(c *gin.Context) (uuid.UUID, bool) {
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: errorBody{Code: "INVALID_ID", Message: "User ID is not a valid UUID"}})
		return uuid.Nil, false
	}
	return targetID, true
}
