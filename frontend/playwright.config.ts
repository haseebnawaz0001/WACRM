import { defineConfig, devices } from '@playwright/test'

// Skip webServer if CI or BASE_URL is set (user has their own server running)
const skipWebServer = !!process.env.CI || !!process.env.BASE_URL

export default defineConfig({
  testDir: './e2e/tests',
  globalSetup: './e2e/global-setup.ts',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 4 : undefined,
  reporter: [
    ['html', { open: 'never' }],
    ['list']
  ],
  use: {
    baseURL: process.env.BASE_URL || 'http://localhost:3000',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        // Undefined by default, so CI keeps using Playwright's own pinned
        // browser. Set PLAYWRIGHT_CHANNEL=chrome to run against a locally
        // installed Chrome instead — the bundled build is a large download
        // that a developer machine may not be able to fetch, and "I cannot
        // run the e2e suite" is how a suite stops being run.
        channel: process.env.PLAYWRIGHT_CHANNEL || undefined,
      },
    },
  ],
  webServer: skipWebServer ? undefined : {
    command: 'npm run dev',
    url: 'http://localhost:3000',
    reuseExistingServer: true,
    timeout: 120000,
  },
})
