import { useState } from 'react'
import { Link, createFileRoute } from '@tanstack/react-router'
import { Button } from '#/components/ui/button'
import { authClient } from '#/lib/auth-client'

// Only same-site paths are accepted, so a crafted link cannot bounce users to another site.
function safeRedirect(value: unknown): string | undefined {
	return typeof value === 'string' &&
		value.startsWith('/') &&
		!value.startsWith('//') &&
		!value.includes('\\')
		? value
		: undefined
}

export const Route = createFileRoute('/login')({
	validateSearch: (
		search: Record<string, unknown>
	): { redirect?: string } => {
		const redirect = safeRedirect(search.redirect)
		return redirect ? { redirect } : {}
	},
	component: Login
})

function Login() {
	// Checked again here: the router handed the raw query value to the component when this was
	// tested in the browser, so validateSearch alone is not a safe place to rely on.
	const redirect = safeRedirect(Route.useSearch().redirect)
	const { data: session, isPending } = authClient.useSession()
	const [error, setError] = useState<string | null>(null)
	const [busy, setBusy] = useState(false)

	async function signInWithGoogle() {
		setBusy(true)
		setError(null)
		const { error: signInError } = await authClient.signIn.social({
			provider: 'google',
			callbackURL: redirect ?? '/'
		})
		// On success the browser is already on its way to Google.
		if (signInError) {
			setError(
				signInError.message === 'Provider not found'
					? 'Google sign-in is not configured on this server'
					: (signInError.message ?? 'Could not start Google sign-in')
			)
			setBusy(false)
		}
	}

	return (
		<div className='mx-auto flex min-h-screen max-w-sm flex-col justify-center gap-6 p-8'>
			<h1 className='text-2xl font-semibold'>Sign in to viteordre</h1>
			{isPending ? null : session?.user ? (
				<p className='text-sm'>
					You are signed in as {session.user.email}.{' '}
					<Link to={redirect ?? '/'} className='underline'>
						Continue
					</Link>
				</p>
			) : (
				<>
					<Button
						onClick={() => void signInWithGoogle()}
						disabled={busy}
					>
						{busy ? 'Redirecting…' : 'Continue with Google'}
					</Button>
					{error ? (
						<p role='alert' className='text-destructive text-sm'>
							{error}
						</p>
					) : null}
				</>
			)}
		</div>
	)
}
