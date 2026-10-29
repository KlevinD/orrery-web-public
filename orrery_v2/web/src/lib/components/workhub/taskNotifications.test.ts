import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { writable } from 'svelte/store'
import { applySession, clearSession } from '#lib/stores/session.svelte'
import { setLocale } from '#lib/i18n'
import type { NotificationItem, NotificationSource } from '#lib/api/notifications'
import Harness from '#lib/test/TaskNotificationsHarness.svelte'

const route = vi.hoisted(() => ({ store: undefined as ReturnType<typeof writable> | undefined }))
vi.mock('$app/stores', async () => {
  const { writable } = await import('svelte/store')
  route.store = writable({ url: new URL('https://orrery.example/notifications') })
  return { page: route.store }
})

type Request = { path: string; method: string; body: unknown; signal?: AbortSignal | null }
let calls: Request[]
let item: NotificationItem
let source: NotificationSource
let handler: (request: Request) => Response | Promise<Response> | undefined
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status })
const failure = (message: string, status = 503) => json({ message }, status)

function signIn(scopes: string[] = [], username = 'reader') {
  applySession({ permissionScope: scopes, expiresAt: '2026-12-31T00:00:00Z', user: {
    userId: username, username, displayName: username, nickname: username, email: '',
    agentProfileId: 'human-shared-profile', role: 'member'
  } })
}
function navigate(search: string) {
  route.store!.set({ url: new URL('https://orrery.example/notifications' + search) })
}
async function tab(name: string) {
  await fireEvent.click(screen.getByRole('button', { name }))
}

beforeEach(() => {
  setLocale('en-US')
  signIn()
  navigate('')
  calls = []
  handler = () => undefined
  source = { id: 'source/1', tenant_id: 'org-a', origin_tenant: 'org-a', task_id: 'task-1',
    title: 'Daily research', agent_id: 'agent-research', enabled: true, audience: 'subscribers',
    people: [], include_failures: false, revision: 7, activated_at: '2026-10-18T01:00:00Z', updated_at: '2026-10-18T01:00:00Z' }
  item = { id: 'notice/1', read: false, reason: 'subscription', pushes: [{ channel: 'feishu', status: 'queued' }],
    event: { id: 'event-1', source_id: source.id, task_id: source.task_id, run_id: 'run-1', tenant_id: 'org-a',
      title: 'Daily research', agent_id: 'agent-research', status: 'partial', summary: '<img src=x onerror=alert(1)> Public report',
      created_at: '2026-10-18T02:00:00Z', artifacts: [{ id: 'file/1', name: 'Report.txt', size_bytes: 3 }] } }
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | globalThis.Request, init?: RequestInit) => {
    const url = new URL(String(input), 'https://orrery.example')
    const request = { path: url.pathname + url.search, method: init?.method ?? 'GET',
      body: init?.body ? JSON.parse(String(init.body)) : undefined, signal: init?.signal }
    calls.push(request)
    const override = await handler(request)
    if (override) return override
    if (url.pathname === '/api/notifications') return json({ items: [item], total: 1, unread: item.read ? 0 : 1, limit: 25, offset: 0 })
    if (url.pathname === '/api/notifications/items/notice%2F1') {
      if (request.method === 'PATCH') { item.read = true; return json({ read: true }) }
      return json(item)
    }
    if (url.pathname.endsWith('/artifacts/file%2F1')) return new Response('abc')
    if (url.pathname.endsWith('/sources')) return json([source])
    if (url.pathname.endsWith('/sources/candidates')) return json([])
    if (url.pathname.endsWith('/members')) return json([{ id: 'human-1', name: 'Reader', channels: ['feishu'] }])
    if (url.pathname.endsWith('/channels')) return json(['feishu'])
    if (url.pathname.endsWith('/preferences')) return json({ channels: [] })
    if (url.pathname.endsWith('/subscription')) return json(request.body)
    if (url.pathname.endsWith('/sources/source%2F1')) return json(source)
    if (url.pathname.endsWith('/events')) return json([])
    throw new Error('Unexpected request: ' + request.path)
  }))
})
afterEach(() => { cleanup(); clearSession(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('task notifications', () => {
  it('lets a member read an escaped report without loading operational or management APIs', async () => {
    const { container } = render(Harness)
    await fireEvent.click(await screen.findByRole('button', { name: /Daily research/ }))
    await screen.findByText('Read in app')
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByText('Feishu · Queued')).toBeTruthy()
    expect(screen.queryByText('Delivered')).toBeNull()
    expect(screen.getByText('Report author: agent-research')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Task notification settings' })).toBeNull()
    expect(calls.every(r => r.path.startsWith('/api/notifications'))).toBe(true)
    expect(calls.some(r => /members|candidates|deliveries/.test(r.path))).toBe(false)
    expect(calls.find(r => r.method === 'PATCH')?.body).toEqual({ read: true })
  })

  it('retains unread state when saving fails and allows an explicit retry', async () => {
    let reject = true
    handler = r => r.method === 'PATCH' && reject ? failure('Read state was not saved') : undefined
    render(Harness)
    await fireEvent.click(await screen.findByRole('button', { name: /Daily research/ }))
    await screen.findByRole('alert')
    expect(screen.getByText('Unread in app')).toBeTruthy()
    expect(screen.queryByText('Read in app')).toBeNull()
    reject = false
    await fireEvent.click(screen.getByRole('button', { name: 'Retry marking as read' }))
    await screen.findByText('Read in app')
  })

  it('persists subscription choices through the encoded source URL without rerunning work', async () => {
    navigate('?tab=subscriptions')
    render(Harness)
    await screen.findByText('Daily research')
    await fireEvent.click(screen.getByLabelText('Subscribe to this task'))
    await fireEvent.click(screen.getByLabelText(/Feishu/))
    await fireEvent.click(screen.getByLabelText('Include failed results with a public Agent report'))
    await tab('Save subscription')
    await screen.findByRole('status')
    expect(calls.find(r => r.method === 'PUT')).toMatchObject({ path: '/api/notifications/sources/source%2F1/subscription',
      body: { enabled: true, channels: ['feishu'], include_failures: true } })
    expect(calls.some(r => /scheduled|run-now/.test(r.path))).toBe(false)
    expect(screen.queryByText(/without results/)).toBeNull()
  })

  it('blocks preference saving after a failed load and recovers after refresh', async () => {
    let reject = true
    handler = r => r.path.endsWith('/preferences') && reject ? failure('Unavailable') : undefined
    navigate('?tab=preferences')
    render(Harness)
    await screen.findByRole('alert')
    expect((screen.getByRole('button', { name: 'Save preferences' }) as HTMLButtonElement).disabled).toBe(true)
    expect(calls.some(r => r.method === 'PUT')).toBe(false)
    reject = false
    await tab('Refresh')
    await waitFor(() => expect((screen.getByRole('button', { name: 'Save preferences' }) as HTMLButtonElement).disabled).toBe(false))
    await fireEvent.click(screen.getByLabelText(/Feishu/))
    await tab('Save preferences')
    await screen.findByRole('status')
    expect(calls.find(r => r.method === 'PUT')?.body).toEqual({ channels: ['feishu'] })
  })

  it('ignores a forged management tab for an ordinary member', async () => {
    navigate('?tab=manage')
    render(Harness)
    await screen.findByRole('button', { name: /Daily research/ })
    expect(screen.queryByText('Configured tasks')).toBeNull()
    expect(calls.some(r => /members|candidates|deliveries/.test(r.path))).toBe(false)
  })

  it('preserves a conflicted rule draft and reloads the server revision before retry', async () => {
    signIn(['scheduler.write'])
    navigate('?tab=manage')
    let conflict = true
    handler = r => {
      if (r.path.endsWith('/sources/source%2F1') && r.method === 'PUT') {
        if (conflict) return failure('Rule changed; reload before saving', 409)
        return json({ ...source, ...(r.body as object), revision: 9 })
      }
      return undefined
    }
    render(Harness)
    await fireEvent.click(await screen.findByRole('button', { name: /Daily research/ }))
    await screen.findByRole('button', { name: 'Save notification rule' })
    await screen.findByRole('option', { name: 'Choose a task…' })
    await waitFor(() => expect((screen.getByRole('button', { name: 'Save notification rule' }) as HTMLButtonElement).disabled).toBe(false))
    await fireEvent.click(screen.getByLabelText('Everyone in the organization'))
    await tab('Save notification rule')
    await screen.findByText('Rule changed; reload before saving')
    expect(screen.queryByRole('status')).toBeNull()
    expect((screen.getByLabelText('Everyone in the organization') as HTMLInputElement).checked).toBe(true)
    source.revision = 8
    await tab('Discard changes and reload rule')
    await waitFor(() => expect((screen.getByLabelText('Subscribers only') as HTMLInputElement).checked).toBe(true))
    conflict = false
    await tab('Save notification rule')
    await screen.findByRole('status')
    expect(calls.filter(r => r.method === 'PUT').map(r => (r.body as NotificationSource).revision)).toEqual([7, 8])
    expect(calls.filter(r => r.method === 'PUT').every(r => !Object.hasOwn(r.body as object, 'subscription'))).toBe(true)
  })

  it('drops a late report response and aborts the request when the security context changes', async () => {
    let resolve!: (response: Response) => void
    handler = r => r.path.endsWith('/items/notice%2F1') ? new Promise<Response>(r => { resolve = r }) : undefined
    render(Harness)
    await fireEvent.click(await screen.findByRole('button', { name: /Daily research/ }))
    await waitFor(() => expect(resolve).toBeTypeOf('function'))
    const old = calls.find(r => r.path.endsWith('/items/notice%2F1'))!
    const privateItem = structuredClone(item)
    item.event.title = 'New account report'
    signIn([], 'another-reader')
    await screen.findByRole('button', { name: /New account report/ })
    expect(old.signal?.aborted).toBe(true)
    resolve(json(privateItem))
    await new Promise(r => setTimeout(r, 20))
    expect(screen.queryByText('Report author: agent-research')).toBeNull()
    expect(screen.queryByText('Daily research')).toBeNull()
    expect(calls.some(r => r.method === 'PATCH')).toBe(false)
  })

  it('keeps management saving disabled while member loading fails', async () => {
    signIn(['scheduler.write'])
    navigate('?tab=manage')
    handler = r => r.path.endsWith('/members') ? failure('Members unavailable') : undefined
    render(Harness)
    await fireEvent.click(await screen.findByRole('button', { name: /Daily research/ }))
    await screen.findByRole('alert')
    expect((screen.getByRole('button', { name: 'Save notification rule' }) as HTMLButtonElement).disabled).toBe(true)
    expect(calls.some(r => r.method === 'PUT')).toBe(false)
  })

  it('uses the source identity when candidates share a task ID across origins', async () => {
    signIn(['scheduler.write'])
    navigate('?tab=manage')
    const candidate = { ...source, id: 'org-b/task-1', origin_tenant: 'org-b', title: 'Other origin task', revision: 0 }
    handler = r => {
      if (r.path.endsWith('/sources/candidates')) return json([candidate])
      if (r.path.endsWith('/sources') && r.method === 'POST') return json({ ...candidate, revision: 1 })
      return undefined
    }
    render(Harness)
    await screen.findByRole('option', { name: 'Other origin task' })
    await fireEvent.change(screen.getByLabelText('Enable another task'), { target: { value: candidate.id } })
    await waitFor(() => expect((screen.getByRole('button', { name: 'Save notification rule' }) as HTMLButtonElement).disabled).toBe(false))
    await tab('Save notification rule')
    await screen.findByRole('status')
    expect(calls.find(r => r.method === 'POST')?.body).toMatchObject({ id: candidate.id, origin_tenant: 'org-b', task_id: 'task-1', enabled: true })
  })

  it('clears management state immediately when permissions are revoked', async () => {
    signIn(['operations.manage'])
    navigate('?tab=manage')
    render(Harness)
    await fireEvent.click(await screen.findByRole('button', { name: /Daily research/ }))
    await screen.findByText('Who receives results')
    signIn([])
    await screen.findByRole('button', { name: /Daily research/ })
    expect(screen.queryByText('Who receives results')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Task notification settings' })).toBeNull()
  })

  it('sends the unread filter and resets pagination through a real inbox query', async () => {
    render(Harness)
    await screen.findByRole('button', { name: /Daily research/ })
    await fireEvent.click(screen.getByLabelText('Unread only'))
    await waitFor(() => expect(calls.some(r => r.path === '/api/notifications?limit=25&offset=0&unread=true')).toBe(true))
  })

  it('does not load notifications without an authenticated human session', async () => {
    clearSession()
    render(Harness)
    await Promise.resolve()
    expect(calls).toHaveLength(0)
  })
})
