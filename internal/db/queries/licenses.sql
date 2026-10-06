-- name: ListLicenses :many
SELECT
  *
FROM
  licenses
ORDER BY
  created_at DESC,
  id;

-- name: GetLicense :one
SELECT
  *
FROM
  licenses
WHERE
  product_id = ?
  AND id = ?;

-- name: CreateLicense :one
INSERT INTO
  licenses (id, product_id, source, email, customer, plan, expires_at, meta, note)
VALUES
  (?, ?, 'manual', ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateLicense :one
UPDATE licenses
SET
  email = ?,
  customer = ?,
  plan = ?,
  expires_at = ?,
  meta = ?,
  note = ?
WHERE
  product_id = ?
  AND id = ?
  AND source = 'manual' RETURNING *;

-- name: SetLicenseNote :one
UPDATE licenses
SET
  note = ?
WHERE
  product_id = ?
  AND id = ? RETURNING *;

-- name: DeleteLicense :execrows
DELETE FROM licenses
WHERE
  product_id = ?
  AND id = ?
  AND source = 'manual';

-- The provider owns everything but the note.
-- name: UpsertProviderLicense :exec
INSERT INTO
  licenses (id, product_id, source, email, customer, customer_gone, plan, expires_at, meta, status, renews, key, seen_at, created_at)
VALUES
  (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (product_id, id) DO UPDATE
SET
  -- A deleted customer comes back empty, the row keeps who it was.
  email = CASE excluded.email WHEN '' THEN licenses.email ELSE excluded.email END,
  customer = CASE excluded.customer WHEN '' THEN licenses.customer ELSE excluded.customer END,
  customer_gone = excluded.customer_gone,
  plan = excluded.plan,
  expires_at = excluded.expires_at,
  meta = excluded.meta,
  status = excluded.status,
  renews = excluded.renews,
  key = excluded.key,
  -- Two syncs can overlap, the later one must not lose what it saw.
  seen_at = max(licenses.seen_at, excluded.seen_at);

-- What the worker no longer lists is gone at the provider. A worker that may
-- not read one time purchases says nothing about them, so they stay.
-- name: DeleteUnseenProviderLicenses :exec
DELETE FROM licenses
WHERE
  product_id = sqlc.arg (product_id)
  AND source != 'manual'
  AND seen_at < sqlc.arg (seen_at)
  AND (
    CAST(sqlc.arg (purchases) AS BOOLEAN)
    OR id NOT LIKE 'cs\_%' ESCAPE '\'
  );

-- name: DeleteSnapshots :exec
DELETE FROM snapshots
WHERE
  day = ?;

-- Licenses that have not run out, counted per product and plan.
-- name: TakeSnapshot :exec
INSERT INTO
  snapshots (day, product_id, plan, active)
SELECT
  sqlc.arg (day),
  product_id,
  plan,
  count(*)
FROM
  licenses
WHERE
  expires_at = 0
  OR expires_at > unixepoch()
GROUP BY
  product_id,
  plan;

-- name: ListSnapshots :many
SELECT
  *
FROM
  snapshots
WHERE
  day >= ?
ORDER BY
  day,
  product_id,
  plan;
