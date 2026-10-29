import { svelte, vitePreprocess } from '@sveltejs/vite-plugin-svelte'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [svelte({ configFile: false, preprocess: vitePreprocess({ style: false }) })],
  resolve: {
    alias: {
      $lib: '/src/lib',
      '$app/navigation': '/src/lib/test/navigation.ts',
      '$app/stores': '/src/lib/test/stores.ts'
    },
    conditions: ['browser']
  },
  test: {
    environment: 'jsdom',
    globals: true,
    include: ['src/**/*.test.ts'],
    setupFiles: './src/test/setup.ts'
  }
})
