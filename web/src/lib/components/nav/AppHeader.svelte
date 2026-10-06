<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { useProducts, useSession, useSignOut } from '#lib/api/queries.js';
	import Inkan from '#lib/assets/inkan.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { LogOut, Moon, Sun } from '@lucide/svelte';
	import { mode, toggleMode } from 'mode-watcher';

	const productsQuery = useProducts();
	const session = useSession();
	const signOut = useSignOut();
	// Syncs run in the background, so a failed one has to show up on its own.
	const syncFailed = $derived(productsQuery.data?.some((p) => p.sync_error) ?? false);

	const links = $derived([
		{ href: resolve('/'), label: 'Licenses', alert: false },
		{ href: resolve('products'), label: 'Products', alert: syncFailed }
	]);
</script>

<!-- The only icon link on the page. With several, browsers pick by type and the live one loses. -->
<svelte:head>
	<link rel="icon" href={syncFailed ? '/favicon-alert.svg' : '/favicon.svg'} />
</svelte:head>

<header
	class="pointer-events-none sticky top-4 z-50 mx-auto mt-4 mb-8 w-full max-w-5xl px-4 sm:px-6"
>
	<div class="flex items-center justify-between gap-3">
		<nav
			class="pointer-events-auto flex h-10 min-w-0 items-center gap-0.5 rounded-full border bg-background/80 px-1.5 shadow-sm backdrop-blur-md"
			aria-label="Main"
		>
			<a href={resolve('/')} class="flex items-center gap-2 pr-3 pl-1.5" aria-label="inkan">
				<Inkan class="size-7 -rotate-12" />
				<span class="font-black tracking-tight max-sm:hidden">Inkan</span>
			</a>
			{#each links as link (link.href)}
				<Button
					href={link.href}
					variant={page.url.pathname === link.href ? 'secondary' : 'ghost'}
					size="sm"
					aria-current={page.url.pathname === link.href ? 'page' : undefined}
				>
					{link.label}
					{#if link.alert}
						<span class="size-1.5 rounded-full bg-chart-4" title="A Stripe sync failed">
							<span class="sr-only">A Stripe sync failed</span>
						</span>
					{/if}
				</Button>
			{/each}
		</nav>

		<div
			class="pointer-events-auto flex h-10 shrink-0 items-center rounded-full border bg-background/80 px-1.5 shadow-sm backdrop-blur-md"
		>
			<Button variant="ghost" size="icon-sm" onclick={toggleMode} aria-label="Toggle theme">
				{#if mode.current === 'light'}
					<Moon />
				{:else}
					<Sun />
				{/if}
			</Button>
			{#if session.data?.protected}
				<Button
					variant="ghost"
					size="icon-sm"
					onclick={() => signOut.mutate({})}
					disabled={signOut.isPending}
					aria-label="Sign out"
				>
					<LogOut />
				</Button>
			{/if}
		</div>
	</div>
</header>
