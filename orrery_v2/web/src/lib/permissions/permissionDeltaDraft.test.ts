import { describe, expect, it } from 'vitest'
import {
  createPermissionDeltaDraft,
  permissionBatchCommand,
  rebasePermissionDeltaDraft,
  resolvePackageGrantState,
  stagePackageChange,
  stageRoleCreate
} from './permissionDeltaDraft'

describe('permission delta draft', () => {
  it('keeps only the latest package operation and overlays it on server state', () => {
    let draft = createPermissionDeltaDraft(9)
    draft = stagePackageChange(draft, { roleId: 'custom.security', packageId: 'permission.read', effect: 'grant' })
    draft = stagePackageChange(draft, { roleId: 'custom.security', packageId: 'permission.read', effect: 'revoke' })

    expect(resolvePackageGrantState('granted', draft, 'custom.security', 'permission.read')).toBe('none')
    expect(permissionBatchCommand(draft).package_changes).toEqual([
      { role_id: 'custom.security', package_id: 'permission.read', effect: 'revoke' }
    ])
  })

  it('stages role creation and its package grants for one unified save', () => {
    let draft = createPermissionDeltaDraft(3)
    draft = stageRoleCreate(draft, { roleId: 'custom.auditor', labelZh: '审计员', labelEn: 'Auditor' })
    draft = stagePackageChange(draft, { roleId: 'custom.auditor', packageId: 'audit.read', effect: 'grant' })

    expect(permissionBatchCommand(draft)).toEqual({
      expected_revision: 3,
      role_creates: [{ role_id: 'custom.auditor', label_zh: '审计员', label_en: 'Auditor' }],
      package_changes: [{ role_id: 'custom.auditor', package_id: 'audit.read', effect: 'grant' }],
      capability_changes: []
    })
  })

  it('rebases a conflicted draft without dropping staged changes', () => {
    let draft = createPermissionDeltaDraft(3)
    draft = stageRoleCreate(draft, { roleId: 'custom.auditor', labelZh: '审计员', labelEn: 'Auditor' })
    draft = stagePackageChange(draft, { roleId: 'custom.auditor', packageId: 'audit.read', effect: 'grant' })

    expect(permissionBatchCommand(rebasePermissionDeltaDraft(draft, 8))).toEqual({
      expected_revision: 8,
      role_creates: [{ role_id: 'custom.auditor', label_zh: '审计员', label_en: 'Auditor' }],
      package_changes: [{ role_id: 'custom.auditor', package_id: 'audit.read', effect: 'grant' }],
      capability_changes: []
    })
  })
})
