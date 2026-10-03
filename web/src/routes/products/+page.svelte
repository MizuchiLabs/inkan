<script lang="ts">
	import {
		useDeleteProduct,
		useLicenses,
		useProducts,
		useSyncProduct,
		type Product
	} from '#lib/api/queries.js';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import * as AlertDialog from '#lib/components/ui/alert-dialog/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Card from '#lib/components/ui/card/index.js';
	import * as Empty from '#lib/components/ui/empty/index.js';
	import { Skeleton } from '#lib/components/ui/skeleton/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import CopyBlock from '#lib/components/util/CopyBlock.svelte';
	import ProductDialog from '#lib/components/util/ProductDialog.svelte';
	import Seal from '#lib/components/util/Seal.svelte';
	import { formatRelativeTime } from '#lib/license.js';
	import { cn } from '#lib/utils.js';
	import { CircleAlertIcon, PencilIcon, PlusIcon, RefreshCwIcon, TrashIcon } from '@lucide/svelte';
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
	// The product added a moment ago. Its seal lands on the new card.
	let createdId = $state<number>();
	let deleting = $state<Product>();

	const buildFlag = (p: Product) =>
		`-X github.com/mizuchilabs/inkan/license.pubkey=${p.public_key}`;

	function syncNow(p: Product) {
		sync.mutate(
			{ path: { id: p.id } },
			{ onSuccess: (res) => toast.success(`Synced ${res.licenses} licenses from Stripe`) }
		);
	}

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
	<div class="flex items-end justify-between gap-4">
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
		<div class="rounded-2xl border">
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
						<div class="flex items-center gap-3">
							<!-- The stamp waits for the dialog to fade. -->
							<div
								class={cn('shrink-0', product.id === createdId ? 'animate-stamp' : '-rotate-6')}
								style:--stamp-delay="0.2s"
							>
								<Seal publicKey={product.public_key} class="size-12" />
							</div>
							<div class="min-w-0">
								<Card.Title><span class="block truncate">{product.name}</span></Card.Title>
								<Card.Description>
									{count}
									{count === 1 ? 'license' : 'licenses'}, plan {product.plan}, {product.grace_days}
									grace days
								</Card.Description>
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
							<CopyBlock title="Public key" text={product.public_key} what="public key" />
							<CopyBlock title="Build flag" text={buildFlag(product)} what="build flag">
								Add it to the ldflags of the release build. The app then verifies
								{product.prefix}- keys offline.
							</CopyBlock>

							{#if product.sync_error}
								<Alert.Root variant="destructive">
									<CircleAlertIcon />
									<Alert.Title>Stripe sync failed</Alert.Title>
									<Alert.Description>{product.sync_error}</Alert.Description>
								</Alert.Root>
							{/if}
						</div>
					</Card.Content>

					<Card.Footer class="justify-between">
						<p class="text-sm text-muted-foreground">
							{#if !product.stripe_connected}
								Not connected to Stripe.
							{:else if product.synced_at}
								Synced with Stripe {formatRelativeTime(product.synced_at)}.
							{:else}
								Connected to Stripe, not synced yet.
							{/if}
						</p>
						{#if product.stripe_connected}
							<Button
								variant="outline"
								size="sm"
								onclick={() => syncNow(product)}
								disabled={sync.isPending}
							>
								{#if sync.isPending && sync.variables?.path.id === product.id}
									<Spinner data-icon="inline-start" />
								{:else}
									<RefreshCwIcon data-icon="inline-start" />
								{/if}
								Sync now
							</Button>
						{:else}
							<Button variant="outline" size="sm" onclick={() => (dialog = product)}>
								Connect Stripe
							</Button>
						{/if}
					</Card.Footer>
				</Card.Root>
			{/each}
		</div>
	{/if}
</div>

{#if dialog !== undefined}
	<ProductDialog
		product={dialog ?? undefined}
		onclose={() => (dialog = undefined)}
		oncreated={(product) => (createdId = product.id)}
	/>
{/if}

<AlertDialog.Root
	open={deleting !== undefined}
	onOpenChange={(isOpen) => !isOpen && (deleting = undefined)}
>
	<AlertDialog.Content>
		<AlertDialog.Header>
			<AlertDialog.Title>Delete {deleting?.name}?</AlertDialog.Title>
			<AlertDialog.Description>
				This destroys the signing key and removes every license of the product from the ledger.
				Without the key no new license can be issued for builds that are already out. There is no
				undo.
			</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel>Keep product</AlertDialog.Cancel>
			<AlertDialog.Action variant="destructive" onclick={deleteProduct}>
				Delete product and key
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
