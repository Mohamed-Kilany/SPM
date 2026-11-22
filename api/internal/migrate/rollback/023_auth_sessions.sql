-- Rollback companion for 023_auth_sessions.
DROP TABLE IF EXISTS auth_session_rotations;
DROP TABLE IF EXISTS auth_sessions;
