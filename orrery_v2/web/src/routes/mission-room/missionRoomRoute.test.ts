import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('Mission Room compatibility routes', () => {
  it('keeps backed-up redirects disabled while the canonical Mission workspace remains available', () => {
    const root = readFileSync('src/routes/mission-room/+page.ts', 'utf8')
    const detail = readFileSync('src/routes/mission-room/[missionId]/+page.ts', 'utf8')
    const workspace = readFileSync('src/routes/missions/[missionId]/+page.svelte', 'utf8')

    expect(root).toContain('requireLegacyRouteAliases()')
    expect(detail).toContain('requireLegacyRouteAliases()')
    expect(root).toContain("redirect(308, '/missions')")
    expect(detail).toContain("query.set('tab', 'operations')")
    expect(detail).toContain('redirect(308, `/missions/${encodeURIComponent(params.missionId)}?${query.toString()}`)')
    expect(workspace).toContain('<MissionRoom missionId={resolvedMissionId} />')
  })
})
