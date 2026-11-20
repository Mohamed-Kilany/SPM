# Observability

This document defines the SPM metrics contract: where metrics are exposed, how
they are named, which labels are allowed and what is deliberately not measured
yet. It is the source of truth for dashboards and alerts built on `OBS-01` and
its follow-ups (`OBS-02`..`OBS-05`).

## 1. Endpoint and exposure

The API serves `/metrics` on a **separate internal listener**, never on the
public API port:

| Setting | Default | Meaning |
|---|---|---|
| `METRICS_ADDR` | empty (disabled) | Listen address for the metrics server, for example `0.0.0.0:9090` |
| `METRICS_TOKEN` | empty (no token) | When set, requests must send `Authorization: Bearer <token>` |

`docker-compose.yaml` sets `METRICS_ADDR=0.0.0.0:9090` for the API container but
does **not** publish port 9090. The endpoint is reachable only from the compose
network. Production must keep the port off the public edge or set
`METRICS_TOKEN`; both controls are supported.

The public API router does not register `/metrics`, so it is absent from
`docs/openapi.yaml` and from the public ingress contract. Scraping is a
cluster-internal operation.

Example Prometheus scrape configuration:

```yaml
scrape_configs:
  - job_name: spm-api
    metrics_path: /metrics
    static_configs:
      - targets: ["api_backend:9090"]
    # Only when METRICS_TOKEN is set:
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/spm-metrics-token
```

## 2. Naming conventions

- Every metric starts with `spm_`.
- Counters end in `_total`. They are monotonic for the life of the process.
- Durations are seconds, use `_seconds`, and are histograms.
- Timestamps use `_timestamp_seconds` and are Unix seconds.
- Labels are low-cardinality and bounded by code, never by user input.
- A route label is Gin's registered template (`/api/v1/projects/:id`), never a
  raw path. Unknown paths use the label `unmatched` instead of echoing the URL.
- Dependency names are fixed: `gitea`, `argocd`, `ai`.

## 3. Metric catalog

### HTTP (RED)

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `spm_http_requests_total` | counter | `method`, `route`, `status` | Requests by route template and status |
| `spm_http_request_duration_seconds` | histogram | `method`, `route` | Request latency |
| `spm_http_requests_in_flight` | gauge | - | Requests currently executing |

### Build

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `spm_build_info` | gauge (always 1) | `component`, `version`, `commit`, `built_at` | Release identity injected at build time |

`spm_build_info` is the metric form of the `/health` `build` object. It is the
join key between a metric anomaly and a released tag.

### PostgreSQL

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `spm_db_pool_connections` | gauge | `state` (`acquired`, `idle`, `total`, `max`) | Pool occupancy |
| `spm_db_pool_acquires_total` | counter | - | Pool acquisitions |
| `spm_db_pool_empty_acquires_total` | counter | - | Acquisitions that had to wait |
| `spm_db_pool_canceled_acquires_total` | counter | - | Canceled acquisitions |
| `spm_db_pool_new_connections_total` | counter | - | Connections opened |
| `spm_db_pool_acquire_duration_seconds_total` | counter | - | Cumulative acquisition time |

### NATS

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `spm_nats_connected` | gauge | - | 1 when connected, 0 otherwise |
| `spm_nats_reconnecting` | gauge | - | 1 while reconnecting |
| `spm_nats_subscriptions` | gauge | - | Active subscriptions |
| `spm_nats_reconnects_total` | counter | - | Reconnect attempts |
| `spm_nats_messages_in_total` | counter | - | Messages received |
| `spm_nats_messages_out_total` | counter | - | Messages published |
| `spm_nats_bytes_in_total` | counter | - | Bytes received |
| `spm_nats_bytes_out_total` | counter | - | Bytes published |

### SSE

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `spm_sse_connections_active` | gauge | - | Live project update streams |
| `spm_authz_invalidations_total` | counter | `scope`, `reason` | Published token/project invalidations (`user`/`project`, fixed reasons) |

### Outbound dependencies

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `spm_dependency_requests_total` | counter | `dependency`, `method`, `status` | Gitea and Argo CD calls |
| `spm_dependency_request_duration_seconds` | histogram | `dependency`, `method` | Dependency latency |
| `spm_ai_requests_total` | counter | `operation`, `outcome` | AI calls by fixed endpoint |
| `spm_ai_request_duration_seconds` | histogram | `operation` | AI latency |

The AI `operation` label is the fixed path (`/semantic-search`, `/ask`,
`/insights`). Gitea and Argo CD calls do not label the path or repository name,
because those values are unbounded.

### Delivery reconciliation

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `spm_delivery_reconcile_runs_total` | counter | `source`, `result` | Reconciliation runs (`gitea-actions`, `argocd`) |
| `spm_delivery_reconcile_duration_seconds` | histogram | `source` | Reconciliation latency |
| `spm_delivery_reconcile_last_success_timestamp_seconds` | gauge | `source` | Last successful run |

### Runtime

The registry also exports the standard Go collector (`go_*`) and process
collector (`process_*`) families. They are not renamed.

## 4. Cardinality rules

1. Never use a request path, query string, project ID, repository name, user
   ID or error message as a label.
2. Route labels come from `gin.Context.FullPath()`; unknown routes collapse to
   `unmatched`.
3. Dependency labels are constants declared in code.
4. AI operations are fixed endpoint paths.
5. New labels require a review of the maximum series count in the pull
   request. A label whose values grow with customer data is rejected.

## 5. Example queries

```promql
# Request rate by route
sum by (route) (rate(spm_http_requests_total[5m]))

# 5xx ratio
sum(rate(spm_http_requests_total{status=~"5.."}[5m]))
  / sum(rate(spm_http_requests_total[5m]))

# p95 latency for the project workspace API
histogram_quantile(0.95,
  sum by (le) (rate(spm_http_request_duration_seconds{route=~"/api/v1/projects/:id.*"}[5m])))

# Pool saturation
spm_db_pool_connections{state="acquired"} / spm_db_pool_connections{state="max"}

# Dependency error rate
sum by (dependency) (rate(spm_dependency_requests_total{status=~"error|5.."}[5m]))
  / sum by (dependency) (rate(spm_dependency_requests_total[5m]))

# Stale reconciliation
time() - spm_delivery_reconcile_last_success_timestamp_seconds > 300
```

## 6. Deliberately deferred

- **Audit write failures.** There is no audit-log subsystem yet (`GOV-04`). A
  counter that nothing increments would be a false claim, so it is not
  exported. `GOV-04` adds the counter with the audit table.
- **Tracing and exemplars.** `OBS-02` adds OpenTelemetry traces; the histograms
  here are ready for exemplars when tracing lands.
- **Dashboards and alert rules.** `OBS-03`..`OBS-05` own the packaged
  dashboards, SLOs and alert routing. This document defines the contract they
  consume.

## 7. Verification

The required gate proves:

- `/metrics` returns Prometheus text and the Go/process/build families.
- The route label is the registered template, not a raw identifier.
- 404 requests use `unmatched`.
- A configured token returns 401 without credentials and 200 with them.
- SSE, AI, dependency and reconciliation collectors record bounded labels.
