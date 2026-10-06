import type { License } from '#lib/api/queries.js';

const DAY = 86_400_000;
// A license that will not renew is worth a look this long before it runs out.
const ENDING_DAYS = 30;

export type LicenseState = 'active' | 'ending' | 'expired';

export const stateLabel: Record<LicenseState, string> = {
	active: 'Active',
	ending: 'Ending soon',
	expired: 'Expired'
};

// One subscription can hold several products, so the id alone names no license.
export function refOf(license: Pick<License, 'product_id' | 'id'>): string {
	return `${license.product_id}/${license.id}`;
}

export function stateOf(license: License, now = Date.now()): LicenseState {
	if (!license.expires_at) return 'active';
	const left = new Date(license.expires_at).getTime() - now;
	if (left <= 0) return 'expired';
	if (!license.renews && left < ENDING_DAYS * DAY) return 'ending';
	return 'active';
}

const rank: Record<LicenseState, number> = { ending: 0, active: 1, expired: 2 };

// What needs a look comes first: ending soon, then active by what runs out
// next, then expired with the latest on top.
export function byAttention(a: License, b: License): number {
	const now = Date.now();
	const state = stateOf(a, now);
	const other = stateOf(b, now);
	if (state !== other) return rank[state] - rank[other];
	const [left, right] = [a, b].map((l) =>
		l.expires_at ? new Date(l.expires_at).getTime() : Infinity
	);
	if (left === right) return 0;
	return state === 'expired' ? right - left : left - right;
}

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' });
const relativeFormat = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });

export function formatDate(iso: string): string {
	return dateFormat.format(new Date(iso));
}

// The day a time falls on here, the way a date input wants it.
export function localDay(iso: string): string {
	const at = new Date(iso);
	const two = (n: number) => String(n).padStart(2, '0');
	return `${at.getFullYear()}-${two(at.getMonth() + 1)}-${two(at.getDate())}`;
}

// "in 12 days", "3 months ago", in the coarsest unit that still says something.
export function formatRelative(iso: string, now = Date.now()): string {
	const days = Math.round((new Date(iso).getTime() - now) / DAY);
	if (Math.abs(days) < 60) return relativeFormat.format(days, 'day');
	if (Math.abs(days) < 730) return relativeFormat.format(Math.round(days / 30), 'month');
	return relativeFormat.format(Math.round(days / 365), 'year');
}

// For things that happened minutes ago, like a sync.
export function formatRelativeTime(iso: string, now = Date.now()): string {
	const minutes = Math.round((new Date(iso).getTime() - now) / 60_000);
	if (Math.abs(minutes) < 1) return 'just now';
	if (Math.abs(minutes) < 60) return relativeFormat.format(minutes, 'minute');
	if (Math.abs(minutes) < 1440) return relativeFormat.format(Math.round(minutes / 60), 'hour');
	return formatRelative(iso, now);
}

// Metadata is typed as one key=value per line.
export function parseMeta(text: string): Record<string, string> {
	const meta: Record<string, string> = {};
	for (const line of text.split('\n')) {
		const at = line.indexOf('=');
		if (at <= 0) continue;
		const key = line.slice(0, at).trim();
		if (key) meta[key] = line.slice(at + 1).trim();
	}
	return meta;
}

export function formatMeta(meta: Record<string, string>): string {
	return Object.entries(meta)
		.map(([key, value]) => `${key}=${value}`)
		.join('\n');
}
