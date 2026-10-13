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
  /** Create an account and sign in first. */
  signedIn?: boolean
}

// The public surface, in BOTH themes.
//
// Dark mode is a separate colour space: `accent` and `ink` invert together, so a token
// that passes in light mode can fail in dark. The earlier contrast fix was computed
// against the dark surfaces and never measured, which is exactly the gap this closes.
const PUBLIC: Target[] = [
  { name: 'landing', path: '/' },
  { name: 'login', path: '/login' },
  { name: 'register', path: '/register' },
  { name: 'discover', path: '/discover' },
  { name: 'categories', path: '/categories' },
  { name: 'landing', path: '/', dark: true },
  { name: 'login', path: '/login', dark: true },
  { name: 'register', path: '/register', dark: true },
  { name: 'discover', path: '/discover', dark: true },
  { name: 'categories', path: '/categories', dark: true },
]

// Signed-in surfaces, which hold the densest interactive controls in the product and
// were entirely unaudited.
//
// Scoped to the account-scoped pages on purpose: the pages under /dashboard/* require
// an active business, so they need a business fixture on top of the account one. These
// need only an account, which keeps the suite's cost proportionate to what it checks.
const SIGNED_IN: Target[] = [
  { name: 'me', path: '/me', signedIn: true },
  { name: 'me/collections', path: '/me/collections', signedIn: true },
  { name: 'me/notifications', path: '/me/notifications', signedIn: true },
  { name: 'me/security', path: '/me/security', signedIn: true },
  { name: 'me (dark)', path: '/me', signedIn: true, dark: true },
  { name: 'me/security (dark)', path: '/me/security', signedIn: true, dark: true },
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
    await audit(page, label)
  })
}

for (const target of SIGNED_IN) {
  const label = target.dark ? `${target.name} (dark)` : target.name
  test(`${label} has no serious accessibility violations`, async ({ page }) => {
    if (target.dark) {
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

    await page.goto(target.path)
    await audit(page, label)
  })
}
