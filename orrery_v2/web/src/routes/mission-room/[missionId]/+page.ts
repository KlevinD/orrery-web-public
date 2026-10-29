import { redirect } from '@sveltejs/kit'
import { requireLegacyRouteAliases } from '#lib/routes/legacy-route-aliases'

export const prerender = false

// Deprecated route alias. Keep until removal is approved; backup: backup/frontend-before-route-consolidation-20260831.
export function load({ params, url }: { params: { missionId: string }; url: URL }) {
  requireLegacyRouteAliases()
  const query = new URLSearchParams(url.searchParams)
  query.set('tab', 'operations')
  throw redirect(308, `/missions/${encodeURIComponent(params.missionId)}?${query.toString()}`)
}
