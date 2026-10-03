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
| `v`     | Always 1                                               |
| `app`   | The product prefix                                     |
| `plan`  | Plan name, the app decides what it means               |
| `email` | Who the license is for                                 |
| `exp`   | Unix seconds, 0 never expires                          |
| `id`    | Stripe subscription id or `lic_…`, missing in old keys |
| `meta`  | Optional. Extra strings, e.g. seats                    |

Your app checks the signature with the public key built into it. That needs no
network and no secret. The signature covers the payload bytes as they arrived,
so a key verifies whatever the JSON of its signer looks like.

Keys get mangled on their way. Before verifying, all whitespace in a key and
any quotes around it are dropped, so a key survives a mail client that wraps
lines or an env file that keeps its quotes.

### In another language

The format is small enough to sign or verify anywhere. This is all of the
signing in Python:

```python
payload = b'{"v":1,"app":"MYAPP","plan":"pro","email":"ops@acme.io","exp":1800000000,"id":"sub_123"}'
sig = Ed25519PrivateKey.from_private_bytes(seed).sign(payload)
enc = base64.b64encode(b"\x01" + payload + sig).decode().rstrip("=")
key = "MYAPP-" + "-".join(enc[i : i + 5] for i in range(0, len(enc), 5))
```

With the seed bytes 0 to 31 the key is `MYAPP-AXsid-iI6MS-…-e0RyC-JdsM`, a test
in `license` pins it. The private key Inkan stores is base64 of 64 bytes, the
seed is the first 32. Only keys from the `license` package are promised to
match the ones Inkan shows, byte for byte.

## What a key can and cannot do

- **It cannot be forged** without the private key of the product.
- **It can be read.** The payload is plain JSON behind base64. Whoever holds a
  key sees the email, the plan and the meta. Put no secrets in meta.
- **It cannot be revoked.** It works offline until it expires.
- **It can be shared.** A key is not bound to a machine.
- **The check lives in your app.** Someone who changes your binary can remove
  it. A license key keeps honest customers honest.
- **Signing keys can rotate.** An app can carry several public keys, comma
  separated. Ship the new one next to the old one and drop the old one once
  its keys have run out. A worker only renews keys signed with its current
  key.

## Where the secrets are

- **Inkan** keeps signing keys and Stripe API keys unencrypted in `inkan.db`.
  The data directory is created owner only. Back it up like a secret.
- **The worker** gets the private key and a Stripe key through its
  environment. A restricted Stripe key that reads subscriptions and customers
  is enough. `inkan worker env` prints the private key, treat its output like
  one.
- **The webhook** only takes events with a valid Stripe signature that are at
  most five minutes old.
- **Refresh** only looks up keys with a valid signature of the product.

## The login

- **One password**, from `INKAN_PASSWORD`. Without it Inkan only listens on
  localhost.
- **Nothing is stored.** A session is a cookie signed with a key derived from
  the password with PBKDF2. It lasts 30 days, and a new password ends every
  session.
- **The cookie** is HttpOnly and SameSite=Strict. It is Secure behind a proxy
  that reports HTTPS.
- **Five tries a minute**, for all clients together, so pick a long random
  password. A flood of wrong tries blocks signing in while it lasts. Sessions
  that exist keep working.
- **Other sites** cannot write through the browser, signed in or not.
- **It is the second layer.** Keep Inkan on a private network or behind your
  own proxy where you can.

## Releases

Release checksums and container images are signed with cosign. Every binary
comes with an SBOM.
