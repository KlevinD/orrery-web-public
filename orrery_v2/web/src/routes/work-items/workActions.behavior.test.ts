import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import { session } from '#lib/stores/session.svelte'
import { page } from '$app/stores'
import type { Writable } from 'svelte/store'
vi.mock('$app/stores', async () => {
  const { writable } = await import('svelte/store')
  return { page: writable({ url: new URL('https://orrery.test/work-items') }) }
})
vi.mock('$app/navigation', () => ({ goto: vi.fn(async (target: string) => { (page as unknown as Writable<any>).set({ url: new URL(target, 'https://orrery.test') }) }), beforeNavigate: vi.fn(), afterNavigate: vi.fn() }))
import Harness from '#lib/test/WorkCenterHarness.svelte'

let requests: Array<{ url: string; method: string; body: any }>
let human: any, approval: any, approvalFailure: boolean, submissionFailure: boolean, historyCount: number, workflow: any, workflowConflict: boolean
beforeEach(() => {
  setLocale('zh-CN'); requests = []; approvalFailure = false; submissionFailure = false; historyCount = 1; workflow = undefined; workflowConflict = false
  session.user.agentProfileId = 'human-1'; session.user.userId = 'user-1'; session.viewerKind = 'human'; session.authenticated = true
  session.permissionScope = ['todo.read', 'todo.write', 'approvals.read', 'approvals.decide']
  human = { todo_id: 'human-todo', title: '请补充封面图片', description: '请提交所需图片或说明。', status: 'inbox', priority: 'medium', assignee_id: 'human-1', assignee_name: '处理人', assignee_kind: 'human', created_by: 'agent-1', work_kind: 'general', automation_mode: 'manual', version: 1, created_at: '2026-09-10T00:00:00Z', updated_at: '2026-09-10T00:00:00Z', attachments: [], source: { conversation_id: 'private-conversation', can_open: false, delivery_status: 'delivered' } }
  approval = { id: 'decision-1', tenant_id: 'tenant', requester: { kind: 'agent', id: 'agent-1' }, action: 'tool.execute', resource: { kind: 'tool', id: 'tool-1' }, args_hash: 'hash', risk_level: 'low', route: { mode: 'approval', reasons: [] }, context: { bundle_id: 'bundle', complete: true }, payload_preview: { title: '审核本期内容', summary: '请确认内容可以进入下一步。', approval_assigned_to: 'human-1' }, status: 'pending', created_at: '2026-09-10T00:00:00Z' }
  ;(page as unknown as Writable<any>).set({ url: new URL('https://orrery.test/work-items') })
  globalThis.fetch = vi.fn(async (input, init) => {
    const url = String(input), method = init?.method || 'GET', body = typeof init?.body === 'string' ? JSON.parse(init.body) : init?.body
    requests.push({ url, method, body })
    let data: any = {}
    if (url.includes('/api/notifications/summary')) data = { total: 0, unread: 0 }
    else if (url === '/api/hil/workflow-tasks/task-1/decision' && method === 'POST') {
      if (workflowConflict) return new Response(JSON.stringify({ message: '审批内容已更新，请刷新' }), { status: 409 })
      workflow.instance.status = 'approved'; workflow.instance.version = 4; workflow.tasks[0].status = 'approved'; approval.status = 'resolved'; data = workflow
    }
    else if (url === '/api/hil/workflow-instances/workflow-1/viewed') data = {}
    else if (url === '/api/hil/workflow-instances/workflow-1') data = workflow
    else if (url.startsWith('/api/hil/workflow-instances')) data = { items: workflow ? [workflow] : [], total: workflow ? 1 : 0 }
    else if (url.startsWith('/api/hil/decisions?')) {
      if (approvalFailure) return new Response(JSON.stringify({ message: 'temporarily unavailable' }), { status: 503 })
      const params = new URLSearchParams(url.split('?')[1]), offset = Number(params.get('offset') || 0)
      const visible = params.get('mode') === 'approval' && (!params.get('status') || params.get('status') === approval.status)
      const count = visible ? historyCount : 0
      data = { items: Array.from({ length: Math.max(0, Math.min(100, count - offset)) }, (_, i) => ({ ...approval, id: offset + i === 0 ? approval.id : `decision-${offset + i + 1}` })), total: count }
    } else if (url === '/api/hil/decisions/decision-1/decision' && method === 'POST') { approval.status = 'resolved'; data = { request: approval } }
    else if (url === '/api/hil/decisions/decision-1') data = approval
    else if (url.startsWith('/api/approvals?')) data = { items: [], page: { total: 0, returned: 0 } }
    else if (url === '/api/todos/human-todo/attachments' && method === 'POST') { const file = (body as FormData).get('file') as File; const attachment = { attachment_id: 'image-1', file_name: file?.name || 'cover.png', content_type: 'image/png', size_bytes: 6, download_url: '/api/todos/human-todo/attachments/image-1' }; human.attachments = [attachment]; data = attachment }
    else if (url === '/api/todos/human-todo/move' && method === 'POST') {
      if (submissionFailure) return new Response(JSON.stringify({ message: 'Please retry' }), { status: 503 })
      human = { ...human, status: 'done', version: 2, feedback: { body: body.result, attachments: human.attachments, author_id: 'human-1', created_at: '2026-09-10T01:00:00Z' } }; data = human
    } else if (url.includes('/api/todos/human-todo/comments')) data = { comments: [], total: 0 }
    else if (url === '/api/todos/human-todo') data = human
    else if (url.startsWith('/api/todos?')) { const params = new URLSearchParams(url.split('?')[1]); const visible = human.status !== 'done' || params.get('include_completed') === 'true'; data = { todos: visible ? [human] : [], total: visible ? 1 : 0 } }
    return new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } })
  })
})
afterEach(cleanup)

describe('work center processing', () => {
  it('opens a human handoff in the center, retains feedback on failure and completes through Todo', async () => {
    render(Harness)
    await fireEvent.click(await screen.findByRole('button', { name: /请补充封面图片/ }))
    const input = await screen.findByRole('textbox', { name: '你的反馈' })
    await fireEvent.input(input, { target: { value: '已提供所需信息' } })
    submissionFailure = true
    await fireEvent.click(screen.getByRole('button', { name: /提交反馈并完成/ }))
    await screen.findByRole('alert')
    expect((input as HTMLTextAreaElement).value).toBe('已提供所需信息')
    submissionFailure = false
    await fireEvent.click(screen.getByRole('button', { name: /提交反馈并完成/ }))
    await screen.findByText('已提供所需信息')
    expect(requests.filter(request => request.url.endsWith('/move')).at(-1)?.body).toMatchObject({ status: 'done', result: '已提供所需信息', version: 1 })
    expect(requests.some(request => request.method === 'POST' && request.url.includes('/decision'))).toBe(false)
    expect(screen.queryByRole('link', { name: /来源对话/ })).toBeNull()
  })
  it('processes the exact HIL approval through the canonical endpoint inside the center', async () => {
    ;(page as unknown as Writable<any>).set({ url: new URL('https://orrery.test/work-items?view=inbox&action=approval%3Adecision-1') })
    render(Harness)
    await fireEvent.click(await screen.findByRole('button', { name: /^批准$|^通过$/ }))
    const dialog = await screen.findByRole('dialog')
    await fireEvent.click(within(dialog).getByRole('button', { name: /^批准$|^通过$/ }))
    await screen.findByText('处理结果已保存')
    expect(requests.filter(request => request.method === 'POST' && request.url.includes('/decision')).map(request => request.url)).toEqual(['/api/hil/decisions/decision-1/decision'])
    expect(requests.some(request => request.url.startsWith('/api/approvals/') && request.method === 'POST')).toBe(false)
  })
  it('keeps human work usable while another source fails', async () => {
    approvalFailure = true
    render(Harness)
    await screen.findByText(/部分事项暂时无法加载/)
    await fireEvent.click(screen.getByRole('button', { name: /请补充封面图片/ }))
    await screen.findByRole('textbox', { name: '你的反馈' })
  })
  it('loads every approval history page and shows completed human feedback alongside it', async () => {
    historyCount = 125; approval.status = 'resolved'; human.status = 'done'
    ;(page as unknown as Writable<any>).set({ url: new URL('https://orrery.test/work-items?view=history') })
    render(Harness)
    await waitFor(() => expect(requests.some(request => request.url.includes('mode=approval') && request.url.includes('offset=100'))).toBe(true))
    await screen.findByText('共 126')
    expect(requests.some(request => request.url.includes('/api/todos?') && request.url.includes('include_completed=true'))).toBe(true)
  })
  it('does not fetch unauthorized work sources for a Todo-only user', async () => {
    session.permissionScope = ['todo.read', 'todo.write']
    render(Harness)
    await screen.findByRole('button', { name: /请补充封面图片/ })
    expect(requests.some(request => request.url.includes('/api/approvals') || request.url.includes('/api/hil/'))).toBe(false)
  })
  it('submits an image without requiring text and keeps the attachment in the feedback', async () => {
    const { container } = render(Harness)
    await fireEvent.click(await screen.findByRole('button', { name: /请补充封面图片/ }))
    await screen.findByRole('textbox', { name: '你的反馈' })
    await fireEvent.change(container.querySelector('input[type=file]')!, { target: { files: [new File(['image'], 'cover.png', { type: 'image/png' })] } })
    await screen.findByText('cover.png')
    await waitFor(() => expect(screen.getByRole('button', { name: /提交反馈并完成/ }).hasAttribute('disabled')).toBe(false))
    await fireEvent.click(screen.getByRole('button', { name: /提交反馈并完成/ }))
    await waitFor(() => expect(requests.find(request => request.url.endsWith('/move'))?.body).toMatchObject({ result: '', result_attachment_ids: ['image-1'], version: 1 }))
    await waitFor(() => expect(container.querySelector('textarea')).toBeNull())
    expect(screen.getByText('cover.png')).toBeTruthy()
  })
  it('keeps approvals read-only without decision permission', async () => {
    session.permissionScope = ['approvals.read']
    workflow = workflowFixture(approval)
    ;(page as unknown as Writable<any>).set({ url: new URL('https://orrery.test/work-items?action=approval%3Adecision-1') })
    render(Harness)
    await screen.findByText('内容审核')
    expect(screen.queryByRole('button', { name: /^批准$|^通过$|^拒绝$/ })).toBeNull()
    expect(requests.some(request => request.method === 'POST')).toBe(false)
  })
  it('uses the selected workflow task and version and leaves stale approval conflicts visible', async () => {
    workflow = workflowFixture(approval)
    ;(page as unknown as Writable<any>).set({ url: new URL('https://orrery.test/work-items?workflow_id=workflow-1') })
    render(Harness)
    await fireEvent.click(await screen.findByRole('button', { name: /^批准$/ }))
    workflowConflict = true
    const dialog = await screen.findByRole('dialog')
    await fireEvent.click(within(dialog).getByRole('button', { name: /确认批准/ }))
    await screen.findByText('审批内容已更新，请刷新')
    const writes = requests.filter(request => request.method === 'POST' && request.url.includes('/decision'))
    expect(writes).toHaveLength(1)
    expect(writes[0]).toMatchObject({ url: '/api/hil/workflow-tasks/task-1/decision', body: { decision: 'approve', expected_instance_version: 3 } })
    expect(screen.queryByText('处理结果已保存')).toBeNull()
  })
})

function workflowFixture(decision: any) {
  return {
    case: { id: 'case-1', requester: { kind: 'agent', id: 'agent-1' }, action_type_id: 'tool_use', resource: { kind: 'task', id: 'task-1' } },
    instance: { id: 'workflow-1', case_id: 'case-1', revision: 1, decision_request_id: decision.id, definition_hash: 'hash', material_hash: 'material', args_hash: 'args', status: 'running', version: 3, created_at: '2026-09-10T00:00:00Z' },
    decision,
    stages: [{ id: 'stage-1', definition: { key: 'review', name: '内容审核', order: 1, type: 'approval' }, status: 'active', resolved_participants: [{ kind: 'human', id: 'human-1' }] }],
    tasks: [{ id: 'task-1', stage_instance_id: 'stage-1', original_assignee: { kind: 'human', id: 'human-1' }, current_assignee: { kind: 'human', id: 'human-1' }, status: 'pending', transfer_count: 0 }],
    audit: [], notifications: [], receipts: []
  }
}
