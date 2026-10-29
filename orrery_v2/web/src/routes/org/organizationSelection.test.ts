import { describe, expect, it } from 'vitest'
import type { OrganizationTreeViewModel } from '#lib/types/view-models'
import { findOrganizationById, selectInitialOrganizationId } from './organizationSelection'

function orgNode(
  id: string,
  code: string,
  status: string,
  children: OrganizationTreeViewModel[] = []
): OrganizationTreeViewModel {
  return {
    id,
    organizationId: id,
    nodeType: 'company',
    code,
    displayCode: code,
    name: id,
    level: 1,
    status,
    ownerProfileId: '',
    ownerEmployeeNo: '',
    ownerName: '',
    parentId: '',
    parentCode: '',
    parentDisplayCode: '',
    parentName: '',
    directMemberCount: 0,
    totalMemberCount: 0,
    memberCount: 0,
    unitCount: children.length,
    childCount: children.length,
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
    units: [],
    children
  }
}

describe('organization selection', () => {
  it('selects the first active root sorted by organization code for the initial screen', () => {
    const organizations = [orgNode('org-02', '02', 'active'), orgNode('org-01', '01', 'active')]

    expect(selectInitialOrganizationId(organizations, '')).toBe('org-01')
  })

  it('keeps the current selection when the node still exists', () => {
    const organizations = [orgNode('org-01', '01', 'active', [orgNode('dept-ai', '0101', 'active')])]

    expect(selectInitialOrganizationId(organizations, 'dept-ai')).toBe('dept-ai')
  })

  it('returns an empty selection when the organization tree has no nodes', () => {
    expect(selectInitialOrganizationId([], 'missing')).toBe('')
  })

  it('finds descendant organizations by id', () => {
    const organizations = [orgNode('org-01', '01', 'active', [orgNode('dept-ai', '0101', 'active')])]

    expect(findOrganizationById(organizations, 'dept-ai')?.name).toBe('dept-ai')
  })
})
