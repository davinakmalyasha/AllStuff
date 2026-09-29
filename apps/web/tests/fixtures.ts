/**
 * Shared E2E fixtures.
 *
 * The previous specs hardcoded two business UUIDs from scripts/seed.sql and
 * asserted on hardcoded business names. That combination fails in a way that
 * looks like a pass: change a UUID in the seed and `/compare?b=...` renders an
 * empty state, `getByText('Rumah Kopi Senja')` finds nothing, and depending on
 * ordering the test can still go green against a 404 page. Resolving the ids
 * from the API means a seed change surfaces as a clear "no businesses to
 * compare" error instead.
 */
import { expect, type APIRequestContext, type Page } from '@playwright/test'

/** A value that is unique per run, safe in an email local part, and readable. */
export function uniqueSuffix(): string {
  return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
}

/**
 * A unique account, created through the real registration path.
 *
 * Uses the visible form fields rather than CSS ids where possible: the two
 * `#field-password` / `#field-confirm-password` ids in the earlier version meant
 * a rename in RegisterPage silently killed the test.
 */
export async function register(page: Page, opts: { email: string; username: string; password: string }) {
  await page.goto('/register')
  await page.getByLabel(/email/i).fill(opts.email)
  await page.getByLabel(/^(your )?name/i).fill('E2E Owner')
  await page.getByLabel(/username/i).fill(opts.username)
  // Two password fields, addressed by their order within the form rather than
  // by id.
  const passwords = page.locator('input[type=password]')
  await passwords.nth(0).fill(opts.password)
  await passwords.nth(1).fill(opts.password)
  await page.getByRole('button', { name: /sign up|create account/i }).click()
  return page
}

/** Resolve the seeded businesses by slug, via the public API. */
export async function seededBusinesses(request: APIRequestContext) {
  const res = await request.get('/api/v1/search?limit=50')
  expect(res.ok(), `search returned ${res.status()}`).toBeTruthy()
  const body = (await res.json()) as { businesses?: { id: string; slug: string; name: string }[] }
  const list = body.businesses ?? []
  expect(list.length, 'no businesses seeded — run: psql "$DATABASE_URL" -f scripts/seed.sql').toBeGreaterThan(1)
  return list
}

/** The first two seeded businesses, for the compare journey. */
export async function twoSeededBusinesses(request: APIRequestContext) {
  const [a, b] = await seededBusinesses(request)
  return { a, b }
}

/** A seeded business whose slug contains the given substring. */
export async function seededBusinessBySlug(request: APIRequestContext, slugPart: string) {
  const all = await seededBusinesses(request)
  const found = all.find((b) => b.slug.includes(slugPart))
  expect(found, `no seeded business with slug containing "${slugPart}"`).toBeTruthy()
  return found!
}
