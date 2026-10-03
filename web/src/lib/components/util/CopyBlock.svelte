<script lang="ts">
	import type { Snippet } from 'svelte';
	import CopyButton from './CopyButton.svelte';

	type Props = {
		title: string;
		text: string;
		// What gets copied, for the toast and screen readers: "key", "public key".
		what: string;
		// Spells the copy button out, for the one thing a view is there to hand over.
		copyLabel?: string;
		children?: Snippet;
	};
	let { title, text, what, copyLabel, children }: Props = $props();
</script>

<div class="flex flex-col gap-1.5">
	<div class="flex items-center justify-between">
		<h3 class="text-sm font-medium">{title}</h3>
		<CopyButton {text} {what} label={copyLabel} variant={copyLabel ? 'secondary' : 'ghost'} />
	</div>
	<p class="rounded-2xl bg-muted p-3 font-mono text-xs break-all select-all">{text}</p>
	{#if children}
		<p class="text-xs text-muted-foreground">{@render children()}</p>
	{/if}
</div>
