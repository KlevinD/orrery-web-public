import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('build configuration readiness', () => {
  it('bundles TanStack SSR dependencies instead of leaving unused external import warnings', () => {
    const config = readFileSync('vite.config.ts', 'utf-8')

    expect(config).toContain('noExternal')
    expect(config).toContain('@tanstack/svelte-query')
    expect(config).toContain('@tanstack/query-core')
    expect(config).not.toContain('manualChunks')
    expect(config).not.toContain('vendor-echarts')
  })
})
