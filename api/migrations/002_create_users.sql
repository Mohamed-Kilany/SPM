-- Migration: 002_create_users
-- Creates the users table with role check constraint and invalidated_at support.
-- Requirements: 9.5, 9.6, 9.9

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username        VARCHAR(100) NOT NULL UNIQUE,
    email           VARCHAR(255) NOT NULL UNIQUE,
    password_hash   VARCHAR(255) NOT NULL,
    role            VARCHAR(20) NOT NULL CHECK (role IN ('admin', 'member', 'viewer')),
    invalidated_at  TIMESTAMPTZ,  -- set on password change or deactivation
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
