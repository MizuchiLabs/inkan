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

The mail is the built in one of the Go worker. These plain variables shape it,
all of them are optional:

| Variable             |                                                         |
| -------------------- | ------------------------------------------------------- |
| `INKAN_PRODUCT_NAME` | How the mail calls the product                          |
| `INKAN_MAIL_LOGO`    | https url of a logo for the dark header, 32 pixels high |
| `INKAN_MAIL_ACCENT`  | Color like `#2f3e8f`                                    |
| `INKAN_MAIL_FOOTER`  | One line under the key, like where to get help          |
| `INKAN_MAIL_COMPANY` | Last line of the mail, your company details             |

For a mail of your own, change `render` in `src/mail.ts`. It is your code to
deploy, so anything that returns a subject, a text and an html body works.
The test that compares it with the mail of the Go worker then has to go.

## Differences to the Go worker

- Mail goes through Resend, not SMTP.
- There is no template directory, the mail is changed in `src/mail.ts`.
- The cache for refresh and the memory of sent events live in one isolate.
  They are shortcuts, the worker is correct without them.

Run the tests with `pnpm test`.
