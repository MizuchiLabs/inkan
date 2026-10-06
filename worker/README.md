# inkan worker

The public side of one inkan product. It reads Stripe, signs the keys of the
paid licenses, mails them and refreshes them. It stores nothing.

| Endpoint        |                                                             |
| --------------- | ----------------------------------------------------------- |
| `POST /refresh` | An app sends its key and gets the current one back          |
| `POST /webhook` | Stripe reports a payment, the key is mailed to the customer |
| `GET /licenses` | The inkan dashboard reads what the worker hands out         |

[WORKER.md](../WORKER.md) describes them in full.

## Settings

Print the settings of the product from inkan, then fill in the comments.

```sh
inkan worker env MYAPP > worker.env
```

`worker.env` holds the private key. Delete it once the worker has it.

| Setting                       |                                                           |
| ----------------------------- | --------------------------------------------------------- |
| `INKAN_STRIPE_KEY`            | A restricted key that reads subscriptions and customers   |
| `INKAN_STRIPE_PRODUCT`        | The Stripe product id, when the product has none set yet  |
| `INKAN_STRIPE_WEBHOOK_SECRET` | Signing secret of the webhook endpoint, for the key mails |
| `RESEND_API_KEY`              | Sends the mails through [Resend](https://resend.com)      |
| `SCW_SECRET_KEY`              | Or through Scaleway Transactional Email                   |
| `SCW_PROJECT_ID`              | Project of the Scaleway key                               |

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
pnpm run deploy
```

## Scaleway Serverless Functions

```sh
pnpm install
pnpm build
cd dist && zip function.zip scaleway.js package.json
```

Create a function on a Node runtime from `function.zip`, with the handler
`scaleway.handle` and the lines of `worker.env` as secrets.

## Anywhere Node runs

Node 24 or newer runs the TypeScript as it is. `PORT` is where it listens,
8080 when not set.

```sh
node --env-file=worker.env src/node.ts
```

`pnpm build` also writes `dist/node.js`, one file without dependencies for a
container or a server.

## Another mail service

Most serverless platforms cannot speak SMTP, so the mails go out through an
HTTP API. `src/send.ts` has one small function per service. Add yours there.

## A mail of your own

Change `render` in `src/mail.ts`. Anything that returns a subject, a text and
an html body works. The Mail tab of the dashboard previews this very function.

## Tests

```sh
pnpm test
pnpm check
```

`test/pages.json` is what `/licenses` answers for the fake Stripe of the
tests. The dashboard reads the same file in its own tests. After a deliberate
change of that answer, run `UPDATE=1 node --test` and the Go tests.
