import { defineConfig, devices } from '@playwright/test'

// E2E tests run against the live dev stack (make up): web on :5173, evrd API
// on :8098. globalSetup seeds the test account.
export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global-setup.js',
  // Tests are independent (each makes its own accounts), so CI shards can
  // split by test; within a run they go one at a time against the one stack.
  fullyParallel: true,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://localhost:5173',
    trace: 'on-first-retry',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
