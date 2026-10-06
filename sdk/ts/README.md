# inkan for TypeScript

Verifies [inkan](https://github.com/MizuchiLabs/inkan) license keys offline.
No dependencies, it uses WebCrypto. Works in Node 20+, Bun, Deno, Electron,
Tauri and Cloudflare Workers.

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

An app that is online can trade its key for the current one. Store what comes
back and start with it next time.

```ts
const key = await refresh("https://<worker>/refresh", oldKey, "MYAPP", publicKey);
```

Check the key where the user cannot change the code, in the main process of a
desktop app or on the server of a web app. A check in the browser is a hint,
not a lock.

Run the tests with `pnpm test`. They read the vectors of the Go package.
