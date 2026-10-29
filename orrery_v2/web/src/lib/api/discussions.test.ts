import { afterEach, describe, expect, it, vi } from 'vitest'
import { discussionApi } from './discussions'
import { setLocale } from '#lib/i18n'
import { applySession, clearSession, session } from '#lib/stores/session.svelte'

const uuidV4Pattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

describe('discussionApi', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    clearSession()
    setLocale('zh-CN')
  })

  it('uses v2 discussion endpoints for list, detail, and sends', async () => {
    applyTestSession()
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () =>
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    )
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000003')

    await discussionApi.listDiscussions()
    await discussionApi.getDiscussion('conv-1')
    await discussionApi.getDiscussion('conv-1', { limit: 20 })
    await discussionApi.listDiscussionMessages('conv-1', { limit: 20, offset: 20 })
    await discussionApi.getLatestRunDebug('conv-1')
    await discussionApi.getRunDebug('conv-1', 'run-1')
    await discussionApi.sendMessage(undefined, { content: 'Plan launch' }, { idempotencyKey: 'new-conv' })
    await discussionApi.sendMessage('conv-1', { content: 'Continue' }, { idempotencyKey: 'existing-conv' })
    await discussionApi.abortDiscussion('conv-1', { idempotencyKey: 'abort-conv' })

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/discussions',
      '/api/discussions/conv-1',
      '/api/discussions/conv-1?limit=20',
      '/api/discussions/conv-1/messages?limit=20&offset=20',
      '/api/discussions/conv-1/debug/latest',
      '/api/discussions/conv-1/runs/run-1/debug',
      '/api/discussions/messages',
      '/api/discussions/conv-1/messages',
      '/api/discussions/conv-1/abort'
    ])
    const newDiscussionHeaders = fetchMock.mock.calls[6][1]?.headers as Headers
    const existingDiscussionHeaders = fetchMock.mock.calls[7][1]?.headers as Headers
    const abortHeaders = fetchMock.mock.calls[8][1]?.headers as Headers
    expect(fetchMock.mock.calls[6][1]?.method).toBe('POST')
    expect(fetchMock.mock.calls[6][1]?.body).toBe(JSON.stringify({ content: 'Plan launch' }))
    expect(fetchMock.mock.calls[8][1]?.method).toBe('POST')
    expect(newDiscussionHeaders.get('X-User-Display-Name')).toBe(encodeURIComponent(session.displayName))
    expect(newDiscussionHeaders.get('Authorization')).toBeNull()
    expect(fetchMock.mock.calls[6][1]?.credentials).toBe('include')
    expect(newDiscussionHeaders.get('X-Agent-Profile-ID')).toBe(session.user.agentProfileId)
    expect(newDiscussionHeaders.get('Idempotency-Key')).toBe('new-conv')
    expect(existingDiscussionHeaders.get('Idempotency-Key')).toBe('existing-conv')
    expect(abortHeaders.get('Idempotency-Key')).toBe('abort-conv')
  })

  it('streams discussion sends as POST SSE events', async () => {
    setLocale('en-US')
    applyTestSession()
    const body = [
      'event: snapshot',
      'data: {"kind":"snapshot","discussion":{"discussion_id":"conv-1","title":"Launch","status":"running","created_by":"operator","created_at":"2026-06-02T00:00:00Z","updated_at":"2026-06-02T00:00:00Z","messages":[],"runs":[],"linked_missions":[]}}',
      '',
      'event: run_completed',
      'data: {"kind":"run_completed","run":{"run_id":"run-1","discussion_id":"conv-1","agent_id":"agent-1","status":"completed","runtime_kind":"codex","created_at":"2026-06-02T00:00:00Z"}}',
      '',
      'id: turn-completed-1',
      'event: turn_completed',
      'data: {"kind":"turn_completed","turn_id":"turn-1","discussion_id":"conv-1","revision":2}',
      ''
    ].join('\n')
    const stream = new ReadableStream({
      start(controller) {
        const encoded = new TextEncoder().encode(body)
        controller.enqueue(encoded.slice(0, 120))
        controller.enqueue(encoded.slice(120))
        controller.close()
      }
    })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(stream, {
        status: 200,
        headers: { 'Content-Type': 'text/event-stream; charset=utf-8' }
      })
    )
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000004')
    const events: string[] = []

    await discussionApi.sendMessageStream(undefined, { content: 'Plan launch' }, { idempotencyKey: 'stream-1' }, (event) => {
      events.push(event.kind)
    })

    expect(events).toEqual(['snapshot', 'run_completed', 'turn_completed'])
    const [, init] = fetchMock.mock.calls[0]
    const headers = init?.headers as Headers
    expect(fetchMock.mock.calls[0][0]).toBe('/api/discussions/messages')
    expect(init?.method).toBe('POST')
    expect(init?.body).toBe(JSON.stringify({ content: 'Plan launch' }))
    expect(init?.credentials).toBe('include')
    expect(headers.get('Accept')).toBe('text/event-stream')
    expect(headers.get('Accept-Language')).toBe('en-US')
    expect(headers.get('X-User-Display-Name')).toBe(encodeURIComponent(session.displayName))
    expect(headers.get('Authorization')).toBeNull()
    expect(headers.get('Idempotency-Key')).toBe('stream-1')
  })

  it('prewarms new and existing discussions without sending a message', async () => {
    applyTestSession()
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () =>
      new Response(JSON.stringify({ discussion_id: 'conv-warm' }), {
        status: 201,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await discussionApi.prewarmDiscussion(undefined, { title: 'My assistant', conversation_kind: 'personal_assistant' }, { idempotencyKey: 'warm-new' })
    await discussionApi.prewarmDiscussion('conv-1', {}, { idempotencyKey: 'warm-existing' })

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/discussions/prewarm',
      '/api/discussions/conv-1/prewarm'
    ])
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ method: 'POST', body: JSON.stringify({ title: 'My assistant', conversation_kind: 'personal_assistant' }) })
    expect(fetchMock.mock.calls[1][1]).toMatchObject({ method: 'POST', body: JSON.stringify({}) })
    expect((fetchMock.mock.calls[0][1]?.headers as Headers).get('Idempotency-Key')).toBe('warm-new')
    expect((fetchMock.mock.calls[1][1]?.headers as Headers).get('Idempotency-Key')).toBe('warm-existing')
  })

  it('updates a discussion HIL mode', async () => {
    applyTestSession()
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ discussion_id: 'conv-1', hil_mode: 'auto' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await discussionApi.setHILMode('conv-1', { mode: 'auto' }, { idempotencyKey: 'hil-mode-1' })

    expect(fetchMock).toHaveBeenCalledWith('/api/discussions/conv-1/hil-mode', expect.objectContaining({
      method: 'PATCH',
      body: JSON.stringify({ mode: 'auto' })
    }))
    const headers = fetchMock.mock.calls[0][1]?.headers as Headers
    expect(headers.get('Idempotency-Key')).toBe('hil-mode-1')
  })

  it('rejects empty discussion streams while still generating request ids without crypto.randomUUID', async () => {
    vi.stubGlobal('crypto', {})
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(null, { status: 200 }))

    await expect(discussionApi.sendMessageStream(undefined, { content: 'Plan launch' }, { idempotencyKey: 'stream-fallback' }, () => {}))
      .rejects.toThrow('unexpected content type')

    const [, init] = fetchMock.mock.calls[0]
    const headers = init?.headers as Headers
    expect(headers.get('X-Request-ID')).toMatch(uuidV4Pattern)
    expect(headers.get('Idempotency-Key')).toBe('stream-fallback')
  })

  it('streams discussion message retries as POST SSE events', async () => {
    applyTestSession()
    const body = [
      'event: snapshot',
      'data: {"kind":"snapshot","discussion":{"discussion_id":"conv-1","title":"Launch","status":"retry_pending","created_by":"operator","created_at":"2026-06-02T00:00:00Z","updated_at":"2026-06-02T00:00:00Z","messages":[],"runs":[],"linked_missions":[]}}',
      ''
    ].join('\n')
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(body))
        controller.close()
      }
    })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(stream, {
        status: 200,
        headers: { 'Content-Type': 'text/event-stream; charset=utf-8' }
      })
    )
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000005')
    const events: string[] = []

    await discussionApi.retryMessageStream('conv-1', 'msg-1', { approve: true }, { idempotencyKey: 'retry-1' }, (event) => {
      events.push(event.kind)
    })

    expect(events).toEqual(['snapshot'])
    const [, init] = fetchMock.mock.calls[0]
    const headers = init?.headers as Headers
    expect(fetchMock.mock.calls[0][0]).toBe('/api/discussions/conv-1/messages/msg-1/retry')
    expect(init?.method).toBe('POST')
    expect(init?.body).toBe(JSON.stringify({ approve: true }))
    expect(headers.get('Accept')).toBe('text/event-stream')
    expect(headers.get('Idempotency-Key')).toBe('retry-1')
  })

  it('lists and uploads discussion attachments', async () => {
    applyTestSession()
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () =>
      new Response(JSON.stringify({ attachments: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    )
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000007')
    const file = new File(['launch brief'], 'brief.txt', { type: 'text/plain' })

    await discussionApi.listDiscussionAttachments('conv-1')
    await discussionApi.uploadDiscussionAttachment('conv-1', file, { idempotencyKey: 'upload-1' })
    await discussionApi.startDiscussionAttachmentDraft({
      file,
      forceNewConversation: true,
      directTargetKind: 'agent',
      directTargetId: 'staffing-agent'
    }, { idempotencyKey: 'draft-upload-1' })

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/discussions/conv-1/attachments',
      '/api/discussions/conv-1/attachments',
      '/api/discussions/attachments'
    ])
    const [, uploadInit] = fetchMock.mock.calls[1]
    const headers = uploadInit?.headers as Headers
    expect(uploadInit?.method).toBe('POST')
    expect(uploadInit?.body).toBeInstanceOf(FormData)
    expect((uploadInit?.body as FormData).get('file')).toBe(file)
    expect(headers.get('Content-Type')).toBeNull()
    expect(headers.get('Idempotency-Key')).toBe('upload-1')
    const [, draftUploadInit] = fetchMock.mock.calls[2]
    const draftForm = draftUploadInit?.body as FormData
    expect(draftForm.get('file')).toBe(file)
    expect(draftForm.get('force_new_conversation')).toBe('true')
    expect(draftForm.get('direct_target_kind')).toBe('agent')
    expect(draftForm.get('direct_target_id')).toBe('staffing-agent')
    expect((draftUploadInit?.headers as Headers).get('Idempotency-Key')).toBe('draft-upload-1')
  })

  it('surfaces backend JSON error messages from discussion stream sends', async () => {
    applyTestSession()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({
        code: 'invalid_request',
        message: 'discussion member unavailable: no available Lead Agent in personnel directory'
      }), {
        status: 400,
        statusText: 'Bad Request',
        headers: { 'Content-Type': 'application/json' }
      })
    )
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000006')

    await expect(discussionApi.sendMessageStream(undefined, { content: 'Plan launch' }, { idempotencyKey: 'stream-error' }, () => {}))
      .rejects.toThrow('discussion member unavailable: no available Lead Agent in personnel directory')
  })

  it('surfaces runtime setup guidance from discussion stream sends', async () => {
    applyTestSession()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({
        code: 'runtime_unavailable',
        message: 'discussion runtime is not configured',
        details: {
          guidance: 'Configure a discussion runtime before starting a chat.',
          env_guidance: 'Install the CLI runtime or configure an LLM API key.'
        }
      }), {
        status: 503,
        statusText: 'Service Unavailable',
        headers: { 'Content-Type': 'application/json' }
      })
    )
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000008')

    await expect(discussionApi.sendMessageStream(undefined, { content: 'Plan launch' }, { idempotencyKey: 'stream-runtime-error' }, () => {}))
      .rejects.toThrow('Configure a discussion runtime before starting a chat. Install the CLI runtime or configure an LLM API key.')
  })
})

function applyTestSession() {
  applySession({
    accessToken: 'token-1',
    expiresAt: '',
    permissionScope: ['discussion.read', 'discussion.write'],
    user: {
      userId: 'superadmin',
      username: 'superadmin',
      displayName: 'superadmin',
      nickname: 'superadmin',
      email: '',
      agentProfileId: 'human-superadmin',
      role: 'superadmin'
    }
  })
}
