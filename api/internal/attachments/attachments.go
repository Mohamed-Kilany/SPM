// Package attachments validates, scans and stores uploaded work item files.
//
// Storage is content-addressed by generated attachment ID, never by the
// client-supplied filename, so a traversal or double-extension attack cannot
// choose a path. Validation is fail-closed: an unknown extension, a magic
// byte mismatch, an oversized body, active markup in a text upload, the
// EICAR test file or a configured-but-unreachable ClamAV daemon all reject
// the upload before anything is persisted.
package attachments

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// EICAR is the standard antivirus test string. It is harmless, and its
// presence always rejects an upload.
const EICAR = `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`

// DefaultMaxBytes is the default per-file upload limit (10 MiB).
const DefaultMaxBytes int64 = 10 << 20

// Rejection is a validation failure caused by the uploaded file itself. The
// handler maps it to 422 Unprocessable Entity.
type Rejection struct {
	Code    string
	Message string
}

func (r *Rejection) Error() string { return r.Message }

// Unavailable is a scanner failure. Fail-closed semantics mean the handler
// refuses the upload instead of storing an unscanned file.
type Unavailable struct{ Message string }

func (u *Unavailable) Error() string { return u.Message }

// Stored describes a successfully persisted, scanned attachment.
type Stored struct {
	StoragePath    string
	Filename       string
	MIMEType       string
	SizeBytes      int64
	ChecksumSHA256 string
	ScanStatus     string
	ScanEngine     string
	ScanDetail     string
}

// Store owns the attachment directory and scanner configuration.
type Store struct {
	dir     string
	max     int64
	clamd   string
	timeout time.Duration
}

// New creates the storage root and returns a store. An empty address disables
// network scanning; the embedded signature checks still run.
func New(dir string, maxBytes int64, clamdAddr string) (*Store, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = filepath.Join("data", "attachments")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	clean := filepath.Clean(dir)
	if err := os.MkdirAll(clean, 0o750); err != nil {
		return nil, fmt.Errorf("create attachment directory: %w", err)
	}
	return &Store{dir: clean, max: maxBytes, clamd: strings.TrimSpace(clamdAddr), timeout: 5 * time.Second}, nil
}

// Dir is the absolute or relative storage root.
func (s *Store) Dir() string { return s.dir }

// MaxBytes is the configured per-file limit.
func (s *Store) MaxBytes() int64 { return s.max }

// Save validates and scans data, then writes it below the storage root.
func (s *Store) Save(ctx context.Context, projectID, attachmentID uuid.UUID, filename string, data []byte) (Stored, error) {
	name, mime, rejection := validateName(filename)
	if rejection != nil {
		return Stored{}, rejection
	}
	if int64(len(data)) == 0 {
		return Stored{}, &Rejection{Code: "ATTACHMENT_EMPTY", Message: "The uploaded file is empty"}
	}
	if int64(len(data)) > s.max {
		return Stored{}, &Rejection{Code: "ATTACHMENT_TOO_LARGE", Message: fmt.Sprintf("Attachments must be %d bytes or smaller", s.max)}
	}
	if rejection := validateContent(name, mime, data); rejection != nil {
		return Stored{}, rejection
	}
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])

	engine := "signature"
	if s.clamd != "" {
		if err := s.scanClamAV(ctx, data); err != nil {
			return Stored{}, err
		}
		engine = "clamav"
	}

	relative := filepath.Join(projectID.String(), attachmentID.String())
	target := filepath.Join(s.dir, relative)
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return Stored{}, fmt.Errorf("create attachment project directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".upload-*")
	if err != nil {
		return Stored{}, fmt.Errorf("create attachment file: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}()
	if _, err := temp.Write(data); err != nil {
		return Stored{}, fmt.Errorf("write attachment: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return Stored{}, fmt.Errorf("sync attachment: %w", err)
	}
	if err := temp.Close(); err != nil {
		return Stored{}, fmt.Errorf("close attachment: %w", err)
	}
	if err := os.Chmod(tempName, 0o640); err != nil {
		return Stored{}, fmt.Errorf("secure attachment: %w", err)
	}
	if err := os.Rename(tempName, target); err != nil {
		return Stored{}, fmt.Errorf("store attachment: %w", err)
	}
	return Stored{
		StoragePath:    relative,
		Filename:       name,
		MIMEType:       mime,
		SizeBytes:      int64(len(data)),
		ChecksumSHA256: checksum,
		ScanStatus:     "clean",
		ScanEngine:     engine,
	}, nil
}

// Open returns a read handle for a path previously returned by Save.
func (s *Store) Open(relative string) (io.ReadCloser, error) {
	target, err := s.resolve(relative)
	if err != nil {
		return nil, err
	}
	return os.Open(target)
}

// Delete removes stored bytes. A missing file is not an error.
func (s *Store) Delete(relative string) error {
	target, err := s.resolve(relative)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// resolve keeps every read or delete inside the storage root even if a stored
// path was tampered with.
func (s *Store) resolve(relative string) (string, error) {
	clean := filepath.Clean(relative)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", &Rejection{Code: "ATTACHMENT_INVALID_PATH", Message: "Attachment path is invalid"}
	}
	target := filepath.Join(s.dir, clean)
	rel, err := filepath.Rel(s.dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", &Rejection{Code: "ATTACHMENT_INVALID_PATH", Message: "Attachment path is invalid"}
	}
	return target, nil
}

// allowed maps the only accepted extensions to their canonical media type.
var allowed = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".pdf":  "application/pdf",
	".txt":  "text/plain",
	".log":  "text/plain",
	".md":   "text/markdown",
	".csv":  "text/csv",
	".json": "application/json",
	".zip":  "application/zip",
}

// blocked can never be uploaded even though they are common evidence files.
var blocked = map[string]bool{
	".html": true, ".htm": true, ".xhtml": true, ".svg": true, ".xml": true,
	".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".jsx": true, ".tsx": true,
	".exe": true, ".dll": true, ".bat": true, ".cmd": true, ".com": true, ".msi": true,
	".ps1": true, ".sh": true, ".bash": true, ".app": true, ".jar": true, ".war": true,
	".php": true, ".py": true, ".rb": true, ".pl": true, ".vbs": true, ".wsf": true,
	".scr": true, ".pif": true, ".hta": true, ".lnk": true, ".iso": true, ".img": true,
}

func validateName(filename string) (string, string, *Rejection) {
	name := filepath.Base(strings.TrimSpace(filename))
	name = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "", "", &Rejection{Code: "ATTACHMENT_NAME", Message: "The uploaded file needs a name"}
	}
	if len(name) > 180 {
		return "", "", &Rejection{Code: "ATTACHMENT_NAME", Message: "Attachment names must be 180 characters or fewer"}
	}
	ext := strings.ToLower(filepath.Ext(name))
	if blocked[ext] {
		return "", "", &Rejection{Code: "ATTACHMENT_TYPE", Message: "This file type is not allowed as an attachment"}
	}
	mime, ok := allowed[ext]
	if !ok {
		return "", "", &Rejection{Code: "ATTACHMENT_TYPE", Message: "Allowed attachments are PNG, JPEG, GIF, WebP, PDF, plain text, Markdown, CSV, JSON and ZIP"}
	}
	return name, mime, nil
}

func validateContent(name, mime string, data []byte) *Rejection {
	if index := strings.Index(string(data), EICAR); index >= 0 {
		return &Rejection{Code: "ATTACHMENT_MALWARE", Message: "The upload matches the antivirus test signature and was rejected"}
	}
	switch mime {
	case "image/png":
		if !hasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
			return magicMismatch(name)
		}
	case "image/jpeg":
		if !hasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) {
			return magicMismatch(name)
		}
	case "image/gif":
		if !hasPrefix(data, []byte("GIF87a")) && !hasPrefix(data, []byte("GIF89a")) {
			return magicMismatch(name)
		}
	case "image/webp":
		if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
			return magicMismatch(name)
		}
	case "application/pdf":
		if !hasPrefix(data, []byte("%PDF-")) {
			return magicMismatch(name)
		}
	case "application/zip":
		if !hasPrefix(data, []byte("PK\x03\x04")) && !hasPrefix(data, []byte("PK\x05\x06")) && !hasPrefix(data, []byte("PK\x07\x08")) {
			return magicMismatch(name)
		}
	default:
		if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
			return &Rejection{Code: "ATTACHMENT_TYPE", Message: "Text attachments must be UTF-8 and must not contain binary data"}
		}
		if mime == "application/json" && !json.Valid(data) {
			return &Rejection{Code: "ATTACHMENT_TYPE", Message: "JSON attachments must contain valid JSON"}
		}
		lower := strings.ToLower(string(data))
		for _, needle := range []string{"<script", "<!doctype html", "<html", "<svg", "<iframe", "javascript:"} {
			if strings.Contains(lower, needle) {
				return &Rejection{Code: "ATTACHMENT_TYPE", Message: "Active markup is not allowed in text attachments"}
			}
		}
	}
	return nil
}

func magicMismatch(name string) *Rejection {
	return &Rejection{Code: "ATTACHMENT_TYPE", Message: "The file content does not match its " + filepath.Ext(name) + " extension"}
}

func hasPrefix(data, prefix []byte) bool {
	return len(data) >= len(prefix) && string(data[:len(prefix)]) == string(prefix)
}

// scanClamAV streams the payload using clamd's INSTREAM protocol. Any network
// or protocol failure is returned as Unavailable so the upload is refused.
func (s *Store) scanClamAV(ctx context.Context, data []byte) error {
	dialer := net.Dialer{Timeout: s.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", s.clamd)
	if err != nil {
		return &Unavailable{Message: "Malware scanning is unavailable; the upload was not stored"}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(s.timeout))
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return &Unavailable{Message: "Malware scanning failed; the upload was not stored"}
	}
	writer := bufio.NewWriter(conn)
	for offset := 0; offset < len(data); {
		end := offset + 32*1024
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(chunk)))
		if _, err := writer.Write(size[:]); err != nil {
			return &Unavailable{Message: "Malware scanning failed; the upload was not stored"}
		}
		if _, err := writer.Write(chunk); err != nil {
			return &Unavailable{Message: "Malware scanning failed; the upload was not stored"}
		}
		offset = end
	}
	if _, err := writer.Write([]byte{0, 0, 0, 0}); err != nil {
		return &Unavailable{Message: "Malware scanning failed; the upload was not stored"}
	}
	if err := writer.Flush(); err != nil {
		return &Unavailable{Message: "Malware scanning failed; the upload was not stored"}
	}
	reply, err := bufio.NewReader(conn).ReadString(0)
	if err != nil && !errors.Is(err, io.EOF) {
		return &Unavailable{Message: "Malware scanning failed; the upload was not stored"}
	}
	reply = strings.TrimRight(reply, "\x00\r\n ")
	lower := strings.ToLower(reply)
	if strings.HasSuffix(lower, "ok") {
		return nil
	}
	if strings.Contains(lower, "found") {
		return &Rejection{Code: "ATTACHMENT_MALWARE", Message: "Malware was detected and the upload was rejected"}
	}
	return &Unavailable{Message: "Malware scanning failed; the upload was not stored"}
}
