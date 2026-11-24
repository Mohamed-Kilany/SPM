package backup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusMissingFile(t *testing.T) {
	_, err := ReadStatus(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadStatus error = %v, want os.ErrNotExist", err)
	}
}

func TestStatusTransitions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "status.json")
	var status Status

	first := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	status.RecordSuccess(first, "bundle.tar.enc", "abc", 1024, true, 90*time.Second)
	if status.ConsecutiveFailures != 0 || status.SuccessesTotal != 1 || status.FailuresTotal != 0 {
		t.Fatalf("first success counters = %+v", status)
	}
	if status.LastSuccessAt == nil || !status.LastSuccessAt.Equal(first) {
		t.Fatalf("last success = %v", status.LastSuccessAt)
	}
	if status.LastDurationMS != 90000 || status.LastSizeBytes != 1024 || !status.Encrypted {
		t.Fatalf("success fields = %+v", status)
	}

	if err := WriteStatus(path, status); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}
	loaded, err := ReadStatus(path)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if loaded.LastBundle != "bundle.tar.enc" || loaded.LastSHA256 != "abc" || !loaded.Encrypted {
		t.Fatalf("loaded status = %+v", loaded)
	}

	failed := first.Add(time.Hour)
	loaded.RecordFailure(failed, "pg_dump failed", 5*time.Second)
	if loaded.LastSuccessAt == nil || !loaded.LastSuccessAt.Equal(first) {
		t.Fatal("failure cleared the previous success timestamp")
	}
	if loaded.LastFailureAt == nil || !loaded.LastFailureAt.Equal(failed) {
		t.Fatalf("last failure = %v", loaded.LastFailureAt)
	}
	if loaded.ConsecutiveFailures != 1 || loaded.FailuresTotal != 1 || loaded.LastError != "pg_dump failed" {
		t.Fatalf("failure fields = %+v", loaded)
	}

	loaded.RecordFailure(failed.Add(time.Minute), "disk full", time.Second)
	if loaded.ConsecutiveFailures != 2 || loaded.FailuresTotal != 2 {
		t.Fatalf("second failure counters = %+v", loaded)
	}

	loaded.RecordSuccess(failed.Add(2*time.Minute), "new.tar", "def", 2048, false, 30*time.Second)
	if loaded.ConsecutiveFailures != 0 || loaded.SuccessesTotal != 2 || loaded.LastError != "" {
		t.Fatalf("success after failures = %+v", loaded)
	}
	if loaded.Encrypted {
		t.Fatal("encrypted flag was not updated")
	}

	verified := failed.Add(3 * time.Minute)
	loaded.RecordVerified(verified)
	loaded.RecordDrill(verified, true)
	if loaded.LastVerifiedAt == nil || !loaded.LastVerifiedAt.Equal(verified) {
		t.Fatalf("verified = %v", loaded.LastVerifiedAt)
	}
	if loaded.LastDrillAt == nil || !loaded.LastDrillOK {
		t.Fatalf("drill = %v / %t", loaded.LastDrillAt, loaded.LastDrillOK)
	}

	if err := WriteStatus(path, loaded); err != nil {
		t.Fatalf("WriteStatus second: %v", err)
	}
	final, err := ReadStatus(path)
	if err != nil {
		t.Fatalf("ReadStatus second: %v", err)
	}
	if final.SuccessesTotal != 2 || final.FailuresTotal != 2 || !final.LastDrillOK {
		t.Fatalf("final status = %+v", final)
	}
}

func TestReadStatusRejectsUnknownSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":99}`), 0o600); err != nil {
		t.Fatalf("write status: %v", err)
	}
	if _, err := ReadStatus(path); err == nil {
		t.Fatal("ReadStatus accepted an unknown schema")
	}
}
