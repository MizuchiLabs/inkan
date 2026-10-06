import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test, type TestContext } from "node:test";

import { Keeper, LicenseError, renewWait, type Claims } from "../src/index.ts";

type Vectors = {
  public_key: string;
  now: number;
  verify: { name: string; key: string; result: string; claims: Claims | null }[];
  renew: { name: string; exp: number; failures: number; wait: number }[];
};

const vectors: Vectors = JSON.parse(
  readFileSync(new URL("../../../license/testdata/vectors.json", import.meta.url), "utf8"),
);
const good = vectors.verify[0];
const goodExp = good.claims!.exp;
const expired = vectors.verify.find((c) => c.result === "expired")!.key;
const withMeta = vectors.verify.find((c) => c.name === "a key with meta")!.key;
const endpoint = "https://keys.example.com/refresh";

for (const c of vectors.renew) {
  test(`renew: ${c.name}`, () => {
    assert.equal(renewWait(c.exp, vectors.now, c.failures), c.wait);
  });
}

// timers stands in for setTimeout. next runs the wait the keeper is in and
// comes back when the try behind it is over. That works because the keeper
// hands its try to setTimeout as it is.
function timers(t: TestContext) {
  const waits: number[] = [];
  let fire: () => unknown = () => {};
  t.mock.method(globalThis, "setTimeout", ((fn: () => unknown, ms: number) => {
    waits.push(ms / 1000);
    fire = fn;
    return 0;
  }) as unknown as typeof setTimeout);
  return {
    waits,
    next: async () => {
      await fire();
    },
  };
}

test("a keeper holds a key and checks its expiry on every call", async (t) => {
  const lic = new Keeper("MYAPP", vectors.public_key);
  assert.equal(lic.valid(), false);
  assert.equal(lic.key(), "");
  assert.throws(() => lic.claims(), { code: "nokey" });

  const claims = await lic.set(` "${good.key}"\n`);
  assert.deepEqual(claims, good.claims);
  assert.equal(lic.key(), good.key, "the key is held clean, the way an app stores it");
  assert.equal(lic.valid(), true);

  await assert.rejects(lic.set("MYAPP-nope"), { code: "format" });
  assert.equal(lic.key(), good.key, "a key that is turned down changes nothing");

  // The key runs out while the app runs.
  t.mock.timers.enable({ apis: ["Date"], now: (goodExp + 1) * 1000 });
  assert.equal(lic.valid(), false);
  assert.throws(
    () => lic.claims(),
    (err) => err instanceof LicenseError && err.code === "expired" && err.claims?.id === "sub_123",
  );
});

test("an expired key is held, renew can trade it for the current one", async () => {
  const lic = new Keeper("MYAPP", vectors.public_key);
  await assert.rejects(lic.set(expired), { code: "expired" });
  assert.equal(lic.key(), expired);
  assert.equal(lic.valid(), false);
});

test("the claims a keeper hands out are a copy", async () => {
  const lic = new Keeper("MYAPP", vectors.public_key);
  const claims = await lic.set(withMeta);
  claims.plan = "enterprise";
  claims.meta!.seats = "9999";
  assert.equal(lic.claims().plan, "team");
  assert.equal(lic.claims().meta?.seats, "5");
});

test("renew trades the held key for the current one", async (t) => {
  const clock = timers(t);
  // The worker is unreachable on the first try and has a new key after that.
  const sent: string[] = [];
  t.mock.method(globalThis, "fetch", async (_input: unknown, init?: RequestInit) => {
    sent.push(String(init?.body));
    if (sent.length === 1) throw new Error("no route to host");
    return new Response(good.key + "\n");
  });

  const lic = new Keeper("MYAPP", vectors.public_key);
  await assert.rejects(lic.set(expired), { code: "expired" });
  const saved: string[] = [];
  const failed: string[] = [];
  lic.renew(endpoint, {
    save: (key) => void saved.push(key),
    failed: (err) => void failed.push(String(err)),
  });
  await clock.next();
  await clock.next();

  assert.deepEqual(clock.waits, [0, 60, 86_400]);
  assert.deepEqual(sent, [expired, expired], "the worker gets the held key every time");
  assert.deepEqual(saved, [good.key], "the new key is saved once");
  assert.equal(failed.length, 1);
  assert.match(failed[0], /no route to host/);
  assert.equal(lic.key(), good.key);
  assert.equal(lic.valid(), true);

  // Nothing new was paid, the worker answers with the same key.
  await clock.next();
  assert.deepEqual(saved, [good.key], "the same key again is not saved again");
});

test("a key that runs out before tomorrow is asked about sooner", async (t) => {
  t.mock.timers.enable({ apis: ["Date"], now: (goodExp - 16 * 3600) * 1000 });
  const clock = timers(t);
  t.mock.method(globalThis, "fetch", async () => new Response(good.key));

  const lic = new Keeper("MYAPP", vectors.public_key);
  await lic.set(good.key);
  lic.renew(endpoint);
  await clock.next();
  assert.deepEqual(clock.waits, [0, 8 * 3600]);
});

test("a keeper without a key asks for nothing and looks again in a minute", async (t) => {
  const clock = timers(t);
  const fetched = t.mock.method(globalThis, "fetch", async () => new Response(good.key));

  new Keeper("MYAPP", vectors.public_key).renew(endpoint);
  await clock.next();
  await clock.next();
  assert.deepEqual(clock.waits, [0, 60, 60]);
  assert.equal(fetched.mock.callCount(), 0);
});

test("a stopped keeper drops the answer it waited for and sets no more timers", async (t) => {
  const clock = timers(t);
  const stop = new AbortController();
  t.mock.method(globalThis, "fetch", async () => {
    stop.abort();
    return new Response(good.key);
  });

  const lic = new Keeper("MYAPP", vectors.public_key);
  await assert.rejects(lic.set(expired), { code: "expired" });
  const saved: string[] = [];
  lic.renew(endpoint, { save: (key) => void saved.push(key), signal: stop.signal });
  await clock.next();

  assert.deepEqual(clock.waits, [0]);
  assert.equal(lic.key(), expired);
  assert.deepEqual(saved, []);
});
