// Prerender public routes for SEO (B4): renders each public page with a
// headless browser after `npm run build`, writing static HTML per route.
// Usage: node scripts/prerender.mjs  (expects API on :8080, dist built)
import { chromium } from '@playwright/test'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const API = 'http://localhost:8080/api/v1'
const APP = 'http://localhost:5173'

const routes = ['/', '/discover', '/categories', '/map', '/compare']

async function main() {
  const [cats, biz, cities] = await Promise.all([
    fetch(`${API}/categories`).then((r) => r.json()),
    fetch(`${API}/sitemap.xml`).then((r) => r.text()),
    fetch(`${API}/cities`).then((r) => r.json()),
  ])
  for (const c of cats.categories ?? []) routes.push(`/c/${c.slug}`)
  for (const city of cities.cities ?? []) routes.push(`/city/${city.slug}`)
  // business slugs from sitemap
  const slugs = [...biz.matchAll(/\/b\/([a-z0-9-]+)/g)].map((m) => m[1])
  for (const slug of new Set(slugs)) routes.push(`/b/${slug}`)

  const browser = await chromium.launch()
  const page = await browser.newPage()
  let count = 0
  for (const route of routes) {
    try {
      await page.goto(`${APP}${route}`, { waitUntil: 'networkidle', timeout: 20000 })
      await page.waitForTimeout(300)
      const html = await page.content()
      const file = route === '/' ? 'index' : route.replace(/^\//, '').replace(/\//g, '__').replace(/[^a-z0-9_]/gi, '_')
      const dir = join('dist', 'prerendered')
      mkdirSync(dir, { recursive: true })
      writeFileSync(join(dir, `${file}.html`), html)
      count++
      console.log(`prerendered /${file}`)
    } catch (e) {
      console.warn(`skipped ${route}: ${e.message}`)
    }
  }
  await browser.close()
  console.log(`done: ${count}/${routes.length} routes`)
}

main().catch((e) => {
  console.error(e)
  process.exit(1)
})
