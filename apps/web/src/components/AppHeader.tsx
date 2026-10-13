import BetterAuthHeader from '#/integrations/better-auth/header-user'

export function AppHeader() {
	return (
		<header className='border-b'>
			<div className='mx-auto flex max-w-3xl items-center justify-between gap-4 px-4 py-3'>
				<span className='font-semibold'>viteordre</span>
				<BetterAuthHeader />
			</div>
		</header>
	)
}
