# inkan worker

The public side of one inkan product. It reads Stripe, signs the keys of the
paid licenses, mails them and refreshes them. It stores nothing.

Deploy it from a clone of the whole repository. It imports the verifier from
`../sdk/ts`, this folder alone does not build.

| Endpoint        |                                                             |
| --------------- | ----------------------------------------------------------- |
| `POST /refresh` | An app sends its key and gets the current one back          |
| `POST /webhook` | Stripe reports a payment, the key is mailed to the customer |
| `GET /licenses` | The inkan dashboard reads what the worker hands out         |

[WORKER.md](../WORKER.md) describes them in full.

## Settings

Write the settings of the product from inkan into this folder, then fill in
the comments.

```sh
inkan worker export MYAPP
# or from a container, a tar of the files comes out of stdout
docker exec inkan inkan worker export MYAPP - | tar x
# or to stdout
docker exec inkan inkan worker export MYAPP - | tar -xO worker.env        # the env file on screen
docker exec inkan inkan worker export MYAPP - | tar -xO src/template.ts   # the mail module
docker exec inkan inkan worker export MYAPP - | tar -xO worker.env > worker.env
```

It writes `worker.env`, the same settings as `worker.json` for a console
that takes JSON, and `src/template.ts`, the key mail of the product. The
first two hold the private key. Delete them once the worker has them. Values
with a space or a `#` are in quotes in the env file, Node and wrangler read
them as they are.

| Setting                       |                                                           |
| ----------------------------- | --------------------------------------------------------- |
| `INKAN_STRIPE_KEY`            | A restricted key that reads subscriptions and customers   |
| `INKAN_STRIPE_PRODUCT`        | The Stripe product id, when the product has none set yet  |
| `INKAN_STRIPE_WEBHOOK_SECRET` | Signing secret of the webhook endpoint, for the key mails |
| `RESEND_API_KEY`              | Sends the mails through [Resend](https://resend.com)      |
| `INKAN_SCW_SECRET_KEY`        | Or through Scaleway Transactional Email                   |
| `INKAN_SCW_PROJECT_ID`        | Project of the Scaleway key                               |

One time purchases also need read access to Checkout sessions, payment intents
and charges.

For the key mails, add a webhook endpoint in Stripe for
`https://<worker>/webhook` that sends `invoice.paid`,
`checkout.session.completed` and `checkout.session.async_payment_succeeded`.
Without the webhook secret, a sender or a mail service there is no `/webhook`
and the worker only refreshes.

Once it runs, enter its url on the product in inkan, under Edit in the Worker
tab.

## Cloudflare Workers

```sh
pnpm install
pnpm wrangler secret bulk worker.env
pnpm deploy
```

## Scaleway Serverless Functions

```sh
pnpm install
pnpm build:scaleway
```

Create a function on a Node runtime from `dist/scaleway.zip`, with every
setting at its default. Switch the Secrets to JSON, paste `worker.json` and
add the Stripe key and the mail service to it. Scaleway keeps the `SCW_`
prefix for itself there, so the worker reads its Transactional Email key as
`INKAN_SCW_SECRET_KEY`.

## Anywhere Node runs

Node 24 or newer runs the TypeScript as it is. `PORT` is where it listens,
8080 when not set.

```sh
node --env-file=worker.env src/node.ts
```

`pnpm build:node` writes `dist/node.js`, one file without dependencies for a
container or a server.

## Another mail service

Most serverless platforms cannot speak SMTP, so the mails go out through an
HTTP API. `src/send.ts` has one small function per service. Add yours there.

## A mail of your own

Upload it on the product in inkan, under Edit in the Mail tab, as html with
`{{key}}`, `{{name}}`, `{{plan}}`, `{{email}}`, `{{expires}}`, `{{footer}}`
and `{{company}}` in it. `worker export` writes it into `src/template.ts`,
the worker fills the placeholders in, and the `<title>` is the subject.
`{{expires}}` is a date like 16 October 2096, or Never. The footer and the
company details are the ones of the product, as for the built-in mail. A mail
without `{{key}}` is not taken.

[`email`](../email) builds such a mail from a React Email component, start
there. Any other html works too. The logo and accent settings only shape the
built-in mail.

`/licenses` names the mail the worker sends by its SHA-256, so the dashboard
can say when the one uploaded is not deployed yet.

## Tests

```sh
pnpm test
pnpm check
```

`test/pages.json` is what `/licenses` answers for the fake Stripe of the
tests. The dashboard reads the same file in its own tests. After a deliberate
change of that answer, run `UPDATE=1 node --test` and the Go tests.
