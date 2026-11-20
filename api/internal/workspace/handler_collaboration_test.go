package workspace

import (
	"strings"
	"testing"
)

func TestSanitizeCommentBody(t *testing.T) {
	body, err := sanitizeCommentBody("  hello\r\nworld\r\x00\t!  ")
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	if body != "hello\nworld\n\t!" {
		t.Fatalf("body = %q", body)
	}
	if _, err := sanitizeCommentBody(" \x00\t "); err == nil {
		t.Fatal("expected an empty-body error")
	}
	if _, err := sanitizeCommentBody(strings.Repeat("a", maxCommentRunes+1)); err == nil {
		t.Fatal("expected a length error")
	}
	// Markup stays literal plain text; clients render it escaped.
	body, err = sanitizeCommentBody("<script>alert(1)</script> @reviewer")
	if err != nil {
		t.Fatalf("sanitize markup: %v", err)
	}
	if body != "<script>alert(1)</script> @reviewer" {
		t.Fatalf("body = %q", body)
	}
}

func TestExtractMentionUsernames(t *testing.T) {
	names := extractMentionUsernames("@Alice please ask @bob and @Alice again, mail a@b.c")
	if len(names) != 2 || names[0] != "alice" || names[1] != "bob" {
		t.Fatalf("names = %#v", names)
	}
	if names := extractMentionUsernames("ask @bob.dev. and @carol-dev,"); len(names) != 2 || names[0] != "bob.dev" || names[1] != "carol-dev" {
		t.Fatalf("trailing punctuation: %#v", names)
	}
	if names := extractMentionUsernames("no mentions here"); len(names) != 0 {
		t.Fatalf("unexpected mentions: %#v", names)
	}
}

func TestNormalizeLabels(t *testing.T) {
	labels, err := normalizeLabels([]string{" Backend ", "backend", "needs QA", "v2"})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(labels) != 3 || labels[0] != "backend" || labels[1] != "needs qa" || labels[2] != "v2" {
		t.Fatalf("labels = %#v", labels)
	}
	if _, err := normalizeLabels([]string{"bad<script>"}); err == nil {
		t.Fatal("expected invalid character error")
	}
	if _, err := normalizeLabels([]string{strings.Repeat("x", maxLabelRunes+1)}); err == nil {
		t.Fatal("expected label length error")
	}
	tooMany := make([]string, maxLabels+1)
	for i := range tooMany {
		tooMany[i] = "label"
	}
	if _, err := normalizeLabels(tooMany); err == nil {
		t.Fatal("expected label count error")
	}
}

func TestLinkArtifactAllowlist(t *testing.T) {
	for _, artifactType := range []string{"prd", "epic", "feature", "work_item", "test_case", "sprint", "release"} {
		if _, ok := linkArtifactTables[artifactType]; !ok {
			t.Fatalf("%s is not linkable", artifactType)
		}
	}
	if _, ok := linkArtifactTables["users"]; ok {
		t.Fatal("users must not be linkable")
	}
	if normalizeArtifactType(" WORK_ITEM ") != "work_item" {
		t.Fatal("artifact type normalization failed")
	}
}
