package search

import "testing"

func TestParseFreeText(t *testing.T) {
	query := Parse("login timeout")
	if len(query.Groups) != 1 {
		t.Fatalf("expected one group, got %d", len(query.Groups))
	}
	if len(query.Groups[0].Terms) != 2 {
		t.Fatalf("expected two terms, got %d", len(query.Groups[0].Terms))
	}
	for _, term := range query.Groups[0].Terms {
		if !term.FreeText {
			t.Fatalf("expected free text term, got %+v", term)
		}
	}
}

func TestParseFieldsAndNegation(t *testing.T) {
	query := Parse(`status:done -type:bug "acceptance criteria"`)
	if len(query.Groups) != 1 {
		t.Fatalf("expected one group, got %d", len(query.Groups))
	}
	terms := query.Groups[0].Terms
	if len(terms) != 3 {
		t.Fatalf("expected three terms, got %d", len(terms))
	}
	if terms[0].Field != "status" || terms[0].Value != "done" {
		t.Fatalf("unexpected status term: %+v", terms[0])
	}
	if !terms[1].Negated || terms[1].Field != "type" {
		t.Fatalf("expected negated type term: %+v", terms[1])
	}
	if !terms[2].FreeText || terms[2].Value != "acceptance criteria" {
		t.Fatalf("expected quoted free text: %+v", terms[2])
	}
}

func TestParseOrGroups(t *testing.T) {
	query := Parse(`priority:critical OR status:blocked assignee:me`)
	if len(query.Groups) != 2 {
		t.Fatalf("expected two groups, got %d", len(query.Groups))
	}
	if len(query.Groups[0].Terms) != 1 || len(query.Groups[1].Terms) != 2 {
		t.Fatalf("unexpected group sizes: %+v", query.Groups)
	}
}

func TestParseQuotedFieldValue(t *testing.T) {
	query := Parse(`feature:"Checkout redesign"`)
	if len(query.Groups) != 1 || len(query.Groups[0].Terms) != 1 {
		t.Fatalf("unexpected query: %+v", query)
	}
	term := query.Groups[0].Terms[0]
	if term.Field != "feature" || term.Value != "Checkout redesign" {
		t.Fatalf("unexpected term: %+v", term)
	}
}
