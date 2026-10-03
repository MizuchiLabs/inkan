<script lang="ts">
	import { useCreateProduct, useUpdateProduct, type Product } from '#lib/api/queries.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { toast } from 'svelte-sonner';
	import Seal from './Seal.svelte';

	type Props = {
		// Editing when given, adding a new product otherwise.
		product?: Product;
		onclose: () => void;
		oncreated?: (product: Product) => void;
	};
	let { product, onclose, oncreated }: Props = $props();

	const create = useCreateProduct();
	const update = useUpdateProduct();
	const pending = $derived(create.isPending || update.isPending);

	// The dialog is mounted per use, so the form is a snapshot of the product.
	function initial() {
		return {
			name: product?.name ?? '',
			prefix: product?.prefix ?? '',
			plan: product?.plan ?? '',
			graceDays: product?.grace_days ?? 14,
			privateKey: '',
			stripeProduct: product?.stripe_product ?? '',
			stripeKey: ''
		};
	}
	const form = $state(initial());

	function save(event: SubmitEvent) {
		event.preventDefault();
		if (product) {
			update.mutate(
				{
					path: { id: product.id },
					body: {
						name: form.name,
						plan: form.plan,
						grace_days: form.graceDays,
						stripe_product: form.stripeProduct,
						stripe_key: form.stripeKey
					}
				},
				{
					onSuccess: (saved) => {
						onclose();
						if (saved.sync_error) toast.error(`Saved, but Stripe said: ${saved.sync_error}`);
						else toast.success('Saved the product');
					}
				}
			);
			return;
		}
		create.mutate(
			{
				body: {
					name: form.name,
					prefix: form.prefix,
					plan: form.plan,
					grace_days: form.graceDays,
					private_key: form.privateKey
				}
			},
			{
				onSuccess: (created) => {
					onclose();
					oncreated?.(created);
					toast.success('Added the product');
				}
			}
		);
	}
</script>

<Dialog.Root open onOpenChange={(isOpen) => !isOpen && onclose()}>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title>{product ? `Edit ${product.name}` : 'Add a product'}</Dialog.Title>
			<Dialog.Description>
				{#if product}
					Grace period and plan shape every Stripe license of this product.
				{:else}
					Each product has its own signing key. Its public half goes into your build.
				{/if}
			</Dialog.Description>
		</Dialog.Header>

		<form id="product-form" onsubmit={save}>
			<Field.Group>
				<div class="flex items-start gap-4">
					<div class="grid flex-1 gap-4 sm:grid-cols-2">
						<Field.Field>
							<Field.Label for="product-name">Name</Field.Label>
							<Input id="product-name" bind:value={form.name} required placeholder="My App" />
						</Field.Field>
						<Field.Field data-disabled={product ? true : undefined}>
							<Field.Label for="product-prefix">Key prefix</Field.Label>
							<Input
								id="product-prefix"
								bind:value={() => form.prefix, (value) => (form.prefix = value.toUpperCase())}
								required
								disabled={!!product}
								pattern="[A-Z0-9]+"
								maxlength={16}
								placeholder="MYAPP"
								autocomplete="off"
							/>
						</Field.Field>
					</div>
					<!-- A new product has no key yet, so there is no seal to show. -->
					{#if product}
						<Seal publicKey={product.public_key} class="mt-1 size-16" />
					{/if}
				</div>
				{#if !product}
					<Field.Description>
						Keys start with the prefix, and the product checks for it. It cannot change later.
					</Field.Description>
				{/if}

				<div class="grid gap-4 sm:grid-cols-2">
					<Field.Field>
						<Field.Label for="product-plan">Plan</Field.Label>
						<Input id="product-plan" bind:value={form.plan} required placeholder="pro" />
					</Field.Field>
					<Field.Field>
						<Field.Label for="product-grace">Grace days</Field.Label>
						<Input
							id="product-grace"
							type="number"
							min={0}
							max={365}
							bind:value={form.graceDays}
							required
						/>
					</Field.Field>
				</div>
				<Field.Description>
					A Stripe license stays valid this many days past its paid period, so the renewed key
					arrives before the old one runs out. The plan goes to manual licenses and to Stripe prices
					that set none.
				</Field.Description>

				{#if product}
					<Field.Separator />
					<Field.Field>
						<Field.Label for="product-stripe-product">Stripe product id</Field.Label>
						<Input
							id="product-stripe-product"
							bind:value={form.stripeProduct}
							placeholder="prod_..."
							autocomplete="off"
						/>
						<Field.Description>
							Every subscription to this product becomes a license. Give a price the metadata
							<code class="font-mono">plan</code> to set its plan. Empty disconnects Stripe.
						</Field.Description>
					</Field.Field>
					<Field.Field>
						<Field.Label for="product-stripe-key">Stripe API key</Field.Label>
						<Input
							id="product-stripe-key"
							type="password"
							bind:value={form.stripeKey}
							placeholder={product.stripe_connected
								? 'Stored. Leave empty to keep it'
								: 'rk_live_...'}
							autocomplete="off"
						/>
						<Field.Description>
							A restricted key that can read subscriptions and customers is enough.
						</Field.Description>
					</Field.Field>
				{:else}
					<Field.Field>
						<Field.Label for="product-private-key">Existing private key</Field.Label>
						<Textarea
							id="product-private-key"
							bind:value={form.privateKey}
							rows={2}
							spellcheck={false}
							autocomplete="off"
						/>
						<Field.Description>
							Only for a product that already ships with a key. Leave empty and a new keypair is
							made.
						</Field.Description>
					</Field.Field>
				{/if}
			</Field.Group>
		</form>

		<Dialog.Footer>
			<Button variant="ghost" onclick={onclose}>Cancel</Button>
			<Button type="submit" form="product-form" disabled={pending}>
				{#if pending}
					<Spinner data-icon="inline-start" />
				{/if}
				{product ? 'Save product' : 'Add product'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
