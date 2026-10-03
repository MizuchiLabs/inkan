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
  products (name, prefix, plan, grace_days, private_key)
VALUES
  (?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateProduct :one
UPDATE products
SET
  name = ?,
  plan = ?,
  grace_days = ?,
  stripe_key = ?,
  stripe_product = ?
WHERE
  id = ? RETURNING *;

-- name: DeleteProduct :exec
DELETE FROM products
WHERE
  id = ?;

-- name: SetProductSynced :exec
UPDATE products
SET
  synced_at = unixepoch(),
  sync_error = ''
WHERE
  id = ?;

-- name: SetProductSyncError :exec
UPDATE products
SET
  sync_error = ?
WHERE
  id = ?;
