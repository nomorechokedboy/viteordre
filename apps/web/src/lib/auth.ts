import { betterAuth } from 'better-auth/minimal'
import { drizzleAdapter } from 'better-auth/adapters/drizzle'
import { tanstackStartCookies } from 'better-auth/tanstack-start'
import { jwt } from 'better-auth/plugins/jwt'
import { db } from '#/db'

const baseURL = process.env.BETTER_AUTH_URL ?? 'http://localhost:3000'

// The Go API (apps/api) only accepts tokens issued for this audience. It must be configured with
// the same value as AUTH_JWT_AUDIENCE, and with AUTH_ISSUER set to the base URL above.
const apiAudience = process.env.AUTH_JWT_AUDIENCE ?? 'viteordre-api'

const googleClientId = process.env.GOOGLE_CLIENT_ID
const googleClientSecret = process.env.GOOGLE_CLIENT_SECRET

export const auth = betterAuth({
	baseURL,
	database: drizzleAdapter(db, { provider: 'sqlite' }),
	emailAndPassword: {
		enabled: true
	},
	// Google sign-in is only switched on when both credentials are present, so the app still
	// starts without them. Register http://localhost:3000/api/auth/callback/google (and the
	// production equivalent) as an authorized redirect URI in Google Cloud Console.
	...(googleClientId && googleClientSecret
		? {
				socialProviders: {
					google: {
						clientId: googleClientId,
						clientSecret: googleClientSecret,
						prompt: 'select_account' as const
					}
				}
			}
		: {}),
	plugins: [
		// Signs short-lived JWTs that the Go API verifies offline against GET /api/auth/jwks.
		// The user id is the `sub` claim; the payload only carries what the API needs.
		jwt({
			jwt: {
				issuer: baseURL,
				audience: apiAudience,
				expirationTime: '10m',
				definePayload: ({ user }) => ({
					email: user.email,
					name: user.name,
					email_verified: user.emailVerified
				})
			}
		}),
		// Must stay the last plugin.
		tanstackStartCookies()
	]
})
