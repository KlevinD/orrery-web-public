import { describe, expect, it, vi } from 'vitest'
import {
  applyRealtimeEnvelope,
  conversationRealtimeChannel,
  createDiscussionRealtimeSubscription,
  discussionStreamEventFromEnvelope
} from './discussionSubscription'
import type { RealtimeEnvelope } from './client'

describe('discussion realtime subscription helpers', () => {
  it('builds conversation channels', () => {
    expect(conversationRealtimeChannel('conv-1')).toBe('conversation:conv-1')
  })

  it('deduplicates envelopes by event id', () => {
    const onEvent = vi.fn()
    const onRecover = vi.fn()
    const envelope = discussionEnvelope({ event_id: 'evt-1', version: 1, type: 'discussion.run.started' })
    let state = applyRealtimeEnvelope(undefined, envelope, { onEvent, onRecover })
    state = applyRealtimeEnvelope(state, envelope, { onEvent, onRecover })

    expect(state.lastVersion).toBe(1)
    expect(onEvent).toHaveBeenCalledTimes(1)
    expect(onRecover).not.toHaveBeenCalled()
  })

  it('requests snapshot refetch when channel version has a gap', () => {
    const onEvent = vi.fn()
    const onRecover = vi.fn()
    const first = discussionEnvelope({ event_id: 'evt-1', version: 1, type: 'discussion.message.created' })
    const gap = discussionEnvelope({ event_id: 'evt-3', version: 3, type: 'discussion.run.completed' })

    const state = applyRealtimeEnvelope(applyRealtimeEnvelope(undefined, first, { onEvent, onRecover }), gap, {
      onEvent,
      onRecover
    })

    expect(state.lastVersion).toBe(3)
    expect(onEvent).toHaveBeenCalledTimes(1)
    expect(onRecover).toHaveBeenCalledWith('version_gap')
  })

  it('ignores stale envelopes that arrive after a newer channel version', () => {
    const onEvent = vi.fn()
    const onRecover = vi.fn()
    const newer = discussionEnvelope({ event_id: 'evt-3', version: 3, type: 'discussion.run.completed' })
    const stale = discussionEnvelope({ event_id: 'evt-2', version: 2, type: 'discussion.tool.result' })

    const state = applyRealtimeEnvelope(applyRealtimeEnvelope(undefined, newer, { onEvent, onRecover }), stale, {
      onEvent,
      onRecover
    })

    expect(state.lastVersion).toBe(3)
    expect(onEvent).toHaveBeenCalledTimes(1)
    expect(onRecover).not.toHaveBeenCalled()
  })

  it('subscribes to conversation channel and handles publications', () => {
    const subscription = new FakeSubscription()
    const client = new FakeRealtimeClient(subscription)
    const onEvent = vi.fn()
    const onRecover = vi.fn()

    const unsubscribe = createDiscussionRealtimeSubscription({
      client,
      discussionId: 'conv-1',
      onEvent,
      onRecover
    })
    subscription.publish(discussionEnvelope({ event_id: 'evt-1', version: 1, type: 'discussion.message.completed' }))
    unsubscribe()

    expect(client.channel).toBe('conversation:conv-1')
    expect(subscription.subscribed).toBe(true)
    expect(subscription.unsubscribed).toBe(true)
    expect(onEvent).toHaveBeenCalledTimes(1)
  })

  it('maps websocket token delta envelopes to discussion stream events', () => {
    const event = discussionStreamEventFromEnvelope(discussionEnvelope({
      event_id: 'evt-token',
      type: 'discussion.token.delta',
      payload: {
        conversation_id: 'conv-1',
        revision: 1,
        turn_id: 'turn-1',
        run_id: 'run-1',
        agent_id: 'agent-1',
        sequence: '2',
        delta: '让我换一种方式加载这个文档。'
      }
    }))

    expect(event).toMatchObject({
      event_id: 'evt-token',
      discussion_id: 'conv-1',
      turn_id: 'turn-1',
      revision: 1,
      kind: 'token_delta',
      run_id: 'run-1',
      sequence: 2,
      delta: '让我换一种方式加载这个文档。'
    })
  })

  it('keeps the Agent display name on websocket run events', () => {
    const event = discussionStreamEventFromEnvelope(discussionEnvelope({
      event_id: 'evt-run',
      type: 'discussion.run.started',
      payload: {
        conversation_id: 'conv-1',
        run_id: 'run-1',
        agent_id: 'profile-f54c4401bfb058eadd0e48a5a878b4e4',
        logical_agent_id: 'pa-tenant-a-account-1',
        agent_name: 'Codex',
        status: 'running'
      }
    }))

    expect(event?.run).toMatchObject({
      agent_id: 'profile-f54c4401bfb058eadd0e48a5a878b4e4',
      logical_agent_id: 'pa-tenant-a-account-1',
      agent_name: 'Codex'
    })
  })

  it('maps websocket reasoning summary envelopes to thinking events', () => {
    const event = discussionStreamEventFromEnvelope(discussionEnvelope({
      event_id: 'evt-thinking',
      type: 'discussion.thinking.delta',
      payload: {
        conversation_id: 'conv-1',
        revision: 2,
        turn_id: 'turn-1',
        run_id: 'run-1',
        sequence: 3,
        delta: 'Checking the workspace.'
      }
    }))

    expect(event).toMatchObject({
      event_id: 'evt-thinking',
      discussion_id: 'conv-1',
      turn_id: 'turn-1',
      revision: 2,
      kind: 'thinking_delta',
      run_id: 'run-1',
      sequence: 3,
      delta: 'Checking the workspace.'
    })
  })

  it.each(['discussion.run.completed', 'discussion.run.failed'])('preserves usage details on %s', (type) => {
    const event = discussionStreamEventFromEnvelope(discussionEnvelope({ type, payload: {
      run_id: 'run-usage', usage: {
        model: { input_tokens: 1000, output_tokens: 100, total_tokens: 1100,
          cached_input_tokens: 800, reasoning_tokens: 0,
          cached_input_tokens_known: true, reasoning_tokens_known: true },
        legacy: { input_tokens: 10 }
      }
    } }))
    expect(event?.run?.usage?.model).toMatchObject({ cached_input_tokens: 800,
      reasoning_tokens: 0, cached_input_tokens_known: true, reasoning_tokens_known: true,
      total_tokens: 1100 })
    expect(event?.run?.usage?.legacy.cached_input_tokens_known).toBeUndefined()
    expect(event?.run?.usage?.legacy.cached_input_tokens).toBeUndefined()
  })

  it('preserves incomplete coverage and rejects malformed detail counters', () => {
    const event = discussionStreamEventFromEnvelope(discussionEnvelope({ type: 'discussion.run.completed', payload: {
      usage: { partial: { cached_input_tokens: '800', cached_input_tokens_known: false },
        invalid: { cached_input_tokens: -1, reasoning_tokens: null },
        empty: { cached_input_tokens: '', reasoning_tokens: true } }
    } }))
    expect(event?.run?.usage?.partial).toMatchObject({ cached_input_tokens: 800, cached_input_tokens_known: false })
    for (const model of ['invalid', 'empty']) {
      expect(event?.run?.usage?.[model].cached_input_tokens).toBeUndefined()
      expect(event?.run?.usage?.[model].reasoning_tokens).toBeUndefined()
    }
  })

  it('maps websocket plan envelopes to discussion stream events', () => {
    const event = discussionStreamEventFromEnvelope(discussionEnvelope({
      event_id: 'evt-plan',
      type: 'discussion.plan.updated',
      payload: {
        conversation_id: 'conv-1',
        revision: 1,
        turn_id: 'turn-1',
        run_id: 'run-1',
        plan: {
          plan_id: 'plan-1',
          status: 'active',
          items: [
            { id: 'step-1', text: 'Create the plan', status: 'completed' },
            { id: 'step-2', text: 'Run verification', status: 'in_progress' }
          ]
        }
      }
    }))

    expect(event).toMatchObject({
      event_id: 'evt-plan',
      discussion_id: 'conv-1',
      turn_id: 'turn-1',
      revision: 1,
      kind: 'plan',
      run_id: 'run-1',
      plan: {
        plan_id: 'plan-1',
        status: 'active',
        items: [
          { id: 'step-1', text: 'Create the plan', status: 'completed' },
          { id: 'step-2', text: 'Run verification', status: 'in_progress' }
        ]
      }
    })
  })

  it('maps lightweight HIL mode updates without a discussion snapshot', () => {
    const event = discussionStreamEventFromEnvelope(discussionEnvelope({
      event_id: 'evt-hil-mode',
      type: 'discussion.hil_mode.updated',
      payload: {
        conversation_id: 'conv-1',
        revision: 8,
        hil_mode: 'auto',
        updated_at: '2026-07-10T00:00:08Z'
      }
    }))

    expect(event).toEqual({
      event_id: 'evt-hil-mode',
      discussion_id: 'conv-1',
      turn_id: '',
      revision: 8,
      kind: 'hil_mode_updated',
      hil_mode: 'auto',
      updated_at: '2026-07-10T00:00:08Z'
    })
  })
})

function discussionEnvelope(overrides: Partial<RealtimeEnvelope> = {}): RealtimeEnvelope {
  return {
    event_id: 'evt-1',
    type: 'discussion.message.created',
    channel: 'conversation:conv-1',
    aggregate_type: 'conversation',
    aggregate_id: 'conv-1',
    version: 1,
    occurred_at: '2026-06-10T14:00:00Z',
    payload: {},
    ...overrides
  }
}

class FakeRealtimeClient {
  channel = ''
  constructor(private readonly subscription: FakeSubscription) {}
  newSubscription(channel: string) {
    this.channel = channel
    return this.subscription
  }
}

class FakeSubscription {
  subscribed = false
  unsubscribed = false
  private publication?: (ctx: { data: RealtimeEnvelope }) => void
  private error?: () => void

  on(event: string, callback: (...args: never[]) => void) {
    if (event === 'publication') this.publication = callback as unknown as (ctx: { data: RealtimeEnvelope }) => void
    if (event === 'error') this.error = callback
    return this
  }

  subscribe() {
    this.subscribed = true
  }

  unsubscribe() {
    this.unsubscribed = true
  }

  publish(envelope: RealtimeEnvelope) {
    this.publication?.({ data: envelope })
  }

  fail() {
    this.error?.()
  }
}
