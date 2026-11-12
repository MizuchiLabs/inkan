# inkan worker

The public side of one inkan product. It reads Stripe, signs the keys of the
paid licenses, mails them and refreshes them. It stores nothing.

| Endpoint        |                                                             |
| --------------- | ----------------------------------------------------------- |
| `POST /refresh` | An app sends its key and gets the current one back          |
| `POST /webhook` | Stripe reports a payment, the key is mailed to the customer |
| `GET /licenses` | The inkan dashboard reads what the worker hands out         |

[WORKER.md](../WORKER.md) describes them in full.

## Deploy it

You do not need this folder to run a worker. The `inkan` binary carries the
built worker and writes it out, ready to deploy.
[STRIPE.md](../STRIPE.md#set-it-up) has the steps.

```sh
inkan worker export MYAPP
```

| File                 |                                                             |
| -------------------- | ----------------------------------------------------------- |
| `worker.keys.env`    | Prefix, signing key, token, plan and name of the product    |
| `worker.keys.json`   | The same for a console that takes JSON                      |
| `worker.env`         | Your settings. Made once, an export never touches it again  |
| `worker.env.example` | Every setting with what it does                             |
| `cloudflare.js`      | The worker for Cloudflare Workers, with `wrangler.jsonc`    |
| `node.js`            | The worker for Node, one file without dependencies          |
| `scaleway.zip`       | The worker for Scaleway Serverless Functions                |

The two keys files hold the private key. Delete them once the worker has
them.

Scaleway keeps the `SCW_` prefix for itself, so the worker reads its
Transactional Email key as `INKAN_SCW_SECRET_KEY`.

## Work on it

This folder is for changing the worker. It imports the verifier from
`../sdk/ts`, so it only builds inside the repository.

```sh
pnpm install
pnpm test
pnpm check
pnpm build
```

`pnpm build` writes the three files into `bundle`. `go generate ./worker`
does the same, and the next build of `inkan` carries them. To deploy a worker
you built here, copy the files from `bundle` over the ones in an exported
folder.

Node 24 or newer runs the TypeScript as it is, with the settings from the
environment. `PORT` is where it listens, 8080 when not set.

```sh
node --env-file=worker.keys.env --env-file=worker.env src/node.ts
```

`test/pages.json` is what `/licenses` answers for the fake Stripe of the
tests. The dashboard reads the same file in its own tests. After a deliberate
change of that answer, run `UPDATE=1 node --test` and the Go tests.

## Another mail service

Most serverless platforms cannot speak SMTP, so the mails go out through an
HTTP API. `src/send.ts` has one small function per service. Add yours there.

## A mail of your own

[`email`](../email) builds one from a React Email component and writes it
into `src/template.ts`. Build the worker after that. Any other html works
too: put it into `src/template.ts` with `{{key}}`, `{{name}}`, `{{plan}}`,
`{{email}}`, `{{expires}}`, `{{footer}}` and `{{company}}` in it. The worker
fills the placeholders in, and the `<title>` is the subject. `{{expires}}` is
when the license ends, a date like 16 October 2096, or Never. The footer and
the company details come from the settings, as for the built-in mail. A mail
without `{{key}}` is not used. The logo and accent settings only shape the
built-in mail.
