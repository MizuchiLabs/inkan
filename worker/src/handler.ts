// The inkan worker, the public side of one product.
//
//  POST /refresh   an app sends its key and gets the current one back
//  POST /webhook   Stripe reports a payment, the key is mailed to the customer
//  GET  /licenses  the inkan dashboard reads what the worker hands out
//
// Nothing is stored. Stripe says what is paid and the private key signs it.
// WORKER.md in the repository describes the endpoints.

import {
	clean,
	LicenseError,
	verify,
	type Claims as SignedClaims
} from '../../sdk/ts/src/index.ts';
import { render } from './mail.ts';
import { sender, type Send, type SendEnv } from './send.ts';
import { importPrivateKey, issue, publicKeyHex, type Claims } from './sign.ts';
import { template } from './template.ts';
import {
	list,
	NotFound,
	NotOwed,
	OtherProduct,
	owed,
	type License,
	type Page,
	type Product
} from './stripe.ts';

export type Env = SendEnv & {
	INKAN_PREFIX: string;
	INKAN_PRIVATE_KEY: string;
	// Hex public keys from before a rotation, comma separated. A refresh takes
	// their keys too and answers with one of the private key above.
	INKAN_OLD_PUBLIC_KEYS?: string;
	INKAN_STRIPE_KEY: string;
	INKAN_STRIPE_PRODUCT: string;
	INKAN_PLAN?: string;
	INKAN_GRACE_DAYS?: string;
	INKAN_MAX_KEY_DAYS?: string;
	// Lets the dashboard read the licenses. Without it there is no /licenses.
	INKAN_WORKER_TOKEN?: string;
	// Without these two or a mail service there is no /webhook.
	INKAN_STRIPE_WEBHOOK_SECRET?: string;
	INKAN_MAIL_FROM?: string;
	// Where an answer to the key mail goes, for a sender that reads none.
	INKAN_MAIL_REPLY_TO?: string;
	// How the key mail looks, all optional.
	INKAN_PRODUCT_NAME?: string;
	INKAN_MAIL_LOGO?: string;
	INKAN_MAIL_ACCENT?: string;
	INKAN_MAIL_FOOTER?: string;
	INKAN_MAIL_COMPANY?: string;
};

const webhookTolerance = 5 * 60;
// A renewal only sends a key when its period is longer than a month.
const shortPeriod = 32 * 86400;
// How long refresh answers from memory. A leaked key must not be a way to
// spend the Stripe rate limit.
const cacheFor = 5 * 60_000;
const rememberEvents = 72 * 3_600_000;
// The token is guessed without a limit on tries, so it has to be long.
const minTokenLength = 32;

// Only shortcuts. Every instance has its own, nothing depends on them. An
// empty key is a license that was not there.
const keys = new Map<string, { key: string; at: number }>();
const events = new Map<string, number>();

/** The settings a worker cannot run without and does not have. */
export function missing(env: Env): string[] {
	const needed = [
		'INKAN_PREFIX',
		'INKAN_PRIVATE_KEY',
		'INKAN_STRIPE_KEY',
		'INKAN_STRIPE_PRODUCT'
	] as const;
	return needed.filter((name) => !env[name]);
}

export async function handle(request: Request, env: Env): Promise<Response> {
	const unset = missing(env);
	if (unset.length > 0) return text('not set: ' + unset.join(', '), 500);

	const send = env.INKAN_STRIPE_WEBHOOK_SECRET && env.INKAN_MAIL_FROM ? sender(env) : undefined;
	const route = request.method + ' ' + new URL(request.url).pathname;
	try {
		if (route === 'GET /healthz') return text(serves(send, env), 200);
		if (route === 'POST /refresh') return await refresh(request, env);
		if (route === 'POST /webhook' && send) return await webhook(request, env, send);
		if (route === 'GET /licenses' && env.INKAN_WORKER_TOKEN) return await licenses(request, env);
	} catch (err) {
		console.error('the worker failed', route, err);
		return text('the worker failed', 500);
	}
	// The usual slip is a webhook endpoint or a refresh url without its path.
	return text('this worker serves POST /refresh' + (send ? ' and POST /webhook' : ''), 404);
}

// What a worker is set up for, so a look at /healthz says what is missing.
function serves(send: Send | undefined, env: Env): string {
	const parts = ['refresh'];
	if (send) parts.push('webhook');
	if (env.INKAN_WORKER_TOKEN) parts.push('licenses');
	return 'ok: ' + parts.join(', ');
}

// refresh trades a key for the one its license is owed now. A canceled or
// unpaid subscription gets its last key back, so the expiry never moves
// further than what was paid.
async function refresh(request: Request, env: Env): Promise<Response> {
	const body = await request.text();
	if (body.length > 8 << 10) return text('the key is too long', 413);

	const publicKeys = [publicKeyHex(env.INKAN_PRIVATE_KEY), ...oldPublicKeys(env)].join(',');
	let claims: SignedClaims;
	try {
		claims = await verify(body, env.INKAN_PREFIX, publicKeys);
	} catch (err) {
		// Anything else is a broken setting, like an old public key that is none.
		if (!(err instanceof LicenseError)) throw err;
		if (!(err.code === 'expired' && err.claims)) {
			return text('not a license key of this product', 400);
		}
		claims = err.claims;
	}

	// A hand issued license, or one without an id, is not in Stripe. The key
	// it has is all there is.
	let key = clean(body);
	const cached = keys.get(claims.id ?? '');
	if (cached && Date.now() - cached.at < cacheFor) {
		if (!cached.key) return text('no license behind this key', 404);
		key = cached.key;
	} else if (claims.id && !claims.id.startsWith('lic_')) {
		try {
			({ key } = await sign(env, claims.id, claims.email));
		} catch (err) {
			if (err instanceof NotFound || err instanceof OtherProduct || err instanceof NotOwed) {
				keys.set(claims.id, { key: '', at: Date.now() });
				return text('no license behind this key', 404);
			}
			console.error('refresh failed', claims.id, err);
			return text('the license could not be looked up', 502);
		}
	}
	return text(key, 200);
}

// webhook sends the key once an invoice or a one time purchase of the product
// is paid. Anything it cannot finish is answered with an error, so Stripe
// tries again.
async function webhook(request: Request, env: Env, send: Send): Promise<Response> {
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
	if (object.billing_reason === 'subscription_cycle' && period <= shortPeriod) {
		return text('', 200);
	}

	try {
		await send({
			from: env.INKAN_MAIL_FROM!,
			replyTo: env.INKAN_MAIL_REPLY_TO?.trim() || undefined,
			to: claims.email,
			...mail(env, key, claims)
		});
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

// licenses hands the dashboard one page of what Stripe holds for the product,
// each license with the key a refresh gives it.
async function licenses(request: Request, env: Env): Promise<Response> {
	const token = env.INKAN_WORKER_TOKEN!;
	if (token.length < minTokenLength) {
		return text(`INKAN_WORKER_TOKEN needs at least ${minTokenLength} characters`, 500);
	}
	if (!(await same(request.headers.get('Authorization') ?? '', 'Bearer ' + token))) {
		return text('the token does not match', 401);
	}

	const query = new URL(request.url).searchParams;
	const limit = Math.min(Math.max(whole(query.get('limit'), 50), 1), 100);
	const settings = product(env);
	let page: Page;
	try {
		page = await list(env.INKAN_STRIPE_KEY, settings, query.get('cursor') ?? '', limit, now());
	} catch (err) {
		console.error('licenses: Stripe could not be read', err);
		// Stripe's own words, they tell the dashboard what is wrong with the key.
		return text(err instanceof Error ? err.message : 'Stripe could not be read', 502);
	}

	const privateKey = await importPrivateKey(env.INKAN_PRIVATE_KEY);
	return Response.json({
		product: {
			provider: 'stripe',
			prefix: env.INKAN_PREFIX,
			public_key: publicKeyHex(env.INKAN_PRIVATE_KEY),
			old_public_keys: oldPublicKeys(env),
			plan: settings.plan,
			grace_days: settings.grace_days,
			max_key_days: settings.max_key_days,
			provider_product: settings.id,
			mail_template: await templateHash()
		},
		licenses: await Promise.all(
			page.licenses.map(async (license) => ({
				id: license.id,
				kind: license.kind,
				email: license.email,
				customer: license.customer,
				customer_gone: license.customer_gone,
				plan: license.plan,
				meta: license.meta ?? {},
				fingerprint: license.fingerprint,
				expires_at: license.expires_at,
				status: license.status,
				renews: license.renews,
				created_at: license.created_at,
				key: await issue(claimsOf(env, license), privateKey)
			}))
		),
		skipped: page.skipped,
		next: page.next
	});
}

// sign signs the key a subscription or one time purchase is owed right now.
// Stripe forgets the email of a deleted customer, lastEmail is the one of the
// key in hand.
async function sign(
	env: Env,
	id: string,
	lastEmail = ''
): Promise<{ key: string; claims: Claims; period: number }> {
	const license = await owed(env.INKAN_STRIPE_KEY, id, product(env), now());
	const claims = claimsOf(env, license, lastEmail);
	const key = await issue(claims, await importPrivateKey(env.INKAN_PRIVATE_KEY));
	keys.set(id, { key, at: Date.now() });
	return { key, claims, period: license.period };
}

function claimsOf(env: Env, license: License, lastEmail = ''): Claims {
	return {
		app: env.INKAN_PREFIX,
		plan: license.plan,
		email: license.email || lastEmail,
		exp: license.expires_at,
		id: license.id,
		fp: license.fingerprint,
		meta: license.meta ?? {}
	};
}

function oldPublicKeys(env: Env): string[] {
	return (env.INKAN_OLD_PUBLIC_KEYS ?? '')
		.split(',')
		.map((key) => key.trim())
		.filter(Boolean);
}

function product(env: Env): Product {
	return {
		id: env.INKAN_STRIPE_PRODUCT,
		plan: env.INKAN_PLAN ?? '',
		grace_days: whole(env.INKAN_GRACE_DAYS, 14),
		max_key_days: whole(env.INKAN_MAX_KEY_DAYS, 0)
	};
}

// The dashboard compares it with the template it holds, empty is the built-in mail.
async function templateHash(): Promise<string> {
	if (!template) return '';
	const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(template));
	return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join('');
}

function mail(env: Env, key: string, claims: Claims) {
	const brand = {
		name: env.INKAN_PRODUCT_NAME,
		logo: env.INKAN_MAIL_LOGO,
		accent: env.INKAN_MAIL_ACCENT,
		footer: env.INKAN_MAIL_FOOTER,
		company: env.INKAN_MAIL_COMPANY
	};
	return render(key, claims, brand, template);
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
	if (!/^\d+$/.test(stamp) || Math.abs(now() - Number(stamp)) > webhookTolerance) return false;

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
		if (await crypto.subtle.verify('HMAC', key, bytes, encoder.encode(`${stamp}.${body}`))) {
			return true;
		}
	}
	return false;
}

// same compares two secrets by their digests, so the time it takes says
// nothing about where they differ.
async function same(given: string, want: string): Promise<boolean> {
	const encoder = new TextEncoder();
	const [a, b] = await Promise.all(
		[given, want].map(
			async (value) => new Uint8Array(await crypto.subtle.digest('SHA-256', encoder.encode(value)))
		)
	);
	let differ = 0;
	for (let i = 0; i < a.length; i++) differ |= a[i] ^ b[i];
	return differ === 0;
}

// A setting that is missing or no whole number falls back.
function whole(value: string | null | undefined, fallback: number): number {
	return /^\d+$/.test(value ?? '') ? Number(value) : fallback;
}

function now(): number {
	return Math.floor(Date.now() / 1000);
}

function text(body: string, status: number): Response {
	return new Response(body, { status, headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
}
