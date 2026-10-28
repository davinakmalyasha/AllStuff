// Substitutes the API origin into vercel.json rewrite destinations at deploy
// time. The committed file points at https://api.example.com ON PURPOSE: it is
// a template, and deploying it verbatim would silently proxy the app to someone
// else's API (the original value pointed at a real third-party host, so this
// is not hypothetical).
//
// Usage: node scripts/inject-api-origin.mjs <origin> [--file vercel.json]
//   origin: https://api.example.com-style URL or bare host (scheme defaults
//           to https; ws:// destinations keep their scheme).
// Exit non-zero when the origin is missing, malformed, or the output would
// still contain the placeholder - so the deploy workflow fails BEFORE
// `vercel build` rather than shipping a proxy to nowhere.
//   node scripts/inject-api-origin.mjs --self-test   (negative cases)

import { readFileSync, writeFileSync, copyFileSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'

export const PLACEHOLDER = 'api.example.com'

export function normalizeOrigin(raw) {
  const trimmed = (raw ?? '').trim().replace(/\/+$/, '')
  if (!trimmed) throw new Error('API origin is empty')
  // Accept a full URL or a bare host; only the host is substituted, so a
  // path or scheme in the input can never leak into a rewrite destination.
  const host = trimmed
    .replace(/^(https?|wss?):\/\//, '')
    .split('/')[0]
  if (!/^[A-Za-z0-9.-]+\.[A-Za-z]{2,}(:\d+)?$/.test(host)) {
    throw new Error(`API origin does not look like a host: ${JSON.stringify(raw)}`)
  }
  return host
}

// Replaces only the placeholder host, preserving each destination's scheme:
// https:// stays https, wss:// stays wss. A blind string replace of the full
// URL would need to know every scheme in the file; replacing the host cannot
// break one.
export function injectApiOrigin(json, host) {
  if (!json.includes(PLACEHOLDER)) {
    throw new Error('vercel.json no longer contains the api.example.com template marker')
  }
  const out = json.replaceAll(PLACEHOLDER, host)
  if (out.includes(PLACEHOLDER)) {
    throw new Error('substitution incomplete: placeholder survives')
  }
  return out
}

function main() {
  const args = process.argv.slice(2)
  if (args.includes('--self-test')) {
    selfTest()
    return
  }
  const file = args.includes('--file') ? args[args.indexOf('--file') + 1] : 'vercel.json'
  const origin = args.find((a) => !a.startsWith('--') && a !== file)
  const host = normalizeOrigin(origin)
  const raw = readFileSync(file, 'utf8')
  const before = raw.split(PLACEHOLDER).length - 1
  writeFileSync(file, injectApiOrigin(raw, host))
  console.log(`inject-api-origin: ok - ${before} destinations now point at ${host}`)
}

function selfTest() {
  // Unit cases for the pure functions.
  assert.equal(normalizeOrigin('https://api.bizverse.example/'), 'api.bizverse.example')
  assert.equal(normalizeOrigin('api.bizverse.example'), 'api.bizverse.example')
  assert.equal(normalizeOrigin('wss://api.bizverse.example/x'), 'api.bizverse.example')
  assert.throws(() => normalizeOrigin(''), /empty/)
  assert.throws(() => normalizeOrigin('not a host!!'), /host/)
  assert.throws(() => normalizeOrigin('https:///path-only'), /host/)
  const sample = '{"destination": "https://api.example.com/api/:path*"}'
  assert.equal(
    injectApiOrigin(sample, 'api.bizverse.example'),
    '{"destination": "https://api.bizverse.example/api/:path*"}',
  )
  const wss = '{"destination": "wss://api.example.com/api/v1/ws"}'
  assert.equal(
    injectApiOrigin(wss, 'api.bizverse.example'),
    '{"destination": "wss://api.bizverse.example/api/v1/ws"}',
  )
  assert.throws(() => injectApiOrigin('{"a":1}', 'x.example'), /template marker/)

  // End to end on a temp copy: substitution lands, placeholder gone.
  const dir = mkdtempSync(join(tmpdir(), 'vercel-'))
  try {
    const root = join(dirname(fileURLToPath(import.meta.url)), '..')
    const tpl = join(dir, 'vercel.json')
    copyFileSync(join(root, 'vercel.json'), tpl)
    execFileSync(process.execPath, [fileURLToPath(import.meta.url), 'https://api.bizverse.example', '--file', tpl], { stdio: 'pipe' })
    const out = readFileSync(tpl, 'utf8')
    assert.doesNotMatch(out, /api\.example\.com/)
    assert.match(out, /https:\/\/api\.bizverse\.example\/api\/:path\*/)
    assert.match(out, /wss:\/\/api\.bizverse\.example\/api\/v1\/ws/)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }

  // Missing origin must fail, not silently keep the template.
  try {
    execFileSync(process.execPath, [fileURLToPath(import.meta.url)], { stdio: 'pipe' })
    assert.fail('missing origin exited 0')
  } catch (e) {
    assert.notEqual(e.status, 0, 'missing origin must fail')
  }
  console.log('inject-api-origin: ok - units, template run, and missing-origin failure')
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  main()
}
