import { error } from '@sveltejs/kit'

export const LEGACY_ROUTE_ALIASES_ENABLED = false

const exactLegacyRouteAliases = new Set([
  '/ideas',
  '/memory',
  '/mission-room',
  '/people/employment',
  '/personal-assistant',
  '/resources/ontology/graph',
  '/settings/approval-workflows',
  '/todos'
])

export function isLegacyRouteAlias(pathname: string) {
  const normalized = normalizePath(pathname)
  return exactLegacyRouteAliases.has(normalized) ||
    normalized.startsWith('/discussion/') ||
    normalized.startsWith('/mission-room/')
}

export function isLegacyRouteAliasDisabled(pathname: string) {
  return !LEGACY_ROUTE_ALIASES_ENABLED && isLegacyRouteAlias(pathname)
}

export function requireLegacyRouteAliases() {
  if (!LEGACY_ROUTE_ALIASES_ENABLED) throw error(404, 'Not found')
}

function normalizePath(pathname: string) {
  const trimmed = pathname.trim()
  if (!trimmed || trimmed === '/') return '/'
  return `/${trimmed.replace(/^\/+|\/+$/g, '')}`
}
