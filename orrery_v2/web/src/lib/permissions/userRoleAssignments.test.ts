import { describe, expect, it } from 'vitest'
import {
  assignedUserRoleIds,
  buildDirectUserRoleAssignment,
  humanAssignableRoles,
  roleSelectionChanged,
  systemManagedRoleIds,
  userRoleAssignmentChanges
} from './userRoleAssignments'
import type { RoleDefinitionDTO } from '#lib/types/api'
import type { OrganizationTreeViewModel, WorkforceMemberViewModel } from '#lib/types/view-models'

const member = {
  profileId: 'profile-1',
  departmentId: 'dept-1',
  organizationId: 'org-1'
} as WorkforceMemberViewModel

const organization = {
  id: 'org-1',
  name: 'Orrery',
  code: '01',
  memberCount: 0,
  unitCount: 1,
  units: [],
  children: [{ id: 'dept-1', name: 'Security', code: '0101', memberCount: 0, unitCount: 0, units: [], children: [] }]
} satisfies OrganizationTreeViewModel

const platformRole = {
  role_id: 'platform.admin',
  category: 'platform',
  assignable_actor_types: ['human']
} as RoleDefinitionDTO

describe('user role assignments', () => {
  it('keeps only roles that support human principals', () => {
    expect(humanAssignableRoles([
      platformRole,
      { ...platformRole, role_id: 'agent.operator', assignable_actor_types: ['agent'] }
    ]).map((role) => role.role_id)).toEqual(['platform.admin'])
  })

  it('combines workforce and direct assignments into one selection', () => {
    expect(assignedUserRoleIds(['basic.user'], [
      { actor: { actor_type: 'human', actor_id: 'profile-1' }, role_id: 'custom.viewer', source: 'direct' }
    ])).toEqual(['basic.user', 'custom.viewer'])
  })

  it('diffs additions and removable direct assignments without deleting system rows', () => {
    const assignments = [
      { assignment_id: 'direct-1', role_id: 'custom.viewer', source: 'direct' },
      { assignment_id: 'system-1', role_id: 'basic.user', source: 'system' }
    ]
    expect(userRoleAssignmentChanges(['custom.viewer', 'basic.user'], ['custom.editor'], assignments)).toEqual({
      addedRoleIds: ['custom.editor'],
      removableAssignments: [assignments[0]]
    })
    expect(systemManagedRoleIds(assignments)).toEqual(new Set(['basic.user']))
  })

  it('compares only roles that are visible in the assignment catalog', () => {
    expect(roleSelectionChanged(['basic.user'], [], ['custom.viewer'])).toBe(false)
    expect(roleSelectionChanged([], ['custom.viewer'], ['custom.viewer'])).toBe(true)
  })

  it('builds platform and department-scoped direct assignments', () => {
    expect(buildDirectUserRoleAssignment(member, platformRole, [organization])).toEqual({
      actor: { actor_type: 'human', actor_id: 'profile-1' },
      role_id: 'platform.admin',
      source: 'direct',
      scope_mode: 'platform'
    })

    expect(buildDirectUserRoleAssignment(member, {
      ...platformRole,
      role_id: 'custom.viewer',
      category: 'custom'
    }, [organization])).toMatchObject({
      organization_node_id: 'dept-1',
      organization_code: '0101',
      scope_mode: 'self'
    })
  })
})
