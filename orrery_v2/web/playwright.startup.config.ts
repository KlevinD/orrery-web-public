import { defineConfig } from '@playwright/test'
import base from './playwright.config'

export default defineConfig({
  ...base,
  testDir: './performance-e2e',
  testMatch: 'frontend-startup.spec.ts',
  use: { ...base.use, baseURL: 'http://127.0.0.1:5189' },
  webServer: {
    command: 'npm run preview -- --port 5189 --strictPort',
    url: 'http://127.0.0.1:5189',
    reuseExistingServer: false,
    timeout: 30_000
  }
})
