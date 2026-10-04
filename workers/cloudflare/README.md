# inkan worker for Cloudflare

The same worker as `inkan worker`, written for Cloudflare Workers. It serves
`POST /refresh` and `POST /webhook` for one product and stores nothing.

It signs the very keys the inkan ledger shows. Its tests run the sign vectors
of the Go package and the license cases of `internal/stripe`, so the two
cannot drift apart unnoticed.

## Deploy

```sh
pnpm install

# The settings of the product, straight from inkan.
inkan worker env MYAPP > worker.env
pnpm wrangler secret bulk worker.env

pnpm run deploy
```

`worker.env` holds the private key. Delete it once the secrets are set.

For the key mails, add three more secrets and a webhook endpoint in Stripe for
`https://<worker>/webhook` that sends `invoice.paid`,
`checkout.session.completed` and `checkout.session.async_payment_succeeded`.

| Secret                        |                                         |
| ----------------------------- | --------------------------------------- |
| `INKAN_STRIPE_WEBHOOK_SECRET` | Signing secret of the webhook endpoint  |
| `INKAN_MAIL_FROM`             | Sender of the key mails                 |
| `RESEND_API_KEY`              | API key of [Resend](https://resend.com) |

Cloudflare cannot speak SMTP, so the mails go through the API of Resend. To
send them another way, swap the `mail` function in `src/index.ts`.

## Differences to the Go worker

- Mail goes through Resend, not SMTP.
- The cache for refresh and the memory of sent events live in one isolate.
  They are shortcuts, the worker is correct without them.

Run the tests with `pnpm test`.
