import { fireEvent, render } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SidebarHarness from './SidebarHarness.svelte'
import { queryClient } from '#lib/query-client'
import { setLocale } from '#lib/i18n'
import { applySession } from '#lib/stores/session.svelte'

const fullSidebarScopes = [
  'todo.read',
  'todo.write',
  'idea.read',
  'discussion.read',
  'mission.read',
  'scheduler.read',
  'resource.read',
  'operations.read',
  'knowledge.read',
  'approvals.read',
  'memory.read',
  'settings.read',
  'workforce.read',
  'org.read',
  'roles.read'
]

describe('Sidebar', () => {
  beforeEach(() => {
    queryClient.clear()
    setLocale('en-US')
    applyTestSession(fullSidebarScopes)
    globalThis.fetch = async () => new Response('{}')
  })

  it('renders navigation without loading discussion and approval lists for badges', async () => {
    const fetchMock = vi.fn(async () => new Response('{}'))
    globalThis.fetch = fetchMock
    const { container } = render(SidebarHarness)

    expect((container.querySelector('[data-app-sidebar]') as HTMLElement).className).toContain('w-[236px]')
    expect(link(container, '/discussions')).toBeTruthy()
    expect(link(container, '/approvals')).toBeNull()
    expect(link(container, '/work-items')).toBeTruthy()
    await Promise.resolve()
    expect(badgeRequests(fetchMock)).toEqual([])
  })

  it('hides inaccessible menu items without making badge requests', async () => {
    const fetchMock = vi.fn(async () => new Response('{}'))
    globalThis.fetch = fetchMock
    applyTestSession(['mission.read'])
    const { container } = render(SidebarHarness)

    expect(link(container, '/discussions')).toBeNull()
    expect(link(container, '/approvals')).toBeNull()
    expect(link(container, '/settings')).toBeNull()
    await Promise.resolve()
    expect(badgeRequests(fetchMock)).toEqual([])
  })

  it('provides a real create shortcut and filters navigation locally', async () => {
    const { container } = render(SidebarHarness)

    expect(link(container, '/?tab=team&create=task')).toBeTruthy()
    const search = container.querySelector('input[type="search"]') as HTMLInputElement
    await fireEvent.input(search, { target: { value: 'skill' } })

    expect(link(container, '/resources/skills')).toBeTruthy()
    expect(link(container, '/discussions')).toBeNull()
  })

  it('hides the create shortcut from read-only users', () => {
    applyTestSession(['todo.read', 'idea.read'])
    const { container } = render(SidebarHarness)

    expect(link(container, '/?tab=team&create=task')).toBeNull()
    expect(link(container, '/')).toBeTruthy()
    expect(link(container, '/work-items')).toBeTruthy()
  })

  it('keeps the create shortcut when the todo page is writable', () => {
    applyTestSession(['todo.read', 'todo.write'])
    const { container } = render(SidebarHarness)

    expect(link(container, '/?tab=team&create=task')).toBeTruthy()
    expect(link(container, '/')).toBeTruthy()
    expect(link(container, '/work-items')).toBeTruthy()
    expect(link(container, '/ideas')).toBeNull()
  })

  it('keeps the transaction center active on idea, todo, and Mission details', () => {
    const { container } = render(SidebarHarness, { props: { currentPath: '/todos/todo-1' } })

    expect(link(container, '/work-items')?.getAttribute('aria-current')).toBe('page')
    expect(link(container, '/')?.getAttribute('aria-current')).toBeNull()
  })
})

function link(container: HTMLElement, href: string) {
  return container.querySelector(`a[href="${href}"]`)
}

function badgeRequests(fetchMock: ReturnType<typeof vi.fn>) {
  return fetchMock.mock.calls
    .map(([input]) => String(input))
    .filter((url) => url.includes('/api/discussions') || url.includes('/api/approvals'))
}

function applyTestSession(permissionScope: string[]) {
  applySession({
    permissionScope,
    expiresAt: '2026-12-31T00:00:00Z',
    user: {
      userId: 'user-1',
      username: 'operator',
      displayName: 'Operator',
      nickname: 'Operator',
      email: 'operator@example.com',
      agentProfileId: 'human-operator',
      role: 'operator'
    }
  })
}
