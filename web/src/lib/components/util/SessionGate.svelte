<script lang="ts">
	import { useSession, useSignIn } from '#lib/api/queries.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Empty from '#lib/components/ui/empty/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import type { Snippet } from 'svelte';
	import Seal from './Seal.svelte';

	let { children }: { children: Snippet } = $props();

	const session = useSession();
	const signIn = useSignIn();

	let password = $state('');

	function submit(event: SubmitEvent) {
		event.preventDefault();
		signIn.mutate(password, { onSuccess: () => (password = '') });
	}
</script>

{#if session.data && !session.data.signed_in}
	<Empty.Root class="min-h-dvh">
		<Empty.Header>
			<Empty.Media>
				<Seal state="blank" class="size-14" />
			</Empty.Media>
			<Empty.Title>Sign in</Empty.Title>
			<Empty.Description>
				{session.data.mode === 'oidc'
					? 'Use your account to open the ledger.'
					: 'Enter the password to open the ledger.'}
			</Empty.Description>
		</Empty.Header>
		<Empty.Content>
			{#if session.data.mode === 'oidc'}
				<!-- A page load, the browser goes to the provider and comes back. -->
				<Button href="/api/session/oidc" data-sveltekit-reload>Sign in</Button>
			{:else}
				<form onsubmit={submit} class="w-full">
					<Field.Group>
						<Field.Field data-invalid={signIn.isError ? true : undefined}>
							<Field.Label for="password">Password</Field.Label>
							<Input
								id="password"
								type="password"
								autocomplete="current-password"
								autofocus
								required
								bind:value={password}
								aria-invalid={signIn.isError ? true : undefined}
							/>
						</Field.Field>
						<Button type="submit" disabled={signIn.isPending}>
							{#if signIn.isPending}
								<Spinner data-icon="inline-start" />
							{/if}
							Sign in
						</Button>
					</Field.Group>
				</form>
			{/if}
		</Empty.Content>
	</Empty.Root>
{:else if !session.isPending}
	<!-- Also when the session cannot be loaded, the pages show their own errors. -->
	{@render children()}
{/if}
