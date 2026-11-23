package migrate

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrationsPassLint(t *testing.T) {
	issues, err := LintEmbedded()
	if err != nil {
		t.Fatalf("LintEmbedded: %v", err)
	}
	if len(issues) != 0 {
		for _, issue := range issues {
			t.Error(issue.String())
		}
		t.Fatalf("%d embedded migrations violate the expand-only rules", len(issues))
	}
}

func TestLintFlagsDestructiveStatements(t *testing.T) {
	body := strings.Join([]string{
		"CREATE TABLE example (id int);",
		"DROP TABLE example;",
		"ALTER TABLE example DROP COLUMN id;",
		"TRUNCATE TABLE example;",
		"DELETE FROM example;",
		"ALTER TABLE example ALTER COLUMN id TYPE bigint;",
		"ALTER TABLE example RENAME COLUMN id TO identifier;",
	}, "\n")

	issues := lintMigration("001_bad.sql", body)
	want := []string{
		"destructive-drop-table",
		"destructive-drop-column",
		"destructive-truncate",
		"destructive-delete",
		"destructive-type-change",
		"destructive-rename",
	}
	if len(issues) != len(want) {
		for _, issue := range issues {
			t.Log(issue.String())
		}
		t.Fatalf("lint found %d issues, want %d", len(issues), len(want))
	}
	for i, rule := range want {
		if issues[i].Rule != rule {
			t.Errorf("issue %d rule = %q, want %q", i, issues[i].Rule, rule)
		}
		if issues[i].Line == 0 {
			t.Errorf("issue %d has no line number", i)
		}
	}
}

func TestLintAllowsSafePatterns(t *testing.T) {
	body := strings.Join([]string{
		"ALTER TABLE audit_events DROP CONSTRAINT IF EXISTS audit_events_pkey;",
		"CREATE TRIGGER audit_events_no_truncate BEFORE TRUNCATE ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_audit_mutation();",
		"CREATE INDEX IF NOT EXISTS example_idx ON example (id);",
		"DROP INDEX IF EXISTS example_idx;",
		"INSERT INTO example (id) VALUES (1) ON CONFLICT DO NOTHING;",
	}, "\n")

	if issues := lintMigration("002_safe.sql", body); len(issues) != 0 {
		for _, issue := range issues {
			t.Error(issue.String())
		}
	}
}

func TestLintIgnoresCommentsStringsAndFunctionBodies(t *testing.T) {
	body := strings.Join([]string{
		"-- DROP TABLE commented_out;",
		"/* TRUNCATE TABLE also_commented; */",
		"INSERT INTO notes (body) VALUES ('DELETE FROM notes');",
		`INSERT INTO notes (body) VALUES ("DROP TABLE quoted");`,
		"CREATE FUNCTION cleanup() RETURNS void AS $$",
		"BEGIN",
		"  EXECUTE format('DROP TABLE public.%I', p_name);",
		"END;",
		"$$ LANGUAGE plpgsql;",
	}, "\n")

	if issues := lintMigration("003_noise.sql", body); len(issues) != 0 {
		for _, issue := range issues {
			t.Error(issue.String())
		}
	}
}

func TestLintRequiresNoTransactionForConcurrentIndex(t *testing.T) {
	body := "CREATE INDEX CONCURRENTLY example_idx ON example (id);"
	issues := lintMigration("004_concurrent.sql", body)
	if len(issues) != 1 || issues[0].Rule != concurrentIndexRule.name {
		t.Fatalf("issues = %v, want one %s issue", issues, concurrentIndexRule.name)
	}

	withDirective := "-- migrate:no-transaction\n" + body
	if issues := lintMigration("005_concurrent.sql", withDirective); len(issues) != 0 {
		t.Errorf("directive did not suppress the concurrent-index issue: %v", issues)
	}
}

func TestLintFSReportsParseAndReadErrors(t *testing.T) {
	if _, err := LintFS(fstest.MapFS{}); err == nil {
		t.Error("LintFS accepted a filesystem without sql/")
	}
}

func TestLintFSSortsFilesAndSkipsDirectories(t *testing.T) {
	fsys := fstest.MapFS{
		"sql/002_bad.sql":  {Data: []byte("DROP TABLE b;")},
		"sql/001_bad.sql":  {Data: []byte("TRUNCATE a;")},
		"sql/notes.txt":    {Data: []byte("DROP TABLE ignored;")},
		"sql/nested/x.sql": {Data: []byte("DROP TABLE ignored;")},
	}
	issues, err := LintFS(fsys)
	if err != nil {
		t.Fatalf("LintFS: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("lint found %d issues, want 2", len(issues))
	}
	if issues[0].File != "001_bad.sql" || issues[1].File != "002_bad.sql" {
		t.Errorf("issue order = %s, %s", issues[0].File, issues[1].File)
	}
}

func TestStripSQLNoisePreservesLineNumbers(t *testing.T) {
	body := "SELECT 1;\n-- DROP TABLE x;\nSELECT 'DROP TABLE y';\nSELECT 2;"
	cleaned := stripSQLNoise(body)
	if strings.Count(cleaned, "\n") != strings.Count(body, "\n") {
		t.Fatalf("stripSQLNoise changed the line count:\n%s", cleaned)
	}
	if strings.Contains(cleaned, "DROP TABLE") {
		t.Fatalf("stripSQLNoise left SQL noise behind:\n%s", cleaned)
	}
	if lineNumber(body, strings.Index(body, "SELECT 2")) != 4 {
		t.Errorf("lineNumber = %d, want 4", lineNumber(body, strings.Index(body, "SELECT 2")))
	}
}
