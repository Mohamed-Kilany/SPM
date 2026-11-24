package backup

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Pack writes a bundle tar to w and returns the manifest that was embedded.
// The manifest is always the first entry. Payload files are re-stat'ed while
// packing, so a staging directory that changed after the manifest was built is
// rejected instead of producing an inconsistent bundle.
func Pack(root string, w io.Writer, opts BuildOptions) (*Manifest, error) {
	manifest, err := BuildManifest(root, opts)
	if err != nil {
		return nil, err
	}
	if err := WriteManifest(root, manifest); err != nil {
		return nil, err
	}
	writer := tar.NewWriter(w)
	if err := writeTarFile(writer, root, ManifestName, 0); err != nil {
		_ = writer.Close()
		return nil, err
	}
	for _, entry := range manifest.Entries {
		if err := writeTarFile(writer, root, entry.Path, entry.Size); err != nil {
			_ = writer.Close()
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("backup: finish archive: %w", err)
	}
	return manifest, nil
}

func writeTarFile(writer *tar.Writer, root, relative string, expected int64) error {
	name := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Stat(name)
	if err != nil {
		return fmt.Errorf("backup: stat %s: %w", relative, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup: %s is not a regular file", relative)
	}
	if expected > 0 && info.Size() != expected {
		return fmt.Errorf("backup: %s changed size after the manifest was built", relative)
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("backup: header %s: %w", relative, err)
	}
	header.Name = relative
	header.Mode = 0o600
	header.Uid = 0
	header.Gid = 0
	header.Uname = ""
	header.Gname = ""
	header.Format = tar.FormatPAX
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("backup: write header %s: %w", relative, err)
	}
	file, err := os.Open(name)
	if err != nil {
		return fmt.Errorf("backup: open %s: %w", relative, err)
	}
	defer file.Close()
	if _, err := io.Copy(writer, file); err != nil {
		return fmt.Errorf("backup: write %s: %w", relative, err)
	}
	return nil
}

// BundleReader is an opened bundle. The embedded reader yields the plaintext
// tar stream for both plain and encrypted bundles.
type BundleReader struct {
	io.Reader
	Encrypted bool
	closer    io.Closer
}

// Close releases the underlying file.
func (r *BundleReader) Close() error {
	if r == nil || r.closer == nil {
		return nil
	}
	return r.closer.Close()
}

// VerifyReport summarizes a successful bundle verification.
type VerifyReport struct {
	Path      string    `json:"path"`
	Encrypted bool      `json:"encrypted"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Manifest  *Manifest `json:"manifest"`
}

// VerifyBundle streams a bundle, recomputes every payload digest and rejects
// missing, extra, truncated or modified entries. It never extracts to disk.
func VerifyBundle(path string, key []byte) (*VerifyReport, error) {
	bundle, err := OpenBundle(path, key)
	if err != nil {
		return nil, err
	}
	defer bundle.Close()

	archive := tar.NewReader(bundle)
	header, err := archive.Next()
	if err != nil {
		return nil, fmt.Errorf("backup: read first archive entry: %w", err)
	}
	if header.Name != ManifestName || header.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("backup: first archive entry must be %s", ManifestName)
	}
	manifest, err := decodeManifest(archive)
	if err != nil {
		return nil, err
	}

	expected := make(map[string]Entry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		expected[entry.Path] = entry
	}
	seen := make(map[string]struct{}, len(expected))
	var files int
	var bytesRead int64

	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("backup: read archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("backup: archive entry %q is not a regular file", header.Name)
		}
		clean, err := SafeRelativePath(header.Name)
		if err != nil {
			return nil, err
		}
		entry, ok := expected[clean]
		if !ok {
			return nil, fmt.Errorf("backup: archive contains unexpected file %q", clean)
		}
		if _, duplicate := seen[clean]; duplicate {
			return nil, fmt.Errorf("backup: archive contains duplicate file %q", clean)
		}
		seen[clean] = struct{}{}
		if header.Size != entry.Size {
			return nil, fmt.Errorf("backup: %s size is %d, manifest says %d", clean, header.Size, entry.Size)
		}
		hash := sha256.New()
		written, err := io.Copy(hash, archive)
		if err != nil {
			return nil, fmt.Errorf("backup: read %s: %w", clean, err)
		}
		if written != entry.Size {
			return nil, fmt.Errorf("backup: %s ended after %d of %d bytes", clean, written, entry.Size)
		}
		digest := hex.EncodeToString(hash.Sum(nil))
		if !strings.EqualFold(digest, entry.SHA256) {
			return nil, fmt.Errorf("backup: %s checksum mismatch (got %s, want %s)", clean, digest, entry.SHA256)
		}
		files++
		bytesRead += written
	}
	for name := range expected {
		if _, ok := seen[name]; !ok {
			return nil, fmt.Errorf("backup: archive is missing %q", name)
		}
	}
	return &VerifyReport{
		Path:      path,
		Encrypted: bundle.Encrypted,
		Files:     files,
		Bytes:     bytesRead,
		Manifest:  manifest,
	}, nil
}

// InspectReport summarizes a bundle without hashing every payload file.
type InspectReport struct {
	Path      string    `json:"path"`
	Encrypted bool      `json:"encrypted"`
	Manifest  *Manifest `json:"manifest"`
}

// InspectBundle reads only the manifest. Use VerifyBundle before a restore.
func InspectBundle(path string, key []byte) (*InspectReport, error) {
	bundle, err := OpenBundle(path, key)
	if err != nil {
		return nil, err
	}
	defer bundle.Close()

	archive := tar.NewReader(bundle)
	header, err := archive.Next()
	if err != nil {
		return nil, fmt.Errorf("backup: read first archive entry: %w", err)
	}
	if header.Name != ManifestName || header.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("backup: first archive entry must be %s", ManifestName)
	}
	manifest, err := decodeManifest(archive)
	if err != nil {
		return nil, err
	}
	return &InspectReport{Path: path, Encrypted: bundle.Encrypted, Manifest: manifest}, nil
}

// Unpack extracts a verified bundle into dest. dest must not already contain
// any of the archived paths. Symlinks, hard links, devices and parent
// traversal are rejected.
func Unpack(reader io.Reader, dest string) (*Manifest, error) {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return nil, fmt.Errorf("backup: create destination %s: %w", dest, err)
	}
	archive := tar.NewReader(reader)
	seen := make(map[string]struct{})
	var manifest *Manifest

	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("backup: read archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("backup: refusing to extract non-regular entry %q", header.Name)
		}
		clean, err := SafeRelativePath(header.Name)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[clean]; duplicate {
			return nil, fmt.Errorf("backup: archive contains duplicate file %q", clean)
		}
		seen[clean] = struct{}{}

		target := filepath.Join(dest, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, fmt.Errorf("backup: create directory for %s: %w", clean, err)
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("backup: create %s: %w", clean, err)
		}
		written, copyErr := io.Copy(file, archive)
		closeErr := file.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("backup: extract %s: %w", clean, copyErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("backup: close %s: %w", clean, closeErr)
		}
		if written != header.Size {
			return nil, fmt.Errorf("backup: %s ended after %d of %d bytes", clean, written, header.Size)
		}
		if clean == ManifestName {
			data, err := os.ReadFile(target)
			if err != nil {
				return nil, fmt.Errorf("backup: read extracted manifest: %w", err)
			}
			manifest, err = decodeManifest(bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
		}
	}
	if manifest == nil {
		return nil, fmt.Errorf("backup: archive is missing %s", ManifestName)
	}
	return manifest, nil
}

// IsEncryptedFile reports whether path starts with the encrypted bundle magic.
func IsEncryptedFile(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("backup: open %s: %w", path, err)
	}
	defer file.Close()
	var magic [len(encryptedMagic)]byte
	read, err := io.ReadFull(file, magic[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, fmt.Errorf("backup: read %s: %w", path, err)
	}
	return read == len(magic) && string(magic[:]) == encryptedMagic, nil
}

// OpenBundle opens a plain or encrypted bundle. The caller must close the
// result. A missing key for an encrypted bundle is an error.
func OpenBundle(path string, key []byte) (*BundleReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("backup: open %s: %w", path, err)
	}
	var magic [len(encryptedMagic)]byte
	read, err := io.ReadFull(file, magic[:])
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("backup: read bundle header: %w", err)
	}
	if read == len(magic) && string(magic[:]) == encryptedMagic {
		if len(key) == 0 {
			_ = file.Close()
			return nil, errors.New("backup: bundle is encrypted; supply a key file")
		}
		decryptor, err := newDecryptReaderAfterMagic(file, key)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		return &BundleReader{Reader: decryptor, Encrypted: true, closer: file}, nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("backup: rewind %s: %w", path, err)
	}
	return &BundleReader{Reader: file, Encrypted: false, closer: file}, nil
}

// newDecryptReaderAfterMagic is called after the caller consumed the
// encryptedMagic prefix; it reads and checks the key identifier.
func newDecryptReaderAfterMagic(src io.Reader, key []byte) (*decryptReader, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	identifier := make([]byte, keyIDSize)
	if _, err := io.ReadFull(src, identifier); err != nil {
		return nil, fmt.Errorf("backup: read encrypted key id: %w", err)
	}
	if !equalBytes(identifier, keyIDBytes(key)) {
		return nil, fmt.Errorf("backup: bundle key id %s does not match the supplied key %s",
			hex.EncodeToString(identifier), KeyID(key))
	}
	return &decryptReader{src: src, gcm: gcm}, nil
}
