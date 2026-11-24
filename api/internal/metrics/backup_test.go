package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"spm-platform/api/internal/backup"
)

func metricValue(t *testing.T, registry *Registry, name string) float64 {
	t.Helper()
	metrics := family(t, registry, name).GetMetric()
	if len(metrics) != 1 {
		t.Fatalf("metric %q has %d samples, want 1", name, len(metrics))
	}
	if metrics[0].GetGauge() != nil {
		return metrics[0].GetGauge().GetValue()
	}
	if metrics[0].GetCounter() != nil {
		return metrics[0].GetCounter().GetValue()
	}
	t.Fatalf("metric %q is neither a gauge nor a counter", name)
	return 0
}

func TestRegisterBackupStatusExposesStatusFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup-status.json")
	success := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	status := backup.Status{}
	status.RecordSuccess(success, "spm-backup-test.tar.enc", "digest", 4096, true, 90*time.Second)
	status.RecordFailure(success.Add(time.Minute), "pg_dump failed", 5*time.Second)
	status.RecordVerified(success.Add(2 * time.Minute))
	if err := backup.WriteStatus(path, status); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	registry := newTestRegistry()
	registry.RegisterBackupStatus(path)

	if got := metricValue(t, registry, "spm_backup_status_present"); got != 1 {
		t.Errorf("status present = %v, want 1", got)
	}
	if got := metricValue(t, registry, "spm_backup_last_success_timestamp_seconds"); got != float64(success.Unix()) {
		t.Errorf("last success = %v, want %d", got, success.Unix())
	}
	if got := metricValue(t, registry, "spm_backup_last_failure_timestamp_seconds"); got != float64(success.Add(time.Minute).Unix()) {
		t.Errorf("last failure = %v", got)
	}
	if got := metricValue(t, registry, "spm_backup_last_verified_timestamp_seconds"); got != float64(success.Add(2*time.Minute).Unix()) {
		t.Errorf("last verified = %v", got)
	}
	if got := metricValue(t, registry, "spm_backup_last_duration_seconds"); got != 5 {
		t.Errorf("last duration = %v, want 5", got)
	}
	if got := metricValue(t, registry, "spm_backup_last_size_bytes"); got != 4096 {
		t.Errorf("last size = %v, want 4096", got)
	}
	if got := metricValue(t, registry, "spm_backup_encrypted"); got != 1 {
		t.Errorf("encrypted = %v, want 1", got)
	}
	if got := metricValue(t, registry, "spm_backup_successes_total"); got != 1 {
		t.Errorf("successes = %v, want 1", got)
	}
	if got := metricValue(t, registry, "spm_backup_failures_total"); got != 1 {
		t.Errorf("failures = %v, want 1", got)
	}
	if got := metricValue(t, registry, "spm_backup_consecutive_failures"); got != 1 {
		t.Errorf("consecutive failures = %v, want 1", got)
	}
	if got := metricValue(t, registry, "spm_backup_age_seconds"); got < 3500 || got > 3700 {
		t.Errorf("backup age = %v, want about 3600", got)
	}
	if got := metricValue(t, registry, "spm_backup_last_drill_ok"); got != 0 {
		t.Errorf("last drill ok = %v, want 0", got)
	}
}

func TestRegisterBackupStatusMissingFile(t *testing.T) {
	registry := newTestRegistry()
	registry.RegisterBackupStatus(filepath.Join(t.TempDir(), "missing.json"))

	if got := metricValue(t, registry, "spm_backup_status_present"); got != 0 {
		t.Errorf("status present = %v, want 0", got)
	}
	if got := metricValue(t, registry, "spm_backup_last_success_timestamp_seconds"); got != 0 {
		t.Errorf("last success = %v, want 0", got)
	}
	if got := metricValue(t, registry, "spm_backup_age_seconds"); got != 0 {
		t.Errorf("age = %v, want 0", got)
	}
	if got := metricValue(t, registry, "spm_backup_status_read_errors_total"); got != 0 {
		t.Errorf("read errors = %v, want 0 for a missing file", got)
	}
}

func gatheredValues(t *testing.T, registry *Registry) map[string]float64 {
	t.Helper()
	families, err := registry.Gatherer().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	values := make(map[string]float64, len(families))
	for _, candidate := range families {
		metrics := candidate.GetMetric()
		if len(metrics) != 1 {
			continue
		}
		if metrics[0].GetGauge() != nil {
			values[candidate.GetName()] = metrics[0].GetGauge().GetValue()
		} else if metrics[0].GetCounter() != nil {
			values[candidate.GetName()] = metrics[0].GetCounter().GetValue()
		}
	}
	return values
}

func TestRegisterBackupStatusCorruptFileCountsReadErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt status: %v", err)
	}
	registry := newTestRegistry()
	registry.RegisterBackupStatus(path)

	first := gatheredValues(t, registry)
	if got := first["spm_backup_status_present"]; got != 0 {
		t.Errorf("status present = %v, want 0", got)
	}
	if got := first["spm_backup_status_read_errors_total"]; got != 1 {
		t.Errorf("read errors = %v, want 1", got)
	}
	second := gatheredValues(t, registry)
	if got := second["spm_backup_status_read_errors_total"]; got != 2 {
		t.Errorf("read errors after second scrape = %v, want 2", got)
	}
}

func TestRegisterBackupStatusNilAndEmpty(t *testing.T) {
	var nilRegistry *Registry
	nilRegistry.RegisterBackupStatus("/tmp/anything")
	registry := newTestRegistry()
	registry.RegisterBackupStatus("   ")
	registry.RegisterBackupStatus("")

	if families, err := registry.Gatherer().Gather(); err != nil {
		t.Fatalf("gather: %v", err)
	} else {
		for _, candidate := range families {
			if candidate.GetName() == "spm_backup_status_present" {
				t.Fatal("empty path registered the backup collector")
			}
		}
	}
}
