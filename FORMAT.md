# The key format

Everything a verifier or signer in another language needs. The Go package
[`license`](license) is the reference, the test vectors in
[`license/testdata/vectors.json`](license/testdata/vectors.json) are the
contract.

## A key

```
MYAPP-AXsid-iI6MS-wiYXB-wIjoi-...
```

1. The claims are encoded as JSON. That is the payload.
2. The payload is signed with the Ed25519 private key of the product.
3. One version byte (`1`), the payload and the 64-byte signature are joined,
   base64 encoded with the standard alphabet and no padding, cut into chunks
   of five and prefixed with the uppercase app name.

| Claim   | Type   |                                                 |
| ------- | ------ | ----------------------------------------------- |
| `app`   | string | The product prefix                              |
| `plan`  | string | Plan name, the app decides what it means        |
| `email` | string | Who the license is for                          |
| `exp`   | number | Unix seconds, 0 never expires                   |
| `id`    | string | Optional. Id at whoever issued it, see below    |
| `fp`    | string | Optional. The machine the key is bound to       |
| `meta`  | object | Optional. Extra strings, e.g. seats             |

The id is the one the license has at whoever issued it. `lic_…` was issued by
hand in inkan, anything else belongs to the payment provider, at Stripe that
looks like `sub_…` or `cs_…`. Old keys have none.

The fingerprint is what the app calls the machine the key is for, like a hash
of its machine id or an install id it keeps in its data directory. The app
decides, inkan only carries it. A key without one works on every machine.

## Verify

1. Clean the key. Drop every character of the list below, wherever it is,
   then all quotes (`"` and `'`) from both ends.
2. The key must start with the uppercase app name and a dash. If not, the
   result is `format`. Only `a` to `z` are uppercased, every other character
   stays as it is.
3. Drop the dashes from the rest and decode it as base64 without padding. On
   an error, with fewer than 66 bytes, or with a first byte other than `1`,
   the result is `format`.
4. The last 64 bytes are the signature, everything between is the payload.
   Check the signature over the payload bytes as they are with every public
   key you carry. If none fits, the result is `signature`.
5. Parse the payload as JSON by the rules below. A missing claim is its zero
   value, unknown claims are ignored. Anything that is not an object, or a
   claim of the wrong type, gives `format`.
6. `app` must equal your app name exactly, case included. Else `app`.
7. If the key has `fp` and the app gave you a fingerprint that is not it, the
   result is `fingerprint`. Hand the claims back with it, so the app can say
   which machine the key is for. Without a fingerprint nothing is compared,
   the dashboard and the worker have none to give.
8. If `exp` is not 0 and the time is past it, the result is `expired`. Hand
   the claims back with it, so the app can say whose license ran out.

Never parse the JSON before the signature is checked. Never sign or verify
anything but the payload bytes as they arrived, a signer may format its JSON
any way it likes.

A public key is 32 bytes as hex. An app can carry several, separated by
commas, while a signing key rotates.

### What cleaning drops

The characters Unicode gives the property White_Space, plus the zero width
space and the byte order mark. Spell the list out, the whitespace of a regex
or a `trim` is another one in every language.

| Code points          |                                             |
| -------------------- | ------------------------------------------- |
| `U+0009` to `U+000D` | Tab, line feed, vertical tab, form feed, carriage return |
| `U+0020`             | Space                                       |
| `U+0085`             | Next line                                   |
| `U+00A0`             | No-break space                              |
| `U+1680`             | Ogham space mark                            |
| `U+2000` to `U+200A` | The spaces of typography                    |
| `U+200B`             | Zero width space                            |
| `U+2028`, `U+2029`   | Line and paragraph separator                |
| `U+202F`, `U+205F`   | Narrow no-break space, math space           |
| `U+3000`             | Ideographic space                           |
| `U+FEFF`             | Byte order mark                             |

Nothing else is dropped. A control character like `U+001C`, a zero width
joiner or a curly quote stays, and the key then fails as `format`.

### How the payload is read

The JSON parser of a language is rarely strict enough alone. These rules make
every verifier give the same answer for the same bytes. Only someone with the
private key can sign a payload that breaks them, so they guard against a
careless signer, not against a forger.

- **Bytes.** The payload is valid UTF-8 and strict JSON. No byte order mark,
  no `NaN` or `Infinity`, nothing after the value. Whitespace around it is
  fine. Anything else is `format`.
- **Object.** The payload is a JSON object. `null`, a list or a bare value is
  `format`.
- **Names.** A claim name matches exactly. `App` or `EXP` is an unknown claim
  and ignored.
- **Twice.** When a name is there twice, the last one counts. The same goes
  for the names in `meta`.
- **Null.** A claim that is `null` is treated as missing. A `null` inside
  `meta` is `format`.
- **Strings.** `app`, `plan`, `email`, `id` and `fp` are strings. `meta` is an
  object and all its values are strings.
- **Escapes.** A `\u` escape that is half of a surrogate pair, like
  `"\ud83d"` alone, is `format`, also in an unknown claim. A whole pair is
  fine.
- **`exp`.** A JSON number, judged by its value as a 64-bit float and not by
  how it is written. It must be whole and between -(2^53 - 1) and 2^53 - 1.
  So `4.1e9` is 4100000000 and `-0.0` is 0. `1.5`, `9007199254740992` and
  `1e30` are `format`. A negative value is a time before 1970, so the key
  ran out.

Base64 leaves up to four bits in the last character that belong to no byte.
They are ignored, as every common decoder does.

## Refresh

`POST` the key as the plain text body to the refresh URL of the worker. The
answer is `200` with a key as plain text, or an error with a reason. Verify
the answer like any key before you keep it, only `valid` and `expired` are
keys. On any error keep the key you have. [WORKER.md](WORKER.md) has the side
of the worker.

### When to refresh

An app that stays up refreshes on a schedule. The keepers of the Go package,
[`sdk/ts`](sdk/ts) and [`sdk/python`](sdk/python) follow this one, and the
`renew` test vectors pin it.

1. Refresh once right away.
2. After a refresh that worked, wait a day. If the key runs out before that,
   wait half of the time it has left, but never less than a minute. A key that
   never expires or has already run out waits the day.
3. After a refresh that failed, wait a minute. Wait twice as long after every
   further failure in a row, up to a day. The first refresh that works ends
   the row.
4. Without a key there is nothing to refresh. Look again in a minute.

Half of the time left is what makes max key days work. A worker with that
limit hands out keys that live between half and all of those days, and it has
the next key ready once the held one is down to half of them. Half of what is
left always lands in that time. Once a day alone lets a key of a day or two
run out.

## Sign

Only needed for a worker of your own. Verifiers can skip this.

Encode the claims as a JSON object and sign those bytes. A verifier checks the
bytes as they arrive, so the order of the fields, the whitespace and the
escaping are yours to choose. Write `app` as the product prefix and leave out
`id`, `fp` and `meta` when they are empty. Write `exp` as a plain whole
number and each name once, and stay inside
[what a verifier reads](#how-the-payload-is-read).

Keep the encoding stable. Ed25519 signatures are deterministic, so the same
bytes always give the same key, and an app that refreshes can tell that
nothing changed.

The private key inkan stores is base64 of 64 bytes, the seed is the first 32
and the public key the last 32.

## Test vectors

`license/testdata/vectors.json` is generated by the Go package and pinned by
its tests.

| Field        |                                                              |
| ------------ | ------------------------------------------------------------ |
| `seed`       | Hex seed of the signing key, the bytes 0 to 31               |
| `public_key` | Hex public key to verify with                                |
| `now`        | Unix time to check the expiry against                        |
| `verify`     | A `key`, `app` and maybe a `fingerprint`, with the `result` and `claims` to expect |
| `sign`       | `claims` with the `payload` and `key` the Go package gives   |
| `renew`      | An `exp` and `failures` with the `wait` in seconds to expect |

`result` is `valid`, `expired`, `format`, `signature`, `app` or
`fingerprint`. `claims` is set for `valid`, `expired` and `fingerprint` and
null otherwise. `fingerprint` is the one the verifier is given, missing when
it gets none.

In a `renew` case `exp` is the expiry of the held key and `failures` counts
the refreshes in a row that failed. The time is `now`.

A verifier is done when it passes every `verify` case, a keeper when it
passes every `renew` case. The second half of the `verify` cases are the
edges of the rules above, one case each. [`sdk/ts`](sdk/ts) and [`sdk/python`](sdk/python)
can serve as a template, the verifier in each is about a hundred lines.
