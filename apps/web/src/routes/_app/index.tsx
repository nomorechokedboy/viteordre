import { createFileRoute } from '@tanstack/react-router'
import { useSuspenseQuery } from '@tanstack/react-query'
import { merchantsQueryOptions } from '#/features/merchants/api'

export const Route = createFileRoute('/_app/')({ component: Home })

function Home() {
	const { data: merchants } = useSuspenseQuery(merchantsQueryOptions)
	// A merchant switcher comes later; until then the first shop is the active one.
	const merchant = merchants.at(0)
	return (
		<div className='flex flex-col gap-2'>
			<h1 className='text-2xl font-semibold'>
				{merchant ? merchant.name : 'Welcome'}
			</h1>
			{merchant ? (
				<p className='text-muted-foreground text-sm'>
					Customers reach your shop at /m/{merchant.slug}
				</p>
			) : null}
		</div>
	)
}
