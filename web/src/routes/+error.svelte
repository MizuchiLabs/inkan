<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Empty from '#lib/components/ui/empty/index.js';
	import Seal from '#lib/components/util/Seal.svelte';

	const isNotFound = $derived(page.status === 404);
	const title = $derived(isNotFound ? 'Nothing sealed here' : `Error ${page.status}`);
	const description = $derived(
		isNotFound
			? "This page doesn't exist, or it moved."
			: (page.error?.message ?? 'Something went wrong.')
	);
</script>

<svelte:head>
	<title>{isNotFound ? 'Not found' : `Error ${page.status}`}, inkan</title>
</svelte:head>

<Empty.Root class="min-h-[calc(100dvh-12rem)]">
	<Empty.Header>
		<Empty.Media>
			<Seal state="blank" class="size-14" />
		</Empty.Media>
		<Empty.Title>{title}</Empty.Title>
		<Empty.Description>{description}</Empty.Description>
	</Empty.Header>
	<Empty.Content>
		<Button href={resolve('/')} size="sm">Back to licenses</Button>
	</Empty.Content>
</Empty.Root>
