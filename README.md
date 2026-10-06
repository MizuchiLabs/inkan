<p align="center">
<img src="./.github/logo.svg" width="80">
<br><br>
<img alt="GitHub Tag" src="https://img.shields.io/github/v/tag/MizuchiLabs/inkan?label=Version">
<img alt="GitHub License" src="https://img.shields.io/github/license/MizuchiLabs/inkan">
<img alt="GitHub Issues or Pull Requests" src="https://img.shields.io/github/issues/MizuchiLabs/inkan">
</p>

# Inkan

A small ledger for offline license keys. It holds the signing key of each
product you sell, issues keys by hand, shows the paid licenses of your Stripe
subscriptions, and tells you what a pasted key really is.

A key is a signed string that your app verifies offline with a public key
built into it. An inkan is the personal seal that makes a document valid in
Japan.

## Features

- License keys that work offline, a plain string to paste or set as an env
- Issue keys by hand, or let Stripe subscriptions and one time purchases
  become licenses
- A license gives the same key every time, until something it says changes
- A small worker reads Stripe, mails keys to customers and renews them for
  apps that are online. It runs on Cloudflare Workers, Scaleway Functions or
  anywhere Node runs
- A Go package to verify, hold and renew keys, small verifiers for TypeScript
  and Python, a documented format for every other language
- Paste any key to see what is inside
- The dashboard is one binary with SQLite and a web UI, Docker-ready

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

There are two parts. The dashboard is this binary. It holds the signing keys,
issues keys by hand and shows every license. It stays private. The
[worker](#the-worker) is the public side of one product. It reads Stripe,
signs the keys of the paid licenses, mails them and refreshes them. The
dashboard reads the paid licenses from the worker, it never talks to Stripe.

Keys you issue by hand need no worker.

- **Products.** A product is a name, a key prefix, a plan and a keypair. Add a
  new one and you get a fresh keypair, or paste the private key of a product
  that already ships. The Products page shows the public key and the build
  flag for it.
- **Licenses.** A license is a row: who, which plan, until when, plus optional
  extra claims. A manual key is signed from the row on demand. Change a signed
  field and the key changes with it. A paid license shows the key its worker
  hands out.
- **Stripe.** Give the worker a Stripe API key and a Stripe product id. Every
  subscription and one time purchase of that product is a license. Stripe
  stays the source of truth, the worker only reads. Every few minutes the
  dashboard reads the list from the worker.
- **One time purchases.** A paid Checkout session in payment mode is a license
  that never expires. Give the price the metadata `days=365` and it lasts a
  year. A refunded purchase gets a key that ran out when it was bought. The
  Stripe key needs read access to Checkout sessions, payment intents and
  charges for this, without it only subscriptions are licenses.
- **Expiry.** A paid key is valid until the paid period ends, plus the grace
  days of the product. A subscription that falls behind on a payment keeps its
  last key and gets no new one.
- **Plans.** One product can have many plans. Give a Stripe price the metadata
  `plan=team` and its subscribers get `team` keys. Prices without it get the
  plan of the product.
- **Seats and features.** Price metadata that starts with `meta_` is signed
  into the key, `meta_tier=gold` becomes the claim `tier=gold`. The metadata
  `quantity=seats` puts the bought quantity into the claim `seats`.
- **Key days.** A signed key cannot be taken back. Without key days it runs
  to the end of the paid period plus the grace days, whatever happens in
  between. Key days cut every paid key short, so the app has to refresh and
  the next key says what Stripe says now. A key lasts between half and all of
  the key days. That is what ends a license after a refund, a chargeback or
  an immediate cancellation, and what stops an old key from keeping a plan or
  seats the customer no longer pays for.

  | Product                                        | Key days |
  | ---------------------------------------------- | -------- |
  | App that runs offline                          | `0`      |
  | Monthly plans only                             | `0`      |
  | Yearly plans, app is online                    | `30`     |
  | One time purchases with refunds, app is online | `30`     |
  | Strict, the app is always online               | `14`     |

  Half of the key days is how long the app may stay offline, so 30 allows two
  weeks. All of them is the longest a refunded key keeps working in an app
  that is kept from refreshing. An app that does refresh picks up a change
  within a day, key days or not.

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

The worker is the small public part of one product, written in TypeScript. It
keeps no state. Stripe says what is paid and the private key signs it.

| Endpoint        |                                                             |
| --------------- | ----------------------------------------------------------- |
| `POST /refresh` | An app sends its key and gets the current one back          |
| `POST /webhook` | Stripe reports a payment, the key is mailed to the customer |
| `GET /licenses` | The dashboard reads what the worker hands out               |

Print the settings of a product into a file. It holds the private key, treat
it like one.

```sh
inkan worker env MYAPP > worker.env
# or from the container of the quick start
docker exec inkan inkan worker env MYAPP > worker.env
```

Add the Stripe key to the file and deploy the worker with it.
[`worker`](worker) has the steps for Cloudflare Workers, Scaleway Functions
and any host that runs Node. Then enter where the worker runs under Edit on
the Products page. Its licenses show up on the Licenses page.

| Env Var                       | Default    | Description                                     |
| ----------------------------- | ---------- | ----------------------------------------------- |
| `INKAN_PREFIX`                | -          | Key prefix of the product                       |
| `INKAN_PRIVATE_KEY`           | -          | Signing key of the product                      |
| `INKAN_STRIPE_KEY`            | -          | API key that reads subscriptions and customers  |
| `INKAN_STRIPE_PRODUCT`        | -          | Its subscriptions and purchases are licenses    |
| `INKAN_PLAN`                  | -          | Plan for prices that set none                   |
| `INKAN_GRACE_DAYS`            | `14`       | Days a key outlives its paid period             |
| `INKAN_KEY_DAYS`              | `0`        | Longest life of a key in days, 0 has no limit   |
| `INKAN_WORKER_TOKEN`          | -          | Lets the dashboard read the licenses            |
| `INKAN_STRIPE_WEBHOOK_SECRET` | -          | Signing secret of the webhook endpoint          |
| `INKAN_MAIL_FROM`             | -          | Sender of the key mails                         |
| `RESEND_API_KEY`              | -          | Sends the mails through Resend                  |
| `SCW_SECRET_KEY`              | -          | Or through Scaleway Transactional Email         |
| `SCW_PROJECT_ID`              | -          | Project of the Scaleway key                     |
| `SCW_REGION`                  | `fr-par`   | Region of Scaleway Transactional Email          |
| `INKAN_PRODUCT_NAME`          | the prefix | How the key mail calls the product              |
| `INKAN_MAIL_LOGO`             | -          | https url of a logo for the dark header         |
| `INKAN_MAIL_ACCENT`           | `#2f3e8f`  | Color of the key mail                           |
| `INKAN_MAIL_FOOTER`           | -          | One line under the key, like where to get help  |
| `INKAN_MAIL_COMPANY`          | -          | Last line of the mail, your company details     |

`worker env` prints what the product has. The secrets of Stripe and the mail
service are not kept in the dashboard, they come out as comments to fill in.

The settings of the worker live on the product, under Edit on the Products
page. A change only reaches the worker when you export the file again and
deploy it. The Products page says so when the worker runs with other settings.

For the mails, add a webhook endpoint in Stripe for `https://<worker>/webhook`
that sends `invoice.paid`, plus `checkout.session.completed` and
`checkout.session.async_payment_succeeded` if you sell one time purchases.
Leave out the webhook secret or the mail service to serve no webhook.

To try it against Stripe test mode, `task worker -- MYAPP` runs the worker on
port 4242 with the Stripe CLI forwarding the three events to it. It takes the
Stripe key and the mail service from your environment.

### The key mail

The mail shows the key, its plan, how long it is valid and who it is for, as
HTML with a plain text twin. A dark header carries the name of the product or
its logo. These make it yours. Set them on the product under Edit, `worker
env` prints them.

- **Logo.** An https url. It sits on the dark header, 32 pixels high, so it
  has to read on a dark background.
- **Accent color.** Tints the key and colors the plan and the links. A pale or
  a dark color is adjusted where it would be hard to read.
- **Footer.** One line under the key, like where to get help.
- **Company details.** The last line of the mail. Many countries want a
  business to state who it is in its mail, in Germany that is the legal name,
  the address, the register number and the directors. What exactly you have
  to state is yours to check.

Links and email addresses in the footer and the company details can be
clicked.

Edit on a product has a Mail tab with a preview that follows what you type. It
runs the very code the worker sends the mail with.

For a mail of your own, change `render` in
[`worker/src/mail.ts`](worker/src/mail.ts). The worker is your code to deploy,
so anything that returns a subject, a text and an html body works.

### How it behaves

- **Mails.** The first payment always sends the key. A renewal only sends one
  when its period is longer than a month. Nobody pastes a key every month, and
  an app that is online refreshes on its own.
- **Refresh.** The answer is the key the subscription is owed now. After a
  renewal that is a new key with a later expiry. If nothing new was paid it is
  the same key again, so an old key never stops working as a ticket.
- **Hand issued keys.** A key with a `lic_…` id, or none, always comes back as
  it is.
- **Deleted customers.** Stripe cancels their subscriptions and forgets who
  they were. The dashboard keeps the last name and email it saw and marks the
  row, and a refresh keeps the email of the key it was sent.
- **Refunds.** Cancel a refunded subscription right away and its next key ends
  with the cancellation. A refunded purchase gets an expired key. An app that
  refreshes picks that up, see key days above.
- **Stripe is asked at most every five minutes per license** for keys that
  come in one after the other. A key that leaks is no easy way to spend your
  Stripe rate limit.
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

### A worker of your own

[WORKER.md](WORKER.md) describes the endpoints. Anything that speaks them is a
worker, in another language or for another payment provider. The dashboard
does not care who answers.

## What it does not do

- It cannot revoke a key that is out. Keys work offline, deleting a license
  only forgets it here. A key ends when it expires, so an unpaid subscription
  runs out with its last paid period plus the grace days. Key days keep that
  time short for apps that refresh.
- It does not bind a key to a machine or count seats.
- It has one password, no user accounts.
- One worker serves one product.
- A hand issued key cannot be renewed by a worker. It comes back as it is.

## Security

How a key is built, what it can and cannot protect, and where the secrets
live is in [SECURITY.md](SECURITY.md).

## License

Apache License 2.0 - See [LICENSE.md](LICENSE.md)
