import { describe, expect, it } from 'vitest'
import { createTranslator } from '#lib/i18n'
import {
  hasRouteAccess,
  navGroups,
  pageForPath,
  routeAccessRequirements,
  routePages,
  visibleNavGroups
} from './navigation'

describe('navigation contract', () => {
  it('declares every migration route requested by the Svelte 5 plan', () => {
    const paths = routePages.map((page) => page.path)

    expect(paths).toEqual([
      '/',
      '/work-items',
      '/discussions',
      '/missions',
      '/responsibilities',
      '/scheduled-tasks',
      '/resources/tools',
      '/resources/skills',
      '/resources/mcp',
      '/resources/memory',
      '/resources/ontology',
      '/resources/knowledge',
      '/approvals',
      '/approvals/workflows',
      '/permissions',
      '/people',
      '/org',
      '/squads',
      '/evaluation',
      '/audit',
      '/settings',
      '/settings/scm-connections',
      '/profile'
    ])
  })

  it('keeps sidebar groups dense and operations-oriented', () => {
    expect(navGroups.map((group) => group.title)).toEqual([
      'nav.group.work',
      'nav.group.people',
      'nav.group.control',
      'nav.group.resources',
      'nav.group.account'
    ])
    expect(navGroups.map((group) => createTranslator('en-US')(group.title))).toEqual([
      'Work and Projects',
      'People and Organization',
      'Governance and Access',
      'Tools and Resources',
      'System and Account'
    ])
  })

  it('keeps the collaboration workspace with projects and tasks', () => {
    expect(navGroups[0].items.map((item) => item.path)).toEqual(['/', '/discussions', '/work-items', '/scheduled-tasks'])
  })

  it('keeps the canonical Mission pages addressable without duplicating them in primary navigation', () => {
    const navPaths = navGroups.flatMap((group) => group.items).map((item) => item.path)

    expect(routePages.map((page) => page.path)).toContain('/missions')
    expect(routePages.map((page) => page.path)).not.toContain('/mission-room')
    expect(navPaths).not.toContain('/missions')
    expect(hasRouteAccess('/missions/mission-1', ['mission.read'])).toBe(true)
    expect(hasRouteAccess('/mission-room/mission-1', ['mission.read'])).toBe(false)
  })

  it('does not expose duplicate business graph or employment pages', () => {
    expect(navGroups[1].items.map((item) => item.path)).toEqual([
      '/people',
      '/org'
    ])
    expect(pageForPath('/business-graph').path).toBe('/')
    expect(pageForPath('/people/employment')).toMatchObject({
      path: '/people/employment',
      title: 'nav.people'
    })
    expect(hasRouteAccess('/people/employment', ['workforce.read', 'org.read', 'roles.read'])).toBe(false)
  })

  it('keeps personal and system preferences in the account group', () => {
    expect(navGroups[4].items.map((item) => item.path)).toEqual([
      '/settings',
      '/settings/scm-connections',
      '/profile'
    ])
  })

  it('exposes the personal profile in account navigation', () => {
    expect(pageForPath('/profile')).toMatchObject({
      path: '/profile',
      title: 'profile.title',
      description: 'profile.description'
    })
    expect(navGroups.flatMap((group) => group.items).map((item) => item.path)).toContain('/profile')
  })

  it('keeps personal assistant settings within the profile navigation surface', () => {
    expect(routePages.map((page) => page.path)).not.toContain('/personal-assistant')
    expect(navGroups[4].items.map((item) => item.path)).toEqual([
      '/settings',
      '/settings/scm-connections',
      '/profile'
    ])
  })

  it('labels the scheduler surface as automation for users', () => {
    expect(createTranslator('zh-CN')(pageForPath('/scheduled-tasks').title)).toBe('自动化')
    expect(createTranslator('zh-CN')(pageForPath('/scheduled-tasks').description)).toContain('自动化任务')
  })

  it('separates governance controls from resource registries', () => {
    expect(navGroups[2].items.map((item) => item.path)).toEqual([
      '/permissions', '/audit', '/evaluation'
    ])
    expect(navGroups[3].items.map((item) => item.path)).toEqual([
      '/resources/tools',
      '/resources/skills',
      '/resources/mcp',
      '/resources/memory',
      '/resources/ontology',
      '/resources/knowledge'
    ])
  })

  it('maps dynamic detail URLs to their owning surface', () => {
    expect(pageForPath('/todos')).toMatchObject({ path: '/', title: 'nav.home' })
    expect(pageForPath('/ideas')).toMatchObject({ path: '/', title: 'nav.home' })
    expect(pageForPath('/todos/todo-1').title).toBe('workhub.detail.task')
    expect(pageForPath('/ideas/idea-1').title).toBe('workhub.detail.idea')
    expect(pageForPath('/resources/mcp/tool-1').title).toBe('nav.mcp')
    expect(pageForPath('/resources/knowledge/kb-1/documents/doc-1').title).toBe('nav.knowledge')
    expect(pageForPath('/resources/skills/skill-1/').title).toBe('nav.skills')
  })

  it('keeps the home page separate from the transaction center', () => {
    expect(routePages.map((page) => page.path)).toContain('/')
    expect(routePages.map((page) => page.path)).toContain('/work-items')
    expect(pageForPath('/').path).toBe('/')
    expect(pageForPath('/').title).toBe('nav.home')
    expect(pageForPath('/work-items').title).toBe('nav.workItems')
  })

  it('does not expose migration stage metadata', () => {
    for (const page of routePages) {
      expect(Object.keys(page).sort()).toEqual(['description', 'path', 'title'])
    }
  })

  it('declares access requirements for every authenticated page', () => {
    for (const page of routePages) {
      expect(Object.hasOwn(routeAccessRequirements, page.path)).toBe(true)
    }
  })

  it('applies route access requirements to nested pages', () => {
    expect(hasRouteAccess('/todos', ['todo.read'])).toBe(false)
    expect(hasRouteAccess('/todos/todo-1', ['todo.read'])).toBe(true)
    expect(hasRouteAccess('/todos/todo-1', ['idea.read'])).toBe(false)
    expect(hasRouteAccess('/ideas', ['idea.read'])).toBe(false)
    expect(hasRouteAccess('/ideas/idea-1', ['idea.read'])).toBe(true)
    expect(hasRouteAccess('/ideas/idea-1', ['todo.read'])).toBe(false)
    expect(hasRouteAccess('/work-items', ['todo.read'])).toBe(true)
    expect(hasRouteAccess('/work-items', ['idea.read'])).toBe(true)
    expect(hasRouteAccess('/work-items', ['mission.read'])).toBe(true)
    expect(hasRouteAccess('/work-items', ['settings.read'])).toBe(false)
    expect(hasRouteAccess('/settings/scm-connections', ['settings.read'])).toBe(false)
    expect(hasRouteAccess('/settings/scm-connections', ['connection.read'])).toBe(true)
    expect(hasRouteAccess('/settings/approval-workflows', ['approvals.read'])).toBe(false)
    expect(hasRouteAccess('/settings/approval-workflows', ['settings.read'])).toBe(false)
    expect(hasRouteAccess('/approvals/workflows', ['approvals.read'])).toBe(true)
    expect(hasRouteAccess('/approvals/workflows', ['settings.read'])).toBe(false)
    expect(hasRouteAccess('/resources/skills/skill-1', ['resource.read'])).toBe(true)
    expect(hasRouteAccess('/resources/skills/skill-1', ['mission.read'])).toBe(false)
    expect(hasRouteAccess('/discussion/mission-1', ['discussion.read'])).toBe(false)
    expect(hasRouteAccess('/discussion/mission-1', ['discussion.read', 'mission.read'])).toBe(false)
    expect(hasRouteAccess('/resources/knowledge/new', ['knowledge.read'])).toBe(false)
    expect(hasRouteAccess('/resources/knowledge/new', ['knowledge.manage'])).toBe(true)
    expect(hasRouteAccess('/permissions', ['permissions.read'])).toBe(false)
    expect(hasRouteAccess('/permissions', ['permissions.read', 'roles.read'])).toBe(true)
    expect(hasRouteAccess('/permissions', ['operations.read'])).toBe(false)
    expect(hasRouteAccess('/audit', ['operations.read'])).toBe(false)
    expect(hasRouteAccess('/audit', ['audit.read'])).toBe(true)
    expect(hasRouteAccess('/responsibilities', ['operations.read'])).toBe(true)
    expect(hasRouteAccess('/responsibilities', ['operations.manage'])).toBe(true)
    expect(hasRouteAccess('/responsibilities', ['mission.read'])).toBe(false)
    expect(hasRouteAccess('/profile', [])).toBe(true)
    expect(hasRouteAccess('/personal-assistant', [])).toBe(false)
    expect(hasRouteAccess('/memory', ['resource.read'])).toBe(false)
    expect(hasRouteAccess('/resources/ontology/graph', ['operations.read'])).toBe(false)
    expect(hasRouteAccess('/unregistered-page', ['operations.manage'])).toBe(false)
  })

  it('keeps approval workflows out of first-level navigation', () => {
    const controlPaths = visibleNavGroups(['approvals.read'])
      .flatMap((group) => group.items)
      .map((item) => item.path)

    expect(controlPaths).toEqual([
      '/work-items',
      '/profile'
    ])
    expect(navGroups.flatMap((group) => group.items).map((item) => item.path))
      .not.toContain('/settings/approval-workflows')
    expect(navGroups.flatMap((group) => group.items).map((item) => item.path))
      .not.toContain('/approvals/workflows')
    expect(createTranslator('zh-CN')(pageForPath('/approvals/workflows').title)).toBe('审批流程')
  })

  it('filters inaccessible items and empty groups from navigation', () => {
    const groups = visibleNavGroups(['mission.read'])

    expect(groups.map((group) => group.title)).toEqual(['nav.group.work', 'nav.group.account'])
    expect(groups[0].items.map((item) => item.path)).toEqual(['/work-items'])
    expect(groups[1].items.map((item) => item.path)).toEqual(['/profile'])
  })

  it('keeps source connections discoverable for connection-only operators', () => {
    const groups = visibleNavGroups(['connection.read'])

    expect(groups.map((group) => group.title)).toEqual(['nav.group.account'])
    expect(groups[0].items.map((item) => item.path)).toEqual(['/settings/scm-connections', '/profile'])
  })

  it('uses People as the primary navigation for people and their responsibilities', () => {
    const navPaths = navGroups.flatMap((group) => group.items).map((item) => item.path)

    expect(navPaths).toContain('/people')
    expect(navPaths).not.toContain('/responsibilities')
    expect(routePages.map((page) => page.path)).toContain('/responsibilities')
    expect(createTranslator('zh-CN')(pageForPath('/people').title)).toBe('人员与责任')
  })

})
