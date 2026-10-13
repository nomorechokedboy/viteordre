import { useState } from 'react'
import { useForm } from '@tanstack/react-form'
import { useQueryClient } from '@tanstack/react-query'
import { z } from 'zod'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { ApiError } from '#/lib/api'
import { createMerchant, merchantsQueryOptions } from '#/features/merchants/api'
import type { Merchant } from '#/features/merchants/api'
import {
	SLUG_MAX,
	SLUG_MIN,
	SLUG_PATTERN,
	slugify
} from '#/features/merchants/slug'

// Checks the shape only, so mistakes show up as the user types. The server owns the real rules
// (reserved words, taken slugs, valid timezones) and its messages are shown as they come.
const schema = z.object({
	name: z
		.string()
		.trim()
		.min(1, 'Enter a name for your shop')
		.max(80, 'Use 80 characters or fewer'),
	slug: z
		.string()
		.min(SLUG_MIN, `Use at least ${SLUG_MIN} characters`)
		.max(SLUG_MAX, `Use ${SLUG_MAX} characters or fewer`)
		.regex(
			SLUG_PATTERN,
			'Use lowercase letters, digits and single hyphens only'
		),
	currency: z.string().regex(/^[A-Z]{3}$/, 'Pick a currency'),
	timezone: z.string().trim().min(1, 'Enter a timezone')
})

type Values = z.infer<typeof schema>
type FieldName = keyof Values

const CURRENCIES = ['VND', 'USD'] as const
const FIELD_NAMES: readonly FieldName[] = [
	'name',
	'slug',
	'currency',
	'timezone'
]

function isFieldName(value: string): value is FieldName {
	return (FIELD_NAMES as readonly string[]).includes(value)
}

function browserTimezone(): string {
	try {
		return Intl.DateTimeFormat().resolvedOptions().timeZone
	} catch {
		return 'Asia/Ho_Chi_Minh'
	}
}

function defaultValues(): Values {
	return {
		name: '',
		slug: '',
		currency: 'VND',
		timezone: browserTimezone()
	}
}

function timezoneOptions(): string[] {
	try {
		return Intl.supportedValuesOf('timeZone')
	} catch {
		return ['Asia/Ho_Chi_Minh']
	}
}

type ServerErrors = Partial<Record<FieldName, string>>

// Puts the API's answer where the user can act on it: field problems under their inputs, anything
// else (an owner limit, a server error) in a banner above the form.
function readApiError(error: ApiError): {
	fields: ServerErrors
	banner: string | null
} {
	const fields: ServerErrors = {}
	for (const f of error.fields) {
		if (isFieldName(f.field)) fields[f.field] ??= f.message
	}
	if (Object.keys(fields).length > 0) return { fields, banner: null }
	if (error.code === 'already_exists' && /slug/i.test(error.message)) {
		return {
			fields: { slug: 'This address is already taken, try another' },
			banner: null
		}
	}
	return { fields: {}, banner: error.message }
}

function issueMessages(errors: readonly unknown[]): string[] {
	return errors.flatMap((e) =>
		typeof e === 'string'
			? [e]
			: typeof e === 'object' && e !== null && 'message' in e
				? [String(e.message)]
				: []
	)
}

/** The "Create your shop" step: POST /merchants, then the checklist moves on by itself. */
export function MerchantStep() {
	const queryClient = useQueryClient()
	const [slugEdited, setSlugEdited] = useState(false)
	const [serverErrors, setServerErrors] = useState<ServerErrors>({})
	const [banner, setBanner] = useState<string | null>(null)

	const form = useForm({
		defaultValues: defaultValues(),
		validators: { onChange: schema, onSubmit: schema },
		onSubmit: async ({ value }) => {
			setServerErrors({})
			setBanner(null)
			try {
				const created = await createMerchant({
					name: value.name.trim(),
					slug: value.slug,
					currency: value.currency,
					timezone: value.timezone.trim()
				})
				// Put the new merchant in the cache first, so the checklist sees the step as done
				// right away instead of waiting for a refetch.
				queryClient.setQueryData<Merchant[]>(
					merchantsQueryOptions.queryKey,
					(old) => [...(old ?? []), created]
				)
				void queryClient.invalidateQueries({
					queryKey: merchantsQueryOptions.queryKey
				})
			} catch (e) {
				if (e instanceof ApiError) {
					const { fields, banner: b } = readApiError(e)
					setServerErrors(fields)
					setBanner(b)
				} else {
					setBanner(
						'Could not reach the server. Check your connection and try again.'
					)
				}
			}
		}
	})

	function clearServerError(name: FieldName) {
		setServerErrors((prev) => {
			if (!prev[name]) return prev
			const next = { ...prev }
			delete next[name]
			return next
		})
	}

	return (
		<form
			className='flex flex-col gap-5'
			noValidate
			onSubmit={(e) => {
				e.preventDefault()
				e.stopPropagation()
				void form.handleSubmit()
			}}
		>
			{banner ? (
				<p
					role='alert'
					className='border-destructive/40 bg-destructive/10 text-destructive rounded-md border px-3 py-2 text-sm'
				>
					{banner}
				</p>
			) : null}

			<form.Field name='name'>
				{(field) => {
					const shown = [
						...(serverErrors.name ? [serverErrors.name] : []),
						...(field.state.meta.isTouched ||
						form.state.submissionAttempts > 0
							? issueMessages(field.state.meta.errors)
							: [])
					]
					return (
						<div className='flex flex-col gap-2'>
							<Label htmlFor='merchant-name'>Shop name</Label>
							<Input
								id='merchant-name'
								name='name'
								autoComplete='organization'
								placeholder='Phở Hà Nội'
								value={field.state.value}
								aria-invalid={shown.length > 0}
								onBlur={field.handleBlur}
								onChange={(e) => {
									const name = e.target.value
									field.handleChange(name)
									clearServerError('name')
									if (!slugEdited) {
										form.setFieldValue(
											'slug',
											slugify(name)
										)
										clearServerError('slug')
									}
								}}
							/>
							<FieldErrors
								id='merchant-name-error'
								errors={shown}
							/>
						</div>
					)
				}}
			</form.Field>

			<form.Field name='slug'>
				{(field) => {
					const shown = [
						...(serverErrors.slug ? [serverErrors.slug] : []),
						...(field.state.meta.isTouched ||
						form.state.submissionAttempts > 0
							? issueMessages(field.state.meta.errors)
							: [])
					]
					return (
						<div className='flex flex-col gap-2'>
							<Label htmlFor='merchant-slug'>Shop address</Label>
							<div className='flex items-center gap-2'>
								<span className='text-muted-foreground text-sm'>
									/m/
								</span>
								<Input
									id='merchant-slug'
									name='slug'
									autoCapitalize='none'
									autoCorrect='off'
									spellCheck={false}
									placeholder='pho-ha-noi'
									value={field.state.value}
									aria-invalid={shown.length > 0}
									onBlur={field.handleBlur}
									onChange={(e) => {
										setSlugEdited(true)
										field.handleChange(
											e.target.value.toLowerCase()
										)
										clearServerError('slug')
									}}
								/>
							</div>
							<p className='text-muted-foreground text-xs'>
								Customers open this address after scanning a
								table&apos;s QR code. It is part of every QR
								code you print, so pick it carefully.
							</p>
							<FieldErrors
								id='merchant-slug-error'
								errors={shown}
							/>
						</div>
					)
				}}
			</form.Field>

			<div className='grid gap-5 sm:grid-cols-2'>
				<form.Field name='currency'>
					{(field) => {
						const shown = [
							...(serverErrors.currency
								? [serverErrors.currency]
								: []),
							...issueMessages(field.state.meta.errors)
						]
						return (
							<div className='flex flex-col gap-2'>
								<Label htmlFor='merchant-currency'>
									Currency
								</Label>
								<select
									id='merchant-currency'
									name='currency'
									className='border-input dark:bg-input/30 focus-visible:border-ring focus-visible:ring-ring/50 h-9 w-full rounded-md border bg-transparent px-3 text-sm shadow-xs outline-none focus-visible:ring-[3px]'
									value={field.state.value}
									onChange={(e) => {
										field.handleChange(e.target.value)
										clearServerError('currency')
									}}
								>
									{CURRENCIES.map((c) => (
										<option key={c} value={c}>
											{c}
										</option>
									))}
								</select>
								<FieldErrors
									id='merchant-currency-error'
									errors={shown}
								/>
							</div>
						)
					}}
				</form.Field>

				<form.Field name='timezone'>
					{(field) => {
						const shown = [
							...(serverErrors.timezone
								? [serverErrors.timezone]
								: []),
							...issueMessages(field.state.meta.errors)
						]
						return (
							<div className='flex flex-col gap-2'>
								<Label htmlFor='merchant-timezone'>
									Timezone
								</Label>
								<Input
									id='merchant-timezone'
									name='timezone'
									list='merchant-timezones'
									value={field.state.value}
									aria-invalid={shown.length > 0}
									onBlur={field.handleBlur}
									onChange={(e) => {
										field.handleChange(e.target.value)
										clearServerError('timezone')
									}}
								/>
								<datalist id='merchant-timezones'>
									{timezoneOptions().map((tz) => (
										<option key={tz} value={tz} />
									))}
								</datalist>
								<FieldErrors
									id='merchant-timezone-error'
									errors={shown}
								/>
							</div>
						)
					}}
				</form.Field>
			</div>

			<form.Subscribe selector={(s) => s.isSubmitting}>
				{(submitting) => (
					<Button
						type='submit'
						disabled={submitting}
						className='self-start'
					>
						{submitting ? 'Creating…' : 'Create shop'}
					</Button>
				)}
			</form.Subscribe>
		</form>
	)
}

function FieldErrors({
	id,
	errors
}: {
	id: string
	errors: readonly string[]
}) {
	if (errors.length === 0) return null
	return (
		<p id={id} role='alert' className='text-destructive text-sm'>
			{errors[0]}
		</p>
	)
}
