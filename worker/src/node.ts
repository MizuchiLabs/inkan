// Entry for Node, for a container or a server of your own:
//
//  node src/node.ts
//
// The settings come from the environment, PORT is where it listens.

import { createServer } from 'node:http';

import { handle, missing, type Env } from './handler.ts';

// A Stripe event stays well below this.
const maxBody = 1 << 20;

const env = process.env as unknown as Env;
const unset = missing(env);
if (unset.length > 0) {
	console.error('not set: ' + unset.join(', '));
	process.exit(1);
}

const server = createServer(async (req, res) => {
	const chunks: Buffer[] = [];
	let size = 0;
	for await (const chunk of req) {
		size += chunk.length;
		if (size > maxBody) {
			res.writeHead(413).end('the request is too large');
			return;
		}
		chunks.push(chunk);
	}

	const headers = new Headers();
	for (const [name, value] of Object.entries(req.headers)) {
		if (value !== undefined) headers.set(name, Array.isArray(value) ? value.join(', ') : value);
	}
	const bodiless = req.method === 'GET' || req.method === 'HEAD';
	const request = new Request(new URL(req.url ?? '/', 'http://' + (req.headers.host ?? 'worker')), {
		method: req.method,
		headers,
		body: bodiless ? undefined : Buffer.concat(chunks)
	});

	const response = await handle(request, env);
	res.writeHead(response.status, Object.fromEntries(response.headers));
	res.end(Buffer.from(await response.arrayBuffer()));
});

const port = Number(process.env.PORT ?? 8080);
server.listen(port, () => console.log(`worker for ${env.INKAN_PREFIX} listening on :${port}`));
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => server.close());
