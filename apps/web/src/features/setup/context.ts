import { useSuspenseQuery } from '@tanstack/react-query'
import type { QueryClient } from '@tanstack/react-query'
import { merchantsQueryOptions } from '#/features/merchants/api'
import { setupSteps } from './steps'
import { computeSetup } from './status'
import type { SetupContext, SetupStatus } from './status'

/** Loads what the steps need, from the query cache when it is fresh. Used by route guards. */
export async function loadSetupContext(
	queryClient: QueryClient
): Promise<SetupContext> {
	const merchants = await queryClient.ensureQueryData(merchantsQueryOptions)
	return { merchants }
}

export async function loadSetupStatus(
	queryClient: QueryClient
): Promise<SetupStatus> {
	return computeSetup(setupSteps, await loadSetupContext(queryClient))
}

/** The live setup context for components. It updates when the cached data does. */
export function useSetupContext(): SetupContext {
	const { data: merchants } = useSuspenseQuery(merchantsQueryOptions)
	return { merchants }
}

export function useSetupStatus(): SetupStatus {
	return computeSetup(setupSteps, useSetupContext())
}
