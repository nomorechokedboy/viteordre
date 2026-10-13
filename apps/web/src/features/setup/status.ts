import type { ComponentType } from 'react'
import type { Merchant } from '#/features/merchants/api'

/**
 * Everything a step needs to know whether it is done. Add a field here when a new step needs
 * data of its own (for example the merchant's bank accounts) and load it in context.ts.
 */
export interface SetupContext {
	merchants: Merchant[]
}

export interface SetupStep {
	/** Stable id, used as the key and in tests. */
	id: string
	title: string
	/** One line shown under the title while the step is open or locked. */
	description: string
	/** True when the user has nothing left to do here. Must be derivable from the context alone. */
	isComplete: (ctx: SetupContext) => boolean
	/** Shown under a finished step, for example "Pho Ha Noi (/m/pho-ha-noi)". */
	summary?: (ctx: SetupContext) => string | undefined
	/** The form or panel shown while this is the current step. */
	Form: ComponentType
}

export type StepState = 'complete' | 'current' | 'locked'

export interface StepStatus {
	step: SetupStep
	state: StepState
}

export interface SetupStatus {
	steps: StepStatus[]
	/** Every step is complete: the user may use the app. */
	done: boolean
	completed: number
	current: SetupStep | undefined
}

/**
 * Steps are sequential: the first incomplete one is current and the ones after it are locked,
 * even if their own check would pass, because later steps build on earlier ones (a bank account
 * belongs to a merchant).
 */
export function computeSetup(
	steps: readonly SetupStep[],
	ctx: SetupContext
): SetupStatus {
	let currentFound = false
	let completed = 0
	let current: SetupStep | undefined
	const out: StepStatus[] = steps.map((step) => {
		if (!currentFound && step.isComplete(ctx)) {
			completed++
			return { step, state: 'complete' }
		}
		if (!currentFound) {
			currentFound = true
			current = step
			return { step, state: 'current' }
		}
		return { step, state: 'locked' }
	})
	return { steps: out, done: !currentFound, completed, current }
}
