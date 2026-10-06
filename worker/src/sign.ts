// Signs license keys. The format is in FORMAT.md.

export type Claims = {
	app: string;
	plan: string;
	email: string;
	exp: number;
	id: string;
	meta: Record<string, string>;
};

// PKCS#8 wrapping of a raw Ed25519 seed, WebCrypto takes no bare seeds.
const pkcs8Prefix = [
	0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20
];

/** Reads the private key inkan stores, base64 of 64 bytes. The seed is the first 32. */
export function importPrivateKey(privateB64: string): Promise<CryptoKey> {
	const pkcs8 = new Uint8Array([...pkcs8Prefix, ...decode(privateB64).subarray(0, 32)]);
	return crypto.subtle.importKey('pkcs8', pkcs8, 'Ed25519', false, ['sign']);
}

export function publicKeyHex(privateB64: string): string {
	return Array.from(decode(privateB64).subarray(32), (byte) =>
		byte.toString(16).padStart(2, '0')
	).join('');
}

function decode(privateB64: string): Uint8Array {
	const raw = Uint8Array.from(atob(privateB64), (char) => char.charCodeAt(0));
	if (raw.length !== 64) throw new Error('the private key must be the base64 64-byte Ed25519 key');
	return raw;
}

export async function issue(claims: Claims, privateKey: CryptoKey): Promise<string> {
	const body = new TextEncoder().encode(payload(claims));
	const signature = new Uint8Array(await crypto.subtle.sign('Ed25519', privateKey, body));
	const data = new Uint8Array([1, ...body, ...signature]);
	const encoded = btoa(String.fromCharCode(...data)).replace(/=+$/, '');
	return claims.app.toUpperCase() + '-' + encoded.match(/.{1,5}/g)!.join('-');
}

// Fixed field order and sorted meta, so the same license always gives the same key.
export function payload(claims: Claims): string {
	const names = Object.keys(claims.meta).sort();
	return JSON.stringify({
		v: 1,
		app: claims.app,
		plan: claims.plan,
		email: claims.email,
		exp: claims.exp,
		...(claims.id && { id: claims.id }),
		...(names.length > 0 && {
			meta: Object.fromEntries(names.map((name) => [name, claims.meta[name]]))
		})
	});
}
