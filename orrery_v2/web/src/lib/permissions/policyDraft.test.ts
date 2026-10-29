import { describe, expect, it } from 'vitest'
import {
  addPolicyRole,
  clonePermissionPolicy,
  toggleRoleAssignment,
  toggleRoleScopeGrant
} from './policyDraft'
import type { PermissionPolicyDTO } from '#lib/types/api'

describe('permission policy draft helpers', () => {
  it('adds localized roles without duplicating ids', () => {
    let policy: PermissionPolicyDTO = {}

    policy = addPolicyRole(policy, { roleId: 'ops-admin', labelZh: '运营管理员', labelEn: 'Operations Admin' })
    policy = addPolicyRole(policy, { roleId: 'ops-admin', labelZh: '重复', labelEn: 'Duplicate' })

    expect(policy.roles).toEqual([{ role_id: 'ops-admin', label_zh: '运营管理员', label_en: 'Operations Admin' }])
  })

  it('creates a role with selected permission packages in one command', () => {
    const policy = addPolicyRole(
      {},
      {
        roleId: 'security-reviewer',
        labelZh: 'Security Reviewer',
        labelEn: 'Security Reviewer',
        permissionScopes: ['permissions.read', 'permissions.manage']
      }
    )

    expect(policy.roles).toEqual([{ role_id: 'security-reviewer', label_zh: 'Security Reviewer', label_en: 'Security Reviewer' }])
    expect(policy.grants).toEqual([
      { subject_kind: 'role', subject_id: 'security-reviewer', scope: 'permissions.read' },
      { subject_kind: 'role', subject_id: 'security-reviewer', scope: 'permissions.manage' }
    ])
  })

  it('toggles role grants using permission package scopes and removes covered route grants', () => {
    let policy: PermissionPolicyDTO = {
      grants: [
        { subject_kind: 'role', subject_id: 'ops-admin', scope: 'permissions.manage' },
        { subject_kind: 'role', subject_id: 'ops-admin', capability_id: 'put.api.permissions.policy.permissions.manage' }
      ]
    }

    policy = toggleRoleScopeGrant(policy, 'ops-admin', ['permissions.manage'], ['put.api.permissions.policy.permissions.manage'], false)
    expect(policy.grants).toEqual([])

    policy = toggleRoleScopeGrant(policy, 'ops-admin', ['permissions.manage'], ['put.api.permissions.policy.permissions.manage'], true)
    policy = toggleRoleScopeGrant(policy, 'ops-admin', ['permissions.manage'], ['put.api.permissions.policy.permissions.manage'], true)
    expect(policy.grants).toEqual([
      {
        subject_kind: 'role',
        subject_id: 'ops-admin',
        scope: 'permissions.manage'
      }
    ])
  })

  it('toggles merged packages that contain multiple scopes', () => {
    let policy: PermissionPolicyDTO = {
      grants: [
        { subject_kind: 'role', subject_id: 'ops-admin', scope: 'idea.read' },
        { subject_kind: 'role', subject_id: 'ops-admin', scope: 'idea.comment.read' },
        { subject_kind: 'role', subject_id: 'ops-admin', capability_id: 'get.api.ideas.idea.read' }
      ]
    }

    policy = toggleRoleScopeGrant(policy, 'ops-admin', ['idea.read', 'idea.comment.read'], ['get.api.ideas.idea.read'], true)

    expect(policy.grants).toEqual([
      { subject_kind: 'role', subject_id: 'ops-admin', scope: 'idea.read' },
      { subject_kind: 'role', subject_id: 'ops-admin', scope: 'idea.comment.read' }
    ])
  })

  it('toggles role assignments immutably', () => {
    const original: PermissionPolicyDTO = {
      role_assignments: [],
      grants: []
    }

    let policy = toggleRoleAssignment(original, 'user', 'operator', 'ops-admin', true)

    expect(original.role_assignments).toEqual([])
    expect(policy.role_assignments).toEqual([{ principal_kind: 'user', principal_id: 'operator', role_id: 'ops-admin' }])

    policy = toggleRoleAssignment(policy, 'user', 'operator', 'ops-admin', false)

    expect(policy.role_assignments).toEqual([])
  })

  it('clones policy command arrays before page-local editing', () => {
    const source: PermissionPolicyDTO = {
      roles: [{ role_id: 'ops-admin', label_zh: '运营管理员', label_en: 'Operations Admin' }],
      role_assignments: [],
      grants: []
    }

    const copy = clonePermissionPolicy(source)
    copy.roles?.push({ role_id: 'security', label_zh: '安全', label_en: 'Security' })

    expect(source.roles).toHaveLength(1)
    expect(copy.roles).toHaveLength(2)
  })
})
