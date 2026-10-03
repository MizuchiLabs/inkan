CREATE TABLE products (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  prefix TEXT NOT NULL UNIQUE, -- app name signed into the keys and their visible prefix, uppercase letters and digits
  plan TEXT NOT NULL, -- plan claim for provider prices without plan metadata, default for manual ones
  grace_days INTEGER NOT NULL DEFAULT 14, -- added to the paid period of a provider license
  private_key TEXT NOT NULL, -- base64 Ed25519, signs every key of this product
  provider_key TEXT NOT NULL DEFAULT '', -- API key of the payment provider, reads subscriptions and customers
  provider_product TEXT NOT NULL DEFAULT '', -- product id at the provider, its subscriptions are licenses
  synced_at INTEGER, -- last successful provider sync
  sync_error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- Keys are never stored. They are signed from these rows on demand, the same
-- row always gives the same key.
CREATE TABLE licenses (
  id TEXT NOT NULL, -- subscription id at the provider, lic_... for manual ones
  product_id INTEGER NOT NULL REFERENCES products (id) ON DELETE CASCADE,
  source TEXT NOT NULL, -- manual, or the name of the provider, like stripe
  email TEXT NOT NULL,
  customer TEXT NOT NULL DEFAULT '', -- display name, not part of the key
  plan TEXT NOT NULL,
  expires_at INTEGER NOT NULL, -- unix seconds, 0 is perpetual
  meta TEXT NOT NULL DEFAULT '{}', -- json object of strings, signed into the key
  note TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '', -- subscription status at the provider, empty for manual
  renews BOOLEAN NOT NULL DEFAULT 0, -- the provider will bill another period
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  -- One subscription can hold several products, each is a license of its own.
  PRIMARY KEY (product_id, id)
);
