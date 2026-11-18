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
  products (name, prefix, plan, private_key, worker_token)
VALUES
  (?, ?, ?, ?, ?) RETURNING *;

-- The report and the error are those of the worker at the url, another url starts without them.
-- name: UpdateProduct :one
UPDATE products
SET
  name = sqlc.arg (name),
  plan = sqlc.arg (plan),
  worker_url = sqlc.arg (worker_url),
  worker_report = CASE worker_url WHEN sqlc.arg (worker_url) THEN worker_report ELSE '' END,
  sync_error = CASE worker_url WHEN sqlc.arg (worker_url) THEN sync_error ELSE '' END
WHERE
  id = sqlc.arg (id) RETURNING *;

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

-- name: SetProductSynced :exec
UPDATE products
SET
  synced_at = unixepoch(),
  sync_error = '',
  worker_report = ?
WHERE
  id = ?;

-- name: SetProductSyncError :exec
UPDATE products
SET
  sync_error = ?
WHERE
  id = ?;
