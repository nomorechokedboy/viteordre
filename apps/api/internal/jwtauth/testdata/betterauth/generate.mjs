// Regenerates the fixtures in this directory from a real Better Auth instance.
//
//   cd apps/api/internal/jwtauth/testdata/betterauth && bunx tsx generate.mjs
//
// It applies the web app's real drizzle migrations to a temporary database, configures Better
// Auth like apps/web/src/lib/auth.ts, signs a user up with email and password, and saves the
// token, the JWKS and the expected claims for every supported signing algorithm.
import { createClient } from '@libsql/client'
import { drizzle } from 'drizzle-orm/libsql'
import { migrate } from 'drizzle-orm/libsql/migrator'
import { betterAuth } from 'better-auth/minimal'
import { drizzleAdapter } from 'better-auth/adapters/drizzle'
import { jwt } from 'better-auth/plugins/jwt'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const web = path.resolve(here, '../../../../../web')
const schema = await import(path.join(web, 'src/db/auth-schema.ts'))

const baseURL = 'http://localhost:3000'
const audience = 'viteordre-api'

for (const alg of ['EdDSA', 'ES256', 'RS256', 'PS256', 'ES512']) {
	const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ba-'))
	const client = createClient({ url: `file:${path.join(dir, 'auth.db')}` })
	const db = drizzle(client, { schema })
	await migrate(db, { migrationsFolder: path.join(web, 'drizzle') })

	const auth = betterAuth({
		baseURL,
		secret: 'fixtures-only-'.repeat(4),
		database: drizzleAdapter(db, { provider: 'sqlite' }),
		emailAndPassword: { enabled: true },
		plugins: [
			jwt({
				jwks: { keyPairConfig: { alg } },
				jwt: {
					issuer: baseURL,
					audience,
					expirationTime: '10m',
					definePayload: ({ user }) => ({
						email: user.email,
						name: user.name,
						email_verified: user.emailVerified
					})
				}
			})
		]
	})
	const call = (p, init = {}) =>
		auth.handler(
			new Request(baseURL + p, {
				...init,
				headers: { origin: baseURL, 'content-type': 'application/json', ...(init.headers ?? {}) }
			})
		)

	const signUp = await call('/api/auth/sign-up/email', {
		method: 'POST',
		body: JSON.stringify({ email: 'owner@example.com', password: 'correct-horse-battery', name: 'Owner One' })
	})
	if (!signUp.ok) throw new Error(`sign-up failed: ${signUp.status} ${await signUp.text()}`)
	const cookie = signUp.headers.getSetCookie().map((c) => c.split(';')[0]).join('; ')
	const userId = (await signUp.json()).user.id

	const tok = await call('/api/auth/token', { headers: { cookie } })
	if (!tok.ok) throw new Error(`token failed: ${tok.status} ${await tok.text()}`)
	const { token } = await tok.json()
	const jwks = await (await call('/api/auth/jwks')).json()

	const out = path.join(here, alg)
	fs.mkdirSync(out, { recursive: true })
	fs.writeFileSync(path.join(out, 'token.txt'), token)
	fs.writeFileSync(path.join(out, 'jwks.json'), JSON.stringify(jwks, null, 2))
	fs.writeFileSync(
		path.join(out, 'expect.json'),
		JSON.stringify({ userId, email: 'owner@example.com', iss: baseURL, aud: audience })
	)
	fs.rmSync(dir, { recursive: true, force: true })
	console.log('wrote', alg)
}
