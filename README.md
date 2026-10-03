<p align="center">
<img src="./.github/logo.svg" width="80">
<br><br>
<img alt="GitHub Tag" src="https://img.shields.io/github/v/tag/MizuchiLabs/inkan?label=Version">
<img alt="GitHub License" src="https://img.shields.io/github/license/MizuchiLabs/inkan">
<img alt="GitHub Issues or Pull Requests" src="https://img.shields.io/github/issues/MizuchiLabs/inkan">
</p>

# inkan

A small ledger for offline license keys. It holds the signing key of each
product you sell, issues keys by hand, mirrors paid subscriptions from Stripe,
and tells you what a pasted key really is.

Keys are Ed25519-signed claims that your app verifies offline with a public
key built into the binary. An inkan is the personal seal that makes a document
valid in Japan.

One Go binary, one SQLite file, a web UI. An optional [worker](#the-worker)
hands keys to paying customers.

## Run

```sh
go generate ./web   # builds the UI once
go run .
```

Open http://127.0.0.1:3000.

| Flag         | Env              | Default          |                            |
| ------------ | ---------------- | ---------------- | -------------------------- |
| `--addr`     | `INKAN_ADDR`     | `127.0.0.1:3000` | Address to listen on       |
| `--data-dir` | `INKAN_DATA_DIR` | `data`           | Directory for the database |
| `--debug`    | `INKAN_DEBUG`    | off              | Debug logging              |

The UI has no login yet. It listens on localhost by default. Keep it on your
machine or a private network.

## How it works

**Products.** A product is a name, a key prefix, a plan and a keypair. Add a
new one and you get a fresh keypair, or paste the private key of a product
that already ships. The Products page shows the public key and the build flag
for it.

**Licenses.** A license is a row: who, which plan, until when, plus optional
extra claims. The key is never stored. It is signed from the row on demand,
and signing is deterministic, so a license shows the same key every time.
Change a signed field and the key changes with it.

**Stripe.** Give a product a Stripe API key and a Stripe product id. Every few
minutes inkan reads the subscriptions and mirrors the ones to that product as
licenses. Stripe stays the source of truth, inkan only reads. It needs no
webhook and no public endpoint. A license key is valid until the paid period
ends, plus the product's grace days. A subscription that falls behind on a
payment keeps its last key and gets no new one.

**Plans.** One product can have many plans. The plan claim comes from the
Stripe price: give a price the metadata `plan=team` and its subscribers get
`team` keys. Prices without it get the plan of the product.

**Check.** Paste any key on the Licenses page. inkan verifies it the way the
product would and shows the claims inside.

## Use the keys in your app

```go
import "github.com/mizuchilabs/inkan/license"

claims, err := license.Verify(os.Getenv("MYAPP_LICENSE_KEY"), "MYAPP")
```

Build with the flag from the Products page:

```sh
go build -ldflags="-X github.com/mizuchilabs/inkan/license.pubkey=<hex>"
```

## The worker

inkan stays private. The worker is the small public part of one product. It is
the same binary, keeps no state and never talks to inkan. Stripe says what is
paid and the private key signs it, so it hands out the very keys the ledger
shows.

```
POST /webhook   Stripe reports a paid invoice, the key is mailed to the customer
POST /refresh   an app sends its key and gets the current one back
```

Print the settings of a product and give them to the worker as its
environment, wherever a container can run:

```sh
inkan worker env MYAPP   # prints the private key too, treat the output like one
inkan worker
```

| Env                           |                                             |
| ----------------------------- | ------------------------------------------- |
| `INKAN_PREFIX`                | Key prefix of the product                   |
| `INKAN_PRIVATE_KEY`           | Signing key of the product                  |
| `INKAN_STRIPE_KEY`            | API key that reads subscriptions, customers |
| `INKAN_STRIPE_PRODUCT`        | Its subscriptions are licenses              |
| `INKAN_PLAN`                  | Plan for prices that set none               |
| `INKAN_GRACE_DAYS`            | Default 14                                  |
| `INKAN_ADDR`                  | Address to listen on, e.g. `:8080`          |
| `INKAN_STRIPE_WEBHOOK_SECRET` | Signing secret of the webhook endpoint      |
| `INKAN_SMTP_ADDR`             | Mail server, host:port with STARTTLS        |
| `INKAN_SMTP_USER`             |                                             |
| `INKAN_SMTP_PASS`             |                                             |
| `INKAN_MAIL_FROM`             | Sender of the key mails                     |

`worker env` prints the first six. In Stripe, add a webhook endpoint for
`https://<worker>/webhook` that sends `invoice.paid`. Leave out the webhook
secret and the mail settings to serve refresh only.

**Refresh.** An app that is online can renew its own key:

```go
key, err := license.Refresh(ctx, "https://<worker>/refresh", key, "MYAPP")
```

The answer is the key the subscription is owed now. After a renewal that is a
new key with a later expiry. If nothing new was paid it is the same key again.
`Refresh` only returns an answer that verifies as a key of the app, so the app
never has to trust the network. On an error the app keeps the key it has.
Once a day is plenty. Airgapped installs skip this and paste the key from the
mail.

It is a plain POST with the key as the body, so any language can do it:

```sh
curl -X POST https://<worker>/refresh --data "$MYAPP_LICENSE_KEY"
```

**In your own service.** The worker is also a plain `http.Handler`, for a
serverless function or an existing Go backend. `Send` is yours to write if
mail is not how your keys travel.

```go
import "github.com/mizuchilabs/inkan/worker"

h, err := worker.New(worker.Config{
	Prefix:        "MYAPP",
	PrivateKey:    os.Getenv("INKAN_PRIVATE_KEY"),
	StripeKey:     os.Getenv("INKAN_STRIPE_KEY"),
	StripeProduct: "prod_...",
	Plan:          "pro",
	GraceDays:     14,
	WebhookSecret: os.Getenv("INKAN_STRIPE_WEBHOOK_SECRET"),
	Send:          worker.SMTP{Addr: "smtp.example.com:587", From: "keys@example.com"}.Send,
})
```

## Key format

A key is built in three steps:

1. The claims are encoded as JSON. That is the payload.
2. The payload is signed with the Ed25519 private key.
3. One version byte (`1`), the payload and the 64-byte signature are joined,
   base64 encoded without padding, cut into chunks of five and prefixed with
   the product prefix.

| Claim   |                                             |
| ------- | ------------------------------------------- |
| `v`     | Always 1                                    |
| `app`   | The product prefix                          |
| `plan`  | Plan name, the app decides what it means    |
| `email` | Who the license is for                      |
| `exp`   | Unix seconds, 0 never expires               |
| `id`    | Optional. Stripe subscription id or `lic_…` |
| `meta`  | Optional. Extra strings, e.g. seats         |

That is small enough to sign from any language. This is all of it in Python:

```python
payload = b'{"v":1,"app":"MYAPP","plan":"pro","email":"ops@acme.io","exp":1800000000,"id":"sub_123"}'
sig = Ed25519PrivateKey.from_private_bytes(seed).sign(payload)
enc = base64.b64encode(b"\x01" + payload + sig).decode().rstrip("=")
key = "MYAPP-" + "-".join(enc[i : i + 5] for i in range(0, len(enc), 5))
```

With the seed bytes 0 to 31 the key is `MYAPP-AXsid-iI6MS-…-e0RyC-JdsM`, a test
in `license` pins it. The private key inkan stores is base64 of 64 bytes, the
seed is the first 32. Verifying checks the signature over the payload as it
arrived, so a key verifies whatever the JSON of its signer looks like. Only
keys from the `license` package are promised to match the ones inkan shows,
byte for byte.

## What it does not do

- It cannot revoke a key. Keys work offline, deleting a license only forgets
  it here. A key ends when it expires, so an unpaid subscription runs out with
  its last paid period plus the grace days.
- One worker serves one product.
- Signing keys and Stripe keys sit unencrypted in the database. The data
  directory is owner only, back it up like a secret.

## Development

```sh
task dev    # backend on :3000, UI dev server with a proxy to it
task gen    # sqlc, OpenAPI spec, web client
task lint   # vet, tests, golangci-lint
```
