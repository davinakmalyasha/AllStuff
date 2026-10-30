// Static CSP/header gate for the Next.js app.
//
// The Vite app checked its <meta> CSP string; Next sets real headers in
// proxy.ts (per-request nonce for hydration scripts) plus static headers in
// vercel.json. A `script-src 'self'` with no nonce would break hydration, and
// two competing CSP headers (proxy + vercel.json) are enforced as an
// INTERSECTION, so vercel.json must not set script-src at all. This script
// asserts both invariants plus the map-tile origins, without booting a server
// (a live header assertion is covered by the E2E console gates).
//
// Self-test: node scripts/check-headers.mjs --self-test
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = join(here, '..')

function check(proxy, vercelRaw) {
  const errors = []
  for (const need of [
    'frame-ancestors',
    "'nonce-",
    'https://tiles.openstreetmap.org',
    'basemaps.cartocdn.com',
    'upgrade-insecure-requests',
  ]) {
    if (!proxy.includes(need)) errors.push(`proxy.ts CSP missing ${need}`)
  }
  if (/\bscript-src\b/.test(vercelRaw)) {
    errors.push('vercel.json must not set script-src (it would intersect the proxy nonce policy and break hydration)')
  }
  if (!/no-store/.test(vercelRaw)) errors.push('vercel.json lost the .map no-store rule')
  return errors
}

function run() {
  const proxy = readFileSync(join(root, 'proxy.ts'), 'utf8')
  const vercelRaw = readFileSync(join(root, 'vercel.json'), 'utf8')
  return check(proxy, vercelRaw)
}

if (process.argv.includes('--self-test')) {
  const goodProxy = "script-src 'self' 'nonce-abc' ... frame-ancestors 'none' ... https://tiles.openstreetmap.org ... basemaps.cartocdn.com ... upgrade-insecure-requests"
  const goodVercel = '{"headers":[{"source":"/(.*).map","headers":[{"key":"Cache-Control","value":"no-store"}]}]}'
  const cases = [
    [goodProxy, goodVercel, 0],
    [goodProxy.replace('frame-ancestors', 'x'), goodVercel, 1],
    [goodProxy, '{"headers":[]}', 2], // missing .map rule + ... (only no-store check fires: 1)
  ]
  let failed = 0
  const r0 = check(goodProxy, goodVercel)
  if (r0.length !== 0) { console.error('good case failed:', r0); failed++ }
  const r1 = check(goodProxy.replace('frame-ancestors', 'x'), goodVercel)
  if (r1.length !== 1) { console.error('bad-proxy case:', r1); failed++ }
  // vercel.json WITH script-src must fail
  const r2 = check(goodProxy, '{"headers":[{"headers":[{"key":"Content-Security-Policy","value":"script-src \'self\'"}]}]}')
  if (!r2.some((e) => e.includes('script-src'))) { console.error('vercel script-src case:', r2); failed++ }
  void cases
  if (failed > 0) process.exit(1)
  console.log('[check-headers] self-test ok')
} else {
  const errors = run()
  if (errors.length > 0) {
    for (const e of errors) console.error(`::error::${e}`)
    process.exit(1)
  }
  console.log('[check-headers] CSP/header invariants ok')
}
