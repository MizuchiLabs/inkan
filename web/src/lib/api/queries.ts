import { createMutation, createQuery } from '@tanstack/svelte-query';
import { queryClient, sessionKey } from './client';
import {
	checkKeyMutation,
	createLicenseMutation,
	createProductMutation,
	deleteLicenseMutation,
	deleteProductMutation,
	dropOldProductKeysMutation,
	listLicensesOptions,
	listLicensesQueryKey,
	listProductsOptions,
	listProductsQueryKey,
	listStatsOptions,
	rotateProductKeyMutation,
	syncProductMutation,
	updateLicenseMutation,
	updateProductMutation
} from './generated/@tanstack/svelte-query.gen';
import { client } from './generated/client.gen';

export type { CheckResult, License, LicenseFields, Product } from './generated/types.gen';

// nushi serves the session routes in front of the API, so they are not in the OpenAPI spec.
export type Session = { mode: 'none' | 'password' | 'oidc'; signed_in: boolean };

const sessionUrl = '/api/session';

// The workers are read in the background, so both lists keep themselves fresh.
export function useProducts() {
	return createQuery(() => ({ ...listProductsOptions(), refetchInterval: 60_000 }));
}

export function useLicenses() {
	return createQuery(() => ({ ...listLicensesOptions(), refetchInterval: 60_000 }));
}

// One count a day, so there is nothing to poll for.
export function useStats() {
	return createQuery(() => ({ ...listStatsOptions(), staleTime: 60 * 60_000 }));
}

function refreshLicenses() {
	return queryClient.invalidateQueries({ queryKey: listLicensesQueryKey() });
}

// Products shape their licenses (plan, grace, deletion), so both lists reload.
function refreshAll() {
	return Promise.all([
		queryClient.invalidateQueries({ queryKey: listProductsQueryKey() }),
		refreshLicenses()
	]);
}

export function useSession() {
	return createQuery(() => ({
		queryKey: sessionKey,
		queryFn: async () => {
			const { data } = await client.get<{ 200: Session }, unknown, true>({
				url: sessionUrl,
				throwOnError: true
			});
			return data;
		}
	}));
}

// Everything was asked for without a session, so all of it loads again.
export function useSignIn() {
	return createMutation(() => ({
		mutationFn: (password: string) =>
			client.post({
				url: sessionUrl,
				body: { password },
				headers: { 'Content-Type': 'application/json' },
				throwOnError: true
			}),
		onSuccess: () => queryClient.invalidateQueries()
	}));
}

// A reload also drops what the cache still holds.
export function useSignOut() {
	return createMutation(() => ({
		mutationFn: () => client.delete({ url: sessionUrl, throwOnError: true }),
		onSuccess: () => location.reload()
	}));
}

export function useCheckKey() {
	return createMutation(() => checkKeyMutation());
}

export function useCreateLicense() {
	return createMutation(() => ({ ...createLicenseMutation(), onSuccess: refreshLicenses }));
}

export function useUpdateLicense() {
	return createMutation(() => ({ ...updateLicenseMutation(), onSuccess: refreshLicenses }));
}

export function useDeleteLicense() {
	return createMutation(() => ({ ...deleteLicenseMutation(), onSuccess: refreshLicenses }));
}

export function useCreateProduct() {
	return createMutation(() => ({ ...createProductMutation(), onSuccess: refreshAll }));
}

export function useUpdateProduct() {
	return createMutation(() => ({ ...updateProductMutation(), onSuccess: refreshAll }));
}

export function useDeleteProduct() {
	return createMutation(() => ({ ...deleteProductMutation(), onSuccess: refreshAll }));
}

// A new signing key gives every manual license a new key, so both lists reload.
export function useRotateProductKey() {
	return createMutation(() => ({ ...rotateProductKeyMutation(), onSuccess: refreshAll }));
}

export function useDropOldProductKeys() {
	return createMutation(() => ({ ...dropOldProductKeysMutation(), onSuccess: refreshAll }));
}

// A failed sync is stored on the product, so the lists reload either way.
export function useSyncProduct() {
	return createMutation(() => ({ ...syncProductMutation(), onSettled: refreshAll }));
}
