import adapter from '@sveltejs/adapter-static';
import tailwindcss from '@tailwindcss/vite';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

const proxy = {
	'^/(api|openapi|docs|schemas)': {
		target: 'http://localhost:3000',
		changeOrigin: true
	}
};
export default defineConfig({
	preview: { proxy },
	// The mail preview renders the key mail of the worker, see src/lib/mail.ts.
	server: { proxy, fs: { allow: ['../worker/src'] } },
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			adapter: adapter({ fallback: 'index.html', precompress: true })
		})
	]
});
