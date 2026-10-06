# Paid licenses with Stripe

Every subscription and one time purchase of a Stripe product becomes a license
key. A small worker does it. It reads Stripe, signs the keys, mails them to
the customers and renews them for apps that are online. Keys you issue by hand
need none of this.

## What becomes a license

- **Stripe.** Give the worker a Stripe API key and a Stripe product id. Every
  subscription and one time purchase of that product is a license. Stripe
  stays the source of truth, the worker only reads. Once an hour the
  dashboard reads the list from the worker, Sync now on the Products page
  reads it right away. One read goes through every subscription and purchase
  of the Stripe account.
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
- **Max key days.** The longest a paid key lives before the app has to fetch a new
  one. It is the closest thing to taking a key back. A signed key works until
  it expires. Without a limit that is the end of the paid period plus the
  grace days, whatever happens in between. With one every paid key is cut
  short, so the app has to refresh and the next key says what Stripe says
  now. A key lasts between half and all of those days. That is what ends a
  license after a refund, a chargeback or an immediate cancellation, and what
  stops an old key from keeping a plan or seats the customer no longer pays
  for.

  | Product                                        | Max key days |
  | ---------------------------------------------- | ------------ |
  | App that runs offline                          | `0`          |
  | Monthly plans only                             | `0`          |
  | Yearly plans, app is online                    | `30`         |
  | One time purchases with refunds, app is online | `30`         |
  | Strict, the app is always online               | `14`         |

  Half of the days is how long the app may stay offline, so 30 allows two
  weeks. All of them is the longest a refunded key keeps working in an app
  that is kept from refreshing. An app that does refresh picks up a change
  within a day, with a limit or not.

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

Add the Stripe key to the file and deploy the worker with it. The worker is
deployed from a clone of this repository, it shares the verifier in `sdk/ts`.
[`worker`](worker) has the steps for Cloudflare Workers, Scaleway Functions
and any host that runs Node. Then enter where the worker runs under Edit on
the Products page. Its licenses show up on the Licenses page.

| Env Var                       | Default    | Description                                    |
| ----------------------------- | ---------- | ---------------------------------------------- |
| `INKAN_PREFIX`                | -          | Key prefix of the product                      |
| `INKAN_PRIVATE_KEY`           | -          | Signing key of the product                     |
| `INKAN_OLD_PUBLIC_KEYS`       | -          | Public keys from before a rotation, to refresh |
| `INKAN_STRIPE_KEY`            | -          | API key that reads subscriptions and customers |
| `INKAN_STRIPE_PRODUCT`        | -          | Its subscriptions and purchases are licenses   |
| `INKAN_PLAN`                  | -          | Plan for prices that set none                  |
| `INKAN_GRACE_DAYS`            | `14`       | Days a key outlives its paid period            |
| `INKAN_MAX_KEY_DAYS`          | `0`        | Longest life of a key in days, 0 has no limit  |
| `INKAN_WORKER_TOKEN`          | -          | Lets the dashboard read the licenses           |
| `INKAN_STRIPE_WEBHOOK_SECRET` | -          | Signing secret of the webhook endpoint         |
| `INKAN_MAIL_FROM`             | -          | Sender of the key mails                        |
| `RESEND_API_KEY`              | -          | Sends the mails through Resend                 |
| `SCW_SECRET_KEY`              | -          | Or through Scaleway Transactional Email        |
| `SCW_PROJECT_ID`              | -          | Project of the Scaleway key                    |
| `SCW_REGION`                  | `fr-par`   | Region of Scaleway Transactional Email         |
| `INKAN_PRODUCT_NAME`          | the prefix | How the key mail calls the product             |
| `INKAN_MAIL_LOGO`             | -          | https url of a logo for the dark header        |
| `INKAN_MAIL_ACCENT`           | `#2f3e8f`  | Color of the key mail                          |
| `INKAN_MAIL_FOOTER`           | -          | One line under the key, like where to get help |
| `INKAN_MAIL_COMPANY`          | -          | Last line of the mail, your company details    |

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

For a mail of your own, design it in [`email`](email) and build it into the
worker. The preview keeps showing the built-in mail.

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
  refreshes picks that up, see max key days above.
- **Stripe is asked about a license every five minutes at most** by one
  running worker, also about a license that is gone. A key that leaks is no
  easy way to spend your Stripe rate limit. A platform that runs many copies
  of the worker gives each its own memory, so there it is a brake and no hard
  limit.
- **Errors.** An app only accepts an answer that verifies as one of its keys.
  When a refresh fails the app keeps the key it has.

## A worker of your own

[WORKER.md](WORKER.md) describes the endpoints. Anything that speaks them is a
worker, in another language or for another payment provider. The dashboard
does not care who answers.
