package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"spm-platform/api/internal/audit"
	"spm-platform/api/internal/auth"
)

const (
	maxSavedFilterBytes = 16 * 1024
	maxFieldOptions     = 50
)

var (
	labelColorTokens = map[string]bool{
		"slate": true, "blue": true, "green": true, "amber": true,
		"red": true, "purple": true, "pink": true, "teal": true,
	}
	customFieldKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	optionValuePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	labelNamePattern      = regexp.MustCompile(`^[a-z0-9 _-]{1,24}$`)
)

// ---------------------------------------------------------------------------
// Labels
// ---------------------------------------------------------------------------

type labelRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type labelInput struct {
	Name  string
	Color string
}

func normalizeLabelInput(req labelRequest) (labelInput, error) {
	names, err := normalizeLabels([]string{req.Name})
	if err != nil {
		return labelInput{}, err
	}
	if len(names) != 1 {
		return labelInput{}, errors.New("Label name is required")
	}
	if !labelNamePattern.MatchString(names[0]) {
		return labelInput{}, errors.New("Label names must use lowercase letters, digits, spaces, hyphens or underscores")
	}
	color := strings.ToLower(strings.TrimSpace(req.Color))
	if color == "" {
		color = "slate"
	}
	if !labelColorTokens[color] {
		return labelInput{}, errors.New("Label color must be one of slate, blue, green, amber, red, purple, pink or teal")
	}
	return labelInput{Name: names[0], Color: color}, nil
}

// ListLabels handles GET /projects/:id/labels.
func (h *Handler) ListLabels(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	w, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
		return
	}
	c.JSON(http.StatusOK, w.Labels)
}

// CreateLabel handles POST /projects/:id/labels.
func (h *Handler) CreateLabel(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req labelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	input, err := normalizeLabelInput(req)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	var label ProjectLabel
	err = h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO project_labels (project_id, name, color, created_by)
		VALUES ($1,$2,$3,$4)
		RETURNING id, project_id, name, color, created_by, created_at, updated_at`,
		projectID, input.Name, input.Color, actorID(c)).Scan(
		&label.ID, &label.ProjectID, &label.Name, &label.Color, &label.CreatedBy,
		&label.CreatedAt, &label.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			apiError(c, http.StatusConflict, "LABEL_EXISTS", "A label with that name already exists")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create label")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "label", &label.ID, "created",
		"Created label "+label.Name, map[string]interface{}{"color": label.Color})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "label.create", TargetType: "label", TargetID: label.ID.String(),
		Result: "success", After: label,
	})
	c.JSON(http.StatusCreated, label)
}

// UpdateLabel handles PUT /projects/:id/labels/:labelId. Renaming a label also
// rewrites the denormalized work item arrays in the same transaction.
func (h *Handler) UpdateLabel(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	labelID, ok := parseParamID(c, "labelId")
	if !ok {
		return
	}
	var req labelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	input, err := normalizeLabelInput(req)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update label")
		return
	}
	defer tx.Rollback(c.Request.Context())
	var previousName string
	if err := tx.QueryRow(c.Request.Context(),
		`SELECT name FROM project_labels WHERE id=$1 AND project_id=$2 FOR UPDATE`,
		labelID, projectID).Scan(&previousName); errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Label not found")
		return
	} else if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update label")
		return
	}
	var label ProjectLabel
	err = tx.QueryRow(c.Request.Context(), `
		UPDATE project_labels SET name=$3, color=$4, updated_at=NOW()
		WHERE id=$1 AND project_id=$2
		RETURNING id, project_id, name, color, created_by, created_at, updated_at`,
		labelID, projectID, input.Name, input.Color).Scan(
		&label.ID, &label.ProjectID, &label.Name, &label.Color, &label.CreatedBy,
		&label.CreatedAt, &label.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			apiError(c, http.StatusConflict, "LABEL_EXISTS", "A label with that name already exists")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update label")
		return
	}
	if previousName != input.Name {
		if _, err := tx.Exec(c.Request.Context(), `
			UPDATE work_items SET labels = array_replace(labels, $2, $3), updated_at=NOW()
			WHERE project_id=$1 AND $2 = ANY(labels)`, projectID, previousName, input.Name); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to rename label usages")
			return
		}
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update label")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "label", &labelID, "updated",
		"Updated label "+label.Name, map[string]interface{}{"previous_name": previousName})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "label.update", TargetType: "label", TargetID: labelID.String(),
		Result: "success",
		Before: map[string]any{"name": previousName},
		After:  map[string]any{"name": label.Name, "color": label.Color},
	})
	c.JSON(http.StatusOK, label)
}

// DeleteLabel handles DELETE /projects/:id/labels/:labelId. Work items keep
// every other label; only the removed name is stripped.
func (h *Handler) DeleteLabel(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	labelID, ok := parseParamID(c, "labelId")
	if !ok {
		return
	}
	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete label")
		return
	}
	defer tx.Rollback(c.Request.Context())
	var name string
	if err := tx.QueryRow(c.Request.Context(),
		`DELETE FROM project_labels WHERE id=$1 AND project_id=$2 RETURNING name`,
		labelID, projectID).Scan(&name); errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Label not found")
		return
	} else if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete label")
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE work_items SET labels = array_remove(labels, $2), updated_at=NOW()
		WHERE project_id=$1 AND $2 = ANY(labels)`, projectID, name); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to remove label usages")
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete label")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "label", &labelID, "deleted",
		"Deleted label "+name, nil)
	h.recordAudit(c, projectID, audit.Entry{
		Action: "label.delete", TargetType: "label", TargetID: labelID.String(),
		Result: "success", Before: map[string]any{"name": name},
	})
	c.Status(http.StatusNoContent)
}

// ensureProjectLabels registers any label used on a work item so it becomes
// reusable and colorable without blocking legacy free-form writes.
func (h *Handler) ensureProjectLabels(ctx context.Context, projectID uuid.UUID, labels []string, actor *uuid.UUID) {
	if len(labels) == 0 {
		return
	}
	_, _ = h.store.DB().Exec(ctx, `
		INSERT INTO project_labels (project_id, name, color, created_by)
		SELECT $1, label, 'slate', $3
		FROM unnest($2::text[]) AS label
		WHERE label ~ '^[a-z0-9 _-]{1,24}$'
		ON CONFLICT (project_id, name) DO NOTHING`, projectID, labels, actor)
}

// ---------------------------------------------------------------------------
// Custom fields
// ---------------------------------------------------------------------------

type customFieldRequest struct {
	Key         string              `json:"key"`
	Name        string              `json:"name"`
	Type        string              `json:"type"`
	Description string              `json:"description"`
	Options     []CustomFieldOption `json:"options"`
	Config      map[string]any      `json:"config"`
	Required    bool                `json:"required"`
	Position    int                 `json:"position"`
	Archived    *bool               `json:"archived"`
}

func slugifyFieldKey(value string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case !lastUnderscore:
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	key := strings.Trim(b.String(), "_")
	if key == "" {
		key = "field"
	}
	if key[0] >= '0' && key[0] <= '9' {
		key = "f_" + key
	}
	if len(key) > 40 {
		key = strings.TrimRight(key[:40], "_")
	}
	return key
}

func validCustomFieldType(value string) bool {
	switch value {
	case "text", "number", "select", "date", "user":
		return true
	}
	return false
}

func validateFieldOptions(fieldType string, options []CustomFieldOption) ([]CustomFieldOption, error) {
	switch fieldType {
	case "select":
		if len(options) == 0 {
			return nil, errors.New("A select field needs at least one option")
		}
		if len(options) > maxFieldOptions {
			return nil, fmt.Errorf("A select field may have at most %d options", maxFieldOptions)
		}
		normalized := make([]CustomFieldOption, 0, len(options))
		seen := map[string]bool{}
		for _, option := range options {
			value := strings.ToLower(strings.TrimSpace(option.Value))
			label := strings.TrimSpace(option.Label)
			if value == "" {
				value = slugifyFieldKey(label)
			}
			if !optionValuePattern.MatchString(value) {
				return nil, fmt.Errorf("Option value %q must use lowercase letters, digits, hyphens or underscores", value)
			}
			if label == "" {
				label = value
			}
			if seen[value] {
				return nil, fmt.Errorf("Duplicate option value %q", value)
			}
			seen[value] = true
			if option.Color != "" && !labelColorTokens[option.Color] {
				return nil, errors.New("Option colors must use a label color token")
			}
			normalized = append(normalized, CustomFieldOption{Value: value, Label: label, Color: option.Color})
		}
		return normalized, nil
	default:
		if len(options) > 0 {
			return nil, errors.New("Only select fields may define options")
		}
		return []CustomFieldOption{}, nil
	}
}

func validateFieldConfig(fieldType string, config map[string]any) (map[string]any, error) {
	if config == nil {
		config = map[string]any{}
	}
	switch fieldType {
	case "number":
		for _, key := range []string{"min", "max"} {
			if raw, exists := config[key]; exists && raw != nil {
				number, ok := raw.(float64)
				if !ok {
					return nil, fmt.Errorf("%s must be a number", key)
				}
				config[key] = number
			}
		}
		if rawMin, ok := config["min"].(float64); ok {
			if rawMax, ok := config["max"].(float64); ok && rawMin > rawMax {
				return nil, errors.New("Minimum cannot be greater than maximum")
			}
		}
	default:
		config = map[string]any{}
	}
	return config, nil
}

// ListCustomFields handles GET /projects/:id/custom-fields.
func (h *Handler) ListCustomFields(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	w, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
		return
	}
	fields := w.CustomFields
	if c.Query("include_archived") != "true" {
		fields = make([]CustomField, 0, len(w.CustomFields))
		for _, field := range w.CustomFields {
			if field.ArchivedAt == nil {
				fields = append(fields, field)
			}
		}
	}
	c.JSON(http.StatusOK, fields)
}

// CreateCustomField handles POST /projects/:id/custom-fields.
func (h *Handler) CreateCustomField(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	var req customFieldRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 80 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Field name is required and must be at most 80 characters")
		return
	}
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	if !validCustomFieldType(req.Type) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid custom field type")
		return
	}
	key := strings.ToLower(strings.TrimSpace(req.Key))
	if key == "" {
		key = slugifyFieldKey(req.Name)
	}
	if !customFieldKeyPattern.MatchString(key) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Field key must start with a letter and use lowercase letters, digits or underscores")
		return
	}
	options, err := validateFieldOptions(req.Type, req.Options)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	config, err := validateFieldConfig(req.Type, req.Config)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	rawOptions, _ := json.Marshal(options)
	rawConfig, _ := json.Marshal(config)
	position := req.Position
	var field CustomField
	err = h.store.DB().QueryRow(c.Request.Context(), `
		INSERT INTO custom_fields (project_id, key, name, type, description, options, config, required, position, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,
		        CASE WHEN $9 > 0 THEN $9 ELSE (SELECT COALESCE(MAX(position), 0) + 1 FROM custom_fields WHERE project_id=$1) END,
		        $10)
		RETURNING id, project_id, key, name, type, description, options, config, required,
		          position, archived_at, created_by, created_at, updated_at`,
		projectID, key, req.Name, req.Type, strings.TrimSpace(req.Description), rawOptions, rawConfig,
		req.Required, position, actorID(c)).Scan(
		&field.ID, &field.ProjectID, &field.Key, &field.Name, &field.Type, &field.Description,
		&rawOptions, &rawConfig, &field.Required, &field.Position, &field.ArchivedAt,
		&field.CreatedBy, &field.CreatedAt, &field.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			apiError(c, http.StatusConflict, "FIELD_EXISTS", "A custom field with that key already exists")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create custom field")
		return
	}
	field.Options = decodeOptions(rawOptions)
	field.Config = decodeFilter(rawConfig)
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "custom_field", &field.ID, "created",
		"Created custom field "+field.Name, map[string]interface{}{"type": field.Type})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "custom_field.create", TargetType: "custom_field", TargetID: field.ID.String(),
		Result: "success", After: field,
	})
	c.JSON(http.StatusCreated, field)
}

// UpdateCustomField handles PUT /projects/:id/custom-fields/:fieldId. A type
// change is rejected once values exist because the typed columns would no
// longer match the definition.
func (h *Handler) UpdateCustomField(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	fieldID, ok := parseParamID(c, "fieldId")
	if !ok {
		return
	}
	var req customFieldRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 80 {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Field name is required and must be at most 80 characters")
		return
	}
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	if !validCustomFieldType(req.Type) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid custom field type")
		return
	}
	key := strings.ToLower(strings.TrimSpace(req.Key))
	if key == "" {
		key = slugifyFieldKey(req.Name)
	}
	if !customFieldKeyPattern.MatchString(key) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Field key must start with a letter and use lowercase letters, digits or underscores")
		return
	}
	options, err := validateFieldOptions(req.Type, req.Options)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	config, err := validateFieldConfig(req.Type, req.Config)
	if err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	var previousType string
	var previousName string
	if err := h.store.DB().QueryRow(c.Request.Context(),
		`SELECT type, name FROM custom_fields WHERE id=$1 AND project_id=$2`,
		fieldID, projectID).Scan(&previousType, &previousName); errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Custom field not found")
		return
	} else if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load custom field")
		return
	}
	if previousType != req.Type {
		var used bool
		if err := h.store.DB().QueryRow(c.Request.Context(),
			`SELECT EXISTS (SELECT 1 FROM custom_field_values WHERE field_id=$1)`, fieldID).Scan(&used); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to inspect custom field values")
			return
		}
		if used {
			apiError(c, http.StatusConflict, "FIELD_TYPE_LOCKED", "A custom field with stored values cannot change type; archive it and create a new field")
			return
		}
	}
	rawOptions, _ := json.Marshal(options)
	rawConfig, _ := json.Marshal(config)
	archived := false
	if req.Archived != nil {
		archived = *req.Archived
	}
	var archivedAt any
	if archived {
		archivedAt = time.Now()
	}
	var field CustomField
	err = h.store.DB().QueryRow(c.Request.Context(), `
		UPDATE custom_fields SET key=$3, name=$4, type=$5, description=$6, options=$7, config=$8,
		required=$9, position=$10, archived_at=$11, updated_at=NOW()
		WHERE id=$1 AND project_id=$2
		RETURNING id, project_id, key, name, type, description, options, config, required,
		          position, archived_at, created_by, created_at, updated_at`,
		fieldID, projectID, key, req.Name, req.Type, strings.TrimSpace(req.Description), rawOptions,
		rawConfig, req.Required, req.Position, archivedAt).Scan(
		&field.ID, &field.ProjectID, &field.Key, &field.Name, &field.Type, &field.Description,
		&rawOptions, &rawConfig, &field.Required, &field.Position, &field.ArchivedAt,
		&field.CreatedBy, &field.CreatedAt, &field.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			apiError(c, http.StatusConflict, "FIELD_EXISTS", "A custom field with that key already exists")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update custom field")
		return
	}
	field.Options = decodeOptions(rawOptions)
	field.Config = decodeFilter(rawConfig)
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "custom_field", &fieldID, "updated",
		"Updated custom field "+field.Name, nil)
	h.recordAudit(c, projectID, audit.Entry{
		Action: "custom_field.update", TargetType: "custom_field", TargetID: fieldID.String(),
		Result: "success",
		Before: map[string]any{"name": previousName, "type": previousType},
		After:  field,
	})
	c.JSON(http.StatusOK, field)
}

// DeleteCustomField handles DELETE /projects/:id/custom-fields/:fieldId.
// Archiving is the default and preserves every stored value. Purging requires
// settings.write and destroys the values, so it is never implicit.
func (h *Handler) DeleteCustomField(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	fieldID, ok := parseParamID(c, "fieldId")
	if !ok {
		return
	}
	purge := c.Query("purge") == "true"
	if purge && !auth.HasProjectCapability(c, auth.CapSettingsWrite) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Only project owners or maintainers may purge a custom field")
		return
	}
	if purge {
		tx, err := h.store.DB().Begin(c.Request.Context())
		if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete custom field")
			return
		}
		defer tx.Rollback(c.Request.Context())
		var name string
		if err := tx.QueryRow(c.Request.Context(),
			`SELECT name FROM custom_fields WHERE id=$1 AND project_id=$2 FOR UPDATE`,
			fieldID, projectID).Scan(&name); errors.Is(err, pgx.ErrNoRows) {
			apiError(c, http.StatusNotFound, "NOT_FOUND", "Custom field not found")
			return
		} else if err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete custom field")
			return
		}
		if _, err := tx.Exec(c.Request.Context(),
			`DELETE FROM custom_field_values WHERE field_id=$1`, fieldID); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to purge custom field values")
			return
		}
		if _, err := tx.Exec(c.Request.Context(),
			`DELETE FROM custom_fields WHERE id=$1 AND project_id=$2`, fieldID, projectID); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete custom field")
			return
		}
		if err := tx.Commit(c.Request.Context()); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete custom field")
			return
		}
		h.recordActivity(c.Request.Context(), projectID, actorID(c), "custom_field", &fieldID, "purged",
			"Purged custom field "+name, nil)
		h.recordAudit(c, projectID, audit.Entry{
			Action: "custom_field.purge", TargetType: "custom_field", TargetID: fieldID.String(),
			Result: "success", Before: map[string]any{"name": name},
		})
		c.Status(http.StatusNoContent)
		return
	}
	tag, err := h.store.DB().Exec(c.Request.Context(), `
		UPDATE custom_fields SET archived_at=COALESCE(archived_at, NOW()), updated_at=NOW()
		WHERE id=$1 AND project_id=$2`, fieldID, projectID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to archive custom field")
		return
	}
	if tag.RowsAffected() == 0 {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Custom field not found")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "custom_field", &fieldID, "archived",
		"Archived custom field", nil)
	h.recordAudit(c, projectID, audit.Entry{
		Action: "custom_field.archive", TargetType: "custom_field", TargetID: fieldID.String(),
		Result: "success",
	})
	c.Status(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Custom field values
// ---------------------------------------------------------------------------

type customValuesRequest struct {
	Values map[string]json.RawMessage `json:"values"`
}

type parsedCustomValue struct {
	empty  bool
	text   string
	number *float64
	date   *time.Time
	user   *uuid.UUID
}

func parseCustomValue(field CustomField, raw json.RawMessage) (parsedCustomValue, error) {
	parsed := parsedCustomValue{}
	if len(raw) == 0 || string(raw) == "null" {
		parsed.empty = true
		return parsed, nil
	}
	switch field.Type {
	case "text":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return parsed, errors.New("must be text")
		}
		value = strings.TrimSpace(value)
		if len([]rune(value)) > 2000 {
			return parsed, errors.New("must be at most 2000 characters")
		}
		parsed.text = value
		parsed.empty = value == ""
	case "number":
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return parsed, errors.New("must be a number")
		}
		if min, ok := field.Config["min"].(float64); ok && value < min {
			return parsed, fmt.Errorf("must be at least %g", min)
		}
		if max, ok := field.Config["max"].(float64); ok && value > max {
			return parsed, fmt.Errorf("must be at most %g", max)
		}
		parsed.number = &value
	case "select":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return parsed, errors.New("must be one of the configured options")
		}
		value = strings.TrimSpace(value)
		valid := false
		for _, option := range field.Options {
			if option.Value == value {
				valid = true
				break
			}
		}
		if value != "" && !valid {
			return parsed, errors.New("must be one of the configured options")
		}
		parsed.text = value
		parsed.empty = value == ""
	case "date":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return parsed, errors.New("must be a YYYY-MM-DD date")
		}
		value = strings.TrimSpace(value)
		if value == "" {
			parsed.empty = true
			return parsed, nil
		}
		parsedDate, err := time.Parse(time.DateOnly, value)
		if err != nil {
			return parsed, errors.New("must be a YYYY-MM-DD date")
		}
		parsed.date = &parsedDate
	case "user":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return parsed, errors.New("must be a user ID")
		}
		value = strings.TrimSpace(value)
		if value == "" {
			parsed.empty = true
			return parsed, nil
		}
		userID, err := uuid.Parse(value)
		if err != nil {
			return parsed, errors.New("must be a user ID")
		}
		parsed.user = &userID
	}
	return parsed, nil
}

func (h *Handler) loadCustomFieldValues(ctx context.Context, projectID, entityID uuid.UUID, entityType string) ([]CustomFieldValue, error) {
	rows, err := h.store.DB().Query(ctx, `
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
		WHERE v.project_id=$1 AND v.entity_type=$2 AND v.entity_id=$3
		ORDER BY f.position, f.name`, projectID, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []CustomFieldValue{}
	for rows.Next() {
		var value CustomFieldValue
		var raw, fieldType string
		if err := rows.Scan(&value.ID, &value.ProjectID, &value.FieldID, &value.EntityType,
			&value.EntityID, &raw, &fieldType, &value.UpdatedAt); err != nil {
			return nil, err
		}
		if fieldType == "number" {
			if number, err := strconv.ParseFloat(raw, 64); err == nil {
				value.Value = number
			} else {
				value.Value = raw
			}
		} else {
			value.Value = raw
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func requiredFieldMissing(fields map[uuid.UUID]CustomField, parsed map[uuid.UUID]parsedCustomValue) (CustomField, bool) {
	for fieldID, field := range fields {
		if field.ArchivedAt != nil || !field.Required {
			continue
		}
		if value, exists := parsed[fieldID]; !exists || value.empty {
			return field, true
		}
	}
	return CustomField{}, false
}

// GetWorkItemCustomFields handles GET /projects/:id/work-items/:itemId/custom-fields.
func (h *Handler) GetWorkItemCustomFields(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	itemID, ok := parseParamID(c, "itemId")
	if !ok {
		return
	}
	var exists bool
	if err := h.store.DB().QueryRow(c.Request.Context(),
		`SELECT EXISTS (SELECT 1 FROM work_items WHERE id=$1 AND project_id=$2)`,
		itemID, projectID).Scan(&exists); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load work item")
		return
	}
	if !exists {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Work item not found")
		return
	}
	values, err := h.loadCustomFieldValues(c.Request.Context(), projectID, itemID, "work_item")
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load custom field values")
		return
	}
	c.JSON(http.StatusOK, values)
}

// PutWorkItemCustomFields handles PUT /projects/:id/work-items/:itemId/custom-fields.
// The request replaces the full value set; an omitted field is cleared.
func (h *Handler) PutWorkItemCustomFields(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	if auth.ProjectRoleFrom(c) == auth.ProjectRoleTester {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Testers can change work item status but cannot edit custom fields")
		return
	}
	itemID, ok := parseParamID(c, "itemId")
	if !ok {
		return
	}
	var req customValuesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if req.Values == nil {
		req.Values = map[string]json.RawMessage{}
	}
	var exists bool
	if err := h.store.DB().QueryRow(c.Request.Context(),
		`SELECT EXISTS (SELECT 1 FROM work_items WHERE id=$1 AND project_id=$2)`,
		itemID, projectID).Scan(&exists); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load work item")
		return
	}
	if !exists {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Work item not found")
		return
	}

	fieldRows, err := h.store.DB().Query(c.Request.Context(), `
		SELECT id, project_id, key, name, type, description, options, config, required,
		       position, archived_at, created_by, created_at, updated_at
		FROM custom_fields WHERE project_id=$1`, projectID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load custom fields")
		return
	}
	fields := map[uuid.UUID]CustomField{}
	for fieldRows.Next() {
		var field CustomField
		var rawOptions, rawConfig []byte
		if err := fieldRows.Scan(&field.ID, &field.ProjectID, &field.Key, &field.Name, &field.Type,
			&field.Description, &rawOptions, &rawConfig, &field.Required, &field.Position,
			&field.ArchivedAt, &field.CreatedBy, &field.CreatedAt, &field.UpdatedAt); err != nil {
			fieldRows.Close()
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load custom fields")
			return
		}
		field.Options = decodeOptions(rawOptions)
		field.Config = decodeFilter(rawConfig)
		fields[field.ID] = field
	}
	fieldRows.Close()
	if err := fieldRows.Err(); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load custom fields")
		return
	}

	parsed := map[uuid.UUID]parsedCustomValue{}
	for rawID, rawValue := range req.Values {
		fieldID, err := uuid.Parse(rawID)
		if err != nil {
			apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Custom field IDs must be UUIDs")
			return
		}
		field, exists := fields[fieldID]
		if !exists {
			apiError(c, http.StatusUnprocessableEntity, "INVALID_FIELD", "Custom field does not belong to this project")
			return
		}
		if field.ArchivedAt != nil {
			apiError(c, http.StatusUnprocessableEntity, "INVALID_FIELD", "Custom field "+field.Name+" is archived")
			return
		}
		value, err := parseCustomValue(field, rawValue)
		if err != nil {
			apiError(c, http.StatusUnprocessableEntity, "INVALID_VALUE", field.Name+" "+err.Error())
			return
		}
		if value.user != nil {
			var member bool
			if err := h.store.DB().QueryRow(c.Request.Context(),
				`SELECT EXISTS (SELECT 1 FROM project_members WHERE project_id=$1 AND user_id=$2)`,
				projectID, *value.user).Scan(&member); err != nil {
				apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to validate user field")
				return
			}
			if !member {
				apiError(c, http.StatusUnprocessableEntity, "INVALID_VALUE", field.Name+" must reference a project member")
				return
			}
		}
		parsed[fieldID] = value
	}
	if field, missing := requiredFieldMissing(fields, parsed); missing {
		apiError(c, http.StatusUnprocessableEntity, "REQUIRED_FIELD", field.Name+" is required")
		return
	}

	before, _ := h.loadCustomFieldValues(c.Request.Context(), projectID, itemID, "work_item")
	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save custom field values")
		return
	}
	defer tx.Rollback(c.Request.Context())
	if _, err := tx.Exec(c.Request.Context(), `
		DELETE FROM custom_field_values
		WHERE project_id=$1 AND entity_type='work_item' AND entity_id=$2
		  AND field_id IN (SELECT id FROM custom_fields WHERE archived_at IS NULL)`,
		projectID, itemID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save custom field values")
		return
	}
	for fieldID, value := range parsed {
		if value.empty {
			continue
		}
		if _, err := tx.Exec(c.Request.Context(), `
			INSERT INTO custom_field_values (project_id, field_id, entity_type, entity_id,
				value_text, value_number, value_date, value_user_id)
			VALUES ($1,$2,'work_item',$3,$4,$5,$6,$7)`,
			projectID, fieldID, itemID, value.text, value.number, value.date, value.user); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save custom field values")
			return
		}
	}
	if _, err := tx.Exec(c.Request.Context(),
		`UPDATE work_items SET updated_at=NOW() WHERE id=$1`, itemID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save custom field values")
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save custom field values")
		return
	}
	after, _ := h.loadCustomFieldValues(c.Request.Context(), projectID, itemID, "work_item")
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "work_item", &itemID, "custom_fields_updated",
		"Updated custom fields", nil)
	h.recordAudit(c, projectID, audit.Entry{
		Action: "work_item.custom_fields.update", TargetType: "work_item", TargetID: itemID.String(),
		Result: "success", Before: before, After: after,
	})
	c.JSON(http.StatusOK, after)
}

// ---------------------------------------------------------------------------
// Saved filters
// ---------------------------------------------------------------------------

type savedFilterRequest struct {
	Name            string         `json:"name"`
	Scope           string         `json:"scope"`
	Visibility      string         `json:"visibility"`
	Filters         map[string]any `json:"filters"`
	Sort            map[string]any `json:"sort"`
	Columns         []string       `json:"columns"`
	Swimlane        string         `json:"swimlane"`
	Pinned          bool           `json:"pinned"`
	IsDefault       bool           `json:"is_default"`
	ExpectedVersion *int64         `json:"expected_version"`
}

func validFilterScope(scope string) bool {
	switch scope {
	case "planning", "board", "test":
		return true
	}
	return false
}

// validSwimlane is the closed set of board swimlane groupings. "none" is the
// ungrouped default; every other value groups cards by one visible facet.
func validSwimlane(swimlane string) bool {
	switch swimlane {
	case "none", "assignee", "priority", "type":
		return true
	}
	return false
}

func validateSavedFilter(req *savedFilterRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 80 {
		return errors.New("Saved filter name is required and must be at most 80 characters")
	}
	req.Scope = strings.ToLower(strings.TrimSpace(req.Scope))
	if !validFilterScope(req.Scope) {
		return errors.New("Saved filter scope must be planning, board or test")
	}
	if req.Visibility == "" {
		req.Visibility = "private"
	}
	if req.Visibility != "private" && req.Visibility != "shared" {
		return errors.New("Visibility must be private or shared")
	}
	if req.Filters == nil {
		req.Filters = map[string]any{}
	}
	if req.Sort == nil {
		req.Sort = map[string]any{}
	}
	if len(req.Columns) > 20 {
		return errors.New("At most 20 columns can be saved")
	}
	for index, column := range req.Columns {
		column = strings.TrimSpace(column)
		if column == "" || len(column) > 40 {
			return errors.New("Saved column names must be 1-40 characters")
		}
		req.Columns[index] = column
	}
	req.Swimlane = strings.ToLower(strings.TrimSpace(req.Swimlane))
	if req.Swimlane == "" {
		req.Swimlane = "none"
	}
	if !validSwimlane(req.Swimlane) {
		return errors.New("Swimlane must be none, assignee, priority or type")
	}
	raw, err := json.Marshal(map[string]any{
		"filters": req.Filters, "sort": req.Sort, "columns": req.Columns, "swimlane": req.Swimlane,
	})
	if err != nil {
		return errors.New("Saved filter is not valid JSON")
	}
	if len(raw) > maxSavedFilterBytes {
		return errors.New("Saved filter is too large")
	}
	return nil
}

func canWriteSharedFilter(c *gin.Context) bool {
	return auth.HasProjectCapability(c, auth.CapPlanningWrite)
}

func canManageSavedFilter(c *gin.Context, filter SavedFilter, actor *uuid.UUID) bool {
	if actor != nil && filter.OwnerID == *actor {
		return true
	}
	if auth.HasProjectCapability(c, auth.CapSettingsWrite) {
		return true
	}
	role := auth.ProjectRoleFrom(c)
	return role == auth.ProjectRoleOwner || role == auth.ProjectRoleMaintainer
}

func savedFilterVisible(filter SavedFilter, actor *uuid.UUID, role string, admin bool) bool {
	if filter.Visibility == "shared" || admin {
		return true
	}
	if actor != nil && filter.OwnerID == *actor {
		return true
	}
	return role == auth.ProjectRoleOwner || role == auth.ProjectRoleMaintainer
}

func filterVisibleSavedFilters(filters []SavedFilter, actor *uuid.UUID, role string, admin bool) []SavedFilter {
	visible := make([]SavedFilter, 0, len(filters))
	for _, filter := range filters {
		if savedFilterVisible(filter, actor, role, admin) {
			visible = append(visible, filter)
		}
	}
	return visible
}

func (h *Handler) visibleSavedFilters(c *gin.Context, filters []SavedFilter) []SavedFilter {
	admin := false
	if claims, ok := auth.ClaimsFrom(c); ok && claims != nil && claims.Role == auth.RoleAdmin {
		admin = true
	}
	return filterVisibleSavedFilters(filters, actorID(c), auth.ProjectRoleFrom(c), admin)
}

// ListSavedFilters handles GET /projects/:id/saved-filters.
func (h *Handler) ListSavedFilters(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	scope := strings.ToLower(strings.TrimSpace(c.Query("scope")))
	if scope != "" && !validFilterScope(scope) {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", "Scope must be planning, board or test")
		return
	}
	w, err := h.store.Load(c.Request.Context(), projectID)
	if err != nil {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Project not found")
		return
	}
	filters := h.visibleSavedFilters(c, w.SavedFilters)
	if scope != "" {
		scoped := make([]SavedFilter, 0, len(filters))
		for _, filter := range filters {
			if filter.Scope == scope {
				scoped = append(scoped, filter)
			}
		}
		filters = scoped
	}
	c.JSON(http.StatusOK, filters)
}

// CreateSavedFilter handles POST /projects/:id/saved-filters.
func (h *Handler) CreateSavedFilter(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	if !auth.HasProjectCapability(c, auth.CapWorkItemWrite) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "You cannot create saved filters in this project")
		return
	}
	var req savedFilterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if err := validateSavedFilter(&req); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	if req.Visibility == "shared" && !canWriteSharedFilter(c) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Shared views require planning write access")
		return
	}
	actor := actorID(c)
	if actor == nil {
		apiError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication is required")
		return
	}
	rawFilters, _ := json.Marshal(req.Filters)
	rawSort, _ := json.Marshal(req.Sort)
	rawColumns, _ := json.Marshal(req.Columns)
	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create saved filter")
		return
	}
	defer tx.Rollback(c.Request.Context())
	if req.IsDefault {
		if _, err := tx.Exec(c.Request.Context(), `
			UPDATE saved_filters SET is_default=FALSE, updated_at=NOW()
			WHERE project_id=$1 AND owner_id=$2 AND scope=$3 AND is_default`,
			projectID, *actor, req.Scope); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create saved filter")
			return
		}
	}
	var filter SavedFilter
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO saved_filters (project_id, name, scope, visibility, owner_id, filters, sort, columns, swimlane, pinned, is_default)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id, project_id, name, scope, visibility, owner_id, filters, sort, columns, swimlane,
		          pinned, is_default, created_at, updated_at, revision`,
		projectID, req.Name, req.Scope, req.Visibility, *actor, rawFilters, rawSort, rawColumns,
		req.Swimlane, req.Pinned, req.IsDefault).Scan(
		&filter.ID, &filter.ProjectID, &filter.Name, &filter.Scope, &filter.Visibility,
		&filter.OwnerID, &rawFilters, &rawSort, &rawColumns, &filter.Swimlane, &filter.Pinned,
		&filter.IsDefault, &filter.CreatedAt, &filter.UpdatedAt, &filter.Revision)
	if err != nil {
		if isUniqueViolation(err) {
			apiError(c, http.StatusConflict, "FILTER_EXISTS", "A saved filter with that name already exists for this scope")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create saved filter")
		return
	}
	filter.Filters = decodeFilter(rawFilters)
	filter.Sort = decodeFilter(rawSort)
	filter.Columns = decodeStringSlice(rawColumns)
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create saved filter")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actor, "saved_filter", &filter.ID, "created",
		"Created saved view "+filter.Name, map[string]interface{}{"scope": filter.Scope})
	h.recordAudit(c, projectID, audit.Entry{
		Action: "saved_filter.create", TargetType: "saved_filter", TargetID: filter.ID.String(),
		Result: "success", After: filter,
	})
	c.JSON(http.StatusCreated, filter)
}

// UpdateSavedFilter handles PUT /projects/:id/saved-filters/:filterId.
func (h *Handler) UpdateSavedFilter(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	filterID, ok := parseParamID(c, "filterId")
	if !ok {
		return
	}
	var req savedFilterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "INVALID_REQUEST", "Request body is not valid JSON")
		return
	}
	if err := validateSavedFilter(&req); err != nil {
		apiError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	existing, err := h.savedFilter(c.Request.Context(), projectID, filterID)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Saved filter not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load saved filter")
		return
	}
	if !canManageSavedFilter(c, existing, actorID(c)) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "You cannot edit this saved filter")
		return
	}
	if req.Visibility == "shared" && !canWriteSharedFilter(c) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "Shared views require planning write access")
		return
	}
	expected, ok := revisionFromRequest(c, req.ExpectedVersion)
	if !ok {
		return
	}
	rawFilters, _ := json.Marshal(req.Filters)
	rawSort, _ := json.Marshal(req.Sort)
	rawColumns, _ := json.Marshal(req.Columns)
	tx, err := h.store.DB().Begin(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update saved filter")
		return
	}
	defer tx.Rollback(c.Request.Context())
	if req.IsDefault {
		if _, err := tx.Exec(c.Request.Context(), `
			UPDATE saved_filters SET is_default=FALSE, updated_at=NOW()
			WHERE project_id=$1 AND owner_id=$2 AND scope=$3 AND is_default AND id<>$4`,
			projectID, existing.OwnerID, req.Scope, filterID); err != nil {
			apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update saved filter")
			return
		}
	}
	var filter SavedFilter
	err = tx.QueryRow(c.Request.Context(), `
		UPDATE saved_filters SET name=$3, scope=$4, visibility=$5, filters=$6, sort=$7, columns=$8,
		swimlane=$9, pinned=$10, is_default=$11, updated_at=NOW()
		WHERE id=$1 AND project_id=$2`+revisionClause(12)+`
		RETURNING id, project_id, name, scope, visibility, owner_id, filters, sort, columns, swimlane,
		          pinned, is_default, created_at, updated_at, revision`,
		filterID, projectID, req.Name, req.Scope, req.Visibility, rawFilters, rawSort, rawColumns,
		req.Swimlane, req.Pinned, req.IsDefault, revisionValue(expected)).Scan(
		&filter.ID, &filter.ProjectID, &filter.Name, &filter.Scope, &filter.Visibility,
		&filter.OwnerID, &rawFilters, &rawSort, &rawColumns, &filter.Swimlane, &filter.Pinned,
		&filter.IsDefault, &filter.CreatedAt, &filter.UpdatedAt, &filter.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		h.revisionFailure(c, "saved filter", "saved_filters", filterID, projectID)
		return
	}
	if err != nil {
		if isUniqueViolation(err) {
			apiError(c, http.StatusConflict, "FILTER_EXISTS", "A saved filter with that name already exists for this scope")
			return
		}
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update saved filter")
		return
	}
	filter.Filters = decodeFilter(rawFilters)
	filter.Sort = decodeFilter(rawSort)
	filter.Columns = decodeStringSlice(rawColumns)
	if err := tx.Commit(c.Request.Context()); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update saved filter")
		return
	}
	setRevisionHeader(c, filter.Revision)
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "saved_filter", &filterID, "updated",
		"Updated saved view "+filter.Name, nil)
	h.recordAudit(c, projectID, audit.Entry{
		Action: "saved_filter.update", TargetType: "saved_filter", TargetID: filterID.String(),
		Result: "success", Before: existing, After: filter,
	})
	c.JSON(http.StatusOK, filter)
}

// DeleteSavedFilter handles DELETE /projects/:id/saved-filters/:filterId.
func (h *Handler) DeleteSavedFilter(c *gin.Context) {
	projectID, ok := parseProjectID(c)
	if !ok {
		return
	}
	filterID, ok := parseParamID(c, "filterId")
	if !ok {
		return
	}
	existing, err := h.savedFilter(c.Request.Context(), projectID, filterID)
	if errors.Is(err, pgx.ErrNoRows) {
		apiError(c, http.StatusNotFound, "NOT_FOUND", "Saved filter not found")
		return
	}
	if err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load saved filter")
		return
	}
	if !canManageSavedFilter(c, existing, actorID(c)) {
		apiError(c, http.StatusForbidden, "FORBIDDEN", "You cannot delete this saved filter")
		return
	}
	if _, err := h.store.DB().Exec(c.Request.Context(),
		`DELETE FROM saved_filters WHERE id=$1 AND project_id=$2`, filterID, projectID); err != nil {
		apiError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete saved filter")
		return
	}
	h.recordActivity(c.Request.Context(), projectID, actorID(c), "saved_filter", &filterID, "deleted",
		"Deleted saved view "+existing.Name, nil)
	h.recordAudit(c, projectID, audit.Entry{
		Action: "saved_filter.delete", TargetType: "saved_filter", TargetID: filterID.String(),
		Result: "success", Before: existing,
	})
	c.Status(http.StatusNoContent)
}

func (h *Handler) savedFilter(ctx context.Context, projectID, filterID uuid.UUID) (SavedFilter, error) {
	var filter SavedFilter
	var rawFilters, rawSort, rawColumns []byte
	err := h.store.DB().QueryRow(ctx, `
		SELECT id, project_id, name, scope, visibility, owner_id, filters, sort, columns, swimlane,
		       pinned, is_default, created_at, updated_at, revision
		FROM saved_filters WHERE id=$1 AND project_id=$2`, filterID, projectID).Scan(
		&filter.ID, &filter.ProjectID, &filter.Name, &filter.Scope, &filter.Visibility,
		&filter.OwnerID, &rawFilters, &rawSort, &rawColumns, &filter.Swimlane, &filter.Pinned,
		&filter.IsDefault, &filter.CreatedAt, &filter.UpdatedAt, &filter.Revision)
	if err != nil {
		return SavedFilter{}, err
	}
	filter.Filters = decodeFilter(rawFilters)
	filter.Sort = decodeFilter(rawSort)
	filter.Columns = decodeStringSlice(rawColumns)
	return filter, nil
}
