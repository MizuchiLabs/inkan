<script lang="ts">
	import { useStats } from '#lib/api/queries.js';
	import * as Chart from '#lib/components/ui/chart/index.js';
	import { formatDate } from '#lib/license.js';
	import { AreaChart } from 'layerchart';

	type Props = {
		// Every product when left out.
		productId?: number;
	};
	let { productId }: Props = $props();

	const stats = useStats();

	const days = $derived.by(() => {
		const totals = new Map<string, number>();
		for (const s of stats.data ?? []) {
			if (productId !== undefined && s.product_id !== productId) continue;
			totals.set(s.day, (totals.get(s.day) ?? 0) + s.active);
		}
		return Array.from(totals, ([day, active]) => ({ date: new Date(day), active }));
	});

	// The year would push the last tick out of the card.
	const shortDate = new Intl.DateTimeFormat(undefined, {
		month: 'short',
		day: 'numeric',
		timeZone: 'UTC'
	});

	const config = {
		active: { label: 'Active', color: 'var(--chart-1)' }
	} satisfies Chart.ChartConfig;
</script>

<!-- One day is a dot, not a trend. -->
{#if days.length > 1}
	<section
		class="flex flex-col gap-2 rounded-2xl border bg-card p-4"
		aria-labelledby="history-title"
	>
		<h2 id="history-title" class="text-sm font-medium">Active licenses</h2>
		<Chart.Container {config} class="aspect-auto h-36 w-full">
			<AreaChart
				data={days}
				x="date"
				series={[{ key: 'active', label: config.active.label, color: config.active.color }]}
				props={{
					area: { 'fill-opacity': 0.15, line: { class: 'stroke-2' } },
					xAxis: { format: (day: Date) => shortDate.format(day), ticks: 4 },
					yAxis: {
						format: (count: number) => (Number.isInteger(count) ? String(count) : ''),
						ticks: 3
					}
				}}
			>
				{#snippet tooltip()}
					<Chart.Tooltip
						labelFormatter={(day: Date) => formatDate(day.toISOString())}
						indicator="line"
					/>
				{/snippet}
			</AreaChart>
		</Chart.Container>
	</section>
{/if}
