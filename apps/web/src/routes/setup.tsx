import { useEffect } from 'react'
import { createFileRoute, redirect, useRouter } from '@tanstack/react-router'
import { AppHeader } from '#/components/AppHeader'
import { ApiErrorView } from '#/components/ApiErrorView'
import { SetupChecklist } from '#/features/setup/checklist'
import { useSetupStatus } from '#/features/setup/context'
import { requireSession, requireSetupStatus } from '#/features/setup/guards'
import { safeRedirect } from '#/lib/redirect'

export const Route = createFileRoute('/setup')({
	// Needs the session cookie and the bearer token, so it renders in the browser only.
	ssr: false,
	validateSearch: (
		search: Record<string, unknown>
	): { redirect?: string } => {
		const to = safeRedirect(search.redirect)
		return to ? { redirect: to } : {}
	},
	beforeLoad: async ({ context, location, search }) => {
		await requireSession(location.href)
		const status = await requireSetupStatus(
			context.queryClient,
			location.href
		)
		// Nothing left to do: go where the user was heading.
		if (status.done) {
			const to = safeRedirect(search.redirect)
			throw to ? redirect({ href: to }) : redirect({ to: '/' })
		}
	},
	component: SetupPage,
	errorComponent: ApiErrorView
})

function SetupPage() {
	const router = useRouter()
	const status = useSetupStatus()
	const target = safeRedirect(Route.useSearch().redirect) ?? '/'

	// When the last step finishes the app opens by itself.
	useEffect(() => {
		if (status.done) router.history.push(target)
	}, [status.done, router, target])

	return (
		<>
			<AppHeader />
			<main className='mx-auto flex max-w-3xl flex-col gap-6 p-4 sm:p-8'>
				<div className='flex flex-col gap-1'>
					<h1 className='text-2xl font-semibold'>Set up your shop</h1>
					<p className='text-muted-foreground text-sm'>
						Finish these steps to start using viteordre.
					</p>
				</div>
				<SetupChecklist status={status} />
			</main>
		</>
	)
}
