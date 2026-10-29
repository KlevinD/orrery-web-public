import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('migration readiness suite structure', () => {
  it('keeps the shared migration readiness suite focused', () => {
    const source = readFileSync('src/lib/routes/migration-readiness.test.ts', 'utf-8')

    expect(source.split(/\r?\n/).length).toBeLessThanOrEqual(800)
    expect(source).not.toContain('keeps settings page behind a view model mapper')
    expect(source).not.toContain('wires runtime registration into system settings')
  })
})
