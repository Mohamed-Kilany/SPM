-- Migration: 011_seed_tester_viewer
-- The seeded tester account demonstrates read-only access. 007 created it
-- before the role was finalised; update installations that already ran 007.
-- Only the untouched seed value is corrected, so operator changes are kept.

UPDATE users
SET role = 'viewer'
WHERE username = 'tester'
  AND role = 'member';
