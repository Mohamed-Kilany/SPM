package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LoadWorkItemDetail loads the drawer aggregate for one work item. Child
// collections are intentionally separate from the workspace snapshot so the
// board stays small even when an item has a long discussion history.
func (s *Store) LoadWorkItemDetail(ctx context.Context, projectID, itemID uuid.UUID, viewer *uuid.UUID) (WorkItemDetail, error) {
	item, err := scanWorkItem(s.db.QueryRow(ctx, `SELECT `+workItemColumns+`
		FROM work_items WHERE id = $1 AND project_id = $2`, itemID, projectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkItemDetail{}, pgx.ErrNoRows
	}
	if err != nil {
		return WorkItemDetail{}, err
	}
	var prefix string
	if err := s.db.QueryRow(ctx, `SELECT key_prefix FROM projects WHERE id = $1`, projectID).Scan(&prefix); err != nil {
		return WorkItemDetail{}, err
	}
	item.Key = prefix + "-" + strconv.FormatInt(item.ItemNumber, 10)

	detail := WorkItemDetail{WorkItem: item}
	detail.Children, err = s.loadChildWorkItems(ctx, projectID, itemID, prefix)
	if err != nil {
		return WorkItemDetail{}, err
	}
	detail.Comments, err = s.loadComments(ctx, projectID, itemID)
	if err != nil {
		return WorkItemDetail{}, err
	}
	detail.Attachments, err = s.loadAttachments(ctx, projectID, itemID)
	if err != nil {
		return WorkItemDetail{}, err
	}
	detail.Watchers, err = s.loadWatchers(ctx, projectID, itemID)
	if err != nil {
		return WorkItemDetail{}, err
	}
	detail.Watching, err = s.isWatching(ctx, itemID, viewer)
	if err != nil {
		return WorkItemDetail{}, err
	}
	detail.Links, err = s.loadLinkedArtifacts(ctx, projectID, itemID)
	if err != nil {
		return WorkItemDetail{}, err
	}
	detail.Activity, err = s.loadWorkItemActivity(ctx, projectID, itemID)
	if err != nil {
		return WorkItemDetail{}, err
	}
	detail.Branches, err = s.loadWorkItemBranches(ctx, projectID, itemID)
	if err != nil {
		return WorkItemDetail{}, err
	}
	detail.PullRequests, err = s.loadWorkItemPullRequests(ctx, projectID, itemID)
	if err != nil {
		return WorkItemDetail{}, err
	}
	detail.ChildCount = len(detail.Children)
	detail.CommentCount = len(detail.Comments)
	detail.AttachmentCount = len(detail.Attachments)
	detail.WatcherCount = len(detail.Watchers)
	return detail, nil
}

func (s *Store) loadChildWorkItems(ctx context.Context, projectID, parentID uuid.UUID, prefix string) ([]WorkItem, error) {
	rows, err := s.db.Query(ctx, `SELECT `+workItemColumns+`
		FROM work_items WHERE project_id = $1 AND parent_id = $2 ORDER BY item_number`, projectID, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WorkItem{}
	for rows.Next() {
		item, err := scanWorkItem(rows)
		if err != nil {
			return nil, err
		}
		item.Key = prefix + "-" + strconv.FormatInt(item.ItemNumber, 10)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) loadComments(ctx context.Context, projectID, itemID uuid.UUID) ([]Comment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.project_id, c.entity_type, c.entity_id, COALESCE(c.author_id::text, ''),
		       COALESCE(NULLIF(u.display_name, ''), u.username, ''), c.body,
		       c.created_at, c.updated_at, c.edited_at
		FROM comments c
		LEFT JOIN users u ON u.id = c.author_id
		WHERE c.project_id = $1 AND c.entity_type = 'work_item' AND c.entity_id = $2
		ORDER BY c.created_at`, projectID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	comments := []Comment{}
	ids := []uuid.UUID{}
	byID := map[uuid.UUID]int{}
	for rows.Next() {
		var comment Comment
		var author string
		if err := rows.Scan(&comment.ID, &comment.ProjectID, &comment.EntityType, &comment.EntityID,
			&author, &comment.AuthorName, &comment.Body, &comment.CreatedAt, &comment.UpdatedAt,
			&comment.EditedAt); err != nil {
			return nil, err
		}
		comment.AuthorID = parseUUIDPtr(author)
		comment.Mentions = []Mention{}
		byID[comment.ID] = len(comments)
		ids = append(ids, comment.ID)
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return comments, nil
	}
	mentionRows, err := s.db.Query(ctx, `
		SELECT cm.comment_id, u.id, u.username, u.display_name
		FROM comment_mentions cm
		JOIN users u ON u.id = cm.user_id
		WHERE cm.comment_id = ANY($1)
		ORDER BY u.username`, ids)
	if err != nil {
		return nil, err
	}
	defer mentionRows.Close()
	for mentionRows.Next() {
		var commentID uuid.UUID
		var mention Mention
		if err := mentionRows.Scan(&commentID, &mention.UserID, &mention.Username, &mention.DisplayName); err != nil {
			return nil, err
		}
		if index, ok := byID[commentID]; ok {
			comments[index].Mentions = append(comments[index].Mentions, mention)
		}
	}
	return comments, mentionRows.Err()
}

func (s *Store) loadMentions(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]Mention, error) {
	result := map[uuid.UUID][]Mention{}
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT cm.comment_id, u.id, u.username, u.display_name
		FROM comment_mentions cm
		JOIN users u ON u.id = cm.user_id
		WHERE cm.comment_id = ANY($1)
		ORDER BY u.username`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var commentID uuid.UUID
		var mention Mention
		if err := rows.Scan(&commentID, &mention.UserID, &mention.Username, &mention.DisplayName); err != nil {
			return nil, err
		}
		result[commentID] = append(result[commentID], mention)
	}
	return result, rows.Err()
}

func (s *Store) loadAttachments(ctx context.Context, projectID, itemID uuid.UUID) ([]Attachment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.id, a.project_id, a.entity_type, a.entity_id, a.filename, a.mime_type, a.size_bytes,
		       a.checksum_sha256, a.scan_status, a.scan_engine, a.scan_detail, COALESCE(a.uploaded_by::text, ''),
		       COALESCE(NULLIF(u.display_name, ''), u.username, ''), a.created_at
		FROM attachments a
		LEFT JOIN users u ON u.id = a.uploaded_by
		WHERE a.project_id = $1 AND a.entity_type = 'work_item' AND a.entity_id = $2
		  AND a.deleted_at IS NULL
		ORDER BY a.created_at DESC`, projectID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Attachment{}
	for rows.Next() {
		var item Attachment
		var uploader string
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.EntityType, &item.EntityID, &item.Filename,
			&item.MIMEType, &item.SizeBytes, &item.ChecksumSHA256, &item.ScanStatus, &item.ScanEngine,
			&item.ScanDetail, &uploader, &item.UploaderName, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.UploadedBy = parseUUIDPtr(uploader)
		item.DownloadURL = "/api/v1/projects/" + projectID.String() + "/attachments/" + item.ID.String() + "/download"
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) loadWatchers(ctx context.Context, projectID, itemID uuid.UUID) ([]Watcher, error) {
	rows, err := s.db.Query(ctx, `
		SELECT w.user_id, u.username, u.display_name, w.created_at
		FROM work_item_watchers w
		JOIN users u ON u.id = w.user_id
		WHERE w.project_id = $1 AND w.work_item_id = $2
		ORDER BY u.username`, projectID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Watcher{}
	for rows.Next() {
		var watcher Watcher
		if err := rows.Scan(&watcher.UserID, &watcher.Username, &watcher.DisplayName, &watcher.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, watcher)
	}
	return items, rows.Err()
}

func (s *Store) isWatching(ctx context.Context, itemID uuid.UUID, viewer *uuid.UUID) (bool, error) {
	if viewer == nil {
		return false, nil
	}
	var watching bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM work_item_watchers WHERE work_item_id = $1 AND user_id = $2)`,
		itemID, *viewer).Scan(&watching)
	return watching, err
}

// loadLinkedArtifacts returns links from both directions so the drawer shows
// the relationship the same way from either artifact.
func (s *Store) loadLinkedArtifacts(ctx context.Context, projectID, itemID uuid.UUID) ([]LinkedArtifact, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, source_type, source_id, target_type, target_id, link_type
		FROM artifact_links
		WHERE project_id = $1
		  AND ((source_type = 'work_item' AND source_id = $2) OR (target_type = 'work_item' AND target_id = $2))
		ORDER BY created_at DESC`, projectID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []LinkedArtifact{}
	for rows.Next() {
		var id, sourceID, targetID uuid.UUID
		var sourceType, targetType, linkType string
		if err := rows.Scan(&id, &sourceType, &sourceID, &targetType, &targetID, &linkType); err != nil {
			return nil, err
		}
		link := LinkedArtifact{LinkID: id, LinkType: linkType, Direction: "outbound"}
		otherType, otherID := targetType, targetID
		if sourceType == "work_item" && sourceID == itemID {
			link.ArtifactType, link.ArtifactID = targetType, targetID
		} else {
			link.Direction = "inbound"
			link.ArtifactType, link.ArtifactID = sourceType, sourceID
			otherType, otherID = sourceType, sourceID
		}
		link.ArtifactKey, link.Title, link.Status = s.artifactSummary(ctx, projectID, otherType, otherID)
		items = append(items, link)
	}
	return items, rows.Err()
}

func (s *Store) loadWorkItemActivity(ctx context.Context, projectID, itemID uuid.UUID) ([]ActivityEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, project_id, COALESCE(actor_id::text, ''), entity_type, COALESCE(entity_id::text, ''),
		       action, summary, COALESCE(metadata, '{}'::jsonb), created_at
		FROM activity_events
		WHERE project_id = $1 AND entity_type = 'work_item' AND entity_id = $2
		ORDER BY created_at DESC LIMIT 100`, projectID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []ActivityEvent{}
	for rows.Next() {
		var event ActivityEvent
		var actor, entity string
		var metadata []byte
		if err := rows.Scan(&event.ID, &event.ProjectID, &actor, &event.EntityType, &entity,
			&event.Action, &event.Summary, &metadata, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.ActorID = parseUUIDPtr(actor)
		event.EntityID = parseUUIDPtr(entity)
		if len(metadata) > 0 {
			_ = json.Unmarshal(metadata, &event.Metadata)
		}
		if event.Metadata == nil {
			event.Metadata = map[string]interface{}{}
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) loadWorkItemBranches(ctx context.Context, projectID, itemID uuid.UUID) ([]GitBranch, error) {
	rows, err := s.db.Query(ctx, `
		SELECT b.id, b.repository_id, COALESCE(b.work_item_id::text, ''), b.name,
		       COALESCE(b.head_commit_sha, ''), b.status, COALESCE(b.created_by::text, ''),
		       b.created_at, b.updated_at, b.merged_at
		FROM git_branches b
		JOIN git_repositories r ON r.id = b.repository_id
		WHERE r.project_id = $1 AND b.work_item_id = $2
		ORDER BY b.updated_at DESC`, projectID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []GitBranch{}
	for rows.Next() {
		var branch GitBranch
		var item, creator string
		if err := rows.Scan(&branch.ID, &branch.RepositoryID, &item, &branch.Name, &branch.HeadCommitSHA,
			&branch.Status, &creator, &branch.CreatedAt, &branch.UpdatedAt, &branch.MergedAt); err != nil {
			return nil, err
		}
		branch.WorkItemID = parseUUIDPtr(item)
		branch.CreatedBy = parseUUIDPtr(creator)
		items = append(items, branch)
	}
	return items, rows.Err()
}

func (s *Store) loadWorkItemPullRequests(ctx context.Context, projectID, itemID uuid.UUID) ([]PullRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.repository_id, COALESCE(p.work_item_id::text, ''), p.number, p.title,
		       COALESCE(p.description, ''), COALESCE(p.source_branch_id::text, ''), p.target_branch,
		       COALESCE(p.author, ''), p.status, p.review_status, COALESCE(p.merge_commit_sha, ''),
		       p.created_at, p.updated_at, p.merged_at
		FROM pull_requests p
		JOIN git_repositories r ON r.id = p.repository_id
		WHERE r.project_id = $1 AND p.work_item_id = $2
		ORDER BY p.updated_at DESC`, projectID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PullRequest{}
	for rows.Next() {
		var pr PullRequest
		var item, source string
		if err := rows.Scan(&pr.ID, &pr.RepositoryID, &item, &pr.Number, &pr.Title, &pr.Description,
			&source, &pr.TargetBranch, &pr.Author, &pr.Status, &pr.ReviewStatus, &pr.MergeCommitSHA,
			&pr.CreatedAt, &pr.UpdatedAt, &pr.MergedAt); err != nil {
			return nil, err
		}
		pr.WorkItemID = parseUUIDPtr(item)
		pr.SourceBranchID = parseUUIDPtr(source)
		items = append(items, pr)
	}
	return items, rows.Err()
}

// artifactSummary resolves the display key, title and status of any linked
// artifact. The type switch is an allowlist: no caller-supplied table name is
// ever interpolated into SQL.
func (s *Store) artifactSummary(ctx context.Context, projectID uuid.UUID, artifactType string, artifactID uuid.UUID) (string, string, string) {
	query := ""
	switch artifactType {
	case "prd":
		query = `SELECT '', title, status FROM prds WHERE id = $1 AND project_id = $2`
	case "epic":
		query = `SELECT '', title, status FROM epics WHERE id = $1 AND project_id = $2`
	case "feature":
		query = `SELECT '', title, status FROM features WHERE id = $1 AND project_id = $2`
	case "work_item":
		query = `SELECT p.key_prefix || '-' || w.item_number, w.title, w.status
			FROM work_items w JOIN projects p ON p.id = w.project_id
			WHERE w.id = $1 AND w.project_id = $2`
	case "test_case":
		query = `SELECT '', title, status FROM test_cases WHERE id = $1 AND project_id = $2`
	case "sprint":
		query = `SELECT '', name, status FROM sprints WHERE id = $1 AND project_id = $2`
	case "release":
		query = `SELECT '', name, status FROM releases WHERE id = $1 AND project_id = $2`
	default:
		return "", "", ""
	}
	var key, title, status string
	if err := s.db.QueryRow(ctx, query, artifactID, projectID).Scan(&key, &title, &status); err != nil {
		return "", "", ""
	}
	return key, title, status
}

// linkArtifactTables is the allowlist used by work-item scoped link mutations.
var linkArtifactTables = map[string]string{
	"prd":       "prds",
	"epic":      "epics",
	"feature":   "features",
	"work_item": "work_items",
	"test_case": "test_cases",
	"sprint":    "sprints",
	"release":   "releases",
}

func normalizeArtifactType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
