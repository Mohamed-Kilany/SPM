package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BundlePrefix is the filename prefix used by scripts/backup.ps1.
const BundlePrefix = "spm-backup-"

// BundleFile is one on-disk backup artifact considered by retention.
type BundleFile struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// ListBundles returns backup artifacts in directory, newest first. Only
// spm-backup-*.tar and spm-backup-*.tar.enc files are considered; checksum
// sidecars and staging directories are ignored.
func ListBundles(directory string) ([]BundleFile, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("backup: list %s: %w", directory, err)
	}
	bundles := make([]BundleFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, BundlePrefix) {
			continue
		}
		if !strings.HasSuffix(name, ".tar") && !strings.HasSuffix(name, ".tar.enc") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("backup: stat %s: %w", name, err)
		}
		bundles = append(bundles, BundleFile{
			Path:    filepath.Join(directory, name),
			Name:    name,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC(),
		})
	}
	sort.Slice(bundles, func(i, j int) bool {
		if bundles[i].ModTime.Equal(bundles[j].ModTime) {
			return bundles[i].Name > bundles[j].Name
		}
		return bundles[i].ModTime.After(bundles[j].ModTime)
	})
	return bundles, nil
}

// SelectExpired returns the bundles that retention may delete. The newest
// keepLast artifacts are always kept. Among the rest, anything younger than
// keepFor is also kept; a zero keepFor means delete everything beyond
// keepLast.
func SelectExpired(bundles []BundleFile, keepLast int, keepFor time.Duration, now time.Time) []BundleFile {
	if keepLast < 0 {
		keepLast = 0
	}
	if keepFor < 0 {
		keepFor = 0
	}
	now = now.UTC()
	expired := make([]BundleFile, 0, len(bundles))
	for index, bundle := range bundles {
		if index < keepLast {
			continue
		}
		if keepFor > 0 && now.Sub(bundle.ModTime) <= keepFor {
			continue
		}
		expired = append(expired, bundle)
	}
	sort.Slice(expired, func(i, j int) bool {
		return expired[i].ModTime.Before(expired[j].ModTime)
	})
	return expired
}

// Prune deletes the selected bundles and their .sha256 sidecars.
func Prune(expired []BundleFile) ([]BundleFile, error) {
	removed := make([]BundleFile, 0, len(expired))
	for _, bundle := range expired {
		if err := os.Remove(bundle.Path); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("backup: remove %s: %w", bundle.Path, err)
		}
		sidecar := bundle.Path + ".sha256"
		if err := os.Remove(sidecar); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("backup: remove %s: %w", sidecar, err)
		}
		removed = append(removed, bundle)
	}
	return removed, nil
}
