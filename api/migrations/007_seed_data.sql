-- Seed: admin user
-- Password: Admin@1234 (bcrypt hash)
INSERT INTO users (id, username, email, password_hash, role, created_at)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'admin',
    'admin@spm.local',
    '$2a$10$VXpub.4xnp4N/jAY2o4yweSTINMDx.gFUIFv6mpa2OUch/LrnXDX2',
    'admin',
    NOW()
) ON CONFLICT (username) DO NOTHING;

-- Seed: demo member user
-- Password: Demo@1234
INSERT INTO users (id, username, email, password_hash, role, created_at)
VALUES (
    '00000000-0000-0000-0000-000000000002',
    'demo',
    'demo@spm.local',
    '$2a$10$2zirQJKXqhfJVonnmhJFLeI.SzeSsIYumbD7Zrny44HcbzY9tE3y6',
    'member',
    NOW()
) ON CONFLICT (username) DO NOTHING;

-- Seed: developer user
-- Password: Demo@1234
INSERT INTO users (id, username, email, password_hash, role, created_at)
VALUES (
    '00000000-0000-0000-0000-000000000003',
    'developer',
    'developer@spm.local',
    '$2a$10$iIiNUzQ2kOcwwdUbhNmov.zjH.4tMwdRM7lCypZFgKHD0pvgm1SKi',
    'member',
    NOW()
) ON CONFLICT (username) DO NOTHING;

-- Seed: tester user
-- Password: Demo@1234
INSERT INTO users (id, username, email, password_hash, role, created_at)
VALUES (
    '00000000-0000-0000-0000-000000000004',
    'tester',
    'tester@spm.local',
    '$2a$10$3KFSH1le6cqgFjDe.n7a.ORt3bfG59VXWu6YDIqCHubXz0OsNl.eq',
    'member',
    NOW()
) ON CONFLICT (username) DO NOTHING;
