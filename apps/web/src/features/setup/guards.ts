import { redirect } from '@tanstack/react-router'
import type { QueryClient } from '@tanstack/react-query'
import { authClient } from '#/lib/auth-client'
import { ApiError } from '#/lib/api'
import { loadSetupStatus } from './context'
import type { SetupStatus } from './status'

/** Where to send someone who is not signed in, coming back to `href` afterwards. */
function toLogin(href: string) {
	return redirect({
		to: '/login',
		search: href === '/' ? {} : { redirect: href }
	})
}

/** Route guard: sends signed-out visitors to /login. Browser only, the session cookie is read there. */
export async function requireSession(href: string) {
	const { data } = await authClient.getSession()
	if (!data) throw toLogin(href)
	return data
}

/**
 * Route guard: loads the setup status. A 401 from the API means the session is gone, so it is
 * handled like a signed-out visitor; any other failure is left for the route's error view.
 */
export async function requireSetupStatus(
	queryClient: QueryClient,
	href: string
): Promise<SetupStatus> {
	try {
		return await loadSetupStatus(queryClient)
	} catch (e) {
		if (e instanceof ApiError && e.status === 401) throw toLogin(href)
		throw e
	}
}
