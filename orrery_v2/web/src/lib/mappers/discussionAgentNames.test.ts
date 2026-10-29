import { describe, expect, it } from 'vitest'
import { applyDiscussionStreamEvent, mapDiscussionView } from './discussions'

describe('discussion Agent display names', () => {
  it('uses the run event display name before a new member snapshot arrives', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-07-22T04:53:00Z',
      updated_at: '2026-07-22T04:53:00Z',
      unread_completed: false,
      messages: [],
      members: [],
      runs: [],
      linked_missions: []
    })

    const afterRun = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-1',
        discussion_id: 'conv-1',
        agent_id: 'profile-f54c4401bfb058eadd0e48a5a878b4e4',
        agent_name: 'Codex',
        status: 'running',
        runtime_kind: 'codex',
        created_at: '2026-07-22T04:53:17Z'
      }
    })
    const afterToken = applyDiscussionStreamEvent(afterRun, {
      kind: 'token_delta',
      delta: 'Hi'
    })

    expect(afterToken?.activeResponders).toEqual(['Codex'])
    expect(afterToken?.messages.at(-1)?.authorName).toBe('Codex')
    expect(afterToken?.activeResponders).not.toContain('profile-f54c4401bfb058eadd0e48a5a878b4e4')
  })

  it('uses a personal assistant logical identity for a streamed host reply', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Mission',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-07-22T04:53:00Z',
      updated_at: '2026-07-22T04:53:00Z',
      unread_completed: false,
      messages: [],
      members: [],
      runs: [],
      linked_missions: []
    })

    const afterRun = applyDiscussionStreamEvent(view, {
      kind: 'run_started',
      run: {
        run_id: 'run-pa-host',
        discussion_id: 'conv-1',
        agent_id: 'lead-agent',
        logical_agent_id: 'pa-tenant-a-account-1',
        agent_name: 'Personal Assistant',
        status: 'running',
        runtime_kind: 'codex',
        created_at: '2026-07-22T04:53:17Z'
      }
    })
    const afterToken = applyDiscussionStreamEvent(afterRun, {
      kind: 'token_delta',
      run_id: 'run-pa-host',
      delta: 'Working.'
    })

    expect(afterToken?.messages.at(-1)).toMatchObject({
      authorId: 'pa-tenant-a-account-1',
      authorName: 'Personal Assistant'
    })
  })

  it('does not expose an Agent id when old events have no display name', () => {
    const view = mapDiscussionView({
      discussion_id: 'conv-1',
      title: 'Launch',
      status: 'running',
      created_by: 'user-1',
      created_at: '2026-07-22T04:53:00Z',
      updated_at: '2026-07-22T04:53:00Z',
      unread_completed: false,
      messages: [],
      members: [],
      runs: [{
        run_id: 'run-legacy',
        discussion_id: 'conv-1',
        agent_id: 'legacy-agent-id',
        status: 'running',
        runtime_kind: 'codex',
        created_at: '2026-07-22T04:53:17Z'
      }],
      linked_missions: []
    })

    expect(view.activeResponders).toEqual(['Agent'])
    expect(view.runs[0].agentName).toBe('Agent')
  })
})
