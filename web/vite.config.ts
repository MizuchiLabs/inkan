import adapter from '@sveltejs/adapter-static';
import tailwindcss from '@tailwindcss/vite';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig, type Plugin } from 'vite';

// In dev the Go server hands the UI out from Vite, so that is the url to open.
function goServerUrl(): Plugin {
	const addr = process.env.INKAN_ADDR || '127.0.0.1:3000';
	const url = `http://${addr.startsWith(':') ? 'localhost' + addr : addr}/`;
	return {
		name: 'go-server-url',
		configureServer(server) {
			server.printUrls = () => server.config.logger.info(`  ➜  Dashboard: ${url}`);
		}
	};
}

export default defineConfig({
	// The Go server proxies the UI from here in dev, so the port must not move.
	// The mail preview renders the key mail of the worker, see src/lib/mail.ts.
	server: { port: 5173, strictPort: true, fs: { allow: ['../worker/src'] } },
	plugins: [
		goServerUrl(),
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
