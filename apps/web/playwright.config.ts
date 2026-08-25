import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests',
  timeout: 180_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:5173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  webServer: [
    {
      command: 'npm run dev',
      url: 'http://localhost:5173',
      reuseExistingServer: true,
      timeout: 60_000,
    },
    {
      command: 'go run ./cmd/api',
      url: 'http://localhost:8080/api/v1/health',
      reuseExistingServer: true,
      timeout: 60_000,
      cwd: '../../services/api',
      // The whole suite funnels through the dev-server proxy (one IP);
      // lift the global per-IP bucket so E2E traffic isn't rate-limited.
      env: {
        RATELIMIT_GLOBAL: '5000',
      },
    },
  ],
})
