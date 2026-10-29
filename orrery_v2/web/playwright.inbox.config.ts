import { defineConfig } from '@playwright/test'
import base from './playwright.config'

export default defineConfig({
  ...base,
  testMatch: 'work-inbox.spec.ts',
  use: { ...base.use, baseURL: 'http://127.0.0.1:5181' },
  webServer: {
    command: 'npm run preview -- --port 5181',
    url: 'http://127.0.0.1:5181',
    reuseExistingServer: false,
    timeout: 30_000
  }
})
