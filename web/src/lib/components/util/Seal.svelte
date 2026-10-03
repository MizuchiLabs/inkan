<script lang="ts" module>
	// The key decides every turn, so a key always carves the same seal.
	function seeded(text: string) {
		let h = 0x811c9dc5;
		for (let i = 0; i < text.length; i++) h = Math.imul(h ^ text.charCodeAt(i), 0x01000193);
		return (below: number) => {
			h = (h + 0x6d2b79f5) | 0;
			let t = Math.imul(h ^ (h >>> 15), 1 | h);
			t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
			return Math.floor((((t ^ (t >>> 14)) >>> 0) / 4294967296) * below);
		};
	}

	// A 5 by 5 lattice without its corners, so the carving fills a round face.
	const onFace = (c: number, r: number) =>
		c >= 0 && c <= 4 && r >= 0 && r <= 4 && (c % 4 !== 0 || r % 4 !== 0);
	const at = (n: number) => 50 + (n - 2) * 14.5;

	function carve(publicKey: string) {
		const pick = seeded(publicKey);
		const seen = new Set<number>();
		let strokes: number[][] = [];

		// A winding walk through the left half and the middle column. It visits every point once.
		function walk(c: number, r: number) {
			seen.add(c * 5 + r);
			const turns = [
				[1, 0],
				[-1, 0],
				[0, 1],
				[0, -1]
			];
			for (let i = 3; i > 0; i--) {
				const j = pick(i + 1);
				[turns[i], turns[j]] = [turns[j], turns[i]];
			}
			for (const [dc, dr] of turns) {
				const [nc, nr] = [c + dc, r + dr];
				if (nc > 2 || !onFace(nc, nr) || seen.has(nc * 5 + nr)) continue;
				strokes.push([c, r, nc, nr]);
				walk(nc, nr);
			}
		}
		walk(2, pick(5));

		// Mirrored like a seal script character. Up to two strokes go missing, so it is not too tidy.
		strokes = strokes.concat(
			strokes.filter(([c, , nc]) => c + nc < 4).map(([c, r, nc, nr]) => [4 - c, r, 4 - nc, nr])
		);
		for (let missing = pick(3); missing > 0; missing--) strokes.splice(pick(strokes.length), 1);

		// The rim is worn in two places. The last dash runs past the end of the circle.
		const first = pick(110);
		const firstWidth = 2 + pick(4);
		const second = first + 60 + pick(100);
		return {
			strokes: strokes.map(([c, r, nc, nr]) => `M${at(c)} ${at(r)}L${at(nc)} ${at(nr)}`).join(''),
			rim: `${first} ${firstWidth} ${second - first - firstWidth} ${2 + pick(4)} 300`
		};
	}
</script>

<script lang="ts">
	import { cn } from '#lib/utils.js';

	type Props = {
		// The seal of a product is carved from its public key.
		publicKey?: string;
		// A fresh impression, one whose ink ran out, or the empty spot of a missing one.
		state?: 'sealed' | 'faded' | 'blank';
		class?: string;
	};
	let { publicKey, state = 'sealed', class: className }: Props = $props();

	const carving = $derived(publicKey && state !== 'blank' ? carve(publicKey) : undefined);
</script>

<svg
	viewBox="0 0 100 100"
	fill="none"
	stroke="currentColor"
	aria-hidden="true"
	class={cn(
		'shrink-0',
		carving ? 'text-seal' : 'text-muted-foreground/50',
		state === 'faded' && 'opacity-40',
		className
	)}
>
	{#if carving}
		<circle cx="50" cy="50" r="45.5" stroke-width="4.5" stroke-dasharray={carving.rim} />
		<path d={carving.strokes} stroke-width="7.5" stroke-linecap="square" />
	{:else}
		<circle cx="50" cy="50" r="44" stroke-width="3" stroke-dasharray="5 7" />
	{/if}
</svg>
