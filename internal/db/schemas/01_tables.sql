CREATE TABLE products (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  prefix TEXT NOT NULL UNIQUE, -- app name signed into the keys and their visible prefix, uppercase letters and digits
  plan TEXT NOT NULL, -- default for manual licenses, the worker gives it to prices without plan metadata
  private_key TEXT NOT NULL, -- base64 Ed25519, signs every key of this product
  old_public_keys TEXT NOT NULL DEFAULT '', -- hex, comma separated, newest first. From before a rotation, their keys still verify
  -- The worker hands out the paid licenses, inkan reads them from it.
  worker_url TEXT NOT NULL DEFAULT '', -- where the worker runs, empty without one
  worker_token TEXT NOT NULL DEFAULT '', -- lets inkan read the licenses of the worker
  -- Settings of the worker. Only kept here so worker env can print them.
  grace_days INTEGER NOT NULL DEFAULT 14, -- added to the paid period of a license
  max_key_days INTEGER NOT NULL DEFAULT 0, -- longest life of a paid key, 0 has no limit, apps refresh for the next one
  provider_product TEXT NOT NULL DEFAULT '', -- product id at Stripe, its subscriptions and purchases are licenses
  mail_from TEXT NOT NULL DEFAULT '', -- an address or "Name <address>"
  mail_logo TEXT NOT NULL DEFAULT '', -- https url, shown in the key mail
  mail_accent TEXT NOT NULL DEFAULT '', -- color of the key mail, #rrggbb
  mail_footer TEXT NOT NULL DEFAULT '', -- one line under the key
  mail_company TEXT NOT NULL DEFAULT '', -- legal name, address and the like, the last line of the key mail
  mail_template TEXT NOT NULL DEFAULT '', -- html of a key mail of its own, with placeholders, empty sends the built-in one
  synced_at INTEGER, -- last time the worker was read
  sync_error TEXT NOT NULL DEFAULT '', -- why the worker could not be read, or what is off with it
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
