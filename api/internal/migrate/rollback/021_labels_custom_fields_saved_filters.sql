-- Contract companion for 021_labels_custom_fields_saved_filters.
-- Reserved for a planned removal after every reader stops using the tables.
-- The work_items.labels array remains in place because it is the display value
-- that survives label deletion.

DROP TRIGGER IF EXISTS planning_revision ON saved_filters;

DROP INDEX IF EXISTS saved_filters_default_idx;
DROP INDEX IF EXISTS saved_filters_owner_idx;
DROP INDEX IF EXISTS saved_filters_project_idx;

DROP TABLE IF EXISTS saved_filters;

DROP INDEX IF EXISTS custom_field_values_user_idx;
DROP INDEX IF EXISTS custom_field_values_entity_idx;
DROP INDEX IF EXISTS custom_field_values_project_idx;

DROP TABLE IF EXISTS custom_field_values;

DROP INDEX IF EXISTS custom_fields_creator_idx;
DROP INDEX IF EXISTS custom_fields_project_idx;

DROP TABLE IF EXISTS custom_fields;

DROP INDEX IF EXISTS project_labels_creator_idx;
DROP INDEX IF EXISTS project_labels_project_idx;

DROP TABLE IF EXISTS project_labels;
