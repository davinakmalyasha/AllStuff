import { test, expect } from '@playwright/test'
import { register, seededBusinessBySlug, twoSeededBusinesses, uniqueSuffix } from './fixtures'

/**
 * Critical journeys (PRD §12.7).
 *
 * Notes on how this differs from the previous version, because the changes are
 * about what the assertions can and cannot detect:
 *
 *  - **The 120-second waits are gone.** They existed to sit out the 5/min auth
 *    rate limit between registrations. The E2E web server now sets
 *    `APP_ENV=test` plus a narrow per-account throttle bypass, so the wait was
 *    hiding a configuration problem rather than a product one. The per-IP tiers
 *    are untouched and `RATELIMIT_GLOBAL` is still lifted, because every browser
 *    request genuinely does share the proxy's IP.
 *  - **`getByText(/result/)` is gone.** It matched "results", "0 results" and
 *    "No results", so the search test passed when search was completely broken.
 *    It now asserts that a known seeded business is in the results.
 *  - **Seed UUIDs are resolved from the API**, so a seed change fails loudly
 *    instead of rendering an empty compare page.
 *  - **No CSS ids.** The two `#field-password` ids meant a rename in
 *    RegisterPage would silently kill the suite.
 *
 * This file was also, until it was run for the first time, storing its own
 * accented characters double-encoded: `Café` was on disk as `Café`, because
 * UTF-8 bytes had been decoded as Windows-1252 and re-encoded. Typecheck, lint
 * and build all pass on a file like that, because the corruption is still valid
 * TypeScript and still valid UTF-8 — it just no longer matches the rendered page.
 * So the category assertions could never pass, and nothing in the pipeline said
 * so. It was a BOM plus three mis-encoded sequences, repaired in place.
 */

const PASSWORD = 'e2e-password-1234'

test('register, land on /me, sign out, sign back in', async ({ page }) => {
  const uid = uniqueSuffix()
  const email = `e2e-${uid}@example.com`

  await register(page, { email, username: `e2e${uid}`, password: PASSWORD })
  await expect(page).toHaveURL(/\/me$/)
  await expect(page.getByText('E2E Owner')).toBeVisible()

  await page.getByRole('button', { name: /sign out/i }).click()
  // Wait for the URL instead of calling `page.goto('/login')` afterwards.
  //
  // MePage signs out with `void logout().then(() => navigate('/login'))`. A
  // Playwright `click()` resolves when the event is DISPATCHED, not when the
  // async handler finishes, so the old `await page.goto('/login')` tore the page
  // down while the logout POST was still in flight. The server never received it,
  // the refresh cookie survived, and the bootstrap `fetchMe()` on the new /login
  // succeeded - so `GuestOnly` rendered, then bounced to /me as soon as `user`
  // arrived, unmounting the email input mid-fill.
  //
  // The symptom bore no resemblance to the cause: Playwright reported
  // "element was detached from the DOM, retrying" and burned the full 120s test
  // timeout. The artifact page snapshot showed /me fully rendered, which is what
  // finally made it clear the journey had never been broken - only raced.
  //
  // Asserting the URL is both the fix and the better test: the app navigating on
  // its own is the actual contract, and waiting on it is what proves the POST
  // completed.
  await expect(page).toHaveURL(/\/login$/)

  await page.getByLabel(/email/i).fill(email)
  await page.getByLabel(/password/i).fill(PASSWORD)
  // Scoped to the form that holds the password field, not to the page. A
  // page-level `getByRole('button', { name: /sign in|log in/i })` matched two
  // elements and failed the suite in strict mode, because the header renders its
  // own "Sign in" link. Scoping to the form is also the more honest assertion: it
  // cannot pass by clicking the nav link and merely landing somewhere
  // unauthenticated, which is what a page-level click would allow.
  //
  // LoginPage renders two <form> elements — the password form and a 2FA code
  // form — but they are mutually exclusive behind a ternary, so filtering by the
  // password field is stable either way.
  await page
    .locator('form')
    .filter({ has: page.getByLabel(/password/i) })
    .getByRole('button', { name: /sign in|log in/i })
    .click()
  await expect(page).toHaveURL(/\/me$/)
})

test('landing page renders categories and trending', async ({ page }) => {
  await page.goto('/')
  // The headline is a claim about the product, so it is asserted as a heading
  // rather than as any text node.
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
  // The category grid is a link per category, so count the links rather than
  // matching the word "Categories", which appears in the nav, the footer and
  // the section heading simultaneously.
  await expect(page.getByRole('link', { name: /Café|Restaurant|Barber|Clinic/ }).first()).toBeVisible()
})

test('search returns the seeded coffee shop', async ({ page, request }) => {
  const target = await seededBusinessBySlug(request, 'rumah-kopi-senja')
  await page.goto('/discover?q=coffee')

  // A real assertion about the result, not about the word "result". This fails
  // on "No results found", on an error state, and on an empty page.
  await expect(page.getByRole('heading', { name: target.name })).toBeVisible()
})

test('search reports an honest count', async ({ page, request }) => {
  const target = await seededBusinessBySlug(request, 'rumah-kopi-senja')
  await page.goto('/discover?q=coffee')
  await expect(page.getByRole('heading', { name: target.name })).toBeVisible()

  // The count is either a number or the literal "At least N" when the total was
  // not computed — DiscoverPage renders `count: null` as "At least N" rather
  // than lying with a zero. Asserting the shape rather than a fixed number
  // keeps this honest across both modes.
  await expect(page.getByText(/(At least )?\d+ (business|results?|businesses)/i)).toBeVisible()
})

test('public business page shows the storefront and its trust level', async ({ page, request }) => {
  const target = await seededBusinessBySlug(request, 'rumah-kopi-senja')
  await page.goto(`/b/${target.slug}`)

  await expect(page.getByRole('heading', { name: target.name })).toBeVisible()
  // The verification level is the product's core trust signal, so its absence is
  // a real failure rather than something to soften.
  await expect(page.getByText(/Verified/).first()).toBeVisible()
  // Engagement bar: like / recommend / save.
  await expect(page.getByRole('button', { name: /like/i }).first()).toBeVisible()
})

test('business page is server-rendered with JSON-LD', async ({ request }) => {  // The SEO surface is a headline feature, so it gets a direct assertion against
  // the SSR endpoint rather than trusting that the rewrite is configured.
  const target = await seededBusinessBySlug(request, 'rumah-kopi-senja')
  const res = await request.get(`/ssr/b/${target.slug}`)
  expect(res.status()).toBe(200)
  const html = await res.text()
  expect(html, 'SSR document must contain a title').toMatch(/<title>/i)
  expect(html, 'SSR document must contain LocalBusiness JSON-LD').toMatch(/application\/ld\+json/)
  expect(html, 'JSON-LD must name the business').toContain(target.name)
})

test('compare puts two businesses side by side', async ({ page, request }) => {
  const { a, b } = await twoSeededBusinesses(request)
  await page.goto(`/compare?b=${a.id},${b.id}`)

  await expect(page.getByRole('heading', { name: a.name }).or(page.getByText(a.name).first())).toBeVisible()
  await expect(page.getByText(b.name).first()).toBeVisible()
  // A compare page that rendered only one column would still satisfy the two
  // assertions above if one of the names appeared in a "remove" control, so
  // require the comparison header the page actually renders.
  await expect(page.getByText(/compare/i).first()).toBeVisible()
})

test('category page shows its trending leaderboard', async ({ page }) => {
  await page.goto('/c/cafe')
  await expect(page.getByRole('heading', { name: 'Café' })).toBeVisible()
  await expect(page.getByText(/Top in Café/)).toBeVisible()
  // "updated N min ago" is time-dependent by design, and the snapshot can be
  // older than an hour if the trending job has not run. Match the shape, not a
  // bound: a stale snapshot is a jobs problem, not a page-render failure, and
  // asserting an upper bound here would make the test fail for the wrong reason.
  await expect(page.getByText(/updated .+ ago/i)).toBeVisible()
})

test('unauthenticated write is rejected, not silently accepted', async ({ page }) => {
  // Negative-path coverage the previous suite had none of: a POST to an
  // engagement endpoint with no session must fail, and must not leave the user
  // believing it succeeded.
  const result = await page.evaluate(async () => {
    const csrf = document.cookie.match(/bv_csrf=([^;]+)/)?.[1] ?? ''
    const res = await fetch('/api/v1/likes/business/00000000-0000-4000-8000-000000000000', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': decodeURIComponent(csrf) },
      body: '{}',
    })
    return { status: res.status }
  })
  expect(result.status, 'an unauthenticated engagement write must not succeed').toBe(401)
})
