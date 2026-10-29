import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import { session } from '#lib/stores/session.svelte'
import { page } from '$app/stores'
import type { Writable } from 'svelte/store'
vi.mock('$app/stores', async () => {
  const { writable } = await import('svelte/store')
  return { page: writable({ url: new URL('https://orrery.example/work-items?view=activity') }) }
})
import Harness from '#lib/test/WorkCenterHarness.svelte'
const note = { id: 'notice-1', read: false, reason: 'organization', pushes: [], event: { id: 'event-1', task_id: 'daily', run_id: 'run-1', title: '每日报告', status: 'completed', summary: '已生成可下载的日报。', created_at: '2026-09-10T02:00:00Z', artifacts: [] } }
const result = { task_title: '每日报告', is_read: false, attention_required: false, run: { scheduled_task_id: 'daily', scheduled_run_id: 'run-1', status: 'completed', result: { summary: note.event.summary }, created_at: note.event.created_at } }
let requested: Array<{ url: string; method: string }>
beforeEach(() => {
  setLocale('zh-CN'); requested = []
  session.user.agentProfileId = 'human-1'; session.user.userId = 'user-1'; session.permissionScope = ['scheduler.read']; session.authenticated = true
  ;(page as unknown as Writable<any>).set({ url: new URL('https://orrery.example/work-items?view=activity') })
  globalThis.fetch = vi.fn(async (input, init) => {
    const url = String(input); const method = init?.method || 'GET'; requested.push({ url, method })
    let data: unknown = {}
    if (url.includes('/api/notifications/summary')) data = { total: 1, unread: note.read ? 0 : 1 }
    else if (url.includes('/api/notifications/items/notice-1')) { if (method === 'PATCH') note.read = true; data = note }
    else if (url.includes('/api/notifications?')) data = { items: [note], total: 1, unread: note.read ? 0 : 1, offset: 0, limit: 25 }
    else if (url.includes('/api/scheduled-runs/run-1/read')) { result.is_read = true; data = { scheduled_run_id: 'run-1', is_read: true } }
    else if (url.includes('/api/scheduled-runs')) data = { items: [result], total: 1, unread_total: result.is_read ? 0 : 1 }
    else if (url.includes('/api/todos')) data = { todos: [{ todo_id: 'approval-todo', title: '请确认采购', status: 'inbox', priority: 'high', created_at: '2026-09-10T02:00:00Z' }], total: 1 }
    return new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } })
  })
  note.read = false; result.is_read = false
})
afterEach(cleanup)
describe('unified work center', () => {
  it('renders one card for a notification and its run, then clears both read receipts', async () => {
    const { container } = render(Harness)
    await fireEvent.click(await screen.findByRole('button', { name: /每日报告/ }))
    await waitFor(() => expect(requested.filter(request => request.method === 'PATCH').map(request => request.url)).toEqual(expect.arrayContaining(['/api/notifications/items/notice-1', '/api/scheduled-runs/run-1/read'])))
    expect(container.querySelectorAll('[data-activity-row]')).toHaveLength(1)
    expect(screen.getByRole('link', { name: '需我处理' }).getAttribute('href')).toBe('/work-items?view=inbox')
    expect(screen.getByRole('link', { name: '全部事务' })).toBeTruthy()
  })
  it('keeps notification-only users inside the center without loading operational data', async () => {
    session.permissionScope = []
    ;(page as unknown as Writable<any>).set({ url: new URL('https://orrery.example/work-items') })
    render(Harness)
    await fireEvent.click(await screen.findByRole('button', { name: /每日报告/ }))
    await waitFor(() => expect(note.read).toBe(true))
    expect(requested.some(request => request.url.includes('/api/scheduled-') || request.url.includes('/api/approvals'))).toBe(false)
    expect(screen.queryByRole('button', { name: '任务通知设置' })).toBeNull()
  })
  it('opens a legacy report destination and preserves pending work after reading', async () => {
    session.permissionScope = ['scheduler.read', 'todo.read']
    ;(page as unknown as Writable<any>).set({ url: new URL('https://orrery.example/work-items?view=activity&item=notice-1') })
    render(Harness)
    await waitFor(() => expect(note.read).toBe(true))
    ;(page as unknown as Writable<any>).set({ url: new URL('https://orrery.example/work-items?view=inbox') })
    await screen.findByText('请确认采购')
    expect(requested.some(request => request.url.includes('/api/todos') && request.method !== 'GET')).toBe(false)
  })
})
