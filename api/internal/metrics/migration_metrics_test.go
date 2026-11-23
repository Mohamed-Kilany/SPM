package metrics

import (
	"testing"
)

func TestObserveMigrationDurationLabelsVersionAndStatus(t *testing.T) {
	registry := newTestRegistry()
	registry.ObserveMigrationDuration("024_ownership_transfers.sql", "success", 1.25)
	registry.ObserveMigrationDuration("024_ownership_transfers.sql", "failure", 0.5)
	registry.ObserveMigrationDuration("025_next.sql", "", 0.1)

	found := map[string]float64{}
	for _, metric := range family(t, registry, "spm_migration_duration_seconds").GetMetric() {
		key := labelValue(metric, "version") + "/" + labelValue(metric, "status")
		found[key] = float64(metric.GetHistogram().GetSampleCount())
	}
	for _, key := range []string{
		"024_ownership_transfers.sql/success",
		"024_ownership_transfers.sql/failure",
		"025_next.sql/unknown",
	} {
		if found[key] != 1 {
			t.Errorf("histogram %s sample count = %v, want 1 (all: %v)", key, found[key], found)
		}
	}
}

func TestObserveMigrationSuccessAdvancesGauge(t *testing.T) {
	registry := newTestRegistry()
	registry.ObserveMigrationSuccess()

	metric := family(t, registry, "spm_migration_last_success_timestamp_seconds").GetMetric()
	if len(metric) != 1 {
		t.Fatalf("gauge has %d samples, want 1", len(metric))
	}
	if value := metric[0].GetGauge().GetValue(); value <= 0 {
		t.Errorf("gauge = %v, want a positive Unix timestamp", value)
	}
}

func TestMigrationMetricsAreNilSafe(t *testing.T) {
	var registry *Registry
	registry.ObserveMigrationDuration("001.sql", "success", 1)
	registry.ObserveMigrationSuccess()
}
