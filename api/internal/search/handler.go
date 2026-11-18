// Package search provides JQL-style work item search and cross-artifact discovery.
package search

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/models"
)

// Handler serves search requests.
type Handler struct {
	db *pgxpool.Pool
}

// NewHandler constructs a search handler.
func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

// WorkItemResult is a single work item match.
type WorkItemResult struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	Type        string `json:"type"`
	Feature     string `json:"feature"`
	Assignee    string `json:"assignee"`
	ProjectID   string `json:"project_id"`
	ProjectKey  string `json:"project_key"`
	ProjectName string `json:"project_name"`
	UpdatedAt   string `json:"updated_at"`
}

// FeatureResult is a single feature match.
type FeatureResult struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	ProjectID   string `json:"project_id"`
	ProjectKey  string `json:"project_key"`
	ProjectName string `json:"project_name"`
	UpdatedAt   string `json:"updated_at"`
}

// TestCaseResult is a single test case match.
type TestCaseResult struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Type        string `json:"type"`
	Feature     string `json:"feature"`
	ProjectID   string `json:"project_id"`
	ProjectKey  string `json:"project_key"`
	ProjectName string `json:"project_name"`
	UpdatedAt   string `json:"updated_at"`
}

// ProjectResult is a project match.
type ProjectResult struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	KeyPrefix string `json:"key_prefix"`
	Status    string `json:"status"`
}

// Response is the unified search payload.
type Response struct {
	Query     string           `json:"query"`
	WorkItems []WorkItemResult `json:"work_items"`
	Features  []FeatureResult  `json:"features"`
	TestCases []TestCaseResult `json:"test_cases"`
	Projects  []ProjectResult  `json:"projects"`
	Total     int              `json:"total"`
	Error     string           `json:"error,omitempty"`
}

// Search handles GET /api/v1/search.
func (h *Handler) Search(c *gin.Context) {
	claims, ok := auth.ClaimsFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	raw := strings.TrimSpace(c.Query("q"))
	limit := 40
	if parsed, err := strconv.Atoi(c.DefaultQuery("limit", "40")); err == nil {
		if parsed < 1 {
			parsed = 1
		}
		if parsed > 100 {
			parsed = 100
		}
		limit = parsed
	}

	projectIDs, err := h.accessibleProjects(c, claims)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve project access"})
		return
	}
	response := Response{
		Query:     raw,
		WorkItems: []WorkItemResult{},
		Features:  []FeatureResult{},
		TestCases: []TestCaseResult{},
		Projects:  []ProjectResult{},
	}
	if len(projectIDs) == 0 {
		c.JSON(http.StatusOK, response)
		return
	}

	parsed := Parse(raw)
	items, err := h.searchWorkItems(c, parsed, projectIDs, claims.CanonicalUserID(), limit)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	response.WorkItems = items

	freeText := freeTextQuery(parsed)
	if freeText != "" {
		response.Features, _ = h.searchFeatures(c, freeText, projectIDs, limit)
		response.TestCases, _ = h.searchTestCases(c, freeText, projectIDs, limit)
		response.Projects, _ = h.searchProjects(c, freeText, projectIDs, 12)
	}
	response.Total = len(response.WorkItems) + len(response.Features) + len(response.TestCases) + len(response.Projects)
	c.JSON(http.StatusOK, response)
}

func (h *Handler) accessibleProjects(c *gin.Context, claims *models.AuthClaims) ([]string, error) {
	if claims == nil {
		return nil, fmt.Errorf("missing claims")
	}
	if claims.Role == auth.RoleAdmin {
		rows, err := h.db.Query(c, `SELECT id::text FROM projects`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanIDs(rows)
	}
	rows, err := h.db.Query(c, `SELECT project_id::text FROM project_members WHERE user_id = $1::uuid`, claims.CanonicalUserID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIDs(rows)
}

func scanIDs(rows pgx.Rows) ([]string, error) {
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func freeTextQuery(query Query) string {
	var parts []string
	for _, group := range query.Groups {
		for _, term := range group.Terms {
			if term.FreeText {
				parts = append(parts, term.Value)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func (h *Handler) searchWorkItems(
	c *gin.Context,
	query Query,
	projectIDs []string,
	userID string,
	limit int,
) ([]WorkItemResult, error) {
	args := []interface{}{projectIDs}
	where := "p.id::text = ANY($1)"

	groupConditions := make([]string, 0, len(query.Groups))
	for _, group := range query.Groups {
		conditions := make([]string, 0, len(group.Terms))
		for _, term := range group.Terms {
			condition, err := buildTerm(term, &args, userID)
			if err != nil {
				return nil, err
			}
			if condition != "" {
				conditions = append(conditions, condition)
			}
		}
		if len(conditions) > 0 {
			groupConditions = append(groupConditions, "("+strings.Join(conditions, " AND ")+")")
		}
	}
	if len(groupConditions) > 0 {
		where += " AND (" + strings.Join(groupConditions, " OR ") + ")"
	}

	args = append(args, limit)
	sql := fmt.Sprintf(`
		SELECT wi.id::text, (p.key_prefix || '-' || wi.item_number), wi.title, wi.description, wi.status, wi.priority, wi.type,
		       COALESCE(f.title, ''), COALESCE(NULLIF(u.display_name, ''), u.username, u.email, ''),
		       p.id::text, p.key_prefix, p.name, wi.updated_at::text
		FROM work_items wi
		JOIN projects p ON p.id = wi.project_id
		LEFT JOIN features f ON f.id = wi.feature_id
		LEFT JOIN sprints s ON s.id = wi.sprint_id
		LEFT JOIN releases r ON r.id = wi.release_id
		LEFT JOIN users u ON u.id = wi.assignee_id
		LEFT JOIN users ru ON ru.id = wi.reporter_id
		WHERE %s
		ORDER BY wi.updated_at DESC
		LIMIT $%d`, where, len(args))

	rows, err := h.db.Query(c, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("search query failed")
	}
	defer rows.Close()

	results := make([]WorkItemResult, 0, limit)
	for rows.Next() {
		var item WorkItemResult
		if err := rows.Scan(
			&item.ID, &item.Key, &item.Title, &item.Description, &item.Status,
			&item.Priority, &item.Type, &item.Feature, &item.Assignee,
			&item.ProjectID, &item.ProjectKey, &item.ProjectName, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to read search result")
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func (h *Handler) searchFeatures(c *gin.Context, text string, projectIDs []string, limit int) ([]FeatureResult, error) {
	pattern := "%" + text + "%"
	rows, err := h.db.Query(c, `
		SELECT f.id::text, f.title, f.description, f.status,
		       p.id::text, p.key_prefix, p.name, f.updated_at::text
		FROM features f
		JOIN projects p ON p.id = f.project_id
		WHERE p.id::text = ANY($1)
		  AND (f.title ILIKE $2 OR f.description ILIKE $2 OR f.acceptance_criteria ILIKE $2)
		ORDER BY f.updated_at DESC
		LIMIT $3`, projectIDs, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]FeatureResult, 0, limit)
	for rows.Next() {
		var item FeatureResult
		if err := rows.Scan(&item.ID, &item.Title, &item.Description, &item.Status,
			&item.ProjectID, &item.ProjectKey, &item.ProjectName, &item.UpdatedAt); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func (h *Handler) searchTestCases(c *gin.Context, text string, projectIDs []string, limit int) ([]TestCaseResult, error) {
	pattern := "%" + text + "%"
	rows, err := h.db.Query(c, `
		SELECT tc.id::text, tc.title, tc.description, tc.status, tc.type,
		       COALESCE(f.title, ''), p.id::text, p.key_prefix, p.name, tc.updated_at::text
		FROM test_cases tc
		JOIN projects p ON p.id = tc.project_id
		LEFT JOIN features f ON f.id = tc.feature_id
		WHERE p.id::text = ANY($1)
		  AND (tc.title ILIKE $2 OR tc.description ILIKE $2 OR tc.preconditions ILIKE $2)
		ORDER BY tc.updated_at DESC
		LIMIT $3`, projectIDs, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]TestCaseResult, 0, limit)
	for rows.Next() {
		var item TestCaseResult
		if err := rows.Scan(&item.ID, &item.Title, &item.Description, &item.Status, &item.Type,
			&item.Feature, &item.ProjectID, &item.ProjectKey, &item.ProjectName, &item.UpdatedAt); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func (h *Handler) searchProjects(c *gin.Context, text string, projectIDs []string, limit int) ([]ProjectResult, error) {
	pattern := "%" + text + "%"
	rows, err := h.db.Query(c, `
		SELECT id::text, name, key_prefix, status
		FROM projects
		WHERE id::text = ANY($1)
		  AND (name ILIKE $2 OR key_prefix ILIKE $2 OR description ILIKE $2)
		ORDER BY updated_at DESC
		LIMIT $3`, projectIDs, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]ProjectResult, 0, limit)
	for rows.Next() {
		var item ProjectResult
		if err := rows.Scan(&item.ID, &item.Name, &item.KeyPrefix, &item.Status); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func buildTerm(term Term, args *[]interface{}, userID string) (string, error) {
	if term.FreeText {
		pattern := "%" + term.Value + "%"
		index := addArg(args, pattern)
		condition := fmt.Sprintf(`((p.key_prefix || '-' || wi.item_number) ILIKE $%d OR wi.title ILIKE $%d OR wi.description ILIKE $%d
			OR COALESCE(f.title, '') ILIKE $%d OR COALESCE(s.name, '') ILIKE $%d)`,
			index, index, index, index, index)
		return maybeNegate(term.Negated, condition), nil
	}

	field := term.Field
	value := term.Value
	operator := strings.ToUpper(term.Operator)
	contains := operator == "~" || operator == ":"
	if operator == "" {
		operator = "="
	}

	var condition string
	switch field {
	case "project":
		pattern := "%" + value + "%"
		index := addArg(args, pattern)
		condition = fmt.Sprintf(`(p.key_prefix ILIKE $%d OR p.name ILIKE $%d)`, index, index)
	case "type":
		condition = fmt.Sprintf(`wi.type %s $%d`, equalityOperator(operator), addArg(args, value))
	case "status":
		condition = fmt.Sprintf(`wi.status %s $%d`, equalityOperator(operator), addArg(args, value))
	case "priority":
		condition = fmt.Sprintf(`wi.priority %s $%d`, equalityOperator(operator), addArg(args, value))
	case "key":
		condition = fmt.Sprintf(`(p.key_prefix || '-' || wi.item_number) %s $%d`, textOperator(operator, contains), addArg(args, textPattern(value, contains)))
	case "title":
		condition = fmt.Sprintf(`wi.title %s $%d`, textOperator(operator, contains), addArg(args, textPattern(value, contains)))
	case "text":
		pattern := textPattern(value, contains)
		index := addArg(args, pattern)
		condition = fmt.Sprintf(`(wi.title ILIKE $%d OR wi.description ILIKE $%d)`, index, index)
	case "feature":
		pattern := textPattern(value, contains)
		index := addArg(args, pattern)
		condition = fmt.Sprintf(`COALESCE(f.title, '') %s $%d`, textOperator(operator, contains), index)
	case "sprint":
		pattern := textPattern(value, contains)
		index := addArg(args, pattern)
		condition = fmt.Sprintf(`COALESCE(s.name, '') %s $%d`, textOperator(operator, contains), index)
	case "release":
		pattern := textPattern(value, contains)
		index := addArg(args, pattern)
		condition = fmt.Sprintf(`COALESCE(r.name, '') %s $%d`, textOperator(operator, contains), index)
	case "assignee":
		if strings.EqualFold(value, "me") {
			condition = fmt.Sprintf(`wi.assignee_id = $%d::uuid`, addArg(args, userID))
		} else {
			index := addArg(args, value)
			pattern := "%" + value + "%"
			patternIndex := addArg(args, pattern)
			condition = fmt.Sprintf(`(u.username ILIKE $%d OR u.email ILIKE $%d OR COALESCE(u.display_name, '') ILIKE $%d OR wi.assignee_id::text = $%d)`,
				patternIndex, patternIndex, patternIndex, index)
		}
	case "reporter":
		pattern := "%" + value + "%"
		index := addArg(args, pattern)
		condition = fmt.Sprintf(`(ru.username ILIKE $%d OR ru.email ILIKE $%d OR COALESCE(ru.display_name, '') ILIKE $%d OR wi.reporter_id::text = $%d)`,
			index, index, index, index)
	case "created", "updated", "due":
		column := map[string]string{
			"created": "wi.created_at",
			"updated": "wi.updated_at",
			"due":     "wi.due_date",
		}[field]
		date, err := parseDateValue(value)
		if err != nil {
			return "", fmt.Errorf("invalid %s date %q", field, value)
		}
		cast := "::timestamptz"
		if field == "due" {
			cast = "::date"
		}
		condition = fmt.Sprintf(`%s %s $%d%s`, column, dateOperator(operator), addArg(args, date), cast)
	default:
		return "", fmt.Errorf("unsupported field %q", field)
	}
	return maybeNegate(term.Negated, condition), nil
}

func maybeNegate(negated bool, condition string) string {
	if negated {
		return "NOT (" + condition + ")"
	}
	return condition
}

func addArg(args *[]interface{}, value interface{}) int {
	*args = append(*args, value)
	return len(*args)
}

func equalityOperator(operator string) string {
	switch operator {
	case "!=", "<>":
		return "<>"
	default:
		return "="
	}
}

func textOperator(operator string, contains bool) string {
	if operator == "!=" || operator == "<>" {
		return "NOT ILIKE"
	}
	if operator == "~" || contains {
		return "ILIKE"
	}
	return "ILIKE"
}

func textPattern(value string, contains bool) string {
	if contains || strings.Contains(value, "%") {
		return "%" + value + "%"
	}
	return value
}

func dateOperator(operator string) string {
	switch operator {
	case "!=", "<>":
		return "<>"
	case ">":
		return ">"
	case ">=":
		return ">="
	case "<":
		return "<"
	case "<=":
		return "<="
	default:
		return "="
	}
}

func parseDateValue(value string) (time.Time, error) {
	now := time.Now().UTC()
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "today" {
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	if strings.HasPrefix(trimmed, "-") && len(trimmed) > 2 {
		unit := trimmed[len(trimmed)-1]
		amount, err := strconv.Atoi(trimmed[1 : len(trimmed)-1])
		if err == nil {
			switch unit {
			case 'd':
				return now.AddDate(0, 0, -amount), nil
			case 'w':
				return now.AddDate(0, 0, -amount*7), nil
			case 'm':
				return now.AddDate(0, -amount, 0), nil
			case 'y':
				return now.AddDate(-amount, 0, 0), nil
			}
		}
	}
	for _, layout := range []string{"2006-01-02", "2006/01/02", time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date")
}
