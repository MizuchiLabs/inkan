# inkan for TypeScript

Verifies [inkan](https://github.com/MizuchiLabs/inkan) license keys offline,
and holds and renews the key of a running app. No dependencies, it uses
WebCrypto. Works in Node 20+, Bun, Deno, Electron, Tauri and Cloudflare
Workers.

It is not on npm yet. Copy [`src/index.ts`](src/index.ts) into your app as
`inkan.ts`, it is one file.

```ts
import { LicenseError, refresh, verify } from "./inkan.ts";

// The hex key from the Products page of inkan.
const publicKey = "03a107bf…";

try {
  const claims = await verify(process.env.MYAPP_LICENSE_KEY ?? "", "MYAPP", publicKey);
  console.log(claims.plan, claims.meta?.seats);
} catch (err) {
  if (err instanceof LicenseError && err.code === "expired") {
    console.log(`the license of ${err.claims?.email} ran out`);
  }
}
```

## In an app that stays up

A `Keeper` holds the key, so it can change while the app runs.

```ts
import { Keeper } from "./inkan.ts";

const lic = new Keeper("MYAPP", publicKey);
try {
  await lic.set(storedKey);
} catch (err) {
  // Not a key of this app. A key that ran out is held all the same.
}
lic.renew("https://<worker>/refresh", { save: (key) => store(key) });

A third argument binds the keeper to this machine. It is whatever the app
calls the machine, like an install id it keeps in its data directory. A key
issued with that fingerprint works nowhere else, a key without one works
everywhere. `verify` takes it as `{ fingerprint }`.

lic.valid(); // is there a license right now
lic.claims(); // plan, email and meta, throws a LicenseError without one
await lic.set(pasted); // a new key came by mail
```

- **Live.** `valid` and `claims` check the expiry on every call and need no
  `await`, so a running app notices when its key runs out.
- **Swap.** `set` replaces the key while the app runs. A key it turns down
  changes nothing.
- **Renew.** `renew` asks the worker right away and then once a day, sooner
  for a key that runs out before that. Every new key goes to `save`, so the
  app can store it for its next start. `failed` gets the error of a try that
  did not work, and an `AbortSignal` passed as `signal` ends it. This needs
  the worker of the product.

A keeper is for a process that stays up. On Cloudflare Workers and wherever
else every request starts from nothing, call `verify` per request.

`refresh` is the one step behind `renew`, for an app with a loop of its own.
It trades a key for the current one. `renewWait` says how long to wait until
the next one.

```ts
const key = await refresh("https://<worker>/refresh", oldKey, "MYAPP", publicKey);
```

Check the key where the user cannot change the code, in the main process of a
desktop app or on the server of a web app. A check in the browser is a hint,
not a lock.

Run the tests with `pnpm test`. They read the vectors of the Go package.
