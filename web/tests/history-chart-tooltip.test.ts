// Run with TZ=America/New_York. The API's day is "YYYY-MM-DD in UTC", so the
// tooltip has to label it in UTC too, or zones west of UTC see the day before.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { formatDay } from '../src/lib/license.ts';

test('a UTC day from the stats is labelled with that same day in the tooltip', () => {
	const day = new Date('2024-03-10'); // what HistoryChart builds from "2024-03-10"
	assert.equal(formatDay(day), 'Mar 10, 2024');
});
