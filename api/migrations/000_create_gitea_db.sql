-- Creates the Gitea database if it doesn't exist.
-- This runs before the SPM migrations since it's alphabetically first.
SELECT 'CREATE DATABASE git'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'git')\gexec
