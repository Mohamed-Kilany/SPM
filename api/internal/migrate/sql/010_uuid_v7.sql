-- Migration: 010_uuid_v7
-- PostgreSQL 18 ships uuidv7(), a time-ordered UUID generator. Entity IDs are
-- generated in Go as v7 as well; these defaults cover rows inserted directly
-- by operators or future services.

ALTER TABLE projects ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE users    ALTER COLUMN id SET DEFAULT uuidv7();

-- Make the new default explicit for databases whose tables were created by an
-- older image. uuidv7() exists only on PostgreSQL 18+; this is intentional.
