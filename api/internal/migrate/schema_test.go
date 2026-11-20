package migrate

import (
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var revisionedPlanningTables = []string{
	"prds",
	"epics",
	"features",
	"work_items",
	"test_cases",
	"test_runs",
	"sprints",
	"releases",
}

func migrationNames(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(migrationFS, "sql")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	body, err := migrationFS.ReadFile("sql/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

func TestEmbeddedMigrationsAreUniqueAndOrdered(t *testing.T) {
	names := migrationNames(t)
	if len(names) == 0 {
		t.Fatal("no embedded migrations found")
	}
	if names[len(names)-1] != "017_work_item_collaboration.sql" {
		t.Fatalf("latest migration = %q, want 017_work_item_collaboration.sql", names[len(names)-1])
	}

	seenVersions := map[string]string{}
	for _, name := range names {
		version := strings.SplitN(name, "_", 2)[0]
		if len(version) != 3 {
			t.Errorf("migration %q does not start with a three digit version", name)
			continue
		}
		if previous, ok := seenVersions[version]; ok {
			t.Errorf("migrations %q and %q share version %s", previous, name, version)
		}
		seenVersions[version] = name
	}
}

func TestPlanningRevisionMigrationCoversEveryPlanningTable(t *testing.T) {
	body := readMigration(t, "016_planning_revisions.sql")

	column := regexp.MustCompile(`(?is)ALTER\s+TABLE\s+(\w+)\s+ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\s+revision\s+BIGINT\s+NOT\s+NULL\s+DEFAULT\s+1\s+CHECK\s*\(\s*revision\s*>\s*0\s*\)`)
	columnTables := map[string]bool{}
	for _, match := range column.FindAllStringSubmatch(body, -1) {
		columnTables[match[1]] = true
	}

	trigger := regexp.MustCompile(`(?is)CREATE\s+TRIGGER\s+planning_revision\s+BEFORE\s+UPDATE\s+ON\s+(\w+)\s+FOR\s+EACH\s+ROW\s+EXECUTE\s+FUNCTION\s+planning_bump_revision\s*\(\s*\)`)
	triggerTables := map[string]bool{}
	for _, match := range trigger.FindAllStringSubmatch(body, -1) {
		triggerTables[match[1]] = true
	}

	for _, table := range revisionedPlanningTables {
		if !columnTables[table] {
			t.Errorf("016 does not add a guarded revision column to %s", table)
		}
		if !triggerTables[table] {
			t.Errorf("016 does not install the revision trigger on %s", table)
		}
	}
	if len(columnTables) != len(revisionedPlanningTables) {
		t.Errorf("016 touches %d revision columns, want %d", len(columnTables), len(revisionedPlanningTables))
	}
	if len(triggerTables) != len(revisionedPlanningTables) {
		t.Errorf("016 installs %d revision triggers, want %d", len(triggerTables), len(revisionedPlanningTables))
	}

	for _, fragment := range []string{
		"NEW.revision := OLD.revision + 1;",
		"REVOKE ALL ON FUNCTION planning_bump_revision() FROM PUBLIC;",
		"work_items_no_self_parent",
		"parent_id IS NULL OR parent_id <> id",
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("016 is missing %q", fragment)
		}
	}
}

func TestPlanningRevisionRollbackIsComplete(t *testing.T) {
	forward := readMigration(t, "016_planning_revisions.sql")
	rollback, err := os.ReadFile("rollback/016_planning_revisions.sql")
	if err != nil {
		t.Fatalf("read rollback companion: %v", err)
	}
	body := string(rollback)

	for _, table := range revisionedPlanningTables {
		if !strings.Contains(body, "DROP TRIGGER IF EXISTS planning_revision ON "+table+";") {
			t.Errorf("rollback does not drop the trigger on %s", table)
		}
		if !strings.Contains(body, "ALTER TABLE "+table+" DROP COLUMN IF EXISTS revision;") {
			t.Errorf("rollback does not drop the revision column on %s", table)
		}
	}
	if !strings.Contains(body, "DROP FUNCTION IF EXISTS planning_bump_revision();") {
		t.Error("rollback does not drop the revision function")
	}
	if !strings.Contains(body, "work_items_no_self_parent") {
		t.Error("rollback does not drop the self-parent constraint")
	}

	index := regexp.MustCompile(`(?i)CREATE\s+INDEX\s+IF\s+NOT\s+EXISTS\s+(\w+)`)
	names := index.FindAllStringSubmatch(forward, -1)
	if len(names) == 0 {
		t.Fatal("016 creates no indexes")
	}
	for _, match := range names {
		if !strings.Contains(body, "DROP INDEX IF EXISTS "+match[1]+";") {
			t.Errorf("rollback does not drop index %s", match[1])
		}
	}

	embedded, err := fs.Glob(migrationFS, "rollback/*")
	if err != nil {
		t.Fatalf("glob embedded rollback: %v", err)
	}
	if len(embedded) != 0 {
		t.Errorf("rollback scripts must not be embedded: %v", embedded)
	}
}

func TestWorkItemCollaborationRollbackIsComplete(t *testing.T) {
	forward := readMigration(t, "017_work_item_collaboration.sql")
	rollback, err := os.ReadFile("rollback/017_work_item_collaboration.sql")
	if err != nil {
		t.Fatalf("read collaboration rollback companion: %v", err)
	}
	body := string(rollback)

	for _, table := range []string{"work_item_watchers", "comment_mentions"} {
		if !strings.Contains(forward, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("017 does not create %s", table)
		}
		if !strings.Contains(body, "DROP TABLE IF EXISTS "+table+";") {
			t.Errorf("rollback does not drop %s", table)
		}
	}
	for _, column := range []struct{ table, name string }{
		{"work_items", "labels"},
		{"comments", "edited_at"},
		{"attachments", "checksum_sha256"},
		{"attachments", "scan_status"},
		{"attachments", "scan_engine"},
		{"attachments", "scan_detail"},
		{"attachments", "deleted_at"},
	} {
		if !strings.Contains(forward, "ALTER TABLE "+column.table+" ADD COLUMN IF NOT EXISTS "+column.name) {
			t.Errorf("017 does not add %s.%s", column.table, column.name)
		}
		if !strings.Contains(body, "ALTER TABLE "+column.table+" DROP COLUMN IF EXISTS "+column.name+";") {
			t.Errorf("rollback does not drop %s.%s", column.table, column.name)
		}
	}
	index := regexp.MustCompile(`(?i)CREATE\s+INDEX\s+IF\s+NOT\s+EXISTS\s+(\w+)`)
	names := index.FindAllStringSubmatch(forward, -1)
	if len(names) == 0 {
		t.Fatal("017 creates no indexes")
	}
	for _, match := range names {
		if !strings.Contains(body, "DROP INDEX IF EXISTS "+match[1]+";") {
			t.Errorf("rollback does not drop index %s", match[1])
		}
	}
	if !strings.Contains(forward, "scan_status IN ('pending', 'clean', 'rejected')") {
		t.Error("017 does not constrain attachment scan status")
	}
}

// TestEveryForeignKeyHasALeadingIndex enforces the database invariant that
// deleting a referenced row never scans a dependent table. PostgreSQL creates
// an index for primary keys and unique constraints, but never for the
// referencing side of a foreign key.
func TestEveryForeignKeyHasALeadingIndex(t *testing.T) {
	type foreignKey struct {
		table  string
		column string
		target string
	}

	var (
		createTable = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)\s*\(`)
		closeTable  = regexp.MustCompile(`^\s*\)\s*(?:PARTITION\s+BY.*)?;`)
		inlineRef   = regexp.MustCompile(`(?i)^\s*([a-z_][a-z0-9_]*)\s+[^,]*?\bREFERENCES\s+([a-z_][a-z0-9_]*)\s*\(\s*([a-z_][a-z0-9_]*)\s*\)`)
		inlinePK    = regexp.MustCompile(`(?i)^\s*([a-z_][a-z0-9_]*)\s+[^,]*\bPRIMARY\s+KEY\b`)
		tableKey    = regexp.MustCompile(`(?i)^\s*(?:CONSTRAINT\s+[a-z_][a-z0-9_]*\s+)?(?:PRIMARY\s+KEY|UNIQUE)\s*\(([^)]*)\)`)
		index       = regexp.MustCompile(`(?is)CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:CONCURRENTLY\s+)?(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)\s+ON\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)\s*(?:USING\s+[a-z_][a-z0-9_]*\s*)?\(([^)]*)\)`)
	)

	covered := map[string]map[string]bool{}
	cover := func(table, column string) {
		if table == "" || column == "" {
			return
		}
		if covered[table] == nil {
			covered[table] = map[string]bool{}
		}
		covered[table][column] = true
	}

	var foreignKeys []foreignKey
	for _, name := range migrationNames(t) {
		body := readMigration(t, name)
		currentTable := ""
		for _, rawLine := range strings.Split(body, "\n") {
			line := rawLine
			if comment := strings.Index(line, "--"); comment >= 0 {
				line = line[:comment]
			}
			if match := createTable.FindStringSubmatch(line); match != nil {
				currentTable = strings.ToLower(match[1])
			}
			if currentTable != "" {
				if match := inlineRef.FindStringSubmatch(line); match != nil {
					foreignKeys = append(foreignKeys, foreignKey{
						table:  currentTable,
						column: strings.ToLower(match[1]),
						target: strings.ToLower(match[3]),
					})
				}
				if match := inlinePK.FindStringSubmatch(line); match != nil {
					cover(currentTable, strings.ToLower(match[1]))
				}
				if match := tableKey.FindStringSubmatch(line); match != nil {
					cover(currentTable, leadingColumn(match[1]))
				}
				if closeTable.MatchString(line) {
					currentTable = ""
				}
			}
		}

		for _, match := range index.FindAllStringSubmatch(body, -1) {
			cover(strings.ToLower(match[2]), leadingColumn(match[3]))
		}
	}

	if len(foreignKeys) == 0 {
		t.Fatal("schema parser found no foreign keys")
	}
	t.Logf("checked %d foreign keys", len(foreignKeys))
	for _, fk := range foreignKeys {
		if !covered[fk.table][fk.column] {
			t.Errorf("foreign key %s.%s -> %s has no index with %s as its leading column",
				fk.table, fk.column, fk.target, fk.column)
		}
	}
}

func leadingColumn(columns string) string {
	first := strings.TrimSpace(strings.Split(columns, ",")[0])
	if strings.Contains(first, "(") {
		// Expression indexes cannot satisfy a foreign-key lookup.
		return ""
	}
	match := regexp.MustCompile(`(?i)^([a-z_][a-z0-9_]*)`).FindStringSubmatch(first)
	if match == nil {
		return ""
	}
	return strings.ToLower(match[1])
}
