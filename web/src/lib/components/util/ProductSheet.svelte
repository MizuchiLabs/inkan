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
	import { render } from '#lib/mail.js';
	import { RotateCwIcon } from '@lucide/svelte';
	import { Debounced } from 'runed';
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
			workerUrl: product?.worker_url ?? '',
			providerProduct: product?.provider_product ?? '',
			graceDays: product?.grace_days ?? 14,
			maxKeyDays: product?.max_key_days ?? 0,
			mailFrom: product?.mail_from ?? '',
			mailLogo: product?.mail_logo ?? '',
			mailAccent: product?.mail_accent ?? '',
			mailFooter: product?.mail_footer ?? '',
			mailCompany: product?.mail_company ?? ''
		};
	}
	const form = $state(initial());

	// Made up, it only has to look like a key.
	const sampleKey = btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(165))))
		.match(/.{1,5}/g)!
		.join('-');
	const sampleExpiry = Math.floor(Date.now() / 1000) + 365 * 86400;

	// The preview is a whole page in a frame, it should not reload on every key.
	const look = new Debounced(
		() => ({
			name: form.name,
			logo: form.mailLogo,
			accent: form.mailAccent,
			footer: form.mailFooter,
			company: form.mailCompany
		}),
		300
	);
	const preview = $derived(
		render(
			`${form.prefix}-${sampleKey}`,
			{
				app: form.prefix,
				plan: form.plan,
				email: 'customer@example.com',
				exp: sampleExpiry,
				id: '',
				meta: {}
			},
			look.current
		)
	);
	// A link would load its page inside the preview. The sandbox allows no
	// new windows, so sending them there leaves a click without effect.
	const inert = $derived(preview.html.replace('<head>', '<head><base target="_blank">'));

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
						worker_url: form.workerUrl,
						provider_product: form.providerProduct,
						grace_days: form.graceDays,
						max_key_days: form.maxKeyDays,
						mail_from: form.mailFrom,
						mail_logo: form.mailLogo,
						mail_accent: form.mailAccent,
						mail_footer: form.mailFooter,
						mail_company: form.mailCompany
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
			Goes to manual licenses, and the worker gives it to Stripe prices that set none.
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
							Its worker hands out the paid licenses. What it runs with is set here.
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
						<Tabs.Trigger value="mail">Mail</Tabs.Trigger>
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
									title="Its settings"
									text={`inkan worker env ${product.prefix} > worker.env`}
									what="command"
								>
									Prints what is set here, with the signing key. Add the Stripe key and deploy the
									worker with it. A change here only reaches the worker that way.
								</CopyBlock>

								<Field.Field>
									<Field.Label for="product-worker-url">Worker url</Field.Label>
									<Input
										id="product-worker-url"
										type="url"
										bind:value={form.workerUrl}
										pattern="https?://.+"
										placeholder="https://keys.example.com"
										autocomplete="off"
									/>
									<Field.Description>
										Where the worker runs. Its licenses show up here once it is set, empty
										disconnects it.
									</Field.Description>
								</Field.Field>

								<Field.Field>
									<Field.Label for="product-stripe-product">Stripe product id</Field.Label>
									<Input
										id="product-stripe-product"
										bind:value={form.providerProduct}
										placeholder="prod_..."
										autocomplete="off"
									/>
									<Field.Description>
										Every subscription and one time purchase of this product becomes a license. Give
										a price the metadata <code class="font-mono">plan</code> to set its plan.
									</Field.Description>
								</Field.Field>

								<div class="grid gap-4 sm:grid-cols-2">
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
									<Field.Field>
										<Field.Label for="product-max-key-days">Max key days</Field.Label>
										<Input
											id="product-max-key-days"
											type="number"
											min={0}
											max={3650}
											bind:value={form.maxKeyDays}
											required
										/>
									</Field.Field>
								</div>
								<Field.Description>
									A paid license stays valid the grace days past its paid period, so the renewed key
									arrives before the old one runs out. Max key days cut every paid key short, so the
									app has to refresh and a refund, a cancellation or a plan change takes effect. A
									key lasts between half and all of them. 30 suits yearly plans and one time
									purchases in an app that is online. Leave it at 0 for apps that run offline or
									have monthly plans only.
								</Field.Description>
							</Field.Group>
						</div>
					</Tabs.Content>

					<Tabs.Content value="mail" class="overflow-y-auto">
						<div class="px-6 pt-2 pb-6">
							<Field.Group>
								<Field.Description>
									The mail the worker sends a key in. All of it is optional but the sender, the mail
									calls the product by its name.
								</Field.Description>
								<Field.Field>
									<Field.Label for="product-mail-from">Sender</Field.Label>
									<Input
										id="product-mail-from"
										bind:value={form.mailFrom}
										maxlength={256}
										placeholder="My App <licenses@example.com>"
										autocomplete="off"
									/>
								</Field.Field>
								<div class="grid gap-4 sm:grid-cols-2">
									<Field.Field>
										<Field.Label for="product-mail-logo">Logo</Field.Label>
										<Input
											id="product-mail-logo"
											type="url"
											bind:value={form.mailLogo}
											pattern="https://.+"
											placeholder="https://example.com/logo.png"
											autocomplete="off"
										/>
									</Field.Field>
									<Field.Field>
										<Field.Label for="product-mail-accent">Accent color</Field.Label>
										<Input
											id="product-mail-accent"
											bind:value={form.mailAccent}
											pattern={'#[0-9a-fA-F]{6}'}
											placeholder="#2f3e8f"
											autocomplete="off"
										/>
									</Field.Field>
								</div>
								<Field.Description>
									The logo sits on the dark header, 32 pixels high, so it has to read on a dark
									background.
								</Field.Description>
								<Field.Field>
									<Field.Label for="product-mail-footer">Footer</Field.Label>
									<Input
										id="product-mail-footer"
										bind:value={form.mailFooter}
										maxlength={256}
										placeholder="Questions? Write to support@example.com"
									/>
								</Field.Field>
								<Field.Field>
									<Field.Label for="product-mail-company">Company details</Field.Label>
									<Input
										id="product-mail-company"
										bind:value={form.mailCompany}
										maxlength={512}
										placeholder="Example GmbH, Street 1, 12345 Berlin, HRB 12345"
									/>
									<Field.Description>
										The last line of the mail, for what your business has to state in its mail.
										Links and email addresses in it and in the footer can be clicked.
									</Field.Description>
								</Field.Field>

								<Field.Separator />
								<p class="text-sm text-muted-foreground">
									Subject: <span class="text-foreground">{preview.subject}</span>
								</p>
								<!-- An empty sandbox runs nothing, the mail is markup only. -->
								<iframe
									title="Preview of the key mail"
									srcdoc={inert}
									sandbox=""
									class="h-176 w-full rounded-lg border"
								></iframe>
								<p class="text-xs text-muted-foreground">
									The key is made up and the links do nothing here. A worker with a mail of its own
									sends that instead.
								</p>
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
					then export worker env and deploy it. A build from before cannot take a key of the new
					signing key. There is no undo.
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
