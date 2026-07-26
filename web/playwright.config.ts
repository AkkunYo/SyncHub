import { defineConfig } from '@playwright/test'

const useSystemChrome = process.env.CI !== 'true' && process.env.PLAYWRIGHT_USE_BUNDLED_CHROMIUM !== '1'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI === 'true' ? 1 : 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: 'http://127.0.0.1:18888',
    browserName: 'chromium',
    channel: useSystemChrome ? 'chrome' : undefined,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'npm run build && node e2e/run-stack.mjs',
    url: 'http://127.0.0.1:18888/api/v1/health',
    timeout: 120_000,
    reuseExistingServer: false,
  },
})
