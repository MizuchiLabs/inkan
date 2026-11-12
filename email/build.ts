// Renders the mail with the placeholders the worker fills in and writes it
// into the worker, which sends it from its next build on.

import { writeFileSync } from 'node:fs';

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

const out = new URL('../worker/src/template.ts', import.meta.url);
writeFileSync(
	out,
	'// Written by pnpm build in the email folder. Empty sends the built-in mail.\n' +
		`export const template: string =\n\t${JSON.stringify(html)};\n`
);
console.log(`wrote ${out.pathname}, build and deploy the worker to send it`);
