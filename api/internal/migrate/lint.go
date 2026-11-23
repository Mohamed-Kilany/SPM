package migrate

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
)

// LintIssue describes one unsafe migration statement.
type LintIssue struct {
	File    string
	Line    int
	Rule    string
	Message string
}

func (i LintIssue) String() string {
	return fmt.Sprintf("%s:%d: [%s] %s", i.File, i.Line, i.Rule, i.Message)
}

type lintRule struct {
	name    string
	pattern *regexp.Regexp
	message string
	skip    func(match []string) bool
}

var destructiveRules = []lintRule{
	{
		name:    "destructive-drop-table",
		pattern: regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`),
		message: "DROP TABLE is a contract step; ship it in a separate release after the code no longer reads the table",
	},
	{
		name:    "destructive-drop-column",
		pattern: regexp.MustCompile(`(?i)\bDROP\s+COLUMN\b`),
		message: "DROP COLUMN is a contract step; ship it in a separate release after the code no longer reads the column",
	},
	{
		name:    "destructive-truncate",
		pattern: regexp.MustCompile(`(?i)\bTRUNCATE\s+(?:TABLE\s+)?([a-zA-Z_][\w."]*)`),
		message: "TRUNCATE is a contract step and must not run in an expand migration",
		skip: func(match []string) bool {
			// BEFORE TRUNCATE ON table is a trigger event, not a statement.
			return len(match) > 1 && strings.EqualFold(match[1], "ON")
		},
	},
	{
		name:    "destructive-delete",
		pattern: regexp.MustCompile(`(?i)\bDELETE\s+FROM\b`),
		message: "DELETE FROM is a data change; use a bounded, resumable backfill instead of a migration",
	},
	{
		name:    "destructive-type-change",
		pattern: regexp.MustCompile(`(?is)\bALTER\s+COLUMN\b[^;]*\bTYPE\b`),
		message: "ALTER COLUMN TYPE can rewrite the table; expand with a new column and backfill in a later release",
	},
	{
		name:    "destructive-rename",
		pattern: regexp.MustCompile(`(?i)\bRENAME\s+(?:TO|COLUMN)\b`),
		message: "RENAME is a breaking change; add the new name, dual-write, then contract in a later release",
	},
}

var concurrentIndexRule = lintRule{
	name:    "concurrent-index-transaction",
	pattern: regexp.MustCompile(`(?i)\bCREATE\s+(?:UNIQUE\s+)?INDEX\s+CONCURRENTLY\b`),
	message: "CREATE INDEX CONCURRENTLY requires the -- migrate:no-transaction directive",
}

// LintEmbedded checks the migrations embedded in the API binary.
func LintEmbedded() ([]LintIssue, error) {
	return LintFS(migrationFS)
}

// LintFS checks forward migrations under sql/. Rollback companions are
// intentionally destructive and are not linted.
func LintFS(fsys fs.FS) ([]LintIssue, error) {
	entries, err := fs.ReadDir(fsys, "sql")
	if err != nil {
		return nil, fmt.Errorf("migrate: read sql directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	var issues []LintIssue
	for _, name := range names {
		body, err := fs.ReadFile(fsys, "sql/"+name)
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", name, err)
		}
		issues = append(issues, lintMigration(name, string(body))...)
	}
	return issues, nil
}

func lintMigration(name, body string) []LintIssue {
	sanitized := stripSQLNoise(body)
	var issues []LintIssue
	for _, rule := range destructiveRules {
		for _, match := range rule.pattern.FindAllStringSubmatchIndex(sanitized, -1) {
			if len(match) < 2 {
				continue
			}
			if rule.skip != nil {
				groups := make([]string, 0, len(match)/2)
				for i := 0; i < len(match); i += 2 {
					if match[i] < 0 {
						groups = append(groups, "")
						continue
					}
					groups = append(groups, sanitized[match[i]:match[i+1]])
				}
				if rule.skip(groups) {
					continue
				}
			}
			issues = append(issues, LintIssue{
				File:    name,
				Line:    lineNumber(sanitized, match[0]),
				Rule:    rule.name,
				Message: rule.message,
			})
		}
	}
	if !hasNoTransactionDirective(body) {
		for _, match := range concurrentIndexRule.pattern.FindAllStringIndex(sanitized, -1) {
			issues = append(issues, LintIssue{
				File:    name,
				Line:    lineNumber(sanitized, match[0]),
				Rule:    concurrentIndexRule.name,
				Message: concurrentIndexRule.message,
			})
		}
	}
	return issues
}

func lineNumber(body string, offset int) int {
	if offset < 0 {
		return 1
	}
	if offset > len(body) {
		offset = len(body)
	}
	return strings.Count(body[:offset], "\n") + 1
}

// stripSQLNoise blanks comments, quoted strings and dollar-quoted function
// bodies while preserving newlines, so line numbers and statement detection
// stay accurate.
func stripSQLNoise(body string) string {
	out := []byte(body)
	blank := func(start, end int) {
		for i := start; i < end && i < len(out); i++ {
			if out[i] != '\n' && out[i] != '\r' {
				out[i] = ' '
			}
		}
	}
	for i := 0; i < len(body); {
		switch {
		case strings.HasPrefix(body[i:], "--"):
			end := strings.IndexByte(body[i:], '\n')
			if end < 0 {
				blank(i, len(body))
				i = len(body)
			} else {
				blank(i, i+end)
				i += end
			}
		case strings.HasPrefix(body[i:], "/*"):
			end := strings.Index(body[i+2:], "*/")
			if end < 0 {
				blank(i, len(body))
				i = len(body)
			} else {
				blank(i, i+2+end+2)
				i += 2 + end + 2
			}
		case body[i] == '\'':
			end := quotedEnd(body, i, '\'')
			blank(i, end)
			i = end
		case body[i] == '"':
			end := quotedEnd(body, i, '"')
			blank(i, end)
			i = end
		case body[i] == '$':
			tagEnd := dollarTagEnd(body, i)
			if tagEnd > i {
				tag := body[i:tagEnd]
				closeAt := strings.Index(body[tagEnd:], tag)
				if closeAt < 0 {
					blank(i, len(body))
					i = len(body)
				} else {
					end := tagEnd + closeAt + len(tag)
					blank(i, end)
					i = end
				}
			} else {
				i++
			}
		default:
			i++
		}
	}
	return string(out)
}

func quotedEnd(body string, start int, quote byte) int {
	for i := start + 1; i < len(body); i++ {
		if body[i] == quote {
			if i+1 < len(body) && body[i+1] == quote {
				i++
				continue
			}
			return i + 1
		}
	}
	return len(body)
}

func dollarTagEnd(body string, start int) int {
	i := start + 1
	for i < len(body) {
		c := body[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			i++
			continue
		}
		if c == '$' {
			return i + 1
		}
		return -1
	}
	return -1
}
