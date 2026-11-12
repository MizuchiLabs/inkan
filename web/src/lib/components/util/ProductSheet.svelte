<script lang="ts">
	import {
		useCreateProduct,
		useDropOldProductKeys,
		useRotateProductKey,
		useUpdateProduct,
		type Product
	} from '#lib/api/queries.js';
	import * as AlertDialog from '#lib/components/ui/alert-dialog/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import * as Sheet from '#lib/components/ui/sheet/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import * as Tabs from '#lib/components/ui/tabs/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { formatRelativeTime } from '#lib/license.js';
	import { RotateCwIcon } from '@lucide/svelte';
	import { tick } from 'svelte';
	import { toast } from 'svelte-sonner';
	import CopyBlock from './CopyBlock.svelte';
	import Seal from './Seal.svelte';

	type Props = {
		// Editing when given, adding a new product otherwise.
		product?: Product;
		// The tab an edit opens on.
		tab?: string;
		onclose: () => void;
		oncreated?: (product: Product) => void;
	};
	let { product, tab = $bindable('general'), onclose, oncreated }: Props = $props();

	const create = useCreateProduct();
	const update = useUpdateProduct();
	const rotate = useRotateProductKey();
	const dropOld = useDropOldProductKeys();
	const pending = $derived(
		create.isPending || update.isPending || rotate.isPending || dropOld.isPending
	);
	// The key action that waits for a yes.
	let confirming = $state<'rotate' | 'drop'>();

	// The sheet is mounted per use, so the form is a snapshot of the product.
	function initial() {
		return {
			name: product?.name ?? '',
			prefix: product?.prefix ?? '',
			plan: product?.plan ?? '',
			privateKey: '',
			workerUrl: product?.worker_url ?? ''
		};
	}
	const form = $state(initial());

	// What the worker said at its last sync. Its settings live in its env alone.
	const runs = $derived(product?.worker_report);
	// prod_a=team,prod_b as one line per Stripe product.
	const stripeProducts = $derived(
		(runs?.provider_product ?? '')
			.split(',')
			.map((entry) => entry.trim())
			.filter(Boolean)
			.map((entry) => {
				const [id = '', plan = ''] = entry.split('=');
				return { id: id.trim(), plan: plan.trim() || runs?.plan || '' };
			})
	);

	// The sheet shows a snapshot, so it closes on a product whose keys changed.
	function changeKeys() {
		if (!product) return;
		const path = { id: product.id };
		if (confirming === 'rotate') {
			rotate.mutate(
				{ path },
				{
					onSuccess: () => {
						onclose();
						toast.success('Rotated the key. Ship the new public keys, then deploy the worker.');
					}
				}
			);
		} else {
			dropOld.mutate(
				{ path },
				{
					onSuccess: () => {
						onclose();
						toast.success('Dropped the old keys');
					}
				}
			);
		}
		confirming = undefined;
	}

	// A hidden tab cannot show what is wrong with its fields, so the form
	// checks itself and opens the tab of the first one first.
	async function save(event: SubmitEvent & { currentTarget: HTMLFormElement }) {
		event.preventDefault();
		const formEl = event.currentTarget;
		const invalid = formEl.querySelector(':invalid');
		if (invalid) {
			tab = invalid.closest<HTMLElement>('[data-slot="tabs-content"]')?.dataset.value ?? tab;
			await tick();
			formEl.reportValidity();
			return;
		}
		if (product) {
			update.mutate(
				{
					path: { id: product.id },
					body: {
						name: form.name,
						plan: form.plan,
						worker_url: form.workerUrl
					}
				},
				{
					onSuccess: (saved) => {
						onclose();
						if (saved.sync_error) toast.warning(`Saved. ${saved.sync_error}`);
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

{#snippet setting(label: string, value: string)}
	<dt class="text-muted-foreground">{label}</dt>
	<dd class="min-w-0 wrap-break-word">{value}</dd>
{/snippet}

{#snippet general()}
	<div class="grid gap-4 sm:grid-cols-2">
		<Field.Field>
			<Field.Label for="product-name">Name</Field.Label>
			<Input
				id="product-name"
				bind:value={form.name}
				onblur={() => (form.prefix = form.name.toUpperCase())}
				required
				placeholder="My App"
			/>
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
	{#if !product}
		<Field.Description>
			Keys start with the prefix, and the product checks for it. It cannot change later.
		</Field.Description>
	{/if}

	<Field.Field>
		<Field.Label for="product-plan">Plan</Field.Label>
		<Input id="product-plan" bind:value={form.plan} required placeholder="pro" />
		<Field.Description>
			Goes to manual licenses, and to the keys of Stripe products that have no plan of their own.
		</Field.Description>
	</Field.Field>
{/snippet}

<Sheet.Root open onOpenChange={(isOpen) => !isOpen && onclose()}>
	<Sheet.Content class="data-[side=right]:sm:max-w-xl data-[side=right]:lg:max-w-2xl">
		<Sheet.Header>
			<div class="flex items-center gap-3">
				<!-- A new product has no key yet, so there is no seal to show. -->
				{#if product}
					<Seal publicKey={product.public_key} class="size-12 shrink-0 -rotate-6" />
				{/if}
				<div class="min-w-0">
					<Sheet.Title>{product ? `Edit ${product.name}` : 'Add a product'}</Sheet.Title>
					<Sheet.Description>
						{#if product}
							Its worker hands out the paid licenses. The worker has its own settings.
						{:else}
							Each product has its own signing key. Its public half goes into your build.
						{/if}
					</Sheet.Description>
				</div>
			</div>
		</Sheet.Header>

		<form id="product-form" class="flex min-h-0 flex-1 flex-col" novalidate onsubmit={save}>
			{#if product}
				<Tabs.Root bind:value={tab} class="min-h-0 flex-1">
					<Tabs.List class="mx-6 w-auto">
						<Tabs.Trigger value="general">General</Tabs.Trigger>
						<Tabs.Trigger value="worker">Worker</Tabs.Trigger>
					</Tabs.List>

					<Tabs.Content value="general" class="overflow-y-auto">
						<div class="px-6 pt-2 pb-6">
							<Field.Group>
								{@render general()}

								<Field.Separator />
								<Field.Field>
									<Field.Title>Signing key</Field.Title>
									<Field.Description>
										Rotate it when the private key may have leaked. Keys that are out keep working
										in an app that carries the old public key next to the new one.
										{#if product.old_public_keys.length}
											This product carries {product.old_public_keys.length} old public
											{product.old_public_keys.length === 1 ? 'key' : 'keys'}.
										{/if}
									</Field.Description>
									<div class="flex flex-wrap gap-2">
										<Button
											variant="outline"
											size="sm"
											disabled={pending}
											onclick={() => (confirming = 'rotate')}
										>
											<RotateCwIcon data-icon="inline-start" />
											Rotate key
										</Button>
										{#if product.old_public_keys.length}
											<Button
												variant="outline"
												size="sm"
												disabled={pending}
												onclick={() => (confirming = 'drop')}
											>
												Drop old keys
											</Button>
										{/if}
									</div>
								</Field.Field>
							</Field.Group>
						</div>
					</Tabs.Content>

					<Tabs.Content value="worker" class="overflow-y-auto">
						<div class="px-6 pt-2 pb-6">
							<Field.Group>
								<Field.Description>
									The worker is the public side of the product. It reads Stripe, mails the keys and
									refreshes them. Inkan only shows what it hands out.
								</Field.Description>
								<CopyBlock
									title="Set it up"
									text={`inkan worker export ${product.prefix}`}
									what="command"
								>
									Writes the folder inkan-worker-{product.prefix.toLowerCase()} with the worker and the
									keys of the product. Put your Stripe key into its worker.env, deploy it and enter its
									url below. Run it again after a key rotation or a new name or plan.
								</CopyBlock>

								<Field.Field>
									<Field.Label for="product-worker-url">Worker url</Field.Label>
									<Input
										id="product-worker-url"
										type="url"
										bind:value={form.workerUrl}
										pattern="https?://.+"
										maxlength={512}
										placeholder="https://keys.example.com"
										autocomplete="off"
									/>
									<Field.Description>
										Where the worker runs. Its licenses show up here once it is set, empty
										disconnects it.
									</Field.Description>
								</Field.Field>

								<Field.Separator />
								<Field.Field>
									<Field.Title>What it runs with</Field.Title>
									{#if runs}
										<dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
											<dt class="text-muted-foreground">Stripe products</dt>
											<dd class="flex min-w-0 flex-col gap-1">
												{#each stripeProducts as stripe (stripe.id)}
													<span class="wrap-break-word">
														<span class="font-mono text-xs">{stripe.id}</span>
														gives the plan {stripe.plan}
													</span>
												{/each}
											</dd>
											{@render setting('Grace days', String(runs.grace_days))}
											{@render setting(
												'Max key days',
												runs.max_key_days ? String(runs.max_key_days) : 'No limit'
											)}
											{#if runs.mail}
												{@render setting('Mail sender', runs.mail.from)}
												{#if runs.mail.reply_to}
													{@render setting('Reply to', runs.mail.reply_to)}
												{/if}
												{#if runs.mail.custom}
													{@render setting('Mail', 'A mail of its own, built into the worker')}
												{:else}
													{#if runs.mail.logo}
														{@render setting('Logo', runs.mail.logo)}
													{/if}
													{#if runs.mail.accent}
														{@render setting('Accent color', runs.mail.accent)}
													{/if}
												{/if}
												{#if runs.mail.footer}
													{@render setting('Footer', runs.mail.footer)}
												{/if}
												{#if runs.mail.company}
													{@render setting('Company details', runs.mail.company)}
												{/if}
											{:else}
												{@render setting(
													'Key mails',
													'None. It has no webhook secret, sender or mail service'
												)}
											{/if}
										</dl>
										<Field.Description>
											{#if product.synced_at}
												As the worker reported it {formatRelativeTime(product.synced_at)}.
											{/if}
											These are set in the worker.env of the worker. Change them there and deploy it again.
										</Field.Description>
									{:else}
										<Field.Description>
											Nothing yet. Its Stripe products, grace days, max key days and mail settings
											show up here once the worker was read. They are set in its worker.env.
										</Field.Description>
									{/if}
								</Field.Field>
							</Field.Group>
						</div>
					</Tabs.Content>
				</Tabs.Root>
			{:else}
				<div class="flex-1 overflow-y-auto px-6 pb-6">
					<Field.Group>
						{@render general()}
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
					</Field.Group>
				</div>
			{/if}
		</form>

		<Sheet.Footer>
			<div class="flex justify-end gap-2">
				<Button variant="ghost" onclick={onclose}>Cancel</Button>
				<Button type="submit" form="product-form" disabled={pending}>
					{#if pending}
						<Spinner data-icon="inline-start" />
					{/if}
					{product ? 'Save product' : 'Add product'}
				</Button>
			</div>
		</Sheet.Footer>
	</Sheet.Content>
</Sheet.Root>

<AlertDialog.Root
	open={confirming !== undefined}
	onOpenChange={(isOpen) => !isOpen && (confirming = undefined)}
>
	<AlertDialog.Content interactOutsideBehavior="close">
		<AlertDialog.Header>
			{#if confirming === 'drop'}
				<AlertDialog.Title>Drop the old keys of {product?.name}?</AlertDialog.Title>
				<AlertDialog.Description>
					A key signed before the rotation then counts as forged, here and in every build that ships
					without the old public keys. The worker stops refreshing such keys once its env is
					exported and deployed. There is no undo.
				</AlertDialog.Description>
			{:else}
				<AlertDialog.Title>Rotate the key of {product?.name}?</AlertDialog.Title>
				<AlertDialog.Description>
					This makes a new signing key and destroys the old private key. Manual licenses get new
					keys right away. Ship a build with the public keys the Products page shows after this,
					then run inkan worker export and deploy it. A build from before cannot take a key of the
					new signing key. There is no undo.
				</AlertDialog.Description>
			{/if}
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel>Keep as it is</AlertDialog.Cancel>
			<AlertDialog.Action variant="destructive" onclick={changeKeys}>
				{confirming === 'drop' ? 'Drop old keys' : 'Rotate key'}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
