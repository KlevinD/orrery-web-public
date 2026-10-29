import type { RealtimeEnvelope } from './client'
import type { DiscussionStreamEventDTO } from '#lib/types/discussions'

export interface RealtimeSubscriptionState {
  lastVersion: number
  seenEventIds: Set<string>
}

export interface RealtimeSubscriptionHandlers {
  onEvent: (envelope: RealtimeEnvelope) => void
  onRecover: (reason: 'version_gap' | 'subscription_error') => void
}

export interface RealtimeClientLike {
  newSubscription(channel: string): RealtimeSubscriptionLike
}

export interface RealtimeSubscriptionLike {
  on(event: string, callback: (ctx: { data: RealtimeEnvelope }) => void): RealtimeSubscriptionLike
  subscribe(): void
  unsubscribe(): void
}

export function conversationRealtimeChannel(discussionId: string) {
  return `conversation:${discussionId.trim()}`
}

export function applyRealtimeEnvelope(
  state: RealtimeSubscriptionState | undefined,
  envelope: RealtimeEnvelope,
  handlers: RealtimeSubscriptionHandlers
): RealtimeSubscriptionState {
  const next: RealtimeSubscriptionState = state ?? { lastVersion: 0, seenEventIds: new Set<string>() }
  if (!envelope?.event_id || next.seenEventIds.has(envelope.event_id)) {
    return next
  }
  const expected = next.lastVersion + 1
  if (next.lastVersion > 0 && envelope.version <= next.lastVersion) {
    rememberRealtimeEvent(next.seenEventIds, envelope.event_id)
    return next
  }
  if (next.lastVersion > 0 && envelope.version > expected) {
    handlers.onRecover('version_gap')
    rememberRealtimeEvent(next.seenEventIds, envelope.event_id)
    next.lastVersion = Math.max(next.lastVersion, envelope.version)
    return next
  }
  rememberRealtimeEvent(next.seenEventIds, envelope.event_id)
  next.lastVersion = Math.max(next.lastVersion, envelope.version)
  handlers.onEvent(envelope)
  return next
}

function rememberRealtimeEvent(seen: Set<string>, eventId: string) {
  seen.add(eventId)
  while (seen.size > 2048) {
    const oldest = seen.values().next().value
    if (!oldest) break
    seen.delete(oldest)
  }
}

export function createDiscussionRealtimeSubscription(options: {
  client: RealtimeClientLike
  discussionId: string
  onEvent: (envelope: RealtimeEnvelope) => void
  onRecover: (reason: 'version_gap' | 'subscription_error') => void
}) {
  let state: RealtimeSubscriptionState | undefined
  const subscription = options.client.newSubscription(conversationRealtimeChannel(options.discussionId))
  subscription.on('publication', (ctx) => {
    state = applyRealtimeEnvelope(state, ctx.data, { onEvent: options.onEvent, onRecover: options.onRecover })
  })
  subscription.on('error', () => {
    options.onRecover('subscription_error')
  })
  subscription.subscribe()
  return () => subscription.unsubscribe()
}

export function discussionStreamEventFromEnvelope(envelope: RealtimeEnvelope): DiscussionStreamEventDTO | undefined {
  const payload = envelope.payload ?? {}
  const correlated = {
    event_id: envelope.event_id,
    discussion_id: String(payload.conversation_id ?? envelope.aggregate_id),
    turn_id: String(payload.turn_id ?? ''),
    revision: realtimePayloadNumber(payload.revision)
  }
  switch (envelope.type) {
    case 'discussion.run.started':
      return {
        ...correlated,
        kind: 'run_started',
        run: {
          run_id: String(payload.run_id ?? ''),
          discussion_id: String(payload.conversation_id ?? envelope.aggregate_id),
          turn_id: String(payload.turn_id ?? ''),
          mode: String(payload.mode ?? ''),
          has_plan: realtimePayloadBoolean(payload.has_plan),
          plan: discussionRunPlanFromPayload(payload.plan),
          agent_id: String(payload.agent_id ?? ''),
          logical_agent_id: String(payload.logical_agent_id ?? ''),
          agent_name: String(payload.agent_name ?? ''),
          status: String(payload.status ?? 'running'),
          runtime_kind: String(payload.runtime_kind ?? ''),
          session_id: String(payload.session_id ?? ''),
          thinking_summary: String(payload.thinking_summary ?? ''),
          tool_calls: discussionToolCallsFromPayload(payload.tool_calls, envelope.occurred_at),
          usage: discussionUsageFromPayload(payload.usage),
          duration_ms: realtimePayloadNumber(payload.duration_ms),
          created_at: realtimePayloadTime(payload.created_at, envelope.occurred_at)
        }
      }
    case 'discussion.run.completed':
      return {
        ...correlated,
        kind: 'run_completed',
        run: {
          run_id: String(payload.run_id ?? ''),
          discussion_id: String(payload.conversation_id ?? envelope.aggregate_id),
          turn_id: String(payload.turn_id ?? ''),
          mode: String(payload.mode ?? ''),
          has_plan: realtimePayloadBoolean(payload.has_plan),
          plan: discussionRunPlanFromPayload(payload.plan),
          agent_id: String(payload.agent_id ?? ''),
          logical_agent_id: String(payload.logical_agent_id ?? ''),
          agent_name: String(payload.agent_name ?? ''),
          status: String(payload.status ?? 'completed'),
          runtime_kind: String(payload.runtime_kind ?? ''),
          session_id: String(payload.session_id ?? ''),
          thinking_summary: String(payload.thinking_summary ?? ''),
          tool_calls: discussionToolCallsFromPayload(payload.tool_calls, envelope.occurred_at),
          usage: discussionUsageFromPayload(payload.usage),
          duration_ms: realtimePayloadNumber(payload.duration_ms),
          completed_at: realtimePayloadTime(payload.completed_at, envelope.occurred_at),
          created_at: realtimePayloadTime(payload.created_at, envelope.occurred_at)
        }
      }
    case 'discussion.run.failed':
      return {
        ...correlated,
        kind: 'run_failed',
        run: {
          run_id: String(payload.run_id ?? ''),
          discussion_id: String(payload.conversation_id ?? envelope.aggregate_id),
          turn_id: String(payload.turn_id ?? ''),
          mode: String(payload.mode ?? ''),
          has_plan: realtimePayloadBoolean(payload.has_plan),
          plan: discussionRunPlanFromPayload(payload.plan),
          agent_id: String(payload.agent_id ?? ''),
          logical_agent_id: String(payload.logical_agent_id ?? ''),
          agent_name: String(payload.agent_name ?? ''),
          status: String(payload.status ?? 'failed'),
          runtime_kind: String(payload.runtime_kind ?? ''),
          session_id: String(payload.session_id ?? ''),
          thinking_summary: String(payload.thinking_summary ?? ''),
          tool_calls: discussionToolCallsFromPayload(payload.tool_calls, envelope.occurred_at),
          usage: discussionUsageFromPayload(payload.usage),
          duration_ms: realtimePayloadNumber(payload.duration_ms),
          completed_at: realtimePayloadTime(payload.completed_at, envelope.occurred_at),
          created_at: realtimePayloadTime(payload.created_at, envelope.occurred_at)
        }
      }
    case 'discussion.token.delta':
      return {
        ...correlated,
        kind: 'token_delta',
        delta: String(payload.delta ?? ''),
        run_id: String(payload.run_id ?? ''),
        sequence: realtimePayloadNumber(payload.sequence)
      }
    case 'discussion.thinking.delta':
      return {
        ...correlated,
        kind: 'thinking_delta',
        delta: String(payload.delta ?? ''),
        run_id: String(payload.run_id ?? ''),
        sequence: realtimePayloadNumber(payload.sequence)
      }
    case 'discussion.message.completed':
      return {
        ...correlated,
        kind: 'message_completed',
        message: {
          message_id: String(payload.message_id ?? ''),
          discussion_id: String(payload.conversation_id ?? envelope.aggregate_id),
          turn_id: String(payload.turn_id ?? ''),
          run_id: String(payload.run_id ?? ''),
          role: String(payload.role ?? ''),
          author_id: String(payload.author_id ?? ''),
          author_name: String(payload.author_name ?? ''),
          content: String(payload.content ?? ''),
          metadata: discussionMessageMetadataFromPayload(payload),
          created_at: envelope.occurred_at
        }
      }
    case 'discussion.plan.updated':
      return {
        ...correlated,
        kind: 'plan',
        run_id: String(payload.run_id ?? ''),
        plan: discussionPlanFromPayload(payload)
      }
    case 'discussion.tool.call':
      return { ...correlated, kind: 'tool_call', run_id: String(payload.run_id ?? ''), tool_call: discussionToolCallFromPayload(payload, envelope.occurred_at) }
    case 'discussion.tool.result':
      return { ...correlated, kind: 'tool_result', run_id: String(payload.run_id ?? ''), tool_call: discussionToolCallFromPayload(payload, envelope.occurred_at) }
    case 'discussion.hil_mode.updated':
      return {
        ...correlated,
        kind: 'hil_mode_updated',
        hil_mode: realtimePayloadHILMode(payload.hil_mode),
        updated_at: realtimePayloadTime(payload.updated_at, envelope.occurred_at)
      }
    default:
      return undefined
  }
}

function discussionPlanFromPayload(payload: Record<string, unknown>) {
  const rawPlan = payload.plan && typeof payload.plan === 'object' ? payload.plan as Record<string, unknown> : payload
  const rawItems = Array.isArray(rawPlan.items) ? rawPlan.items : []
  return {
    plan_id: String(rawPlan.plan_id ?? rawPlan.planId ?? payload.plan_id ?? ''),
    status: String(rawPlan.status ?? payload.status ?? ''),
    items: rawItems
      .map((item) => {
        if (!item || typeof item !== 'object') return undefined
        const values = item as Record<string, unknown>
        return {
          id: String(values.id ?? ''),
          text: String(values.text ?? ''),
          status: String(values.status ?? '')
        }
      })
      .filter((item): item is { id: string; text: string; status: string } => Boolean(item))
  }
}

function discussionRunPlanFromPayload(value: unknown) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined
  return discussionPlanFromPayload({ plan: value })
}

function discussionToolCallFromPayload(payload: Record<string, unknown>, occurredAt: string) {
  return {
    tool_call_id: String(payload.tool_call_id ?? ''),
    name: String(payload.name ?? ''),
    status: String(payload.status ?? ''),
    arguments: realtimePayloadStringRecord(payload.arguments),
    result: realtimePayloadStringRecord(payload.result),
    created_at: realtimePayloadTime(payload.created_at, occurredAt)
  }
}

function discussionToolCallsFromPayload(value: unknown, occurredAt: string) {
  if (!Array.isArray(value)) return undefined
  const calls = value
    .filter((item): item is Record<string, unknown> => Boolean(item) && typeof item === 'object' && !Array.isArray(item))
    .map((item) => discussionToolCallFromPayload(item, occurredAt))
    .filter((item) => item.tool_call_id.length > 0)
  return calls.length > 0 ? calls : undefined
}

function discussionUsageFromPayload(value: unknown) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined
  const entries = Object.entries(value).flatMap(([model, usage]) => {
    if (!usage || typeof usage !== 'object' || Array.isArray(usage)) return []
    const raw = usage as Record<string, unknown>
    return [[model, {
      cached_input_tokens: realtimePayloadTokenCount(raw.cached_input_tokens),
      reasoning_tokens: realtimePayloadTokenCount(raw.reasoning_tokens),
      cached_input_tokens_known: realtimePayloadBoolean(raw.cached_input_tokens_known),
      reasoning_tokens_known: realtimePayloadBoolean(raw.reasoning_tokens_known),
      input_tokens: realtimePayloadNumber(raw.input_tokens),
      output_tokens: realtimePayloadNumber(raw.output_tokens),
      cache_read_tokens: realtimePayloadNumber(raw.cache_read_tokens),
      cache_write_tokens: realtimePayloadNumber(raw.cache_write_tokens),
      total_tokens: realtimePayloadNumber(raw.total_tokens)
    }] as const]
  })
  return entries.length > 0 ? Object.fromEntries(entries) : undefined
}

function discussionMessageMetadataFromPayload(payload: Record<string, unknown>) {
  const entries = Object.entries(payload)
    .filter(([key]) => key.startsWith('metadata.'))
    .map(([key, value]) => [key.slice('metadata.'.length), String(value ?? '')] as const)
    .filter(([key]) => key.length > 0)
  return entries.length > 0 ? Object.fromEntries(entries) : undefined
}

function realtimePayloadTime(value: unknown, fallback: string) {
  const text = String(value ?? '').trim()
  return text || fallback
}

function realtimePayloadNumber(value: unknown) {
  const number = Number(value)
  return Number.isFinite(number) && number > 0 ? number : undefined
}

function realtimePayloadTokenCount(value: unknown) {
  if (typeof value !== 'number' && (typeof value !== 'string' || !value.trim())) return undefined
  const number = Number(value)
  return Number.isSafeInteger(number) && number >= 0 ? number : undefined
}

function realtimePayloadBoolean(value: unknown) {
  if (typeof value === 'boolean') return value
  const normalized = String(value ?? '').trim().toLowerCase()
  if (normalized === 'true') return true
  if (normalized === 'false') return false
  return undefined
}

function realtimePayloadHILMode(value: unknown) {
  return String(value ?? '').trim().toLowerCase() === 'auto' ? 'auto' as const : 'manual' as const
}

function realtimePayloadStringRecord(value: unknown): Record<string, string> | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined
  const entries = Object.entries(value).map(([key, item]) => [key, typeof item === 'string' ? item : JSON.stringify(item)] as const)
  return entries.length > 0 ? Object.fromEntries(entries) : undefined
}
