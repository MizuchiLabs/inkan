// Entry for Cloudflare Workers, see wrangler.jsonc.

import { handle, type Env } from './handler.ts';

export default {
	fetch: (request: Request, env: Env): Promise<Response> => handle(request, env)
};
