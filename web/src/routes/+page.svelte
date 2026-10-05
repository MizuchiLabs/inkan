<script lang="ts">
	import { resolve } from '$app/paths';
	import { useLicenses, useProducts, type License } from '#lib/api/queries.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Empty from '#lib/components/ui/empty/index.js';
	import * as InputGroup from '#lib/components/ui/input-group/index.js';
	import * as Select from '#lib/components/ui/select/index.js';
	import * as Tabs from '#lib/components/ui/tabs/index.js';
	import { Skeleton } from '#lib/components/ui/skeleton/index.js';
	import * as Table from '#lib/components/ui/table/index.js';
	import CopyButton from '#lib/components/util/CopyButton.svelte';
	import HistoryChart from '#lib/components/util/HistoryChart.svelte';
	import KeyCheck from '#lib/components/util/KeyCheck.svelte';
	import LicenseDialog from '#lib/components/util/LicenseDialog.svelte';
	import LicenseSheet from '#lib/components/util/LicenseSheet.svelte';
	import Seal from '#lib/components/util/Seal.svelte';
	import StatusBadge from '#lib/components/util/StatusBadge.svelte';
	import {
		byAttention,
		formatDate,
		formatRelative,
		refOf,
		stateLabel,
		stateOf,
		type LicenseState
	} from '#lib/license.js';
	import { PlusIcon, RefreshCwIcon, SearchIcon, StampIcon } from '@lucide/svelte';

	const productsQuery = useProducts();
	const licensesQuery = useLicenses();

	const products = $derived(productsQuery.data ?? []);
	const licenses = $derived((licensesQuery.data ?? []).toSorted(byAttention));
	const productById = $derived(new Map(products.map((p) => [p.id, p])));

	let search = $state('');
	let productFilter = $state('all');
	let stateFilter = $state<'all' | LicenseState>('all');

	let selectedRef = $state<string>();
	// The license issued a moment ago. Its seal lands when the sheet opens.
	let issuedRef = $state<string>();
	// undefined is closed, null issues a new license, a license edits it.
	let dialog = $state<License | null>();

	const selected = $derived(licenses.find((l) => refOf(l) === selectedRef));

	// Everything but the status filter, so its counts match what a click shows.
	const matching = $derived.by(() => {
		const needle = search.trim().toLowerCase();
		return licenses.filter((l) => {
			if (productFilter !== 'all' && String(l.product_id) !== productFilter) return false;
			if (!needle) return true;
			return [l.email, l.customer, l.id, l.plan, l.note, l.key].some((field) =>
				field.toLowerCase().includes(needle)
			);
		});
	});
	const counts = $derived.by(() => {
		const n: Record<LicenseState, number> = { active: 0, ending: 0, expired: 0 };
		for (const l of matching) n[stateOf(l)]++;
		return n;
	});
	const shown = $derived(
		stateFilter === 'all' ? matching : matching.filter((l) => stateOf(l) === stateFilter)
	);

	const states: LicenseState[] = ['active', 'ending', 'expired'];
</script>

<div class="flex flex-col gap-8">
	<KeyCheck {products} onopen={(ref) => (selectedRef = ref)} />

	{#if productsQuery.isPending || licensesQuery.isPending}
		<div class="flex flex-col gap-2">
			<Skeleton class="h-8 w-64" />
			<Skeleton class="h-64 w-full" />
		</div>
	{:else if products.length === 0}
		<div class="rounded-2xl border">
			<Empty.Root>
				<Empty.Header>
					<Empty.Media>
						<Seal state="blank" class="size-14" />
					</Empty.Media>
					<Empty.Title>No product to issue keys for yet</Empty.Title>
					<Empty.Description>
						A product holds the signing key. Add one, build its public key into your app, and every
						key issued here verifies offline.
					</Empty.Description>
				</Empty.Header>

				<Empty.Content><Button href={resolve('products')}>Add a product</Button></Empty.Content>
			</Empty.Root>
		</div>
	{:else}
		<HistoryChart productId={productFilter === 'all' ? undefined : Number(productFilter)} />

		<section class="flex flex-col gap-3" aria-label="Licenses">
			<div class="flex flex-wrap items-center gap-2">
				<Tabs.Root
					bind:value={
						() => stateFilter, (value) => (stateFilter = (value || 'all') as typeof stateFilter)
					}
					aria-label="Filter by status"
				>
					<Tabs.List>
						<Tabs.Trigger value="all">
							<span class="font-mono">All {matching.length}</span>
						</Tabs.Trigger>
						{#each states as s (s)}
							<Tabs.Trigger value={s}>
								<span class="font-mono">{stateLabel[s]} {counts[s]}</span>
							</Tabs.Trigger>
						{/each}
					</Tabs.List>
				</Tabs.Root>

				<div class="flex flex-1 items-center justify-end gap-2">
					{#if products.length > 1}
						<Select.Root type="single" bind:value={productFilter}>
							<Select.Trigger aria-label="Filter by product">
								{productById.get(Number(productFilter))?.name ?? 'All products'}
							</Select.Trigger>
							<Select.Content>
								<Select.Group>
									<Select.Item value="all">All products</Select.Item>
									{#each products as p (p.id)}
										<Select.Item value={String(p.id)}>{p.name}</Select.Item>
									{/each}
								</Select.Group>
							</Select.Content>
						</Select.Root>
					{/if}
					<InputGroup.Root class="max-w-64">
						<InputGroup.Addon>
							<SearchIcon />
						</InputGroup.Addon>
						<InputGroup.Input
							bind:value={search}
							placeholder="Email, customer, id"
							aria-label="Search licenses"
						/>
					</InputGroup.Root>

					<Button onclick={() => (dialog = null)}>
						<PlusIcon data-icon="inline-start" />Issue license
					</Button>
				</div>
			</div>

			{#if shown.length === 0}
				<div class="rounded-2xl border">
					<Empty.Root>
						<Empty.Header>
							<Empty.Media variant="icon">
								<StampIcon />
							</Empty.Media>
							<Empty.Title>
								{licenses.length === 0 ? 'No licenses yet' : 'Nothing matches'}
							</Empty.Title>
							<Empty.Description>
								{#if licenses.length === 0}
									Issue one by hand, or connect a product to Stripe and its paid subscriptions show
									up here.
								{:else}
									No license fits this search and filter.
								{/if}
							</Empty.Description>
						</Empty.Header>
					</Empty.Root>
				</div>
			{:else}
				<div class="rounded-2xl border bg-card">
					<Table.Root>
						<Table.Header>
							<Table.Row>
								<Table.Head>Customer</Table.Head>
								<Table.Head class="max-md:hidden">Product</Table.Head>
								<Table.Head>Valid until</Table.Head>
								<Table.Head class="max-sm:hidden">Status</Table.Head>
								<Table.Head class="w-0"><span class="sr-only">Key</span></Table.Head>
							</Table.Row>
						</Table.Header>
						<Table.Body>
							{#each shown as license (refOf(license))}
								{@const product = productById.get(license.product_id)}
								{@const status = stateOf(license)}

								<Table.Row class="cursor-pointer" onclick={() => (selectedRef = refOf(license))}>
									<Table.Cell>
										<button
											type="button"
											class="flex max-w-64 flex-col text-left outline-none focus-visible:underline"
										>
											{#if license.customer || license.email}
												<span class="truncate font-medium">{license.customer || license.email}</span
												>
											{:else}
												<span class="truncate text-muted-foreground">Unknown customer</span>
											{/if}
											{#if license.customer_gone}
												<span class="truncate text-xs text-muted-foreground">
													{license.customer ? `${license.email}, deleted` : 'Deleted in Stripe'}
												</span>
											{:else if license.customer}
												<span class="truncate text-xs text-muted-foreground">{license.email}</span>
											{/if}
										</button>
									</Table.Cell>
									<Table.Cell class="max-md:hidden">
										<div class="flex items-center gap-2">
											<Seal
												publicKey={product?.public_key}
												state={status === 'expired' ? 'faded' : 'sealed'}
												class="size-6"
											/>
											<span>{product?.name}</span>
											<span class="text-muted-foreground">{license.plan}</span>
										</div>
									</Table.Cell>
									<Table.Cell>
										<div class="flex items-center gap-2.5">
											<StatusBadge state={status} compact class="sm:hidden" />
											{#if license.expires_at}
												<div class="flex flex-col">
													<span class="flex items-center gap-1.5">
														{formatDate(license.expires_at)}
														{#if license.renews}
															<RefreshCwIcon
																class="size-3 text-muted-foreground"
																aria-label="Renews"
															/>
														{/if}
													</span>
													<span class="text-xs text-muted-foreground">
														{formatRelative(license.expires_at)}
													</span>
												</div>
											{:else}
												<span class="text-muted-foreground">Never expires</span>
											{/if}
										</div>
									</Table.Cell>
									<Table.Cell class="max-sm:hidden">
										<StatusBadge state={status} />
									</Table.Cell>
									<Table.Cell>
										<CopyButton text={license.key} what="key" />
									</Table.Cell>
								</Table.Row>
							{/each}
						</Table.Body>
					</Table.Root>
				</div>
			{/if}
		</section>
	{/if}
</div>

<LicenseSheet
	license={selected}
	product={selected && productById.get(selected.product_id)}
	fresh={!!selected && refOf(selected) === issuedRef}
	onclose={() => {
		selectedRef = undefined;
		issuedRef = undefined;
	}}
	onedit={(license) => (dialog = license)}
/>

{#if dialog !== undefined}
	<LicenseDialog
		{products}
		license={dialog ?? undefined}
		onclose={() => (dialog = undefined)}
		onsaved={(license, issued) => {
			selectedRef = refOf(license);
			issuedRef = issued ? selectedRef : undefined;
		}}
	/>
{/if}
