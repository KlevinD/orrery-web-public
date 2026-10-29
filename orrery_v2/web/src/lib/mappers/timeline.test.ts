import { describe, expect, it } from 'vitest'
import { mapDiscussionView } from './timeline'
import type { DiscussionDTO } from '#lib/types/api'

describe('timeline mapper', () => {
  it('maps discussion messages and reply metadata', () => {
    const view = mapDiscussionView({
      discussion_group_id: 'dg1',
      messages: [{
        message_id: 'msg1',
        discussion_group_id: 'dg1',
        kind: 'update',
        author_id: 'agent-a',
        author_display_name: 'Agent A',
        author_type: 'agent',
        body: 'Done',
        mentions: ['agent-b'],
        metadata: { tool_name: 'search', old_status: 'active', new_status: 'done' },
        posted_at: '2026-06-01T01:00:00Z'
      }],
      pending_mentions: [],
      decisions: [],
      blockers: []
    } satisfies DiscussionDTO)

    expect(view.groupId).toBe('dg1')
    expect(view.messages[0]).toMatchObject({ id: 'msg1', authorName: 'Agent A', toolName: 'search', oldStatus: 'active', newStatus: 'done' })
  })

  it('uses a translatable sentinel when discussion author type is missing', () => {
    const view = mapDiscussionView({
      discussion_group_id: 'dg1',
      messages: [{
        message_id: 'msg1',
        discussion_group_id: 'dg1',
        kind: 'message',
        author_id: 'agent-a',
        body: 'Done',
        posted_at: '2026-06-01T01:00:00Z'
      }],
      pending_mentions: [],
      decisions: [],
      blockers: []
    } satisfies DiscussionDTO)

    expect(view.messages[0].authorType).toBe('Not provided')
  })
})
