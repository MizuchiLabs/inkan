// Renders the mail with the placeholders the worker fills in, ready to upload
// in the dashboard.

import { mkdirSync, writeFileSync } from 'node:fs';

import { createElement } from 'react';
import { render } from 'react-email';

import License from './emails/license.tsx';

const html = await render(
	createElement(License, {
		licenseKey: '{{key}}',
		name: '{{name}}',
		plan: '{{plan}}',
		email: '{{email}}',
		expires: '{{expires}}',
		footer: '{{footer}}',
		company: '{{company}}'
	})
);
if (!html.includes('{{key}}')) throw new Error('the mail shows no licenseKey');

const out = new URL('dist/license.html', import.meta.url);
mkdirSync(new URL('dist/', import.meta.url), { recursive: true });
writeFileSync(out, html);
console.log(`wrote ${out.pathname}, upload it under Edit on the product, in the Mail tab`);
