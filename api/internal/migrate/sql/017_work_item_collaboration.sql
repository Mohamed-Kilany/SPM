-- 017_work_item_collaboration.sql
-- Expand-only collaboration surfaces for the work item detail drawer.
--
-- Every object here is additive. The matching contract companion lives in
-- migrate/rollback/017_work_item_collaboration.sql and is reserved for a
-- later planned removal, not for a routine rollback.

-- Work item labels are stored as text so the board can filter without a
-- second join table. The GIN index supports label containment queries.
ALTER TABLE work_items ADD COLUMN IF NOT EXISTS labels TEXT[] NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS work_items_labels_idx ON work_items USING GIN (labels);

-- Watchers are explicit subscriptions. project_id is denormalized so a
-- project-scoped listing never has to join work_items.
CREATE TABLE IF NOT EXISTS work_item_watchers (
    work_item_id UUID NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id   UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (work_item_id, user_id)
);
CREATE INDEX IF NOT EXISTS work_item_watchers_user_idx ON work_item_watchers (user_id);
CREATE INDEX IF NOT EXISTS work_item_watchers_project_idx ON work_item_watchers (project_id);

-- Mentions are resolved to project members by the API and stored separately
-- from the comment body so notification and permission checks stay queryable.
ALTER TABLE comments ADD COLUMN IF NOT EXISTS edited_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS comment_mentions (
    comment_id UUID NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (comment_id, user_id)
);
CREATE INDEX IF NOT EXISTS comment_mentions_user_idx ON comment_mentions (user_id);

-- Attachment scanning is mandatory before a download is served. A rejected
-- or pending row is never streamed. deleted_at keeps the audit trail while
-- removing the file from the drawer.
ALTER TABLE attachments ADD COLUMN IF NOT EXISTS checksum_sha256 CHAR(64) NOT NULL DEFAULT '';
ALTER TABLE attachments ADD COLUMN IF NOT EXISTS scan_status VARCHAR(20) NOT NULL DEFAULT 'pending'
    CHECK (scan_status IN ('pending', 'clean', 'rejected'));
ALTER TABLE attachments ADD COLUMN IF NOT EXISTS scan_engine VARCHAR(40) NOT NULL DEFAULT '';
ALTER TABLE attachments ADD COLUMN IF NOT EXISTS scan_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE attachments ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS attachments_project_status_idx ON attachments (project_id, scan_status, created_at DESC);
