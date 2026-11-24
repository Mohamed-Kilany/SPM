package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spm-platform/api/internal/backup"
)

func runCommand(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, err := runCommand(t, args...)
	if err != nil {
		t.Fatalf("backupctl %s: %v\nstderr: %s", strings.Join(args, " "), err, stderr)
	}
	return stdout
}

func TestBackupctlRoundTrip(t *testing.T) {
	directory := t.TempDir()
	staging := filepath.Join(directory, "staging")
	if err := os.MkdirAll(filepath.Join(staging, "databases"), 0o700); err != nil {
		t.Fatalf("create staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "databases", "spm.dump"), []byte("dump-data"), 0o600); err != nil {
		t.Fatalf("write dump: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "manifest.json"), []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale manifest: %v", err)
	}
	keyPath := filepath.Join(directory, "backup.key")
	mustRun(t, "keygen", "-output", keyPath)
	key, err := backup.LoadKey(keyPath)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}

	plainPath := filepath.Join(directory, "spm-backup-test.tar")
	stdout := mustRun(t, "pack", "-root", staging, "-output", plainPath, "-host", "ci", "-commit", "abc")
	if !strings.Contains(stdout, "bundle: "+plainPath) || !strings.Contains(stdout, "files: 1") {
		t.Fatalf("pack output = %s", stdout)
	}

	stdout = mustRun(t, "inspect", "-bundle", plainPath)
	if !strings.Contains(stdout, "encrypted: false") {
		t.Fatalf("inspect output = %s", stdout)
	}
	stdout = mustRun(t, "verify", "-bundle", plainPath)
	if !strings.Contains(stdout, "verified: "+plainPath) || !strings.Contains(stdout, "bytes: 9") {
		t.Fatalf("verify output = %s", stdout)
	}

	sealedPath := plainPath + ".enc"
	stdout = mustRun(t, "seal", "-input", plainPath, "-output", sealedPath, "-key-file", keyPath)
	if !strings.Contains(stdout, "key id: "+backup.KeyID(key)) {
		t.Fatalf("seal output = %s", stdout)
	}
	stdout = mustRun(t, "verify", "-bundle", sealedPath, "-key-file", keyPath)
	if !strings.Contains(stdout, "encrypted: true") {
		t.Fatalf("verify encrypted output = %s", stdout)
	}
	if _, _, err := runCommand(t, "verify", "-bundle", sealedPath); err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("verify without key error = %v", err)
	}

	openedPath := filepath.Join(directory, "opened.tar")
	mustRun(t, "open", "-input", sealedPath, "-output", openedPath, "-key-file", keyPath)
	original, err := os.ReadFile(plainPath)
	if err != nil {
		t.Fatalf("read plain bundle: %v", err)
	}
	opened, err := os.ReadFile(openedPath)
	if err != nil {
		t.Fatalf("read opened bundle: %v", err)
	}
	if !bytes.Equal(original, opened) {
		t.Fatal("open did not reproduce the plain bundle")
	}

	restore := filepath.Join(directory, "restore")
	stdout = mustRun(t, "unpack", "-input", sealedPath, "-output", restore, "-key-file", keyPath)
	if !strings.Contains(stdout, "encrypted: true") {
		t.Fatalf("unpack output = %s", stdout)
	}
	data, err := os.ReadFile(filepath.Join(restore, "databases", "spm.dump"))
	if err != nil || string(data) != "dump-data" {
		t.Fatalf("restored dump = %q, err=%v", data, err)
	}
	if _, err := backup.ReadManifest(restore); err != nil {
		t.Fatalf("extracted manifest: %v", err)
	}

	statusPath := filepath.Join(directory, "backup-status.json")
	mustRun(t, "status", "-file", statusPath, "-set", "success",
		"-bundle", sealedPath, "-sha256", "abc", "-size", "1234",
		"-duration", "90s", "-encrypted")
	status, err := backup.ReadStatus(statusPath)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if status.LastBundle != sealedPath || status.LastSizeBytes != 1234 ||
		status.LastDurationMS != 90000 || !status.Encrypted {
		t.Fatalf("recorded status = %+v", status)
	}
	mustRun(t, "status", "-file", statusPath, "-set", "verified")
	mustRun(t, "status", "-file", statusPath, "-set", "drill-ok")
	status, err = backup.ReadStatus(statusPath)
	if err != nil {
		t.Fatalf("ReadStatus after verify: %v", err)
	}
	if status.LastVerifiedAt == nil || status.LastDrillAt == nil || !status.LastDrillOK {
		t.Fatalf("verification/drill status = %+v", status)
	}

	failurePath := filepath.Join(directory, "failure.json")
	mustRun(t, "status", "-file", failurePath, "-set", "failure", "-error", "disk full", "-duration", "2s")
	failed, err := backup.ReadStatus(failurePath)
	if err != nil {
		t.Fatalf("ReadStatus failure: %v", err)
	}
	if failed.FailuresTotal != 1 || failed.ConsecutiveFailures != 1 || failed.LastError != "disk full" {
		t.Fatalf("failure status = %+v", failed)
	}
}

func TestBackupctlRetention(t *testing.T) {
	directory := t.TempDir()
	staging := filepath.Join(directory, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatalf("create staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "metadata.txt"), []byte("data"), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	first := filepath.Join(directory, "spm-backup-first.tar")
	mustRun(t, "pack", "-root", staging, "-output", first)
	time.Sleep(20 * time.Millisecond)
	second := filepath.Join(directory, "spm-backup-second.tar")
	mustRun(t, "pack", "-root", staging, "-output", second)

	stdout := mustRun(t, "retention", "-directory", directory, "-keep-last", "1", "-keep-days", "0")
	if !strings.Contains(stdout, "would remove "+first) || !strings.Contains(stdout, "expired: 1") {
		t.Fatalf("retention dry-run output = %s", stdout)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("dry-run removed a bundle: %v", err)
	}

	mustRun(t, "retention", "-directory", directory, "-keep-last", "1", "-keep-days", "0", "-apply")
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("first bundle still exists: %v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("newest bundle was removed: %v", err)
	}
}

func TestBackupctlRejectsOutputInsideStaging(t *testing.T) {
	staging := t.TempDir()
	if err := os.WriteFile(filepath.Join(staging, "file.txt"), []byte("data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	output := filepath.Join(staging, "bundle.tar")
	_, _, err := runCommand(t, "pack", "-root", staging, "-output", output)
	if err == nil || !strings.Contains(err.Error(), "outside the staging root") {
		t.Fatalf("pack error = %v", err)
	}
}

func TestBackupctlStatusValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	if _, _, err := runCommand(t, "status", "-file", path, "-set", "failure"); err == nil {
		t.Fatal("status accepted a failure without -error")
	}
	if _, _, err := runCommand(t, "status", "-file", path, "-set", "unknown"); err == nil {
		t.Fatal("status accepted an unknown -set value")
	}
	if _, _, err := runCommand(t, "retention", "-directory", path, "-keep-last", "-1"); err == nil {
		t.Fatal("retention accepted a negative keep-last")
	}
}

func TestBackupctlHelpAndUnknownCommand(t *testing.T) {
	stdout, _, err := runCommand(t, "help")
	if err != nil || !strings.Contains(stdout, "backupctl manages SPM backup bundles") {
		t.Fatalf("help = %q, err=%v", stdout, err)
	}
	if _, _, err := runCommand(t, "unknown"); err == nil {
		t.Fatal("unknown command was accepted")
	}
	if _, _, err := runCommand(t, "pack", "-h"); err != nil {
		t.Fatalf("pack -h returned an error: %v", err)
	}
}
