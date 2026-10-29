import { afterEach, describe, expect, it, vi } from 'vitest'
import { ideaApi } from './ideas'

describe('ideaApi', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('calls the production idea endpoints for every command', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    ))
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000020')

    await ideaApi.listIdeas('?status=submitted&q=planning')
    await ideaApi.getIdea('idea-1')
    await ideaApi.createIdea({
      title: 'Capture insight',
      content: 'Useful later',
      tags: ['planning', 'ai']
    })
    await ideaApi.updateIdea('idea-1', {
      title: 'Refined insight',
      tags: ['experiment']
    })
    await ideaApi.archiveIdea('idea-1')
    await ideaApi.restoreIdea('idea-1')
    await ideaApi.convertIdeaToTodo('idea-1', { todo_title: 'Todo from idea' })
    await ideaApi.deleteIdea('idea-1')
    await ideaApi.listIdeaComments('idea-1', '?limit=20&offset=0')
    await ideaApi.createIdeaComment('idea-1', { body: 'Idea reply' })
    await ideaApi.transitionIdea('idea-1', { status: 'accepted', decision_reason: 'High impact' })
    await ideaApi.voteIdea('idea-1')

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/ideas?status=submitted&q=planning',
      '/api/ideas/idea-1',
      '/api/ideas',
      '/api/ideas/idea-1',
      '/api/ideas/idea-1/archive',
      '/api/ideas/idea-1/restore',
      '/api/ideas/idea-1/convert-to-todo',
      '/api/ideas/idea-1',
      '/api/ideas/idea-1/comments?limit=20&offset=0',
      '/api/ideas/idea-1/comments',
      '/api/ideas/idea-1/transition',
      '/api/ideas/idea-1/vote'
    ])
    expect(fetchMock.mock.calls.map(([, init]) => init?.method)).toEqual([
      undefined,
      undefined,
      'POST',
      'PATCH',
      'POST',
      'POST',
      'POST',
      'DELETE',
      undefined,
      'POST',
      'POST',
      'POST'
    ])
    expect(fetchMock.mock.calls[2][1]?.body).toBe(JSON.stringify({
      title: 'Capture insight',
      content: 'Useful later',
      tags: ['planning', 'ai']
    }))
    expect(fetchMock.mock.calls[3][1]?.body).toBe(JSON.stringify({
      title: 'Refined insight',
      tags: ['experiment']
    }))
    expect(fetchMock.mock.calls[6][1]?.body).toBe(JSON.stringify({ todo_title: 'Todo from idea' }))
    expect(fetchMock.mock.calls[9][1]?.body).toBe(JSON.stringify({ body: 'Idea reply' }))
    expect(fetchMock.mock.calls[10][1]?.body).toBe(JSON.stringify({ status: 'accepted', decision_reason: 'High impact' }))
  })
})
