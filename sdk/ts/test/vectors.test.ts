import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createServer } from "node:http";
import { test } from "node:test";

import { LicenseError, refresh, verify, type Claims } from "../src/index.ts";

type Vectors = {
  public_key: string;
  now: number;
  verify: {
    name: string;
    key: string;
    app: string;
    fingerprint?: string;
    result: string;
    claims: Claims | null;
  }[];
};

const vectors: Vectors = JSON.parse(
  readFileSync(new URL("../../../license/testdata/vectors.json", import.meta.url), "utf8"),
);

for (const c of vectors.verify) {
  test(`verify: ${c.name}`, async () => {
    let result = "valid";
    let claims: Claims | null = null;
    try {
      claims = await verify(c.key, c.app, vectors.public_key, {
        fingerprint: c.fingerprint,
        now: vectors.now,
      });
    } catch (err) {
      assert.ok(err instanceof LicenseError, String(err));
      result = err.code;
      claims = err.claims ?? null;
    }
    assert.equal(result, c.result);
    assert.deepEqual(claims, c.claims);
  });
}

test("several public keys, any may have signed", async () => {
  const other = "00".repeat(32);
  const good = vectors.verify[0];
  const now = { now: vectors.now };
  await verify(good.key, good.app, `${other}, ${vectors.public_key}`, now);
  await assert.rejects(verify(good.key, good.app, other, now), { code: "signature" });
  await assert.rejects(verify(good.key, good.app, "nope", now), /public key/);
});

test("refresh only returns a key of this app", async () => {
  const good = vectors.verify[0];
  const expired = vectors.verify.find((c) => c.result === "expired")!;
  const answers: Record<string, [number, string]> = {
    "/renewed": [200, good.key + "\n"],
    "/expired": [200, expired.key],
    "/hostile": [200, "MYAPP-AAAAA"],
    "/gone": [404, "no license behind this key"],
  };
  const server = createServer((req, res) => {
    const [status, body] = answers[req.url!];
    res.writeHead(status).end(body);
  }).listen(0);
  const { port } = server.address() as { port: number };
  const url = (path: string) => `http://127.0.0.1:${port}${path}`;
  try {
    assert.equal(
      await refresh(url("/renewed"), expired.key, "MYAPP", vectors.public_key),
      good.key,
    );
    assert.equal(
      await refresh(url("/expired"), expired.key, "MYAPP", vectors.public_key),
      expired.key,
    );
    await assert.rejects(
      refresh(url("/hostile"), expired.key, "MYAPP", vectors.public_key),
      LicenseError,
    );
    await assert.rejects(
      refresh(url("/gone"), expired.key, "MYAPP", vectors.public_key),
      /no license behind/,
    );
  } finally {
    server.close();
  }
});
