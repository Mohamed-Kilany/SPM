// Package search implements the JQL-style query language used by global search.
package search

import (
	"strings"
)

// Term is a single predicate or free-text term.
type Term struct {
	Field    string
	Operator string
	Value    string
	Negated  bool
	FreeText bool
}

// Group is a conjunction of terms. Groups are combined with OR.
type Group struct {
	Terms []Term
}

// Query is a parsed search expression.
type Query struct {
	Raw    string
	Groups []Group
}

var knownFields = map[string]bool{
	"project":  true,
	"type":     true,
	"status":   true,
	"priority": true,
	"assignee": true,
	"reporter": true,
	"feature":  true,
	"sprint":   true,
	"release":  true,
	"key":      true,
	"title":    true,
	"text":     true,
	"created":  true,
	"updated":  true,
	"due":      true,
}

// Parse converts a query string into OR groups of AND terms. The grammar is
// intentionally JQL-like but bounded: implicit AND, explicit AND/OR, quoted
// values, negation with - or NOT, and field:value / field=value comparisons.
func Parse(raw string) Query {
	query := Query{Raw: raw, Groups: []Group{{}}}
	tokens := tokenize(raw)
	negateNext := false
	for _, token := range tokens {
		upper := strings.ToUpper(token)
		switch upper {
		case "OR":
			if len(query.Groups[len(query.Groups)-1].Terms) > 0 {
				query.Groups = append(query.Groups, Group{})
			}
			continue
		case "AND":
			continue
		case "NOT":
			negateNext = true
			continue
		}

		negated := negateNext
		negateNext = false
		if strings.HasPrefix(token, "-") && len(token) > 1 {
			negated = !negated
			token = token[1:]
		}
		if token == "" {
			continue
		}

		term := parseTerm(token)
		term.Negated = negated
		query.Groups[len(query.Groups)-1].Terms = append(
			query.Groups[len(query.Groups)-1].Terms, term,
		)
	}

	groups := query.Groups[:0]
	for _, group := range query.Groups {
		if len(group.Terms) > 0 {
			groups = append(groups, group)
		}
	}
	query.Groups = groups
	return query
}

func parseTerm(token string) Term {
	field, operator, value, ok := splitField(token)
	if !ok || !knownFields[strings.ToLower(field)] {
		return Term{FreeText: true, Value: unquote(token)}
	}
	return Term{
		Field:    strings.ToLower(field),
		Operator: operator,
		Value:    unquote(value),
	}
}

func splitField(token string) (field, operator, value string, ok bool) {
	operators := []string{"!=", ">=", "<=", "=", ":", ">", "<", "~"}
	best := -1
	bestOp := ""
	for _, op := range operators {
		if index := strings.Index(token, op); index > 0 {
			if best == -1 || index < best {
				best = index
				bestOp = op
			}
		}
	}
	if best <= 0 {
		return "", "", "", false
	}
	return token[:best], bestOp, token[best+len(bestOp):], true
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func tokenize(raw string) []string {
	var tokens []string
	var current strings.Builder
	var quote rune
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, char := range raw {
		switch {
		case quote != 0:
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
		case char == '"' || char == '\'':
			quote = char
		case char == ' ' || char == '\t' || char == '\n' || char == '\r':
			flush()
		default:
			current.WriteRune(char)
		}
	}
	flush()
	return tokens
}
