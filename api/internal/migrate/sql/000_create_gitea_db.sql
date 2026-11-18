-- Creates the Gitea database if it does not exist (psql initdb only; the Go
-- migration runner skips this file because \gexec is a psql meta-command).

SELECT 'CREATE DATABASE git'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'git')\gexec
