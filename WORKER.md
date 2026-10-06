# The worker

The public side of one product. It asks the payment provider what is paid,
signs the keys of those licenses, mails them and refreshes them. It keeps no
state.

The one in [`worker`](worker) is the reference, in TypeScript and for Stripe.
Anything that speaks the endpoints below is a worker, in any language and for
any payment provider. [`worker/test/pages.json`](worker/test/pages.json) holds
what the reference answers on `/licenses`. The dashboard reads that same file
in its tests, so the two cannot drift apart unnoticed.

A worker signs keys the way [FORMAT.md](FORMAT.md) says. Sign the same license
into the same bytes as long as nothing about it changes, then an app that
refreshes can tell that it already has the current key.

## `POST /refresh`

An app sends the key it has as the plain text body and gets back the key its
license is owed now.

| Status | Body                                                  |
| ------ | ----------------------------------------------------- |
| `200`  | A key as plain text                                   |
| `400`  | Not a key of this product                             |
| `404`  | The key is real, there is no paid license behind it   |
| `413`  | Longer than 8 kB                                      |
| `502`  | The payment provider could not be asked, try again    |

1. Clean and verify the key like any verifier does. An expired key is fine,
   the renewal may come late. Anything else is a `400`, so only keys of the
   product ever reach the payment provider.
2. A key with a `lic_…` id or without an id was issued by hand. Answer with
   the cleaned key, it is all there is.
3. Look the license up by the id of the key and sign what it is owed now.
   When the provider no longer knows who the customer was, keep the email of
   the key that came in.

Answer from memory for a few minutes per license. A key that leaks must not be
a way to spend the rate limit of the provider.

## `POST /webhook`

The payment provider reports a payment and the worker mails the key. This one
belongs to the provider, the reference takes these Stripe events:

| Event                                      |                                 |
| ------------------------------------------ | ------------------------------- |
| `invoice.paid`                             | A subscription was paid         |
| `checkout.session.completed`               | A one time purchase went through |
| `checkout.session.async_payment_succeeded` | A slow payment method paid      |

It checks the `Stripe-Signature` header and takes events that are at most five
minutes old. It looks the license up again instead of trusting the event. What
it cannot finish is answered with an error, so Stripe tries again. The renewal
of a period of a month or less sends no mail, those keys are left to refresh.

## `GET /licenses`

The dashboard reads the paid licenses of the product, page by page.

```
GET /licenses?cursor=<next of the page before>&limit=50
Authorization: Bearer <INKAN_WORKER_TOKEN>
```

`cursor` is empty for the first page. `limit` is 1 to 100. A page can hold
fewer licenses than the limit, or none, and still have a `next`.

```json
{
  "product": {
    "prefix": "MYAPP",
    "public_key": "03a107bf…",
    "plan": "pro",
    "grace_days": 14,
    "key_days": 0,
    "stripe_product": "prod_…"
  },
  "licenses": [
    {
      "id": "sub_…",
      "email": "ops@acme.io",
      "customer": "Acme",
      "customer_gone": false,
      "plan": "team",
      "meta": { "seats": "5" },
      "expires_at": 1801209600,
      "status": "active",
      "renews": true,
      "created_at": 1700000000,
      "key": "MYAPP-AXsid-…"
    }
  ],
  "skipped": [],
  "next": "s:sub_…"
}
```

| Field           |                                                                      |
| --------------- | -------------------------------------------------------------------- |
| `product`       | What the worker runs with, the same on every page                    |
| `id`            | Id at the provider, never `lic_…`. The key carries it as its `id`    |
| `email`         | Who the license is for                                               |
| `customer`      | Display name, not part of the key                                    |
| `customer_gone` | The provider deleted the customer, `email` and `customer` are empty  |
| `plan`, `meta`  | As signed into the key                                               |
| `expires_at`    | Unix seconds, 0 never expires                                        |
| `status`        | Status at the provider, like `active`, `canceled`, `paid`, `refunded` |
| `renews`        | Whether the provider will bill another period                        |
| `created_at`    | Unix seconds                                                         |
| `key`           | The key a refresh gives this license right now                       |
| `skipped`       | What the worker could not list. `purchases` is the one there is      |
| `next`          | Cursor of the next page, `null` on the last one                      |

| Status | Body                                                    |
| ------ | ------------------------------------------------------- |
| `200`  | A page                                                  |
| `401`  | The token does not match                                |
| `404`  | The worker has no token, so it serves no `/licenses`    |
| `502`  | Why the payment provider could not be read, as text     |

What the dashboard does with it:

- It checks `prefix` and `public_key` against the product and reads nothing
  from a worker that signs for another one.
- It tells the user when `plan`, `grace_days`, `key_days` or `stripe_product`
  differ from what the product shows.
- It keeps a row per license and drops the rows a full read no longer lists.
  It keeps the one time purchases when `skipped` names them, and it drops
  nothing while `stripe_product` differs.
- It keeps the last `email` and `customer` it saw for a deleted customer. The
  `key` of that license carries no email then.

The token has at least 32 characters. Compare it in constant time.

## `GET /healthz`

`200` with `ok`.
