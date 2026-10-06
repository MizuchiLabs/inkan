-- name: ListProducts :many
SELECT
  *
FROM
  products
ORDER BY
  name;

-- name: GetProduct :one
SELECT
  *
FROM
  products
WHERE
  id = ?;

-- name: GetProductByPrefix :one
SELECT
  *
FROM
  products
WHERE
  prefix = ?;

-- name: CreateProduct :one
INSERT INTO
  products (name, prefix, plan, grace_days, max_key_days, private_key, worker_token)
VALUES
  (?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateProduct :one
UPDATE products
SET
  name = ?,
  plan = ?,
  grace_days = ?,
  max_key_days = ?,
  provider_product = ?,
  worker_url = ?,
  mail_from = ?,
  mail_logo = ?,
  mail_accent = ?,
  mail_footer = ?,
  mail_company = ?
WHERE
  id = ? RETURNING *;

-- name: DeleteProduct :exec
DELETE FROM products
WHERE
  id = ?;

-- A rotation moves the public half of the old key into the list.
-- name: SetProductKeys :one
UPDATE products
SET
  private_key = ?,
  old_public_keys = ?
WHERE
  id = ? RETURNING *;

-- name: SetProductWorkerToken :exec
UPDATE products
SET
  worker_token = ?
WHERE
  id = ?;

-- The worker was read. sync_error is empty, or what is off with the worker.
-- name: SetProductSynced :exec
UPDATE products
SET
  synced_at = unixepoch(),
  sync_error = ?
WHERE
  id = ?;

-- name: SetProductSyncError :exec
UPDATE products
SET
  sync_error = ?
WHERE
  id = ?;
