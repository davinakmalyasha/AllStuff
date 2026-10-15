import { test, expect, type Page } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'
import { register, uniqueSuffix } from './fixtures'

/**
 * Real accessibility testing, replacing an attempt that could not work.
 *
 * A regex pass over the JSX reported 188 unlabelled form controls, then 16
 * "placeholder-only" ones, then zero - because it cannot see component contracts.
 * `<Input label="Business name" …>` renders `useId()` plus `<label htmlFor={id}>`, and
 * a scan of the `<input>` tag alone has no way to know that. It also flagged an `<img
 * onerror>` that existed only inside a comment describing an already-fixed XSS.
 *
 * axe-core runs against the RENDERED accessibility tree, which is the only place any
 * of those questions can actually be answered. It has already found two defects no
 * amount of reading could have: `ink-3` failing AA contrast app-wide, and
 * `text-accent-ink` being a no-op class that left every primary button dark-on-dark.
 *
 * Scoping, because a gate that fails on everything gets switched off:
 *
 *   - Only `serious` and `critical` impact fails the build. Those are the findings
 *     that stop someone using the product. `minor` and `moderate` are printed so they
 *     are visible and can be worked through, without blocking unrelated work.
 *   - `best-practice` rules are excluded. They are the maintainers' opinions rather
 *     than WCAG requirements, and mixing the two makes a failure ambiguous: it stops
 *     being clear whether the product has a barrier or a style disagreement.
 */

const BLOCKING = new Set(['serious', 'critical'])

type Target = {
  name: string
  path: string
  /** Emulate the stored dark preference before load. */
  dark?: boolean
  /**
   * Text that must be visible before the audit runs.
   *
   * For pages whose content depends on seeded data. A 404 shell is perfectly
   * accessible, so without this a renamed seed slug would audit the error page and
   * report success - the failure mode this suite exists to catch, arriving through the
   * back door.
   */
  expectText?: RegExp
}

// The public surface, in BOTH themes.
//
// Derived rather than listed twice. The first version wrote light and dark entries by
// hand, so a path added to one list silently skipped the other; expanding them from one
// array makes that unrepresentable.
//
// Dark mode is a separate colour space - `accent` and `ink` invert together - so a
// token that passes in light mode can fail in dark.
const PUBLIC_PATHS: Target[] = [
  { name: 'landing', path: '/' },
  { name: 'login', path: '/login' },
  { name: 'register', path: '/register' },
  { name: 'discover', path: '/discover' },
  { name: 'categories', path: '/categories' },
  // The storefront was the largest gap of all: it is THE product page, and it holds
  // the engagement controls, the hours table, the catalogue and the review list.
  { name: 'business storefront', path: '/b/rumah-kopi-senja', expectText: /Rumah Kopi Senja/ },
  { name: 'category page', path: '/c/cafe', expectText: /Café/ },
  { name: 'public profile', path: '/u/demoowner', expectText: /Demo Owner/ },
  { name: 'city page', path: '/city/jakarta', expectText: /Jakarta/ },
  { name: 'leaderboards', path: '/leaderboards' },
  { name: 'for business', path: '/for-business' },
  { name: 'pricing', path: '/pricing' },
  { name: 'help', path: '/help' },
  // Legal pages are a routine source of contrast and heading-order failures, and they
  // are reached by every user at least once.
  { name: 'terms', path: '/terms' },
  { name: 'privacy', path: '/privacy' },
]

const PUBLIC: Target[] = PUBLIC_PATHS.flatMap((t) => [t, { ...t, dark: true }])

// Signed-in surfaces, which hold the densest interactive controls in the product and
// were entirely unaudited.
//
// Scoped to the account-scoped pages on purpose: the pages under /dashboard/* require
// an active business, so they need a business fixture on top of the account one. These
// need only an account, which keeps the suite's cost proportionate to what it checks.
// Account-scoped and dashboard pages.
//
// `/dashboard` renders an empty state for an account with no businesses, so it needs
// only a registered user.
//
// `/dashboard/register` is the business wizard, and it CANNOT reach its wizard state
// here: it creates a draft business on mount, POST /businesses requires a VERIFIED
// email, and the E2E suite cannot produce one - registration sends a verification link
// and nothing in the test environment can read it, since CI starts no SMTP sink and
// there is no test-only verify path.
//
// So what gets audited there is the wizard's FAILURE state, and the expectText below
// asserts it is the error rather than a spinner. That makes this the regression guard
// for a real bug: the failure message was set but the render guard returned the spinner
// first, so an unverified user sat on "Starting your storefront draft…" forever with no
// explanation. If that guard regresses, this line times out.
//
// The rest of the dashboard - storefront, products, settings, analytics, billing - has
// the same verified-email blocker, which is why those pages are absent rather than
// listed and silently auditing an empty state.
const SIGNED_IN: Target[] = [
  { name: '/me', path: '/me' },
  { name: '/me/collections', path: '/me/collections' },
  { name: '/me/notifications', path: '/me/notifications' },
  { name: '/me/security', path: '/me/security' },
  { name: '/dashboard', path: '/dashboard', expectText: /No businesses yet/ },
  { name: '/dashboard/register', path: '/dashboard/register', expectText: /Verify your email/ },
]

/**
 * Runs axe and asserts. Failures are the assertion.
 *
 * `reducedMotion` is emulated because the first run measured elements mid-animation - a
 * `mono-label` reporting #929297 is exactly #6e6e75 blended 75% toward white, which no
 * reader ever sees as a resting state. The app honours prefers-reduced-motion, so this
 * is deterministic rather than a sleep.
 */
async function audit(page: Page, name: string): Promise<void> {
  await page.emulateMedia({ reducedMotion: 'reduce' })

  // Wait for the SPA to render. `networkidle` is unusable: the app holds a WebSocket
  // open, so the network never goes idle and the wait would time out rather than report
  // anything about accessibility.
  await page.waitForFunction(() => {
    const root = document.getElementById('root')
    return !!root && root.children.length > 0
  })

  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
    .analyze()

  const blocking = results.violations.filter((v) => BLOCKING.has(v.impact ?? ''))
  const advisory = results.violations.filter((v) => !BLOCKING.has(v.impact ?? ''))

  // Printed even when empty, so a run with no findings is distinguishable from a run
  // where the scan silently did nothing.
  console.log(
    `[a11y] ${name}: ${results.violations.length} violation type(s), ` +
      `${results.passes.length} rule(s) passed; blocking=${blocking.length}`,
  )
  for (const v of advisory) {
    console.log(`[a11y] ${name} advisory (${v.impact}): ${v.id} - ${v.help} [${v.nodes.length} node(s)]`)
  }

  // Structured data rather than a bare length, so the failure names the rule, its
  // impact and how many nodes are affected. Each node carries axe's own colour maths -
  // the foreground it measured, the background, and the resulting ratio - because a
  // failure that says only "colour contrast" leaves the reader guessing which pair is
  // wrong, and guessing is how a rule ends up disabled instead of a colour fixed.
  expect(
    blocking.map((v) => ({
      id: v.id,
      impact: v.impact,
      help: v.help,
      nodes: v.nodes.length,
      worst: v.nodes.slice(0, 3).map((n) => {
        const d = n.any[0]?.data as
          | { fgColor?: string; bgColor?: string; contrastRatio?: number; expectedContrastRatio?: string }
          | undefined
        return {
          target: n.target.join(' '),
          fg: d?.fgColor,
          bg: d?.bgColor,
          ratio: d?.contrastRatio,
          need: d?.expectedContrastRatio,
          html: n.html.slice(0, 120),
        }
      }),
    })),
    `${name}: ${blocking.length} serious/critical accessibility violation(s)`,
  ).toEqual([])
}

for (const target of PUBLIC) {
  const label = target.dark ? `${target.name} (dark)` : target.name
  test(`${label} has no serious accessibility violations`, async ({ page }) => {
    if (target.dark) {
      // The FOUC guard reads this key before first paint, so the page renders dark from
      // the start rather than flashing light and being audited mid-toggle.
      await page.addInitScript(() => localStorage.setItem('bv.theme', 'dark'))
    }
    await page.goto(target.path)
    if (target.expectText) {
      // Proves the real page rendered. Without it a seed rename would turn this into an
      // audit of the 404 page, which passes and means nothing.
      await expect(page.getByText(target.expectText).first()).toBeVisible()
    }
    await audit(page, label)
  })
}

// ONE registration per theme, not one per page.
//
// The first version registered an account for each of the six signed-in audits, and the
// sixth failed with the browser still sitting on /register - registration itself was
// being refused. The suite had gone from a handful of accounts to a couple of dozen
// across all specs, and the auth tiers are per-IP because every request reaches the API
// through the Vite proxy and shares one address.
//
// Auditing four paths per account is also simply cheaper, and the session is
// established once.
for (const dark of [false, true]) {
  const suffix = dark ? ' (dark)' : ''
  test(`signed-in surfaces${suffix} have no serious accessibility violations`, async ({ page }) => {
    if (dark) {
      await page.addInitScript(() => localStorage.setItem('bv.theme', 'dark'))
    }
    const uid = uniqueSuffix()
    await register(page, {
      email: `a11y-${uid}@example.com`,
      username: `a11y${uid}`,
      password: 'e2e-password-1234',
    })
    // The fixture submits the form and returns without waiting; without this the audit
    // would run against the login page and report on the wrong surface.
    await expect(page).toHaveURL(/\/me$/)

    // Audited in sequence in one test rather than as four. A failure stops the test at
    // the offending path, so the console line naming it arrives first and the paths
    // after it are not reached - a deliberate trade for two registrations instead of
    // eight. Every audit prints which path it covered, so the output is never ambiguous
    // about how far it got.
    for (const target of SIGNED_IN) {
      await page.goto(target.path)
      if (target.expectText) {
        // Proves the real page rendered rather than an empty or redirected state.
        await expect(page.getByText(target.expectText).first()).toBeVisible()
      }
      await audit(page, `${target.name}${suffix}`)
    }
  })
}
