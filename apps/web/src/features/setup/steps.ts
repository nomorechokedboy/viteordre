import { MerchantStep } from './merchant-step'
import type { SetupStep } from './status'

/**
 * The steps a user must finish before the app opens, in order.
 *
 * To add one (bank account, SePay, ...):
 *   1. build its form component, which saves through the API and updates the query cache;
 *   2. add the data it needs to SetupContext (status.ts) and load it in context.ts;
 *   3. append an entry here with an isComplete check.
 * The checklist page, the route guards and the redirects pick it up on their own.
 */
export const setupSteps: readonly SetupStep[] = [
	{
		id: 'merchant',
		title: 'Create your shop',
		description:
			'Name your shop and pick the address customers will scan to order.',
		isComplete: (ctx) => ctx.merchants.length > 0,
		summary: (ctx) => {
			const m = ctx.merchants.at(0)
			return m ? `${m.name} (/m/${m.slug})` : undefined
		},
		Form: MerchantStep
	}
]
