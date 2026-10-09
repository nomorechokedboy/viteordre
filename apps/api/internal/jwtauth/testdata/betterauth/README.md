Real output of Better Auth with the jwt plugin configured as in apps/web/src/lib/auth.ts
(issuer http://localhost:3000, audience viteordre-api), one directory per signing algorithm.

- token.txt: the token returned by GET /api/auth/token after an email sign-up
- jwks.json: the response of GET /api/auth/jwks
- expect.json: the user id and email the token must yield

The tokens expired ten minutes after they were issued, so the tests pin the clock to just after
the iat claim. The keys were generated for these fixtures and protect nothing.

Regenerate them, for example after upgrading better-auth, with `bunx tsx generate.mjs` from this
directory. It needs the web app's dependencies installed (`bun install` at the repo root).
