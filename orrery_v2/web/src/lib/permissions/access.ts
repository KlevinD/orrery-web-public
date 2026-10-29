export function hasPermissionScope(scopes: readonly string[], required: string) {
  const requiredScope = required.trim()
  if (!requiredScope) return true

  return scopes.some((value) => permissionScopeMatches(value.trim(), requiredScope))
}

export function hasAllPermissionScopes(scopes: readonly string[], required: readonly string[]) {
  return required.every((scope) => hasPermissionScope(scopes, scope))
}

export function isPermissionDeniedMessage(message: string | undefined) {
  const normalized = (message ?? '').trim().toLowerCase()
  return normalized === 'forbidden' ||
    normalized.includes('permission denied') ||
    normalized.includes('access denied') ||
    normalized.includes('没有权限') ||
    normalized.includes('无权限')
}

function permissionScopeMatches(granted: string, required: string) {
  if (!granted) return false
  if (granted === required) return true

  if (required.endsWith('.read') && granted === `${required.slice(0, -'.read'.length)}.manage`) {
    return true
  }

  return false
}
