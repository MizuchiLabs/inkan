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
  id = ?;

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
  id = ?
  AND source = 'manual' RETURNING *;

-- name: SetLicenseNote :one
UPDATE licenses
SET
  note = ?
WHERE
  id = ? RETURNING *;

-- name: DeleteLicense :execrows
DELETE FROM licenses
WHERE
  id = ?
  AND source = 'manual';

-- Stripe owns everything but the note.
-- name: UpsertStripeLicense :exec
INSERT INTO
  licenses (id, product_id, source, email, customer, plan, expires_at, stripe_status, renews, created_at)
VALUES
  (?, ?, 'stripe', ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE
SET
  email = excluded.email,
  customer = excluded.customer,
  plan = excluded.plan,
  expires_at = excluded.expires_at,
  stripe_status = excluded.stripe_status,
  renews = excluded.renews;
