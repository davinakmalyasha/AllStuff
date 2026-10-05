// Guards the CSP in index.html against the three mistakes it has actually made.
//
// The policy shipped with three defects that no type checker, linter or build step
// could see, because a <meta> CSP is just a string in an HTML file:
//
//   1. `frame-ancestors 'none'` was present and did NOTHING. The spec ignores
//      frame-ancestors, report-uri and sandbox when CSP arrives via <meta>, so the
//      only clickjacking directive on the page was inert. Worse than absent: it
//      reads as protection.
//   2. `img-src` listed `/api/v1/media`. That is a path, not an origin, so it is
//      not a valid source expression. The browser dropped it and logged an error.
//   3. The theme FOUC guard was an inline <script> while script-src was 'self',
//      so the browser blocked it - the guard never ran, and dark-mode users got a
//      white flash on every single load. A real bug, shipped, invisible to CI.
//
// All three are now structurally prevented rather than merely fixed:
//
//   - frame-ancestors must NOT appear in the meta. It must be a real header; the
//     API's withSecurityHeaders sends it, and there is a Go test for that.
//   - No path-like source may appear in any directive. Paths only look plausible
//     next to the same-origin URLs people legitimately need, which is exactly how
//     it got there.
//   - No inline <script> may exist unless its sha256 is listed in script-src.
//     The theme guard moved to /theme-init.js instead, so there is no hash to
//     rot, but the check still permits a correctly-hashed inline script so the
//     escape hatch stays honest rather than being quietly reintroduced.
//
// Run: node scripts/check-csp.mjs   (wired into `npm run verify` and CI)

import { readFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
// Optional path argument so the check can be pointed at a fixture. Used by the
// negative tests: a guard that has only ever been seen passing is not known to
// detect anything.
const target = process.argv[2] ?? join(root, 'index.html')
const raw = readFileSync(target, 'utf8')

// Comments are stripped before anything is scanned, and that is not cosmetic.
//
// Without it this script scans the prose in index.html that explains WHY
// frame-ancestors is absent, and that prose contains the literal text
// "<script>" - which is then reported as an unhashed inline script. The check
// fails on the documentation of its own rule.
//
// Worse, the failure is not merely noisy: a commented-out CSP would satisfy the
// meta lookup, so removing the live policy and leaving it commented out would
// pass this check while shipping no policy at all. Comments are not markup.
const html = raw.replace(/<!--[\s\S]*?-->/g, '')

const problems = []

const meta = html.match(
  /<meta[^>]*http-equiv=["']Content-Security-Policy["'][^>]*>/i,
)
if (!meta) {
  console.error('csp: FAIL - index.html has no Content-Security-Policy <meta>')
  process.exit(1)
}

// Attribute order is not guaranteed, so read the content= out of the tag rather
// than assuming it follows http-equiv.
//
// The two forms are matched separately on purpose. A CSP is full of single
// quotes - 'self', 'unsafe-inline', 'sha256-...' - so a combined character class
// like /content=["']([^"']*)["']/ captures only up to the first apostrophe and
// yields the policy "default-src". Every directive check then silently passes on
// a policy containing exactly one directive, which is precisely the failure this
// script exists to catch.
const dq = meta[0].match(/\bcontent="([^"]*)"/)
const sq = meta[0].match(/\bcontent='([^']*)'/)
const policy = dq?.[1] ?? sq?.[1]
if (!policy) {
  console.error('csp: FAIL - the CSP <meta> has no content attribute')
  process.exit(1)
}

const directives = new Map()
for (const part of policy.split(';')) {
  const bits = part.trim().split(/\s+/)
  if (!bits[0]) continue
  directives.set(bits[0].toLowerCase(), bits.slice(1))
}

// 1. frame-ancestors is ignored in a <meta>, so it must not be here.
if (directives.has('frame-ancestors')) {
  problems.push(
    "frame-ancestors is ignored when CSP is delivered via <meta>. It reads as " +
      'clickjacking protection while providing none. Send it as an HTTP header ' +
      '(the API does) and keep it out of this policy.',
  )
}

// 2. No directive may contain a path. Paths are not origins, so they are always
//    invalid, but they sit naturally beside the same-origin URLs a policy really
//    does need, which is how '/api/v1/media' ended up in img-src unnoticed.
for (const [name, sources] of directives) {
  for (const source of sources) {
    // A host with a path component is still an origin expression and is fine.
    // A bare path - no scheme, no host - is the mistake.
    if (source.startsWith('/') && !source.startsWith('//')) {
      problems.push(
        `${name} lists '${source}', which is a path, not an origin. Source ` +
          "expressions are origins, 'self', 'nonce-...', 'sha256-...' or " +
          "keywords. Same-origin paths are already covered by 'self'.",
      )
    }
  }
}

// 3. Every inline script must be hashed in script-src.
const scriptSrc = directives.get('script-src') ?? []
if (scriptSrc.includes("'unsafe-inline'")) {
  problems.push(
    "script-src contains 'unsafe-inline', which disables CSP as a defence " +
      'against script injection - the one thing it is here for.',
  )
}

const inline = [...html.matchAll(/<script(?![^>]*\bsrc=)[^>]*>([\s\S]*?)<\/script>/gi)]
for (const [, body] of inline) {
  const hash = createHash('sha256').update(body, 'utf8').digest('base64')
  const token = `'sha256-${hash}'`
  if (!scriptSrc.includes(token)) {
    problems.push(
      `index.html has an inline <script> whose sha256 (${token}) is not listed ` +
        "in script-src, so the browser blocks it. Either add the hash or move " +
        'the script into public/ and load it from self.',
    )
  }
}

// style-src legitimately keeps 'unsafe-inline': Tailwind writes inline style
// attributes, which are governed by style-src-attr and cannot be hashed per
// element. Anything else that would need an allowance should fail here.
if (!directives.has('default-src')) {
  problems.push("default-src is missing, so unlisted directive types fall back to 'unsafe-allow-all'.")
}
if (!directives.has('object-src')) {
  problems.push(
    'object-src is missing. Without it <object>/<embed> fall back to default-src, ' +
      'which is fine today, but stating it keeps plugin content dead if the ' +
      'default is ever relaxed.',
  )
}

if (problems.length) {
  console.error(`csp: FAIL - ${problems.length} problem(s) in index.html`)
  for (const p of problems) console.error(`  - ${p}`)
  process.exit(1)
}

const rendered = [...directives.keys()].join(', ')
console.log(`csp: ok - ${directives.size} directives (${rendered}); ${inline.length} inline script(s), all hashed`)