// Verifies inkan license keys. The format is in FORMAT.md, the Go package
// github.com/mizuchilabs/inkan/license is the reference.

export type Claims = {
  app: string;
  plan: string;
  email: string;
  /** Unix seconds, 0 never expires. */
  exp: number;
  /** Stable id of the license, missing in old keys. */
  id?: string;
  /** The machine the key is bound to, as the app names it. Missing binds to nothing. */
  fp?: string;
  /** Extras the app defines, like seats. */
  meta?: Record<string, string>;
};

export type LicenseErrorCode =
  | "format"
  | "signature"
  | "app"
  | "fingerprint"
  | "expired"
  /** Only from a Keeper that holds no key. */
  | "nokey";

const messages: Record<LicenseErrorCode, string> = {
  format: "license format is invalid",
  signature: "signature verification failed",
  app: "license issued for a different application",
  fingerprint: "license is bound to another machine",
  expired: "license has expired",
  nokey: "no license key set",
};

export type VerifyOptions = {
  /**
   * What this machine is called, in whatever way the app picks, like a hash
   * of /etc/machine-id or an install id kept in its data dir. A key bound to
   * another machine fails with "fingerprint". Leave it out in an app that
   * does not bind keys, then nothing is compared.
   */
  fingerprint?: string;
  /** Unix seconds, for tests. */
  now?: number;
};

export class LicenseError extends Error {
  code: LicenseErrorCode;
  /** Set when the key is real but ran out or is bound to another machine, to show who it was for. */
  claims?: Claims;

  constructor(code: LicenseErrorCode, claims?: Claims) {
    super(messages[code]);
    this.name = "LicenseError";
    this.code = code;
    this.claims = claims;
  }
}

const wireVersion = 1;
const signatureSize = 64;
const minute = 60;
const day = 86_400;

/**
 * Strips what a key picks up on its way: the line breaks of a mail client,
 * stray spaces, the quotes of an env file. verify does it by itself.
 */
export function clean(key: string): string {
  return key.replace(/\s+/g, "").replace(/^["']+|["']+$/g, "");
}

/**
 * Checks a key and returns its claims. app is compared exactly, the prefix of
 * the key is its uppercase form. publicKeys is the hex key from the Products
 * page, several can be given separated by commas while a signing key rotates.
 *
 * Throws a LicenseError. An expired key throws with code "expired", a key
 * bound to another machine with "fingerprint", both carry their claims.
 */
export async function verify(
  key: string,
  app: string,
  publicKeys: string,
  { fingerprint = "", now = Date.now() / 1000 }: VerifyOptions = {},
): Promise<Claims> {
  const keys = await Promise.all(publicKeys.split(",").map((hex) => importPublicKey(hex.trim())));

  const prefix = app.toUpperCase() + "-";
  const cleaned = clean(key);
  if (!cleaned.startsWith(prefix)) throw new LicenseError("format");
  const raw = decodeBase64(cleaned.slice(prefix.length).replaceAll("-", ""));
  if (!raw || raw.length < 1 + signatureSize + 1 || raw[0] !== wireVersion) {
    throw new LicenseError("format");
  }
  const payload = raw.subarray(1, raw.length - signatureSize);
  const signature = raw.subarray(raw.length - signatureSize);

  let signed = false;
  for (const publicKey of keys) {
    if (await crypto.subtle.verify("Ed25519", publicKey, signature, payload)) {
      signed = true;
      break;
    }
  }
  if (!signed) throw new LicenseError("signature");

  const claims = parseClaims(payload);
  if (claims.app !== app) throw new LicenseError("app");
  if (fingerprint && claims.fp && claims.fp !== fingerprint) {
    throw new LicenseError("fingerprint", claims);
  }
  if (ranOut(claims, now)) throw new LicenseError("expired", claims);
  return claims;
}

/**
 * Asks a worker for the key a license is owed now. The answer is verified
 * before it is returned, so a broken server cannot hand the app anything but
 * a real key of this app. It can still be expired or bound to another
 * machine, verify tells. On an error keep the key you have.
 */
export async function refresh(
  endpoint: string,
  key: string,
  app: string,
  publicKeys: string,
): Promise<string> {
  const response = await fetch(endpoint, {
    method: "POST",
    headers: { "Content-Type": "text/plain; charset=utf-8" },
    body: clean(key),
    signal: AbortSignal.timeout(15_000),
  });
  const answer = (await response.text()).trim();
  if (!response.ok) {
    throw new Error(`refreshing license: ${response.status}: ${answer.slice(0, 200)}`);
  }
  try {
    await verify(answer, app, publicKeys);
  } catch (err) {
    if (!(err instanceof LicenseError && err.code === "expired")) throw err;
  }
  return clean(answer);
}

/**
 * How many seconds a keeper waits before it asks the worker again. exp is the
 * expiry of the held key and now the time, both in unix seconds. failures
 * counts the tries in a row that did not work.
 *
 * Once a day, at half of the time a key has left when that comes sooner, and
 * a minute after a failed try, twice as long after every further one.
 * FORMAT.md has the rule. Keeper.renew follows it, an app with a loop of its
 * own can ask here.
 */
export function renewWait(exp: number, now: number, failures: number): number {
  if (failures > 0) return Math.min(minute * 2 ** (failures - 1), day);
  const left = exp - now;
  if (exp === 0 || left <= 0) return day;
  // Never under a minute, or the end of a key that nobody renews is asked
  // about in a tight loop.
  return Math.min(day, Math.max(left / 2, minute));
}

export type RenewOptions = {
  /** Gets every new key, so the app can store it and start with it next time. */
  save?: (key: string) => void | Promise<void>;
  /** Gets the error of every try that did not work, for the app to log. */
  failed?: (err: unknown) => void;
  /** Ends the renewal when it aborts. */
  signal?: AbortSignal;
};

/**
 * Holds the license key of a running app, so the key can change without a
 * restart. Either someone hands the app a new key, or the app fetches it from
 * a worker:
 *
 *     const lic = new Keeper("MYAPP", publicKey);
 *     await lic.set(storedKey);
 *     lic.renew("https://keys.example.com/refresh", { save });
 *
 * Ask valid() wherever the app gates a feature, or claims() if it depends on
 * the plan or the meta. Both are cheap and need no await, the signature is
 * only checked in set.
 *
 * A keeper is for a process that stays up. Where every request starts from
 * nothing, like on Cloudflare Workers, call verify per request instead.
 */
export class Keeper {
  #app: string;
  #publicKeys: string;
  #fingerprint: string;
  #held?: { key: string; claims: Claims };

  /**
   * app, publicKeys and fingerprint are the ones verify takes. Leave the
   * fingerprint out in an app that does not bind keys to machines.
   */
  constructor(app: string, publicKeys: string, fingerprint = "") {
    this.#app = app;
    this.#publicKeys = publicKeys;
    this.#fingerprint = fingerprint;
  }

  /**
   * Swaps in a key and returns its claims like verify does. An expired key
   * is held too, renew can trade it for the current one, and set throws
   * "expired" with its claims. On any other error, a key bound to another
   * machine among them, the keeper keeps the key it has.
   */
  async set(key: string): Promise<Claims> {
    await this.#hold(key);
    return this.claims();
  }

  /**
   * Whether a key is held that has not run out. It is all an app needs that
   * only asks if there is a license.
   */
  valid(): boolean {
    return this.#held !== undefined && !ranOut(this.#held.claims, Date.now() / 1000);
  }

  /**
   * The claims of the held key. The expiry is checked on every call, so a key
   * that runs out while the app runs throws "expired" with its claims.
   * Without a key it throws "nokey".
   */
  claims(): Claims {
    const held = this.#held;
    if (!held) throw new LicenseError("nokey");
    // A copy, the caller must not be able to change what is held.
    const claims = { ...held.claims };
    if (claims.meta) claims.meta = { ...claims.meta };
    if (ranOut(claims, Date.now() / 1000)) throw new LicenseError("expired", claims);
    return claims;
  }

  /** The held key, for the app to store. It is empty without one. */
  key(): string {
    return this.#held?.key ?? "";
  }

  /**
   * Keeps the held key current until signal aborts. It asks the worker at
   * endpoint with refresh, right away and then as renewWait says: once a
   * day, sooner for a key that runs out before that, and with growing waits
   * after a failed try. The held key stays as it is when a try fails.
   *
   * The timer does not keep a Node process alive that is otherwise done.
   */
  renew(endpoint: string, { save, failed, signal }: RenewOptions = {}): void {
    let failures = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const next = (seconds: number) => {
      if (signal?.aborted) return;
      timer = setTimeout(attempt, seconds * 1000);
      (timer as unknown as { unref?: () => void }).unref?.();
    };
    const attempt = async () => {
      const old = this.key();
      // A key pasted later must not wait a day for its first refresh.
      if (!old) return next(minute);
      try {
        const key = await refresh(endpoint, old, this.#app, this.#publicKeys);
        if (signal?.aborted) return;
        if (key !== old) {
          // refresh checked everything but the machine, hold does that.
          await this.#hold(key);
          await save?.(key);
        }
        // The next outage starts with short waits again.
        failures = 0;
      } catch (err) {
        if (signal?.aborted) return;
        failures++;
        failed?.(err);
      }
      next(renewWait(this.#held?.claims.exp ?? 0, Date.now() / 1000, failures));
    };
    signal?.addEventListener("abort", () => clearTimeout(timer), { once: true });
    next(0);
  }

  async #hold(key: string): Promise<void> {
    let claims: Claims;
    try {
      claims = await verify(key, this.#app, this.#publicKeys, { fingerprint: this.#fingerprint });
    } catch (err) {
      if (!(err instanceof LicenseError && err.code === "expired" && err.claims)) throw err;
      claims = err.claims;
    }
    this.#held = { key: clean(key), claims };
  }
}

function ranOut(claims: Claims, now: number): boolean {
  return claims.exp !== 0 && now > claims.exp;
}

function importPublicKey(hex: string): Promise<CryptoKey> {
  if (!/^[0-9a-fA-F]{64}$/.test(hex)) throw new Error("invalid ed25519 public key hex");
  const bytes = Uint8Array.from(hex.match(/../g)!, (byte) => parseInt(byte, 16));
  return crypto.subtle.importKey("raw", bytes, "Ed25519", false, ["verify"]);
}

// Standard alphabet, no padding.
function decodeBase64(text: string): Uint8Array<ArrayBuffer> | null {
  if (!/^[A-Za-z0-9+/]*$/.test(text) || text.length % 4 === 1) return null;
  return Uint8Array.from(atob(text), (char) => char.charCodeAt(0));
}

function parseClaims(payload: Uint8Array): Claims {
  let parsed: unknown;
  try {
    parsed = JSON.parse(new TextDecoder().decode(payload));
  } catch {
    throw new LicenseError("format");
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    throw new LicenseError("format");
  }
  const fields = parsed as Record<string, unknown>;
  const field = <T>(name: string, type: string, zero: T): T => {
    const value = fields[name] ?? zero;
    if (typeof value !== type) throw new LicenseError("format");
    return value as T;
  };

  const claims: Claims = {
    app: field("app", "string", ""),
    plan: field("plan", "string", ""),
    email: field("email", "string", ""),
    exp: field("exp", "number", 0),
  };
  if (!Number.isInteger(claims.exp)) throw new LicenseError("format");
  const id = field("id", "string", "");
  if (id) claims.id = id;
  const fp = field("fp", "string", "");
  if (fp) claims.fp = fp;
  const meta = field<Record<string, unknown>>("meta", "object", {});
  if (Array.isArray(meta) || Object.values(meta).some((value) => typeof value !== "string")) {
    throw new LicenseError("format");
  }
  if (Object.keys(meta).length > 0) claims.meta = meta as Record<string, string>;
  return claims;
}
