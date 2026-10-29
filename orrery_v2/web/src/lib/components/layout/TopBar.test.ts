import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import TopBarHarness from './TopBarHarness.svelte'
import { queryClient } from '#lib/query-client'
import { theme, setThemeBrand } from '#lib/stores/theme.svelte'
import { setLocale } from '#lib/i18n'
import { applySession, session } from '#lib/stores/session.svelte'
import { navigateToIdentityLogout } from '#lib/routes/identityLoginNavigation'

vi.mock('#lib/routes/identityLoginNavigation', () => ({
  navigateToIdentityLogout: vi.fn()
}))

const fullTopBarScopes = ['discussion.read', 'approvals.read', 'settings.read']

describe('TopBar', () => {
  beforeEach(() => {
    queryClient.clear()
    localStorage.clear()
    document.documentElement.classList.remove('light', 'dark', 'brand-ocean', 'brand-forest', 'brand-purple')
    theme.mode = 'dark'
    setThemeBrand('default')
    setLocale('en-US')
    vi.mocked(navigateToIdentityLogout).mockClear()
    applyTestSession(fullTopBarScopes)
    globalThis.fetch = async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/hil/decisions')) {
        return new Response(JSON.stringify({
          items: [{
            id: 'decision-council-1', tenant_id: 'system', requester: { kind: 'agent', id: 'agent-alpha' }, action: 'Publish launch plan',
            resource: { kind: 'mission', id: 'mission-1' }, args_hash: 'sha256:council', risk_level: 'high',
            route: { mode: 'council', reasons: ['multi-party review required'] },
            context: { bundle_id: 'bundle-council', complete: true }, status: 'pending', council_id: 'council-1', created_at: '2026-06-02T00:00:00Z'
          }],
          total: 1
        }))
      }
      if (url.includes('/api/approvals')) {
        return new Response(
          JSON.stringify({
            items: Array.from({ length: 4 }, (_, index) => ({
              approval_id: `approval-${index}`,
              approval_type: 'task_completion',
              status: 'pending',
              source_type: 'task',
              source_id: `task-${index}`,
              title: 'Task completion',
              requested_by: 'agent-alpha',
              available_actions: [],
              created_at: '2026-06-02T00:00:00Z'
            })),
            page: { total: 4, returned: 4 }
          })
        )
      }
      if (url.includes('/api/discussions')) {
        return new Response(JSON.stringify({ discussions: [] }))
      }
      return new Response('{}')
    }
  })

  function renderTopBar() {
    return render(TopBarHarness)
  }

  it('renders the current page as a compact breadcrumb', () => {
    renderTopBar()

    expect(screen.queryByRole('img', { name: 'Minvia 明微知径' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Orrery' })).toBeTruthy()
    expect(screen.getByText('Home')).toBeTruthy()
  })

  it('renders localized shell actions', () => {
    renderTopBar()

    expect(screen.getByRole('button', { name: 'Toggle context panel' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'User menu' })).toBeTruthy()
  })

  it('switches the root between dark and light mode from the top navigation', async () => {
    renderTopBar()

    const toggle = screen.getByRole('button', { name: 'Switch to light mode' })
    await fireEvent.click(toggle)

    expect(theme.mode).toBe('light')
    expect(document.documentElement.classList.contains('light')).toBe(true)
    expect(localStorage.getItem('orrery.theme.mode')).toBe('light')
  })

  it('toggles the workspace context panel', async () => {
    renderTopBar()

    expect(screen.getByTestId('context-state').textContent).toBe('open')
    await fireEvent.click(screen.getByRole('button', { name: 'Toggle context panel' }))
    expect(screen.getByTestId('context-state').textContent).toBe('closed')
  })

  it('opens user dropdown menu on click', async () => {
    renderTopBar()

    const userButton = screen.getByRole('button', { name: 'User menu' })
    await fireEvent.click(userButton)

    expect(screen.getByText('Personal settings')).toBeTruthy()
    expect(screen.getByText('Settings')).toBeTruthy()
  })

  it('clears sensitive query data before waiting for logout to finish', async () => {
    let finishLogout: ((response: Response) => void) | undefined
    globalThis.fetch = async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/auth/logout')) {
        return new Promise<Response>((resolve) => {
          finishLogout = resolve
        })
      }
      if (url.includes('/api/approvals')) return new Response(JSON.stringify({ items: [], page: { total: 0, returned: 0 } }))
      if (url.includes('/api/discussions')) return new Response(JSON.stringify({ discussions: [] }))
      return new Response('{}')
    }
    queryClient.setQueryData(['operations', 'audit'], { items: [{ secret: 'high-privilege-data' }] })
    renderTopBar()

    await fireEvent.click(screen.getByRole('button', { name: 'User menu' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Log out' }))

    expect(queryClient.getQueryData(['operations', 'audit'])).toBeUndefined()
    await waitFor(() => expect(finishLogout).toBeDefined())
    finishLogout?.(new Response(null, { status: 204 }))
    await waitFor(() => expect(navigateToIdentityLogout).toHaveBeenCalledWith(
      '/api/auth/identity/kratos/logout?return_to=%2Fapi%2Fauth%2Fidentity%2Fkratos%2Fbegin%3Freturn_to%3D%2F'
    ))
  })

  it('keeps the Kratos client session stable until provider logout navigation', async () => {
    globalThis.fetch = async () => new Response(null, { status: 204 })
    renderTopBar()

    await fireEvent.click(screen.getByRole('button', { name: 'User menu' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Log out' }))

    await waitFor(() => expect(navigateToIdentityLogout).toHaveBeenCalled())
    expect(session.authenticated).toBe(true)
  })

  it('keeps local logout on the Orrery login page', async () => {
    applyTestSession(fullTopBarScopes, 'local')
    globalThis.fetch = async () => new Response(null, { status: 204 })
    renderTopBar()

    await fireEvent.click(screen.getByRole('button', { name: 'User menu' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Log out' }))

    await waitFor(() => expect(navigateToIdentityLogout).not.toHaveBeenCalled())
  })

  it('shows the combined approval and council count on the notification link', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/hil/decisions')) return new Response(JSON.stringify({ items: [], total: 0 }))
      if (url.includes('/api/approvals')) return new Response(JSON.stringify({ items: [], page: { total: 0, returned: 0 } }))
      return new Response('{}')
    })
    globalThis.fetch = fetchMock
    renderTopBar()

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    expect(screen.getByRole('link', { name: /0\+? pending items, 0 unread updates/ })).toBeTruthy()
    expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('/api/approvals?status=pending'), expect.anything())
    expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('/api/hil/decisions?mode=approval&status=pending&mine=true&limit=100'), expect.anything())
    expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('/api/hil/decisions?mode=council&status=pending&mine=true&limit=100'), expect.anything())
  })

  it('renders a notification badge for approvals and council participation', async () => {
    renderTopBar()

    expect(await screen.findByRole('link', { name: '5 pending items, 0 unread updates' })).toBeTruthy()
  })

  it('hides settings and notifications when the account cannot access them', async () => {
    applyTestSession([])
    renderTopBar()

    expect(screen.getByRole('link', { name: /0\+? pending items, 0 unread updates/ }).getAttribute('href')).toBe('/work-items')
    await fireEvent.click(screen.getByRole('button', { name: 'User menu' }))
    expect(screen.getByText('Personal settings')).toBeTruthy()
    expect(screen.queryByText('Settings')).toBeNull()
  })

  it('counts human handoffs as pending and reports unread updates separately', async () => {
    applyTestSession(['todo.read'])
    globalThis.fetch = async input => new Response(JSON.stringify(String(input).includes('/api/todos')
      ? { todos: [{ todo_id: 'human-work', title: 'Provide feedback', status: 'inbox', priority: 'medium', created_at: '2026-09-10T00:00:00Z' }], total: 1 }
      : { total: 7, unread: 7 }))
    renderTopBar()
    expect(await screen.findByRole('link', { name: '1 pending items, 7 unread updates' })).toBeTruthy()
    expect(screen.getByLabelText('Unread updates')).toBeTruthy()
  })
})

function applyTestSession(permissionScope: string[], subjectProvider = 'kratos') {
  applySession({
    subjectProvider,
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
