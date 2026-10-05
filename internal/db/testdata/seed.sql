-- Made up data to look at the UI, loaded by task seed. The signing keys are
-- throwaway ones, never use them for a real product.
INSERT INTO
  products (id, name, prefix, plan, private_key)
VALUES
  (1, 'My App', 'MYAPP', 'pro', 'PflHossu9+rs1nOWDD2gw3ThJvb1gM9jsYq9gUnqqWveB95P+IIk3/JzxdK4GoUwZYH4igBn0j2fZxk2jKt5Ug=='),
  (2, 'Other App', 'OTHER', 'solo', 'qBvmEnQiTlMrvrrrr8GV0pLRPVSNj8jBwR9eI6+KNdv8fH0D2PeQsWdcLTchVFroLoOEKN5sdr1yp3TAJVefeQ==');

-- 48 licenses, a few run out, a few end soon, every sixth never expires.
WITH RECURSIVE
  n (i) AS (
    SELECT 1
    UNION ALL
    SELECT i + 1 FROM n WHERE i < 48
  )
INSERT INTO
  licenses (id, product_id, source, email, customer, plan, expires_at, created_at)
SELECT
  printf('lic_seed%04d', i),
  1 + (i % 3 = 0),
  'manual',
  printf('user%d@example.com', i),
  CASE WHEN i % 2 = 0 THEN printf('Company %d', i) ELSE '' END,
  CASE WHEN i % 3 = 0 THEN 'solo' WHEN i % 4 = 0 THEN 'team' ELSE 'pro' END,
  CASE WHEN i % 6 = 0 THEN 0 ELSE unixepoch() + (i * 9 - 60) * 86400 END,
  unixepoch() - i * 2 * 86400
FROM
  n;

-- 90 days of counts that grow, with a dip in the middle.
WITH RECURSIVE
  d (ago) AS (
    SELECT 90
    UNION ALL
    SELECT ago - 1 FROM d WHERE ago > 0
  ),
  plans (product_id, plan, size) AS (
    VALUES (1, 'pro', 20), (1, 'team', 8), (2, 'solo', 12)
  )
INSERT INTO
  snapshots (day, product_id, plan, active)
SELECT
  date('now', printf('-%d days', ago)),
  product_id,
  plan,
  max(1, size * (90 - ago) / 90 + size / 4 - (ago BETWEEN 35 AND 45) * size / 5 + abs(random()) % 3)
FROM
  d,
  plans;
