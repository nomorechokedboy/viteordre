import { useRouter } from '@tanstack/react-router'
import { Button } from '#/components/ui/button'

/** Shown by a route when its data could not be loaded. */
export function ApiErrorView({
	error,
	reset
}: {
	error: unknown
	reset: () => void
}) {
	const router = useRouter()
	const message = error instanceof Error ? error.message : ''
	return (
		<div className='mx-auto flex min-h-[50vh] max-w-md flex-col justify-center gap-4 p-8'>
			<h1 className='text-xl font-semibold'>Something went wrong</h1>
			<p className='text-muted-foreground text-sm'>
				{message || 'We could not load your shop.'}
			</p>
			<Button
				className='self-start'
				onClick={() => {
					reset()
					void router.invalidate()
				}}
			>
				Try again
			</Button>
		</div>
	)
}
