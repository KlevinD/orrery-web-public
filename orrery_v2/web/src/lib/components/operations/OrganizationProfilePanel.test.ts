import { readFileSync } from 'node:fs'
import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import type { OrganizationTreeViewModel } from '#lib/types/view-models'
import OrganizationProfilePanel from './OrganizationProfilePanel.svelte'

const queryMocks = vi.hoisted(() => ({
  useOrganizationResponsibilityProfileQuery: vi.fn(() => ({
    data: {
      scope_type: 'organization',
      scope_id: 'org-1',
      source: 'people',
      responsibility_count: 1,
      version_count: 1,
      updated_at: '2026-08-25T00:00:00Z',
      portfolios: [{
        binding_id: 'binding-1',
        people_tenant_id: 'people-1',
        subject_id: 'subject-1',
        agent_profile_id: 'agent-1',
        agent_display_name: 'Agent One',
        status: 'active',
        current_version_id: 'version-1',
        current_source_revision: '1',
        current_source_digest: 'digest-1',
        responsibilities: [{
          id: 'responsibility-1',
          code: 'PRODUCT',
          title: 'Product delivery',
          description: 'Own product delivery.',
          valid_from: '2026-08-25T00:00:00Z',
          valid_to: null,
          revision: 1,
          sources: []
        }],
        versions: [],
        audit: [],
        source_as_of: '2026-08-25T00:00:00Z',
        synced_at: '2026-08-25T00:00:00Z'
      }]
    },
    isLoading: false,
    isError: false
  }))
}))

vi.mock('#lib/queries/workforce', () => queryMocks)
vi.mock('#lib/components/operations/OrganizationGraphWorkspace.svelte', async () => ({
  default: (await import('./OrganizationProfilePanelTestStub.svelte')).default
}))
vi.mock('#lib/components/operations/ResponsibilityProfilePanel.svelte', async () => ({
  default: (await import('./OrganizationProfilePanelTestStub.svelte')).default
}))

describe('OrganizationProfilePanel', () => {
  it('renders responsibility data, owner profile fallback, and the precise node-role label', async () => {
    queryMocks.useOrganizationResponsibilityProfileQuery.mockClear()
    const onConfigureOwner = vi.fn()
    const { container } = render(OrganizationProfilePanel, {
      props: { organization: organizationFixture, onConfigureOwner }
    })

    expect(screen.getAllByText('Own product delivery.')).toHaveLength(2)
    expect(container.textContent).toContain('profile-owner')
    expect(container.textContent).toContain('节点角色')

    expect(queryMocks.useOrganizationResponsibilityProfileQuery).toHaveBeenCalledTimes(2)
    const responsibilityQueryCalls = queryMocks.useOrganizationResponsibilityProfileQuery.mock.calls as unknown as Array<[
      () => string,
      () => boolean,
      () => number
    ]>
    const currentOnlyLimit = responsibilityQueryCalls[0]?.[2]
    const historyEnabled = responsibilityQueryCalls[1]?.[1]
    const historyLimit = responsibilityQueryCalls[1]?.[2]
    expect(currentOnlyLimit?.()).toBe(0)
    expect(historyLimit?.()).toBe(20)
    expect(historyEnabled?.()).toBe(false)

    await fireEvent.click(screen.getByRole('tab', { name: '组织责任' }))
    expect(historyEnabled?.()).toBe(true)

    await fireEvent.click(screen.getByRole('button', { name: '配置组织负责人' }))
    expect(onConfigureOwner).toHaveBeenCalledOnce()
  })

  it('keeps loading and empty responsibility states distinct', () => {
    const source = readFileSync('src/lib/components/operations/OrganizationProfilePanel.svelte', 'utf8')

    expect(source).toContain('currentResponsibilityProfileQuery.isLoading')
    expect(source).toContain("activeTab === 'responsibilities'")
    expect(source).toContain('() => 0')
    expect(source).toContain("? '正在读取责任边界...'")
    expect(source).toContain("? '责任边界读取失败。'")
    expect(source).toContain("responsibilities[0]?.description || '当前组织尚未配置责任边界。'")
  })
})

const organizationFixture: OrganizationTreeViewModel = {
  id: 'org-1',
  organizationId: 'org-1',
  nodeType: 'company',
  code: '01',
  displayCode: '01',
  name: 'Minvia',
  level: 1,
  status: 'active',
  ownerProfileId: 'profile-owner',
  ownerEmployeeNo: '',
  ownerName: '',
  memberCount: 1,
  unitCount: 0,
  directMembers: [],
  permissionSummary: {
    effectiveRoleCount: 0,
    directRoleCount: 0,
    inheritedRoleCount: 0,
    keyPermissions: [],
    denyPolicyCount: 0,
    approvalRequiredCount: 0
  },
  roleAssignments: [],
  approvalPolicyRefs: [],
  positions: [],
  units: [],
  children: []
}
