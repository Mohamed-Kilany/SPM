package metrics

import (
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"spm-platform/api/internal/backup"
)

var (
	backupStatusPresent = prometheus.NewDesc(
		"spm_backup_status_present",
		"Whether the backup status file was readable at scrape time (1) or not (0).",
		nil, nil,
	)
	backupLastAttempt = prometheus.NewDesc(
		"spm_backup_last_attempt_timestamp_seconds",
		"Unix timestamp of the last backup attempt.",
		nil, nil,
	)
	backupLastSuccess = prometheus.NewDesc(
		"spm_backup_last_success_timestamp_seconds",
		"Unix timestamp of the last successful backup.",
		nil, nil,
	)
	backupLastFailure = prometheus.NewDesc(
		"spm_backup_last_failure_timestamp_seconds",
		"Unix timestamp of the last failed backup attempt.",
		nil, nil,
	)
	backupLastVerified = prometheus.NewDesc(
		"spm_backup_last_verified_timestamp_seconds",
		"Unix timestamp of the last successful bundle verification.",
		nil, nil,
	)
	backupLastDrill = prometheus.NewDesc(
		"spm_backup_last_drill_timestamp_seconds",
		"Unix timestamp of the last restore drill.",
		nil, nil,
	)
	backupLastDrillOK = prometheus.NewDesc(
		"spm_backup_last_drill_ok",
		"Whether the last restore drill succeeded (1) or failed (0).",
		nil, nil,
	)
	backupLastDuration = prometheus.NewDesc(
		"spm_backup_last_duration_seconds",
		"Duration of the last backup attempt in seconds.",
		nil, nil,
	)
	backupLastSize = prometheus.NewDesc(
		"spm_backup_last_size_bytes",
		"Size of the last successful backup bundle in bytes.",
		nil, nil,
	)
	backupAge = prometheus.NewDesc(
		"spm_backup_age_seconds",
		"Seconds since the last successful backup, or 0 when none has succeeded.",
		nil, nil,
	)
	backupEncrypted = prometheus.NewDesc(
		"spm_backup_encrypted",
		"Whether the last successful backup bundle is encrypted (1) or not (0).",
		nil, nil,
	)
	backupSuccessesTotal = prometheus.NewDesc(
		"spm_backup_successes_total",
		"Successful backup attempts recorded in the status file.",
		nil, nil,
	)
	backupFailuresTotal = prometheus.NewDesc(
		"spm_backup_failures_total",
		"Failed backup attempts recorded in the status file.",
		nil, nil,
	)
	backupConsecutiveFailures = prometheus.NewDesc(
		"spm_backup_consecutive_failures",
		"Consecutive failed backup attempts.",
		nil, nil,
	)
	backupStatusReadErrors = prometheus.NewDesc(
		"spm_backup_status_read_errors_total",
		"Status file reads that failed for a reason other than a missing file.",
		nil, nil,
	)
)

// RegisterBackupStatus exposes the backup job status file on the metrics
// listener. The file is read on every scrape, so the API does not need to
// share a process or a database with the backup job. A missing file is
// reported as spm_backup_status_present 0 rather than breaking the scrape.
func (r *Registry) RegisterBackupStatus(path string) {
	if r == nil || strings.TrimSpace(path) == "" {
		return
	}
	r.gatherer.MustRegister(&backupStatusCollector{path: path})
}

type backupStatusCollector struct {
	path string

	mutex      sync.Mutex
	readErrors float64
}

func (c *backupStatusCollector) Describe(channel chan<- *prometheus.Desc) {
	channel <- backupStatusPresent
	channel <- backupLastAttempt
	channel <- backupLastSuccess
	channel <- backupLastFailure
	channel <- backupLastVerified
	channel <- backupLastDrill
	channel <- backupLastDrillOK
	channel <- backupLastDuration
	channel <- backupLastSize
	channel <- backupAge
	channel <- backupEncrypted
	channel <- backupSuccessesTotal
	channel <- backupFailuresTotal
	channel <- backupConsecutiveFailures
	channel <- backupStatusReadErrors
}

func (c *backupStatusCollector) Collect(channel chan<- prometheus.Metric) {
	status, err := backup.ReadStatus(c.path)
	present := 0.0
	if err == nil {
		present = 1
	} else if !errors.Is(err, os.ErrNotExist) {
		c.mutex.Lock()
		c.readErrors++
		c.mutex.Unlock()
	}

	channel <- prometheus.MustNewConstMetric(backupStatusPresent, prometheus.GaugeValue, present)
	channel <- prometheus.MustNewConstMetric(backupLastAttempt, prometheus.GaugeValue, unixTime(status.LastAttemptAt))
	channel <- prometheus.MustNewConstMetric(backupLastSuccess, prometheus.GaugeValue, unixTime(status.LastSuccessAt))
	channel <- prometheus.MustNewConstMetric(backupLastFailure, prometheus.GaugeValue, unixTime(status.LastFailureAt))
	channel <- prometheus.MustNewConstMetric(backupLastVerified, prometheus.GaugeValue, unixTime(status.LastVerifiedAt))
	channel <- prometheus.MustNewConstMetric(backupLastDrill, prometheus.GaugeValue, unixTime(status.LastDrillAt))

	drillOK := 0.0
	if status.LastDrillAt != nil && status.LastDrillOK {
		drillOK = 1
	}
	channel <- prometheus.MustNewConstMetric(backupLastDrillOK, prometheus.GaugeValue, drillOK)
	channel <- prometheus.MustNewConstMetric(backupLastDuration, prometheus.GaugeValue, float64(status.LastDurationMS)/1000)
	channel <- prometheus.MustNewConstMetric(backupLastSize, prometheus.GaugeValue, float64(status.LastSizeBytes))

	age := 0.0
	if status.LastSuccessAt != nil {
		age = time.Since(*status.LastSuccessAt).Seconds()
		if age < 0 {
			age = 0
		}
	}
	channel <- prometheus.MustNewConstMetric(backupAge, prometheus.GaugeValue, age)

	encrypted := 0.0
	if status.Encrypted {
		encrypted = 1
	}
	channel <- prometheus.MustNewConstMetric(backupEncrypted, prometheus.GaugeValue, encrypted)
	channel <- prometheus.MustNewConstMetric(backupSuccessesTotal, prometheus.CounterValue, float64(status.SuccessesTotal))
	channel <- prometheus.MustNewConstMetric(backupFailuresTotal, prometheus.CounterValue, float64(status.FailuresTotal))
	channel <- prometheus.MustNewConstMetric(backupConsecutiveFailures, prometheus.GaugeValue, float64(status.ConsecutiveFailures))

	c.mutex.Lock()
	readErrors := c.readErrors
	c.mutex.Unlock()
	channel <- prometheus.MustNewConstMetric(backupStatusReadErrors, prometheus.CounterValue, readErrors)
}

func unixTime(value *time.Time) float64 {
	if value == nil || value.IsZero() {
		return 0
	}
	return float64(value.Unix())
}
