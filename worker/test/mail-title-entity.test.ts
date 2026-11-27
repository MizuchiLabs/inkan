import assert from 'node:assert/strict';
import { test } from 'node:test';

import { render } from '../src/mail.ts';

const claims = {
	app: 'APP',
	plan: 'pro',
	email: 'a@x.io',
	exp: 0,
	id: 'sub_1',
	fp: '',
	meta: {}
};

test('a mail title with an out of range character reference still renders a subject', () => {
	const template =
		'<html><head><title>Key &#99999999; for {{name}}</title></head><body>{{key}}</body></html>';
	const mail = render('APP-KEY', claims, { name: 'My App' }, template);
	assert.match(mail.subject, /for My App/);
	assert.match(mail.html, /APP-KEY/);
});

test('a numeric reference in a mail title is decoded once', () => {
	const template =
		'<html><head><title>Use &#38;lt; for {{name}}</title></head><body>{{key}}</body></html>';
	const mail = render('APP-KEY', claims, { name: 'My App' }, template);
	assert.equal(mail.subject, 'Use &lt; for My App');
});
