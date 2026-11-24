package backup

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatalf("create directory for %s: %v", name, err)
	}
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestBuildManifestClassifiesAndHashesFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "databases", "spm.dump"), []byte("database"))
	writeTestFile(t, filepath.Join(root, "volumes", "gitea.tar"), []byte("volume"))
	writeTestFile(t, filepath.Join(root, "config", "docker-compose.yaml"), []byte("services:"))
	writeTestFile(t, filepath.Join(root, "metadata", "git.txt"), []byte("abc123"))
	writeTestFile(t, filepath.Join(root, "manifest.json"), []byte("stale manifest is ignored"))

	created := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	manifest, err := BuildManifest(root, BuildOptions{
		CreatedAt: created,
		Host:      "backup-host",
		Commit:    "abc123",
		Databases: []string{"spm", "git"},
		Volumes:   []string{"gitea_data"},
	})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if manifest.SchemaVersion != SchemaVersion {
		t.Errorf("schema = %d, want %d", manifest.SchemaVersion, SchemaVersion)
	}
	if !manifest.CreatedAt.Equal(created) {
		t.Errorf("created = %s, want %s", manifest.CreatedAt, created)
	}
	if manifest.Host != "backup-host" || manifest.Commit != "abc123" {
		t.Errorf("provenance = %q/%q", manifest.Host, manifest.Commit)
	}
	if len(manifest.Entries) != 4 {
		t.Fatalf("entries = %d, want 4 (manifest.json must be excluded)", len(manifest.Entries))
	}
	wantKinds := map[string]string{
		"databases/spm.dump":         "database",
		"volumes/gitea.tar":          "volume",
		"config/docker-compose.yaml": "config",
		"metadata/git.txt":           "metadata",
	}
	for _, entry := range manifest.Entries {
		if entry.Kind != wantKinds[entry.Path] {
			t.Errorf("kind for %s = %q, want %q", entry.Path, entry.Kind, wantKinds[entry.Path])
		}
		if entry.Size <= 0 || len(entry.SHA256) != sha256.Size*2 {
			t.Errorf("entry %s has size=%d sha=%q", entry.Path, entry.Size, entry.SHA256)
		}
	}
	if manifest.TotalBytes != int64(len("database")+len("volume")+len("services:")+len("abc123")) {
		t.Errorf("total bytes = %d", manifest.TotalBytes)
	}
	for index := 1; index < len(manifest.Entries); index++ {
		if manifest.Entries[index-1].Path >= manifest.Entries[index].Path {
			t.Fatalf("entries are not sorted: %v", manifest.Entries)
		}
	}
	for _, entry := range manifest.Entries {
		if entry.Path == "metadata/git.txt" && entry.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte("abc123"))) {
			t.Error("metadata digest does not match its content")
		}
	}
}

func TestBuildManifestDerivesDatabaseAndVolumeLists(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "databases", "spm.dump"), []byte("database"))
	writeTestFile(t, filepath.Join(root, "databases", "git.dump"), []byte("gitea"))
	writeTestFile(t, filepath.Join(root, "volumes", "nats_data.tar"), []byte("volume"))
	writeTestFile(t, filepath.Join(root, "config", "docker-compose.yaml"), []byte("services:"))

	manifest, err := BuildManifest(root, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if got, want := strings.Join(manifest.Databases, ","), "git,spm"; got != want {
		t.Errorf("databases = %q, want %q", got, want)
	}
	if got, want := strings.Join(manifest.Volumes, ","), "nats_data"; got != want {
		t.Errorf("volumes = %q, want %q", got, want)
	}
}

func TestBuildManifestRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "target.txt"), []byte("data"))
	if err := os.Symlink(filepath.Join(root, "target.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if _, err := BuildManifest(root, BuildOptions{}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("BuildManifest error = %v, want symlink rejection", err)
	}
}

func TestManifestValidate(t *testing.T) {
	digest := strings.Repeat("a", 64)
	valid := func() *Manifest {
		return &Manifest{
			SchemaVersion: SchemaVersion,
			CreatedAt:     time.Now().UTC(),
			Entries: []Entry{{
				Path:   "databases/spm.dump",
				Kind:   "database",
				Size:   3,
				SHA256: digest,
			}},
			TotalBytes: 3,
		}
	}

	tests := []struct {
		name   string
		mutate func(*Manifest)
		want   string
	}{
		{"wrong schema", func(m *Manifest) { m.SchemaVersion = 99 }, "schema"},
		{"no time", func(m *Manifest) { m.CreatedAt = time.Time{} }, "creation time"},
		{"reserved path", func(m *Manifest) { m.Entries[0].Path = ManifestName }, "reserved"},
		{"unsafe path", func(m *Manifest) { m.Entries[0].Path = "../escape" }, "unsafe"},
		{"duplicate", func(m *Manifest) { m.Entries = append(m.Entries, m.Entries[0]) }, "duplicate"},
		{"negative size", func(m *Manifest) { m.Entries[0].Size = -1 }, "negative"},
		{"short digest", func(m *Manifest) { m.Entries[0].SHA256 = "abc" }, "malformed"},
		{"non-hex digest", func(m *Manifest) { m.Entries[0].SHA256 = strings.Repeat("z", 64) }, "malformed"},
		{"total mismatch", func(m *Manifest) { m.TotalBytes = 9 }, "total"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := valid()
			test.mutate(manifest)
			err := manifest.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %q", err, test.want)
			}
		})
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestWriteAndReadManifest(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "metadata", "git.txt"), []byte("abc"))
	manifest, err := BuildManifest(root, BuildOptions{Host: "h"})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if err := WriteManifest(root, manifest); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	loaded, err := ReadManifest(root)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if loaded.Host != manifest.Host || len(loaded.Entries) != 1 || loaded.TotalBytes != manifest.TotalBytes {
		t.Fatalf("loaded manifest = %+v, want %+v", loaded, manifest)
	}
}

func TestReadManifestRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ManifestName), []byte(`{"schema_version":1,"created_at":"2026-11-23T00:00:00Z","host":"h","unexpected":true}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := ReadManifest(root); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ReadManifest error = %v, want unknown field", err)
	}
}

func TestSafeRelativePath(t *testing.T) {
	accepted := []string{"manifest.json", "databases/spm.dump", "config/envoy/envoy.yaml"}
	for _, name := range accepted {
		if _, err := SafeRelativePath(name); err != nil {
			t.Errorf("SafeRelativePath(%q) error = %v", name, err)
		}
	}
	rejected := []string{"", ".", "/absolute", "../escape", "a/../../b", "a\\..\\b", "./a"}
	for _, name := range rejected {
		if _, err := SafeRelativePath(name); err == nil {
			t.Errorf("SafeRelativePath(%q) accepted an unsafe path", name)
		}
	}
}
