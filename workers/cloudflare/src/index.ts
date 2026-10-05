// The inkan worker for Cloudflare Workers. It does what `inkan worker` does:
//
//	POST /webhook  Stripe reports a payment, the key is mailed to the customer
//	POST /refresh  an app sends its key and gets the current one back
//
// Nothing is stored. Stripe says what is paid and the private key signs it.

import { clean, LicenseError, verify, type Claims as SignedClaims } from '../../../sdk/ts/src/index.ts';
import { importPrivateKey, issue, publicKeyHex, type Claims } from './sign.ts';
import { NotFound, NotOwed, OtherProduct, owed, type Product } from './stripe.ts';

export type Env = {
	INKAN_PREFIX: string;
	INKAN_PRIVATE_KEY: string;
	INKAN_STRIPE_KEY: string;
	INKAN_STRIPE_PRODUCT: string;
	INKAN_PLAN: string;
	INKAN_GRACE_DAYS?: string;
	INKAN_KEY_DAYS?: string;
	// Leave these three out to serve refresh only.
	INKAN_STRIPE_WEBHOOK_SECRET?: string;
	INKAN_MAIL_FROM?: string;
	RESEND_API_KEY?: string;
};

const webhookTolerance = 5 * 60;
// A renewal only sends a key when its period is longer than a month.
const shortPeriod = 32 * 86400;
// How long refresh answers from memory. A leaked key must not be a way to
// spend the Stripe rate limit.
const cacheFor = 5 * 60_000;
const rememberEvents = 72 * 3_600_000;

// Only shortcuts. Every isolate has its own, nothing depends on them.
const keys = new Map<string, { key: string; at: number }>();
const events = new Map<string, number>();

export default {
	async fetch(request: Request, env: Env): Promise<Response> {
		const { pathname } = new URL(request.url);
		if (request.method === 'GET' && pathname === '/healthz') return new Response('ok');
		if (request.method !== 'POST') return text('not found', 404);
		if (pathname === '/refresh') return refresh(request, env);
		const mails = env.INKAN_STRIPE_WEBHOOK_SECRET && env.INKAN_MAIL_FROM && env.RESEND_API_KEY;
		if (pathname === '/webhook' && mails) return webhook(request, env);
		return text('not found', 404);
	}
};

async function refresh(request: Request, env: Env): Promise<Response> {
	const body = await request.text();
	if (body.length > 8 << 10) return text('the key is too long', 413);

	let claims: SignedClaims;
	try {
		claims = await verify(body, env.INKAN_PREFIX, publicKeyHex(env.INKAN_PRIVATE_KEY));
	} catch (err) {
		if (!(err instanceof LicenseError && err.code === 'expired' && err.claims)) {
			return text('not a license key of this product', 400);
		}
		claims = err.claims;
	}

	// A hand issued license, or one without an id, is not in Stripe. The key
	// it has is all there is.
	let key = clean(body);
	const cached = keys.get(claims.id ?? '');
	if (cached && Date.now() - cached.at < cacheFor) {
		key = cached.key;
	} else if (claims.id && !claims.id.startsWith('lic_')) {
		try {
			({ key } = await sign(env, claims.id, claims.email));
		} catch (err) {
			if (err instanceof NotFound || err instanceof OtherProduct || err instanceof NotOwed) {
				return text('no license behind this key', 404);
			}
			console.error('refresh failed', claims.id, err);
			return text('the license could not be looked up', 502);
		}
	}
	return text(key, 200);
}

// Anything it cannot finish is answered with an error, so Stripe tries again.
async function webhook(request: Request, env: Env): Promise<Response> {
	const body = await request.text();
	const header = request.headers.get('Stripe-Signature') ?? '';
	if (!(await signed(body, header, env.INKAN_STRIPE_WEBHOOK_SECRET!))) {
		return text('the signature does not match', 400);
	}

	let event: {
		id?: string;
		type?: string;
		data?: {
			object?: {
				id?: string;
				mode?: string;
				billing_reason?: string;
				subscription?: string; // API versions before 2025-03-31
				parent?: { subscription_details?: { subscription?: string } | null } | null;
			};
		};
	};
	try {
		event = JSON.parse(body);
	} catch {
		return text('the event cannot be read', 400);
	}
	const object = event.data?.object ?? {};
	let id = '';
	let oneTime = false;
	if (event.type === 'invoice.paid') {
		id = object.parent?.subscription_details?.subscription || object.subscription || '';
	} else if (
		event.type === 'checkout.session.completed' ||
		event.type === 'checkout.session.async_payment_succeeded'
	) {
		// In subscription mode the invoice of the session brings the key.
		if (object.mode === 'payment') {
			id = object.id ?? '';
			oneTime = true;
		}
	}
	if (!id || (event.id && events.has(event.id))) return text('', 200);

	let key: string, claims: Claims, period: number;
	try {
		({ key, claims, period } = await sign(env, id));
	} catch (err) {
		if (err instanceof OtherProduct) return text('', 200);
		// A slow payment method completes the session first and pays later.
		if (oneTime && err instanceof NotOwed) return text('', 200);
		console.error('webhook: no key for a payment', id, err);
		return text('the key could not be issued', 500);
	}
	if (!claims.email) {
		console.warn('webhook: the customer has no email, the key was not sent', id);
		return text('', 200);
	}
	// Nobody pastes a key every month, and an app that is online refreshes.
	if (object.billing_reason === 'subscription_cycle' && period <= shortPeriod) return text('', 200);

	try {
		await mail(env, key, claims);
	} catch (err) {
		console.error('webhook: sending the key failed', id, err);
		return text('the key could not be sent', 500);
	}
	if (event.id) {
		const now = Date.now();
		for (const [seen, at] of events) if (now - at > rememberEvents) events.delete(seen);
		events.set(event.id, now);
	}
	return text('', 200);
}

// sign signs the key a subscription or one time purchase is owed right now.
// Stripe forgets the email of a deleted customer, lastEmail is the one of the
// key in hand.
async function sign(env: Env, id: string, lastEmail = ''): Promise<{ key: string; claims: Claims; period: number }> {
	const product: Product = {
		id: env.INKAN_STRIPE_PRODUCT,
		plan: env.INKAN_PLAN,
		grace_days: Number(env.INKAN_GRACE_DAYS ?? 14),
		key_days: Number(env.INKAN_KEY_DAYS ?? 0)
	};
	const license = await owed(env.INKAN_STRIPE_KEY, id, product, Math.floor(Date.now() / 1000));
	const claims: Claims = {
		app: env.INKAN_PREFIX,
		plan: license.plan,
		email: license.email || lastEmail,
		exp: license.expires_at,
		id: license.id,
		meta: license.meta ?? {}
	};
	const key = await issue(claims, await importPrivateKey(env.INKAN_PRIVATE_KEY));
	keys.set(id, { key, at: Date.now() });
	return { key, claims, period: license.period };
}

// Cloudflare has no SMTP, so the key goes out through the API of Resend.
// Swap this one function to send it another way.
async function mail(env: Env, key: string, claims: Claims): Promise<void> {
	let body = `This is your license key for ${claims.app}:\n\n${key}\n\n`;
	if (claims.exp !== 0) {
		const until = new Date(claims.exp * 1000).toLocaleDateString('en-GB', {
			day: 'numeric',
			month: 'long',
			year: 'numeric',
			timeZone: 'UTC'
		});
		body += `It is valid until ${until}.\n`;
	}
	const response = await fetch('https://api.resend.com/emails', {
		method: 'POST',
		headers: { Authorization: 'Bearer ' + env.RESEND_API_KEY, 'Content-Type': 'application/json' },
		body: JSON.stringify({
			from: env.INKAN_MAIL_FROM,
			to: [claims.email],
			subject: `Your ${claims.app} license key`,
			text: body
		})
	});
	if (!response.ok) throw new Error(`resend: ${response.status}: ${(await response.text()).slice(0, 200)}`);
}

// signed checks a Stripe-Signature header, t=<unix>,v1=<hex>. The signature
// is an HMAC-SHA256 of "<t>.<body>" with the endpoint secret.
async function signed(body: string, header: string, secret: string): Promise<boolean> {
	let stamp = '';
	const signatures: string[] = [];
	for (const part of header.split(',')) {
		const [name, value = ''] = part.trim().split('=', 2);
		if (name === 't') stamp = value;
		if (name === 'v1') signatures.push(value);
	}
	if (!/^\d+$/.test(stamp) || Math.abs(Date.now() / 1000 - Number(stamp)) > webhookTolerance) return false;

	const encoder = new TextEncoder();
	const key = await crypto.subtle.importKey(
		'raw',
		encoder.encode(secret),
		{ name: 'HMAC', hash: 'SHA-256' },
		false,
		['verify']
	);
	for (const signature of signatures) {
		if (!/^([0-9a-f]{2})+$/i.test(signature)) continue;
		const bytes = Uint8Array.from(signature.match(/../g)!, (byte) => parseInt(byte, 16));
		// subtle.verify compares in constant time.
		if (await crypto.subtle.verify('HMAC', key, bytes, encoder.encode(`${stamp}.${body}`))) return true;
	}
	return false;
}

function text(body: string, status: number): Response {
	return new Response(body, { status, headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
}
