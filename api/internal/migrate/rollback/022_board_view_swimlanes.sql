-- Contract companion for 022_board_view_swimlanes.
-- Reserved for a planned removal after every reader stops using the column.

ALTER TABLE saved_filters DROP COLUMN IF EXISTS swimlane;
