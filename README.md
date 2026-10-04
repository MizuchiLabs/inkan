<p align="center">
<img src="./.github/logo.svg" width="80">
<br><br>
<img alt="GitHub Tag" src="https://img.shields.io/github/v/tag/MizuchiLabs/inkan?label=Version">
<img alt="GitHub License" src="https://img.shields.io/github/license/MizuchiLabs/inkan">
<img alt="GitHub Issues or Pull Requests" src="https://img.shields.io/github/issues/MizuchiLabs/inkan">
</p>

# Inkan

A small ledger for offline license keys. It holds the signing key of each
product you sell, issues keys by hand, mirrors paid subscriptions from Stripe,
and tells you what a pasted key really is.

A key is a signed string that your app verifies offline with a public key
built into it. An inkan is the personal seal that makes a document valid in
Japan.

## Features

- License keys that work offline, a plain string to paste or set as an env
- Issue keys by hand, or let Stripe subscriptions and one time purchases
  become licenses
- Keys are never stored, a license gives the same key every time
- An optional worker mails keys to customers and renews them for apps that
  are online
- A Go package to verify, hold and renew keys, small verifiers for TypeScript
  and Python, a documented format for every other language
- The worker also runs on Cloudflare Workers
- Paste any key to see what is inside
- One binary with SQLite and a web UI, Docker-ready

## Quick Start

### Using Docker

```bash
docker run -d \
   -p 127.0.0.1:3000:3000 \
   -v $(pwd)/data:/data \
   -e INKAN_ADDR=:3000 \
   -e INKAN_DATA_DIR=/data \
   -e INKAN_PASSWORD=change-me \
   ghcr.io/mizuchilabs/inkan:latest
```

### Using Docker Compose

```yaml
services:
  inkan:
    image: ghcr.io/mizuchilabs/inkan:latest
    container_name: inkan
    ports:
      - "127.0.0.1:3000:3000"
    volumes:
      - ./data:/data
    environment:
      - INKAN_ADDR=:3000
      - INKAN_DATA_DIR=/data
      - INKAN_PASSWORD=change-me
    restart: unless-stopped
```

### Using Binary

Get one from the [releases](https://github.com/MizuchiLabs/inkan/releases).

```bash
inkan
```

Open http://127.0.0.1:3000 and add a product.

Without a password Inkan only listens on localhost. A container listens on
all of its addresses, so it needs one. The examples above still publish the
port on localhost only, put your own proxy or VPN in front to reach it from
elsewhere.

## Configuration

| Env Var           | Flag          | Default          | Description                |
| ----------------- | ------------- | ---------------- | -------------------------- |
| `INKAN_ADDR`      | `--addr`      | `127.0.0.1:3000` | Address to listen on       |
| `INKAN_DATA_DIR`  | `--data-dir`  | `data`           | Directory for the database |
| `INKAN_PASSWORD`  | `--password`  | -                | Password for the UI        |
| `INKAN_API_TOKEN` | `--api-token` | -                | Bearer token for scripts   |
| `INKAN_DEBUG`     | `--debug`     | `false`          | Enable debug logging       |

Inkan backs up `inkan.db` to `inkan.db.backup` before it changes the schema on
an upgrade.

A script that issues or reads licenses sends the token instead of signing in.
The API is described at `/docs`.

```sh
curl -H "Authorization: Bearer $INKAN_API_TOKEN" http://127.0.0.1:3000/api/licenses
```

## How it works

- **Products.** A product is a name, a key prefix, a plan and a keypair. Add a
  new one and you get a fresh keypair, or paste the private key of a product
  that already ships. The Products page shows the public key and the build
  flag for it.
- **Licenses.** A license is a row: who, which plan, until when, plus optional
  extra claims. The key is signed from the row on demand. Change a signed
  field and the key changes with it.
- **Stripe.** Give a product a Stripe API key and a Stripe product id. Every
  few minutes Inkan reads the subscriptions and one time purchases of that
  product and mirrors them as licenses. Stripe stays the source of truth,
  Inkan only reads. It needs no webhook and no public endpoint.
- **One time purchases.** A paid Checkout session in payment mode is a license
  that never expires. Give the price the metadata `days=365` and it lasts a
  year. A refunded purchase gets a key that ran out on the day it was bought.
  The Stripe key needs read access to Checkout sessions, payment intents and
  charges for this, without it only subscriptions are mirrored.
- **Expiry.** A paid key is valid until the paid period ends, plus the grace
  days of the product. A subscription that falls behind on a payment keeps its
  last key and gets no new one.
- **Plans.** One product can have many plans. Give a Stripe price the metadata
  `plan=team` and its subscribers get `team` keys. Prices without it get the
  plan of the product.
- **Seats and features.** Price metadata that starts with `meta_` is signed
  into the key, `meta_tier=gold` becomes the claim `tier=gold`. The metadata
  `quantity=seats` puts the bought quantity into the claim `seats`.
- **Key days.** A product can limit how long its Stripe keys live. With 30 key
  days a key lasts 15 to 30 days and the app refreshes for the next one. That
  is what stops a license after a refund or a cancellation. Leave it at 0 for
  apps that run offline, their customers would have to paste a new key every
  few weeks.
- **History.** Once a day the active licenses are counted per product and
  plan. The Licenses page shows how they moved.
- **Check.** Paste any key on the Licenses page. Inkan verifies it the way the
  product would and shows the claims inside.

## Use the keys in your app

```go
import "github.com/mizuchilabs/inkan/license"

lic := license.NewKeeper("MYAPP")
_, err := lic.Set(os.Getenv("MYAPP_LICENSE_KEY"))

ok := lic.Valid()             // is there a license right now
claims, err := lic.Claims()   // plan, email and meta, or why there is none
claims, err = lic.Set(pasted) // a new key came by mail
```

Build your app with the flag from the Products page:

```sh
go build -ldflags="-X github.com/mizuchilabs/inkan/license.pubkey=<hex>"
```

- **Live.** `Valid` and `Claims` check the expiry on every call, so a running
  app notices when its key runs out.
- **Swap.** `Set` replaces the key while the app runs. It is what a settings
  page calls with a pasted key. A key it turns down changes nothing.
- **Once.** `license.Verify(key, "MYAPP")` checks a single key, for an app
  that needs no more than that.
- **Self-compiled.** A build without the flag cannot verify any key and
  `Valid` is always false. `license.HasPublicKey()` tells such a build apart,
  for an app that wants to show it.

The `license` package is a Go module of its own with no dependencies, so it
adds nothing else to your build.

### Other languages

| Language   |                                                   |
| ---------- | ------------------------------------------------- |
| TypeScript | [`sdk/ts`](sdk/ts), no dependencies               |
| Python     | [`sdk/python`](sdk/python), one file              |
| Any other  | [FORMAT.md](FORMAT.md) and the test vectors in it |

Check the key where the user cannot change the code. For a web app that is
the server, a check in the browser is a hint and not a lock.

## The worker

Inkan stays private. The worker is the small public part of one product. It is
the same binary, keeps no state and never talks to Inkan. Stripe says what is
paid and the private key signs it, so it hands out the very keys the ledger
shows.

| Endpoint        |                                                             |
| --------------- | ----------------------------------------------------------- |
| `POST /webhook` | Stripe reports a payment, the key is mailed to the customer |
| `POST /refresh` | An app sends its key and gets the current one back          |

Print the settings of a product into a file and run the worker with it,
wherever a container can run. The file holds the private key, treat it like
one.

```sh
inkan worker env MYAPP > worker.env
# or from the container of the quick start
docker exec inkan inkan worker env MYAPP > worker.env
```

```yaml
services:
  worker:
    image: ghcr.io/mizuchilabs/inkan:latest
    command: worker
    ports:
      - "8080:8080"
    env_file: worker.env
    environment:
      - INKAN_ADDR=:8080
    restart: unless-stopped
```

| Env Var                       | Default          | Description                                    |
| ----------------------------- | ---------------- | ---------------------------------------------- |
| `INKAN_PREFIX`                | -                | Key prefix of the product                      |
| `INKAN_PRIVATE_KEY`           | -                | Signing key of the product                     |
| `INKAN_STRIPE_KEY`            | -                | API key that reads subscriptions and customers |
| `INKAN_STRIPE_PRODUCT`        | -                | Its subscriptions are licenses                 |
| `INKAN_PLAN`                  | -                | Plan for prices that set none                  |
| `INKAN_GRACE_DAYS`            | `14`             | Days a key outlives its paid period            |
| `INKAN_KEY_DAYS`              | `0`              | Longest life of a key in days, 0 has no limit  |
| `INKAN_ADDR`                  | `127.0.0.1:3000` | Address to listen on                           |
| `INKAN_STRIPE_WEBHOOK_SECRET` | -                | Signing secret of the webhook endpoint         |
| `INKAN_SMTP_ADDR`             | -                | Mail server, host:port with STARTTLS           |
| `INKAN_SMTP_USER`             | -                | Mail server login                              |
| `INKAN_SMTP_PASS`             | -                | Mail server password                           |
| `INKAN_MAIL_FROM`             | -                | Sender of the key mails                        |

`worker env` prints the first seven. For the mails, add a webhook endpoint in
Stripe for `https://<worker>/webhook` that sends `invoice.paid`, plus
`checkout.session.completed` and `checkout.session.async_payment_succeeded` if
you sell one time purchases. Leave out the webhook secret and the mail
settings to serve refresh only.

### How it behaves

- **Mails.** The first payment always sends the key. A renewal only sends one
  when its period is longer than a month. Nobody pastes a key every month, and
  an app that is online refreshes on its own.
- **Refresh.** The answer is the key the subscription is owed now. After a
  renewal that is a new key with a later expiry. If nothing new was paid it is
  the same key again, so an old key never stops working as a ticket.
- **Hand issued keys.** A key with a `lic_…` id, or none, always comes back as
  it is.
- **Refunds.** Cancel a refunded subscription right away and its next key ends
  with the cancellation. A refunded purchase gets an expired key. An app that
  refreshes picks that up, see key days above.
- **Stripe is asked at most every five minutes per license.** A key that leaks
  cannot be used to spend your Stripe rate limit.
- **Errors.** An app only accepts an answer that verifies as one of its keys.
  When a refresh fails the app keeps the key it has.

### Renew from your app

```go
go lic.Renew(ctx, "https://<worker>/refresh", save)
```

The `Keeper` asks right away and then once a day. Every new key goes to
`save`, so the app can store it for its next start. Airgapped installs skip
this and paste the key from the mail.

It is a plain POST with the key as the body, so any language can do it:

```sh
curl -X POST https://<worker>/refresh --data "$MYAPP_LICENSE_KEY"
```

### In your own service

The worker is also a plain `http.Handler`, for an existing Go backend or a
serverless platform that runs Go or containers, like Scaleway Functions, AWS
Lambda, Cloud Run or Fly. `worker.New` takes a `worker.Config` with the same
settings. Its `Send` is yours to write if mail is not how your keys travel.

### On Cloudflare

Cloudflare Workers do not run Go, so [`workers/cloudflare`](workers/cloudflare)
holds the same worker in TypeScript. It takes the same settings and signs the
same keys.

## What it does not do

- It cannot revoke a key that is out. Keys work offline, deleting a license
  only forgets it here. A key ends when it expires, so an unpaid subscription
  runs out with its last paid period plus the grace days. Key days keep that
  time short for apps that refresh.
- It does not bind a key to a machine or count seats.
- It has one password, no user accounts.
- One worker serves one product.

## Security

How a key is built, what it can and cannot protect, and where the secrets
live is in [SECURITY.md](SECURITY.md).

## License

Apache License 2.0 - See [LICENSE.md](LICENSE.md)
