# Paid licenses with Stripe

Every subscription and one time purchase of a Stripe product becomes a license
key. A small worker does it. It reads Stripe, signs the keys, mails them to
the customers and renews them for apps that are online. Keys you issue by hand
need none of this.

## Set it up

There are two parts. The dashboard stays private and holds the signing keys.
The worker is public and holds your Stripe key. You need no clone of the
repository, the worker comes out of the `inkan` binary.

1. **Export the worker** of a product. This writes the folder
   `inkan-worker-myapp` with the built worker and the keys of the product.

   ```sh
   inkan worker export MYAPP
   # or from the container of the quick start
   docker exec inkan inkan worker export MYAPP - | tar x
   cp -n inkan-worker-myapp/worker.env.example inkan-worker-myapp/worker.env
   ```

2. **Fill in `worker.env`** in that folder. The Stripe key and the Stripe
   products are all it needs to start, the rest is optional.
3. **Deploy it** from that folder, on one of these.

   ```sh
   # Cloudflare Workers
   npx wrangler deploy
   npx wrangler secret bulk worker.keys.env
   npx wrangler secret bulk worker.env

   # Anywhere Node 22 or newer runs
   node --env-file=worker.keys.env --env-file=worker.env node.js
   ```

   For Scaleway Serverless Functions, create a function on a Node runtime
   from `scaleway.zip`, switch its secrets to JSON, paste `worker.keys.json`
   and add your settings to it.

4. **Enter the url** of the worker under Edit on the Products page, in the
   Worker tab. Its licenses show up on the Licenses page, and the tab shows
   what the worker runs with.

`worker.keys.env` and `worker.keys.json` hold the signing key. Treat them
like one and delete them once the worker has them.

## What becomes a license

- **Stripe.** Give the worker a Stripe API key and the Stripe products it
  sells. Every subscription and one time purchase of them is a license.
  Stripe stays the source of truth, the worker only reads. Once an hour the
  dashboard reads the list from the worker, Sync now on the Products page
  asks for a read right away. One read goes through every subscription and
  purchase of the Stripe account, in the background. On a big account
  [reading by price](#a-big-stripe-account) keeps that short.
- **Metadata is the one source.** What a key says is set in Stripe metadata
  with three names. Put them on the Stripe product and they hold for everyone
  who buys it.

  | Metadata    | In the key                                                         |
  | ----------- | ------------------------------------------------------------------ |
  | `plan=team` | The plan. Without it the key gets the plan of the product in inkan |
  | `meta_…`    | An extra claim, `meta_tier=gold` becomes `tier=gold`               |
  | `days=365`  | A one time purchase lasts that long, without it forever            |

  All prices of a Stripe product, monthly or yearly, give the same key.
  Metadata on prices is not read, the Stripe dashboard locks a price once it
  has been used. An upgrade to another product keeps the subscription, so the
  next refresh brings a key of the new plan.

- **One customer, another deal.** The same three names on a single
  subscription or Checkout session win over the product. A customer who gets
  enterprise features on a team price is `plan=enterprise` on that
  subscription. Nobody else is touched, and the next refresh brings the new
  key. Only your Stripe account can edit a subscription. A Checkout session
  gets its metadata from the code that creates it, so never copy what a
  buyer typed into it.
- **One time purchases.** A paid Checkout session in payment mode is a
  license. A refunded or disputed purchase gets a key that ran out when it
  was bought.
  The Stripe key needs read access to Checkout sessions, payment intents and
  charges for this, without it only subscriptions are licenses.
- **Expiry.** A paid key is valid until the paid period ends, plus the grace
  days of the worker. The grace is the time a renewal has to get paid, so
  only a subscription that renews gets it. One that is canceled, set to
  cancel, paused or marked unpaid ends with its paid period. A trial gets one
  day, enough for the first paid period to take over. A subscription that
  falls behind on a payment keeps its last key and gets no new one.
  A new period counts once its invoice is paid. Stripe opens it about an
  hour before it charges, until then a refresh brings the last key. A
  customer who pays by invoice has the grace days to pay it.
- **Failed payments.** Let Stripe cancel a subscription once every retry
  failed, under Manage failed payments in its settings. A subscription left
  past due moves to a new period every billing cycle without a payment, and
  each one gets the grace days again.
- **Seats.** The quantity bought is signed into the key as the claim `seats`.
  It is always the quantity, `meta_seats` is left out.
- **Machines.** The metadata `fingerprint` on a subscription or a Checkout
  session binds its key to that machine. A Checkout session your site creates
  can carry the fingerprint the app shows. What the app does with it is in
  [the README](README.md#use-the-keys-in-your-app).
- **What the plan means is up to your app.** Inkan carries the plan and the
  claims as plain words. It does not know what `team` or `tier=gold` allow.
- **Max key days.** How long a paid key lives before the app has to fetch a
  new one. It is the closest thing to taking a key back. A signed key works
  until it expires. Without a limit that is the end of the paid period plus
  the grace days, whatever happens in between. With one every paid key is cut
  short, so the app has to refresh and the next key says what Stripe says
  now. That is what ends a license after a refund, a chargeback or an
  immediate cancellation, and what stops an old key from keeping a plan or
  seats the customer no longer pays for.

  | Product                                        | Max key days |
  | ---------------------------------------------- | ------------ |
  | App that runs offline                          | `0`          |
  | Monthly plans only                             | `0`          |
  | Yearly plans, app is online                    | `30`         |
  | One time purchases with refunds, app is online | `30`         |
  | Strict, the app is always online               | `14`         |

  A key lives that many days from when it is fetched, and up to a day more.
  So with 30 an app that refreshes daily may stay offline for 29 days, and a
  refunded key stops within 31. The license itself does not get shorter. The
  mail and the dashboard show when the license ends, only the key in hand
  runs out sooner. The key of such a license changes once a day.

## The worker

The worker is the small public part of one product, written in TypeScript. It
keeps no state. Stripe says what is paid and the private key signs it. One
worker serves one product, each export has a name of its own.

| Endpoint        |                                                             |
| --------------- | ----------------------------------------------------------- |
| `POST /refresh` | An app sends its key and gets the current one back          |
| `POST /webhook` | Stripe reports a payment, the key is mailed to the customer |
| `GET /licenses` | The dashboard reads what the worker hands out               |

The Stripe key is best a restricted key. Read access to these is all the
worker needs, it never writes to Stripe.

| Read access to    | For                                                    |
| ----------------- | ------------------------------------------------------ |
| Products          | The metadata of the products: `plan`, `meta_…`, `days` |
| Subscriptions     | Subscriptions are licenses                             |
| Customers         | The email and the name of a license                    |
| Invoices          | Whether a new period is paid yet                       |
| Checkout Sessions | One time purchases, leave it out if you sell none      |
| Payment Intents   | Whether a one time purchase was refunded               |
| Charges           | Whether a one time purchase was refunded or disputed   |
| Prices            | Only with `INKAN_STRIPE_BY_PRICE`, to list by price    |

A key without Products or Invoices makes the worker fail with Stripe's own
words on the Products page, the apps keep the keys they have.

| Env Var                       | Default    | Description                                    |
| ----------------------------- | ---------- | ---------------------------------------------- |
| `INKAN_PREFIX`                | -          | Key prefix of the product                      |
| `INKAN_PRIVATE_KEY`           | -          | Signing key of the product                     |
| `INKAN_OLD_PUBLIC_KEYS`       | -          | Public keys from before a rotation, to refresh |
| `INKAN_WORKER_TOKEN`          | -          | Lets the dashboard read the licenses           |
| `INKAN_PLAN`                  | -          | Plan for Stripe products without `plan`        |
| `INKAN_PRODUCT_NAME`          | the prefix | How the key mail calls the product             |
| `INKAN_STRIPE_KEY`            | -          | Restricted key with the read access above      |
| `INKAN_STRIPE_PRODUCT`        | -          | Stripe product ids like `prod_a,prod_b`        |
| `INKAN_STRIPE_BY_PRICE`       | -          | `1` reads subscriptions price by price         |
| `INKAN_GRACE_DAYS`            | `14`       | Days a renewal has to get paid                 |
| `INKAN_MAX_KEY_DAYS`          | `0`        | Days a key lives before a refresh, 0 no limit  |
| `INKAN_STRIPE_WEBHOOK_SECRET` | -          | Signing secret of the webhook endpoint         |
| `INKAN_MAIL_FROM`             | -          | Sender of the key mails                        |
| `INKAN_MAIL_REPLY_TO`         | the sender | Address an answer to the key mail goes to      |
| `RESEND_API_KEY`              | -          | Sends the mails through Resend                 |
| `INKAN_SCW_SECRET_KEY`        | -          | Or through Scaleway Transactional Email        |
| `INKAN_SCW_PROJECT_ID`        | -          | Project of the Scaleway key                    |
| `INKAN_SCW_REGION`            | `fr-par`   | Region of Scaleway Transactional Email         |
| `INKAN_MAIL_FOOTER`           | -          | One line under the key, like where to get help |
| `INKAN_MAIL_COMPANY`          | -          | Last line of the mail, your company details    |

The first six come from `inkan worker export` as `worker.keys.env`: the
prefix, the signing key, the old public keys, the token, the plan and the
name. Export again and deploy when one of them changes, the Products page
says so when the worker still runs with an old one. Everything else is yours
and lives in `worker.env`, which an export never touches. To change a
setting, edit it where the worker runs and deploy. The dashboard shows what
the worker reports, it does not edit it.

A value with a space or a `#` needs quotes in an env file, like
`INKAN_MAIL_FROM='My App <licenses@example.com>'`.

For the mails, add a webhook endpoint in Stripe for `https://<worker>/webhook`
that sends `invoice.paid`, plus `checkout.session.completed` and
`checkout.session.async_payment_succeeded` if you sell one time purchases.
Leave out the webhook secret or the mail service to serve no webhook.

To try it against Stripe test mode from a clone, `task worker -- MYAPP` runs
the worker on port 4242 with the Stripe CLI forwarding the three events to
it. It takes the Stripe key and the mail service from your environment or
from `worker/worker.env`.

### A big Stripe account

Stripe can list subscriptions by price but not by product. So by default a
read goes through every subscription of the account and keeps the ones of
your products. That is always complete, and slow when the account holds many
subscriptions of other products.

With `INKAN_STRIPE_BY_PRICE=1` the worker asks Stripe for the recurring prices
of your products, the archived ones too, and then only for the subscriptions
of each price. The rest of the account costs nothing then.

Only turn it on when every subscription of the products was made with a
price from the catalog. Stripe does not list inline prices, the ones a
checkout or an API call makes on the spot with `price_data`. A subscription
on such a price would be missing from the dashboard. Its key keeps working
and refreshing, the worker looks those up by id.

A product with more than a hundred active or a hundred archived prices is
read the default way. One time purchases have no such filter at Stripe, every
completed Checkout session of the account is read either way.

### The key mail

The mail shows the key, its plan, how long it is valid and who it is for, as
HTML with a plain text twin. A dark header carries the name of the product.
Two settings in `worker.env` add your words to it.

- **Footer.** One line under the key, like where to get help.
- **Company details.** The last line of the mail. Many countries want a
  business to state who it is in its mail, in Germany that is the legal name,
  the address, the register number and the directors. What exactly you have
  to state is yours to check.

Links and email addresses in the footer and the company details can be
clicked.

For your own logo, colors and words, send a mail of your own. Any html file
with `{{key}}` in it works:

```sh
inkan worker export MYAPP --mail mail.html
```

The export puts the mail into the worker and keeps it as `mail.html` in the
folder, so every later export sends it too. Delete the file and export again
to get the built-in mail back. With `-` as the dir there is no folder to look
in, so name the mail every time. From a container that is
`--mail /dev/stdin < mail.html` with `docker exec -i`.

[`email`](email) builds such a file from a React Email component, see
[its README](email/README.md) for the placeholders a mail can use.

### How it behaves

- **Mails.** The first payment always sends the key. A renewal only sends one
  when its period is longer than a month. Nobody pastes a key every month, and
  an app that is online refreshes on its own. So a monthly plan needs an app
  that refreshes. An airgapped install gets a yearly plan or a one time
  purchase.
- **Trials.** A trial is the first payment, so its key goes out when it
  starts. The first paid period counts as a renewal. A yearly plan mails its
  key then, a monthly one leaves it to the refresh.
- **Refresh.** The answer is the key the subscription is owed now. After a
  renewal that is a new key with a later expiry. If nothing new was paid it is
  the same key again, so an old key never stops working as a ticket. With max
  key days the key is a new one every day.
- **Hand issued keys.** A key with a `lic_…` id, or none, always comes back as
  it is.
- **Deleted customers.** Stripe cancels their subscriptions and forgets who
  they were. The dashboard keeps the last name and email it saw and marks the
  row, and a refresh keeps the email of the key it was sent.
- **Refunds.** Cancel a refunded subscription right away and its next key ends
  with the cancellation. A refunded purchase gets an expired key. An app that
  refreshes picks that up, see max key days above.
- **Chargebacks.** A disputed purchase gets an expired key like a refunded
  one, already while the dispute is open. It stays that way when you win, so
  send that customer a hand issued key then. A disputed subscription keeps
  running at Stripe. Let Stripe cancel it under Manage disputed payments in
  its settings, then its next key ends with the cancellation.
- **Stripe is asked about a license every 30 seconds at most** by one
  running worker, also about a license that is gone. A key that leaks is no
  easy way to spend your Stripe rate limit. A change in Stripe, like new
  seats, reaches an app that refreshes within those 30 seconds. The metadata
  of the Stripe products is asked for as often, once for all their licenses.
  A platform that runs many copies of the worker gives each its own memory,
  so there it is a brake and no hard limit.
- **Errors.** An app only accepts an answer that verifies as one of its keys.
  When a refresh fails the app keeps the key it has.

## A worker of your own

[WORKER.md](WORKER.md) describes the endpoints. Anything that speaks them is a
worker, in another language or for another payment provider. The dashboard
does not care who answers.
