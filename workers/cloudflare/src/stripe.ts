// Decides what license a Stripe subscription or one time purchase is owed.
// A port of internal/stripe in the inkan repo. Both run the cases in
// internal/stripe/testdata/licenses.json, so they decide alike.

export type Product = {
	id: string;
	plan: string;
	grace_days: number;
	key_days: number;
};

export type License = {
	id: string;
	email: string;
	customer: string;
	plan: string;
	meta: Record<string, string> | null;
	expires_at: number;
	/** Seconds one billing period lasts, 0 for a one time purchase. */
	period: number;
	status: string;
	renews: boolean;
	created_at: number;
};

export class NotOwed extends Error {}
export class OtherProduct extends Error {}
export class NotFound extends Error {}

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
	customer?: { email?: string | null; name?: string | null } | null;
	cancellation_details?: { reason?: string | null } | null;
	items: {
		data: { current_period_start?: number; current_period_end?: number; quantity?: number; price: Price }[];
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
		email: sub.metadata?.email || sub.customer?.email || '',
		customer: sub.metadata?.org || sub.customer?.name || '',
		plan: item.price.metadata?.plan || product.plan,
		meta: metaOf(item.price.metadata, item.quantity),
		expires_at: capped(paid + product.grace_days * day, product.key_days, now),
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
	const days = /^[+-]?\d+$/.test(text) ? Number(text) : 0;
	const refunded = session.payment_intent?.latest_charge?.refunded === true;
	let expires = 0;
	if (refunded) expires = created + product.grace_days * day;
	else if (days > 0) expires = created + (days + product.grace_days) * day;

	return {
		id: session.id,
		email: session.metadata?.email || session.customer_details?.email || '',
		customer: session.metadata?.org || session.customer_details?.name || '',
		plan: item.price.metadata?.plan || product.plan,
		meta: metaOf(item.price.metadata, item.quantity),
		expires_at: capped(expires, product.key_days, now),
		period: 0,
		status: refunded ? 'refunded' : 'paid',
		renews: false,
		created_at: created
	};
}

// meta_seats=5 becomes the claim seats=5, quantity=seats puts the bought
// quantity into the claim seats.
function metaOf(priceMeta: Record<string, string> | null | undefined, quantity = 0): Record<string, string> | null {
	const out: Record<string, string> = {};
	for (const [name, value] of Object.entries(priceMeta ?? {})) {
		if (name.startsWith('meta_') && name.length > 5) out[name.slice(5)] = value;
	}
	const seats = priceMeta?.quantity;
	if (seats) out[seats] = String(Math.max(quantity, 1));
	return Object.keys(out).length > 0 ? out : null;
}

// The limit only moves every half of the key days, so everyone who signs
// within that time signs the same key.
function capped(expires: number, keyDays: number, now: number): number {
	if (keyDays <= 0) return expires;
	const step = Math.trunc((keyDays * day) / 2);
	const limit = (Math.trunc(now / step) + 2) * step;
	return expires === 0 ? limit : Math.min(expires, limit);
}

/** Fetches what is behind a license id and decides what it is owed. */
export async function owed(stripeKey: string, id: string, product: Product, now: number): Promise<License> {
	if (id.startsWith('cs_')) {
		const query = 'expand[]=line_items&expand[]=payment_intent.latest_charge';
		const session = await get<Session>(stripeKey, `/v1/checkout/sessions/${encodeURIComponent(id)}?${query}`);
		return sessionLicense(session, product, now);
	}
	const sub = await get<Subscription>(stripeKey, `/v1/subscriptions/${encodeURIComponent(id)}?expand[]=customer`);
	return subscriptionLicense(sub, product, now);
}

async function get<T>(stripeKey: string, path: string): Promise<T> {
	const response = await fetch('https://api.stripe.com' + path, {
		headers: { Authorization: 'Bearer ' + stripeKey }
	});
	if (response.status === 404) throw new NotFound();
	if (!response.ok) {
		const failure = (await response.json().catch(() => null)) as { error?: { message?: string } } | null;
		throw new Error('stripe: ' + (failure?.error?.message ?? response.status));
	}
	return (await response.json()) as T;
}
