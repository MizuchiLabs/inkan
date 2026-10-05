<script lang="ts">
	import {
		useCreateProduct,
		useMailPreview,
		useUpdateProduct,
		type Product
	} from '#lib/api/queries.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import * as Sheet from '#lib/components/ui/sheet/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import * as Tabs from '#lib/components/ui/tabs/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { Debounced } from 'runed';
	import { tick } from 'svelte';
	import { toast } from 'svelte-sonner';
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
	const pending = $derived(create.isPending || update.isPending);

	// The sheet is mounted per use, so the form is a snapshot of the product.
	function initial() {
		return {
			name: product?.name ?? '',
			prefix: product?.prefix ?? '',
			plan: product?.plan ?? '',
			graceDays: product?.grace_days ?? 14,
			keyDays: product?.key_days ?? 0,
			privateKey: '',
			providerProduct: product?.provider_product ?? '',
			providerKey: '',
			webhookSecret: '',
			smtpAddr: product?.smtp_addr ?? '',
			smtpUser: product?.smtp_user ?? '',
			smtpPass: '',
			mailFrom: product?.mail_from ?? '',
			mailLogo: product?.mail_logo ?? '',
			mailAccent: product?.mail_accent ?? '',
			mailFooter: product?.mail_footer ?? '',
			mailCompany: product?.mail_company ?? ''
		};
	}
	const form = $state(initial());

	const look = new Debounced(
		() => ({
			name: form.name,
			mail_logo: form.mailLogo,
			mail_accent: form.mailAccent,
			mail_footer: form.mailFooter,
			mail_company: form.mailCompany
		}),
		300
	);
	const preview = useMailPreview(
		() => product?.id,
		() => (tab === 'mail' ? look.current : undefined)
	);
	// A link would load its page inside the preview. The sandbox allows no
	// new windows, so sending them there leaves a click without effect.
	const inert = $derived(preview.data?.html.replace('<head>', '<head><base target="_blank">'));

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
						grace_days: form.graceDays,
						key_days: form.keyDays,
						provider_product: form.providerProduct,
						provider_key: form.providerKey,
						webhook_secret: form.webhookSecret,
						smtp_addr: form.smtpAddr,
						smtp_user: form.smtpUser,
						smtp_pass: form.smtpPass,
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
					key_days: form.keyDays,
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
		A Stripe license stays valid this many days past its paid period, so the renewed key arrives
		before the old one runs out. The plan goes to manual licenses and to Stripe prices that set
		none.
	</Field.Description>

	<Field.Field>
		<Field.Label for="product-key-days">Key days</Field.Label>
		<Input
			id="product-key-days"
			type="number"
			min={0}
			max={3650}
			bind:value={form.keyDays}
			required
		/>
		<Field.Description>
			Cuts every Stripe key short, so the app has to refresh and a refund, a cancellation or a plan
			change takes effect. A key lasts between half and all of these days. 30 suits yearly plans and
			one time purchases in an app that is online. Leave it at 0 for apps that run offline or have
			monthly plans only.
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
							Grace period and plan shape every Stripe license of this product.
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
						<Tabs.Trigger value="stripe">Stripe</Tabs.Trigger>
						<Tabs.Trigger value="worker">Worker</Tabs.Trigger>
						<Tabs.Trigger value="mail">Mail</Tabs.Trigger>
					</Tabs.List>

					<Tabs.Content value="general" class="overflow-y-auto">
						<div class="px-6 pt-2 pb-6">
							<Field.Group>
								{@render general()}
							</Field.Group>
						</div>
					</Tabs.Content>

					<Tabs.Content value="stripe" class="overflow-y-auto">
						<div class="px-6 pt-2 pb-6">
							<Field.Group>
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
										a price the metadata <code class="font-mono">plan</code> to set its plan. Empty disconnects
										Stripe.
									</Field.Description>
								</Field.Field>
								<Field.Field>
									<Field.Label for="product-stripe-key">Stripe API key</Field.Label>
									<Input
										id="product-stripe-key"
										type="password"
										bind:value={form.providerKey}
										placeholder={product.provider_connected
											? 'Stored. Leave empty to keep it'
											: 'rk_live_...'}
										autocomplete="off"
									/>
									<Field.Description>
										A restricted key that can read subscriptions and customers is enough. One time
										purchases also need Checkout sessions, payment intents and charges.
									</Field.Description>
								</Field.Field>
							</Field.Group>
						</div>
					</Tabs.Content>

					<Tabs.Content value="worker" class="overflow-y-auto">
						<div class="px-6 pt-2 pb-6">
							<Field.Group>
								<Field.Description>
									Only for a worker that mails keys. Inkan sends no mail itself, it keeps these so
									<code class="font-mono">inkan worker env</code> prints a complete file.
								</Field.Description>
								<Field.Field>
									<Field.Label for="product-webhook-secret">Webhook signing secret</Field.Label>
									<Input
										id="product-webhook-secret"
										type="password"
										bind:value={form.webhookSecret}
										placeholder={product.webhook_secret_set
											? 'Stored. Leave empty to keep it'
											: 'whsec_...'}
										autocomplete="off"
									/>
									<Field.Description>
										Of the Stripe webhook endpoint that points at
										<code class="font-mono">/webhook</code> of the worker.
									</Field.Description>
								</Field.Field>
								<div class="grid gap-4 sm:grid-cols-2">
									<Field.Field>
										<Field.Label for="product-smtp-addr">Mail server</Field.Label>
										<Input
											id="product-smtp-addr"
											bind:value={form.smtpAddr}
											placeholder="smtp.example.com:587"
											autocomplete="off"
										/>
									</Field.Field>
									<Field.Field>
										<Field.Label for="product-mail-from">Sender</Field.Label>
										<Input
											id="product-mail-from"
											bind:value={form.mailFrom}
											required={!!form.smtpAddr}
											placeholder="licenses@example.com"
											autocomplete="off"
										/>
									</Field.Field>
									<Field.Field>
										<Field.Label for="product-smtp-user">Mail login</Field.Label>
										<Input id="product-smtp-user" bind:value={form.smtpUser} autocomplete="off" />
									</Field.Field>
									<Field.Field>
										<Field.Label for="product-smtp-pass">Mail password</Field.Label>
										<Input
											id="product-smtp-pass"
											type="password"
											bind:value={form.smtpPass}
											placeholder={product.smtp_pass_set ? 'Stored. Leave empty to keep it' : ''}
											autocomplete="new-password"
										/>
									</Field.Field>
								</div>
								<Field.Description>
									The server has to speak STARTTLS, usually on port 587. An empty mail server
									forgets the login and the sender.
								</Field.Description>
							</Field.Group>
						</div>
					</Tabs.Content>

					<Tabs.Content value="mail" class="overflow-y-auto">
						<div class="px-6 pt-2 pb-6">
							<Field.Group>
								<Field.Description>
									How the key mail looks. All of it is optional, the mail calls the product by its
									name.
								</Field.Description>
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
								{#if preview.data}
									<p class="text-sm text-muted-foreground">
										Subject: <span class="text-foreground">{preview.data.subject}</span>
									</p>
									<!-- An empty sandbox runs nothing, the mail is markup only. -->
									<iframe
										title="Preview of the key mail"
										srcdoc={inert}
										sandbox=""
										class="h-176 w-full rounded-lg border"
									></iframe>
									<p class="text-xs text-muted-foreground">
										The key is made up and the links do nothing here. A worker with templates of its
										own sends those instead. <code class="font-mono">inkan worker mail</code> sends a
										sample to a real inbox.
									</p>
								{:else if preview.isError}
									<p class="text-sm text-destructive">The preview could not be loaded.</p>
								{:else}
									<Spinner />
								{/if}
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
