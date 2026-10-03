<script lang="ts">
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { stateLabel, type LicenseState } from '#lib/license.js';
	import { cn } from '#lib/utils.js';

	type Props = {
		state: LicenseState;
		// Only the dot, for places too narrow for the label.
		compact?: boolean;
		class?: string;
	};
	let { state, compact = false, class: className }: Props = $props();

	// chart-3 is fine, chart-4 needs a look.
	const dot: Record<LicenseState, string> = {
		active: 'bg-chart-3',
		ending: 'bg-chart-4',
		expired: 'bg-muted-foreground/50'
	};
</script>

{#if compact}
	<span class={cn('size-2 shrink-0 rounded-full', dot[state], className)} title={stateLabel[state]}>
		<span class="sr-only">{stateLabel[state]}</span>
	</span>
{:else}
	<Badge variant="outline" class={className}>
		<span class={['size-1.5 rounded-full', dot[state]]}></span>
		{stateLabel[state]}
	</Badge>
{/if}
