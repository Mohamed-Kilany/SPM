package backup

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateAndLoadKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.key")
	identifier, err := GenerateKey(path, false)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if len(strings.TrimSpace(string(data))) != KeySize*2 {
		t.Fatalf("key file is not 64 hex characters: %q", data)
	}
	key, err := LoadKey(path)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if len(key) != KeySize {
		t.Fatalf("key length = %d, want %d", len(key), KeySize)
	}
	if KeyID(key) != identifier {
		t.Fatalf("generated key id = %q, loaded key id = %q", identifier, KeyID(key))
	}
	if _, err := GenerateKey(path, false); err == nil {
		t.Fatal("GenerateKey overwrote an existing key without -force")
	}
	if _, err := GenerateKey(path, true); err != nil {
		t.Fatalf("GenerateKey force: %v", err)
	}
}

func TestParseKeyFormats(t *testing.T) {
	key := make([]byte, KeySize)
	for index := range key {
		key[index] = byte(index)
	}
	cases := map[string][]byte{
		"hex":    []byte(hex.EncodeToString(key) + "\n"),
		"base64": []byte(base64.StdEncoding.EncodeToString(key) + "\n"),
		"raw":    key,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			parsed, err := parseKey(data)
			if err != nil {
				t.Fatalf("parseKey: %v", err)
			}
			if string(parsed) != string(key) {
				t.Fatal("parsed key differs")
			}
		})
	}
	for _, invalid := range [][]byte{nil, []byte("   "), []byte("short"), make([]byte, KeySize+1)} {
		if _, err := parseKey(invalid); err == nil {
			t.Errorf("parseKey(%q) accepted an invalid key", invalid)
		}
	}
}

func TestLoadKeyMissingFile(t *testing.T) {
	if _, err := LoadKey(filepath.Join(t.TempDir(), "missing.key")); err == nil {
		t.Fatal("LoadKey accepted a missing file")
	}
}
