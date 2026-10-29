import { describe, expect, it } from 'vitest'
import {
  applyAuthoritativeDiscussionSnapshot,
  applyDiscussionHILModeUpdate,
  applyDiscussionStreamEvent,
  discussionStreamEventIsCorrelated,
  mapDiscussionView
} from './discussions'
import type { DiscussionDTO, DiscussionStreamEventDTO, DiscussionViewModel } from '#lib/types/discussions'

describe('production discussion projection', () => {
  it('keeps concurrent streaming messages isolated by run id', () => {
    let view = mapDiscussionView(groupDiscussion({
      revision: 1,
      runs: [runningRun('run-a', 'turn-a', 'agent-a'), runningRun('run-b', 'turn-b', 'agent-b')]
    }))

    view = apply(view, tokenEvent('evt-a-1', 2, 'turn-a', 'run-a', 1, 'A working'))
    view = apply(view, tokenEvent('evt-b-1', 3, 'turn-b', 'run-b', 1, 'B working'))
    view = apply(view, {
      kind: 'message_completed',
      event_id: 'evt-a-done',
      discussion_id: 'conv-1',
      turn_id: 'turn-a',
      revision: 4,
      message: {
        message_id: 'msg-a',
        discussion_id: 'conv-1',
        turn_id: 'turn-a',
        run_id: 'run-a',
        role: 'assistant',
        author_id: 'agent-a',
        author_name: 'Agent A',
        content: 'A complete',
        created_at: '2026-07-10T00:00:04Z'
      }
    })

    expect(view.messages.map((message) => [message.id, message.content])).toEqual([
      ['streaming-run-b', 'B working'],
      ['msg-a', 'A complete']
    ])
    expect(view.timeline.some((item) => item.id.startsWith('streaming-run-a'))).toBe(false)
    expect(view.timeline.some((item) => item.id === 'streaming-run-b')).toBe(true)
  })

  it('folds group work into progress and preview after a plan starts', () => {
    let view = mapDiscussionView(groupDiscussion({
      revision: 1,
      runs: [runningRun('run-plan', 'turn-plan', 'agent-a')]
    }))

    view = apply(view, tokenEvent('evt-plan-token-1', 2, 'turn-plan', 'run-plan', 1, 'Inspecting repository. '))
    view = apply(view, planEvent(3))
    view = apply(view, tokenEvent('evt-plan-token-2', 4, 'turn-plan', 'run-plan', 2, 'Running verification.'))

    expect(view.messages.some((message) => message.id.startsWith('streaming-run-plan'))).toBe(false)
    expect(view.runs[0].hasPlan).toBe(true)
    expect(view.runs[0].preview).toBe('Inspecting repository. Running verification.')
    expect(view.timeline.some((item) => item.kind === 'run' && item.run?.id === 'run-plan')).toBe(true)

    view = apply(view, {
      kind: 'message_completed',
      event_id: 'evt-plan-summary',
      discussion_id: 'conv-1',
      turn_id: 'turn-plan',
      revision: 5,
      message: {
        message_id: 'msg-summary',
        discussion_id: 'conv-1',
        turn_id: 'turn-plan',
        run_id: 'run-plan',
        role: 'assistant',
        author_id: 'agent-a',
        author_name: 'Agent A',
        content: 'Verification completed successfully.',
        created_at: '2026-07-10T00:00:05Z'
      }
    })

    expect(view.messages.map((message) => message.content)).toEqual(['Verification completed successfully.'])
    expect(view.timeline.filter((item) => item.kind === 'message').map((item) => item.message?.content)).toEqual(['Verification completed successfully.'])
  })

  it('keeps direct chat output visible even when the run has a plan', () => {
    let view = mapDiscussionView({
      ...groupDiscussion({ revision: 1, runs: [runningRun('run-direct', 'turn-direct', 'agent-a')] }),
      conversation_kind: 'direct'
    })

    view = apply(view, {
      ...planEvent(2),
      event_id: 'evt-direct-plan',
      turn_id: 'turn-direct',
      run_id: 'run-direct'
    })
    view = apply(view, tokenEvent('evt-direct-token', 3, 'turn-direct', 'run-direct', 1, 'I am inspecting every step.'))

    expect(view.messages.at(-1)?.content).toBe('I am inspecting every step.')
    expect(view.timeline.some((item) => item.id === 'streaming-run-direct')).toBe(true)
    expect(view.timeline.some((item) => item.kind === 'run' && item.run?.id === 'run-direct')).toBe(true)
  })

  it('deduplicates event ids and rejects stale token sequences', () => {
    let view = mapDiscussionView(groupDiscussion({
      revision: 1,
      runs: [runningRun('run-a', 'turn-a', 'agent-a')]
    }))
    const first = tokenEvent('evt-token-1', 2, 'turn-a', 'run-a', 1, 'one')

    view = apply(view, first)
    view = apply(view, first)
    view = apply(view, tokenEvent('evt-token-stale', 3, 'turn-a', 'run-a', 1, ' duplicate'))
    view = apply(view, tokenEvent('evt-token-2', 4, 'turn-a', 'run-a', 2, ' two'))

    expect(view.messages.at(-1)?.content).toBe('one two')
    expect(view.streamState?.seenEventIds).toEqual(['evt-token-1', 'evt-token-stale', 'evt-token-2'])
  })

  it('replaces local projections only with a current authoritative snapshot', () => {
    const current = apply(
      mapDiscussionView(groupDiscussion({ revision: 5, runs: [runningRun('run-a', 'turn-a', 'agent-a')] })),
      tokenEvent('evt-token-1', 5, 'turn-a', 'run-a', 1, 'local progress')
    )
    const stale = mapDiscussionView(groupDiscussion({ revision: 4, status: 'running' }))
    const fresh = mapDiscussionView(groupDiscussion({
      revision: 6,
      status: 'completed',
      messages: [{
        message_id: 'msg-final',
        discussion_id: 'conv-1',
        turn_id: 'turn-a',
        run_id: 'run-a',
        role: 'assistant',
        author_id: 'agent-a',
        content: 'server result',
        created_at: '2026-07-10T00:00:06Z'
      }]
    }))

    expect(applyAuthoritativeDiscussionSnapshot(current, stale)).toBe(current)
    const next = applyAuthoritativeDiscussionSnapshot(current, fresh)
    expect(next.revision).toBe(6)
    expect(next.messages.map((message) => message.content)).toEqual(['server result'])
    expect(next.messages.some((message) => message.id.startsWith('streaming-'))).toBe(false)
  })

  it('requires stable correlation before projecting incremental production events', () => {
    expect(discussionStreamEventIsCorrelated({ kind: 'token_delta', run_id: 'run-a', sequence: 1, delta: 'x' }, 'conv-1')).toBe(false)
    expect(discussionStreamEventIsCorrelated(tokenEvent('evt-1', 2, 'turn-a', 'run-a', 1, 'x'), 'conv-1')).toBe(true)
  })

  it('applies a HIL mode update without replacing discussion content', () => {
    const current = mapDiscussionView(groupDiscussion({
      revision: 5,
      hil_mode: 'manual',
      messages: [{
        message_id: 'msg-1',
        discussion_id: 'conv-1',
        role: 'assistant',
        author_id: 'agent-a',
        content: 'Keep this content',
        created_at: '2026-07-10T00:00:01Z'
      }]
    }))

    const updated = applyDiscussionHILModeUpdate(current, {
      discussion_id: 'conv-1',
      revision: 6,
      hil_mode: 'auto',
      updated_at: '2026-07-10T00:00:06Z'
    })

    expect(updated.hilMode).toBe('auto')
    expect(updated.revision).toBe(6)
    expect(updated.messages).toEqual(current.messages)
    expect(updated.runs).toEqual(current.runs)
  })
})

function apply(view: DiscussionViewModel, event: DiscussionStreamEventDTO) {
  const next = applyDiscussionStreamEvent(view, event)
  if (!next) throw new Error('expected discussion projection')
  return next
}

function groupDiscussion(overrides: Partial<DiscussionDTO> = {}): DiscussionDTO {
  return {
    discussion_id: 'conv-1',
    conversation_kind: 'group',
    revision: 1,
    title: 'Production flow',
    status: 'running',
    created_by: 'user-1',
    created_at: '2026-07-10T00:00:00Z',
    updated_at: '2026-07-10T00:00:00Z',
    unread_completed: false,
    messages: [],
    members: [
      { kind: 'agent', id: 'agent-a', display_name: 'Agent A' },
      { kind: 'agent', id: 'agent-b', display_name: 'Agent B' }
    ],
    runs: [],
    linked_missions: [],
    ...overrides
  }
}

function runningRun(runId: string, turnId: string, agentId: string) {
  return {
    run_id: runId,
    discussion_id: 'conv-1',
    turn_id: turnId,
    mode: 'group',
    has_plan: false,
    agent_id: agentId,
    status: 'running',
    runtime_kind: 'codex',
    created_at: '2026-07-10T00:00:01Z'
  }
}

function tokenEvent(eventId: string, revision: number, turnId: string, runId: string, sequence: number, delta: string): DiscussionStreamEventDTO {
  return {
    kind: 'token_delta',
    event_id: eventId,
    discussion_id: 'conv-1',
    turn_id: turnId,
    revision,
    run_id: runId,
    sequence,
    delta
  }
}

function planEvent(revision: number): DiscussionStreamEventDTO {
  return {
    kind: 'plan',
    event_id: `evt-plan-${revision}`,
    discussion_id: 'conv-1',
    turn_id: 'turn-plan',
    revision,
    run_id: 'run-plan',
    plan: {
      plan_id: 'plan-1',
      status: 'active',
      items: [
        { id: 'step-1', text: 'Inspect repository', status: 'completed' },
        { id: 'step-2', text: 'Run verification', status: 'in_progress' }
      ]
    }
  }
}
