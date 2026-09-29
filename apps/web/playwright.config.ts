import { defineConfig, devices } from '@playwright/test'

const isCI = !!process.env.CI

// The API port, shared by the dev server's proxy, the API's own PORT, and the
// readiness probe. Overridable so the suite can run beside other work; the
// default keeps CI exactly as it was.
const apiPort = process.env.E2E_API_PORT ?? '8080'

export default defineConfig({
  testDir: './tests',
  // Generous, because the first test after a cold Vite start pays the transform
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
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:5173',
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
      command: 'npm run dev',
      url: 'http://localhost:5173',
      // In CI there must be no server to reuse. A stale dev server left on a
      // reused runner image would otherwise be adopted silently, and the suite
      // would test whatever that server happened to be running.
      reuseExistingServer: !isCI,
      timeout: 120_000,
      // The dev server proxies /api here, so it must agree with the API's port.
      // Default is unchanged for CI; set E2E_API_PORT to run the suite beside
      // other work on the same machine.
      env: {
        VITE_API_TARGET: `http://localhost:${apiPort}`,
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
        // Every browser request reaches the API through the Vite proxy, so they
        // all share one client IP. Lifting the *global* per-IP bucket is
        // required. The per-account auth tier is not lifted, which is what the
        // 120s waits in smoke.spec.ts were compensating for — E2E_ACCOUNT_*
        // below is the proper fix.
        RATELIMIT_GLOBAL: '5000',
        // Test-only escape hatch for the per-account auth and 2FA buckets, read
        // by internal/config. It is refused outside APP_ENV=test, so a
        // production deployment cannot be started with it set.
        E2E_ACCOUNT_THROTTLE_BYPASS: process.env.E2E_ACCOUNT_THROTTLE_BYPASS ?? '',
      },
    },
  ],
})
