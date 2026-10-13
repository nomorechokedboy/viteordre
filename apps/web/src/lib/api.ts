import { authClient } from '#/lib/auth-client'
import { env } from '#/env'

const API_URL = env.VITE_API_URL ?? 'http://localhost:4000'

interface CachedToken {
	token: string
	expiresAt: number // ms since epoch
}

let cached: CachedToken | null = null

// The `exp` claim of a JWT, in milliseconds. The signature is not checked here, the Go API does
// that. This is only used to know when to ask for a fresh token.
function expiryOf(token: string): number {
	try {
		const payload = token.split('.')[1] ?? ''
		const json = atob(payload.replace(/-/g, '+').replace(/_/g, '/'))
		const { exp } = JSON.parse(json) as { exp?: number }
		if (typeof exp === 'number') return exp * 1000
	} catch {
		// fall through
	}
	return Date.now() + 60_000
}

/** A bearer token for the Go API, reused until 30 seconds before it expires. */
export async function getApiToken(force = false): Promise<string> {
	if (!force && cached && cached.expiresAt - 30_000 > Date.now()) {
		return cached.token
	}
	const { data, error } = await authClient.token()
	if (error || !data.token) {
		cached = null
		throw new Error('Not signed in')
	}
	cached = { token: data.token, expiresAt: expiryOf(data.token) }
	return cached.token
}

/** Forget the cached token, call this on sign-out. */
export function clearApiToken() {
	cached = null
}

/**
 * fetch() for the Go API. Adds the bearer token and retries once with a fresh token when the
 * API answers 401, which covers a token that expired or a signing key that rotated.
 */
export async function apiFetch(
	path: string,
	init: RequestInit = {}
): Promise<Response> {
	const send = async (force: boolean) => {
		const headers = new Headers(init.headers)
		headers.set('Authorization', `Bearer ${await getApiToken(force)}`)
		if (init.body && !headers.has('Content-Type')) {
			headers.set('Content-Type', 'application/json')
		}
		return fetch(`${API_URL}${path}`, { ...init, headers })
	}
	const res = await send(false)
	return res.status === 401 ? send(true) : res
}

/** One bad input field, as the API reports it under `details.fields`. */
export interface ApiFieldError {
	field: string
	code: string
	message: string
}

/**
 * An error answered by the Go API. `code` is Encore's error code (`invalid_argument`,
 * `already_exists`, `not_found`, ...), `message` is safe to show, and `fields` lists the inputs
 * that were rejected when there are any.
 */
export class ApiError extends Error {
	readonly status: number
	readonly code: string
	readonly fields: ApiFieldError[]

	constructor(
		status: number,
		code: string,
		message: string,
		fields: ApiFieldError[] = []
	) {
		super(message)
		this.name = 'ApiError'
		this.status = status
		this.code = code
		this.fields = fields
	}
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null
}

async function toApiError(res: Response): Promise<ApiError> {
	let code = 'unknown'
	let message = `Request failed (${res.status})`
	let fields: ApiFieldError[] = []
	try {
		const body: unknown = await res.json()
		if (isRecord(body)) {
			if (typeof body.code === 'string') code = body.code
			if (typeof body.message === 'string' && body.message) {
				message = body.message
			}
			const details = body.details
			if (isRecord(details) && Array.isArray(details.fields)) {
				fields = details.fields.filter(
					(f): f is ApiFieldError =>
						isRecord(f) &&
						typeof f.field === 'string' &&
						typeof f.message === 'string'
				)
			}
		}
	} catch {
		// Not JSON (a proxy error page, for example): keep the generic message.
	}
	return new ApiError(res.status, code, message, fields)
}

/** apiFetch that parses the JSON answer and throws an ApiError on any non-2xx status. */
export async function apiJson<T>(
	path: string,
	init: RequestInit = {}
): Promise<T> {
	const res = await apiFetch(path, init)
	if (!res.ok) throw await toApiError(res)
	return (await res.json()) as T
}
