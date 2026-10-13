/**
 * Only same-site paths are accepted, so a crafted link cannot bounce users to another site.
 * Returns undefined for anything else.
 */
export function safeRedirect(value: unknown): string | undefined {
	return typeof value === 'string' &&
		value.startsWith('/') &&
		!value.startsWith('//') &&
		!value.includes('\\')
		? value
		: undefined
}
