import { describe, expect, it, vi } from 'vitest'
import { todoApi } from '#lib/api/todos'
import { buildTodoQuery, createTodoMutationRegistry, todoQueryKeys } from './todos'
import type { TodoItemDTO } from '#lib/types/todo'

describe('todo queries', () => {
  it('serializes filters and cache keys', () => {
    expect(buildTodoQuery({
      status: 'inbox',
      assigneeId: 'human-pm',
      q: 'release notes',
      mine: true,
      unassigned: true,
      limit: 20,
      offset: 40
    })).toBe('?status=inbox&assignee_id=human-pm&q=release+notes&mine=true&unassigned=true&limit=20&offset=40')
    expect(todoQueryKeys.list({ status: 'done' })).toEqual(['todos', '?status=done'])
    expect(todoQueryKeys.detail('todo-1')).toEqual(['todos', 'todo-1'])
    expect(todoQueryKeys.summary('Asia/Shanghai')).toEqual(['todos', 'work-hub-summary', 'Asia/Shanghai'])
  })

  it('invalidates todo lists after every mutation', async () => {
    const invalidateQueries = vi.fn()
    const registry = createTodoMutationRegistry({ invalidateQueries } as any)
    const item = todoFixture()
    vi.spyOn(todoApi, 'createTodo').mockResolvedValue(item)
    vi.spyOn(todoApi, 'updateTodo').mockResolvedValue({ ...item, title: 'Updated' })
    vi.spyOn(todoApi, 'claimTodo').mockResolvedValue({ ...item, assignee_id: 'human-pm' })
    vi.spyOn(todoApi, 'moveTodo').mockResolvedValue({ ...item, status: 'in_progress' })
    vi.spyOn(todoApi, 'completeTodo').mockResolvedValue({ ...item, status: 'done' })
    vi.spyOn(todoApi, 'cancelTodo').mockResolvedValue({ ...item, status: 'canceled' })
    vi.spyOn(todoApi, 'reopenTodo').mockResolvedValue(item)
    vi.spyOn(todoApi, 'deleteTodo').mockResolvedValue(undefined)
    vi.spyOn(todoApi, 'createTodoComment').mockResolvedValue({
      comment_id: 'comment-1',
      issue_kind: 'todo',
      issue_id: 'todo-1',
      body: 'Looks good',
      author_id: 'user-1',
      created_at: '2026-06-16T10:00:00Z',
      updated_at: '2026-06-16T10:00:00Z'
    })
    vi.spyOn(todoApi, 'suggestDraft').mockResolvedValue({ title: 'Use inbox signals', tags: ['inbox'] })

    await registry.createTodo({ title: 'Ship homepage' })
    await registry.updateTodo('todo-1', { title: 'Updated' })
    await registry.claimTodo('todo-1', { assignee_id: 'human-pm' })
    await registry.moveTodo('todo-1', { status: 'in_progress' })
    await registry.completeTodo('todo-1')
    await registry.cancelTodo('todo-1')
    await registry.reopenTodo('todo-1')
    await registry.deleteTodo('todo-1')
    await registry.createTodoComment('todo-1', { body: 'Looks good' })
    await registry.suggestDraft({ kind: 'idea', content: 'Use inbox signals', current_tags: ['inbox'] })

    expect(todoApi.createTodo).toHaveBeenCalledWith({ title: 'Ship homepage' }, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(todoApi.updateTodo).toHaveBeenCalledWith('todo-1', { title: 'Updated' }, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(todoApi.claimTodo).toHaveBeenCalledWith('todo-1', { assignee_id: 'human-pm' }, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(todoApi.moveTodo).toHaveBeenCalledWith('todo-1', { status: 'in_progress' }, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(todoApi.completeTodo).toHaveBeenCalledWith('todo-1', undefined, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(todoApi.cancelTodo).toHaveBeenCalledWith('todo-1', undefined, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(todoApi.reopenTodo).toHaveBeenCalledWith('todo-1', undefined, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(todoApi.deleteTodo).toHaveBeenCalledWith('todo-1', undefined, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(todoApi.createTodoComment).toHaveBeenCalledWith('todo-1', { body: 'Looks good' }, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(todoApi.suggestDraft).toHaveBeenCalledWith(
      { kind: 'idea', content: 'Use inbox signals', current_tags: ['inbox'] },
      expect.objectContaining({ idempotencyKey: expect.any(String) })
    )
    expect(invalidateQueries).toHaveBeenCalledTimes(17)
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: todoQueryKeys.root })
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: todoQueryKeys.detail('todo-1') })
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: todoQueryKeys.comments('todo-1') })
  })
})

function todoFixture(overrides: Partial<TodoItemDTO> = {}): TodoItemDTO {
  return {
    todo_id: 'todo-1',
    title: 'Ship homepage',
    description: 'Todo list',
    status: 'inbox',
    priority: 'medium',
    work_kind: 'general',
    automation_mode: 'manual',
    created_by: 'superadmin',
    created_at: '2026-06-15T10:00:00Z',
    updated_at: '2026-06-15T10:00:00Z',
    version: 1,
    ...overrides
  }
}
