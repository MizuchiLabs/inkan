<script lang="ts">
	import { queryClient } from '#lib/api/client.js';
	import AppFooter from '#lib/components/nav/AppFooter.svelte';
	import AppHeader from '#lib/components/nav/AppHeader.svelte';
	import { Toaster } from '#lib/components/ui/sonner/index.js';
	import * as Tooltip from '#lib/components/ui/tooltip/index.js';
	import SessionGate from '#lib/components/util/SessionGate.svelte';
	import { QueryClientProvider } from '@tanstack/svelte-query';
	import { ModeWatcher } from 'mode-watcher';
	import './layout.css';

	let { children } = $props();
</script>

<svelte:head>
	<title>inkan</title>
</svelte:head>

<!-- Same values as --background in layout.css. -->
<ModeWatcher themeColors={{ dark: '#0c0f18', light: '#f5f7fa' }} />
<Toaster />

<QueryClientProvider client={queryClient}>
	<Tooltip.Provider>
		<SessionGate>
			<div class="flex min-h-screen flex-col">
				<AppHeader />
				<main class="mx-auto mb-16 w-full max-w-5xl flex-1 px-4 sm:px-6">
					{@render children?.()}
				</main>
				<AppFooter />
			</div>
		</SessionGate>
	</Tooltip.Provider>
</QueryClientProvider>
