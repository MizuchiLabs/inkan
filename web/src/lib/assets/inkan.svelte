<script lang="ts">
	import { cn } from '#lib/utils.js';
	import type { SVGAttributes } from 'svelte/elements';

	let { class: className, ...restProps }: SVGAttributes<SVGSVGElement> = $props();
</script>

<!-- The character is cut out of the seal, so the paper behind it shows through. -->
<svg
	xmlns="http://www.w3.org/2000/svg"
	viewBox="0 0 100 100"
	fill="currentColor"
	fill-rule="evenodd"
	aria-hidden="true"
	class={cn('shrink-0 text-seal', className)}
	{...restProps}
>
	<path
		d="M50 2a48 48 0 1 0 0 96 48 48 0 1 0 0-96ZM49.87 80.1V24.26H77.15V64.28Q77.15 68.31 74.85 70.07Q72.54 71.84 67.8 71.84H62.87Q62.81 70.55 62.49 68.57Q62.17 66.58 61.75 64.7Q61.33 62.81 60.82 61.72H65.05Q66.39 61.72 66.97 61.3Q67.55 60.89 67.55 59.61V33.03H59.41V80.1ZM22.85 70.62V26.37Q25.15 26.12 27.94 25.51Q30.73 24.9 33.58 24Q36.42 23.11 38.92 22.05Q41.42 20.99 43.21 19.9L49.49 27.2Q47.05 28.55 43.98 29.83Q40.91 31.11 37.8 32.1Q34.7 33.1 32.07 33.67V41.68H47.31V50.58H32.07V61.85H47.31V70.62Z"
	/>
</svg>
