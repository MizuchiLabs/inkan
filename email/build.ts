// Renders the mail with the placeholders the worker fills in and writes it
// where the worker reads it.

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
		expires: '{{expires}}'
	})
);
if (!html.includes('{{key}}')) throw new Error('the mail shows no licenseKey');

const out = new URL('../worker/src/template.ts', import.meta.url);
writeFileSync(
	out,
	'// Written by the build in ../../email. Empty sends the built-in mail.\n' +
		`export const template =\n\t${JSON.stringify(html)};\n`
);
console.log(`wrote ${out.pathname}, deploy the worker to send it`);
