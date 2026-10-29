import { describe, expect, it } from 'vitest'
import { hasAllPermissionScopes, hasPermissionScope, isPermissionDeniedMessage } from './access'

describe('permission access helpers', () => {
  it('lets a domain manage scope satisfy the matching read scope', () => {
    expect(hasPermissionScope(['resource.manage'], 'resource.read')).toBe(true)
    expect(hasPermissionScope(['settings.manage'], 'settings.read')).toBe(true)
  })

  it('does not expand operations scopes into specific domains', () => {
    expect(hasAllPermissionScopes(['operations.read'], ['workforce.read', 'org.read', 'roles.read'])).toBe(false)
    expect(hasPermissionScope(['operations.manage'], 'permissions.manage')).toBe(false)
    expect(hasPermissionScope(['operations.read'], 'resource.read')).toBe(false)
    expect(hasPermissionScope(['operations.manage'], 'approvals.read')).toBe(false)
  })

  it('recognizes permission-denied backend messages', () => {
    expect(isPermissionDeniedMessage('permission denied')).toBe(true)
    expect(isPermissionDeniedMessage('knowledge permission denied')).toBe(true)
    expect(isPermissionDeniedMessage('forbidden')).toBe(true)
    expect(isPermissionDeniedMessage('backend unavailable')).toBe(false)
  })
})
