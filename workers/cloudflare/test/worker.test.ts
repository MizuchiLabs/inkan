import assert from 'node:assert/strict';
import { createHmac } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

import { verify } from '../../../sdk/ts/src/index.ts';
import worker, { type Env } from '../src/index.ts';
import { importPrivateKey, issue, payload, type Claims } from '../src/sign.ts';
import { NotOwed, OtherProduct, sessionLicense, subscriptionLicense } from '../src/stripe.ts';

const read = (path: string) => JSON.parse(readFileSync(new URL('../../../' + path, import.meta.url), 'utf8'));
const vectors = read('license/testdata/vectors.json');
const cases = read('internal/stripe/testdata/licenses.json');

// The Go private key format, base64 of seed and public key.
const hexBytes = (hex: string) => Buffer.from(hex, 'hex');
const privateB64 = Buffer.concat([hexBytes(vectors.seed), hexBytes(vectors.public_key)]).toString('base64');

for (const c of vectors.sign) {
	test(`sign: ${c.name}`, async () => {
		const claims: Claims = {
			app: c.app,
			plan: c.claims.plan,
			email: c.claims.email,
			exp: c.claims.exp,
			id: c.claims.id ?? '',
			meta: c.claims.meta ?? {}
		};
		assert.equal(payload(claims), c.payload);
		assert.equal(await issue(claims, await importPrivateKey(privateB64)), c.key);
	});
}

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
		assert.deepEqual(decide(), c.want);
	});
}

const env: Env = {
	INKAN_PREFIX: 'MYAPP',
	INKAN_PRIVATE_KEY: privateB64,
	INKAN_STRIPE_KEY: 'sk_test',
	INKAN_STRIPE_PRODUCT: 'prod_app',
	INKAN_PLAN: 'pro',
	INKAN_STRIPE_WEBHOOK_SECRET: 'whsec_test',
	INKAN_MAIL_FROM: 'keys@example.com',
	RESEND_API_KEY: 're_test'
};

const sub = {
	id: 'sub_1',
	status: 'active',
	created: 1700000000,
	customer: { email: 'ops@acme.io', name: 'Acme' },
	items: {
		data: [
			{
				price: { product: 'prod_app', metadata: { plan: 'team' } },
				current_period_start: 1797000000,
				current_period_end: 4000000000
			}
		]
	}
};
const session = {
	id: 'cs_1',
	mode: 'payment',
	payment_status: 'paid',
	created: 1780000000,
	customer_details: { email: 'buyer@acme.io' },
	line_items: { data: [{ quantity: 3, price: { product: 'prod_app', metadata: { quantity: 'seats' } } }] }
};

// Stands in for Stripe and Resend. Returns what was asked and what was mailed.
function fakeFetch(t: { mock: { method: Function } }) {
	const asked: string[] = [];
	const mails: { to: string[]; text: string }[] = [];
	t.mock.method(globalThis, 'fetch', async (input: string, init?: RequestInit) => {
		const url = new URL(input);
		if (url.host === 'api.resend.com') {
			mails.push(JSON.parse(init!.body as string));
			return Response.json({ id: 'mail_1' });
		}
		asked.push(url.pathname);
		if (url.pathname === '/v1/subscriptions/sub_1') return Response.json(sub);
		if (url.pathname === '/v1/checkout/sessions/cs_1') return Response.json(session);
		return Response.json({ error: { message: 'No such subscription' } }, { status: 404 });
	});
	return { asked, mails };
}

function post(path: string, body: string, headers: Record<string, string> = {}): Promise<Response> {
	return worker.fetch(new Request('https://worker.test' + path, { method: 'POST', body, headers }), env);
}

async function keyFor(id: string, exp = 1): Promise<string> {
	const claims: Claims = { app: 'MYAPP', plan: 'pro', email: '', exp, id, meta: {} };
	return issue(claims, await importPrivateKey(privateB64));
}

test('refresh trades a key for the one that is owed now', async (t) => {
	const { asked } = fakeFetch(t);

	const response = await post('/refresh', (await keyFor('sub_1')) + '\n');
	assert.equal(response.status, 200);
	const key = await response.text();
	const claims = await verify(key, 'MYAPP', vectors.public_key);
	assert.equal(claims.exp, 4000000000 + 14 * 86400);
	assert.equal(claims.plan, 'team');
	assert.equal(claims.email, 'ops@acme.io');

	assert.equal(await (await post('/refresh', key)).text(), key);
	assert.equal(asked.length, 1, 'a key that keeps asking does not keep asking Stripe');

	const bought = await (await post('/refresh', await keyFor('cs_1'))).text();
	assert.deepEqual((await verify(bought, 'MYAPP', vectors.public_key)).meta, { seats: '3' });

	const manual = await keyFor('lic_abc', 0);
	assert.equal(await (await post('/refresh', `"${manual}"`)).text(), manual, 'a hand issued key comes back as it is');

	assert.equal((await post('/refresh', await keyFor('sub_gone'))).status, 404);
	assert.equal((await post('/refresh', 'MYAPP-AAAAA')).status, 400);
	assert.equal((await post('/refresh', vectors.verify[9].key)).status, 400, 'signed with another key');
});

test('webhook mails the key once', async (t) => {
	const { mails } = fakeFetch(t);
	const send = (body: string, at = Math.floor(Date.now() / 1000)) => {
		const mac = createHmac('sha256', 'whsec_test').update(`${at}.${body}`).digest('hex');
		return post('/webhook', body, { 'Stripe-Signature': `t=${at},v1=${mac},v0=old` });
	};

	const paid = JSON.stringify({
		id: 'evt_1',
		type: 'invoice.paid',
		data: {
			object: { billing_reason: 'subscription_create', parent: { subscription_details: { subscription: 'sub_1' } } }
		}
	});
	assert.equal((await send(paid)).status, 200);
	assert.equal((await send(paid)).status, 200);
	assert.equal(mails.length, 1, 'Stripe repeats events, the customer wants one mail');
	assert.deepEqual(mails[0].to, ['ops@acme.io']);
	const key = mails[0].text.match(/MYAPP-\S+/)![0];
	assert.equal((await verify(key, 'MYAPP', vectors.public_key)).id, 'sub_1');
	assert.match(mails[0].text, /valid until 16 October 2096/);

	const bought = JSON.stringify({
		id: 'evt_2',
		type: 'checkout.session.completed',
		data: { object: { id: 'cs_1', mode: 'payment' } }
	});
	assert.equal((await send(bought)).status, 200);
	assert.equal(mails.length, 2);
	assert.deepEqual(mails[1].to, ['buyer@acme.io']);

	assert.equal((await send(paid, 1700000000)).status, 400, 'an old event cannot be replayed');
	assert.equal((await post('/webhook', paid)).status, 400);
	const gone = paid.replace('evt_1', 'evt_3').replace('sub_1', 'sub_gone');
	assert.equal((await send(gone)).status, 500, 'Stripe retries on an error');
	assert.equal(mails.length, 2);
});

test('without mail settings there is no webhook', async () => {
	const bare = { ...env, RESEND_API_KEY: undefined };
	const request = new Request('https://worker.test/webhook', { method: 'POST', body: '{}' });
	assert.equal((await worker.fetch(request, bare)).status, 404);
	assert.equal((await worker.fetch(new Request('https://worker.test/healthz'), bare)).status, 200);
});
