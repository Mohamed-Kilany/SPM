package backup

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadKey reads a backup encryption key from path. The file may contain 32 raw
// bytes, 64 hexadecimal characters or a standard base64 encoding of 32 bytes.
// Surrounding whitespace is ignored.
func LoadKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("backup: read key %s: %w", path, err)
	}
	key, err := parseKey(data)
	if err != nil {
		return nil, fmt.Errorf("backup: key %s: %w", path, err)
	}
	return key, nil
}

func parseKey(data []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("key file is empty")
	}
	if len(trimmed) == KeySize*2 {
		if decoded, err := hex.DecodeString(trimmed); err == nil && len(decoded) == KeySize {
			return decoded, nil
		}
	}
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(decoded) == KeySize {
		return decoded, nil
	}
	if len(data) == KeySize {
		return append([]byte(nil), data...), nil
	}
	return nil, fmt.Errorf("expected %d raw bytes, %d hex characters or base64 of %d bytes", KeySize, KeySize*2, KeySize)
}

// GenerateKey creates a new random AES-256 key and writes it as hex. Existing
// files are preserved unless overwrite is true. On Unix the file mode is 0600;
// on Windows the operator must restrict the ACL separately.
func GenerateKey(path string, overwrite bool) (string, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("backup: generate key: %w", err)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("backup: create key directory: %w", err)
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return "", fmt.Errorf("backup: create key %s: %w", path, err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("backup: restrict key %s: %w", path, err)
	}
	if _, err := fmt.Fprintf(file, "%s\n", hex.EncodeToString(key)); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("backup: write key %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("backup: sync key %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("backup: close key %s: %w", path, err)
	}
	return KeyID(key), nil
}
