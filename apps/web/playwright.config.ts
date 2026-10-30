import { defineConfig, devices } from '@playwright/test'

const isCI = !!process.env.CI

// The API port, shared by the dev server's proxy, the API's own PORT, and the
// readiness probe. Overridable so the suite can run beside other work; the
// default keeps CI exactly as it was.
const apiPort = process.env.E2E_API_PORT ?? '8080'

export default defineConfig({
  testDir: './tests',
  // Generous, because the first test after a cold start pays the compile
  // cost. The suite has few tests, so a per-test budget this high does not
  // meaningfully slow the run.
  timeout: 120_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  // Serial, and deliberately so: the E2E journey mutates shared fixture data
  // (registers users against a persistent database, creates chat threads), so
  // parallel workers would race each other's fixtures.
  workers: 1,
  // Zero retries hides a real flake behind a lucky re-run only if there is no
  // retry — but the opposite is worse: without retries, one flake reddens a PR.
  // One retry, and a test that fails twice is a genuine failure.
  retries: isCI ? 1 : 0,
  // The `list` reporter writes no report directory, so the CI artifact upload of
  // `playwright-report/` was uploading nothing on every failure while
  // appearing to succeed. The HTML reporter is what actually produces it.
  reporter: isCI ? [['list'], ['html', { open: 'never' }]] : [['list']],
  outputDir: './test-results',
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:3000',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
  globalSetup: './tests/global-setup.mjs',
  webServer: [
    {
      // Production server, not dev: the suite gates what users get. `next dev`
      // proved too fragile as a suite dependency (cold-compile wedging left
      // #__next empty until the assertion timeout, then the process died
      // mid-run and the whole suite went red without testing anything). The
      // build runs on every invocation so a stale .next can never be adopted
      // silently; the `web` CI job also builds, so this is a second compile,
      // not the only one.
      command: 'npm run build && npm run start -- --port 3000',
      url: 'http://localhost:3000',
      // In CI there must be no server to reuse. A stale server left on a
      // reused runner image would otherwise be adopted silently, and the suite
      // would test whatever that server happened to be running.
      reuseExistingServer: !isCI,
      timeout: 420_000,
      // The Next server proxies /api to Go (next.config.ts rewrites from
      // API_ORIGIN) and the browser opens its WebSocket at NEXT_PUBLIC_WS_BASE
      // (same-origin fallback is for Vercel, whose vercel.json WS rule handles
      // the upgrade — `next dev`/`next start` never proxy upgrades).
      // Defaults keep CI exactly as it was; set E2E_API_PORT to run the suite
      // beside other work on the same machine.
      env: {
        API_ORIGIN: `http://localhost:${apiPort}`,
        API_INTERNAL_URL: `http://localhost:${apiPort}`,
        NEXT_PUBLIC_WS_BASE: `ws://localhost:${apiPort}`,
        SITE_URL: 'http://localhost:3000',
      },
    },
    {
      command: 'go run ./cmd/api',
      // /health queries Postgres, so this is a real readiness probe: the API
      // cannot serve without a migrated database.
      url: `http://localhost:${apiPort}/api/v1/health`,
      reuseExistingServer: !isCI,
      timeout: 120_000,
      cwd: '../../services/api',
env: {
        PORT: apiPort,
        // Every browser request reaches the API through the Next rewrite, so they
        // all share one client IP. Lifting the *global* per-IP bucket is
        // required.
        RATELIMIT_GLOBAL: '5000',
        // APP_ENV=test plus the per-account bypass, set HERE rather than left to
        // the caller.
        //
        // Both halves are required. internal/httpapi gates the bypass on
        // APP_ENV=test, and config.Validate refuses to boot outside it, so
        // setting only the bypass is a loud boot failure and setting only
        // APP_ENV leaves the throttle armed. Neither was set: the bypass read
        // `process.env.E2E_ACCOUNT_THROTTLE_BYPASS ?? ''`, which nothing in the
        // repository ever set, so it resolved to '' and the per-account 10-per-15
        // minutes bucket stayed live for the whole suite.
        //
        // That is what made register/land-on-/me fail. The suite registers several
        // accounts in one run from one shared proxy IP, trips the bucket, and the
        // next registration is refused for 15 minutes - which surfaces as "landed on
        // /login instead of /me", with no rate-limit error anywhere in the test
        // output. The specs had previously papered over it with 120-second expect
        // timeouts; those were removed as part of a cleanup that assumed this
        // hatch was already working.
        //
        // The hatch stays narrow by construction: per-ACCOUNT buckets only, never
        // the per-IP tiers, and refused outside APP_ENV=test.
        APP_ENV: 'test',
        E2E_ACCOUNT_THROTTLE_BYPASS: '1',
      },
    },
  ],
})
