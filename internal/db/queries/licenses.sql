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
  licenses (id, product_id, source, email, customer, plan, expires_at, status, renews, created_at)
VALUES
  (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (product_id, id) DO UPDATE
SET
  email = excluded.email,
  customer = excluded.customer,
  plan = excluded.plan,
  expires_at = excluded.expires_at,
  status = excluded.status,
  renews = excluded.renews;
