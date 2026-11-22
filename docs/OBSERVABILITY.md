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
| `spm_authz_invalidations_total` | counter | `scope`, `reason` | Published token/project/session invalidations (`user`/`project`/`session`, fixed reasons) |

### Sessions

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `spm_auth_sessions_active` | gauge | - | Sessions that can currently authenticate |
| `spm_auth_session_events_total` | counter | `event` | Session lifecycle events |

`event` is one of `created`, `refreshed`, `revoked`, `revoke_all`, `expired`,
`session_limit` or `reuse_detected`. `revoke_all` counts a bulk revocation pass
(password change, account deactivation, admin update), not the number of
sessions it revoked. `session_limit` means the per-user cap evicted older
sessions; a sustained rate points at a client that signs in on every launch or a
cap that is too low. `reuse_detected` means an already rotated refresh token was
presented again and the whole family was revoked. Treat any sustained reuse rate
as a security incident and correlate it with `auth.refresh` audit events.

```promql
sum(rate(spm_auth_session_events_total{event="reuse_detected"}[15m])) > 0
```

The session model and client refresh flow are documented in
[`SESSIONS.md`](SESSIONS.md).

### Planning

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `spm_planning_conflicts_total` | counter | `resource` | Rejected stale writes on revisioned planning resources |
| `spm_board_move_failures_total` | counter | `reason` | Rejected or failed board moves by bounded reason |

`resource` is a fixed lowercase name (`prd`, `epic`, `feature`, `work item`, `test case`,
`sprint`, `release`), never an ID. A sustained conflict rate means clients hold
stale revisions; investigate the UI/API snapshot path before raising it. Revision and ETag
semantics are in [`docs/DATA-MODEL.md`](DATA-MODEL.md).

`reason` for board moves is one of `conflict`, `wip`, `invalid_transition`,
`invalid_request`, `not_found` or `error`. The counter increments once for each
rejected `PATCH /projects/{id}/work-items/{itemId}/status`; successful moves are
covered by the HTTP request metric and the `work_item.move` audit event. A move
that fails after the client applied it optimistically is retried by the board,
so a short burst of `conflict` or `error` is expected during a stale-snapshot
recovery. Alert on a sustained failure share or on `wip` rejections rising after
a policy change:

```promql
sum(rate(spm_board_move_failures_total[5m]))
/
clamp_min(
  sum(rate(spm_http_requests_total{
    method="PATCH",
    route="/api/v1/projects/:id/work-items/:itemId/status"
  }[5m])),
  1
) > 0.25
```

Move audit events and the UI retry contract are described in
[`docs/BOARD-VIEWS.md`](BOARD-VIEWS.md#8-moving-work-items).

### Test execution

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `spm_test_runs_completed_total` | counter | `outcome` (`passed`, `failed`, `incomplete`) | Runs that transitioned to `completed` |

The counter increments exactly once per run, whether completion came from the
last case result or from `POST .../test-runs/{runId}/finish`. Repeating finish
returns `already_completed: true` without a second increment. `incomplete`
means the run was force-finished with `not_run` cases remaining.

The pass rate is computed per run from cases: `passed / (passed + failed)`,
excluding blocked, skipped and `not_run`. The run-level outcome counter supports
a coarse trend of how often completed runs are fully green:

```promql
sum(rate(spm_test_runs_completed_total{outcome="passed"}[7d]))
/
clamp_min(sum(rate(spm_test_runs_completed_total[7d])), 1)
```

Alert on a sustained drop in the `passed` share, or on any increase in
`outcome="incomplete"` when the project expects full coverage. Finishing rules
and the outcome mapping are in
[`docs/TEST-MANAGEMENT.md`](TEST-MANAGEMENT.md#finishing-and-pass-rate).

### Audit

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `spm_audit_writes_total` | counter | `outcome` (`success`, `failure`) | Audit persistence attempts |
| `spm_audit_write_duration_seconds` | histogram | - | Audit insert latency |
| `spm_audit_chain_valid` | gauge | - | `1` when the last chain verification passed |
| `spm_audit_chain_rows_checked` | gauge | - | Rows examined by the last verification |
| `spm_audit_chain_last_verified_timestamp_seconds` | gauge | - | Time of the last verification |
| `spm_audit_partitions_pruned_total` | counter | - | Expired audit partitions dropped |

A failed audit write is a security signal. Alert on any
`spm_audit_writes_total{outcome="failure"}` increment and on
`spm_audit_chain_valid == 0`. The audit schema, retention and incident runbook
are in [`docs/AUDIT.md`](AUDIT.md).

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

# Audit writes failing, or a broken audit chain
sum(rate(spm_audit_writes_total{outcome="failure"}[5m])) > 0
spm_audit_chain_valid == 0
```

## 6. Deliberately deferred

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
