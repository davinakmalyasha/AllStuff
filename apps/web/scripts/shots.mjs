// One-off portfolio screenshot run (not part of the E2E suite).
// Usage (run from apps/web): vite dev on :5173 with
// VITE_API_TARGET=http://localhost:8090, API on :8090 against the seeded
// allstuff_demo database, then: node scripts/shots.mjs
import { chromium } from 'playwright';
import { mkdir } from 'node:fs/promises';

// Dismiss the PWA update toasts: they fire in dev because the freshly built
// service worker differs from the registered one, and they would cover the
// hero in every capture.
async function dismissToasts(page) {
  for (const b of await page.getByRole('button', { name: 'Dismiss' }).all()) {
    await b.click().catch(() => {});
  }
}

const shots = [
  { path: 'docs/demo/landing.png', route: '/', width: 1440, height: 900, fullPage: false },
  { path: 'docs/demo/storefront.png', route: '/b/rumah-kopi-senja', width: 1440, height: 900, fullPage: false },
  { path: 'docs/demo/leaderboards.png', route: '/leaderboards', width: 1440, height: 900, fullPage: false },
];

const BASE = process.env.E2E_BASE_URL ?? 'http://localhost:5173';
const browser = await chromium.launch();
try {
  await mkdir('docs/demo', { recursive: true });
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  for (const s of shots) {
    await page.setViewportSize({ width: s.width, height: s.height });
    await page.goto(BASE + s.route, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1500);
    await dismissToasts(page);
    await page.screenshot({ path: s.path, fullPage: s.fullPage });
    console.log('saved', s.path);
  }
  // Owner dashboard behind a real login.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(BASE + '/login', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1000);
  await page.getByLabel(/email/i).fill('demo@allstuff.test');
  await page.getByLabel(/password/i).fill('DemoPass123!');
  await page.locator('form button[type=submit]').click();
  await page.waitForURL('**/me', { timeout: 15000 });
  await page.goto(BASE + '/dashboard', { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  await dismissToasts(page);
  await page.screenshot({ path: 'docs/demo/dashboard.png', fullPage: false });
  console.log('saved docs/demo/dashboard.png');
  // OG card source: the landing hero at exactly 1200x630, logged OUT, in a
  // fresh context so no session header or toast leaks into the card.
  const og = await browser.newContext({ viewport: { width: 1200, height: 630 } }).then((c) => c.newPage());
  await og.goto(BASE + '/', { waitUntil: 'networkidle' });
  await og.waitForTimeout(1500);
  await dismissToasts(og);
  await og.screenshot({ path: 'public/og-default.png', fullPage: false });
  await og.close();
  console.log('saved public/og-default.png');
} finally {
  await browser.close();
}
