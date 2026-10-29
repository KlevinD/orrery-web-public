import { requireLegacyRouteAliases } from '#lib/routes/legacy-route-aliases'

export const prerender = false

export function load({ params }) {
  requireLegacyRouteAliases()
  return { missionId: params.missionId }
}
