import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('personal assistant compatibility route', () => {
  it('keeps the backed-up redirect disabled while profile owns assistant settings', () => {
    const route = readFileSync('src/routes/personal-assistant/+page.ts', 'utf8')
    const profile = readFileSync('src/routes/profile/+page.svelte', 'utf8')

    expect(route).toContain('requireLegacyRouteAliases()')
    expect(route).toContain("params.set('section', 'personal-assistant')")
    expect(route).toContain('redirect(308, `/profile?${params.toString()}`)')
    expect(profile).toContain("searchParams.get('section') === 'personal-assistant'")
    expect(profile).toContain('href="/profile?section=personal-assistant"')
  })
})
