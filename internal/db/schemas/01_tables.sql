CREATE TABLE products (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  prefix TEXT NOT NULL UNIQUE, -- app name signed into the keys and their visible prefix, uppercase letters and digits
  plan TEXT NOT NULL, -- default for manual licenses, the worker gives it to Stripe products without a plan
  private_key TEXT NOT NULL, -- base64 Ed25519, signs every key of this product
  old_public_keys TEXT NOT NULL DEFAULT '', -- hex, comma separated, newest first. From before a rotation, their keys still verify
  -- The worker hands out the paid licenses, inkan reads them from it.
  worker_url TEXT NOT NULL DEFAULT '', -- where the worker runs, empty without one
  worker_token TEXT NOT NULL DEFAULT '', -- lets inkan read the licenses of the worker
  worker_report TEXT NOT NULL DEFAULT '', -- json, what the worker said it runs with at the last sync. Its settings live in its env alone
  synced_at INTEGER, -- last time the worker was read
  sync_error TEXT NOT NULL DEFAULT '', -- why the worker could not be read at the last try
  sync_notice TEXT NOT NULL DEFAULT '', -- what is off with a worker that could be read, like an export that was not deployed
  created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- Manual keys are never stored. They are signed from these rows on demand, the
-- same row always gives the same key. A paid license holds the key its worker
-- hands out.
CREATE TABLE licenses (
  id TEXT NOT NULL, -- id at the provider, lic_... for manual ones
  product_id INTEGER NOT NULL REFERENCES products (id) ON DELETE CASCADE,
  source TEXT NOT NULL, -- manual, or the name of the provider, like stripe
  kind TEXT NOT NULL DEFAULT '', -- subscription or purchase at the provider, empty for manual
  email TEXT NOT NULL,
  customer TEXT NOT NULL DEFAULT '', -- display name, not part of the key
  customer_gone BOOLEAN NOT NULL DEFAULT 0, -- deleted at the provider, email and customer are the last known ones
  plan TEXT NOT NULL,
  expires_at INTEGER NOT NULL, -- unix seconds, 0 is perpetual
  meta TEXT NOT NULL DEFAULT '{}', -- json object of strings, signed into the key
  fingerprint TEXT NOT NULL DEFAULT '', -- the machine the key is bound to, signed into the key, empty binds to nothing
  note TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '', -- subscription status at the provider, paid or refunded for a one time purchase, empty for manual
  renews BOOLEAN NOT NULL DEFAULT 0, -- the provider will bill another period
  key TEXT NOT NULL DEFAULT '', -- the key the worker hands out, empty for manual
  seen_at INTEGER NOT NULL DEFAULT 0, -- start of the last sync that found it at the worker
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  -- One subscription can hold several products, each is a license of its own.
  PRIMARY KEY (product_id, id)
);

-- One row per day, product and plan, so the UI can show how the numbers moved.
CREATE TABLE snapshots (
  day TEXT NOT NULL, -- YYYY-MM-DD in UTC
  product_id INTEGER NOT NULL REFERENCES products (id) ON DELETE CASCADE,
  plan TEXT NOT NULL,
  active INTEGER NOT NULL, -- licenses that had not run out
  PRIMARY KEY (day, product_id, plan)
);
