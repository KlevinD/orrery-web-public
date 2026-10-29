import { defineConfig } from '@playwright/test'

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  timeout: 30_000,
  use: {
    baseURL: 'http://127.0.0.1:5178',
    trace: 'retain-on-failure',
    launchOptions: executablePath ? { executablePath } : undefined
  },
  webServer: {
    command: 'npm run dev -- --port 5178',
    url: 'http://127.0.0.1:5178',
    reuseExistingServer: true,
    timeout: 30_000
  }
})
