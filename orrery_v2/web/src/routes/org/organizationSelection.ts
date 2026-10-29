import type { OrganizationTreeViewModel } from '#lib/types/view-models'

export function selectInitialOrganizationId(
  organizations: OrganizationTreeViewModel[],
  currentId: string
): string {
  if (currentId && findOrganizationById(organizations, currentId)) return currentId
  const activeRoots = organizations
    .filter((organization) => (organization.status || 'active') === 'active')
    .sort(compareByCode)
  const roots = activeRoots.length > 0 ? activeRoots : [...organizations].sort(compareByCode)
  return roots[0]?.id ?? ''
}

export function findOrganizationById(
  organizations: OrganizationTreeViewModel[],
  id: string
): OrganizationTreeViewModel | undefined {
  if (!id) return undefined
  for (const organization of organizations) {
    if (organization.id === id) return organization
    const child = findOrganizationById(organization.children ?? [], id)
    if (child) return child
  }
  return undefined
}

function compareByCode(left: OrganizationTreeViewModel, right: OrganizationTreeViewModel) {
  const leftCode = left.code || left.displayCode || left.name
  const rightCode = right.code || right.displayCode || right.name
  return leftCode.localeCompare(rightCode, undefined, { numeric: true })
}
