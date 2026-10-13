export const SLUG_MIN = 3
export const SLUG_MAX = 40
export const SLUG_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/

/**
 * Turns a shop name into a URL slug: "Phở Hà Nội 1" becomes "pho-ha-noi-1". The server still
 * decides what is allowed (it also rejects reserved words and taken slugs), this only suggests.
 */
export function slugify(name: string): string {
	return name
		.normalize('NFD')
		.replace(/\p{M}/gu, '')
		.replace(/đ/gi, 'd')
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, '-')
		.replace(/^-+|-+$/g, '')
		.slice(0, SLUG_MAX)
		.replace(/-+$/g, '')
}
