import { redirect } from '@sveltejs/kit'
import { requireLegacyRouteAliases } from '#lib/routes/legacy-route-aliases'

export const prerender = false

// Deprecated route alias. Keep until removal is approved; backup: backup/frontend-before-route-consolidation-20260831.
export function load({ url }: { url: URL }) {
  requireLegacyRouteAliases()
  const mode = url.searchParams.get('mode')
  const target = mode === 'context_graph' ? '/resources/ontology/context' : '/resources/ontology/schema'
  const params = new URLSearchParams()
  const tenantId = url.searchParams.get('tenant_id')
  if (tenantId?.trim()) params.set('tenant_id', tenantId)
  params.set('view', mode === 'context_graph' ? 'graph' : 'semantic-map')
  throw redirect(307, `${target}?${params.toString()}`)
}
