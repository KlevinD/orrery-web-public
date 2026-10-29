import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const readRoute = (path: string) => readFileSync(path, 'utf-8')

describe('workforce Figma layout parity', () => {
  it('uses the compact squad directory and member-detail split view', () => {
    const source = readRoute('src/routes/squads/+page.svelte')

    expect(source).toContain('max-w-[1040px]')
    expect(source).toContain('data-squad-directory="master-detail"')
    expect(source).toContain('lg:grid-cols-[220px_minmax(0,1fr)]')
    expect(source).toContain('selectedSquadId')
    expect(source).toContain('activeSquad')
  })

  it('uses the compact people tabs, filters, and personnel-record workspace', () => {
    const page = readRoute('src/routes/people/+page.svelte')
    const list = readRoute('src/lib/components/operations/WorkforceMemberList.svelte')
    const profile = readRoute('src/lib/components/operations/WorkforceMemberProfilePanel.svelte')
    const selector = readRoute('src/lib/components/operations/WorkforceMemberSelector.svelte')

    expect(page).toContain('max-w-[1080px]')
    expect(page).toContain('data-people-tabs="directory"')
    expect(page).toContain('border-b border-border')
    expect(page).toContain('data-people-directory="profile-workspace"')
    expect(page).toContain('xl:grid-cols-[268px_minmax(0,1fr)]')
    expect(page).not.toContain('data-people-profile-pager')
    expect(page).toContain('WorkforceMemberSelector')
    expect(page).toContain('const next = current ?? filteredMembers[0]')
    expect(page).toContain('let selectedMember = $state.raw<WorkforceMemberViewModel | undefined>()')
    expect(page).toContain('xl:sticky xl:top-4')
    expect(page).not.toContain('{#key selectedMember.profileId}')
    expect(page).not.toContain('people.compactStatsTotal')
    expect(page).toContain('refreshDirectory')
    expect(page).toContain('<RefreshCw')
    expect(list).toContain('h-11 w-11')
    expect(list).toContain('rounded-xl border border-border bg-card p-4')
    expect(list).toContain('flex-col items-stretch')
    expect(list).toContain('showPlacementAction?: boolean')
    expect(selector).toContain('data-workforce-member-selector')
    expect(selector).toContain('role="listbox"')
    expect(selector).toContain("activeId === member.profileId ? 'selection-wash'")
    expect(selector).toContain('h-12 w-12')
    expect(selector).toContain('px-2.5 py-2.5')
    expect(selector).toContain('<WorkforceAvatar')
    expect(selector).not.toContain('<Badge')
    expect(profile).toContain('data-member-profile-hero')
    expect(profile).toContain('data-member-profile-resources')
    expect(profile).toContain('rounded-2xl border border-border')
    expect(profile).toContain('rounded-xl border border-border')
  })

  it('keeps employment information inside the selected member profile', () => {
    const source = readRoute('src/lib/components/operations/WorkforceMemberProfilePanel.svelte')

    expect(source).toContain("key: 'employment'")
    expect(source).toContain('data-member-profile-tabs')
    expect(source).toContain('data-employment-information')
    expect(source).not.toContain('data-employment-record-list')
  })

  it('uses the four-metric organization overview and compact tree workspace', () => {
    const page = readRoute('src/routes/org/+page.svelte')
    const tree = readRoute('src/lib/components/operations/OrgTreeNode.svelte')

    expect(page).toContain('max-w-[1000px]')
    expect(page).toContain('data-org-kpi-grid')
    expect(page).toContain('lg:grid-cols-4')
    expect(page).toContain('data-org-workspace="tree-detail"')
    expect(page).toContain('lg:grid-cols-[260px_minmax(0,1fr)]')
    expect(tree).toContain('min-h-8')
    expect(tree).toContain('bg-muted')
  })
})
