package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StatusSchemaVersion is the status file format consumed by the API metrics
// collector and the backup scripts.
const StatusSchemaVersion = 1

// Status is the last-known state of the backup job. It is written atomically
// after every attempt so the API can expose it without talking to the backup
// host.
type Status struct {
	SchemaVersion  int        `json:"schema_version"`
	LastAttemptAt  *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt  *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt  *time.Time `json:"last_failure_at,omitempty"`
	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`
	LastDrillAt    *time.Time `json:"last_drill_at,omitempty"`
	LastDrillOK    bool       `json:"last_drill_ok"`

	LastError      string `json:"last_error,omitempty"`
	LastBundle     string `json:"last_bundle,omitempty"`
	LastSHA256     string `json:"last_sha256,omitempty"`
	LastSizeBytes  int64  `json:"last_size_bytes"`
	LastDurationMS int64  `json:"last_duration_ms"`
	Encrypted      bool   `json:"encrypted"`

	SuccessesTotal      int64 `json:"successes_total"`
	FailuresTotal       int64 `json:"failures_total"`
	ConsecutiveFailures int   `json:"consecutive_failures"`
}

// ReadStatus loads the status file. A missing file is reported with the
// underlying os.ErrNotExist wrapped, so callers can distinguish "never run"
// from "unreadable".
func ReadStatus(path string) (Status, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Status{}, fmt.Errorf("backup: read status %s: %w", path, err)
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		return Status{}, fmt.Errorf("backup: decode status %s: %w", path, err)
	}
	if status.SchemaVersion != StatusSchemaVersion {
		return Status{}, fmt.Errorf("backup: unsupported status schema %d", status.SchemaVersion)
	}
	return status, nil
}

// WriteStatus writes the status file atomically. The parent directory is
// created with 0700 and the file with 0600.
func WriteStatus(path string, status Status) error {
	status.SchemaVersion = StatusSchemaVersion
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: encode status: %w", err)
	}
	data = append(data, '\n')
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("backup: create status directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, ".backup-status-*.tmp")
	if err != nil {
		return fmt.Errorf("backup: create status: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = os.Remove(tempName)
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("backup: chmod status: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("backup: write status: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("backup: sync status: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("backup: close status: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("backup: install status: %w", err)
	}
	return nil
}

// RecordSuccess marks a completed, verified bundle.
func (s *Status) RecordSuccess(now time.Time, bundle, sha256 string, size int64, encrypted bool, duration time.Duration) {
	now = now.UTC()
	s.LastAttemptAt = &now
	s.LastSuccessAt = &now
	s.LastError = ""
	s.LastBundle = bundle
	s.LastSHA256 = sha256
	s.LastSizeBytes = size
	s.LastDurationMS = duration.Milliseconds()
	s.Encrypted = encrypted
	s.SuccessesTotal++
	s.ConsecutiveFailures = 0
}

// RecordFailure marks a failed attempt and keeps the previous success fields.
func (s *Status) RecordFailure(now time.Time, message string, duration time.Duration) {
	now = now.UTC()
	s.LastAttemptAt = &now
	s.LastFailureAt = &now
	s.LastError = message
	s.LastDurationMS = duration.Milliseconds()
	s.FailuresTotal++
	s.ConsecutiveFailures++
}

// RecordVerified marks a successful bundle verification or restore drill.
func (s *Status) RecordVerified(now time.Time) {
	now = now.UTC()
	s.LastVerifiedAt = &now
}

// RecordDrill marks the outcome of a restore drill.
func (s *Status) RecordDrill(now time.Time, ok bool) {
	now = now.UTC()
	s.LastDrillAt = &now
	s.LastDrillOK = ok
}
