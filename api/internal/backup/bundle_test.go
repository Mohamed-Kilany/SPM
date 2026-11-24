package backup

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createStaging(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "databases", "spm.dump"), []byte("spm database dump"))
	writeTestFile(t, filepath.Join(root, "databases", "gitea.dump"), []byte("gitea database dump"))
	writeTestFile(t, filepath.Join(root, "volumes", "gitea_data.tar"), bytes.Repeat([]byte("g"), 2048))
	writeTestFile(t, filepath.Join(root, "volumes", "nats_data.tar"), bytes.Repeat([]byte("n"), 1024))
	writeTestFile(t, filepath.Join(root, "config", "docker-compose.yaml"), []byte("services:\n  api_backend: {}\n"))
	writeTestFile(t, filepath.Join(root, "config", "nats", "nats.conf"), []byte("jetstream {}\n"))
	writeTestFile(t, filepath.Join(root, "metadata", "git.txt"), []byte("commit\n"))
	return root
}

func packStaging(t *testing.T, root, output string) *Manifest {
	t.Helper()
	file, err := os.Create(output)
	if err != nil {
		t.Fatalf("create bundle: %v", err)
	}
	manifest, err := Pack(root, file, BuildOptions{
		CreatedAt: time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC),
		Host:      "test-host",
		Commit:    "deadbeef",
		Databases: []string{"spm", "git"},
		Volumes:   []string{"gitea_data", "nats_data"},
	})
	closeErr := file.Close()
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if closeErr != nil {
		t.Fatalf("close bundle: %v", closeErr)
	}
	return manifest
}

func TestPackInspectVerifyUnpack(t *testing.T) {
	staging := createStaging(t)
	directory := t.TempDir()
	bundlePath := filepath.Join(directory, "spm-backup-test.tar")
	manifest := packStaging(t, staging, bundlePath)

	inspect, err := InspectBundle(bundlePath, nil)
	if err != nil {
		t.Fatalf("InspectBundle: %v", err)
	}
	if inspect.Encrypted {
		t.Error("plain bundle reported as encrypted")
	}
	if inspect.Manifest.Host != "test-host" || len(inspect.Manifest.Entries) != len(manifest.Entries) {
		t.Fatalf("inspect manifest = %+v", inspect.Manifest)
	}

	report, err := VerifyBundle(bundlePath, nil)
	if err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}
	if report.Files != len(manifest.Entries) || report.Bytes != manifest.TotalBytes {
		t.Fatalf("report = %+v, manifest entries=%d bytes=%d", report, len(manifest.Entries), manifest.TotalBytes)
	}

	destination := filepath.Join(directory, "restore")
	bundle, err := OpenBundle(bundlePath, nil)
	if err != nil {
		t.Fatalf("OpenBundle: %v", err)
	}
	extracted, err := Unpack(bundle, destination)
	_ = bundle.Close()
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if len(extracted.Entries) != len(manifest.Entries) {
		t.Fatalf("extracted entries = %d, want %d", len(extracted.Entries), len(manifest.Entries))
	}
	for _, entry := range manifest.Entries {
		original, err := os.ReadFile(filepath.Join(staging, filepath.FromSlash(entry.Path)))
		if err != nil {
			t.Fatalf("read original %s: %v", entry.Path, err)
		}
		restored, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(entry.Path)))
		if err != nil {
			t.Fatalf("read restored %s: %v", entry.Path, err)
		}
		if !bytes.Equal(original, restored) {
			t.Errorf("restored content differs for %s", entry.Path)
		}
	}
}

type tarFile struct {
	Name string
	Data []byte
}

func readTarFiles(t *testing.T, path string) []tarFile {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open tar: %v", err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	var files []tarFile
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read tar entry %s: %v", header.Name, err)
		}
		files = append(files, tarFile{Name: header.Name, Data: data})
	}
	return files
}

func writeTarFiles(t *testing.T, path string, files []tarFile) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create tar: %v", err)
	}
	writer := tar.NewWriter(file)
	modified := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	for _, entry := range files {
		header := &tar.Header{
			Name:     entry.Name,
			Mode:     0o600,
			Size:     int64(len(entry.Data)),
			Typeflag: tar.TypeReg,
			ModTime:  modified,
			Format:   tar.FormatPAX,
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("write header %s: %v", entry.Name, err)
		}
		if _, err := writer.Write(entry.Data); err != nil {
			t.Fatalf("write data %s: %v", entry.Name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}

func TestVerifyRejectsModifiedPayload(t *testing.T) {
	staging := createStaging(t)
	directory := t.TempDir()
	bundlePath := filepath.Join(directory, "bundle.tar")
	packStaging(t, staging, bundlePath)

	files := readTarFiles(t, bundlePath)
	for index := range files {
		if files[index].Name == "databases/spm.dump" {
			files[index].Data = []byte("spm database Xump") // same length, different bytes
		}
	}
	tampered := filepath.Join(directory, "tampered.tar")
	writeTarFiles(t, tampered, files)

	if _, err := VerifyBundle(tampered, nil); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("VerifyBundle error = %v, want checksum mismatch", err)
	}
}

func TestVerifyRejectsExtraAndMissingFiles(t *testing.T) {
	staging := createStaging(t)
	directory := t.TempDir()
	bundlePath := filepath.Join(directory, "bundle.tar")
	packStaging(t, staging, bundlePath)
	files := readTarFiles(t, bundlePath)

	withExtra := append(append([]tarFile(nil), files...), tarFile{Name: "databases/extra.dump", Data: []byte("extra")})
	extraPath := filepath.Join(directory, "extra.tar")
	writeTarFiles(t, extraPath, withExtra)
	if _, err := VerifyBundle(extraPath, nil); err == nil || !strings.Contains(err.Error(), "unexpected file") {
		t.Fatalf("VerifyBundle extra error = %v", err)
	}

	missingPath := filepath.Join(directory, "missing.tar")
	writeTarFiles(t, missingPath, files[:len(files)-1])
	if _, err := VerifyBundle(missingPath, nil); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("VerifyBundle missing error = %v", err)
	}
}

func TestVerifyRejectsTruncatedBundle(t *testing.T) {
	staging := createStaging(t)
	directory := t.TempDir()
	bundlePath := filepath.Join(directory, "bundle.tar")
	packStaging(t, staging, bundlePath)
	info, err := os.Stat(bundlePath)
	if err != nil {
		t.Fatalf("stat bundle: %v", err)
	}
	truncated := filepath.Join(directory, "truncated.tar")
	if err := os.WriteFile(truncated, make([]byte, info.Size()/2), 0o600); err != nil {
		t.Fatalf("write truncated bundle: %v", err)
	}
	if _, err := VerifyBundle(truncated, nil); err == nil {
		t.Fatal("VerifyBundle accepted a truncated archive")
	}
}

func TestEncryptedBundleRoundTrip(t *testing.T) {
	staging := createStaging(t)
	directory := t.TempDir()
	plainPath := filepath.Join(directory, "bundle.tar")
	sealedPath := filepath.Join(directory, "bundle.tar.enc")
	packStaging(t, staging, plainPath)

	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	source, err := os.Open(plainPath)
	if err != nil {
		t.Fatalf("open plain bundle: %v", err)
	}
	target, err := os.Create(sealedPath)
	if err != nil {
		t.Fatalf("create sealed bundle: %v", err)
	}
	if err := SealStream(target, source, key); err != nil {
		t.Fatalf("SealStream: %v", err)
	}
	_ = source.Close()
	if err := target.Close(); err != nil {
		t.Fatalf("close sealed bundle: %v", err)
	}

	report, err := VerifyBundle(sealedPath, key)
	if err != nil {
		t.Fatalf("VerifyBundle encrypted: %v", err)
	}
	if !report.Encrypted {
		t.Error("encrypted bundle reported as plain")
	}
	if _, err := VerifyBundle(sealedPath, nil); err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("VerifyBundle without key error = %v", err)
	}
	wrong := make([]byte, KeySize)
	if _, err := VerifyBundle(sealedPath, wrong); err == nil || !strings.Contains(err.Error(), "key id") {
		t.Fatalf("VerifyBundle wrong key error = %v", err)
	}

	destination := filepath.Join(directory, "restore")
	bundle, err := OpenBundle(sealedPath, key)
	if err != nil {
		t.Fatalf("OpenBundle encrypted: %v", err)
	}
	if _, err := Unpack(bundle, destination); err != nil {
		t.Fatalf("Unpack encrypted: %v", err)
	}
	_ = bundle.Close()
	if _, err := os.Stat(filepath.Join(destination, "databases", "spm.dump")); err != nil {
		t.Fatalf("restored dump missing: %v", err)
	}
}

func TestEncryptedBundleRejectsTampering(t *testing.T) {
	staging := createStaging(t)
	directory := t.TempDir()
	plainPath := filepath.Join(directory, "bundle.tar")
	sealedPath := filepath.Join(directory, "bundle.tar.enc")
	packStaging(t, staging, plainPath)
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	plain, err := os.ReadFile(plainPath)
	if err != nil {
		t.Fatalf("read plain bundle: %v", err)
	}
	var sealed bytes.Buffer
	if err := SealStream(&sealed, bytes.NewReader(plain), key); err != nil {
		t.Fatalf("SealStream: %v", err)
	}
	if err := os.WriteFile(sealedPath, sealed.Bytes(), 0o600); err != nil {
		t.Fatalf("write sealed bundle: %v", err)
	}

	data, err := os.ReadFile(sealedPath)
	if err != nil {
		t.Fatalf("read sealed bundle: %v", err)
	}
	data[len(data)/2] ^= 0xff
	tampered := filepath.Join(directory, "tampered.enc")
	if err := os.WriteFile(tampered, data, 0o600); err != nil {
		t.Fatalf("write tampered bundle: %v", err)
	}
	if _, err := VerifyBundle(tampered, key); err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("VerifyBundle tampered error = %v", err)
	}

	if err := os.WriteFile(tampered, data[:len(data)-4], 0o600); err != nil {
		t.Fatalf("write truncated bundle: %v", err)
	}
	if _, err := VerifyBundle(tampered, key); err == nil {
		t.Fatal("VerifyBundle accepted a truncated encrypted bundle")
	}
}

func TestUnpackRejectsTraversalAndDuplicates(t *testing.T) {
	manifest := &Manifest{
		SchemaVersion: SchemaVersion,
		CreatedAt:     time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC),
		Host:          "host",
		Entries:       []Entry{},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	cases := []struct {
		name  string
		files []tarFile
		want  string
	}{
		{
			name: "traversal",
			files: []tarFile{
				{Name: ManifestName, Data: data},
				{Name: "../escape", Data: []byte("bad")},
			},
			want: "unsafe archive path",
		},
		{
			name: "duplicate",
			files: []tarFile{
				{Name: ManifestName, Data: data},
				{Name: "metadata/a.txt", Data: []byte("one")},
				{Name: "metadata/a.txt", Data: []byte("two")},
			},
			want: "duplicate",
		},
		{
			name: "directory",
			files: []tarFile{
				{Name: ManifestName, Data: data},
			},
			want: "", // the valid manifest alone is accepted
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bundle.tar")
			writeTarFiles(t, path, test.files)
			destination := t.TempDir()
			bundle, err := OpenBundle(path, nil)
			if err != nil {
				t.Fatalf("OpenBundle: %v", err)
			}
			defer bundle.Close()
			_, unpackErr := Unpack(bundle, destination)
			if test.want == "" {
				if unpackErr != nil {
					t.Fatalf("Unpack error = %v", unpackErr)
				}
				return
			}
			if unpackErr == nil || !strings.Contains(unpackErr.Error(), test.want) {
				t.Fatalf("Unpack error = %v, want %q", unpackErr, test.want)
			}
		})
	}
}

func TestUnpackDoesNotOverwriteExistingFiles(t *testing.T) {
	staging := createStaging(t)
	directory := t.TempDir()
	bundlePath := filepath.Join(directory, "bundle.tar")
	packStaging(t, staging, bundlePath)
	destination := filepath.Join(directory, "restore")
	if err := os.MkdirAll(filepath.Join(destination, "databases"), 0o700); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destination, "databases", "spm.dump"), []byte("existing"), 0o600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}
	bundle, err := OpenBundle(bundlePath, nil)
	if err != nil {
		t.Fatalf("OpenBundle: %v", err)
	}
	defer bundle.Close()
	if _, err := Unpack(bundle, destination); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("Unpack error = %v, want existing-file rejection", err)
	}
}
