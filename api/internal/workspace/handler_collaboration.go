package workspace

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"spm-platform/api/internal/attachments"
	"spm-platform/api/internal/audit"
	"spm-platform/api/internal/auth"
)

const (
	maxCommentRunes = 10000
	maxLabels       = 10
	maxLabelRunes   = 24
	maxLinkType     = 40
)

var mentionPattern = regexp.MustCompile(`(^|[^A-Za-z0-9_.-])@([A-Za-z0-9](?:[A-Za-z0-9_.-]*[A-Za-z0-9_])?)`)

// sanitizeCommentBody normalizes line endings, removes control characters and
// enforces the length limit. The body stays plain text; clients must render it
// with text interpolation (never as HTML), which is documented in the API
// contract.
func sanitizeCommentBody(raw string) (string, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	var builder strings.Builder
	for _, r := range raw {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			builder.WriteRune(r)
		}
	}
	body := strings.TrimSpace(builder.String())
	if body == "" {
		return "", errors.New("Comment body is required")
	}
	if utf8.RuneCountInString(body) > maxCommentRunes {
		return "", fmt.Errorf("Comments must be at most %d characters", maxCommentRunes)
	}
	return body, nil
}

// extractMentionUsernames returns unique, lower-case @mentions in order of
// first appearance. More than 20 mentions are ignored to bound the write.
func extractMentionUsernames(body string) []string {
	matches := mentionPattern.FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	names := []string{}
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(match[2]))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) == 20 {
			break
		}
	}
	return names
}

func normalizeLabels(values []string) ([]string, error) {
	if len(values) > maxLabels {
		return nil, fmt.Errorf("A work item may have at most %d labels", maxLabels)
	}
	labels := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		label := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
		if label == "" {
			continue
		}
		if utf8.RuneCountInString(label) > maxLabelRunes {
			return nil, fmt.Errorf("Labels must be at most %d characters", maxLabelRunes)
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' && r != ' ' {
				return nil, errors.New("Labels may contain letters, digits, spaces, hyphens and underscores")
			}
		}
		if !seen[label] {
			seen[label] = true
			labels = append(labels, label)
		}
	}
	return labels, nil
}

func (h *Handler) ensureWorkItem(ctx *gin.Context, projectID, itemID uuid.UUID) bool {
	err := h.ensureBelongs(ctx.Request.Context(), "work_items", projectID, itemID)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(ctx, http.StatusNotFound, "NOT_FOUND", "Work item not found")
		return false
	}
	if err != nil {
		apiError(ctx, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load work item")
		return false
	}
	return true
}

func canModerate(c *gin.Context) bool {
	role := auth.ProjectRoleFrom(c)
	return role == auth.ProjectRoleOwner || role == auth.ProjectRoleMaintainer || role == auth.RoleAdmin
}

// GetWorkItemDetail returns the collaboration drawer aggregate.
func (h *Handler) GetWorkItemDetail(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	itemID, ok := parseParamID(c, "itemId")
	if !ok {
		return
	}
	detail, err := h.store.LoadWorkItemDetail(c.Request.Context(), projectID, itemID, actorID(c))
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Work item not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load work item")
		return
	}
	c.JSON(http.StatusOK, detail)
}

// ---------------------------------------------------------------------------
// Comments
// ---------------------------------------------------------------------------

type commentRequest struct {
	Body string `json:"body"`
}

func (h *Handler) resolveMentions(c *gin.Context, projectID uuid.UUID, usernames []string) ([]Mention, error) {
	if len(usernames) == 0 {
		return []Mention{}, nil
	}
	rows, err := h.store.DB().Query(c.Request.Context(), `
		SELECT u.id, u.username, u.display_name
		FROM users u
		JOIN project_members pm ON pm.user_id = u.id
		WHERE pm.project_id = $1 AND lower(u.username) = ANY($2)
		ORDER BY u.username`, projectID, usernames)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	mentions := []Mention{}
	for rows.Next() {
		var mention Mention
		if err := rows.Scan(&mention.UserID, &mention.Username, &mention.DisplayName); err != nil {
			return nil, err
		}
		mentions = append(mentions, mention)
	}
	return mentions, rows.Err()
}

func (h *Handler) saveMentions(c *gin.Context, tx pgx.Tx, commentID uuid.UUID, mentions []Mention) error {
	for _, mention := range mentions {
		if _, err := tx.Exec(c.Request.Context(),
			`INSERT INTO comment_mentions (comment_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			commentID, mention.UserID); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) loadComment(c *gin.Context, projectID, commentID uuid.UUID) (Comment, error) {
	var comment Comment
	var author string
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT c.id, c.project_id, c.entity_type, c.entity_id, COALESCE(c.author_id::text, ''),
		       COALESCE(NULLIF(u.display_name, ''), u.username, ''), c.body,
		       c.created_at, c.updated_at, c.edited_at
		FROM comments c
		LEFT JOIN users u ON u.id = c.author_id
		WHERE c.id = $1 AND c.project_id = $2`, commentID, projectID).Scan(
		&comment.ID, &comment.ProjectID, &comment.EntityType, &comment.EntityID, &author,
		&comment.AuthorName, &comment.Body, &comment.CreatedAt, &comment.UpdatedAt, &comment.EditedAt)
	if err != nil {
		return Comment{}, err
	}
	comment.AuthorID = parseUUIDPtr(author)
	mentions, err := h.store.loadMentions(c.Request.Context(), []uuid.UUID{commentID})
	if err != nil {
		return Comment{}, err
	}
	comment.Mentions = mentions[commentID]
	if comment.Mentions == nil {
		comment.Mentions = []Mention{}
	}
	return comment, nil
}

func (h *Handler) CreateComment(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	itemID, ok := parseParamID(c, "itemId")
	if !ok || !h.ensureWorkItem(c, projectID, itemID) {
		return
	}
	var req commentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	body, err := sanitizeCommentBody(req.Body)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	mentions, err := h.resolveMentions(c, projectID, extractMentionUsernames(body))
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to resolve mentions")
		return
	}
	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create comment")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()
	var commentID uuid.UUID
	if err := tx.QueryRow(c.Request.Context(), `
		INSERT INTO comments (project_id, entity_type, entity_id, author_id, body)
		VALUES ($1, 'work_item', $2, $3, $4) RETURNING id`,
		projectID, itemID, actorID(c), body).Scan(&commentID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create comment")
		return
	}
	if err := h.saveMentions(c, tx, commentID, mentions); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save mentions")
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create comment")
		return
	}
	comment, err := h.loadComment(c, projectID, commentID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load comment")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "work_item", &itemID, "commented",
		"Added a comment", map[string]interface{}{"comment_id": commentID.String(), "mentions": len(mentions)})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "work_item.comment.create", TargetType: "comment", TargetID: commentID.String(), Result: "success",
		After: map[string]any{"work_item_id": itemID.String(), "mentions": len(mentions)},
	})
	c.JSON(http.StatusCreated, comment)
}

func (h *Handler) UpdateComment(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	commentID, ok := parseParamID(c, "commentId")
	if !ok {
		return
	}
	var req commentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	body, err := sanitizeCommentBody(req.Body)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	var author, entityType string
	var entityID uuid.UUID
	err = h.store.DB().QueryRow(c.Request.Context(), `
		SELECT COALESCE(author_id::text, ''), entity_type, entity_id
		FROM comments WHERE id = $1 AND project_id = $2`, commentID, projectID).Scan(&author, &entityType, &entityID)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Comment not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load comment")
		return
	}
	actor := actorID(c)
	if actor == nil || (author != actor.String() && !canModerate(c)) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Only the author or a project maintainer may edit this comment")
		return
	}
	mentions, err := h.resolveMentions(c, projectID, extractMentionUsernames(body))
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to resolve mentions")
		return
	}
	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update comment")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE comments SET body = $3, edited_at = NOW(), updated_at = NOW() WHERE id = $1 AND project_id = $2`,
		commentID, projectID, body); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update comment")
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `DELETE FROM comment_mentions WHERE comment_id = $1`, commentID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update mentions")
		return
	}
	if err := h.saveMentions(c, tx, commentID, mentions); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save mentions")
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update comment")
		return
	}
	comment, err := h.loadComment(c, projectID, commentID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load comment")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actor, "work_item", &entityID, "comment_edited",
		"Edited a comment", map[string]interface{}{"comment_id": commentID.String()})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "work_item.comment.update", TargetType: "comment", TargetID: commentID.String(), Result: "success",
		After: map[string]any{"work_item_id": entityID.String()},
	})
	c.JSON(http.StatusOK, comment)
}

func (h *Handler) DeleteComment(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	commentID, ok := parseParamID(c, "commentId")
	if !ok {
		return
	}
	var author, entityType string
	var entityID uuid.UUID
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT COALESCE(author_id::text, ''), entity_type, entity_id
		FROM comments WHERE id = $1 AND project_id = $2`, commentID, projectID).Scan(&author, &entityType, &entityID)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Comment not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load comment")
		return
	}
	actor := actorID(c)
	if actor == nil || (author != actor.String() && !canModerate(c)) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Only the author or a project maintainer may delete this comment")
		return
	}
	if _, err := h.store.DB().Exec(c.Request.Context(), `DELETE FROM comments WHERE id = $1 AND project_id = $2`, commentID, projectID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete comment")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actor, "work_item", &entityID, "comment_deleted",
		"Deleted a comment", map[string]interface{}{"comment_id": commentID.String()})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "work_item.comment.delete", TargetType: "comment", TargetID: commentID.String(), Result: "success",
		Before: map[string]any{"work_item_id": entityID.String()},
	})
	c.Status(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Watchers
// ---------------------------------------------------------------------------

func (h *Handler) WatchWorkItem(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	itemID, ok := parseParamID(c, "itemId")
	if !ok || !h.ensureWorkItem(c, projectID, itemID) {
		return
	}
	actor := actorID(c)
	var userID uuid.UUID
	if actor != nil {
		userID = *actor
	}
	if userID == uuid.Nil {
		apiError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication is required")
		return
	}
	if _, err := h.store.DB().Exec(c.Request.Context(), `
		INSERT INTO work_item_watchers (work_item_id, user_id, project_id) VALUES ($1, $2, $3)
		ON CONFLICT (work_item_id, user_id) DO NOTHING`, itemID, userID, projectID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to watch work item")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actor, "work_item", &itemID, "watch_added",
		"Started watching work item", nil)
	h.recordAudit(c, projectID, audit.Entry{
		Action: "work_item.watch.add", TargetType: "work_item", TargetID: itemID.String(), Result: "success",
	})
	c.JSON(http.StatusOK, gin.H{"watching": true})
}

func (h *Handler) UnwatchWorkItem(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	itemID, ok := parseParamID(c, "itemId")
	if !ok || !h.ensureWorkItem(c, projectID, itemID) {
		return
	}
	actor := actorID(c)
	if actor == nil {
		apiError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication is required")
		return
	}
	if _, err := h.store.DB().Exec(c.Request.Context(), `
		DELETE FROM work_item_watchers WHERE work_item_id = $1 AND user_id = $2 AND project_id = $3`,
		itemID, *actor, projectID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to stop watching work item")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actor, "work_item", &itemID, "watch_removed",
		"Stopped watching work item", nil)
	h.recordAudit(c, projectID, audit.Entry{
		Action: "work_item.watch.remove", TargetType: "work_item", TargetID: itemID.String(), Result: "success",
	})
	c.Status(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Attachments
// ---------------------------------------------------------------------------

func (h *Handler) UploadAttachment(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	itemID, ok := parseParamID(c, "itemId")
	if !ok || !h.ensureWorkItem(c, projectID, itemID) {
		return
	}
	if h.attachments == nil {
		apiError(c, http.StatusServiceUnavailable, "ATTACHMENT_STORAGE_UNAVAILABLE", "Attachment storage is not configured")
		return
	}
	limit := h.attachments.MaxBytes()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit+1<<20)
	header, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			apiError(c, http.StatusBadRequest, "ATTACHMENT_TOO_LARGE", fmt.Sprintf("Attachments must be %d bytes or smaller", limit))
			return
		}
		apiError(c, http.StatusBadRequest, "ATTACHMENT_REQUIRED", "A file part named \"file\" is required")
		return
	}
	file, err := header.Open()
	if err != nil {
		apiError(c, http.StatusBadRequest, "ATTACHMENT_REQUIRED", "The uploaded file could not be read")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		apiError(c, http.StatusBadRequest, "ATTACHMENT_REQUIRED", "The uploaded file could not be read")
		return
	}
	attachmentID := uuid.New()
	stored, err := h.attachments.Save(c.Request.Context(), projectID, attachmentID, header.Filename, data)
	if err != nil {
		var rejection *attachments.Rejection
		var unavailable *attachments.Unavailable
		switch {
		case errors.As(err, &rejection):
			h.recordAudit(c, projectID, audit.Entry{
				Action: "work_item.attachment.upload", TargetType: "attachment", TargetID: attachmentID.String(),
				Result: "failure", Reason: rejection.Message,
				After: map[string]any{"work_item_id": itemID.String(), "filename": header.Filename},
			})
			apiError(c, http.StatusUnprocessableEntity, rejection.Code, rejection.Message)
		case errors.As(err, &unavailable):
			apiError(c, http.StatusServiceUnavailable, "SCAN_UNAVAILABLE", unavailable.Message)
		default:
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to store attachment")
		}
		return
	}
	actor := actorID(c)
	if _, err := h.store.DB().Exec(c.Request.Context(), `
		INSERT INTO attachments (id, project_id, entity_type, entity_id, filename, mime_type, size_bytes,
		                         storage_path, uploaded_by, checksum_sha256, scan_status, scan_engine, scan_detail)
		VALUES ($1, $2, 'work_item', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		attachmentID, projectID, itemID, stored.Filename, stored.MIMEType, stored.SizeBytes,
		stored.StoragePath, actor, stored.ChecksumSHA256, stored.ScanStatus, stored.ScanEngine, stored.ScanDetail); err != nil {
		_ = h.attachments.Delete(stored.StoragePath)
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to record attachment")
		return
	}
	uploaderName := ""
	if actor != nil {
		_ = h.store.DB().QueryRow(c.Request.Context(),
			`SELECT COALESCE(NULLIF(display_name, ''), username, '') FROM users WHERE id = $1`, *actor).Scan(&uploaderName)
	}
	attachment := Attachment{
		ID: attachmentID, ProjectID: projectID, EntityType: "work_item", EntityID: itemID,
		Filename: stored.Filename, MIMEType: stored.MIMEType, SizeBytes: stored.SizeBytes,
		ChecksumSHA256: stored.ChecksumSHA256, ScanStatus: stored.ScanStatus, ScanEngine: stored.ScanEngine,
		ScanDetail: stored.ScanDetail, UploadedBy: actor, UploaderName: uploaderName,
		DownloadURL: "/api/v1/projects/" + projectID.String() + "/attachments/" + attachmentID.String() + "/download",
	}
	h.recordActivity(c.Request.Context(), projectID, actor, "work_item", &itemID, "attachment_added",
		"Attached "+stored.Filename, map[string]interface{}{"attachment_id": attachmentID.String()})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "work_item.attachment.upload", TargetType: "attachment", TargetID: attachmentID.String(), Result: "success",
		After: map[string]any{"work_item_id": itemID.String(), "filename": stored.Filename,
			"size_bytes": stored.SizeBytes, "checksum_sha256": stored.ChecksumSHA256, "scan_engine": stored.ScanEngine},
	})
	c.JSON(http.StatusCreated, attachment)
}

func (h *Handler) DownloadAttachment(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	attachmentID, ok := parseParamID(c, "attachmentId")
	if !ok {
		return
	}
	if h.attachments == nil {
		apiError(c, http.StatusServiceUnavailable, "ATTACHMENT_STORAGE_UNAVAILABLE", "Attachment storage is not configured")
		return
	}
	var filename, mediaType, storagePath, scanStatus, checksum string
	var size int64
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT filename, mime_type, size_bytes, storage_path, scan_status, checksum_sha256
		FROM attachments
		WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL`, attachmentID, projectID).Scan(
		&filename, &mediaType, &size, &storagePath, &scanStatus, &checksum)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Attachment not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load attachment")
		return
	}
	if scanStatus != "clean" {
		apiError(c, http.StatusForbidden, "ATTACHMENT_NOT_CLEAN", "This attachment has not passed malware scanning")
		return
	}
	handle, err := h.attachments.Open(storagePath)
	if errors.Is(err, os.ErrNotExist) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Attachment file is missing")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to open attachment")
		return
	}
	defer handle.Close()
	h.recordAudit(c, projectID, audit.Entry{
		Action: "work_item.attachment.download", TargetType: "attachment", TargetID: attachmentID.String(),
		Result: "success", After: map[string]any{"filename": filename, "size_bytes": size},
	})
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if disposition == "" {
		disposition = "attachment"
	}
	headers := map[string]string{
		"Content-Disposition":     disposition,
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "private, no-store",
		"Content-Security-Policy": "sandbox",
		"ETag":                    `"` + checksum + `"`,
	}
	c.DataFromReader(http.StatusOK, size, mediaType, handle, headers)
}

func (h *Handler) DeleteAttachment(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	attachmentID, ok := parseParamID(c, "attachmentId")
	if !ok {
		return
	}
	var uploader, entityType, storagePath, filename string
	var entityID uuid.UUID
	err := h.store.DB().QueryRow(c.Request.Context(), `
		SELECT COALESCE(uploaded_by::text, ''), entity_type, entity_id, storage_path, filename
		FROM attachments WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL`,
		attachmentID, projectID).Scan(&uploader, &entityType, &entityID, &storagePath, &filename)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Attachment not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load attachment")
		return
	}
	actor := actorID(c)
	if actor == nil || (uploader != actor.String() && !canModerate(c)) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Only the uploader or a project maintainer may delete this attachment")
		return
	}
	if _, err := h.store.DB().Exec(c.Request.Context(),
		`UPDATE attachments SET deleted_at = NOW() WHERE id = $1 AND project_id = $2`, attachmentID, projectID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete attachment")
		return
	}
	if h.attachments != nil {
		_ = h.attachments.Delete(storagePath)
	}
	if entityType == "work_item" {
		h.recordActivity(c.Request.Context(), projectID, actor, "work_item", &entityID, "attachment_deleted",
			"Removed "+filename, map[string]interface{}{"attachment_id": attachmentID.String()})
	}
	h.recordAudit(c, projectID, audit.Entry{
		Action: "work_item.attachment.delete", TargetType: "attachment", TargetID: attachmentID.String(), Result: "success",
		Before: map[string]any{"work_item_id": entityID.String(), "filename": filename},
	})
	c.Status(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Work item links
// ---------------------------------------------------------------------------

type workItemLinkRequest struct {
	TargetType string    `json:"target_type"`
	TargetID   uuid.UUID `json:"target_id"`
	LinkType   string    `json:"link_type"`
}

func (h *Handler) CreateWorkItemLink(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	itemID, ok := parseParamID(c, "itemId")
	if !ok || !h.ensureWorkItem(c, projectID, itemID) {
		return
	}
	var req workItemLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	targetType := normalizeArtifactType(req.TargetType)
	table, allowedType := linkArtifactTables[targetType]
	if !allowedType {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Unsupported target artifact type")
		return
	}
	if req.TargetID == uuid.Nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A target artifact is required")
		return
	}
	if targetType == "work_item" && req.TargetID == itemID {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "A work item cannot link to itself")
		return
	}
	if err := h.ensureBelongs(c.Request.Context(), table, projectID, req.TargetID); err != nil {
		apiError(c, http.StatusUnprocessableEntity, "LINK_TARGET_INVALID", "The target artifact does not belong to this project")
		return
	}
	linkType := strings.ToLower(strings.TrimSpace(req.LinkType))
	if linkType == "" {
		linkType = "related"
	}
	if len(linkType) > maxLinkType {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Link type is too long")
		return
	}
	for _, r := range linkType {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Link type may contain letters, digits, hyphens and underscores")
			return
		}
	}
	var linkID uuid.UUID
	if err := h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO artifact_links (project_id, source_type, source_id, target_type, target_id, link_type, created_by)
		VALUES ($1, 'work_item', $2, $3, $4, $5, $6)
		ON CONFLICT (project_id, source_type, source_id, target_type, target_id, link_type)
		DO UPDATE SET link_type = EXCLUDED.link_type
		RETURNING id`, projectID, itemID, targetType, req.TargetID, linkType, actorID(c)).Scan(&linkID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create link")
		return
	}
	key, title, status := h.store.artifactSummary(c.Request.Context(), projectID, targetType, req.TargetID)
	link := LinkedArtifact{
		LinkID: linkID, LinkType: linkType, Direction: "outbound", ArtifactType: targetType,
		ArtifactID: req.TargetID, ArtifactKey: key, Title: title, Status: status,
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "work_item", &itemID, "link_added",
		"Linked "+targetType+" "+title, map[string]interface{}{"link_id": linkID.String(), "target_type": targetType})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "work_item.link.create", TargetType: "artifact_link", TargetID: linkID.String(), Result: "success",
		After: map[string]any{"work_item_id": itemID.String(), "target_type": targetType, "target_id": req.TargetID.String(), "link_type": linkType},
	})
	c.JSON(http.StatusCreated, link)
}

func (h *Handler) DeleteWorkItemLink(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	itemID, ok := parseParamID(c, "itemId")
	if !ok {
		return
	}
	linkID, ok := parseParamID(c, "linkId")
	if !ok {
		return
	}
	tag, err := h.store.DB().Exec(c.Request.Context(), `
		DELETE FROM artifact_links
		WHERE id = $1 AND project_id = $2
		  AND ((source_type = 'work_item' AND source_id = $3) OR (target_type = 'work_item' AND target_id = $3))`,
		linkID, projectID, itemID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete link")
		return
	}
	if tag.RowsAffected() == 0 {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Link not found")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "work_item", &itemID, "link_removed",
		"Removed an artifact link", map[string]interface{}{"link_id": linkID.String()})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "work_item.link.delete", TargetType: "artifact_link", TargetID: linkID.String(), Result: "success",
		Before: map[string]any{"work_item_id": itemID.String()},
	})
	c.Status(http.StatusNoContent)
}
