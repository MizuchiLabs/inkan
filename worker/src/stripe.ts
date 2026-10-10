// Reads subscriptions and one time purchases from Stripe and decides what
// license each one is owed. This is the one place that decides it.

export type Product = {
	id: string;
	plan: string;
	grace_days: number;
	max_key_days: number;
};

export type License = {
	id: string;
	/** A subscription or a one time purchase. */
	kind: 'subscription' | 'purchase';
	email: string;
	customer: string;
	/** The customer was deleted at Stripe, email and name are lost there. */
	customer_gone: boolean;
	plan: string;
	meta: Record<string, string> | null;
	/** The machine the key is bound to, from the metadata of the subscription or session. */
	fingerprint: string;
	expires_at: number;
	/** Seconds one billing period lasts, 0 for a one time purchase. */
	period: number;
	status: string;
	renews: boolean;
	created_at: number;
};

export type Page = {
	licenses: License[];
	/** The kinds the Stripe key may not read, so they are missing here. */
	skipped: string[];
	next: string | null;
};

export class NotOwed extends Error {}
export class OtherProduct extends Error {}
export class NotFound extends Error {}
export class Forbidden extends Error {}

type Price = { product: string; metadata?: Record<string, string> | null };

type Subscription = {
	id: string;
	status: string;
	created?: number;
	ended_at?: number | null;
	cancel_at_period_end?: boolean;
	current_period_start?: number;
	current_period_end?: number;
	metadata?: Record<string, string> | null;
	customer?: { email?: string | null; name?: string | null; deleted?: boolean } | null;
	cancellation_details?: { reason?: string | null } | null;
	items: {
		data: {
			current_period_start?: number;
			current_period_end?: number;
			quantity?: number;
			price: Price;
		}[];
	};
};

type Session = {
	id: string;
	mode: string;
	payment_status: string;
	created?: number;
	metadata?: Record<string, string> | null;
	customer_details?: { email?: string | null; name?: string | null } | null;
	payment_intent?: { latest_charge?: { refunded?: boolean } | null } | null;
	line_items?: { data: { quantity?: number; price: Price }[] };
};

type List<T> = { data: T[]; has_more: boolean };

// Pinned, so a new default version of the account cannot change what comes back.
const apiVersion = '2025-03-31.basil';
const day = 86400;

export function subscriptionLicense(sub: Subscription, product: Product, now: number): License {
	const item = sub.items.data.find((it) => it.price.product === product.id);
	if (!item) throw new OtherProduct();
	const start = item.current_period_start || sub.current_period_start || 0;
	let paid = item.current_period_end || sub.current_period_end || 0;
	const period = paid - start;

	switch (sub.status) {
		case 'active':
		case 'trialing':
			break;
		case 'incomplete':
		case 'incomplete_expired':
			throw new NotOwed();
		case 'canceled':
			if (sub.cancellation_details?.reason === 'payment_failed') paid = start;
			else if (sub.ended_at) paid = Math.min(paid, sub.ended_at);
			break;
		default:
			// past_due, unpaid, paused: Stripe already moved to a period nobody paid for.
			paid = start;
	}
	if (paid === 0) throw new NotOwed();

	const active = sub.status === 'active' || sub.status === 'trialing';
	return {
		id: sub.id,
		kind: 'subscription',
		email: sub.metadata?.email || sub.customer?.email || '',
		customer: sub.metadata?.org || sub.customer?.name || '',
		customer_gone: sub.customer?.deleted === true,
		plan: item.price.metadata?.plan || product.plan,
		meta: metaOf(item.price.metadata, item.quantity),
		fingerprint: sub.metadata?.fingerprint ?? '',
		// The grace keeps the old key alive until the renewed one is in place.
		expires_at: capped(paid + product.grace_days * day, product.max_key_days, now),
		period,
		status: sub.status,
		renews: active && !sub.cancel_at_period_end,
		created_at: sub.created ?? 0
	};
}

export function sessionLicense(session: Session, product: Product, now: number): License {
	const item = session.line_items?.data.find((it) => it.price.product === product.id);
	// A session in subscription mode is covered by its subscription.
	if (!item || session.mode !== 'payment') throw new OtherProduct();
	if (session.payment_status === 'unpaid') throw new NotOwed();

	const created = session.created ?? 0;
	const text = item.price.metadata?.days ?? '';
	const days = /^\d+$/.test(text) ? Number(text) : 0;
	const refunded = session.payment_intent?.latest_charge?.refunded === true;
	let expires = 0;
	if (refunded) expires = created + product.grace_days * day;
	else if (days > 0) expires = created + (days + product.grace_days) * day;

	return {
		id: session.id,
		kind: 'purchase',
		email: session.metadata?.email || session.customer_details?.email || '',
		customer: session.metadata?.org || session.customer_details?.name || '',
		customer_gone: false,
		plan: item.price.metadata?.plan || product.plan,
		meta: metaOf(item.price.metadata, item.quantity),
		fingerprint: session.metadata?.fingerprint ?? '',
		expires_at: capped(expires, product.max_key_days, now),
		period: 0,
		status: refunded ? 'refunded' : 'paid',
		renews: false,
		created_at: created
	};
}

// meta_seats=5 becomes the claim seats=5, quantity=seats puts the bought
// quantity into the claim seats.
function metaOf(
	priceMeta: Record<string, string> | null | undefined,
	quantity = 0
): Record<string, string> | null {
	const out: Record<string, string> = {};
	for (const [name, value] of Object.entries(priceMeta ?? {})) {
		if (name.startsWith('meta_') && name.length > 5) out[name.slice(5)] = value;
	}
	const seats = priceMeta?.quantity;
	if (seats) out[seats] = String(Math.max(quantity, 1));
	return Object.keys(out).length > 0 ? out : null;
}

// The limit only moves every half of the max key days, so a license gets the
// same key for that long. A fresh key lives between half and all of them.
function capped(expires: number, maxKeyDays: number, now: number): number {
	if (maxKeyDays <= 0) return expires;
	const step = Math.trunc((maxKeyDays * day) / 2);
	const limit = (Math.trunc(now / step) + 2) * step;
	return expires === 0 ? limit : Math.min(expires, limit);
}

/** Fetches what is behind a license id and decides what it is owed. */
export async function owed(
	stripeKey: string,
	id: string,
	product: Product,
	now: number
): Promise<License> {
	if (id.startsWith('cs_')) {
		const query = 'expand[]=line_items&expand[]=payment_intent.latest_charge';
		const path = `/v1/checkout/sessions/${encodeURIComponent(id)}?${query}`;
		return sessionLicense(await get<Session>(stripeKey, path), product, now);
	}
	const path = `/v1/subscriptions/${encodeURIComponent(id)}?expand[]=customer`;
	return subscriptionLicense(await get<Subscription>(stripeKey, path), product, now);
}

/**
 * Reads one page of the licenses of a product, with one call to Stripe. The
 * subscriptions come first, then the one time purchases. cursor is the next of
 * the page before, empty for the first one.
 *
 * Stripe cannot filter by product, so a page can hold fewer licenses than
 * limit, or none, and still have a next.
 */
export async function list(
	stripeKey: string,
	product: Product,
	cursor: string,
	limit: number,
	now: number
): Promise<Page> {
	const purchases = cursor.startsWith('p:');
	const after = cursor.slice(2);
	const query = new URLSearchParams({ limit: String(limit) });
	if (after) query.set('starting_after', after);

	if (!purchases) {
		query.set('status', 'all');
		query.append('expand[]', 'data.customer');
		const page = await get<List<Subscription>>(stripeKey, '/v1/subscriptions?' + query);
		return {
			licenses: decided(page.data, (sub) => subscriptionLicense(sub, product, now)),
			skipped: [],
			next: more(page) ? 's:' + page.data.at(-1)!.id : 'p:'
		};
	}

	query.set('status', 'complete');
	query.append('expand[]', 'data.line_items');
	query.append('expand[]', 'data.payment_intent.latest_charge');
	let page: List<Session>;
	try {
		page = await get<List<Session>>(stripeKey, '/v1/checkout/sessions?' + query);
	} catch (err) {
		// A key that only reads subscriptions keeps working, it just has no one time purchases.
		if (err instanceof Forbidden) return { licenses: [], skipped: ['purchase'], next: null };
		throw err;
	}
	return {
		licenses: decided(page.data, (session) => sessionLicense(session, product, now)),
		skipped: [],
		next: more(page) ? 'p:' + page.data.at(-1)!.id : null
	};
}

function more(page: List<{ id: string }>): boolean {
	return page.has_more && page.data.length > 0;
}

// What belongs to another product or was never paid is no license.
function decided<T>(objects: T[], decide: (object: T) => License): License[] {
	const out: License[] = [];
	for (const object of objects) {
		try {
			out.push(decide(object));
		} catch (err) {
			if (!(err instanceof OtherProduct || err instanceof NotOwed)) throw err;
		}
	}
	return out;
}

async function get<T>(stripeKey: string, path: string): Promise<T> {
	const response = await fetch('https://api.stripe.com' + path, {
		headers: { Authorization: 'Bearer ' + stripeKey, 'Stripe-Version': apiVersion }
	});
	if (response.status === 404) throw new NotFound();
	if (!response.ok) {
		const failure = (await response.json().catch(() => null)) as {
			error?: { message?: string };
		} | null;
		const message = 'stripe: ' + (failure?.error?.message ?? response.status);
		throw response.status === 403 ? new Forbidden(message) : new Error(message);
	}
	return (await response.json()) as T;
}
