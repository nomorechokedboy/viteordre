import { betterAuth } from 'better-auth/minimal'
import { drizzleAdapter } from 'better-auth/adapters/drizzle'
import { tanstackStartCookies } from 'better-auth/tanstack-start'
import { db } from '#/db'

export const auth = betterAuth({
	database: drizzleAdapter(db, { provider: 'sqlite' }),
	emailAndPassword: {
		enabled: true
	},
	plugins: [tanstackStartCookies()]
})
