import type { PermissionPolicyBatchDTO } from '#lib/types/api'

export type PermissionChangeEffect = 'grant' | 'revoke'

export interface PermissionDeltaDraft {
  expectedRevision: number
  roleCreates: Array<{ roleId: string; labelZh: string; labelEn: string }>
  packageChanges: Array<{ roleId: string; packageId: string; effect: PermissionChangeEffect }>
  capabilityChanges: Array<{ roleId: string; capabilityId: string; effect: PermissionChangeEffect }>
}

export function createPermissionDeltaDraft(expectedRevision: number): PermissionDeltaDraft {
  return { expectedRevision, roleCreates: [], packageChanges: [], capabilityChanges: [] }
}

export function rebasePermissionDeltaDraft(draft: PermissionDeltaDraft, expectedRevision: number): PermissionDeltaDraft {
  const next = cloneDraft(draft)
  next.expectedRevision = expectedRevision
  return next
}

export function stageRoleCreate(
  draft: PermissionDeltaDraft,
  role: { roleId: string; labelZh: string; labelEn: string }
): PermissionDeltaDraft {
  const roleId = role.roleId.trim()
  if (!roleId || draft.roleCreates.some((item) => item.roleId === roleId)) return cloneDraft(draft)
  const next = cloneDraft(draft)
  next.roleCreates.push({
    roleId,
    labelZh: role.labelZh.trim() || roleId,
    labelEn: role.labelEn.trim() || role.labelZh.trim() || roleId
  })
  return next
}

export function stagePackageChange(
  draft: PermissionDeltaDraft,
  change: { roleId: string; packageId: string; effect: PermissionChangeEffect }
): PermissionDeltaDraft {
  const next = cloneDraft(draft)
  const roleId = change.roleId.trim()
  const packageId = change.packageId.trim()
  next.packageChanges = next.packageChanges.filter((item) => item.roleId !== roleId || item.packageId !== packageId)
  if (roleId && packageId) next.packageChanges.push({ roleId, packageId, effect: change.effect })
  return next
}

export function resolvePackageGrantState(
  serverState: string,
  draft: PermissionDeltaDraft,
  roleId: string,
  packageId: string
) {
  const change = [...draft.packageChanges].reverse().find((item) => item.roleId === roleId && item.packageId === packageId)
  if (!change) return serverState
  return change.effect === 'grant' ? 'granted' : 'none'
}

export function permissionBatchCommand(draft: PermissionDeltaDraft): PermissionPolicyBatchDTO {
  return {
    expected_revision: draft.expectedRevision,
    role_creates: draft.roleCreates.map((role) => ({ role_id: role.roleId, label_zh: role.labelZh, label_en: role.labelEn })),
    package_changes: draft.packageChanges.map((change) => ({
      role_id: change.roleId,
      package_id: change.packageId,
      effect: change.effect
    })),
    capability_changes: draft.capabilityChanges.map((change) => ({
      role_id: change.roleId,
      capability_id: change.capabilityId,
      effect: change.effect
    }))
  }
}

export function permissionDraftDirty(draft: PermissionDeltaDraft) {
  return draft.roleCreates.length + draft.packageChanges.length + draft.capabilityChanges.length > 0
}

function cloneDraft(draft: PermissionDeltaDraft): PermissionDeltaDraft {
  return {
    expectedRevision: draft.expectedRevision,
    roleCreates: draft.roleCreates.map((item) => ({ ...item })),
    packageChanges: draft.packageChanges.map((item) => ({ ...item })),
    capabilityChanges: draft.capabilityChanges.map((item) => ({ ...item }))
  }
}
