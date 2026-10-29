import { describe, expect, it, vi } from 'vitest'
import { ideaApi } from '#lib/api/ideas'
import { buildIdeaQuery, createIdeaMutationRegistry, ideaQueryKeys } from './ideas'
import { todoQueryKeys } from './todos'
import type { IdeaItemDTO } from '#lib/types/idea'

describe('idea queries', () => {
  it('serializes filters and cache keys', () => {
    expect(buildIdeaQuery({
      status: 'submitted',
      q: 'planning note',
      limit: 10,
      offset: 20
    })).toBe('?status=submitted&q=planning+note&limit=10&offset=20')
    expect(ideaQueryKeys.list({ status: 'archived' })).toEqual(['ideas', '?status=archived'])
    expect(ideaQueryKeys.detail('idea-1')).toEqual(['ideas', 'idea-1'])
  })

  it('invalidates idea and todo lists after mutations', async () => {
    const invalidateQueries = vi.fn()
    const registry = createIdeaMutationRegistry({ invalidateQueries } as any)
    const item = ideaFixture()
    vi.spyOn(ideaApi, 'createIdea').mockResolvedValue(item)
    vi.spyOn(ideaApi, 'updateIdea').mockResolvedValue({ ...item, title: 'Updated' })
    vi.spyOn(ideaApi, 'transitionIdea').mockResolvedValue({ ...item, status: 'under_review' })
    vi.spyOn(ideaApi, 'voteIdea').mockResolvedValue({ ...item, vote_count: 1, viewer_has_voted: true })
    vi.spyOn(ideaApi, 'archiveIdea').mockResolvedValue({ ...item, status: 'archived' })
    vi.spyOn(ideaApi, 'restoreIdea').mockResolvedValue(item)
    vi.spyOn(ideaApi, 'convertIdeaToTodo').mockResolvedValue({
      idea: { ...item, status: 'converted', converted_todo_id: 'todo-2' },
      todo: {
        todo_id: 'todo-2',
        title: item.title,
        status: 'planned',
        priority: 'medium',
        work_kind: 'general',
        automation_mode: 'manual',
        created_by: 'superadmin',
        created_at: item.created_at,
        updated_at: item.updated_at,
        version: 1
      }
    })
    vi.spyOn(ideaApi, 'deleteIdea').mockResolvedValue(undefined)
    vi.spyOn(ideaApi, 'createIdeaComment').mockResolvedValue({
      comment_id: 'comment-1',
      issue_kind: 'idea',
      issue_id: 'idea-1',
      body: 'Idea reply',
      author_id: 'user-1',
      created_at: '2026-06-16T10:00:00Z',
      updated_at: '2026-06-16T10:00:00Z'
    })

    await registry.createIdea({ title: 'Capture idea' })
    await registry.updateIdea('idea-1', { title: 'Updated' })
    await registry.transitionIdea('idea-1', { status: 'under_review' })
    await registry.voteIdea('idea-1')
    await registry.archiveIdea('idea-1')
    await registry.restoreIdea('idea-1')
    await registry.convertIdeaToTodo('idea-1', { todo_title: 'Todo from idea' })
    await registry.deleteIdea('idea-1')
    await registry.createIdeaComment('idea-1', { body: 'Idea reply' })

    expect(ideaApi.createIdea).toHaveBeenCalledWith({ title: 'Capture idea' }, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(ideaApi.updateIdea).toHaveBeenCalledWith('idea-1', { title: 'Updated' }, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(ideaApi.archiveIdea).toHaveBeenCalledWith('idea-1', expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(ideaApi.restoreIdea).toHaveBeenCalledWith('idea-1', expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(ideaApi.convertIdeaToTodo).toHaveBeenCalledWith('idea-1', { todo_title: 'Todo from idea' }, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(ideaApi.deleteIdea).toHaveBeenCalledWith('idea-1', undefined, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(ideaApi.createIdeaComment).toHaveBeenCalledWith('idea-1', { body: 'Idea reply' }, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ideaQueryKeys.root })
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ideaQueryKeys.detail('idea-1') })
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['todos'] })
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['todos', 'todo-2'] })
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: todoQueryKeys.summaryRoot })
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ideaQueryKeys.comments('idea-1') })
  })
})

function ideaFixture(overrides: Partial<IdeaItemDTO> = {}): IdeaItemDTO {
  return {
    idea_id: 'idea-1',
    title: 'Capture idea',
    content: 'Useful later',
    status: 'submitted',
    visibility: 'organization',
    impact: 'medium',
    effort: 'medium',
    vote_count: 0,
    viewer_has_voted: false,
    created_by: 'superadmin',
    created_at: '2026-06-16T10:00:00Z',
    updated_at: '2026-06-16T10:00:00Z',
    version: 1,
    ...overrides
  }
}
