import { existsSync, readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('people employment records page', () => {
  it('renders employment records inside each member profile without a second member list', () => {
    const page = readFileSync('src/routes/people/+page.svelte', 'utf-8')
    const profile = readFileSync('src/lib/components/operations/WorkforceMemberProfilePanel.svelte', 'utf-8')

    expect(page).not.toContain('EmploymentRecordsView')
    expect(page).not.toContain('data-people-page-tabs')
    expect(existsSync('src/lib/components/operations/EmploymentRecordsView.svelte')).toBe(false)
    expect(profile).toContain("t('people.profileOverview')")
    expect(profile).toContain("t('people.employmentInformation')")
    expect(profile).toContain("t('people.currentAssignment')")
    expect(profile).not.toContain("t('people.employmentTimeline')")
    expect(profile).not.toContain('member.joinedAt')
    expect(profile).toContain('member.releasedAt')
    expect(profile).not.toContain('useWorkforceMembersInfiniteQuery')
  })

  it('keeps the former standalone redirect backed up but disabled', () => {
    const source = readFileSync('src/routes/people/employment/+page.ts', 'utf-8')

    expect(source).toContain('requireLegacyRouteAliases()')
    expect(source).toContain("redirect(307, '/people')")
  })

  it('gates human and agent creation behind workforce.manage', () => {
    const page = readFileSync('src/routes/people/+page.svelte', 'utf-8')

    expect(page).toContain("const canManageWorkforce = $derived(hasAllPermissionScopes(session.permissionScope, ['workforce.manage']))")
    expect(page).toMatch(/function openCreate\([^)]+\) \{\s+if \(!canManageWorkforce\) return/)
    expect(page).toMatch(/\{#if canManageWorkforce\}[\s\S]*openCreate\('human'\)[\s\S]*openCreate\('agent'\)[\s\S]*\{\/if\}/)
  })
})
