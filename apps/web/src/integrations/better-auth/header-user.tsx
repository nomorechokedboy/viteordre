import { Link, useRouter } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { Button } from '#/components/ui/button'
import { authClient } from '#/lib/auth-client'
import { clearApiToken } from '#/lib/api'

export default function BetterAuthHeader() {
	const { data: session, isPending } = authClient.useSession()
	const router = useRouter()
	const queryClient = useQueryClient()

	async function signOut() {
		clearApiToken()
		await authClient.signOut()
		await router.navigate({ to: '/login' })
		// Cleared after leaving the page, so no mounted query refetches without a session.
		queryClient.clear()
	}

	if (isPending) {
		return <div className='bg-muted h-8 w-8 animate-pulse rounded-full' />
	}

	if (session?.user) {
		return (
			<div className='flex items-center gap-3'>
				<span className='text-muted-foreground hidden text-sm sm:inline'>
					{session.user.email}
				</span>
				<Button
					variant='outline'
					size='sm'
					onClick={() => void signOut()}
				>
					Sign out
				</Button>
			</div>
		)
	}

	return (
		<Button asChild variant='outline' size='sm'>
			<Link to='/login'>Sign in</Link>
		</Button>
	)
}
