import { afterEach, describe, expect, it, vi } from 'vitest'
import { todoApi } from './todos'

describe('todoApi', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('calls the production todo endpoints for every command', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    ))
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000010')

    await todoApi.listTodos('?status=inbox&q=ship')
    await todoApi.getTodo('todo-1')
    await todoApi.createTodo({
      title: 'Ship homepage',
      description: 'Todo list',
      status: 'planned',
      tags: ['frontend', 'ai']
    })
    await todoApi.updateTodo('todo-1', {
      title: 'Ship full homepage',
      tags: ['release']
    })
    await todoApi.claimTodo('todo-1', { assignee_id: 'human-pm', assignee_name: 'PM' })
    await todoApi.moveTodo('todo-1', { status: 'in_progress' })
    await todoApi.completeTodo('todo-1')
    await todoApi.cancelTodo('todo-1')
    await todoApi.reopenTodo('todo-1')
    await todoApi.deleteTodo('todo-1')
    await todoApi.suggestDraft({
      kind: 'idea',
      content: 'Capture smarter triage',
      current_tags: ['inbox']
    })
    await todoApi.listTodoComments('todo-1', '?limit=20&offset=0')
    await todoApi.createTodoComment('todo-1', { body: 'Looks good' })
    await todoApi.getWorkHubSummary('Asia/Shanghai')

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/todos?status=inbox&q=ship',
      '/api/todos/todo-1',
      '/api/todos',
      '/api/todos/todo-1',
      '/api/todos/todo-1/claim',
      '/api/todos/todo-1/move',
      '/api/todos/todo-1/complete',
      '/api/todos/todo-1/cancel',
      '/api/todos/todo-1/reopen',
      '/api/todos/todo-1',
      '/api/todo-drafts/suggest',
      '/api/todos/todo-1/comments?limit=20&offset=0',
      '/api/todos/todo-1/comments',
      '/api/work-hub/summary?timezone=Asia%2FShanghai'
    ])
    expect(fetchMock.mock.calls.map(([, init]) => init?.method)).toEqual([
      undefined,
      undefined,
      'POST',
      'PATCH',
      'POST',
      'POST',
      'POST',
      'POST',
      'POST',
      'DELETE',
      'POST',
      undefined,
      'POST',
      undefined
    ])
    expect(fetchMock.mock.calls[2][1]?.body).toBe(JSON.stringify({
      title: 'Ship homepage',
      description: 'Todo list',
      status: 'planned',
      tags: ['frontend', 'ai']
    }))
    expect(fetchMock.mock.calls[3][1]?.body).toBe(JSON.stringify({
      title: 'Ship full homepage',
      tags: ['release']
    }))
    expect(fetchMock.mock.calls[4][1]?.body).toBe(JSON.stringify({ assignee_id: 'human-pm', assignee_name: 'PM' }))
    expect(fetchMock.mock.calls[5][1]?.body).toBe(JSON.stringify({ status: 'in_progress' }))
    expect(fetchMock.mock.calls[10][1]?.body).toBe(JSON.stringify({
      kind: 'idea',
      content: 'Capture smarter triage',
      current_tags: ['inbox']
    }))
    expect(fetchMock.mock.calls[12][1]?.body).toBe(JSON.stringify({ body: 'Looks good' }))
  })
})
