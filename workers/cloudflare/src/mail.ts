// The built in key mail. It is the one of `inkan worker`, down to the byte:
// worker/templates in the repository holds the original, and both are tested
// against worker/testdata/mails.json. Change them together.

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

export function render(key: string, claims: Claims, brand: Brand): { subject: string; text: string; html: string } {
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
		logo: escape(logo),
	};
	const html = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<meta name="supported-color-schemes" content="light dark">
<title>Your ${d.name} license key</title>
<style>
@media (prefers-color-scheme: dark) {
  .page { background: #101113 !important; }
  .band { border-color: #2e3138 !important; }
  .body { background: #1b1d21 !important; border-color: #2e3138 !important; }
  .foot { background: #16181b !important; border-color: #2e3138 !important; }
  .text { color: #ededf0 !important; }
  .soft { color: #a2a6af !important; }
  .rule { border-color: #2e3138 !important; }
  .keybox { background: ${s.tintDark} !important; border-color: ${s.tintBorderDark} !important; }
  .accent { color: ${s.textDark} !important; }
}
</style>
</head>
<body class="page" style="margin:0; padding:0; background:#f1f2f5;">
<table role="presentation" class="page" width="100%" cellpadding="0" cellspacing="0" bgcolor="#f1f2f5" style="background:#f1f2f5;">
<tr><td align="center" style="padding:32px 12px; font-family:-apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;">
  <tr><td class="band" bgcolor="#15171c" style="background:#15171c; border:1px solid #15171c; border-radius:14px 14px 0 0; padding:26px 32px;">
    ${d.logo ? `<img src="${d.logo}" alt="${d.name}" height="32" style="display:block; height:32px; border:0;">` : `<span style="font-size:22px; line-height:32px; font-weight:700; letter-spacing:-0.3px; color:#ffffff;">${d.name}</span>`}
  </td></tr>
  <tr><td class="body" bgcolor="#ffffff" style="background:#ffffff; border-left:1px solid #dfe1e7; border-right:1px solid #dfe1e7; padding:36px 32px 32px;">
    <p class="accent" style="margin:0 0 10px; font-size:12px; line-height:16px; font-weight:700; letter-spacing:1.2px; text-transform:uppercase; color:${s.text};">${d.plan} license</p>
    <h1 class="text" style="margin:0 0 14px; font-size:28px; line-height:34px; font-weight:700; letter-spacing:-0.5px; color:#17181c;">Your license key</h1>
    <p class="soft" style="margin:0 0 28px; font-size:16px; line-height:26px; color:#555a65;">Here is the ${d.name} license for <strong class="text" style="color:#17181c;">${d.email}</strong>. Paste the key into the app to activate it, and keep this mail in case you need it again.</p>
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0">
    <tr><td class="keybox" bgcolor="${s.tint}" style="background:${s.tint}; border:1px solid ${s.tintBorder}; border-radius:12px; padding:18px 20px;">
      <p class="soft" style="margin:0 0 6px; font-size:11px; line-height:16px; font-weight:700; letter-spacing:1.2px; text-transform:uppercase; color:#555a65;">License key</p>
      <p class="text" style="margin:0 0 12px; font-family:ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size:13px; line-height:21px; font-weight:600; color:#17181c; word-break:break-all;">${d.key}</p>
      <p class="soft" style="margin:0; font-size:13px; line-height:20px; color:#555a65;">${expires ? `Valid until <strong class="text" style="color:#17181c;">${expires}</strong>.` : `It never expires.`}</p>
    </td></tr>
    </table>
    ${footer ? `<p class="soft rule" style="margin:28px 0 0; padding:24px 0 0; border-top:1px solid #e6e8ed; font-size:15px; line-height:23px; color:#555a65;">${linked(footer, s.text)}</p>` : ''}
  </td></tr>
  <tr><td class="foot" bgcolor="#f8f9fb" style="background:#f8f9fb; border:1px solid #dfe1e7; border-radius:0 0 14px 14px; padding:24px 32px 26px;">
    <p class="soft" style="margin:0; font-size:13px; line-height:20px; color:#555a65;">You are getting this mail because a license for ${d.name} was paid for with this address.</p>
    ${company ? `<p class="soft" style="margin:12px 0 0; font-size:12px; line-height:19px; color:#555a65;">${linked(company, s.text)}</p>` : ''}
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
	const ink = [23, 24, 28];
	const card = [27, 29, 33];
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

// The characters html/template escapes in Go, written the way it writes them.
function escape(value: string): string {
	return value
		.replaceAll('&', '&amp;')
		.replaceAll('<', '&lt;')
		.replaceAll('>', '&gt;')
		.replaceAll('"', '&#34;')
		.replaceAll("'", '&#39;')
		.replaceAll('+', '&#43;');
}
