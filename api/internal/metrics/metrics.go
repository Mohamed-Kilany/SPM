// Package metrics owns the Prometheus collectors and the process-wide
// recorder for the SPM API. The endpoint is deliberately served by a separate
// internal listener; the public API router never exposes /metrics.
package metrics

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// BuildInfo is the release metadata exported as spm_build_info.
type BuildInfo struct {
	Version string
	Commit  string
	BuiltAt string
}

// Registry holds every SPM collector and the isolated Prometheus registry they
// are registered on. Methods are safe to call on a nil receiver so packages
// that record metrics can stay usable in tests and command-line tools.
type Registry struct {
	gatherer *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	httpInFlight prometheus.Gauge

	dependencyRequests *prometheus.CounterVec
	dependencyDuration *prometheus.HistogramVec

	aiRequests *prometheus.CounterVec
	aiDuration *prometheus.HistogramVec

	reconcileRuns        *prometheus.CounterVec
	reconcileDuration    *prometheus.HistogramVec
	reconcileLastSuccess *prometheus.GaugeVec

	authzInvalidations *prometheus.CounterVec
	planningConflicts  *prometheus.CounterVec

	testRunCompletions *prometheus.CounterVec

	auditWrites            *prometheus.CounterVec
	auditWriteDuration     prometheus.Histogram
	auditChainValid        prometheus.Gauge
	auditChainChecked      prometheus.Gauge
	auditChainLastVerified prometheus.Gauge
	auditPartitions        prometheus.Counter
}

var defaultRegistry atomic.Pointer[Registry]

// SetDefault installs the process-wide registry used by client libraries that
// do not receive the registry through their constructors.
func SetDefault(registry *Registry) {
	defaultRegistry.Store(registry)
}

// Default returns the process-wide registry, or nil when metrics are not
// initialized. Every method on Registry tolerates nil.
func Default() *Registry {
	return defaultRegistry.Load()
}

// New creates a registry with the SPM RED metrics, build metadata and the Go
// runtime collectors.
func New(info BuildInfo) *Registry {
	registry := prometheus.NewRegistry()
	metrics := &Registry{
		gatherer: registry,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "spm_http_requests_total",
			Help: "Total HTTP requests by method, route template and status code.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "spm_http_request_duration_seconds",
			Help:    "HTTP request latency by method and route template.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		httpInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "spm_http_requests_in_flight",
			Help: "HTTP requests currently being served.",
		}),
		dependencyRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "spm_dependency_requests_total",
			Help: "Outbound dependency requests by dependency, method and status code.",
		}, []string{"dependency", "method", "status"}),
		dependencyDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "spm_dependency_request_duration_seconds",
			Help:    "Outbound dependency latency by dependency and method.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, []string{"dependency", "method"}),
		aiRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "spm_ai_requests_total",
			Help: "AI service requests by bounded operation and outcome.",
		}, []string{"operation", "outcome"}),
		aiDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "spm_ai_request_duration_seconds",
			Help:    "AI service latency by bounded operation.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, []string{"operation"}),
		reconcileRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "spm_delivery_reconcile_runs_total",
			Help: "Delivery reconciliation runs by source and result.",
		}, []string{"source", "result"}),
		reconcileDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "spm_delivery_reconcile_duration_seconds",
			Help:    "Delivery reconciliation latency by source.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		}, []string{"source"}),
		reconcileLastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "spm_delivery_reconcile_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successful delivery reconciliation by source.",
		}, []string{"source"}),
		authzInvalidations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "spm_authz_invalidations_total",
			Help: "Authorization invalidations published by scope and reason.",
		}, []string{"scope", "reason"}),
		planningConflicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "spm_planning_conflicts_total",
			Help: "Rejected optimistic-concurrency writes by planning resource.",
		}, []string{"resource"}),
		testRunCompletions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "spm_test_runs_completed_total",
			Help: "Completed test runs by outcome: passed, failed or incomplete.",
		}, []string{"outcome"}),
		auditWrites: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "spm_audit_writes_total",
			Help: "Audit log writes by outcome.",
		}, []string{"outcome"}),
		auditWriteDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "spm_audit_write_duration_seconds",
			Help:    "Audit log write latency.",
			Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		}),
		auditChainValid: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "spm_audit_chain_valid",
			Help: "1 when the last audit chain verification passed, 0 otherwise.",
		}),
		auditChainChecked: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "spm_audit_chain_rows_checked",
			Help: "Rows examined by the last audit chain verification.",
		}),
		auditChainLastVerified: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "spm_audit_chain_last_verified_timestamp_seconds",
			Help: "Unix timestamp of the last audit chain verification.",
		}),
		auditPartitions: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "spm_audit_partitions_pruned_total",
			Help: "Expired audit partitions removed by the audited retention job.",
		}),
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "spm_build_info",
		Help: "Build metadata for the running API process; the value is always 1.",
	}, []string{"component", "version", "commit", "built_at"})
	buildInfo.WithLabelValues(
		"api",
		valueOr(info.Version, "dev"),
		valueOr(info.Commit, "none"),
		valueOr(info.BuiltAt, "unknown"),
	).Set(1)

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
		metrics.httpRequests,
		metrics.httpDuration,
		metrics.httpInFlight,
		metrics.dependencyRequests,
		metrics.dependencyDuration,
		metrics.aiRequests,
		metrics.aiDuration,
		metrics.reconcileRuns,
		metrics.reconcileDuration,
		metrics.reconcileLastSuccess,
		metrics.authzInvalidations,
		metrics.planningConflicts,
		metrics.testRunCompletions,
		metrics.auditWrites,
		metrics.auditWriteDuration,
		metrics.auditChainValid,
		metrics.auditChainChecked,
		metrics.auditChainLastVerified,
		metrics.auditPartitions,
	)
	return metrics
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// HTTPMiddleware records the RED metrics. The route label is Gin's registered
// template, never the raw request path, so resource IDs cannot explode metric
// cardinality or leak into the monitoring system.
func (r *Registry) HTTPMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if r == nil {
			c.Next()
			return
		}
		started := time.Now()
		r.httpInFlight.Inc()
		c.Next()
		r.httpInFlight.Dec()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		method := c.Request.Method
		r.httpRequests.WithLabelValues(method, route, strconv.Itoa(c.Writer.Status())).Inc()
		r.httpDuration.WithLabelValues(method, route).Observe(time.Since(started).Seconds())
	}
}

// ObserveDependency records one outbound request to an external system.
func (r *Registry) ObserveDependency(dependency, method, status string, elapsed time.Duration) {
	if r == nil {
		return
	}
	r.dependencyRequests.WithLabelValues(dependency, method, status).Inc()
	r.dependencyDuration.WithLabelValues(dependency, method).Observe(elapsed.Seconds())
}

// ObserveAI records one AI service call. The operation is the fixed endpoint
// path (/semantic-search, /ask or /insights), not a user-supplied value.
func (r *Registry) ObserveAI(operation string, elapsed time.Duration, err error) {
	if r == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	r.aiRequests.WithLabelValues(operation, outcome).Inc()
	r.aiDuration.WithLabelValues(operation).Observe(elapsed.Seconds())
}

// ObserveReconcile records one delivery reconciliation run. Failures still
// count as runs; only a success advances the last-success timestamp.
func (r *Registry) ObserveReconcile(source string, elapsed time.Duration, err error) {
	if r == nil {
		return
	}
	result := "success"
	if err != nil {
		result = "error"
	}
	r.reconcileRuns.WithLabelValues(source, result).Inc()
	r.reconcileDuration.WithLabelValues(source).Observe(elapsed.Seconds())
	if err == nil {
		r.reconcileLastSuccess.WithLabelValues(source).SetToCurrentTime()
	}
}

// ObserveAuthzInvalidation records one published authorization invalidation.
// Both labels are bounded enumerations chosen by the caller, never user input.
func (r *Registry) ObserveAuthzInvalidation(scope, reason string) {
	if r == nil {
		return
	}
	r.authzInvalidations.WithLabelValues(scope, reason).Inc()
}

// ObservePlanningConflict records a rejected stale write. The resource label
// is a bounded enumeration supplied by the handler, never user input.
func (r *Registry) ObservePlanningConflict(resource string) {
	if r == nil {
		return
	}
	r.planningConflicts.WithLabelValues(resource).Inc()
}

// ObserveTestRunCompleted records one run that transitioned to completed. The
// outcome label is a bounded enumeration (passed, failed or incomplete).
func (r *Registry) ObserveTestRunCompleted(outcome string) {
	if r == nil {
		return
	}
	switch outcome {
	case "passed", "failed":
	default:
		outcome = "incomplete"
	}
	r.testRunCompletions.WithLabelValues(outcome).Inc()
}

// ObserveAuditWrite records one audit persistence attempt.
func (r *Registry) ObserveAuditWrite(elapsed time.Duration, err error) {
	if r == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "failure"
	}
	r.auditWrites.WithLabelValues(outcome).Inc()
	r.auditWriteDuration.Observe(elapsed.Seconds())
}

// ObserveAuditVerification records the outcome of a chain verification.
func (r *Registry) ObserveAuditVerification(ok bool) {
	if r == nil {
		return
	}
	value := 0.0
	if ok {
		value = 1
	}
	r.auditChainValid.Set(value)
	r.auditChainLastVerified.SetToCurrentTime()
}

// ObserveAuditChecked records how many rows a verification examined.
func (r *Registry) ObserveAuditChecked(rows int64) {
	if r == nil {
		return
	}
	r.auditChainChecked.Set(float64(rows))
}

// ObserveAuditPruned records the number of expired partitions dropped.
func (r *Registry) ObserveAuditPruned(partitions int) {
	if r == nil || partitions <= 0 {
		return
	}
	r.auditPartitions.Add(float64(partitions))
}

// RegisterPool exports pgx pool saturation and acquire statistics.
func (r *Registry) RegisterPool(pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	states := map[string]func(*pgxpool.Stat) float64{
		"acquired": func(stat *pgxpool.Stat) float64 { return float64(stat.AcquiredConns()) },
		"idle":     func(stat *pgxpool.Stat) float64 { return float64(stat.IdleConns()) },
		"total":    func(stat *pgxpool.Stat) float64 { return float64(stat.TotalConns()) },
		"max":      func(stat *pgxpool.Stat) float64 { return float64(stat.MaxConns()) },
	}
	for state, read := range states {
		read := read
		state := state
		r.gatherer.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "spm_db_pool_connections",
			Help:        "PostgreSQL pool connections by state.",
			ConstLabels: prometheus.Labels{"state": state},
		}, func() float64 { return read(pool.Stat()) }))
	}

	r.gatherer.MustRegister(
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "spm_db_pool_acquires_total",
			Help: "Total PostgreSQL pool acquisitions.",
		}, func() float64 { return float64(pool.Stat().AcquireCount()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "spm_db_pool_empty_acquires_total",
			Help: "PostgreSQL pool acquisitions that had to wait for a connection.",
		}, func() float64 { return float64(pool.Stat().EmptyAcquireCount()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "spm_db_pool_canceled_acquires_total",
			Help: "PostgreSQL pool acquisitions canceled by the caller.",
		}, func() float64 { return float64(pool.Stat().CanceledAcquireCount()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "spm_db_pool_new_connections_total",
			Help: "PostgreSQL pool connections opened since process start.",
		}, func() float64 { return float64(pool.Stat().NewConnsCount()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "spm_db_pool_acquire_duration_seconds_total",
			Help: "Cumulative PostgreSQL pool acquisition time in seconds.",
		}, func() float64 { return pool.Stat().AcquireDuration().Seconds() }),
	)
}

// RegisterNATS exports connection and message counters.
func (r *Registry) RegisterNATS(conn *nats.Conn) {
	if r == nil || conn == nil {
		return
	}
	r.gatherer.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "spm_nats_connected",
			Help: "Whether the API has an active NATS connection (1) or not (0).",
		}, func() float64 {
			if conn.IsConnected() {
				return 1
			}
			return 0
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "spm_nats_reconnecting",
			Help: "Whether the NATS client is reconnecting (1) or not (0).",
		}, func() float64 {
			if conn.IsReconnecting() {
				return 1
			}
			return 0
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "spm_nats_subscriptions",
			Help: "Active NATS subscriptions held by the API process.",
		}, func() float64 { return float64(conn.NumSubscriptions()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "spm_nats_reconnects_total",
			Help: "NATS reconnect attempts since process start.",
		}, func() float64 { return float64(conn.Stats().Reconnects) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "spm_nats_messages_in_total",
			Help: "NATS messages received since process start.",
		}, func() float64 { return float64(conn.Stats().InMsgs) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "spm_nats_messages_out_total",
			Help: "NATS messages published since process start.",
		}, func() float64 { return float64(conn.Stats().OutMsgs) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "spm_nats_bytes_in_total",
			Help: "NATS bytes received since process start.",
		}, func() float64 { return float64(conn.Stats().InBytes) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "spm_nats_bytes_out_total",
			Help: "NATS bytes published since process start.",
		}, func() float64 { return float64(conn.Stats().OutBytes) }),
	)
}

// RegisterSSE exposes the number of live SSE connections.
func (r *Registry) RegisterSSE(active func() int) {
	if r == nil || active == nil {
		return
	}
	r.gatherer.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "spm_sse_connections_active",
		Help: "SSE connections currently subscribed to project updates.",
	}, func() float64 { return float64(active()) }))
}

// Handler returns the scrape handler. A non-empty token requires
// "Authorization: Bearer <token>" before any metrics are exposed.
func (r *Registry) Handler(token string) http.Handler {
	if r == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "metrics are not initialized", http.StatusServiceUnavailable)
		})
	}
	handler := promhttp.HandlerFor(r.gatherer, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		ErrorHandling:     promhttp.HTTPErrorOnError,
	})
	if token == "" {
		return handler
	}
	expected := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		presented := []byte(strings.TrimSpace(req.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(presented, expected) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, req)
	})
}

// Gatherer exposes the underlying registry for scrape tests and future
// exporters. It is nil-safe.
func (r *Registry) Gatherer() prometheus.Gatherer {
	if r == nil {
		return prometheus.NewRegistry()
	}
	return r.gatherer
}

type instrumentedTransport struct {
	dependency string
	base       http.RoundTripper
}

func (t *instrumentedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	status := "error"
	if resp != nil {
		status = strconv.Itoa(resp.StatusCode)
	}
	Default().ObserveDependency(t.dependency, req.Method, status, time.Since(started))
	return resp, err
}

// InstrumentTransport wraps an HTTP transport with dependency metrics. The
// dependency name is fixed by the caller; no URL path is used as a label.
func InstrumentTransport(dependency string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &instrumentedTransport{dependency: dependency, base: base}
}
