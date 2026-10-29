import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('skills registry page', () => {
  it('matches the Figma registry table while keeping both catalog views', () => {
    const source = readFileSync('src/routes/resources/skills/+page.svelte', 'utf-8')

    expect(source).toContain("activeView = 'skills'")
    expect(source).toContain("activeView = 'bundles'")
    expect(source).toContain("t('resources.skills.importPackage')")
    expect(source).toContain("t('resources.skills.exportShort')")
    expect(source).toContain('grid-cols-[minmax(0,1fr)_90px_120px_110px_80px]')
    expect(source).not.toContain('grid grid-cols-2 gap-3 lg:grid-cols-5')
    expect(source).not.toContain('mobileFiltersOpen')
  })
})
