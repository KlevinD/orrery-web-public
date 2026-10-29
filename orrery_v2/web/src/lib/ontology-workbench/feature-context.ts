export interface OntologyFeatureContext {
  search?: string
}

const featureDomains: Array<{ paths: string[]; search: string }> = [
  { paths: ['/', '/todos', '/ideas'], search: 'planning' },
  { paths: ['/missions'], search: 'mission_control' },
  { paths: ['/people', '/org', '/squads'], search: 'operations' },
  { paths: ['/resources/tools', '/resources/skills', '/resources/mcp'], search: 'resource_admin' },
  { paths: ['/resources/knowledge'], search: 'knowledge' },
  { paths: ['/resources/memory', '/memory'], search: 'memory' },
  { paths: ['/approvals'], search: 'legal' },
  { paths: ['/evaluation'], search: 'value' }
]

export function ontologyFeatureContextForPath(pathname: string): OntologyFeatureContext {
  const path = normalizePath(pathname)
  for (const feature of featureDomains) {
    if (feature.paths.some((candidate) => pathMatches(path, candidate))) {
      return { search: feature.search }
    }
  }
  return {}
}

export function ontologySchemaHref(tenantId: string, search?: string) {
  const params = new URLSearchParams({ tenant_id: tenantId.trim(), view: 'semantic-map' })
  if (search?.trim()) params.set('search', search.trim())
  return `/resources/ontology/schema?${params.toString()}`
}

function normalizePath(pathname: string) {
  const trimmed = pathname.trim()
  if (!trimmed || trimmed === '/') return '/'
  return `/${trimmed.replace(/^\/+|\/+$/g, '')}`
}

function pathMatches(path: string, candidate: string) {
  return candidate === '/' ? path === '/' : path === candidate || path.startsWith(`${candidate}/`)
}
