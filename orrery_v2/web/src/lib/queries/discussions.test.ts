import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  discussionQueryKeys,
  retryDiscussionQuery,
  retryDiscussionMessageStream,
  sendDiscussionMessageStream,
  subscribeDiscussionStream
} from './discussions'
import { ApiClientError } from '#lib/api/client'
import { applySession, clearSession } from '#lib/stores/session.svelte'
import type { DiscussionDTO, DiscussionStreamEventDTO, DiscussionViewModel } from '#lib/types/discussions'
import type { RealtimeEnvelope } from '#lib/realtime/client'

const uuidV4Pattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

const apiMocks = vi.hoisted(() => ({
  getLatestRunDebug: vi.fn(),
  getRunDebug: vi.fn(),
  listDiscussionMessages: vi.fn(),
  sendMessageStream: vi.fn(),
  retryMessageStream: vi.fn(),
  abortDiscussion: vi.fn()
}))
const realtimeMocks = vi.hoisted(() => {
  const handlers = new Map<string, (ctx: { data: RealtimeEnvelope }) => void>()
  const subscription = {
    on: vi.fn((event: string, callback: (ctx: { data: RealtimeEnvelope }) => void) => {
      handlers.set(event, callback)
      return subscription
    }),
    subscribe: vi.fn(),
    unsubscribe: vi.fn()
  }
  const client = {
    newSubscription: vi.fn(() => subscription),
    connect: vi.fn(),
    disconnect: vi.fn()
  }
  return {
    client,
    handlers,
    subscription,
    createRealtimeClient: vi.fn(() => client)
  }
})

vi.mock('#lib/api/discussions', () => ({
  discussionApi: {
    listDiscussions: vi.fn(),
    getDiscussion: vi.fn(),
    getLatestRunDebug: apiMocks.getLatestRunDebug,
    getRunDebug: apiMocks.getRunDebug,
    listDiscussionMessages: apiMocks.listDiscussionMessages,
    sendMessage: vi.fn(),
    sendMessageStream: apiMocks.sendMessageStream,
    retryMessageStream: apiMocks.retryMessageStream,
    abortDiscussion: apiMocks.abortDiscussion
  }
}))

vi.mock('#lib/realtime/client', () => ({
  createRealtimeClient: realtimeMocks.createRealtimeClient
}))

describe('discussion queries', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  beforeEach(() => {
    realtimeMocks.handlers.clear()
    realtimeMocks.client.newSubscription.mockClear()
    realtimeMocks.client.connect.mockClear()
    realtimeMocks.client.disconnect.mockClear()
    realtimeMocks.subscription.on.mockClear()
    realtimeMocks.subscription.subscribe.mockClear()
    realtimeMocks.subscription.unsubscribe.mockClear()
    realtimeMocks.createRealtimeClient.mockClear()
    apiMocks.getLatestRunDebug.mockReset()
    apiMocks.getRunDebug.mockReset()
    apiMocks.sendMessageStream.mockReset()
    apiMocks.retryMessageStream.mockReset()
    apiMocks.abortDiscussion.mockReset()
    applySession({
      accessToken: 'token-1',
      user: {
        userId: 'operator',
        username: 'operator',
        displayName: 'Operator',
        nickname: 'Operator',
        email: '',
        agentProfileId: '',
        role: 'admin'
      },
      permissionScope: ['discussion.read', 'discussion.write'],
      expiresAt: '2999-01-01T00:00:00Z'
    })
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000098')
  })

  it('scopes discussion query keys', () => {
    expect(discussionQueryKeys.list()).toEqual(['discussions', 'list', 1, 20])
    expect(discussionQueryKeys.detail('conv-1')).toEqual(['discussions', 'conv-1'])
    expect(discussionQueryKeys.debug('conv-1')).toEqual(['discussions', 'conv-1', 'debug', 'latest'])
    expect(discussionQueryKeys.debug('conv-1', 'run-1')).toEqual(['discussions', 'conv-1', 'debug', 'run-1'])
  })

  it('does not retry forbidden discussion reads', () => {
    const forbidden = new ApiClientError({ status: 403, code: 'forbidden', message: 'permission denied' })

    expect(retryDiscussionQuery(0, forbidden)).toBe(false)
    expect(retryDiscussionQuery(0, new Error('temporary failure'))).toBe(true)
    expect(retryDiscussionQuery(1, new Error('temporary failure'))).toBe(false)
  })

  it('only invalidates realtime envelopes that need a detail refetch', () => {
    const invalidate = vi.fn()
    const unsubscribe = subscribeDiscussionStream('conv-1', invalidate)

    expect(realtimeMocks.client.newSubscription).toHaveBeenCalledWith('conversation:conv-1')
    expect(realtimeMocks.client.connect).toHaveBeenCalledTimes(1)
    dispatchRealtimeEnvelope({ event_id: 'evt-1', type: 'discussion.message.created', version: 1 })
    dispatchRealtimeEnvelope({ event_id: 'evt-2', type: 'discussion.run.started', version: 2, payload: correlatedRunPayload() })
    dispatchRealtimeEnvelope({ event_id: 'evt-3', type: 'discussion.tool.call', version: 3, payload: correlatedToolPayload() })
    dispatchRealtimeEnvelope({ event_id: 'evt-4', type: 'discussion.tool.result', version: 4, payload: correlatedToolPayload() })
    dispatchRealtimeEnvelope({ event_id: 'evt-5', type: 'discussion.plan.updated', version: 5, payload: correlatedPlanPayload() })
    dispatchRealtimeEnvelope({ event_id: 'evt-6', type: 'discussion.message.completed', version: 6, payload: correlatedMessagePayload() })
    dispatchRealtimeEnvelope({ event_id: 'evt-7', type: 'discussion.run.completed', version: 7, payload: { ...correlatedRunPayload(), status: 'completed' } })
    dispatchRealtimeEnvelope({
      event_id: 'evt-8',
      type: 'discussion.hil_mode.updated',
      version: 8,
      payload: { conversation_id: 'conv-1', revision: 8, hil_mode: 'auto', updated_at: '2026-07-10T00:00:08Z' }
    })

    expect(invalidate).toHaveBeenCalledTimes(4)
    unsubscribe()
    expect(realtimeMocks.subscription.unsubscribe).toHaveBeenCalledTimes(1)
    expect(realtimeMocks.client.disconnect).toHaveBeenCalledTimes(1)
  })

  it('does not open realtime when the session has no viewer credentials', () => {
    clearSession()
    const invalidate = vi.fn()

    const unsubscribe = subscribeDiscussionStream('conv-1', invalidate)
    unsubscribe()

    expect(realtimeMocks.createRealtimeClient).not.toHaveBeenCalled()
    expect(realtimeMocks.client.connect).not.toHaveBeenCalled()
    expect(invalidate).not.toHaveBeenCalled()
  })

  it('forwards parsed discussion realtime events for live rendering', () => {
    const invalidate = vi.fn()
    const onEvent = vi.fn()
    subscribeDiscussionStream('conv-1', invalidate, onEvent)

    dispatchRealtimeEnvelope({
      type: 'discussion.run.started',
      payload: {
        run_id: 'run-1',
        conversation_id: 'conv-1',
        revision: 1,
        turn_id: 'turn-1',
        mode: 'group',
        agent_id: 'agent-beta',
        status: 'running'
      }
    })

    expect(invalidate).not.toHaveBeenCalled()
    expect(onEvent).toHaveBeenCalledWith(expect.objectContaining({
      event_id: 'evt-1',
      discussion_id: 'conv-1',
      turn_id: 'turn-1',
      revision: 1,
      kind: 'run_started',
      run: expect.objectContaining({
        run_id: 'run-1',
        discussion_id: 'conv-1',
        turn_id: 'turn-1',
        mode: 'group',
        agent_id: 'agent-beta',
        status: 'running',
        runtime_kind: '',
        created_at: '2026-06-10T14:00:00Z'
      })
    }))
  })

  it('forwards completed message realtime events for incremental rendering', () => {
    const invalidate = vi.fn()
    const onEvent = vi.fn()
    subscribeDiscussionStream('conv-1', invalidate, onEvent)

    dispatchRealtimeEnvelope({
      type: 'discussion.message.completed',
      payload: {
        conversation_id: 'conv-1',
        revision: 1,
        turn_id: 'turn-1',
        run_id: 'run-1',
        message_id: 'msg-agent',
        role: 'assistant',
        author_id: 'agent-beta',
        author_name: '秦若兰',
        content: '先查一下人才库\n目前有这些成员。',
        'metadata.display_timeline_json': '[{"kind":"text","text":"先查一下人才库"}]'
      }
    })

    expect(invalidate).toHaveBeenCalledTimes(1)
    expect(onEvent).toHaveBeenCalledWith(expect.objectContaining({
      event_id: 'evt-1',
      discussion_id: 'conv-1',
      turn_id: 'turn-1',
      revision: 1,
      kind: 'message_completed',
      message: expect.objectContaining({
        message_id: 'msg-agent',
        discussion_id: 'conv-1',
        turn_id: 'turn-1',
        run_id: 'run-1',
        role: 'assistant',
        author_id: 'agent-beta',
        author_name: '秦若兰',
        content: '先查一下人才库\n目前有这些成员。',
        metadata: { display_timeline_json: '[{"kind":"text","text":"先查一下人才库"}]' },
        created_at: '2026-06-10T14:00:00Z'
      })
    }))
  })

  it('marks the optimistic discussion failed when the send stream rejects', async () => {
    apiMocks.sendMessageStream.mockRejectedValue(new Error('stream disconnected'))
    const discussions: string[] = []
    const errors: Array<string | undefined> = []

    await expect(
      sendDiscussionMessageStream({
        form: { content: 'Plan launch' },
        onDiscussion: (discussion) => {
          discussions.push(discussion.runtimeStatus)
          errors.push(discussion.runtimeError)
        }
      })
    ).rejects.toThrow('stream disconnected')

    expect(discussions).toEqual(['running', 'failed'])
    expect(errors).toEqual([undefined, 'stream disconnected'])
  })

  it('sends discussion streams when crypto.randomUUID is unavailable', async () => {
    vi.stubGlobal('crypto', {})
    apiMocks.sendMessageStream.mockImplementation(async (_discussionId, _body, _options, onEvent) => {
      onEvent({ kind: 'snapshot', discussion: discussionSnapshot() })
    })

    await sendDiscussionMessageStream({
      form: { content: 'Plan launch' },
      onDiscussion: () => {}
    })

    expect(apiMocks.sendMessageStream.mock.calls[0][2]).toMatchObject({
      idempotencyKey: expect.stringMatching(uuidV4Pattern)
    })
  })

  it('keeps the created discussion id when the send stream returns snapshot then error', async () => {
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, _body: unknown, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({
          kind: 'snapshot',
          discussion: {
            discussion_id: 'conv-1',
            title: 'Plan launch',
            status: 'failed',
            created_by: 'operator',
            created_at: '2026-06-02T00:00:00Z',
            updated_at: '2026-06-02T00:00:01Z',
            unread_completed: false,
            messages: [],
            runs: [{
              run_id: 'run-1',
              discussion_id: 'conv-1',
              agent_id: 'lead-agent',
              status: 'failed',
              runtime_kind: 'managed',
              created_at: '2026-06-02T00:00:00Z',
              completed_at: '2026-06-02T00:00:01Z'
            }],
            linked_missions: []
          }
        })
        onEvent(correlatedErrorEvent('discussion runtime unavailable: tool run_terminal not found'))
      }
    )
    const ids: string[] = []
    const discussions: DiscussionViewModel[] = []

    const completed = await sendDiscussionMessageStream({
      form: { content: 'Plan launch' },
      onDiscussion: (discussion) => discussions.push(discussion),
      onDiscussionId: (discussionId) => ids.push(discussionId)
    })

    expect(completed.id).toBe('conv-1')
    expect(completed.runtimeStatus).toBe('failed')
    expect(completed.runtimeError).toContain('run_terminal')
    expect(ids).toContain('conv-1')
    expect(discussions.at(-1)?.id).toBe('conv-1')
  })

  it('does not overwrite streamed runtime diagnostics when the send stream later rejects', async () => {
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, _body: unknown, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({
          kind: 'snapshot',
          discussion: {
            discussion_id: 'conv-1',
            title: 'Plan launch',
            status: 'failed',
            created_by: 'operator',
            created_at: '2026-06-02T00:00:00Z',
            updated_at: '2026-06-02T00:00:01Z',
            unread_completed: false,
            messages: [],
            runs: [{
              run_id: 'run-1',
              discussion_id: 'conv-1',
              agent_id: 'lead-agent',
              status: 'failed',
              runtime_kind: 'pi',
              created_at: '2026-06-02T00:00:00Z',
              completed_at: '2026-06-02T00:00:01Z'
            }],
            linked_missions: []
          }
        })
        onEvent(correlatedErrorEvent('discussion runtime execution failed: runtime kind "pi": summary cannot be generated without runtime output, deliverables, or evidence'))
        throw new Error('Message send failed')
      }
    )
    const errors: Array<string | undefined> = []

    await expect(
      sendDiscussionMessageStream({
        form: { content: 'Plan launch' },
        onDiscussion: (discussion) => errors.push(discussion.runtimeError)
      })
    ).rejects.toThrow('summary cannot be generated')

    expect(errors.at(-1)).toContain('summary cannot be generated')
  })

  it('keeps the latest streamed discussion when the send stream is aborted', async () => {
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, _body: unknown, options: { signal?: AbortSignal }, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({
          kind: 'snapshot',
          discussion: {
            discussion_id: 'conv-1',
            title: 'Launch',
            status: 'running',
            created_by: 'operator',
            created_at: '2026-06-02T00:00:00Z',
            updated_at: '2026-06-02T00:00:00Z',
            unread_completed: false,
            messages: [],
            runs: [{
              run_id: 'run-1',
              discussion_id: 'conv-1',
              agent_id: 'agent-alpha',
              status: 'running',
              runtime_kind: 'codex',
              created_at: '2026-06-02T00:00:01Z'
            }],
            linked_missions: []
          }
        })
        onEvent({
          kind: 'token_delta',
          event_id: 'evt-token-1',
          discussion_id: 'conv-1',
          turn_id: 'turn-1',
          revision: 2,
          run_id: 'run-1',
          sequence: 1,
          delta: 'partial answer'
        })
        options.signal?.dispatchEvent(new Event('abort'))
        throw new DOMException('aborted', 'AbortError')
      }
    )
    const discussions: DiscussionViewModel[] = []

    await expect(
      sendDiscussionMessageStream({
        form: { content: 'Plan launch' },
        onDiscussion: (discussion) => discussions.push(discussion)
      })
    ).rejects.toThrow(DOMException)

    expect(discussions.at(-1)?.messages.at(-1)?.content).toBe('partial answer')
    expect(discussions.at(-1)?.runtimeStatus).toBe('running')
  })

  it('coalesces token rendering into one animation frame', async () => {
    const frames: FrameRequestCallback[] = []
    vi.stubGlobal('requestAnimationFrame', vi.fn((callback: FrameRequestCallback) => {
      frames.push(callback)
      return frames.length
    }))
    vi.stubGlobal('cancelAnimationFrame', vi.fn())
    let releaseStream!: () => void
    const holdStream = new Promise<void>((resolve) => {
      releaseStream = resolve
    })
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, _body: unknown, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({
          kind: 'snapshot',
          discussion: {
            discussion_id: 'conv-1',
            revision: 1,
            title: 'Launch',
            status: 'running',
            created_by: 'operator',
            created_at: '2026-06-02T00:00:00Z',
            updated_at: '2026-06-02T00:00:00Z',
            unread_completed: false,
            messages: [],
            runs: [{
              run_id: 'run-1',
              discussion_id: 'conv-1',
              turn_id: 'turn-1',
              agent_id: 'agent-alpha',
              status: 'running',
              runtime_kind: 'codex',
              created_at: '2026-06-02T00:00:00Z'
            }],
            linked_missions: []
          }
        })
        onEvent({ kind: 'token_delta', event_id: 'token-1', discussion_id: 'conv-1', turn_id: 'turn-1', revision: 1, run_id: 'run-1', sequence: 1, delta: 'A' })
        onEvent({ kind: 'token_delta', event_id: 'token-2', discussion_id: 'conv-1', turn_id: 'turn-1', revision: 1, run_id: 'run-1', sequence: 2, delta: 'B' })
        await holdStream
      }
    )
    const states: DiscussionViewModel[] = []
    const onEvent = vi.fn()

    const pending = sendDiscussionMessageStream({
      form: { content: 'Plan launch' },
      onDiscussion: (discussion) => states.push(discussion),
      onEvent
    })
    await Promise.resolve()

    expect(frames).toHaveLength(1)
    expect(states.some((discussion) => discussion.messages.some((message) => message.content === 'AB'))).toBe(false)
    frames[0](16)
    expect(states.filter((discussion) => discussion.messages.some((message) => message.content === 'AB'))).toHaveLength(1)
    expect(onEvent).toHaveBeenCalledTimes(3)

    releaseStream()
    await pending
  })

  it('coalesces public thinking summaries into one animation frame', async () => {
    const frames: FrameRequestCallback[] = []
    vi.stubGlobal('requestAnimationFrame', vi.fn((callback: FrameRequestCallback) => {
      frames.push(callback)
      return frames.length
    }))
    vi.stubGlobal('cancelAnimationFrame', vi.fn())
    let releaseStream!: () => void
    const holdStream = new Promise<void>((resolve) => {
      releaseStream = resolve
    })
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, _body: unknown, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({
          kind: 'snapshot',
          discussion: {
            discussion_id: 'conv-1',
            revision: 1,
            title: 'Launch',
            status: 'running',
            created_by: 'operator',
            created_at: '2026-06-02T00:00:00Z',
            updated_at: '2026-06-02T00:00:00Z',
            unread_completed: false,
            messages: [],
            runs: [{
              run_id: 'run-1',
              discussion_id: 'conv-1',
              turn_id: 'turn-1',
              agent_id: 'agent-alpha',
              status: 'running',
              runtime_kind: 'codex',
              created_at: '2026-06-02T00:00:00Z'
            }],
            linked_missions: []
          }
        })
        onEvent({ kind: 'thinking_delta', event_id: 'thinking-1', discussion_id: 'conv-1', turn_id: 'turn-1', revision: 1, run_id: 'run-1', sequence: 1, delta: 'Checking ' })
        onEvent({ kind: 'thinking_delta', event_id: 'thinking-2', discussion_id: 'conv-1', turn_id: 'turn-1', revision: 1, run_id: 'run-1', sequence: 2, delta: 'the runtime.' })
        await holdStream
      }
    )
    const states: DiscussionViewModel[] = []
    const onEvent = vi.fn()

    const pending = sendDiscussionMessageStream({
      form: { content: 'Plan launch' },
      onDiscussion: (discussion) => states.push(discussion),
      onEvent
    })
    await Promise.resolve()

    expect(frames).toHaveLength(1)
    expect(states.some((discussion) => discussion.runs.some((run) => run.thinking === 'Checking the runtime.'))).toBe(false)
    frames[0](16)
    expect(states.filter((discussion) => discussion.runs.some((run) => run.thinking === 'Checking the runtime.'))).toHaveLength(1)
    expect(onEvent).toHaveBeenCalledTimes(3)

    releaseStream()
    await pending
  })

  it('flushes queued tokens before a terminal stream event', async () => {
    const cancelFrame = vi.fn()
    vi.stubGlobal('requestAnimationFrame', vi.fn(() => 1))
    vi.stubGlobal('cancelAnimationFrame', cancelFrame)
    apiMocks.sendMessageStream.mockImplementation(
      async (_discussionId: string | undefined, _body: unknown, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({
          kind: 'snapshot',
          discussion: {
            discussion_id: 'conv-1',
            revision: 1,
            title: 'Launch',
            status: 'running',
            created_by: 'operator',
            created_at: '2026-06-02T00:00:00Z',
            updated_at: '2026-06-02T00:00:00Z',
            unread_completed: false,
            messages: [],
            runs: [{
              run_id: 'run-1',
              discussion_id: 'conv-1',
              turn_id: 'turn-1',
              agent_id: 'agent-alpha',
              status: 'running',
              runtime_kind: 'codex',
              created_at: '2026-06-02T00:00:00Z'
            }],
            linked_missions: []
          }
        })
        onEvent({ kind: 'token_delta', event_id: 'token-1', discussion_id: 'conv-1', turn_id: 'turn-1', revision: 1, run_id: 'run-1', sequence: 1, delta: 'partial' })
        onEvent(correlatedErrorEvent('runtime failed'))
      }
    )
    const states: DiscussionViewModel[] = []

    await sendDiscussionMessageStream({
      form: { content: 'Plan launch' },
      onDiscussion: (discussion) => states.push(discussion)
    })

    const partialIndex = states.findIndex((discussion) => discussion.messages.some((message) => message.content === 'partial'))
    const failedIndex = states.findIndex((discussion) => discussion.runtimeStatus === 'failed')
    expect(partialIndex).toBeGreaterThan(-1)
    expect(failedIndex).toBeGreaterThan(partialIndex)
    expect(cancelFrame).toHaveBeenCalledWith(1)
  })

  it('streams retry requests and applies snapshot updates', async () => {
    apiMocks.retryMessageStream.mockImplementation(
      async (_discussionId: string, _messageId: string, _body: unknown, _options: unknown, onEvent: (event: DiscussionStreamEventDTO) => void) => {
        onEvent({
          kind: 'snapshot',
          discussion: {
            discussion_id: 'conv-1',
            title: 'Launch',
            status: 'retry_pending',
            created_by: 'operator',
            created_at: '2026-06-02T00:00:00Z',
            updated_at: '2026-06-02T00:00:00Z',
            unread_completed: false,
            messages: [
              {
                message_id: 'msg-1',
                discussion_id: 'conv-1',
                role: 'user',
                author_id: 'operator',
                author_name: 'Operator',
                content: 'Plan launch',
                created_at: '2026-06-02T00:00:00Z'
              }
            ],
            runs: [],
            events: [
              {
                event_id: 'retry-1',
                discussion_id: 'conv-1',
                kind: 'message_retry_requested',
                status: 'pending',
                metadata: { message_id: 'msg-1' },
                created_at: '2026-06-02T00:00:01Z'
              }
            ],
            linked_missions: []
          }
        })
      }
    )
    const states: string[] = []

    const completed = await retryDiscussionMessageStream({
      discussion: discussionView(),
      messageId: 'msg-1',
      approve: false,
      onDiscussion: (discussion) => states.push(discussion.runtimeStatus)
    })

    expect(apiMocks.retryMessageStream.mock.calls[0][0]).toBe('conv-1')
    expect(apiMocks.retryMessageStream.mock.calls[0][1]).toBe('msg-1')
    expect(apiMocks.retryMessageStream.mock.calls[0][2]).toEqual({ approve: false })
    expect(states).toEqual(['running', 'retry_pending'])
    expect(completed.messages[0].retryPending).toBe(true)
  })
})

function discussionView(): DiscussionViewModel {
  return {
    id: 'conv-1',
    kind: 'group',
    directAgentId: '',
    directAgentName: '',
    title: 'Launch',
    status: 'completed',
    createdBy: 'Operator',
    createdById: 'operator',
    runtimeStatus: 'completed',
    activeResponders: [],
    updatedAt: 'Jun 02, 12:00 AM',
    messages: [
      {
        id: 'msg-1',
        role: 'user',
        authorId: 'operator',
        authorName: 'Operator',
        content: 'Plan launch',
        createdAt: 'Jun 02, 12:00 AM'
      }
    ],
    members: [],
    runs: [],
    timeline: [],
    linkedMissions: []
  }
}

function discussionSnapshot(): DiscussionDTO {
  return {
    discussion_id: 'conv-1',
    revision: 1,
    title: 'Launch',
    status: 'completed',
    created_by: 'operator',
    created_at: '2026-06-02T00:00:00Z',
    updated_at: '2026-06-02T00:00:01Z',
    unread_completed: false,
    messages: [],
    runs: [],
    linked_missions: []
  }
}

function correlatedRunPayload() {
  return {
    conversation_id: 'conv-1',
    revision: 1,
    turn_id: 'turn-1',
    run_id: 'run-1',
    mode: 'group',
    agent_id: 'agent-beta',
    status: 'running'
  }
}

function correlatedToolPayload() {
  return {
    conversation_id: 'conv-1',
    revision: 1,
    turn_id: 'turn-1',
    run_id: 'run-1',
    tool_call_id: 'tool-1',
    name: 'search',
    status: 'running'
  }
}

function correlatedPlanPayload() {
  return {
    conversation_id: 'conv-1',
    revision: 1,
    turn_id: 'turn-1',
    run_id: 'run-1',
    plan: {
      plan_id: 'plan-1',
      status: 'active',
      items: [{ id: 'step-1', text: 'Inspect', status: 'in_progress' }]
    }
  }
}

function correlatedMessagePayload() {
  return {
    conversation_id: 'conv-1',
    revision: 1,
    turn_id: 'turn-1',
    run_id: 'run-1',
    message_id: 'msg-agent',
    role: 'assistant',
    author_id: 'agent-beta',
    content: 'Done'
  }
}

function correlatedErrorEvent(error: string): DiscussionStreamEventDTO {
  return {
    kind: 'error',
    event_id: 'evt-error',
    discussion_id: 'conv-1',
    turn_id: 'turn-1',
    revision: 2,
    run_id: 'run-1',
    error
  }
}

function dispatchRealtimeEnvelope(overrides: Partial<RealtimeEnvelope> = {}) {
  const listener = realtimeMocks.handlers.get('publication')
  listener?.({
    data: {
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
  })
}
