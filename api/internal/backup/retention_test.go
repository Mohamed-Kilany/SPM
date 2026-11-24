package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeBundleFixture(t *testing.T, directory, name string, modified time.Time) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("bundle"), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatalf("timestamp %s: %v", name, err)
	}
	return path
}

func TestListBundlesFiltersAndSorts(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	old := writeBundleFixture(t, directory, BundlePrefix+"old.tar", now.Add(-72*time.Hour))
	middle := writeBundleFixture(t, directory, BundlePrefix+"middle.tar.enc", now.Add(-24*time.Hour))
	newest := writeBundleFixture(t, directory, BundlePrefix+"new.tar.enc", now.Add(-time.Hour))
	writeBundleFixture(t, directory, "notes.txt", now)
	writeBundleFixture(t, directory, BundlePrefix+"new.tar.enc.sha256", now)
	if err := os.Mkdir(filepath.Join(directory, BundlePrefix+"staging"), 0o700); err != nil {
		t.Fatalf("create staging directory: %v", err)
	}

	bundles, err := ListBundles(directory)
	if err != nil {
		t.Fatalf("ListBundles: %v", err)
	}
	if len(bundles) != 3 {
		t.Fatalf("bundles = %d, want 3: %+v", len(bundles), bundles)
	}
	if bundles[0].Path != newest || bundles[1].Path != middle || bundles[2].Path != old {
		t.Fatalf("sort order = %v, %v, %v", bundles[0].Path, bundles[1].Path, bundles[2].Path)
	}
}

func TestSelectExpiredKeepsNewestAndYoung(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	bundles := []BundleFile{
		{Path: "newest", ModTime: now.Add(-time.Hour)},
		{Path: "young", ModTime: now.Add(-2 * time.Hour)},
		{Path: "old-kept-by-age", ModTime: now.Add(-24 * time.Hour)},
		{Path: "old-expired", ModTime: now.Add(-48 * time.Hour)},
		{Path: "older-expired", ModTime: now.Add(-96 * time.Hour)},
	}
	expired := SelectExpired(bundles, 2, 3*24*time.Hour, now)
	if len(expired) != 1 || expired[0].Path != "older-expired" {
		t.Fatalf("expired = %+v, want only older-expired", expired)
	}

	expired = SelectExpired(bundles, 2, 0, now)
	if len(expired) != 3 {
		t.Fatalf("zero keep-days expired %d, want 3", len(expired))
	}
	if expired[0].Path != "older-expired" || expired[2].Path != "old-kept-by-age" {
		t.Fatalf("expired order = %+v", expired)
	}

	if got := SelectExpired(bundles, len(bundles), 0, now); len(got) != 0 {
		t.Fatalf("keeping everything still expired %d", len(got))
	}
	if got := SelectExpired(nil, 7, 30*24*time.Hour, now); len(got) != 0 {
		t.Fatalf("empty input expired %d", len(got))
	}
}

func TestPruneRemovesBundleAndSidecar(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	old := writeBundleFixture(t, directory, BundlePrefix+"old.tar.enc", now.Add(-72*time.Hour))
	sidecar := old + ".sha256"
	if err := os.WriteFile(sidecar, []byte("digest"), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	current := writeBundleFixture(t, directory, BundlePrefix+"current.tar.enc", now)

	removed, err := Prune([]BundleFile{{Path: old, Name: filepath.Base(old), ModTime: now.Add(-72 * time.Hour)}})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %d, want 1", len(removed))
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("bundle still exists: %v", err)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatalf("sidecar still exists: %v", err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("current bundle was removed: %v", err)
	}
}
