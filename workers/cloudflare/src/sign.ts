// Signs license keys with the exact bytes the Go package license gives, so a
// key from this worker is the key the inkan ledger shows. The sign vectors in
// license/testdata/vectors.json hold it to that.

export type Claims = {
	app: string;
	plan: string;
	email: string;
	exp: number;
	id: string;
	meta: Record<string, string>;
};

// PKCS#8 wrapping of a raw Ed25519 seed, WebCrypto takes no bare seeds.
const pkcs8Prefix = [0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20];

/** Reads the private key inkan stores, base64 of 64 bytes. The seed is the first 32. */
export function importPrivateKey(privateB64: string): Promise<CryptoKey> {
	const raw = Uint8Array.from(atob(privateB64), (char) => char.charCodeAt(0));
	if (raw.length !== 64) throw new Error('the private key must be the base64 64-byte Ed25519 key');
	const pkcs8 = new Uint8Array([...pkcs8Prefix, ...raw.subarray(0, 32)]);
	return crypto.subtle.importKey('pkcs8', pkcs8, 'Ed25519', false, ['sign']);
}

export function publicKeyHex(privateB64: string): string {
	const raw = Uint8Array.from(atob(privateB64), (char) => char.charCodeAt(0));
	return Array.from(raw.subarray(32), (byte) => byte.toString(16).padStart(2, '0')).join('');
}

export async function issue(claims: Claims, privateKey: CryptoKey): Promise<string> {
	const body = new TextEncoder().encode(payload(claims));
	const signature = new Uint8Array(await crypto.subtle.sign('Ed25519', privateKey, body));
	const data = new Uint8Array([1, ...body, ...signature]);
	const encoded = btoa(String.fromCharCode(...data)).replace(/=+$/, '');
	return claims.app.toUpperCase() + '-' + encoded.match(/.{1,5}/g)!.join('-');
}

/** The claims as Go's encoding/json writes them: fixed field order, sorted meta. */
export function payload(claims: Claims): string {
	let out =
		`{"v":1,"app":${quote(claims.app)},"plan":${quote(claims.plan)}` +
		`,"email":${quote(claims.email)},"exp":${claims.exp}`;
	if (claims.id) out += `,"id":${quote(claims.id)}`;
	const names = Object.keys(claims.meta).sort(byCodePoint);
	if (names.length > 0) {
		out += `,"meta":{${names.map((name) => `${quote(name)}:${quote(claims.meta[name])}`).join(',')}}`;
	}
	return out + '}';
}

// Go sorts map keys by their bytes, which is code point order, not the
// UTF-16 order of a plain sort.
function byCodePoint(a: string, b: string): number {
	const left = Array.from(a, (char) => char.codePointAt(0)!);
	const right = Array.from(b, (char) => char.codePointAt(0)!);
	for (let i = 0; i < Math.min(left.length, right.length); i++) {
		if (left[i] !== right[i]) return left[i] - right[i];
	}
	return left.length - right.length;
}

const short: Record<string, string> = {
	'"': '\\"',
	'\\': '\\\\',
	'\b': '\\b',
	'\f': '\\f',
	'\n': '\\n',
	'\r': '\\r',
	'\t': '\\t'
};

// Go escapes more than JSON.stringify: the html characters and the two line
// separators.
function quote(text: string): string {
	const escaped = text
		.toWellFormed()
		.replace(
			/["\\<>&\u0000-\u001f\u2028\u2029]/g,
			(char) => short[char] ?? '\\u' + char.charCodeAt(0).toString(16).padStart(4, '0')
		);
	return `"${escaped}"`;
}
