package workspace

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"spm-platform/api/internal/audit"
	"spm-platform/api/internal/auth"
)

// OwnershipTransfer is the API projection of one ownership transfer request.
type OwnershipTransfer struct {
	ID              string     `json:"id"`
	ProjectID       string     `json:"project_id"`
	ProjectName     string     `json:"project_name"`
	FromUserID      string     `json:"from_user_id"`
	FromUser        string     `json:"from_user"`
	ToUserID        string     `json:"to_user_id"`
	ToUser          string     `json:"to_user"`
	RequestedBy     string     `json:"requested_by"`
	RequestedByName string     `json:"requested_by_name"`
	Status          string     `json:"status"`
	Reason          string     `json:"reason"`
	CreatedAt       time.Time  `json:"created_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy      *string    `json:"resolved_by,omitempty"`
}

type ownershipTransferRequest struct {
	ToUserID string `json:"to_user_id"`
	Reason   string `json:"reason"`
}

type myOwnershipTransfers struct {
	Incoming []OwnershipTransfer `json:"incoming"`
	Outgoing []OwnershipTransfer `json:"outgoing"`
}

const ownershipTransferSelect = `
	SELECT t.id::text, t.project_id::text, p.name, t.from_user_id::text,
	       COALESCE(NULLIF(fu.display_name, ''), fu.username, ''),
	       t.to_user_id::text, COALESCE(NULLIF(tu.display_name, ''), tu.username, ''),
	       t.requested_by::text, COALESCE(NULLIF(ru.display_name, ''), ru.username, ''),
	       t.status, t.reason, t.created_at, t.resolved_at,
	       t.resolved_by::text
	FROM ownership_transfers t
	JOIN projects p ON p.id = t.project_id
	JOIN users fu ON fu.id = t.from_user_id
	JOIN users tu ON tu.id = t.to_user_id
	JOIN users ru ON ru.id = t.requested_by`

func scanOwnershipTransfer(row pgx.Row) (OwnershipTransfer, error) {
	var t OwnershipTransfer
	err := row.Scan(&t.ID, &t.ProjectID, &t.ProjectName, &t.FromUserID, &t.FromUser,
		&t.ToUserID, &t.ToUser, &t.RequestedBy, &t.RequestedByName,
		&t.Status, &t.Reason, &t.CreatedAt, &t.ResolvedAt, &t.ResolvedBy)
	return t, err
}

// ListOwnershipTransfers handles GET /projects/:id/ownership-transfers. It
// returns the pending request (if any) followed by recent history.
func (h *Handler) ListOwnershipTransfers(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	rows, err := h.store.DB().Query(c.Request.Context(),
		ownershipTransferSelect+`
		WHERE t.project_id = $1
		ORDER BY (t.status = 'pending') DESC, t.created_at DESC
		LIMIT 25`, projectID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load ownership transfers")
		return
	}
	defer rows.Close()
	transfers := make([]OwnershipTransfer, 0)
	for rows.Next() {
		transfer, scanErr := scanOwnershipTransfer(rows)
		if scanErr != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load ownership transfers")
			return
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load ownership transfers")
		return
	}
	c.JSON(http.StatusOK, transfers)
}

// RequestOwnershipTransfer handles POST /projects/:id/ownership-transfers.
// Owners and global admins may request that another active project member
// accepts ownership.
func (h *Handler) RequestOwnershipTransfer(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	if !canGrantOwnership(c) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Only an owner or a global admin can request an ownership transfer")
		return
	}
	var req ownershipTransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	toUserID, err := uuid.Parse(strings.TrimSpace(req.ToUserID))
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "to_user_id must be a UUID")
		return
	}
	reason, err := normalizeTransferReason(req.Reason)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Reason must be at most 500 characters")
		return
	}

	ctx := c.Request.Context()
	fromUserID, err := h.currentProjectOwner(ctx, projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apiError(c, http.StatusConflict, "NO_OWNER", "The project has no owner membership to transfer")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load the project owner")
		return
	}
	if fromUserID == toUserID {
		apiError(c, http.StatusConflict, "ALREADY_OWNER", "That user already owns the project")
		return
	}
	var targetActive bool
	if err := h.store.DB().QueryRow(ctx,
		`SELECT is_active FROM users WHERE id = $1`, toUserID).Scan(&targetActive); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apiError(c, http.StatusNotFound, "NOT_FOUND", "User not found")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load the target user")
		return
	}
	if !targetActive {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Cannot transfer ownership to a disabled user")
		return
	}
	var targetMember bool
	if err := h.store.DB().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM project_members WHERE project_id = $1 AND user_id = $2)`,
		projectID, toUserID).Scan(&targetMember); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load the target membership")
		return
	}
	if !targetMember {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "The target must already be a project member")
		return
	}

	actorID := ""
	if claims, ok := auth.ClaimsFrom(c); ok && claims != nil {
		actorID = claims.CanonicalUserID()
	}
	transferID := uuid.New()
	var createdAt time.Time
	err = h.store.DB().QueryRow(ctx, `
		INSERT INTO ownership_transfers (id, project_id, from_user_id, to_user_id, requested_by, reason)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at`,
		transferID, projectID, fromUserID, toUserID, actorID, reason).Scan(&createdAt)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "one_pending") ||
			strings.Contains(strings.ToLower(err.Error()), "unique") {
			apiError(c, http.StatusConflict, "TRANSFER_PENDING", "An ownership transfer is already pending for this project")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create the ownership transfer")
		return
	}

	transfer, err := h.loadOwnershipTransfer(ctx, projectID, transferID.String())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load the ownership transfer")
		return
	}
	h.recordActivity(ctx, projectID, actorIDPtr(actorID), "ownership_transfer", &transferID, "requested", "Requested project ownership transfer", map[string]interface{}{
		"to_user_id": toUserID.String(),
	})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "project.ownership.request", TargetType: "ownership_transfer", TargetID: transferID.String(),
		Result: "success", After: map[string]any{
			"from_user_id": fromUserID.String(),
			"to_user_id":   toUserID.String(),
			"reason":       reason,
		},
	})
	c.JSON(http.StatusCreated, transfer)
}

// AcceptOwnershipTransfer handles POST /projects/:id/ownership-transfers/:transferId/accept.
func (h *Handler) AcceptOwnershipTransfer(c *gin.Context) {
	h.resolveOwnershipTransfer(c, "accept")
}

// DeclineOwnershipTransfer handles POST /projects/:id/ownership-transfers/:transferId/decline.
func (h *Handler) DeclineOwnershipTransfer(c *gin.Context) {
	h.resolveOwnershipTransfer(c, "decline")
}

// ConfirmOwnershipTransfer handles POST /projects/:id/ownership-transfers/:transferId/confirm.
// A global admin can confirm on the target's behalf; the target does not need
// to accept first.
func (h *Handler) ConfirmOwnershipTransfer(c *gin.Context) {
	h.resolveOwnershipTransfer(c, "confirm")
}

// CancelOwnershipTransfer handles DELETE /projects/:id/ownership-transfers/:transferId.
func (h *Handler) CancelOwnershipTransfer(c *gin.Context) {
	h.resolveOwnershipTransfer(c, "cancel")
}

func (h *Handler) resolveOwnershipTransfer(c *gin.Context, action string) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	transferID, ok := parseParamID(c, "transferId")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	claims, _ := auth.ClaimsFrom(c)
	actorID := ""
	globalAdmin := false
	if claims != nil {
		actorID = claims.CanonicalUserID()
		globalAdmin = claims.Role == auth.RoleAdmin
	}

	transfer, err := h.loadOwnershipTransfer(ctx, projectID, transferID.String())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apiError(c, http.StatusNotFound, "NOT_FOUND", "Ownership transfer not found")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load the ownership transfer")
		return
	}
	if err := authorizeTransferAction(action, actorID, transfer, globalAdmin); err != nil {
		if errors.Is(err, errTransferNotPending) {
			apiError(c, http.StatusConflict, "TRANSFER_RESOLVED", "This ownership transfer is no longer pending")
			return
		}
		message := "You cannot resolve this ownership transfer"
		switch action {
		case "accept", "decline":
			message = "Only the transfer target can accept or decline it"
		case "confirm":
			message = "Only a global admin can confirm a transfer on the target's behalf"
		case "cancel":
			message = "Only the requester, the current owner or a global admin can cancel this transfer"
		}
		apiError(c, http.StatusForbidden, "FORBIDDEN", message)
		return
	}

	if action == "accept" || action == "confirm" {
		if err := h.applyOwnershipTransfer(ctx, projectID, transferID, transfer, actorID, action); err != nil {
			switch {
			case errors.Is(err, errProjectArchived):
				apiError(c, http.StatusConflict, "PROJECT_ARCHIVED", "Archived projects cannot change ownership")
			case errors.Is(err, errOwnershipChanged):
				apiError(c, http.StatusConflict, "OWNERSHIP_CHANGED", "Project ownership changed after this request was created")
			default:
				apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to transfer ownership")
			}
			return
		}
		h.invalidateMemberSessions(ctx, uuid.MustParse(transfer.FromUserID), projectID, "ownership_transferred")
		h.invalidateMemberSessions(ctx, uuid.MustParse(transfer.ToUserID), projectID, "ownership_received")
		h.recordActivity(ctx, projectID, actorIDPtr(actorID), "ownership_transfer", &transferID, action, "Resolved project ownership transfer", map[string]interface{}{
			"to_user_id": transfer.ToUserID,
		})
		h.recordAudit(c, projectID, audit.Entry{
			Action: "project.ownership." + action, TargetType: "ownership_transfer", TargetID: transferID.String(),
			Result: "success", Before: map[string]any{"owner_id": transfer.FromUserID},
			After: map[string]any{"owner_id": transfer.ToUserID},
		})
	} else {
		status := "declined"
		if action == "cancel" {
			status = "cancelled"
		}
		tag, err := h.store.DB().Exec(ctx, `
			UPDATE ownership_transfers
			SET status = $1, resolved_at = NOW(), resolved_by = $2
			WHERE id = $3 AND project_id = $4 AND status = 'pending'`,
			status, nullUUID(actorID), transferID, projectID)
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to resolve the ownership transfer")
			return
		}
		if tag.RowsAffected() == 0 {
			apiError(c, http.StatusConflict, "TRANSFER_RESOLVED", "This ownership transfer is no longer pending")
			return
		}
		h.recordActivity(ctx, projectID, actorIDPtr(actorID), "ownership_transfer", &transferID, action, "Resolved project ownership transfer", nil)
		h.recordAudit(c, projectID, audit.Entry{
			Action: "project.ownership." + action, TargetType: "ownership_transfer", TargetID: transferID.String(),
			Result: "success", After: map[string]any{"status": status},
		})
	}

	updated, err := h.loadOwnershipTransfer(ctx, projectID, transferID.String())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load the resolved transfer")
		return
	}
	c.JSON(http.StatusOK, updated)
}

var (
	errProjectArchived  = errors.New("project archived")
	errOwnershipChanged = errors.New("ownership changed")

	errTransferReason     = errors.New("invalid transfer reason")
	errTransferNotPending = errors.New("transfer is not pending")
	errTransferAction     = errors.New("transfer action is not allowed")
)

// normalizeTransferReason trims the optional reason and enforces the 500
// character bound shared by the API and the database.
func normalizeTransferReason(raw string) (string, error) {
	reason := strings.TrimSpace(raw)
	if len(reason) > 500 {
		return "", errTransferReason
	}
	return reason, nil
}

// authorizeTransferAction encodes who may resolve a pending transfer:
//   - the target accepts or declines;
//   - a global admin confirms on the target's behalf;
//   - the requester, the current owner or a global admin cancels.
func authorizeTransferAction(action, actorID string, transfer OwnershipTransfer, globalAdmin bool) error {
	if transfer.Status != "pending" {
		return errTransferNotPending
	}
	switch action {
	case "accept", "decline":
		if actorID != transfer.ToUserID {
			return errTransferAction
		}
	case "confirm":
		if !globalAdmin {
			return errTransferAction
		}
	case "cancel":
		if actorID != transfer.RequestedBy && actorID != transfer.FromUserID && !globalAdmin {
			return errTransferAction
		}
	default:
		return errTransferAction
	}
	return nil
}

// applyOwnershipTransfer moves projects.owner_id and the owner membership in
// one transaction. The transfer row is resolved in the same transaction so a
// concurrent accept/confirm cannot apply it twice.
func (h *Handler) applyOwnershipTransfer(ctx context.Context, projectID, transferID uuid.UUID, transfer OwnershipTransfer, actorID, action string) error {
	tx, err := h.store.DB().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM projects WHERE id = $1 FOR UPDATE`, projectID).Scan(&status); err != nil {
		return err
	}
	if status == "archived" {
		return errProjectArchived
	}
	var currentOwner string
	if err := tx.QueryRow(ctx, `
		SELECT user_id::text FROM project_members
		WHERE project_id = $1 AND role = 'owner'
		ORDER BY updated_at DESC LIMIT 1`, projectID).Scan(&currentOwner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			currentOwner = transfer.FromUserID
		} else {
			return err
		}
	}
	if currentOwner != transfer.FromUserID {
		return errOwnershipChanged
	}

	// Demote every other owner, then upsert the successor as owner. This keeps
	// the single-owner invariant used by the existing membership endpoints.
	if _, err := tx.Exec(ctx, `
		UPDATE project_members SET role = 'maintainer', updated_at = NOW()
		WHERE project_id = $1 AND role = 'owner' AND user_id <> $2`,
		projectID, transfer.ToUserID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role)
		VALUES ($1, $2, 'owner')
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = 'owner', updated_at = NOW()`,
		projectID, transfer.ToUserID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE projects SET owner_id = $1, updated_at = NOW() WHERE id = $2`,
		transfer.ToUserID, projectID); err != nil {
		return err
	}
	statusValue := "accepted"
	if action == "confirm" {
		statusValue = "confirmed"
	}
	tag, err := tx.Exec(ctx, `
		UPDATE ownership_transfers
		SET status = $1, resolved_at = NOW(), resolved_by = $2
		WHERE id = $3 AND project_id = $4 AND status = 'pending'`,
		statusValue, nullUUID(actorID), transferID, projectID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errOwnershipChanged
	}
	return tx.Commit(ctx)
}

// ListMyOwnershipTransfers handles GET /auth/ownership-transfers. It gives the
// target an inbox of pending requests and shows the caller's outgoing requests
// without requiring project access.
func (h *Handler) ListMyOwnershipTransfers(c *gin.Context) {
	claims, ok := auth.ClaimsFrom(c)
	if !ok || claims == nil {
		apiError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	userID := claims.CanonicalUserID()
	incoming, err := h.queryOwnershipTransfers(c.Request.Context(), `
		WHERE t.to_user_id = $1 AND t.status = 'pending'
		ORDER BY t.created_at DESC LIMIT 20`, userID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load ownership transfers")
		return
	}
	outgoing, err := h.queryOwnershipTransfers(c.Request.Context(), `
		WHERE (t.requested_by = $1 OR t.from_user_id = $1) AND t.status = 'pending'
		ORDER BY t.created_at DESC LIMIT 20`, userID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load ownership transfers")
		return
	}
	c.JSON(http.StatusOK, myOwnershipTransfers{Incoming: incoming, Outgoing: outgoing})
}

func (h *Handler) queryOwnershipTransfers(ctx context.Context, suffix string, userID string) ([]OwnershipTransfer, error) {
	rows, err := h.store.DB().Query(ctx, ownershipTransferSelect+" "+suffix, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	transfers := make([]OwnershipTransfer, 0)
	for rows.Next() {
		transfer, scanErr := scanOwnershipTransfer(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		transfers = append(transfers, transfer)
	}
	return transfers, rows.Err()
}

func (h *Handler) loadOwnershipTransfer(ctx context.Context, projectID uuid.UUID, transferID string) (OwnershipTransfer, error) {
	return scanOwnershipTransfer(h.store.DB().QueryRow(ctx,
		ownershipTransferSelect+` WHERE t.project_id = $1 AND t.id = $2`, projectID, transferID))
}

// currentProjectOwner returns the authoritative owner membership and repairs
// projects.owner_id when the two drifted apart in older data.
func (h *Handler) currentProjectOwner(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	var ownerID uuid.UUID
	err := h.store.DB().QueryRow(ctx, `
		SELECT user_id FROM project_members
		WHERE project_id = $1 AND role = 'owner'
		ORDER BY updated_at DESC LIMIT 1`, projectID).Scan(&ownerID)
	if err != nil {
		return uuid.Nil, err
	}
	_, _ = h.store.DB().Exec(ctx,
		`UPDATE projects SET owner_id = $1 WHERE id = $2 AND owner_id <> $1`, ownerID, projectID)
	return ownerID, nil
}

func nullUUID(value string) interface{} {
	id, err := uuid.Parse(value)
	if err != nil {
		return nil
	}
	return id
}

func actorIDPtr(value string) *uuid.UUID {
	id, err := uuid.Parse(value)
	if err != nil {
		return nil
	}
	return &id
}
