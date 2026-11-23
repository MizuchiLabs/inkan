<script lang="ts">
	import {
		useDeleteProduct,
		useLicenses,
		useProducts,
		useSyncProduct,
		type Product
	} from '#lib/api/queries.js';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Card from '#lib/components/ui/card/index.js';
	import * as Empty from '#lib/components/ui/empty/index.js';
	import { Skeleton } from '#lib/components/ui/skeleton/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import ConfirmDialog from '#lib/components/util/ConfirmDialog.svelte';
	import CopyBlock from '#lib/components/util/CopyBlock.svelte';
	import ProductSheet from '#lib/components/util/ProductSheet.svelte';
	import Seal from '#lib/components/util/Seal.svelte';
	import { formatRelativeTime } from '#lib/license.js';
	import { cn } from '#lib/utils.js';
	import {
		CircleAlertIcon,
		InfoIcon,
		PencilIcon,
		PlusIcon,
		RefreshCwIcon,
		TrashIcon
	} from '@lucide/svelte';
	import { toast } from 'svelte-sonner';

	const productsQuery = useProducts();
	const licensesQuery = useLicenses();
	const sync = useSyncProduct();
	const remove = useDeleteProduct();

	const products = $derived(productsQuery.data ?? []);
	const licenseCount = $derived.by(() => {
		const counts = new Map<number, number>();
		for (const l of licensesQuery.data ?? []) {
			counts.set(l.product_id, (counts.get(l.product_id) ?? 0) + 1);
		}
		return counts;
	});

	// undefined is closed, null adds a product, a product edits it.
	let dialog = $state<Product | null>();
	let dialogTab = $state('general');
	let createdId = $state<number>();
	let deleting = $state<Product>();

	function deleteProduct() {
		if (!deleting) return;
		const name = deleting.name;
		remove.mutate(
			{ path: { id: deleting.id } },
			{ onSuccess: () => toast.success(`Deleted ${name}`) }
		);
		deleting = undefined;
	}
</script>

<svelte:head>
	<title>Products, inkan</title>
</svelte:head>

<div class="flex flex-col gap-6">
	<div class="flex flex-wrap items-end justify-between gap-4">
		<div>
			<h1 class="text-2xl font-black tracking-tight">Products</h1>
			<p class="text-sm text-muted-foreground">
				One signing key per product. The web UI never shows the private half.
			</p>
		</div>
		<Button onclick={() => (dialog = null)}>
			<PlusIcon data-icon="inline-start" />
			Add product
		</Button>
	</div>

	{#if productsQuery.isPending}
		<Skeleton class="h-56 w-full" />
	{:else if products.length === 0}
		<div class="rounded-4xl border">
			<Empty.Root>
				<Empty.Header>
					<Empty.Media>
						<Seal state="blank" class="size-14" />
					</Empty.Media>
					<Empty.Title>No products yet</Empty.Title>
					<Empty.Description>
						Add the app you sell. You get a keypair, or bring the private key of a product that
						already ships.
					</Empty.Description>
				</Empty.Header>
				<Empty.Content>
					<Button onclick={() => (dialog = null)}>Add a product</Button>
				</Empty.Content>
			</Empty.Root>
		</div>
	{:else}
		<div class="grid gap-4 lg:grid-cols-2">
			{#each products as product (product.id)}
				{@const count = licenseCount.get(product.id) ?? 0}
				<Card.Root>
					<Card.Header>
						<div class="flex items-center gap-4">
							<div
								class={cn('shrink-0', product.id === createdId ? 'animate-stamp' : '-rotate-6')}
								style:--stamp-delay="0.2s"
							>
								<Seal publicKey={product.public_key} class="size-16" />
							</div>
							<div class="min-w-0">
								<Card.Title><span class="block truncate">{product.name}</span></Card.Title>
								<div class="mt-1.5 flex flex-wrap gap-1.5">
									<Badge variant="secondary"><span class="font-mono">{product.prefix}-</span></Badge
									>
									<Badge variant="secondary">{count} {count === 1 ? 'license' : 'licenses'}</Badge>
									<Badge variant="secondary">plan {product.plan}</Badge>
								</div>
							</div>
						</div>
						<Card.Action>
							<Button
								variant="ghost"
								size="icon-sm"
								onclick={() => (dialog = product)}
								aria-label="Edit {product.name}"
							>
								<PencilIcon />
							</Button>
							<Button
								variant="ghost"
								size="icon-sm"
								onclick={() => (deleting = product)}
								aria-label="Delete {product.name}"
							>
								<TrashIcon />
							</Button>
						</Card.Action>
					</Card.Header>

					<Card.Content>
						<div class="flex flex-col gap-4">
							<CopyBlock
								title={product.old_public_keys.length ? 'Public keys' : 'Public key'}
								text={[product.public_key, ...product.old_public_keys].join(',')}
								what="public key"
							>
								Give it to the verifier in your app. The app then verifies
								{product.prefix}- keys offline.
								{#if product.old_public_keys.length}
									The key was rotated, so this is the new one and the old ones behind it. Drop the
									old ones under Edit once no key from before is in use.
								{/if}
							</CopyBlock>

							{#if product.sync_error}
								<Alert.Root variant="destructive">
									<CircleAlertIcon />
									<Alert.Title>The worker needs a look</Alert.Title>
									<Alert.Description>{product.sync_error}</Alert.Description>
								</Alert.Root>
							{:else if product.sync_notice}
								<Alert.Root>
									<InfoIcon />
									<Alert.Title>The worker runs on an older export</Alert.Title>
									<Alert.Description>{product.sync_notice}</Alert.Description>
								</Alert.Root>
							{/if}
						</div>
					</Card.Content>

					<Card.Footer class="flex-wrap justify-between">
						<p class="text-sm text-muted-foreground">
							{#if !product.worker_connected}
								No worker, so no paid licenses.
							{:else if product.syncing}
								Reading the worker.
							{:else if product.synced_at}
								Read from the worker {formatRelativeTime(product.synced_at)}.
							{:else}
								Has a worker, not read yet.
							{/if}
						</p>
						{#if product.worker_connected}
							{@const syncing =
								product.syncing || (sync.isPending && sync.variables?.path.id === product.id)}
							<Button
								variant="outline"
								size="sm"
								onclick={() => sync.mutate({ path: { id: product.id } })}
								disabled={syncing}
							>
								{#if syncing}
									<Spinner data-icon="inline-start" />
								{:else}
									<RefreshCwIcon data-icon="inline-start" />
								{/if}
								Sync now
							</Button>
						{:else}
							<Button
								variant="outline"
								size="sm"
								onclick={() => {
									dialogTab = 'worker';
									dialog = product;
								}}
							>
								Connect a worker
							</Button>
						{/if}
					</Card.Footer>
				</Card.Root>
			{/each}

			<div class="flex rounded-4xl border border-dashed">
				<Empty.Root>
					<Empty.Header>
						<Empty.Media>
							<Seal state="blank" class="size-16" />
						</Empty.Media>
						<Empty.Title>Another product</Empty.Title>
						<Empty.Description>
							You get a keypair, or bring the private key of a product that already ships.
						</Empty.Description>
					</Empty.Header>
					<Empty.Content>
						<Button variant="outline" onclick={() => (dialog = null)}>Add a product</Button>
					</Empty.Content>
				</Empty.Root>
			</div>
		</div>
	{/if}
</div>

{#if dialog !== undefined}
	<ProductSheet
		product={dialog ?? undefined}
		tab={dialogTab}
		onclose={() => {
			dialog = undefined;
			dialogTab = 'general';
		}}
		oncreated={(product) => (createdId = product.id)}
	/>
{/if}

<ConfirmDialog
	bind:open={() => deleting !== undefined, (isOpen) => !isOpen && (deleting = undefined)}
	title={`Delete ${deleting?.name ?? ''}?`}
	description="This destroys the signing key and removes every license of the product from the ledger. Without the key no new license can be issued for builds that are already out. There is no undo."
	cancel="Keep product"
	action="Delete product and key"
	onconfirm={deleteProduct}
/>
