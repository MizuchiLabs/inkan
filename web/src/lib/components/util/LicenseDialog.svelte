<script lang="ts">
	import {
		useCreateLicense,
		useUpdateLicense,
		type License,
		type LicenseFields,
		type Product
	} from '#lib/api/queries.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import * as Select from '#lib/components/ui/select/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import * as ToggleGroup from '#lib/components/ui/toggle-group/index.js';
	import { formatMeta, localDay, parseMeta } from '#lib/license.js';
	import { toast } from 'svelte-sonner';

	type Props = {
		products: Product[];
		// Editing when given, issuing a new license otherwise.
		license?: License;
		onclose: () => void;
		// Tells a license that was just issued from one that was edited.
		onsaved?: (license: License, issued: boolean) => void;
	};
	let { products, license, onclose, onsaved }: Props = $props();

	const terms = [
		{ value: '14', label: '14 days' },
		{ value: '30', label: '30 days' },
		{ value: '365', label: '1 year' },
		{ value: 'never', label: 'Never' },
		{ value: 'date', label: 'Pick a date' }
	];

	const create = useCreateLicense();
	const update = useUpdateLicense();
	const pending = $derived(create.isPending || update.isPending);

	// The dialog is mounted per use, so the form is a snapshot of the license.
	function initial() {
		return {
			productId: String(license?.product_id ?? products[0]?.id ?? ''),
			email: license?.email ?? '',
			customer: license?.customer ?? '',
			plan: license?.plan ?? '',
			term: license ? (license.expires_at ? 'date' : 'never') : '365',
			date: license?.expires_at ? localDay(license.expires_at) : '',
			meta: formatMeta(license?.meta ?? {}),
			fingerprint: license?.fingerprint ?? '',
			note: license?.note ?? ''
		};
	}
	const form = $state(initial());

	const product = $derived(products.find((p) => String(p.id) === form.productId) ?? products[0]);

	function expiresAt(): string | undefined {
		if (form.term === 'never') return undefined;
		if (form.term === 'date') {
			// An expiry nobody touched goes back as it came, a new time would be a new key.
			if (license?.expires_at && form.date === localDay(license.expires_at)) {
				return license.expires_at;
			}
			// A picked day counts in full, the key runs out when that day ends.
			return new Date(`${form.date}T23:59:59`).toISOString();
		}
		return new Date(Date.now() + Number(form.term) * 86_400_000).toISOString();
	}

	function save(event: SubmitEvent) {
		event.preventDefault();
		const body: LicenseFields = {
			email: form.email,
			customer: form.customer,
			plan: form.plan,
			expires_at: expiresAt(),
			meta: parseMeta(form.meta),
			fingerprint: form.fingerprint,
			note: form.note
		};
		const done = (saved: License) => {
			onclose();
			toast.success(license ? 'Saved the license' : 'Issued the license');
			onsaved?.(saved, !license);
		};
		if (license) {
			update.mutate(
				{ path: { product_id: license.product_id, id: license.id }, body },
				{ onSuccess: done }
			);
		} else if (product) {
			create.mutate({ body: { ...body, product_id: product.id } }, { onSuccess: done });
		}
	}
</script>

<Dialog.Root open onOpenChange={(isOpen) => !isOpen && onclose()}>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title>{license ? 'Edit license' : 'Issue a license'}</Dialog.Title>
			<Dialog.Description>
				{#if license}
					The key is signed from these fields. Change one and the customer needs the new key. The
					key they have keeps working until it expires, an earlier date here does not take it back.
				{:else}
					For trials, comps and anything else that does not come through Stripe.
				{/if}
			</Dialog.Description>
		</Dialog.Header>

		<form id="license-form" onsubmit={save}>
			<Field.Group>
				{#if !license}
					<Field.Field>
						<Field.Label for="license-product">Product</Field.Label>
						<Select.Root type="single" bind:value={form.productId}>
							<Select.Trigger id="license-product" class="w-full">
								{product?.name ?? 'Pick a product'}
							</Select.Trigger>
							<Select.Content>
								<Select.Group>
									{#each products as p (p.id)}
										<Select.Item value={String(p.id)}>{p.name}</Select.Item>
									{/each}
								</Select.Group>
							</Select.Content>
						</Select.Root>
					</Field.Field>
				{/if}

				<Field.Field>
					<Field.Label for="license-email">Email</Field.Label>
					<Input
						id="license-email"
						type="email"
						bind:value={form.email}
						required
						autocomplete="off"
					/>
				</Field.Field>

				<div class="grid gap-4 sm:grid-cols-2">
					<Field.Field>
						<Field.Label for="license-customer">Customer</Field.Label>
						<Input id="license-customer" bind:value={form.customer} placeholder="Acme Inc" />
					</Field.Field>
					<Field.Field>
						<Field.Label for="license-plan">Plan</Field.Label>
						<Input id="license-plan" bind:value={form.plan} placeholder={product?.plan} />
					</Field.Field>
				</div>

				<Field.Field>
					<Field.Label>Expires</Field.Label>
					<ToggleGroup.Root
						type="single"
						variant="outline"
						size="sm"
						spacing={1}
						class="flex-wrap"
						bind:value={() => form.term, (value) => value && (form.term = value)}
						aria-label="Expires"
					>
						{#each terms as t (t.value)}
							<ToggleGroup.Item value={t.value}>{t.label}</ToggleGroup.Item>
						{/each}
					</ToggleGroup.Root>
					{#if form.term === 'date'}
						<Input type="date" bind:value={form.date} required aria-label="Expiry date" />
					{/if}
				</Field.Field>

				<Field.Field>
					<Field.Label for="license-meta">Extra claims</Field.Label>
					<Textarea
						id="license-meta"
						bind:value={form.meta}
						rows={2}
						placeholder="seats=25"
						spellcheck={false}
					/>
					<Field.Description>
						One name=value per line. They are signed into the key and make it longer.
					</Field.Description>
				</Field.Field>

				<Field.Field>
					<Field.Label for="license-fingerprint">Machine</Field.Label>
					<Input
						id="license-fingerprint"
						bind:value={form.fingerprint}
						autocomplete="off"
						spellcheck={false}
					/>
					<Field.Description>
						The fingerprint the app shows, to bind the key to that one machine. Empty works
						everywhere.
					</Field.Description>
				</Field.Field>

				<Field.Field>
					<Field.Label for="license-note">Note</Field.Label>
					<Textarea id="license-note" bind:value={form.note} rows={2} />
					<Field.Description>Only shown here, never part of the key.</Field.Description>
				</Field.Field>
			</Field.Group>
		</form>

		<Dialog.Footer>
			<Button variant="ghost" onclick={onclose}>Cancel</Button>
			<Button type="submit" form="license-form" disabled={pending || (!license && !product)}>
				{#if pending}
					<Spinner data-icon="inline-start" />
				{/if}
				{license ? 'Save license' : 'Issue license'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
