import { queryOptions } from '@tanstack/react-query'
import { apiJson } from '#/lib/api'

export type MerchantRole = 'owner' | 'manager' | 'staff'

/** A merchant (shop) the signed-in user belongs to, as GET /merchants returns it. */
export interface Merchant {
	id: string
	slug: string
	name: string
	role?: MerchantRole
}

export interface CreateMerchantInput {
	name: string
	slug: string
	currency?: string
	timezone?: string
}

export async function listMerchants(): Promise<Merchant[]> {
	const res = await apiJson<{ merchants: Merchant[] }>('/merchants')
	return res.merchants
}

export function createMerchant(input: CreateMerchantInput): Promise<Merchant> {
	return apiJson<Merchant>('/merchants', {
		method: 'POST',
		body: JSON.stringify(input)
	})
}

export const merchantsQueryOptions = queryOptions({
	queryKey: ['merchants'],
	queryFn: listMerchants,
	staleTime: 30_000
})
