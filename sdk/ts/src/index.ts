// Verifies inkan license keys. The format is in FORMAT.md, the Go package
// github.com/mizuchilabs/inkan/license is the reference.

export type Claims = {
  v: number;
  app: string;
  plan: string;
  email: string;
  /** Unix seconds, 0 never expires. */
  exp: number;
  /** Stable id of the license, missing in old keys. */
  id?: string;
  /** Extras the app defines, like seats. */
  meta?: Record<string, string>;
};

export type LicenseErrorCode = "format" | "signature" | "app" | "version" | "expired";

const messages: Record<LicenseErrorCode, string> = {
  format: "license format is invalid",
  signature: "signature verification failed",
  app: "license issued for a different application",
  version: "invalid license version",
  expired: "license has expired",
};

export class LicenseError extends Error {
  code: LicenseErrorCode;
  /** Set when the key is real but ran out, to show who it was for. */
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
 * Throws a LicenseError. An expired key throws with code "expired" and
 * carries its claims. now is in unix seconds.
 */
export async function verify(
  key: string,
  app: string,
  publicKeys: string,
  now: number = Date.now() / 1000,
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
  if (claims.v !== wireVersion) throw new LicenseError("version");
  if (claims.exp !== 0 && now > claims.exp) throw new LicenseError("expired", claims);
  return claims;
}

/**
 * Asks a worker for the key a license is owed now. The answer is verified
 * before it is returned, so a broken server cannot hand the app anything but
 * a real key of this app. It can still be expired, verify tells. On an error
 * keep the key you have.
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
    v: field("v", "number", 0),
    app: field("app", "string", ""),
    plan: field("plan", "string", ""),
    email: field("email", "string", ""),
    exp: field("exp", "number", 0),
  };
  if (!Number.isInteger(claims.v) || !Number.isInteger(claims.exp))
    throw new LicenseError("format");
  const id = field("id", "string", "");
  if (id) claims.id = id;
  const meta = field<Record<string, unknown>>("meta", "object", {});
  if (Array.isArray(meta) || Object.values(meta).some((value) => typeof value !== "string")) {
    throw new LicenseError("format");
  }
  if (Object.keys(meta).length > 0) claims.meta = meta as Record<string, string>;
  return claims;
}
