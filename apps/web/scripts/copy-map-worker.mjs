// Copies the stock maplibre web worker into public/ so it is served as a
// static same-origin file.
//
// Why: maplibre derives its worker URL from its own script location, which
// breaks once a bundler (Next/Turbopack) inlines the library — the map then
// fails with "Worker failed to load" and renders an empty canvas. Pointing
// config.WORKER_URL at a static file (see BizMap.tsx) is the documented fix.
// The worker .mjs is self-contained (no relative imports), so one file suffices.
//
// Runs on postinstall (covers `npm ci` in CI and fresh clones). Also runnable
// by hand: `node scripts/copy-map-worker.mjs [--self-test]`.
import { copyFileSync, existsSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = join(here, '..')
const src = join(root, 'node_modules', 'maplibre-gl', 'dist', 'maplibre-gl-worker.mjs')
const dest = join(root, 'public', 'maplibre-worker.mjs')

function run() {
  if (!existsSync(src)) {
    throw new Error(`maplibre worker not found at ${src}; run npm install first`)
  }
  copyFileSync(src, dest)
  const kb = Math.round(statSync(dest).size / 1024)
  console.log(`[copy-map-worker] public/maplibre-worker.mjs (${kb} KB)`)
}

if (process.argv.includes('--self-test')) {
  // Self-test: copy to a temp name, verify non-trivial size, clean up.
  copyFileSync(src, dest + '.selftest')
  const size = statSync(dest + '.selftest').size
  const { unlinkSync } = await import('node:fs')
  unlinkSync(dest + '.selftest')
  if (size < 100_000) throw new Error(`worker suspiciously small: ${size} bytes`)
  console.log(`[copy-map-worker] self-test ok (${Math.round(size / 1024)} KB)`)
} else {
  run()
}
