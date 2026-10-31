// Fail-loud guard for the API origin used by Next rewrites.
//
// The committed apps/web/vercel.json points every rewrite at
// https://api.example.com ON PURPOSE — it is a template, and deploying it
// verbatim would silently proxy the app to someone else's API. Next reads the
// real origin from the API_ORIGIN environment at build/deploy time
// (next.config.ts, proxy.ts), so unlike the old Vite setup there is no file
// to substitute — but the failure mode is identical: an unset variable ships
// a broken app. This script fails BEFORE `vercel build` when API_ORIGIN is
// missing or still the placeholder.
//
// API_ORIGIN must ALSO be set as a Vercel project environment variable so the
// remote build sees it; this check only validates the value handed to it.
//
// Usage: API_ORIGIN=https://api.example.com node scripts/check-api-origin.mjs
// Self-test: node scripts/check-api-origin.mjs --self-test
import { execFileSync } from 'node:child_process'

const PLACEHOLDER = 'https://api.example.com'

function check(value) {
  if (!value) return 'API_ORIGIN is missing or empty'
  if (value.includes('api.example.com')) return `API_ORIGIN still points at the placeholder (${value})`
  let u
  try {
    u = new URL(value)
  } catch {
    return `API_ORIGIN is not a URL: ${value}`
  }
  if (u.protocol !== 'https:' && u.hostname !== 'localhost' && !u.hostname.startsWith('127.')) {
    return `API_ORIGIN must be https (or localhost for dev): ${value}`
  }
  return null
}

if (process.argv.includes('--self-test')) {
  const cases = [
    ['', 'missing'],
    ['https://api.example.com/api/x', 'placeholder'],
    ['not-a-url', 'not a URL'],
    ['http://evil.example/api', 'https'],
    ['https://api.allstuff.app', null],
    ['http://localhost:8080', null],
  ]
  let failed = 0
  for (const [input, want] of cases) {
    const got = check(input)
    const ok = want === null ? got === null : got !== null
    if (!ok) {
      console.error(`self-test: check(${JSON.stringify(input)}) = ${JSON.stringify(got)}, want ${want === null ? 'ok' : 'an error'}`)
      failed++
    }
  }
  // Real substitution path is gone (env-based), but prove the script runs
  // from the repo root the way CI calls it.
  if (failed > 0) process.exit(1)
  console.log('[check-api-origin] self-test ok')
} else {
  const err = check(process.env.API_ORIGIN)
  if (err) {
    console.error(`::error::${err}. Set the API_ORIGIN repo variable (and the Vercel project env var) to the Go API origin.`)
    process.exit(1)
  }
  console.log(`[check-api-origin] API_ORIGIN ok (${process.env.API_ORIGIN})`)
}
