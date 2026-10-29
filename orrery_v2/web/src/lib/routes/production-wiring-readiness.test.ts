import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

function productionSources(directory: string): string[] {
  return readdirSync(directory).flatMap((entry) => {
    const path = join(directory, entry)
    if (statSync(path).isDirectory()) return productionSources(path)
    if (!/\.(?:svelte|ts)$/.test(path) || path.endsWith('.test.ts')) return []
    return [path]
  })
}

describe('production wiring readiness', () => {
  it('keeps test doubles outside production frontend modules', () => {
    expect(existsSync('src/routes/evaluation/temporaryEvaluationMock.ts')).toBe(false)

    for (const file of productionSources('src')) {
      const source = readFileSync(file, 'utf-8')
      expect(source, file).not.toMatch(/from\s+['"][^'"]*(?:mock|fixture)[^'"]*['"]/i)
      expect(source, file).not.toMatch(/\b(?:create|load|use)\w*(?:Mock|Fixture)\b/)
      expect(source, file).not.toMatch(/fake[-_ ]?success/i)
    }
  })
})
