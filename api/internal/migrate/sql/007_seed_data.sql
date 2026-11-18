-- Seed users. Passwords:
--   admin     -> Admin@1234
--   demo      -> Demo@1234
--   developer -> Demo@1234
--   tester    -> Demo@1234

INSERT INTO users (id, username, email, password_hash, role, created_at)
VALUES
    ('00000000-0000-0000-0000-000000000001', 'admin', 'admin@spm.local',
     '$2a$10$VXpub.4xnp4N/jAY2o4yweSTINMDx.gFUIFv6mpa2OUch/LrnXDX2', 'admin', NOW()),
    ('00000000-0000-0000-0000-000000000002', 'demo', 'demo@spm.local',
     '$2a$10$2zirQJKXqhfJVonnmhJFLeI.SzeSsIYumbD7Zrny44HcbzY9tE3y6', 'member', NOW()),
    ('00000000-0000-0000-0000-000000000003', 'developer', 'developer@spm.local',
     '$2a$10$iIiNUzQ2kOcwwdUbhNmov.zjH.4tMwdRM7lCypZFgKHD0pvgm1SKi', 'member', NOW()),
    ('00000000-0000-0000-0000-000000000004', 'tester', 'tester@spm.local',
     '$2a$10$3KFSH1le6cqgFjDe.n7a.ORt3bfG59VXWu6YDIqCHubXz0OsNl.eq', 'viewer', NOW())
ON CONFLICT (username) DO NOTHING;
