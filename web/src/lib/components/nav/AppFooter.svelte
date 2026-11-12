<script lang="ts">
	import Github from '#lib/assets/github.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
</script>

<!-- In the flow of the page. Fixed to a corner it sat on top of the table on a phone. -->
<footer class="mx-auto flex w-full max-w-5xl justify-end px-4 pb-3 sm:px-6">
	<Button
		href="https://github.com/mizuchilabs/inkan"
		target="_blank"
		rel="noopener noreferrer"
		variant="ghost"
		size="icon-sm"
		aria-label="inkan on GitHub"
		title="Powered by inkan"
	>
		<Github />
	</Button>
</footer>
