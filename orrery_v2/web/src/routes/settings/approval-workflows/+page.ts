import { redirect } from '@sveltejs/kit'
import { requireLegacyRouteAliases } from '#lib/routes/legacy-route-aliases'

export const prerender = false

// Deprecated route alias. Keep until removal is approved; backup: backup/frontend-before-route-consolidation-20260831.
export const load = () => {
  requireLegacyRouteAliases()
  throw redirect(308, '/approvals/workflows')
}
