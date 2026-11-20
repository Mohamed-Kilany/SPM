-- Contract companion for 017_work_item_collaboration.sql.
--
-- Do not apply this during a routine rollback. It removes columns and tables
-- that the previous API version may still read. It exists so a planned
-- contract step has a reviewed, complete script.

DROP INDEX IF EXISTS attachments_project_status_idx;
ALTER TABLE attachments DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE attachments DROP COLUMN IF EXISTS scan_detail;
ALTER TABLE attachments DROP COLUMN IF EXISTS scan_engine;
ALTER TABLE attachments DROP COLUMN IF EXISTS scan_status;
ALTER TABLE attachments DROP COLUMN IF EXISTS checksum_sha256;
DROP INDEX IF EXISTS comment_mentions_user_idx;

DROP TABLE IF EXISTS comment_mentions;
ALTER TABLE comments DROP COLUMN IF EXISTS edited_at;

DROP INDEX IF EXISTS work_item_watchers_project_idx;
DROP INDEX IF EXISTS work_item_watchers_user_idx;
DROP TABLE IF EXISTS work_item_watchers;

DROP INDEX IF EXISTS work_items_labels_idx;
ALTER TABLE work_items DROP COLUMN IF EXISTS labels;
