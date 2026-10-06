import assert from 'node:assert/strict';
import { createHmac } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { test, type TestContext } from 'node:test';

import { verify } from '../../sdk/ts/src/index.ts';
import { handle, type Env } from '../src/handler.ts';
import { render } from '../src/mail.ts';
import { handle as scaleway } from '../src/scaleway.ts';
import { importPrivateKey, issue, type Claims } from '../src/sign.ts';
import { NotOwed, OtherProduct, sessionLicense, subscriptionLicense } from '../src/stripe.ts';

const read = (path: string) => JSON.parse(readFileSync(new URL(path, import.meta.url), 'utf8'));
const vectors = read('../../license/testdata/vectors.json');
const cases = read('./licenses.json');

// The private key the way inkan stores it, base64 of seed and public key.
const hexBytes = (hex: string) => Buffer.from(hex, 'hex');
const privateB64 = Buffer.concat([hexBytes(vectors.seed), hexBytes(vectors.public_key)]).toString(
	'base64'
);
const strangerB64 = Buffer.alloc(64, 7).toString('base64');

const env: Env = {
	INKAN_PREFIX: 'MYAPP',
	INKAN_PRIVATE_KEY: privateB64,
	INKAN_STRIPE_KEY: 'sk_test',
	INKAN_STRIPE_PRODUCT: 'prod_app',
	INKAN_PLAN: 'pro',
	INKAN_WORKER_TOKEN: 'a-long-token-only-the-dashboard-knows',
	INKAN_STRIPE_WEBHOOK_SECRET: 'whsec_test',
	INKAN_MAIL_FROM: 'Keys <keys@example.com>',
	RESEND_API_KEY: 're_test',
	INKAN_PRODUCT_NAME: 'My <App>',
	INKAN_MAIL_ACCENT: 'red; background: url(x)'
};

const periodStart = 1_797_000_000;
const periodEnd = 4_000_000_000;
const grace = 14 * 86400;
const item = {
	price: { product: 'prod_app', metadata: { plan: 'team' } },
	current_period_start: periodStart,
	current_period_end: periodEnd
};
// Any sub_ok id is a paid subscription, so every test can have its own.
const paidSub = (id: string) => ({
	id,
	status: 'active',
	created: 1700000000,
	customer: { email: 'ops@acme.io', name: 'Acme' },
	items: { data: [item] }
});
const subscriptions: Record<string, object> = {
	sub_monthly: {
		id: 'sub_monthly',
		status: 'active',
		created: 1700000000,
		customer: { email: 'm@acme.io' },
		items: {
			data: [
				{ ...item, price: { product: 'prod_app' }, current_period_start: periodEnd - 30 * 86400 }
			]
		}
	},
	sub_deleted: {
		id: 'sub_deleted',
		status: 'canceled',
		ended_at: 3_999_000_000,
		created: 1700000000,
		customer: { id: 'cus_1', deleted: true },
		items: { data: [item] }
	},
	sub_other: {
		id: 'sub_other',
		status: 'active',
		customer: { email: 'x@x.io' },
		items: { data: [{ ...item, price: { product: 'prod_other' } }] }
	},
	sub_unpaid: {
		id: 'sub_unpaid',
		status: 'incomplete',
		customer: { email: 'x@x.io' },
		items: { data: [item] }
	}
};
const sessions: Record<string, object> = {
	cs_1: {
		id: 'cs_1',
		mode: 'payment',
		payment_status: 'paid',
		created: 1780000000,
		customer_details: { email: 'buyer@acme.io', name: 'Buyer' },
		payment_intent: { latest_charge: { refunded: false } },
		line_items: {
			data: [
				{
					quantity: 3,
					price: { product: 'prod_app', metadata: { plan: 'lifetime', quantity: 'seats' } }
				}
			]
		}
	},
	cs_pending: {
		id: 'cs_pending',
		mode: 'payment',
		payment_status: 'unpaid',
		line_items: { data: [{ price: { product: 'prod_app' } }] }
	}
};

type Sent = { url: string; headers: Record<string, string>; body: Record<string, any> };

// Stands in for Stripe and the mail service. Returns what Stripe was asked and
// what was mailed.
function fakeFetch(t: TestContext) {
	const asked: string[] = [];
	const mails: Sent[] = [];
	const state = { mailDown: false };
	t.mock.method(globalThis, 'fetch', async (input: string | URL, init?: RequestInit) => {
		const url = new URL(input);
		const headers = (init?.headers ?? {}) as Record<string, string>;
		if (url.host !== 'api.stripe.com') {
			if (state.mailDown) return new Response('down', { status: 503 });
			mails.push({ url: url.href, headers, body: JSON.parse(init!.body as string) });
			return Response.json({ id: 'mail_1' });
		}

		asked.push(url.pathname);
		assert.match(headers['Stripe-Version'], /^\d{4}-\d{2}-\d{2}\./, 'the API version is pinned');
		const failure = (status: number, message: string) =>
			Response.json({ error: { message } }, { status });
		if (headers.Authorization === 'Bearer sk_bad') return failure(401, 'Invalid API Key provided');

		const after = url.searchParams.get('starting_after');
		if (url.pathname === '/v1/subscriptions') {
			assert.equal(url.searchParams.get('status'), 'all');
			assert.equal(url.searchParams.get('expand[]'), 'data.customer');
			if (!after) {
				const data = [paidSub('sub_ok_1'), subscriptions.sub_other, subscriptions.sub_unpaid];
				return Response.json({ has_more: true, data });
			}
			assert.equal(after, 'sub_unpaid');
			return Response.json({ has_more: false, data: [subscriptions.sub_deleted] });
		}
		if (url.pathname === '/v1/checkout/sessions') {
			if (headers.Authorization === 'Bearer sk_subs_only') {
				return failure(403, 'The key lacks the permission');
			}
			assert.equal(url.searchParams.get('status'), 'complete');
			return Response.json({ has_more: false, data: Object.values(sessions) });
		}

		const id = url.pathname.split('/').at(-1)!;
		if (id === 'sub_down') return new Response('', { status: 500 });
		const found = url.pathname.startsWith('/v1/checkout/sessions/')
			? sessions[id]
			: (subscriptions[id] ?? (id.startsWith('sub_ok') ? paidSub(id) : undefined));
		return found ? Response.json(found) : failure(404, 'No such object');
	});
	return { asked, mails, state };
}

function call(
	method: string,
	path: string,
	body?: string,
	headers: Record<string, string> = {},
	with_: Env = env
): Promise<Response> {
	return handle(new Request('https://worker.test' + path, { method, body, headers }), with_);
}

const post = (path: string, body: string, headers?: Record<string, string>) =>
	call('POST', path, body, headers);

async function keyFor(id: string, fields: Partial<Claims> = {}, signer = privateB64) {
	const claims: Claims = { app: 'MYAPP', plan: 'pro', email: '', exp: 1, id, meta: {}, ...fields };
	return issue(claims, await importPrivateKey(signer));
}

const claimsOf = (key: string) => verify(key, 'MYAPP', vectors.public_key);

test('a signed key verifies and the same license gives the same key', async () => {
	const privateKey = await importPrivateKey(privateB64);
	const claims: Claims = {
		app: 'MYAPP',
		plan: 'te"am',
		email: 'ops@acme.io',
		exp: periodEnd,
		id: 'sub_1',
		meta: { tier: '<gold>', seats: '5' }
	};
	const key = await issue(claims, privateKey);
	assert.deepEqual(await claimsOf(key), { v: 1, ...claims });

	const shuffled = { ...claims, meta: { seats: '5', tier: '<gold>' } };
	assert.equal(await issue(shuffled, privateKey), key, 'the order of the meta does not matter');

	const bare = await claimsOf(await issue({ ...claims, id: '', meta: {} }, privateKey));
	assert.ok(!('id' in bare) && !('meta' in bare), 'empty id and meta are left out');
});

test('the mail holds the key and nothing a setting smuggles in', () => {
	const claims: Claims = {
		app: 'MYAPP',
		plan: 'pro',
		email: 'a@x.io',
		exp: 4001209600,
		id: '',
		meta: {}
	};
	const mail = render('MYAPP-KEY', claims, {
		name: 'My <App>',
		accent: 'red; background: url(x)',
		logo: 'javascript:alert(1)',
		footer: 'Ask <us> at https://example.com/help.',
		company: 'Example GmbH, mail@example.com'
	});
	assert.equal(mail.subject, 'Your My <App> license key');
	assert.match(mail.text, /MYAPP-KEY/);
	assert.match(mail.text, /Valid until: 16 October 2096/);
	assert.ok(mail.html.includes('MYAPP-KEY'), 'the html mail holds the same key');
	assert.ok(mail.html.includes('My &lt;App&gt;'), 'a product name cannot bring markup');
	assert.ok(
		mail.html.includes('#2f3e8f') && !mail.html.includes('red;'),
		'only a hex color gets in'
	);
	assert.ok(!mail.html.includes('javascript:'), 'only an https logo gets in');
	assert.ok(mail.html.includes('Ask &lt;us&gt; at <a href="https://example.com/help"'));
	assert.ok(mail.html.includes('href="mailto:mail@example.com"'));

	const forever = render('MYAPP-KEY', { ...claims, exp: 0 }, {});
	assert.match(forever.text, /It never expires\./);
	assert.equal(forever.subject, 'Your MYAPP license key', 'without a name the prefix stands in');
});

for (const c of cases) {
	test(`license: ${c.name}`, () => {
		const decide = () =>
			c.kind === 'session'
				? sessionLicense(c.object, c.product, c.now)
				: subscriptionLicense(c.object, c.product, c.now);
		if (c.error) {
			assert.throws(decide, c.error === 'not_owed' ? NotOwed : OtherProduct);
			return;
		}
		assert.deepEqual(decide(), { customer_gone: false, ...c.want });
	});
}

// A key must never reach further than what was paid. 0 means no key is owed.
const paidUntil: [string, object, number][] = [
	['active', { status: 'active' }, periodEnd],
	['trialing', { status: 'trialing' }, periodEnd],
	['renewal not paid yet', { status: 'past_due' }, periodStart],
	['renewal never paid', { status: 'unpaid' }, periodStart],
	['trial ended without a card', { status: 'paused' }, periodStart],
	['canceled at period end', { status: 'canceled', ended_at: periodEnd }, periodEnd],
	['canceled mid period', { status: 'canceled', ended_at: 1_798_000_000 }, 1_798_000_000],
	[
		'canceled after failed payments',
		{
			status: 'canceled',
			ended_at: 1_798_000_000,
			cancellation_details: { reason: 'payment_failed' }
		},
		periodStart
	],
	['first payment open', { status: 'incomplete' }, 0],
	['first payment never came', { status: 'incomplete_expired' }, 0]
];
for (const [name, fields, want] of paidUntil) {
	test(`paid until: ${name}`, () => {
		const sub = { id: 'sub_1', status: '', items: { data: [item] }, ...fields };
		const product = { id: 'prod_app', plan: 'pro', grace_days: 14, key_days: 0 };
		if (want === 0) {
			assert.throws(() => subscriptionLicense(sub, product, 0), NotOwed);
			return;
		}
		assert.equal(subscriptionLicense(sub, product, 0).expires_at, want + grace);
	});
}

test('refresh trades a key for the one that is owed now', async (t) => {
	const { asked } = fakeFetch(t);

	// The key of last period, already run out.
	const old = await keyFor('sub_ok_refresh', { email: 'old@acme.io' });
	const response = await post('/refresh', old + '\n');
	assert.equal(response.status, 200, 'an expired key still refreshes, the renewal may come late');
	const key = await response.text();
	const claims = await claimsOf(key);
	assert.equal(claims.exp, periodEnd + grace);
	assert.equal(claims.plan, 'team', 'the plan follows the price');
	assert.equal(claims.email, 'ops@acme.io');
	assert.equal(claims.id, 'sub_ok_refresh');

	assert.equal(await (await post('/refresh', key)).text(), key, 'nothing new was paid');
	assert.equal(asked.length, 1, 'a key that keeps asking does not keep asking Stripe');

	const bought = await claimsOf(await (await post('/refresh', await keyFor('cs_1'))).text());
	assert.equal(bought.exp, 0, 'a one time purchase never expires');
	assert.equal(bought.plan, 'lifetime');
	assert.deepEqual(bought.meta, { seats: '3' }, 'the quantity becomes the seats');

	const orphan = await keyFor('sub_deleted', { email: 'left@acme.io' });
	const kept = await claimsOf(await (await post('/refresh', orphan)).text());
	assert.equal(kept.email, 'left@acme.io', 'Stripe forgot the deleted customer, the key did not');
	assert.equal(kept.exp, 3_999_000_000 + grace);

	const manual = await keyFor('lic_abc', { exp: 0, email: 'a@x.io' });
	assert.equal(await (await post('/refresh', manual)).text(), manual, 'a hand issued key stays');
	const mangled = `"${manual.slice(0, 40)}\r\n${manual.slice(40)}"`;
	assert.equal(await (await post('/refresh', mangled)).text(), manual, 'and comes back clean');
	const noId = await keyFor('', { exp: 0 });
	assert.equal(await (await post('/refresh', noId)).text(), noId, 'so does a key without an id');

	const forged = await keyFor('sub_ok_refresh', {}, strangerB64);
	assert.equal(
		(await post('/refresh', forged)).status,
		400,
		'only keys of the product reach Stripe'
	);
	assert.equal((await post('/refresh', 'MYAPP-AAAAA')).status, 400);
	assert.equal((await post('/refresh', 'x'.repeat(9000))).status, 413);

	const refused: Record<string, number> = {
		'I-FOREIGN': 404, // only lic_ ids are hand issued, any other is Stripe's
		sub_gone: 404,
		sub_other: 404,
		sub_unpaid: 404,
		sub_down: 502
	};
	for (const [id, want] of Object.entries(refused)) {
		const answer = await post('/refresh', await keyFor(id));
		assert.equal(answer.status, want, id);
		assert.ok(!(await answer.text()).includes('MYAPP-'), `no key goes out for ${id}`);
	}
});

test('webhook mails the key once', async (t) => {
	const { mails, state } = fakeFetch(t);
	const send = (body: string, at = Math.floor(Date.now() / 1000), signedBody = body) => {
		const mac = createHmac('sha256', 'whsec_test').update(`${at}.${signedBody}`).digest('hex');
		return post('/webhook', body, { 'Stripe-Signature': `t=${at},v1=${mac},v0=old` });
	};
	const paid = (event: string, sub: string, reason: string) =>
		JSON.stringify({
			id: event,
			type: 'invoice.paid',
			data: {
				object: { billing_reason: reason, parent: { subscription_details: { subscription: sub } } }
			}
		});
	const bought = (event: string, session: string, mode = 'payment') =>
		JSON.stringify({
			id: event,
			type: 'checkout.session.completed',
			data: { object: { id: session, mode } }
		});

	const first = paid('evt_1', 'sub_ok_webhook', 'subscription_create');
	assert.equal((await send(first)).status, 200);
	assert.equal((await send(first)).status, 200);
	assert.equal(mails.length, 1, 'Stripe repeats events, the customer wants one mail');
	assert.equal(mails[0].url, 'https://api.resend.com/emails');
	assert.equal(mails[0].body.from, 'Keys <keys@example.com>');
	assert.deepEqual(mails[0].body.to, ['ops@acme.io']);
	const key = mails[0].body.text.match(/MYAPP-\S+/)![0];
	assert.equal((await claimsOf(key)).id, 'sub_ok_webhook');
	assert.ok(mails[0].body.html.includes(key), 'the html mail holds the same key');

	const older = JSON.stringify({
		id: 'evt_2',
		type: 'invoice.paid',
		data: { object: { subscription: 'sub_ok_webhook', parent: null } }
	});
	assert.equal((await send(older)).status, 200);
	assert.equal(mails.length, 2, 'older API versions name the subscription on the invoice');

	const quiet: Record<string, string> = {
		'another event': JSON.stringify({ type: 'invoice.created', data: { object: {} } }),
		'a one off invoice': JSON.stringify({
			type: 'invoice.paid',
			data: { object: { parent: null } }
		}),
		'the subscription of another product': paid('evt_3', 'sub_other', 'subscription_create'),
		'a monthly renewal': paid('evt_4', 'sub_monthly', 'subscription_cycle'),
		'a purchase that is not paid yet': bought('evt_5', 'cs_pending'),
		'the session of a subscription': bought('evt_6', 'cs_1', 'subscription')
	};
	for (const [name, body] of Object.entries(quiet)) {
		assert.equal((await send(body)).status, 200, name);
	}
	assert.equal(mails.length, 2, 'none of these owe a mail');

	// A monthly subscription still gets its first key, a longer period every key.
	assert.equal((await send(paid('evt_7', 'sub_monthly', 'subscription_create'))).status, 200);
	assert.equal((await send(paid('evt_8', 'sub_ok_webhook', 'subscription_cycle'))).status, 200);
	assert.equal(mails.length, 4);
	assert.deepEqual(mails[2].body.to, ['m@acme.io']);

	for (let i = 0; i < 2; i++) assert.equal((await send(bought('evt_9', 'cs_1'))).status, 200);
	assert.equal(mails.length, 5, 'a one time purchase sends its key once');
	assert.deepEqual(mails[4].body.to, ['buyer@acme.io']);

	const again = paid('evt_10', 'sub_ok_webhook', 'subscription_create');
	assert.equal((await send(again, undefined, first)).status, 400, 'the signature covers the body');
	assert.equal((await send(again, 1700000000)).status, 400, 'an old event cannot be replayed');
	assert.equal((await post('/webhook', again)).status, 400);

	// Stripe retries on an error, so these keys are not lost.
	const early = paid('evt_11', 'sub_unpaid', 'subscription_create');
	assert.equal((await send(early)).status, 500, 'paid invoice, subscription not active yet');
	assert.equal((await send(paid('evt_12', 'sub_gone', 'subscription_create'))).status, 500);
	state.mailDown = true;
	assert.equal((await send(again)).status, 500, 'the mail service is down');
	state.mailDown = false;
	assert.equal((await send(again)).status, 200, 'a failed mail is not remembered as sent');
	assert.equal(mails.length, 6);
});

test('the mail goes out through Scaleway when that is what is set', async (t) => {
	const { mails } = fakeFetch(t);
	const scw: Env = {
		...env,
		RESEND_API_KEY: undefined,
		SCW_SECRET_KEY: 'scw_secret',
		SCW_PROJECT_ID: 'project_1'
	};
	const body = JSON.stringify({
		id: 'evt_scw',
		type: 'checkout.session.completed',
		data: { object: { id: 'cs_1', mode: 'payment' } }
	});
	const at = Math.floor(Date.now() / 1000);
	const mac = createHmac('sha256', 'whsec_test').update(`${at}.${body}`).digest('hex');
	const headers = { 'Stripe-Signature': `t=${at},v1=${mac}` };
	assert.equal((await call('POST', '/webhook', body, headers, scw)).status, 200);

	assert.equal(mails.length, 1);
	assert.match(
		mails[0].url,
		/api\.scaleway\.com\/transactional-email\/.*\/regions\/fr-par\/emails$/
	);
	assert.equal(mails[0].headers['X-Auth-Token'], 'scw_secret');
	assert.deepEqual(mails[0].body.from, { name: 'Keys', email: 'keys@example.com' });
	assert.deepEqual(mails[0].body.to, [{ email: 'buyer@acme.io' }]);
	assert.equal(mails[0].body.project_id, 'project_1');
});

test('licenses hands the dashboard what the worker hands out', async (t) => {
	fakeFetch(t);
	const auth = { Authorization: 'Bearer ' + env.INKAN_WORKER_TOKEN };
	const get = (query: string, headers: Record<string, string> = auth, with_ = env) =>
		call('GET', '/licenses' + query, undefined, headers, with_);

	type Listed = { id: string; key: string; expires_at: number };
	type Page = { product: { public_key: string }; licenses: Listed[]; next: string | null };
	const pages: Page[] = [];
	let cursor: string | null = '';
	while (cursor !== null) {
		const response = await get('?cursor=' + encodeURIComponent(cursor));
		assert.equal(response.status, 200);
		const page = (await response.json()) as Page;
		pages.push(page);
		cursor = page.next;
	}
	// The dashboard reads these pages in its own tests, they pin WORKER.md.
	const fixture = new URL('./pages.json', import.meta.url);
	if (process.env.UPDATE) writeFileSync(fixture, JSON.stringify(pages, null, '\t') + '\n');
	assert.deepEqual(pages, read('./pages.json'), 'run with UPDATE=1 after a deliberate change');

	const listed = pages.flatMap((page) => page.licenses);
	assert.deepEqual(
		listed.map((license) => license.id),
		['sub_ok_1', 'sub_deleted', 'cs_1'],
		'unpaid and foreign ones are left out'
	);
	for (const license of listed) {
		const claims = await verify(license.key, 'MYAPP', pages[0].product.public_key);
		assert.equal(claims.id, license.id, 'every license comes with its key');
		assert.equal(claims.exp, license.expires_at);
	}
	const refreshed = await (await post('/refresh', await keyFor('sub_ok_1'))).text();
	assert.equal(refreshed, pages[0].licenses[0].key, 'the very key a refresh gives');

	assert.equal((await get('', {})).status, 401);
	assert.equal((await get('', { Authorization: 'Bearer ' + 'x'.repeat(40) })).status, 401);
	assert.equal((await get('', auth, { ...env, INKAN_WORKER_TOKEN: undefined })).status, 404);
	assert.equal((await get('', auth, { ...env, INKAN_WORKER_TOKEN: 'short' })).status, 500);

	const subsOnly = await get('?cursor=p%3A', auth, { ...env, INKAN_STRIPE_KEY: 'sk_subs_only' });
	assert.deepEqual(
		{ ...(await subsOnly.json()), product: undefined },
		{ licenses: [], skipped: ['purchases'], next: null, product: undefined },
		'a key without access to Checkout still lists its subscriptions'
	);

	const bad = await get('', auth, { ...env, INKAN_STRIPE_KEY: 'sk_bad' });
	assert.equal(bad.status, 502);
	assert.match(
		await bad.text(),
		/Invalid API Key provided/,
		"Stripe's own words reach the dashboard"
	);
});

test('a worker says what it is missing', async () => {
	const bare = { ...env, RESEND_API_KEY: undefined };
	const webhook = await call('POST', '/webhook', '{}', {}, bare);
	assert.equal(webhook.status, 404, 'without a mail service there is no webhook');
	assert.equal((await call('GET', '/healthz', undefined, {}, bare)).status, 200);

	const broken = await call('GET', '/healthz', undefined, {}, { ...env, INKAN_STRIPE_PRODUCT: '' });
	assert.equal(broken.status, 500);
	assert.equal(await broken.text(), 'not set: INKAN_STRIPE_PRODUCT');

	const mangled = { ...env, INKAN_PRIVATE_KEY: 'nope' };
	assert.equal((await call('POST', '/refresh', 'MYAPP-AAAAA', {}, mangled)).status, 500);
});

test('the Scaleway entry speaks its events', async (t) => {
	const before = { ...process.env };
	t.after(() => {
		process.env = before;
	});
	Object.assign(process.env, { ...env, RESEND_API_KEY: '' });

	assert.deepEqual(await scaleway({ httpMethod: 'GET', path: '/healthz' }), {
		statusCode: 200,
		headers: { 'content-type': 'text/plain; charset=utf-8' },
		body: 'ok'
	});

	const manual = await keyFor('lic_abc', { exp: 0 });
	const answer = await scaleway({
		httpMethod: 'POST',
		path: '/refresh',
		body: Buffer.from(manual).toString('base64'),
		isBase64Encoded: true
	});
	assert.equal(answer.body, manual);
});
