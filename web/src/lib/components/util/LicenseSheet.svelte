<script lang="ts">
	import {
		useDeleteLicense,
		useUpdateLicense,
		type License,
		type Product
	} from '#lib/api/queries.js';
	import * as AlertDialog from '#lib/components/ui/alert-dialog/index.js';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import * as Sheet from '#lib/components/ui/sheet/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { formatDate, formatRelative, stateOf } from '#lib/license.js';
	import { cn } from '#lib/utils.js';
	import { PencilIcon, RefreshCwIcon, TrashIcon } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import CopyBlock from './CopyBlock.svelte';
	import Seal from './Seal.svelte';
	import StatusBadge from './StatusBadge.svelte';

	type Props = {
		license?: License;
		product?: Product;
		// Issued a moment ago, so the seal lands as the sheet opens.
		fresh?: boolean;
		onclose: () => void;
		onedit: (license: License) => void;
	};
	let { license, product, fresh = false, onclose, onedit }: Props = $props();

	const update = useUpdateLicense();
	const remove = useDeleteLicense();

	const status = $derived(license ? stateOf(license) : 'active');
	const meta = $derived(Object.entries(license?.meta ?? {}));
	// Follows the license, and stays editable in between.
	let note = $derived(license?.note ?? '');

	let confirmDelete = $state(false);

	function saveNote() {
		if (!license) return;
		update.mutate(
			{
				path: { product_id: license.product_id, id: license.id },
				body: {
					email: license.email,
					customer: license.customer,
					plan: license.plan,
					expires_at: license.expires_at ?? undefined,
					meta: license.meta,
					note
				}
			},
			{ onSuccess: () => toast.success('Saved the note') }
		);
	}

	function deleteLicense() {
		if (!license) return;
		remove.mutate(
			{ path: { product_id: license.product_id, id: license.id } },
			{
				onSuccess: () => {
					toast.success('Deleted the license');
					onclose();
				}
			}
		);
	}
</script>

<Sheet.Root open={license !== undefined} onOpenChange={(isOpen) => !isOpen && onclose()}>
	<Sheet.Content class="sm:max-w-lg">
		{#if license}
			<Sheet.Header>
				<div class="flex items-center gap-3">
					<!-- The stamp waits for the sheet to slide in. -->
					<div
						class={cn('shrink-0', fresh ? 'animate-stamp' : '-rotate-6')}
						style:--stamp-delay="0.25s"
					>
						<Seal
							publicKey={product?.public_key}
							state={status === 'expired' ? 'faded' : 'sealed'}
							class="size-12"
						/>
					</div>
					<div class="min-w-0">
						<Sheet.Title>
							<span class="block truncate">{license.customer || license.email}</span>
						</Sheet.Title>
						<Sheet.Description>{product?.name} {license.plan} license</Sheet.Description>
					</div>
				</div>
			</Sheet.Header>

			<div class="flex flex-1 flex-col gap-6 overflow-y-auto px-4 pb-4">
				<CopyBlock title="License key" text={license.key} what="key" copyLabel="Copy key">
					Signed from the fields below. It is the same key every time, nothing is stored.
				</CopyBlock>

				<dl class="grid grid-cols-3 gap-x-4 gap-y-3 text-sm">
					<dt class="text-muted-foreground">Status</dt>
					<dd class="col-span-2 flex flex-wrap items-center gap-2">
						<StatusBadge state={status} />
						{#if license.renews}
							<Badge variant="secondary">
								<RefreshCwIcon />
								Renews
							</Badge>
						{/if}
					</dd>

					<dt class="text-muted-foreground">Valid until</dt>
					<dd class="col-span-2">
						{#if license.expires_at}
							{formatDate(license.expires_at)}, {formatRelative(license.expires_at)}
						{:else}
							Never expires
						{/if}
					</dd>

					<dt class="text-muted-foreground">Email</dt>
					<dd class="col-span-2 break-all">{license.email || 'None'}</dd>

					<dt class="text-muted-foreground">Source</dt>
					<dd class="col-span-2">
						{#if license.source === 'stripe'}
							Stripe subscription, {license.status.replaceAll('_', ' ')}
						{:else}
							Issued by hand
						{/if}
					</dd>

					<dt class="text-muted-foreground">License id</dt>
					<dd class="col-span-2 font-mono text-xs break-all">{license.id}</dd>

					<dt class="text-muted-foreground">Created</dt>
					<dd class="col-span-2">{formatDate(license.created_at)}</dd>

					{#if meta.length > 0}
						<dt class="text-muted-foreground">Extra claims</dt>
						<dd class="col-span-2 flex flex-wrap gap-1.5">
							{#each meta as [name, value] (name)}
								<Badge variant="outline"><span class="font-mono">{name}={value}</span></Badge>
							{/each}
						</dd>
					{/if}
				</dl>

				<Field.Field>
					<Field.Label for="sheet-note">Note</Field.Label>
					<Textarea id="sheet-note" bind:value={note} rows={3} />
					<Field.Description>Only shown here, never part of the key.</Field.Description>
					{#if note !== license.note}
						<Button size="sm" class="self-start" onclick={saveNote} disabled={update.isPending}>
							Save note
						</Button>
					{/if}
				</Field.Field>
			</div>

			<Sheet.Footer>
				{#if license.source === 'manual'}
					<div class="flex gap-2">
						<Button variant="outline" class="flex-1" onclick={() => onedit(license)}>
							<PencilIcon data-icon="inline-start" />
							Edit license
						</Button>
						<Button variant="destructive" onclick={() => (confirmDelete = true)}>
							<TrashIcon data-icon="inline-start" />
							Delete
						</Button>
					</div>
				{:else}
					<p class="text-sm text-muted-foreground">
						Stripe owns this license. Plan and grace period come from the product, everything else
						from the subscription.
					</p>
				{/if}
			</Sheet.Footer>
		{/if}
	</Sheet.Content>
</Sheet.Root>

<AlertDialog.Root bind:open={confirmDelete}>
	<AlertDialog.Content>
		<AlertDialog.Header>
			<AlertDialog.Title>Delete this license?</AlertDialog.Title>
			<AlertDialog.Description>
				It disappears from the ledger. The key is signed and works offline, so a copy the customer
				already has keeps working until it expires.
			</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel>Keep license</AlertDialog.Cancel>
			<AlertDialog.Action variant="destructive" onclick={deleteLicense}>
				Delete license
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
