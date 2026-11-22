# Audit log

SPM stores security and privileged actions in a single append-only, hash-chained
audit log. The database owns the chain; application code can only append. This
document is the operational contract for schema owners, API changes, incident
response and retention.

## 1. Guarantees

- **Append-only.** `UPDATE`, `DELETE` and `TRUNCATE` on `audit_events` raise
  `42501` from database triggers. There is no application path that mutates a
  stored row.
- **Tamper-evident.** Every row stores `prev_hash` and
  `sha256(prev_hash || '|' || payload)`. The trigger computes both values under
  an advisory lock, so concurrent writers cannot fork the chain.
- **Partitioned retention.** Rows are partitioned by month and expired months
  are dropped as whole partitions. A drop writes its own
  `audit.retention.partition_dropped` event and records the dropped partition's
  last hash in `audit_chain_anchors`, so verification continues across the
  retention boundary.
- **Redacted at write time.** Sensitive keys never reach PostgreSQL as plain
  text; the writer masks them before the insert.
- **Fail-open for the business request, fail-visible for security.** A failed
  audit write never rolls back the action that produced it. It logs
  `audit_write_failed` and increments
  `spm_audit_writes_total{outcome="failure"}`, which is the alerting signal.

## 2. Schema

Migration: `api/internal/migrate/sql/015_audit_log.sql`.

| Object | Purpose |
|---|---|
| `audit_events` | Monthly range partitions keyed by `(occurred_at, id)` |
| `audit_partitions` | Registry of created and dropped partitions |
| `audit_chain_anchors` | Last hash of each partition removed by retention |
| `audit_events_hash_chain()` | Computes `payload`, `prev_hash` and `hash` |
| `audit_events_deny_mutation()` | Rejects update, delete and truncate |
| `audit_ensure_partitions(months)` | Creates monthly partitions ahead of time |
| `audit_prune(retention_days)` | Drops fully expired partitions through the audited path |
| `audit_drop_partition(name)` | Audited single-partition drop and anchor write |

Stored columns include actor, actor role, action, target, project, result,
reason, IP, user agent, request ID, `before_state`, `after_state`, `payload`,
`prev_hash` and `hash`. The API names the state fields `before` and `after`;
`before`/`after` are reserved SQL words, so the columns use the `_state` suffix.

The migration creates the current month plus 24 future months. The API calls
`audit_ensure_partitions(3)` at startup and daily, so a running deployment always
has a writable partition. There is deliberately no default partition: a missing
partition is a loud failure, not a silent route to an unpartitioned table.

Recommended database permissions:

```sql
GRANT INSERT, SELECT ON audit_events TO spm_api;
REVOKE UPDATE, DELETE, TRUNCATE ON audit_events FROM spm_api;
GRANT SELECT ON audit_partitions, audit_chain_anchors TO spm_api;
-- Split-role deployments only. Never grant audit_drop_partition(); audit_prune()
-- calls it as its SECURITY DEFINER owner.
GRANT EXECUTE ON FUNCTION audit_ensure_partitions(INT), audit_prune(INT) TO spm_api;
```

Triggers enforce the same rule even for the table owner. `audit_drop_partition`
is revoked from `PUBLIC`; only the owning role (or the audited `audit_prune`
entry point) can drop a partition.

## 3. Hash chain

The trigger serializes writers with
`pg_advisory_xact_lock(hashtext('spm.audit_events.chain'))`, reads the newest
`hash`, then writes:

```text
payload  = canonical JSONB object of the row's audited fields
prev_hash = previous row's hash, or '' for the first retained row
hash      = hex(sha256(prev_hash || '|' || payload::text))
```

`GET /api/v1/admin/audit/verify` walks the retained log in
`(occurred_at, id)` order and reports the first failure:

| Cause | Meaning |
|---|---|
| `missing_anchor` | The first retained row points at a pruned hash with no recorded anchor |
| `broken_link` | A row's `prev_hash` does not match the previous row's `hash` |
| `hash_mismatch` | The row payload no longer hashes to its stored `hash` |

The verifier uses constant-time comparison and returns
`{"ok":true,"checked":N,"checked_at":"..."}` when the chain is intact. It runs
once at startup and then daily from the maintenance loop, and it also runs on
demand through the admin endpoint; every run refreshes the chain gauges.

## 4. Retention

| Variable | Default | Behaviour |
|---|---|---|
| `AUDIT_RETENTION_DAYS` | `365` | Whole partitions whose `ends_at` is older than the cutoff are dropped |
| `AUDIT_RETENTION_DAYS=0` | - | Disables pruning; partitions are still extended |

Maintenance runs once at startup and then every 24 hours. Pruning is
partition-only; a partially expired month is retained until the whole month is
outside the window. Do not run `DROP TABLE audit_events_YYYYMM` manually: it
bypasses the audit event and the chain anchor. Use `audit_prune()` or the API's
maintenance loop.

## 5. Redaction

The writer walks maps and slices and replaces values whose normalized key
contains any of `password`, `passwd`, `secret`, `token`, `authorization`,
`cookie`, `credential`, `privatekey`, `apikey` or `sessionid` with
`[REDACTED]`. Strings longer than 1024 runes are truncated with
`...[truncated]`. Callers must still avoid placing secrets in unrelated fields;
redaction is defence in depth, not permission to log credentials.

Covered events never include passwords, password hashes, JWTs, webhook tokens or
stream tickets. Password changes record `password_changed: true` instead.

## 6. Event catalog

| Action | Producer | Notes |
|---|---|---|
| `auth.login` | `internal/auth` | `success`, `failure` or `denied`; unknown users are not resolved to an ID |
| `auth.password.change` | `internal/auth` | Never stores password material |
| `auth.stream_ticket.issue` | `internal/auth` | Stream ticket value is not stored |
| `admin.user.create` | `internal/auth` | After state excludes the password |
| `admin.user.update` | `internal/auth` | Before/after include role, active state and profile fields |
| `project.create` | `internal/project` | Records name and repository name |
| `project.update` | `internal/workspace` | Records only supplied fields |
| `project.settings.update` | `internal/workspace` | Before/after settings document |
| `project.member.add` | `internal/workspace` | Includes ownership transfer flag |
| `project.member.update` | `internal/workspace` | Before/after role |
| `project.member.remove` | `internal/workspace` | Before state includes the removed role |
| `work_item.move` | `internal/workspace` | Before/after status for every successful board or drawer status change |
| `repository.create` | `internal/workspace` | Webhook token is never recorded |
| `branch.create` | `internal/workspace` | - |
| `pull_request.create` | `internal/workspace` | - |
| `pull_request.merge` | `internal/workspace` | Merge commit SHA |
| `environment.create` | `internal/workspace` | Type and name |
| `deployment.create` | `internal/workspace` | Environment, status, version, release |
| `deployment.status` | `internal/workspace` | Status and health status |
| `deployment.rollback` | `internal/workspace` | Reason, failed deployment and previous deployment |
| `deployment.observed` | `internal/webhook` | Argo CD observation; `failure` when health is Degraded |
| `audit.retention.partition_dropped` | database | Rows, window and last hash of the dropped partition |
| `audit.exported` | `internal/audit` | Admin export with the active filter summary |

## 7. API

All routes require a global `admin` role and are absent from project-scoped
capability checks.

| Route | Purpose |
|---|---|
| `GET /api/v1/admin/audit/events` | List with filters and cursor pagination |
| `GET /api/v1/admin/audit/events/{auditId}` | Event detail |
| `GET /api/v1/admin/audit/export?format=csv\|json` | Export up to 50,000 rows |
| `GET /api/v1/admin/audit/verify` | Walk and verify the retained chain |

Filters: `action` (prefix), `actor_id`, `project_id`, `result`, `from`, `to`,
`cursor`, `limit` (1-200, default 50). `from` accepts RFC3339 or `YYYY-MM-DD`;
`to` accepts RFC3339 or `YYYY-MM-DD` and is inclusive of that whole day. Results
are newest first. The cursor is opaque and keyset-based, so inserts do not skip
or duplicate rows between pages.

CSV export prefixes cells that begin with `=`, `+`, `-`, `@`, tab or carriage
return with an apostrophe to prevent spreadsheet formula injection. Exports set
`Content-Disposition: attachment` and `X-Content-Type-Options: nosniff`.

## 8. Telemetry and alerts

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `spm_audit_writes_total` | counter | `outcome` (`success`, `failure`) | Audit persistence attempts |
| `spm_audit_write_duration_seconds` | histogram | - | Audit insert latency |
| `spm_audit_chain_valid` | gauge | - | `1` when the last verification passed |
| `spm_audit_chain_rows_checked` | gauge | - | Rows examined by the last verification |
| `spm_audit_chain_last_verified_timestamp_seconds` | gauge | - | Time of the last verification |
| `spm_audit_partitions_pruned_total` | counter | - | Expired partitions dropped |

Minimum alerts:

```promql
# A failed audit write is a security event, not just an application error.
sum(rate(spm_audit_writes_total{outcome="failure"}[5m])) > 0

# The chain was checked and found broken.
spm_audit_chain_valid == 0

# No verification has completed in 26 hours (daily maintenance is overdue).
time() - spm_audit_chain_last_verified_timestamp_seconds > 93600
```

Run verification after every deployment that changes audit schema, triggers or
retention, and on demand during incident response.

## 9. Incident response

1. Capture `GET /api/v1/admin/audit/verify`, the failing event ID and its
   `payload`, `prev_hash` and `hash`.
2. Snapshot the affected partitions before any operational action. Never attempt
   to repair a row in place; triggers will reject it and a rewrite would destroy
   evidence.
3. Treat a `hash_mismatch` as unauthorized database access, not as an
   application bug. Rotate database credentials and review PostgreSQL audit logs
   and network access.
4. Treat `missing_anchor` as a retention or restore problem: confirm the
   partition registry and anchor table were restored together.
5. Record the incident and the chain evidence in the incident ticket.

## 10. Verification

- `go test ./internal/audit` covers redaction, truncation, retention parsing,
  CSV injection, cursor validation, valid/tampered/broken/anchored chains,
  filter validation, disabled-logger safety and export headers.
- `go test ./internal/server` proves the admin routes are registered and present
  in the generated OpenAPI contract.
- `go test ./internal/metrics` covers the audit write, chain and pruning series.
- The UI provides `/admin/audit` with filters, cursor pagination, detail with
  before/after JSON, CSV/JSON export and a verify-chain action.

> **Limitation:** this baseline stores the hash chain in the same PostgreSQL
> database as the log. It detects accidental or in-place tampering and broken
> retention. It does not defend against a database superuser who rewrites the
> entire chain. External chain anchoring or WORM storage is tracked by the
> backup, data-governance and certification tasks (`OPS-01`, `OPS-08`,
> `OPS-12`, `QAT-10`).
