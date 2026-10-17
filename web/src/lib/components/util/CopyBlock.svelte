<script lang="ts">
	import type { Snippet } from 'svelte';
	import CopyButton from './CopyButton.svelte';
	import Seal from './Seal.svelte';

	type Props = {
		title: string;
		text: string;
		// What gets copied, for the toast and screen readers: "key", "public key".
		what: string;
		// Spells the copy button out, for the one thing a view is there to hand over.
		copyLabel?: string;
		// A public key leaves its seal faintly in the corner, like a certificate.
		seal?: string;
		children?: Snippet;
	};
	let { title, text, what, copyLabel, seal, children }: Props = $props();
</script>

<div
	class="relative flex flex-col gap-3 overflow-hidden rounded-2xl border border-dashed border-muted-foreground/35 bg-muted/50 p-4"
>
	{#if seal}
		<Seal
			publicKey={seal}
			class="pointer-events-none absolute -right-6 -bottom-8 size-36 -rotate-12 opacity-8"
		/>
	{/if}
	<div class="relative flex items-center justify-between gap-3">
		<h3 class="text-sm font-medium">{title}</h3>
		<CopyButton {text} {what} label={copyLabel} variant={copyLabel ? 'outline' : 'ghost'} />
	</div>
	<p class="relative font-mono text-xs leading-relaxed break-all select-all">{text}</p>
	{#if children}
		<p class="relative text-xs text-muted-foreground">{@render children()}</p>
	{/if}
</div>
