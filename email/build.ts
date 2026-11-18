// Renders the mail with the placeholders the worker fills in and writes it
// as mail.html, for inkan worker export to put into a worker.

import { writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

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

const out = fileURLToPath(new URL('./mail.html', import.meta.url));
writeFileSync(out, html);
console.log(`wrote ${out}\nsend it with: inkan worker export <PREFIX> --mail '${out}'`);
