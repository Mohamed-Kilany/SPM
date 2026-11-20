package attachments

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var png = append([]byte("\x89PNG\r\n\x1a\n"), []byte("payload")...)

func newStore(t *testing.T, max int64) *Store {
	t.Helper()
	store, err := New(t.TempDir(), max, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}

func TestSaveValidatesAndStoresPNG(t *testing.T) {
	store := newStore(t, DefaultMaxBytes)
	projectID, attachmentID := uuid.New(), uuid.New()
	stored, err := store.Save(context.Background(), projectID, attachmentID, "../../shot.png", png)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if stored.Filename != "shot.png" || stored.MIMEType != "image/png" {
		t.Fatalf("unexpected metadata: %+v", stored)
	}
	if stored.ScanStatus != "clean" || stored.ScanEngine != "signature" {
		t.Fatalf("unexpected scan result: %+v", stored)
	}
	if len(stored.ChecksumSHA256) != 64 {
		t.Fatalf("checksum = %q", stored.ChecksumSHA256)
	}
	if strings.Contains(stored.StoragePath, "..") || strings.Contains(stored.StoragePath, "shot") {
		t.Fatalf("storage path must not use the client filename: %q", stored.StoragePath)
	}
	handle, err := store.Open(stored.StoragePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer handle.Close()
	info, err := os.Stat(filepath.Join(store.Dir(), stored.StoragePath))
	if err != nil || info.Size() != int64(len(png)) {
		t.Fatalf("stored file stat: %v, %+v", err, info)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := store.Delete(stored.StoragePath); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), stored.StoragePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file still exists: %v", err)
	}
}

func TestSaveRejectsInvalidUploads(t *testing.T) {
	store := newStore(t, 32)
	cases := []struct {
		name     string
		filename string
		data     []byte
		code     string
	}{
		{"traversal name only", "../..", png, "ATTACHMENT_NAME"},
		{"blocked html", "notes.html", []byte("<html></html>"), "ATTACHMENT_TYPE"},
		{"blocked svg", "logo.svg", []byte("<svg/>"), "ATTACHMENT_TYPE"},
		{"blocked script", "run.js", []byte("alert(1)"), "ATTACHMENT_TYPE"},
		{"unknown extension", "payload.bin", png, "ATTACHMENT_TYPE"},
		{"magic mismatch", "fake.png", []byte("not a png"), "ATTACHMENT_TYPE"},
		{"empty", "empty.txt", nil, "ATTACHMENT_EMPTY"},
		{"too large", "large.txt", []byte(strings.Repeat("a", 33)), "ATTACHMENT_TOO_LARGE"},
		{"active text", "notes.txt", []byte("hello <script>alert(1)</script>"), "ATTACHMENT_TYPE"},
		{"binary text", "notes.txt", []byte{0x00, 0x01}, "ATTACHMENT_TYPE"},
		{"invalid json", "data.json", []byte("{not json"), "ATTACHMENT_TYPE"},
		{"bad zip", "archive.zip", []byte("PK nope"), "ATTACHMENT_TYPE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Save(context.Background(), uuid.New(), uuid.New(), tc.filename, tc.data)
			var rejection *Rejection
			if !errors.As(err, &rejection) {
				t.Fatalf("err = %v, want Rejection", err)
			}
			if rejection.Code != tc.code {
				t.Fatalf("code = %q, want %q (%s)", rejection.Code, tc.code, rejection.Message)
			}
		})
	}
}

func TestSaveRejectsEICAR(t *testing.T) {
	store := newStore(t, DefaultMaxBytes)
	_, err := store.Save(context.Background(), uuid.New(), uuid.New(), "eicar.txt", []byte(EICAR))
	var rejection *Rejection
	if !errors.As(err, &rejection) || rejection.Code != "ATTACHMENT_MALWARE" {
		t.Fatalf("err = %v, want ATTACHMENT_MALWARE rejection", err)
	}
}

func TestSaveAcceptsPlainTextCSVAndZIP(t *testing.T) {
	store := newStore(t, DefaultMaxBytes)
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"notes.txt", []byte("line one\nline two\n")},
		{"rows.csv", []byte("id,title\n1,ok\n")},
		{"data.json", []byte(`{"ok":true}`)},
		{"bundle.zip", []byte("PK\x03\x04rest")},
	} {
		if _, err := store.Save(context.Background(), uuid.New(), uuid.New(), tc.name, tc.data); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
}

func TestResolveRejectsEscapingPaths(t *testing.T) {
	store := newStore(t, DefaultMaxBytes)
	for _, path := range []string{"../secret", "..\\secret", filepath.Join("..", "secret"), absolutePath(store.Dir())} {
		if _, err := store.Open(path); err == nil {
			t.Fatalf("Open(%q) succeeded, want rejection", path)
		}
	}
}

func absolutePath(dir string) string {
	return filepath.Join(dir, "..", "outside")
}

func TestClamAVConfiguredButUnreachableFailsClosed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listen: %v", err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	store, err := New(t.TempDir(), DefaultMaxBytes, address)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = store.Save(context.Background(), uuid.New(), uuid.New(), "notes.txt", []byte("clean text"))
	var unavailable *Unavailable
	if !errors.As(err, &unavailable) {
		t.Fatalf("err = %v, want Unavailable", err)
	}
}
