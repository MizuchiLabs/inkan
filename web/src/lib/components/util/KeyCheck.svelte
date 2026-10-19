<script lang="ts">
	import { useCheckKey, type Product } from '#lib/api/queries.js';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as InputGroup from '#lib/components/ui/input-group/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import { formatDate, formatRelative, refOf } from '#lib/license.js';
	import { cn } from '#lib/utils.js';
	import { ScanSearchIcon } from '@lucide/svelte';
	import Seal from './Seal.svelte';

	type Props = {
		products: Product[];
		onopen: (ref: string) => void;
	};
	let { products, onopen }: Props = $props();

	const check = useCheckKey();

	let key = $state('');
	// The verdict belongs to the key it was given for, editing the input hides it.
	let checked = $state('');

	const result = $derived(checked !== '' && checked === key.trim() ? check.data : undefined);
	const product = $derived(products.find((p) => p.id === result?.product_id));
	const meta = $derived(Object.entries(result?.meta ?? {}));

	function run(value = key) {
		const trimmed = value.trim();
		if (!trimmed) return;
		key = trimmed;
		check.mutate({ body: { key: trimmed } }, { onSuccess: () => (checked = trimmed) });
	}

	const keyShape = /^[A-Z0-9]+-\S+$/;
	const fields = 'input, textarea, [contenteditable], [role="dialog"], [role="alertdialog"]';

	// A key pasted outside of any field gets checked too.
	function pasteAnywhere(event: ClipboardEvent) {
		if (event.target instanceof Element && event.target.closest(fields)) return;
		const pasted = event.clipboardData?.getData('text').trim();
		if (!pasted || !keyShape.test(pasted)) return;
		run(pasted);
		window.scrollTo({ top: 0 });
	}
</script>

<svelte:window onpaste={pasteAnywhere} />

<section
	class="flex items-start gap-4 rounded-4xl border bg-card p-4 shadow-sm sm:gap-5 sm:p-5"
	aria-label="Check a license key"
>
	<!-- The well the seal lands in. The stamp is on a wrapper, so a faded seal keeps its own opacity. -->
	<div class="grid size-14 shrink-0 place-items-center rounded-full bg-muted/70 sm:size-21">
		{#key check.submittedAt}
			<div class={cn(result && result.state !== 'invalid' && 'animate-stamp')}>
				<Seal
					publicKey={product?.public_key}
					state={result?.state === 'valid'
						? 'sealed'
						: result?.state === 'expired'
							? 'faded'
							: 'blank'}
					class="size-11 sm:size-16"
				/>
			</div>
		{/key}
	</div>

	<div class="flex min-w-0 flex-1 flex-col gap-3.5">
		<form
			onsubmit={(event) => {
				event.preventDefault();
				run();
			}}
		>
			<InputGroup.Root>
				<InputGroup.Addon>
					<ScanSearchIcon />
				</InputGroup.Addon>
				<InputGroup.Input
					bind:value={key}
					placeholder="Paste a license key anywhere to check it"
					aria-label="License key"
					autocomplete="off"
					spellcheck={false}
					onpaste={(event) => {
						const pasted = event.clipboardData?.getData('text');
						if (!pasted) return;
						event.preventDefault();
						run(pasted);
					}}
				/>
				<InputGroup.Addon align="inline-end">
					<InputGroup.Button
						type="submit"
						variant="default"
						disabled={check.isPending || key.trim() === ''}
					>
						{#if check.isPending}
							<Spinner data-icon="inline-start" />
						{/if}
						Check
					</InputGroup.Button>
				</InputGroup.Addon>
			</InputGroup.Root>
		</form>

		<div class="px-1" aria-live="polite">
			{#if !result}
				<p class="text-sm text-muted-foreground">
					If it verifies, the seal of its product lands on the left.
				</p>
			{:else if result.state === 'invalid'}
				<p class="font-bold">Not a valid key</p>
				<p class="text-sm text-muted-foreground">{result.reason}</p>
			{:else}
				<div class="flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
					<div class="flex min-w-0 flex-[1_1_22rem] flex-col gap-2">
						<div>
							<p class="font-bold">
								{result.state === 'valid' ? 'Valid key' : 'Expired key'}
							</p>
							<p class="text-sm text-muted-foreground">
								{product?.name}
								{result.plan} license for {result.email || 'no email'}.
								{#if !result.expires_at}
									It never expires.
								{:else if result.state === 'valid'}
									Valid until {formatDate(result.expires_at)}, {formatRelative(result.expires_at)}.
								{:else}
									Ran out on {formatDate(result.expires_at)}, {formatRelative(result.expires_at)}.
								{/if}
							</p>
						</div>

						{#if meta.length > 0}
							<div class="flex flex-wrap gap-1.5">
								{#each meta as [name, value] (name)}
									<Badge variant="outline"><span class="font-mono">{name}={value}</span></Badge>
								{/each}
							</div>
						{/if}

						{#if result.fingerprint}
							<p class="text-sm text-muted-foreground">
								Bound to the machine <span class="font-mono">{result.fingerprint}</span>.
							</p>
						{/if}
					</div>

					{#if result.known && result.product_id !== null}
						{@const ref = refOf({ product_id: result.product_id, id: result.license_id })}
						<Button variant="secondary" size="sm" onclick={() => onopen(ref)}>Open license</Button>
					{:else if result.license_id}
						<p class="text-sm text-muted-foreground">
							<span class="font-mono">{result.license_id}</span> is not in the ledger. It was deleted,
							or the worker was not read yet.
						</p>
					{:else}
						<p class="text-sm text-muted-foreground">
							Issued before keys carried an id, so it cannot be matched to a license.
						</p>
					{/if}
				</div>
			{/if}
		</div>
	</div>
</section>
