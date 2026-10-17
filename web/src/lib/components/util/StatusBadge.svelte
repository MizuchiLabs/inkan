<script lang="ts">
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { stateDot, stateLabel, type LicenseState } from '#lib/license.js';
	import { cn } from '#lib/utils.js';

	type Props = {
		state: LicenseState;
		// Only the dot, for places too narrow for the label.
		compact?: boolean;
		class?: string;
	};
	let { state, compact = false, class: className }: Props = $props();
</script>

{#if compact}
	<span
		class={cn('size-2 shrink-0 rounded-full', stateDot[state], className)}
		title={stateLabel[state]}
	>
		<span class="sr-only">{stateLabel[state]}</span>
	</span>
{:else}
	<Badge variant="outline" class={className}>
		<span class={['size-1.5 rounded-full', stateDot[state]]}></span>
		{stateLabel[state]}
	</Badge>
{/if}
