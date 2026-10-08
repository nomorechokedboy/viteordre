import { createClient } from '@libsql/client'
import { drizzle } from 'drizzle-orm/libsql/node'

import * as schema from './auth-schema'

const client = createClient({
	url: process.env.DATABASE_URL!
})

export const db = drizzle(client, { schema })
