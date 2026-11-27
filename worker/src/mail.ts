import type { Claims } from './sign.ts';

export type Brand = {
	name?: string;
	footer?: string;
	company?: string;
};

const accent = '#2f3e8f';
const linkable = /https:\/\/[^ \t\n\f\r<>"]+|[\w.+-]+@[\w-]+(?:\.[\w-]+)+/g;
const placeholder = /\{\{\s*(\w+)\s*\}\}/g;

export function usable(template: string): boolean {
	return /\{\{\s*key\s*\}\}/.test(template);
}

// A template is html with {{key}}, {{name}}, {{plan}}, {{email}}, {{expires}}, {{footer}}, {{company}}, its title is the subject.
export function render(
	key: string,
	claims: Claims,
	brand: Brand,
	template = '',
	ends = claims.exp
): { subject: string; text: string; html: string } {
	const name = brand.name || claims.app;
	const footer = brand.footer ?? '';
	const company = brand.company ?? '';
	const expires = date(ends);
	const renews =
		claims.exp !== ends
			? `${name} renews this key by itself while it is online. Without a connection the key works until ${date(claims.exp)}.`
			: '';
	let text =
		`Here is the ${name} license for ${claims.email}. Paste the key into the app to activate it, ` +
		`and keep this mail in case you need it again.\n\n${key}\n\nPlan: ${claims.plan}\n`;
	text += (expires ? `Valid until: ${expires}` : 'It never expires.') + '\n';
	if (renews) text += `${renews}\n`;
	if (footer) text += `\n${footer}\n`;
	text += `\n--\nYou are getting this mail because a license for ${name} was paid for with this address.\n`;
	if (company) text += `${company}\n`;

	if (usable(template)) {
		const values = {
			key,
			name,
			plan: claims.plan,
			email: claims.email,
			expires: expires || 'Never',
			footer,
			company
		};
		const title = template.match(/<title[^>]*>([\s\S]*?)<\/title>/i)?.[1] ?? '';
		return {
			subject: oneLine(fill(unescape(title), values) || `Your ${name} license key`),
			text,
			html: fill(template, values, escape)
		};
	}

	const d = {
		key: escape(key),
		name: escape(name),
		plan: escape(claims.plan),
		email: escape(claims.email)
	};
	const html = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="x-apple-disable-message-reformatting">
<meta name="color-scheme" content="light dark">
<meta name="supported-color-schemes" content="light dark">
<title>Your ${d.name} license key</title>
<style>
@media (max-width: 480px) {
  .pad { padding-left: 20px !important; padding-right: 20px !important; }
  .title { font-size: 24px !important; line-height: 30px !important; }
}
@media (prefers-color-scheme: dark) {
  .page { background: #10121a !important; }
  .band { border-color: #2c3040 !important; }
  .body { background: #1a1d29 !important; border-color: #2c3040 !important; }
  .foot { background: #151823 !important; border-color: #2c3040 !important; }
  .text { color: #eceef4 !important; }
  .soft { color: #a4a9ba !important; }
  .rule { border-color: #2c3040 !important; }
  .keybox { background: #1d2239 !important; border-color: #222a52 !important; }
  .accent { color: #979fc7 !important; }
}
</style>
</head>
<body class="page" style="margin:0; padding:0; background:#f3f4f8; -webkit-text-size-adjust:100%;">
<div style="display:none; max-height:0; max-width:0; overflow:hidden; opacity:0; font-size:1px; line-height:1px; color:#f3f4f8; mso-hide:all;">Your ${d.plan} license key for ${d.name}${expires ? `, valid until ${expires}` : ''}.</div>
<table role="presentation" class="page" width="100%" cellpadding="0" cellspacing="0" bgcolor="#f3f4f8" style="background:#f3f4f8;">
<tr><td align="center" style="padding:32px 12px; font-family:-apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;">
  <tr><td class="band pad" bgcolor="#1a1d2b" style="background:#1a1d2b; border:1px solid #1a1d2b; border-radius:10px 10px 0 0; padding:22px 32px;">
    <span style="font-size:20px; line-height:32px; font-weight:700; letter-spacing:-0.2px; color:#ffffff;">${d.name}</span>
  </td></tr>
  <tr><td class="body pad" bgcolor="#ffffff" style="background:#ffffff; border-left:1px solid #dcdfe8; border-right:1px solid #dcdfe8; padding:32px 32px 28px;">
    <p class="accent" style="margin:0 0 6px; font-size:12px; line-height:16px; font-weight:700; letter-spacing:1.2px; text-transform:uppercase; color:${accent};">${d.plan} license</p>
    <h1 class="text title" style="margin:0; font-size:28px; line-height:34px; font-weight:700; letter-spacing:-0.5px; color:#1c1f2e;">Your license key</h1>
    <p class="soft" style="margin:16px 0 24px; font-size:16px; line-height:26px; color:#5b6072;">Paste the key into ${d.name} to activate it. Keep this mail in case you need the key again.</p>
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0">
    <tr><td class="keybox" bgcolor="#eef0f6" style="background:#eef0f6; border:1px solid #c9cde2; border-radius:8px; padding:16px 20px 18px;">
      <p class="soft" style="margin:0 0 8px; font-size:11px; line-height:16px; font-weight:700; letter-spacing:1.2px; text-transform:uppercase; color:#5b6072;">License key</p>
      <p class="text" style="margin:0; font-family:ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size:14px; line-height:23px; font-weight:600; color:#1c1f2e; word-break:break-all; -webkit-user-select:all; user-select:all;">${d.key}</p>
    </td></tr>
    </table>
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="margin-top:12px;">
    <tr>
      <td class="soft rule" valign="top" style="padding:12px 0; border-bottom:1px solid #e6e8ef; font-size:14px; line-height:20px; color:#5b6072; white-space:nowrap;">Licensed to</td>
      <td class="text rule" align="right" valign="top" style="padding:12px 0 12px 16px; border-bottom:1px solid #e6e8ef; font-size:14px; line-height:20px; font-weight:600; color:#1c1f2e; word-break:break-all;">${d.email}</td>
    </tr>
    <tr>
      <td class="soft" valign="top" style="padding:12px 0 0; font-size:14px; line-height:20px; color:#5b6072; white-space:nowrap;">Valid until</td>
      <td class="text" align="right" valign="top" style="padding:12px 0 0 16px; font-size:14px; line-height:20px; font-weight:600; color:#1c1f2e;">${expires || 'Never expires'}</td>
    </tr>
    </table>
    ${renews ? `<p class="soft" style="margin:16px 0 0; font-size:13px; line-height:20px; color:#5b6072;">${escape(renews)}</p>` : ''}
    ${footer ? `<p class="soft rule" style="margin:24px 0 0; padding:20px 0 0; border-top:1px solid #e6e8ef; font-size:15px; line-height:23px; color:#5b6072;">${linked(footer)}</p>` : ''}
  </td></tr>
  <tr><td class="foot pad" bgcolor="#f8f9fc" style="background:#f8f9fc; border:1px solid #dcdfe8; border-radius:0 0 10px 10px; padding:20px 32px 22px;">
    <p class="soft" style="margin:0; font-size:13px; line-height:20px; color:#5b6072;">You are getting this mail because a license for ${d.name} was paid for with this address.</p>
    ${company ? `<p class="soft" style="margin:10px 0 0; font-size:12px; line-height:19px; color:#5b6072;">${linked(company)}</p>` : ''}
  </td></tr>
  </table>
</td></tr>
</table>
</body>
</html>
`;
	return { subject: oneLine(`Your ${name} license key`), text, html };
}

function date(unix: number): string {
	if (unix === 0) return '';
	return new Date(unix * 1000).toLocaleDateString('en-GB', {
		day: 'numeric',
		month: 'long',
		year: 'numeric',
		timeZone: 'UTC'
	});
}

// One pass, so a value that holds a placeholder is not filled in again.
function fill(
	template: string,
	values: Record<string, string>,
	safe = (value: string) => value
): string {
	return template.replace(placeholder, (all, name) =>
		Object.hasOwn(values, name) ? safe(values[name]) : all
	);
}

function oneLine(value: string): string {
	return value.split(/\s+/).filter(Boolean).join(' ');
}

function linked(text: string): string {
	let out = '';
	let last = 0;
	for (const found of text.matchAll(linkable)) {
		// The full stop after a link is the end of the sentence.
		const match = found[0].replace(/[.,;:!?)]+$/, '');
		const isLink = match.startsWith('https://');
		const href = isLink ? match : 'mailto:' + match;
		const label = isLink ? match.slice('https://'.length).replace(/\/$/, '') : match;
		out += escape(text.slice(last, found.index));
		out += `<a href="${escape(href)}" class="accent" style="color:${accent};">${escape(label)}</a>`;
		last = found.index + match.length;
	}
	return out + escape(text.slice(last));
}

function escape(value: string): string {
	return value
		.replaceAll('&', '&amp;')
		.replaceAll('<', '&lt;')
		.replaceAll('>', '&gt;')
		.replaceAll('"', '&#34;')
		.replaceAll("'", '&#39;');
}

const named: Record<string, string> = { lt: '<', gt: '>', quot: '"', amp: '&' };

function unescape(value: string): string {
	return value.replace(
		/&(?:#x([0-9a-f]+)|#([0-9]+)|(lt|gt|quot|amp));/gi,
		(all, hex, dec, name) => {
			if (name) return named[name] ?? all;
			const code = hex ? parseInt(hex, 16) : parseInt(dec, 10);
			return code <= 0x10ffff ? String.fromCodePoint(code) : all;
		}
	);
}
