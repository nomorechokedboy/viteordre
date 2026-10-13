import { Circle, CircleCheck, Lock } from 'lucide-react'
import { cn } from 'cn'
import { useSetupContext } from './context'
import type { SetupStatus, StepStatus } from './status'

/**
 * The setup checklist: finished steps show a summary, the current step shows its form and the
 * steps after it stay locked until it is done.
 */
export function SetupChecklist({ status }: { status: SetupStatus }) {
	const total = status.steps.length
	return (
		<div className='flex flex-col gap-6'>
			<div className='flex flex-col gap-2'>
				<p className='text-muted-foreground text-sm'>
					{status.completed} of {total} steps done
				</p>
				<div
					role='progressbar'
					aria-valuemin={0}
					aria-valuemax={total}
					aria-valuenow={status.completed}
					className='bg-muted h-1.5 overflow-hidden rounded-full'
				>
					<div
						className='bg-primary h-full transition-all'
						style={{
							width: `${total === 0 ? 100 : (status.completed / total) * 100}%`
						}}
					/>
				</div>
			</div>
			<ol className='flex flex-col gap-4'>
				{status.steps.map((s, i) => (
					<StepCard key={s.step.id} item={s} index={i + 1} />
				))}
			</ol>
		</div>
	)
}

function StepCard({ item, index }: { item: StepStatus; index: number }) {
	const { step, state } = item
	const ctx = useSetupContext()
	const Form = step.Form
	return (
		<li
			data-step={step.id}
			data-state={state}
			className={cn(
				'bg-card text-card-foreground rounded-xl border p-5',
				state === 'locked' && 'opacity-60'
			)}
		>
			<div className='flex items-start gap-3'>
				<StateIcon state={state} />
				<div className='flex min-w-0 flex-1 flex-col gap-1'>
					<h2 className='font-semibold'>
						{index}. {step.title}
					</h2>
					<p className='text-muted-foreground text-sm'>
						{state === 'complete'
							? (step.summary?.(ctx) ?? 'Done')
							: state === 'locked'
								? 'Finish the previous step first.'
								: step.description}
					</p>
				</div>
			</div>
			{state === 'current' ? (
				<div className='mt-5 pl-8'>
					<Form />
				</div>
			) : null}
		</li>
	)
}

function StateIcon({ state }: { state: StepStatus['state'] }) {
	const common = 'mt-0.5 size-5 shrink-0'
	if (state === 'complete') {
		return (
			<CircleCheck
				aria-label='Done'
				className={cn(common, 'text-green-600')}
			/>
		)
	}
	if (state === 'locked') {
		return (
			<Lock
				aria-label='Locked'
				className={cn(common, 'text-muted-foreground')}
			/>
		)
	}
	return (
		<Circle
			aria-label='In progress'
			className={cn(common, 'text-primary')}
		/>
	)
}
