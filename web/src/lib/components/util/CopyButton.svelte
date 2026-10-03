<script lang="ts">
	import { Button, type ButtonProps } from '#lib/components/ui/button/index.js';
	import { CheckIcon, CopyIcon } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';

	type Props = {
		text: string;
		// What gets copied, for the toast and screen readers: "key", "public key".
		what: string;
		label?: string;
		variant?: ButtonProps['variant'];
		size?: ButtonProps['size'];
	};
	let { text, what, label, variant = 'ghost', size }: Props = $props();

	let copied = $state(false);

	async function copy(event: MouseEvent) {
		// Copy buttons sit inside clickable rows.
		event.stopPropagation();
		try {
			await navigator.clipboard.writeText(text);
		} catch {
			toast.error('The browser blocked the clipboard');
			return;
		}
		copied = true;
		toast.success(`Copied the ${what}`);
		setTimeout(() => (copied = false), 1500);
	}
</script>

<Button
	{variant}
	size={size ?? (label ? 'sm' : 'icon-sm')}
	onclick={copy}
	aria-label={label ? undefined : `Copy the ${what}`}
	title={label ? undefined : `Copy the ${what}`}
>
	{#if copied}
		<CheckIcon data-icon={label ? 'inline-start' : undefined} />
	{:else}
		<CopyIcon data-icon={label ? 'inline-start' : undefined} />
	{/if}
	{label}
</Button>
