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

-- A product without a worker url has no report to show.
-- name: UpdateProduct :one
UPDATE products
SET
  name = sqlc.arg (name),
  plan = sqlc.arg (plan),
  worker_url = sqlc.arg (worker_url),
  worker_report = CASE sqlc.arg (worker_url) WHEN '' THEN '' ELSE worker_report END,
  sync_notice = CASE sqlc.arg (worker_url) WHEN '' THEN '' ELSE sync_notice END,
  sync_error = CASE sqlc.arg (worker_url) WHEN '' THEN '' ELSE sync_error END
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

-- The worker was read. The notice is empty, or what is off with it all the same.
-- name: SetProductSynced :exec
UPDATE products
SET
  synced_at = unixepoch(),
  sync_error = '',
  sync_notice = ?,
  worker_report = ?
WHERE
  id = ?;

-- name: SetProductSyncError :exec
UPDATE products
SET
  sync_error = ?
WHERE
  id = ?;
