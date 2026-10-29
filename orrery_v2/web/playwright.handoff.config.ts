import { defineConfig } from '@playwright/test'
import base from './playwright.config'

export default defineConfig({
  ...base,
  testMatch: ['human-handoff.spec.ts', 'human-feedback.spec.ts'],
  use: { ...base.use, baseURL: 'http://127.0.0.1:5183' },
  webServer: { command: 'npm run preview -- --port 5183', url: 'http://127.0.0.1:5183', reuseExistingServer: false, timeout: 30_000 }
})
