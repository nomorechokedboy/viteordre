import { Outlet, createFileRoute, redirect } from '@tanstack/react-router'
import { AppHeader } from '#/components/AppHeader'
import { ApiErrorView } from '#/components/ApiErrorView'
import { requireSession, requireSetupStatus } from '#/features/setup/guards'

// Everything inside the app needs a signed-in user who has finished setup. Both checks need the
// browser (session cookie, bearer token for the Go API), so this branch renders on the client.
export const Route = createFileRoute('/_app')({
	ssr: false,
	beforeLoad: async ({ context, location }) => {
		await requireSession(location.href)
		const status = await requireSetupStatus(
			context.queryClient,
			location.href
		)
		if (!status.done) {
			throw redirect({
				to: '/setup',
				search:
					location.pathname === '/' ? {} : { redirect: location.href }
			})
		}
	},
	component: AppLayout,
	errorComponent: ApiErrorView
})

function AppLayout() {
	return (
		<>
			<AppHeader />
			<main className='mx-auto max-w-3xl p-4 sm:p-8'>
				<Outlet />
			</main>
		</>
	)
}
