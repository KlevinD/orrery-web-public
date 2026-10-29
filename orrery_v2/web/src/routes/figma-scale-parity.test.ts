import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const source = (path: string) => readFileSync(path, 'utf-8')

describe('Figma scale parity', () => {
  it('keeps the confirmed global typography and radius scale', () => {
    const css = source('src/app.css')

    expect(css).toContain('--font-ui-latin: ui-sans-serif, system-ui, sans-serif')
    expect(css).toContain('--radius: 0.625rem')
    expect(css).toContain('font-size: 1.375rem')
    expect(css).toContain('line-height: 1.5')
    expect(css).toContain('border-radius: calc(var(--radius) + 4px)')
  })

  it.each([
    ['missions', 'src/routes/missions/+page.svelte', 'max-w-[960px]'],
    ['automation', 'src/routes/scheduled-tasks/+page.svelte', 'max-w-[920px]'],
    ['people', 'src/routes/people/+page.svelte', 'max-w-[1080px]'],
    ['organization', 'src/routes/org/+page.svelte', 'max-w-[1000px]'],
    ['permissions', 'src/routes/permissions/+page.svelte', 'max-w-[1080px]'],
    ['audit', 'src/routes/audit/+page.svelte', 'max-w-[1040px]'],
    ['knowledge', 'src/routes/resources/knowledge/+page.svelte', 'max-w-[1040px]'],
    ['settings', 'src/lib/components/settings/SettingsWorkspace.svelte', 'max-w-[920px]'],
    ['profile', 'src/routes/profile/+page.svelte', 'max-w-[720px]']
  ])('keeps the %s page width from the Figma reference', (_name, path, width) => {
    expect(source(path)).toContain(width)
  })

  it('uses 14px content surfaces and 16px identity/auth shells', () => {
    expect(source('src/lib/components/mission/MissionList.svelte')).toContain('rounded-xl border border-border bg-card')
    expect(source('src/routes/evaluation/+page.svelte')).toContain('rounded-xl border border-border bg-card')
    expect(source('src/lib/components/settings/GeneralSettingsPanel.svelte')).toContain('rounded-xl border border-border bg-card')
    expect(source('src/routes/resources/knowledge/+page.svelte')).toContain('rounded-xl border border-border bg-card')
    expect(source('src/lib/components/operations/WorkforceMemberProfilePanel.svelte')).toContain('rounded-2xl border border-border')
    expect(source('src/routes/login/+page.svelte')).toContain('rounded-2xl border border-border bg-card')
  })
})
