// The key mail. The worker sends it and the dashboard previews it, so this
// file has to run in a browser too. For a mail of your own, change render.

import type { Claims } from './sign.ts';

export type Brand = {
	name?: string;
	logo?: string;
	accent?: string;
	footer?: string;
	company?: string;
};

const defaultAccent = '#2f3e8f';
// An https link or an email address.
const linkable = /https:\/\/[^ \t\n\f\r<>"]+|[\w.+-]+@[\w-]+(?:\.[\w-]+)+/g;

export function render(
	key: string,
	claims: Claims,
	brand: Brand
): { subject: string; text: string; html: string } {
	const name = brand.name || claims.app;
	const footer = brand.footer ?? '';
	const company = brand.company ?? '';
	const expires =
		claims.exp === 0
			? ''
			: new Date(claims.exp * 1000).toLocaleDateString('en-GB', {
					day: 'numeric',
					month: 'long',
					year: 'numeric',
					timeZone: 'UTC'
				});
	// These end up in the markup, so only what is known to be harmless gets in.
	const accent = /^#[0-9a-fA-F]{6}$/.test(brand.accent ?? '') ? brand.accent! : defaultAccent;
	const logo = brand.logo?.startsWith('https://') ? brand.logo : '';

	let text =
		`Here is the ${name} license for ${claims.email}. Paste the key into the app to activate it, ` +
		`and keep this mail in case you need it again.\n\n${key}\n\nPlan: ${claims.plan}\n`;
	text += (expires ? `Valid until: ${expires}` : 'It never expires.') + '\n';
	if (footer) text += `\n${footer}\n`;
	text += `\n--\nYou are getting this mail because a license for ${name} was paid for with this address.\n`;
	if (company) text += `${company}\n`;

	const s = shades(accent);
	const d = {
		key: escape(key),
		name: escape(name),
		plan: escape(claims.plan),
		email: escape(claims.email),
		logo: escape(logo)
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
  .keybox { background: ${s.tintDark} !important; border-color: ${s.tintBorderDark} !important; }
  .accent { color: ${s.textDark} !important; }
}
</style>
</head>
<body class="page" style="margin:0; padding:0; background:#f3f4f8; -webkit-text-size-adjust:100%;">
<div style="display:none; max-height:0; max-width:0; overflow:hidden; opacity:0; font-size:1px; line-height:1px; color:#f3f4f8; mso-hide:all;">Your ${d.plan} license key for ${d.name}${expires ? `, valid until ${expires}` : ''}.</div>
<table role="presentation" class="page" width="100%" cellpadding="0" cellspacing="0" bgcolor="#f3f4f8" style="background:#f3f4f8;">
<tr><td align="center" style="padding:32px 12px; font-family:-apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;">
  <tr><td class="band pad" bgcolor="#1a1d2b" style="background:#1a1d2b; border:1px solid #1a1d2b; border-radius:10px 10px 0 0; padding:22px 32px;">
    ${d.logo ? `<img src="${d.logo}" alt="${d.name}" height="32" style="display:block; height:32px; border:0;">` : `<span style="font-size:20px; line-height:32px; font-weight:700; letter-spacing:-0.2px; color:#ffffff;">${d.name}</span>`}
  </td></tr>
  <tr><td class="body pad" bgcolor="#ffffff" style="background:#ffffff; border-left:1px solid #dcdfe8; border-right:1px solid #dcdfe8; padding:32px 32px 28px;">
    <p class="accent" style="margin:0 0 6px; font-size:12px; line-height:16px; font-weight:700; letter-spacing:1.2px; text-transform:uppercase; color:${s.text};">${d.plan} license</p>
    <h1 class="text title" style="margin:0; font-size:28px; line-height:34px; font-weight:700; letter-spacing:-0.5px; color:#1c1f2e;">Your license key</h1>
    <p class="soft" style="margin:16px 0 24px; font-size:16px; line-height:26px; color:#5b6072;">Paste the key into ${d.name} to activate it. Keep this mail in case you need the key again.</p>
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0">
    <tr><td class="keybox" bgcolor="${s.tint}" style="background:${s.tint}; border:1px solid ${s.tintBorder}; border-radius:8px; padding:16px 20px 18px;">
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
    ${footer ? `<p class="soft rule" style="margin:24px 0 0; padding:20px 0 0; border-top:1px solid #e6e8ef; font-size:15px; line-height:23px; color:#5b6072;">${linked(footer, s.text)}</p>` : ''}
  </td></tr>
  <tr><td class="foot pad" bgcolor="#f8f9fc" style="background:#f8f9fc; border:1px solid #dcdfe8; border-radius:0 0 10px 10px; padding:20px 32px 22px;">
    <p class="soft" style="margin:0; font-size:13px; line-height:20px; color:#5b6072;">You are getting this mail because a license for ${d.name} was paid for with this address.</p>
    ${company ? `<p class="soft" style="margin:10px 0 0; font-size:12px; line-height:19px; color:#5b6072;">${linked(company, s.text)}</p>` : ''}
  </td></tr>
  </table>
</td></tr>
</table>
</body>
</html>
`;
	return { subject: `Your ${name} license key`.split(/\s+/).filter(Boolean).join(' '), text, html };
}

// The colors the mail works out from the accent, so a pale or a dark accent
// stays readable in the light and the dark mail.
function shades(accent: string) {
	const n = parseInt(accent.slice(1), 16);
	const c = [n >> 16, (n >> 8) & 0xff, n & 0xff];
	const white = [255, 255, 255];
	const ink = [28, 31, 46];
	const card = [26, 29, 41];
	// share is how many percent of to go into the mix.
	const mix = (to: number[], share: number) =>
		'#' +
		c
			.map((value, i) => Math.floor((value * (100 - share) + to[i] * share + 50) / 100))
			.map((value) => value.toString(16).padStart(2, '0'))
			.join('');
	// How bright the eye takes the color to be, 0 to 255.
	const bright = Math.floor((299 * c[0] + 587 * c[1] + 114 * c[2]) / 1000);
	return {
		text: bright > 110 ? mix(ink, 50) : accent,
		textDark: bright < 150 ? mix(white, 50) : accent,
		tint: mix(white, 92),
		tintBorder: mix(white, 74),
		tintDark: mix(card, 84),
		tintBorderDark: mix(card, 60)
	};
}

// linked escapes a text and turns its https links and email addresses into
// links of the given color.
function linked(text: string, color: string): string {
	let out = '';
	let last = 0;
	for (const found of text.matchAll(linkable)) {
		// The full stop after a link is the end of the sentence.
		const match = found[0].replace(/[.,;:!?)]+$/, '');
		const isLink = match.startsWith('https://');
		const href = isLink ? match : 'mailto:' + match;
		const label = isLink ? match.slice('https://'.length).replace(/\/$/, '') : match;
		out += escape(text.slice(last, found.index));
		out += `<a href="${escape(href)}" class="accent" style="color:${escape(color)};">${escape(label)}</a>`;
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
