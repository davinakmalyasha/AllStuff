import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

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
 * of those questions can actually be answered.
 *
 * Scoping, because a gate that fails on everything gets switched off:
 *
 *   - Only `serious` and `critical` impact fails the build. Those are the findings
 *     that stop someone using the product. `minor` and `moderate` are printed so they
 *     are visible and can be worked through, but they do not block unrelated work.
 *   - `best-practice` rules are excluded. They are opinions from the axe maintainers
 *     rather than WCAG requirements, and mixing the two makes a failure ambiguous: it
 *     is not clear whether the product has a barrier or a style disagreement.
 *
 * The tag set is the WCAG A and AA success criteria, which is the level the PRD
 * implies by targeting a public directory.
 */

const PAGES = [
  { name: 'landing', path: '/' },
  { name: 'login', path: '/login' },
  { name: 'register', path: '/register' },
  { name: 'discover', path: '/discover' },
  { name: 'categories', path: '/categories' },
] as const

const BLOCKING = new Set(['serious', 'critical'])

// Audit the RESTING state.
//
// The first run measured several elements mid-animation: a `mono-label` reported
// fg #929297 on white, which is exactly #6e6e75 blended 75% toward white, and a button
// reported #515153 on #464647, the same 0.75 blend of ink over accent. Neither is a
// state a reader ever sees - the animation is a fade-up that finishes - so auditing it
// would have produced findings nobody could act on.
//
// The app honours prefers-reduced-motion (index.css), so emulating it settles the
// animations rather than needing a sleep, and it is deterministic rather than
// timing-dependent. Applied per page inside the test, since `test.use` does not accept
// this option in the installed Playwright version.

for (const p of PAGES) {
  test(`${p.name} has no serious accessibility violations`, async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.goto(p.path)

    // Wait for the SPA to render. `networkidle` is unusable here: the app holds a
    // WebSocket open, so the network never goes idle and the wait would time out
    // rather than report anything about accessibility.
    await page.waitForFunction(() => {
      const root = document.getElementById('root')
      return !!root && root.children.length > 0
    })

    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
      .analyze()

    const blocking = results.violations.filter((v) => BLOCKING.has(v.impact ?? ''))
    const advisory = results.violations.filter((v) => !BLOCKING.has(v.impact ?? ''))

    // Printed even when empty, so a run with no findings is distinguishable from a
    // run where the scan silently did nothing.
    console.log(
      `[a11y] ${p.name}: ${results.violations.length} violation type(s), ` +
        `${results.passes.length} rule(s) passed; blocking=${blocking.length}`,
    )
    for (const v of advisory) {
      console.log(`[a11y] ${p.name} advisory (${v.impact}): ${v.id} - ${v.help} [${v.nodes.length} node(s)]`)
    }

    // Asserted as structured data rather than a bare length, so the failure message
    // names the rule, its impact and how many nodes are affected.
    //
    // Each node carries axe's own colour math: the foreground it measured, the
    // background it measured, and the resulting ratio. Without those a failure says
    // "colour contrast" and leaves the reader to guess which pair is wrong - and
    // guessing is how a suite ends up with a rule disabled instead of a colour fixed.
    expect(
      blocking.map((v) => ({
        id: v.id,
        impact: v.impact,
        help: v.help,
        nodes: v.nodes.length,
        worst: v.nodes
          .slice(0, 3)
          .map((n) => {
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
      `${p.name}: ${blocking.length} serious/critical accessibility violation(s)`,
    ).toEqual([])
  })
}
