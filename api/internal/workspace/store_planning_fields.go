package workspace

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"
)

// loadPlanningMetadata loads reusable labels, typed custom fields, their
// values and saved filters. Custom-field values deliberately use a typed side
// table, so a project can add a field without a migration.
func (s *Store) loadPlanningMetadata(ctx context.Context, projectID uuid.UUID, w *Workspace) error {
	labelRows, err := s.db.Query(ctx, `
		SELECT id, project_id, name, color, created_by, created_at, updated_at
		FROM project_labels WHERE project_id = $1 ORDER BY name`, projectID)
	if err != nil {
		return err
	}
	defer labelRows.Close()
	w.Labels = []ProjectLabel{}
	for labelRows.Next() {
		var label ProjectLabel
		if err := labelRows.Scan(&label.ID, &label.ProjectID, &label.Name, &label.Color,
			&label.CreatedBy, &label.CreatedAt, &label.UpdatedAt); err != nil {
			return err
		}
		w.Labels = append(w.Labels, label)
	}
	if err := labelRows.Err(); err != nil {
		return err
	}

	fieldRows, err := s.db.Query(ctx, `
		SELECT id, project_id, key, name, type, description, options, config, required,
		       position, archived_at, created_by, created_at, updated_at
		FROM custom_fields WHERE project_id = $1 ORDER BY position, name`, projectID)
	if err != nil {
		return err
	}
	defer fieldRows.Close()
	w.CustomFields = []CustomField{}
	for fieldRows.Next() {
		var field CustomField
		var rawOptions, rawConfig []byte
		if err := fieldRows.Scan(&field.ID, &field.ProjectID, &field.Key, &field.Name, &field.Type,
			&field.Description, &rawOptions, &rawConfig, &field.Required, &field.Position,
			&field.ArchivedAt, &field.CreatedBy, &field.CreatedAt, &field.UpdatedAt); err != nil {
			return err
		}
		field.Options = decodeOptions(rawOptions)
		field.Config = decodeFilter(rawConfig)
		w.CustomFields = append(w.CustomFields, field)
	}
	if err := fieldRows.Err(); err != nil {
		return err
	}

	valueRows, err := s.db.Query(ctx, `
		SELECT v.id, v.project_id, v.field_id, v.entity_type, v.entity_id,
		       CASE f.type
		         WHEN 'number' THEN v.value_number::text
		         WHEN 'date' THEN to_char(v.value_date, 'YYYY-MM-DD')
		         WHEN 'user' THEN COALESCE(v.value_user_id::text, '')
		         ELSE v.value_text
		       END,
		       f.type, v.updated_at
		FROM custom_field_values v
		JOIN custom_fields f ON f.id = v.field_id
		WHERE v.project_id = $1
		ORDER BY v.entity_type, v.entity_id, f.position`, projectID)
	if err != nil {
		return err
	}
	defer valueRows.Close()
	w.FieldValues = []CustomFieldValue{}
	for valueRows.Next() {
		var value CustomFieldValue
		var raw, fieldType string
		if err := valueRows.Scan(&value.ID, &value.ProjectID, &value.FieldID, &value.EntityType,
			&value.EntityID, &raw, &fieldType, &value.UpdatedAt); err != nil {
			return err
		}
		switch fieldType {
		case "number":
			if number, err := strconv.ParseFloat(raw, 64); err == nil {
				value.Value = number
			} else {
				value.Value = raw
			}
		default:
			value.Value = raw
		}
		w.FieldValues = append(w.FieldValues, value)
	}
	if err := valueRows.Err(); err != nil {
		return err
	}

	filterRows, err := s.db.Query(ctx, `
		SELECT id, project_id, name, scope, visibility, owner_id, filters, sort, columns,
		       pinned, is_default, created_at, updated_at, revision
		FROM saved_filters WHERE project_id = $1 ORDER BY pinned DESC, name`, projectID)
	if err != nil {
		return err
	}
	defer filterRows.Close()
	w.SavedFilters = []SavedFilter{}
	for filterRows.Next() {
		var filter SavedFilter
		var rawFilters, rawSort, rawColumns []byte
		if err := filterRows.Scan(&filter.ID, &filter.ProjectID, &filter.Name, &filter.Scope,
			&filter.Visibility, &filter.OwnerID, &rawFilters, &rawSort, &rawColumns,
			&filter.Pinned, &filter.IsDefault, &filter.CreatedAt, &filter.UpdatedAt,
			&filter.Revision); err != nil {
			return err
		}
		filter.Filters = decodeFilter(rawFilters)
		filter.Sort = decodeFilter(rawSort)
		filter.Columns = decodeStringSlice(rawColumns)
		w.SavedFilters = append(w.SavedFilters, filter)
	}
	return filterRows.Err()
}

func decodeOptions(raw []byte) []CustomFieldOption {
	options := []CustomFieldOption{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &options)
	}
	return options
}

func decodeFilter(raw []byte) map[string]any {
	values := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &values)
	}
	return values
}

func decodeStringSlice(raw []byte) []string {
	values := []string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &values)
	}
	return values
}
