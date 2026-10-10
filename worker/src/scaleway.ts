// Entry for Scaleway Serverless Functions on a Node runtime. `pnpm build`
// bundles it into dist/handler.js, where the default handler handler.handle
// finds it.

import { handle as answer, type Env } from './handler.ts';

type Event = {
	httpMethod: string;
	path: string;
	headers?: Record<string, string>;
	queryStringParameters?: Record<string, string> | null;
	body?: string | null;
	isBase64Encoded?: boolean;
};

export async function handle(event: Event) {
	const query = new URLSearchParams(event.queryStringParameters ?? {}).toString();
	const bodiless = event.httpMethod === 'GET' || event.httpMethod === 'HEAD';
	// The Stripe signature covers the body as it was sent, so it must not be parsed on the way.
	const body = event.isBase64Encoded ? Buffer.from(event.body ?? '', 'base64') : event.body;
	const request = new Request('https://worker' + event.path + (query ? '?' + query : ''), {
		method: event.httpMethod,
		headers: event.headers,
		body: bodiless ? undefined : (body ?? '')
	});

	const response = await answer(request, process.env as unknown as Env);
	return {
		statusCode: response.status,
		headers: Object.fromEntries(response.headers),
		body: await response.text()
	};
}
