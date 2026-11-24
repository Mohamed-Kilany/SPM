// Package backup implements the SPM backup bundle format: a checksummed
// manifest, a deterministic tar packer, authenticated encryption and the
// status file used by the backup tooling and the API metrics collector.
//
// A bundle contains one manifest.json at the archive root followed by the
// database dumps, volume archives and configuration snapshots listed in the
// manifest. The manifest records a SHA-256 digest and size for every payload
// file. Verification recomputes both, so a truncated or modified bundle is
// rejected before a restore starts.
package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// ManifestName is the reserved archive root entry. It is not listed in its
	// own entries and cannot be used as a payload path.
	ManifestName = "manifest.json"

	// SchemaVersion is the bundle manifest format written by this package.
	SchemaVersion = 1

	// maxManifestBytes bounds manifest.json so a corrupt or hostile archive
	// cannot exhaust memory during inspection.
	maxManifestBytes = 4 << 20
)

// Entry describes one payload file inside a bundle.
type Entry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest is the signed-by-checksum index of a backup bundle. When the bundle
// is encrypted, the whole manifest is authenticated by AES-GCM.
type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	CreatedAt     time.Time `json:"created_at"`
	Host          string    `json:"host"`
	Commit        string    `json:"commit,omitempty"`
	ToolVersion   string    `json:"tool_version,omitempty"`
	Databases     []string  `json:"databases"`
	Volumes       []string  `json:"volumes"`
	Entries       []Entry   `json:"entries"`
	TotalBytes    int64     `json:"total_bytes"`
}

// BuildOptions carries the provenance written into a new manifest.
type BuildOptions struct {
	CreatedAt   time.Time
	Host        string
	Commit      string
	ToolVersion string
	Databases   []string
	Volumes     []string
}

// BuildManifest walks root and returns a manifest for every regular file.
// Symlinks, sockets and devices are rejected so a bundle cannot reference data
// outside the staging tree. manifest.json itself is skipped.
func BuildManifest(root string, opts BuildOptions) (*Manifest, error) {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("backup: inspect root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("backup: root %s is not a directory", root)
	}

	manifest := &Manifest{
		SchemaVersion: SchemaVersion,
		CreatedAt:     opts.CreatedAt.UTC(),
		Host:          opts.Host,
		Commit:        opts.Commit,
		ToolVersion:   opts.ToolVersion,
		Databases:     append([]string{}, opts.Databases...),
		Volumes:       append([]string{}, opts.Volumes...),
		Entries:       make([]Entry, 0, 32),
	}
	if manifest.CreatedAt.IsZero() {
		manifest.CreatedAt = time.Now().UTC()
	}

	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("backup: symlink %q is not supported", current)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("backup: %q is not a regular file", current)
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == ManifestName {
			return nil
		}
		size, digest, err := fileDigest(current)
		if err != nil {
			return err
		}
		manifest.Entries = append(manifest.Entries, Entry{
			Path:   relative,
			Kind:   KindFor(relative),
			Size:   size,
			SHA256: digest,
		})
		manifest.TotalBytes += size
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("backup: build manifest: %w", err)
	}

	sort.Slice(manifest.Entries, func(i, j int) bool {
		return manifest.Entries[i].Path < manifest.Entries[j].Path
	})
	if len(manifest.Databases) == 0 {
		manifest.Databases = deriveArchiveNames(manifest.Entries, "databases/", ".dump")
	}
	if len(manifest.Volumes) == 0 {
		manifest.Volumes = deriveArchiveNames(manifest.Entries, "volumes/", ".tar")
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return manifest, nil
}

// deriveArchiveNames lists the direct database or volume artifacts present in
// the staging tree when the caller did not supply an explicit inventory.
func deriveArchiveNames(entries []Entry, prefix, suffix string) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Path, prefix) || !strings.HasSuffix(entry.Path, suffix) {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(entry.Path, prefix), suffix)
		if name == "" || strings.Contains(name, "/") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// KindFor classifies a payload path by its top-level directory.
func KindFor(relative string) string {
	top := strings.SplitN(relative, "/", 2)[0]
	switch top {
	case "databases":
		return "database"
	case "volumes":
		return "volume"
	case "config":
		return "config"
	case "metadata":
		return "metadata"
	default:
		return "other"
	}
}

// Validate checks the structural invariants shared by writers and readers.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("backup: nil manifest")
	}
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("backup: unsupported manifest schema %d (want %d)", m.SchemaVersion, SchemaVersion)
	}
	if m.CreatedAt.IsZero() {
		return errors.New("backup: manifest has no creation time")
	}
	seen := make(map[string]struct{}, len(m.Entries))
	var total int64
	for _, entry := range m.Entries {
		if entry.Path == "" || entry.Path == ManifestName {
			return fmt.Errorf("backup: reserved or empty manifest path %q", entry.Path)
		}
		if !fs.ValidPath(entry.Path) {
			return fmt.Errorf("backup: unsafe manifest path %q", entry.Path)
		}
		if _, duplicate := seen[entry.Path]; duplicate {
			return fmt.Errorf("backup: duplicate manifest path %q", entry.Path)
		}
		seen[entry.Path] = struct{}{}
		if entry.Size < 0 {
			return fmt.Errorf("backup: negative size for %q", entry.Path)
		}
		if len(entry.SHA256) != sha256.Size*2 {
			return fmt.Errorf("backup: %q has a malformed SHA-256", entry.Path)
		}
		if _, err := hex.DecodeString(entry.SHA256); err != nil {
			return fmt.Errorf("backup: %q has a malformed SHA-256", entry.Path)
		}
		total += entry.Size
	}
	if total != m.TotalBytes {
		return fmt.Errorf("backup: manifest total %d does not match entry sizes %d", m.TotalBytes, total)
	}
	return nil
}

// WriteManifest writes manifest.json below root atomically.
func WriteManifest(root string, manifest *Manifest) error {
	return WriteManifestFile(filepath.Join(root, ManifestName), manifest)
}

// WriteManifestFile writes a manifest to an explicit path atomically.
func WriteManifestFile(target string, manifest *Manifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: encode manifest: %w", err)
	}
	data = append(data, '\n')
	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("backup: create manifest directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("backup: create manifest: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = os.Remove(tempName)
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("backup: chmod manifest: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("backup: write manifest: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("backup: sync manifest: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("backup: close manifest: %w", err)
	}
	if err := os.Rename(tempName, target); err != nil {
		return fmt.Errorf("backup: install manifest: %w", err)
	}
	return nil
}

// ReadManifest reads and validates root/manifest.json.
func ReadManifest(root string) (*Manifest, error) {
	file, err := os.Open(filepath.Join(root, ManifestName))
	if err != nil {
		return nil, fmt.Errorf("backup: open manifest: %w", err)
	}
	defer file.Close()
	manifest, err := decodeManifest(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	return manifest, nil
}

func decodeManifest(reader io.Reader) (*Manifest, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("backup: read manifest: %w", err)
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("backup: manifest exceeds %d bytes", maxManifestBytes)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("backup: decode manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func fileDigest(name string) (int64, string, error) {
	file, err := os.Open(name)
	if err != nil {
		return 0, "", fmt.Errorf("backup: open %s: %w", name, err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, "", fmt.Errorf("backup: hash %s: %w", name, err)
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

// SafeRelativePath validates an archive path before it is joined to a
// destination directory. It rejects absolute paths, parent traversal, empty
// names, Windows separators and non-canonical names such as "./a" or "a//b".
func SafeRelativePath(name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") {
		return "", fmt.Errorf("backup: invalid archive path %q", name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned != name || !fs.ValidPath(cleaned) {
		return "", fmt.Errorf("backup: unsafe archive path %q", name)
	}
	return cleaned, nil
}
