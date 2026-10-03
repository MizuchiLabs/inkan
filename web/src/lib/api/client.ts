import { browser } from '$app/env';
import { QueryClient } from '@tanstack/svelte-query';
import { getSessionQueryKey } from './generated/@tanstack/svelte-query.gen';
import { client } from './generated/client.gen';
import type { ErrorModel } from './generated/types.gen';
import { toast } from 'svelte-sonner';

// Relative base URL: same-origin in production, routed to the Go backend by
// the Vite dev proxy in development.
client.setConfig({ baseUrl: '/' });

// The API answers with problem details, a failed validation names the field.
function errorMessage(err: unknown): string {
	const problem = err as ErrorModel & { message?: string };
	const first = problem?.errors?.[0];
	if (first?.message) {
		const field = first.location?.replace(/^body\./, '');
		return field ? `${field}: ${first.message}` : first.message;
	}
	return problem?.detail ?? problem?.message ?? 'The request failed';
}

export const queryClient = new QueryClient({
	defaultOptions: {
		queries: {
			enabled: browser,
			retry: false,
			staleTime: 15_000,
			refetchOnWindowFocus: true,
			// The favicon warns about a failed sync, also in a tab nobody looks at.
			refetchIntervalInBackground: true
		},
		mutations: {
			retry: false,
			onError: (err) => toast.error(errorMessage(err))
		}
	}
});

// A session can run out while the page is open. Asking again brings the sign in back.
client.interceptors.response.use((response) => {
	if (response.status === 401) queryClient.invalidateQueries({ queryKey: getSessionQueryKey() });
	return response;
});
