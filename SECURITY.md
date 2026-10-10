# Security

## Reporting a vulnerability

Please do not open a public issue. Use "Report a vulnerability" under the
[Security tab](https://github.com/MizuchiLabs/inkan/security) of this
repository. That reaches the maintainers in private.

## What a license key is

A key is a signed statement, not a secret. It is built in three steps:

1. The claims are encoded as JSON. That is the payload.
2. The payload is signed with the Ed25519 private key of the product.
3. One version byte (`1`), the payload and the 64-byte signature are joined,
   base64 encoded without padding, cut into chunks of five and prefixed with
   the product prefix.

| Claim   |                                                        |
| ------- | ------------------------------------------------------ |
| `app`   | The product prefix                                     |
| `plan`  | Plan name, the app decides what it means               |
| `email` | Who the license is for                                 |
| `exp`   | Unix seconds, 0 never expires                          |
| `id`    | Id at the issuer, `lic_…` by hand, missing in old keys |
| `fp`    | Optional. The machine the key is bound to              |
| `meta`  | Optional. Extra strings, e.g. seats                    |

Your app checks the signature with the public key built into it. That needs no
network and no secret. The signature covers the payload bytes as they arrived,
so a key verifies whatever the JSON of its signer looks like.

Keys get mangled on their way. Before verifying, all whitespace in a key and
any quotes around it are dropped, so a key survives a mail client that wraps
lines or an env file that keeps its quotes.

### In another language

[FORMAT.md](FORMAT.md) has the steps to verify a key and the test vectors to
check a port against. [`sdk/ts`](sdk/ts) and [`sdk/python`](sdk/python) are
ready to use.

The format is small enough to sign anywhere too. This is all of the signing
in Python:

```python
payload = b'{"app":"MYAPP","plan":"pro","email":"ops@acme.io","exp":1800000000,"id":"sub_123"}'
sig = Ed25519PrivateKey.from_private_bytes(seed).sign(payload)
enc = base64.b64encode(b"\x01" + payload + sig).decode().rstrip("=")
key = "MYAPP-" + "-".join(enc[i : i + 5] for i in range(0, len(enc), 5))
```

With the seed bytes 0 to 31 the key is `MYAPP-AXsiY-XBwIj-…-m7AO4-I`, a test
in `license` pins it. The private key Inkan stores is base64 of 64 bytes, the
seed is the first 32.

## What a key can and cannot do

- **It cannot be forged** without the private key of the product.
- **It can be read.** The payload is plain JSON behind base64. Whoever holds a
  key sees the email, the plan and the meta. Put no secrets in meta.
- **It cannot be revoked.** It works offline until it expires. Max key days on a
  product keep that time short, for apps that refresh.
- **It can be shared**, unless it carries a fingerprint. A bound key only
  works in an app that checks its own fingerprint against it, and that
  fingerprint is whatever the app picks, so a copied data directory may carry
  it along. It stops a key from being pasted around and no more.
- **The check lives in your app.** Someone who changes your binary can remove
  it. A license key keeps honest customers honest.
- **It trusts the clock.** The expiry is checked against the time of the
  machine the app runs on. Someone who sets that clock back keeps an expired
  key alive. An app that is online can compare with a time it gets elsewhere.
- **Signing keys can rotate.** An app can carry several public keys, comma
  separated. See below.

## Rotating a signing key

For when a private key may have leaked, or the people who had it moved on.

1. **Rotate** under Edit on the Products page. The product gets a new keypair
   and the old private key is destroyed. Its public half stays on the product
   as an old key. Manual licenses show new keys from now on.
2. **Ship a build** with the public keys the Products page now shows, the new
   one and the old ones behind it. It takes keys of both.
3. **Deploy the worker** with a fresh `inkan worker export`. It signs with the new
   key and still refreshes keys of the old one, so an app trades its old key
   for a new one on its next refresh. The settings are swapped in one go on
   Cloudflare and Scaleway, on Node the worker restarts.
4. **Drop the old keys** under Edit once no key from before is in use, then
   ship a build with the new public key alone and deploy the worker again.

Nothing breaks in between. Keys that are out work offline the whole time, and
the worker keeps signing with the old key until step 3.

- **Mind the order of 2 and 3.** A build from before the rotation cannot take
  a key of the new signing key. It keeps the key it has until that runs out.
  Give your users time to update before you deploy the worker.
- **After a leak the old public key is the hole.** Whoever has the old private
  key can forge keys for every build that still carries its public half. Get
  to step 4 as fast as your users update.
- **Hand issued keys** do not refresh. Send their owners the new key before
  step 4.
- **There is no undo.** Run `inkan backup` first if you are not sure.

## Where the secrets are

- **Inkan** keeps the signing keys and the worker tokens unencrypted in
  `inkan.db`. The file is owner only, and so is the data directory when Inkan
  creates it. Back it up like a
  secret, with `inkan backup` for the database or `inkan keys` for the
  signing keys alone. It holds no Stripe key and no secret of a mail service.
- **The worker** gets the private key, a Stripe key and the key of a mail
  service through its environment. A restricted Stripe key that reads
  subscriptions and customers is enough. One time purchases also need
  Checkout sessions, payment intents and charges. `inkan worker export`
  writes the private key into `worker.env`, treat the file like one.
- **The webhook** only takes events with a valid Stripe signature that are at
  most five minutes old.
- **Refresh** only looks up keys with a valid signature of the product, by
  its current key or one from before a rotation that it was given.
- **The license list** of a worker is only for the dashboard. It takes a token
  of at least 32 characters that Inkan makes for each product. Whoever has it
  reads every customer and key of the product.

## The login

- **One password**, from `INKAN_PASSWORD`. Without it Inkan only listens on
  localhost and only answers to a local host name. A site that points a name
  of its own at your machine gets nothing.
- **Nothing is stored.** A session is a cookie signed with a key derived from
  the password with PBKDF2. It lasts 30 days, and a new password ends every
  session.
- **The cookie** is HttpOnly and SameSite=Strict. It is Secure behind a proxy
  that reports HTTPS.
- **Five tries a minute**, for all clients together, so pick a long random
  password. A flood of wrong tries blocks signing in while it lasts. Sessions
  that exist keep working.
- **Other sites** cannot write through the browser, signed in or not.
- **The API token** is optional, from `INKAN_API_TOKEN`. It lets a script use
  the API without a session and has no limit on tries, so it must be at least
  32 characters. Make it random.
- **It is the second layer.** Keep Inkan on a private network or behind your
  own proxy where you can.

## Releases

Release checksums and container images are signed with cosign. Every binary
comes with an SBOM.
