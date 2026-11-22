# Sessions and device management

SPM issues short-lived access tokens and long-lived, rotating refresh tokens.
Every sign-in is a server-side session that the owner can list and revoke. This
document is the source of truth for the session model (`GOV-09`), the API
contract and the client refresh flow.

## 1. Model

| Object | Storage | Meaning |
|---|---|---|
| Session family | `auth_sessions` | One browser/device sign-in. Holds the current JTI, the current refresh-token hash, IP, user agent and expiry windows. |
| Rotation history | `auth_session_rotations` | One row per consumed refresh token. Used to detect replay of an already rotated token. |
| Access token | Client only (JWT) | Signed HS256 token with `sub`, `role`, `project_ids`, `sid` and `jti`. Never stored server-side. |
| Refresh token | Client only | 32 random bytes encoded as unpadded base64url. Only its SHA-256 hex digest is stored. |

Migration `023_auth_sessions` creates both tables. Rotation rows cascade when a
session is deleted. The current and historical hashes are `VARCHAR(64)` with a
hex-format check, and both are uniquely indexed.

### Expiry windows

- **Access token** — `ACCESS_TOKEN_TTL_MINUTES` (default 15 minutes).
  `JWT_EXPIRY_HOURS` remains a fallback for deployments that have not migrated.
- **Refresh/session lifetime** — `REFRESH_TOKEN_TTL_DAYS` (default 30 days).
  This is an absolute cap; refreshing never extends it.
- **Idle timeout** — `SESSION_IDLE_TIMEOUT_MINUTES` (default 24 hours). Each
  authenticated request extends the idle window, capped by the absolute expiry.
  Last-seen writes are throttled to one per 30 seconds per session.
- **Concurrent sessions** — `SESSION_LIMIT_PER_USER` (default 20). The oldest
  sessions beyond the cap are revoked with reason `session_limit`. `0` disables
  the cap.
- **Retention** — `SESSION_RETENTION_DAYS` (default 30 days). Revoked or expired
  rows are deleted after this window; rotation history cascades.

## 2. Sign-in and refresh

`POST /api/v1/auth/login` returns:

```json
{
  "token": "<access JWT>",
  "refresh_token": "<opaque token>",
  "session_id": "<uuid>",
  "expires_in": 900,
  "user": { "...": "..." }
}
```

The UI stores the access token, refresh token and session ID in `localStorage`
(`spm_token`, `spm_refresh_token`, `spm_session_id`). The refresh token is never
placed in a URL, query string or log.

`POST /api/v1/auth/refresh` accepts `{ "refresh_token": "..." }` and returns the
same shape with a **new** access token, a **new** refresh token and the same
`session_id`. Rotation is atomic:

1. Look up the current hash with `FOR UPDATE`.
2. Reject revoked, idle-expired or absolutely expired sessions.
3. Move the old hash into `auth_session_rotations`.
4. Replace the current hash and JTI and extend the idle window.

Presenting a hash that exists only in `auth_session_rotations` is treated as
token theft: the whole session family is revoked with reason `refresh_reuse`,
the reuse is marked, and the caller receives `401 REFRESH_TOKEN_REUSE`. Every
other device signed in to that session is signed out as well.

The browser client performs a **single-flight** refresh: concurrent 401s share
one refresh request, then retry the original requests once. The Web Locks API
serializes rotation across tabs, so two tabs cannot present the same token and
trigger false reuse detection. A failed refresh due to a network or 5xx error
does not clear the local session; only a 401/403 from the refresh endpoint signs
the user out.

## 3. Revocation and enforcement

| Operation | Endpoint | Result |
|---|---|---|
| Revoke current session | `POST /api/v1/auth/logout` | `204`; also used by the sidebar sign-out |
| List active sessions | `GET /api/v1/auth/sessions` | `200` with `sessions[]` |
| Revoke one owned session | `DELETE /api/v1/auth/sessions/{sessionId}` | `204`, or `404 SESSION_NOT_FOUND` |
| Revoke every other session | `POST /api/v1/auth/sessions/revoke-others` | `200 { "revoked": n }`; current session stays active |

The list projection contains `id`, `device`, `ip`, `user_agent`, `created_at`,
`last_seen_at`, `idle_expires_at`, `expires_at` and `current`. `device` is a
conservative browser/OS label derived from the user agent.

Every authenticated request validates `sid` against `auth_sessions`:

| Condition | Response |
|---|---|
| Row active | Request proceeds; idle window is extended |
| `revoked_at` set | `401 SESSION_REVOKED` |
| Idle or absolute expiry passed, or row missing | `401 SESSION_EXPIRED` |
| Database failure | `503 SESSION_CHECK_FAILED` (fail closed) |

Tokens without `sid` (issued before this feature) skip the session check and
remain valid until they expire. New logins always include `sid`.

Revocation also publishes a NATS invalidation with `scope: "session"` and
`session_id`. Each API replica closes matching SSE streams immediately. The SSE
heartbeat (20 seconds) revalidates the session as a fallback when NATS is
unavailable.

Password changes, account deactivation and admin password/role changes revoke
all of the target user's sessions. A password change therefore signs every
device out, including the caller.

## 4. UI behaviour

- The profile page lists active sessions, marks the current one with **This
  device**, and offers per-row **Revoke** plus **Revoke other sessions** behind a
  confirmation dialog.
- Signing out calls the logout endpoint (best effort) and always clears local
  storage, so a network failure cannot strand a session in the browser.
- A `spm:token-refreshed` event keeps the Pinia auth store in sync after a
  background rotation; a `spm:unauthorized` event performs the global sign-out.
- A `storage` listener mirrors token/user changes made by another tab, so a
  sign-out or rotation propagates without a reload.

## 5. Operations

Metrics are documented in [`OBSERVABILITY.md`](OBSERVABILITY.md#sessions):

- `spm_auth_sessions_active` — sessions that can currently authenticate.
- `spm_auth_session_events_total{event}` — `created`, `refreshed`, `revoked`,
  `revoke_all`, `expired`, `session_limit`, `reuse_detected`.

A non-zero `reuse_detected` rate is a security signal: investigate the account
and the client that presented the rotated token. Cleanup runs once at startup
and then daily; it logs `sessions_cleaned` with the number of deleted rows.

Operational notes:

- Session validation depends on PostgreSQL. Keep the pool healthy; a database
  outage returns `503` rather than accepting a possibly revoked token.
- Clock skew between API replicas affects idle/absolute expiry. Run NTP.
- `auth_sessions.ip` is stored as `INET`; proxies must set `X-Forwarded-For`
  correctly for the address to be meaningful.
- Do not log refresh tokens or their hashes. Audit events reference the session
  ID, never the token.

## 6. Related documents

- [`API.md`](API.md) — generated contract and versioning policy.
- [`OBSERVABILITY.md`](OBSERVABILITY.md) — metric catalog and alerts.
- [`SECURITY.md`](../SECURITY.md) — authentication and secret-handling policy.
