import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import {
  LEGACY_ROUTE_ALIASES_ENABLED,
  isLegacyRouteAlias,
  isLegacyRouteAliasDisabled,
  requireLegacyRouteAliases
} from './legacy-route-aliases'

const aliases = [
  '/todos',
  '/ideas/',
  '/mission-room',
  '/mission-room/mission-1',
  '/personal-assistant',
  '/memory',
  '/people/employment',
  '/settings/approval-workflows',
  '/resources/ontology/graph',
  '/discussion/mission-1'
]

const guardedRouteFiles = [
  'src/routes/todos/+page.ts',
  'src/routes/ideas/+page.ts',
  'src/routes/mission-room/+page.ts',
  'src/routes/mission-room/[missionId]/+page.ts',
  'src/routes/personal-assistant/+page.ts',
  'src/routes/memory/+page.ts',
  'src/routes/people/employment/+page.ts',
  'src/routes/settings/approval-workflows/+page.ts',
  'src/routes/resources/ontology/graph/+page.ts',
  'src/routes/discussion/[missionId]/+page.ts'
]

describe('legacy route aliases', () => {
  it('keeps every backed-up alias disabled', () => {
    expect(LEGACY_ROUTE_ALIASES_ENABLED).toBe(false)
    for (const pathname of aliases) {
      expect(isLegacyRouteAlias(pathname)).toBe(true)
      expect(isLegacyRouteAliasDisabled(pathname)).toBe(true)
    }
  })

  it('does not disable canonical issue details or replacement pages', () => {
    for (const pathname of [
      '/',
      '/todos/todo-1',
      '/ideas/idea-1',
      '/missions/mission-1',
      '/discussions/conversation-1',
      '/profile',
      '/resources/memory',
      '/resources/ontology/context',
      '/approvals/workflows',
      '/people'
    ]) {
      expect(isLegacyRouteAlias(pathname)).toBe(false)
      expect(isLegacyRouteAliasDisabled(pathname)).toBe(false)
    }
  })

  it('returns 404 before any compatibility redirect can execute', () => {
    try {
      requireLegacyRouteAliases()
      throw new Error('legacy route guard did not throw')
    } catch (routeError) {
      expect(routeError).toMatchObject({ status: 404 })
    }
  })

  it('guards every archived route and excludes it from prerendered HTML', () => {
    for (const file of guardedRouteFiles) {
      const source = readFileSync(file, 'utf8')
      expect(source).toContain('requireLegacyRouteAliases()')
      expect(source).toContain('export const prerender = false')
    }
  })
})
