-- Migration: 022_board_view_swimlanes
-- Saved board views capture the swimlane grouping alongside filters, sort and
-- visible fields. Expand-only: one nullable-free column with a safe default.
--
-- The matching contract script lives in
-- migrate/rollback/022_board_view_swimlanes.sql.

ALTER TABLE saved_filters
    ADD COLUMN IF NOT EXISTS swimlane VARCHAR(16) NOT NULL DEFAULT 'none'
    CHECK (swimlane IN ('none', 'assignee', 'priority', 'type'));
