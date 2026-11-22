-- Migration: 023_auth_sessions
-- Persistent, revocable sessions with rotating refresh tokens.
--
-- One auth_sessions row is one device/browser sign-in. The row holds the
-- current access-token JTI and the SHA-256 hash of the current refresh token.
-- Every completed refresh moves the previous refresh hash into
-- auth_session_rotations; presenting a rotated hash again is treated as token
-- theft and revokes the whole session.

CREATE TABLE IF NOT EXISTS auth_sessions (
    id                   UUID PRIMARY KEY,
    user_id              UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    current_jti          UUID NOT NULL,
    current_refresh_hash VARCHAR(64) NOT NULL,
    ip                   INET,
    user_agent           VARCHAR(1024) NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    idle_expires_at      TIMESTAMPTZ NOT NULL,
    expires_at           TIMESTAMPTZ NOT NULL,
    revoked_at           TIMESTAMPTZ,
    revoke_reason        VARCHAR(100) NOT NULL DEFAULT '',
    CONSTRAINT auth_sessions_idle_within_absolute CHECK (idle_expires_at <= expires_at),
    CONSTRAINT auth_sessions_expiry_after_creation CHECK (expires_at > created_at),
    CONSTRAINT auth_sessions_refresh_hash_sha256 CHECK (current_refresh_hash ~ '^[0-9a-f]{64}$')
);

CREATE UNIQUE INDEX IF NOT EXISTS auth_sessions_refresh_hash_idx
    ON auth_sessions (current_refresh_hash);
CREATE INDEX IF NOT EXISTS auth_sessions_user_active_idx
    ON auth_sessions (user_id, last_seen_at DESC)
    WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS auth_sessions_expiry_idx
    ON auth_sessions (expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS auth_session_rotations (
    id           UUID PRIMARY KEY,
    session_id   UUID NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
    refresh_hash VARCHAR(64) NOT NULL,
    jti          UUID NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    rotated_at   TIMESTAMPTZ,
    reused_at    TIMESTAMPTZ,
    CONSTRAINT auth_session_rotations_hash_sha256 CHECK (refresh_hash ~ '^[0-9a-f]{64}$')
);

CREATE UNIQUE INDEX IF NOT EXISTS auth_session_rotations_hash_idx
    ON auth_session_rotations (refresh_hash);
CREATE INDEX IF NOT EXISTS auth_session_rotations_session_idx
    ON auth_session_rotations (session_id, created_at DESC);

COMMENT ON TABLE auth_sessions IS
    'Active sign-ins. Revoking a row blocks its access token and closes its SSE stream.';
COMMENT ON TABLE auth_session_rotations IS
    'Previously issued refresh-token hashes used to detect rotation reuse.';
