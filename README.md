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
- Small packages for Go, TypeScript and Python that verify, hold and renew
  keys, a documented format for every other language
- Paste any key to see what is inside
- The dashboard is one binary with SQLite and a web UI, Docker-ready

## Quick Start

1. **Run it.** `inkan` starts the dashboard on http://127.0.0.1:3000. See
   [Install](#install) for Docker and the binary.
2. **Add a product.** A name, a key prefix like `MYAPP` and a plan. It gets a
   signing key of its own.
3. **Copy the public key** from the Products page into your app.
4. **Verify.** Three lines in Go, see
   [Use the keys in your app](#use-the-keys-in-your-app). TypeScript, Python
   and every other language work too.
5. **Issue a key** on the Licenses page and paste it into your app.

That is all an app with hand issued keys needs. To sell licenses, let Stripe
subscriptions and purchases become keys with the worker, see
[STRIPE.md](STRIPE.md).

## Install

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

Without a password Inkan only listens on localhost and only answers when it
is asked for as `localhost` or `127.0.0.1`. A container listens on all of its
addresses, so it needs one. The examples above still publish the port on
localhost only, put your own proxy or VPN in front to reach it from elsewhere.
A proxy that asks under another name needs the password too.

## Configuration

| Env Var           | Flag          | Default          | Description                |
| ----------------- | ------------- | ---------------- | -------------------------- |
| `INKAN_ADDR`      | `--addr`      | `127.0.0.1:3000` | Address to listen on       |
| `INKAN_DATA_DIR`  | `--data-dir`  | `data`           | Directory for the database |
| `INKAN_PASSWORD`  | `--password`  | -                | Password for the UI        |
| `INKAN_API_TOKEN` | `--api-token` | -                | Bearer token for scripts   |
| `INKAN_DEBUG`     | `--debug`     | `false`          | Enable debug logging       |

A script that issues or reads licenses sends the token instead of signing in.
The API is described at `/docs`.

```sh
curl -H "Authorization: Bearer $INKAN_API_TOKEN" http://127.0.0.1:3000/api/licenses
```

## Use the keys in your app

```go
import "github.com/mizuchilabs/inkan/license"

// The public key from the Products page. It is no secret.
const publicKey = "03a107bf…"

lic := license.NewKeeper("MYAPP", publicKey, "")
_, err := lic.Set(os.Getenv("MYAPP_LICENSE_KEY"))

ok := lic.Valid()             // is there a license right now
claims, err := lic.Claims()   // plan, email and meta, or why there is none
claims, err = lic.Set(pasted) // a new key came by mail
```

- **Live.** `Valid` and `Claims` check the expiry on every call, so a running
  app notices when its key runs out.
- **Swap.** `Set` replaces the key while the app runs. It is what a settings
  page calls with a pasted key. A key it turns down changes nothing.
- **Once.** `license.Verify(key, "MYAPP", publicKey, "")` checks a single
  key, for an app that needs no more than that.
- **Bound.** The last argument is the fingerprint of this machine, in whatever
  way the app names it, like a hash of its machine id or an install id it
  writes to its data directory at first start. A key issued with that
  fingerprint works nowhere else, a key without one works everywhere, and an
  app that passes `""` checks nothing. Show the fingerprint on the license
  page so the customer can send it to you, then set it on the license under
  Edit, or in the Stripe metadata, see [STRIPE.md](STRIPE.md).
- **Open source.** An app whose source is public can keep the key out of it,
  so that only its own builds take licenses. Declare `var publicKey string`
  and set it in the release build:

  ```sh
  go build -ldflags="-X main.publicKey=<hex>"
  ```

  A build without it verifies no key, `Set` answers `license.ErrNoPublicKey`
  and `Valid` is always false.

The `license` package is a Go module of its own with no dependencies, so it
adds nothing else to your build.

### Renew from your app

```go
go lic.Renew(ctx, "https://<worker>/refresh", save, nil)
```

This needs the [worker](STRIPE.md#the-worker) of the product.

The `Keeper` asks right away and then once a day, or sooner for a key that
runs out before that. Every new key goes to `save`, so the app can store it
for its next start. The last argument gets the error of a try that did not
work, for an app that wants to log it. The package itself logs nothing.
Airgapped installs skip this and paste the key from the mail.

It is a plain POST with the key as the body, so any language can do it:

```sh
curl -X POST https://<worker>/refresh --data "$MYAPP_LICENSE_KEY"
```

### Other languages

| Language   |                                                   |
| ---------- | ------------------------------------------------- |
| TypeScript | [`sdk/ts`](sdk/ts), no dependencies               |
| Python     | [`sdk/python`](sdk/python), one file              |
| Any other  | [FORMAT.md](FORMAT.md) and the test vectors in it |

Neither is on npm or PyPI yet. Each is one file, copy it into your app. Both
have the same `Keeper` as the Go package.

Check the key where the user cannot change the code. For a web app that is
the server, a check in the browser is a hint and not a lock.

## How it works

There are two parts. The dashboard is this binary. It holds the signing keys,
issues keys by hand and shows every license. It stays private. The
[worker](STRIPE.md#the-worker) is the public side of one product. It reads
Stripe, signs the keys of the paid licenses, mails them and refreshes them.
The dashboard reads the paid licenses from the worker, it never talks to
Stripe.

Keys you issue by hand need no worker. [STRIPE.md](STRIPE.md) has everything
about the paid ones.

- **Products.** A product is a name, a key prefix, a plan and a keypair. Add a
  new one and you get a fresh keypair, or paste the private key of a product
  that already ships. The Products page shows the public key your app
  verifies with.
- **Licenses.** A license is a row: who, which plan, until when, plus optional
  extra claims. A manual key is signed from the row on demand. Change a signed
  field and the key changes with it. A paid license shows the key its worker
  hands out.
- **History.** Once a day the active licenses are counted per product and
  plan. The Licenses page shows how they moved.
- **Check.** Paste any key on the Licenses page. Inkan verifies it the way the
  product would and shows the claims inside.

## Backups

The signing key of a product cannot be made again. Lose it and no app that
ships with its public key takes a new license. Keep it in two places.

```sh
inkan keys              # prefix and signing key of every product
inkan backup            # a copy of the database, next to it
inkan backup /mnt/safe/inkan.db
```

- **Keys.** `inkan keys` prints secrets. Put its lines into a password
  manager. To get a product back, add one with the same prefix and paste the
  key as its existing private key.
- **Database.** `inkan backup` also works while Inkan runs. The copy holds the
  signing keys, the hand issued licenses and the notes. To restore it, stop
  Inkan and put the copy in place of `inkan.db`. Delete `inkan.db-wal` and
  `inkan.db-shm` if they are there. They are what a crash leaves of the old
  database and would be played back over the copy.
- **Upgrades.** Before Inkan changes the schema on an upgrade it writes a copy
  to `inkan.db.<date>.backup` in the data directory. They are never deleted,
  clear out old ones yourself.

Paid licenses need no backup. They come back from the worker with the next
sync.

## What it does not do

- It cannot revoke a key that is out. Keys work offline, deleting a license
  only forgets it here. A key ends when it expires, so an unpaid subscription
  runs out with its last paid period plus the grace days.
  [Max key days](STRIPE.md#what-becomes-a-license) keep that time short for
  apps that refresh.
- It does not count seats or see which machines run a key. A key can be bound
  to one machine, but only the app on that machine checks it.
- It has one password, no user accounts.
- One worker serves one product.
- A hand issued key cannot be renewed by a worker. It comes back as it is.

## Security

How a key is built, what it can and cannot protect, where the secrets live
and how to rotate a signing key is in [SECURITY.md](SECURITY.md).

## License

Apache License 2.0 - See [LICENSE.md](LICENSE.md)
