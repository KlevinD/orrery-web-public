import type { ApprovalActionDTO, ApprovalItemDTO, ApprovalPageDTO, ApprovalPayloadDTO } from '#lib/types/api'
import type { DiscussionDTO, DiscussionAttachmentDTO, DiscussionEventDTO, DiscussionHILModeUpdateDTO, DiscussionMessageDTO, DiscussionMessagePageDTO, DiscussionMessagePageViewModel, DiscussionMessageRequest, DiscussionDisplayTimelineSegmentViewModel, DiscussionPageDTO, DiscussionRunDebugDTO, DiscussionRunDTO, DiscussionStreamEventDTO, DiscussionListViewModel, DiscussionMessageFormViewModel, DiscussionMessageViewModel, DiscussionRunDebugViewModel, DiscussionRunViewModel, DiscussionTimelineItemViewModel, DiscussionToolCallViewModel, DiscussionViewModel } from '#lib/types/discussions'
import { renderPlainDiscussionMentions } from '#lib/discussionMentions'
import { mapApprovalCenterView } from '#lib/mappers/approval'
import { isCurrentSessionUserIdentity, session } from '#lib/stores/session.svelte'

export function mapDiscussionListView(dto: DiscussionPageDTO): DiscussionListViewModel {
  const discussions = dto.discussions ?? []
  const page = dto.page ?? {
    page: 1,
    page_size: discussions.length || 20,
    total: discussions.length
  }
  const currentPage = Math.max(1, page.page || 1)
  const pageSize = Math.max(1, page.page_size || 20)
  const total = Math.max(0, page.total ?? discussions.length)
  return {
    discussions: discussions.map((discussion) => ({
      ...discussionDirectTargetSummary(discussion),
      id: discussion.discussion_id,
      kind: discussion.conversation_kind || 'group',
      title: discussion.title || discussion.discussion_id,
      status: discussion.status,
      lastMessage: renderPlainMentions(discussion.last_message || ''),
      linkedMissionCount: discussion.linked_missions ?? 0,
      attachmentCount: discussion.attachment_count ?? 0,
      updatedAt: formatDate(discussion.updated_at),
      workspaceRootDir: discussion.workspace_root_dir || undefined,
      hilMode: normalizeDiscussionHILMode(discussion.hil_mode),
      personalAssistantRuntime: discussion.personal_assistant_runtime === true,
      unreadCompleted: discussion.unread_completed === true
    })),
    page: {
      page: currentPage,
      pageSize,
      total,
      hasPrevious: currentPage > 1,
      hasNext: currentPage * pageSize < total
    }
  }
}

export function mapDiscussionView(dto: DiscussionDTO): DiscussionViewModel {
  const runs = dto.runs ?? []
  const latestRun = runs.at(-1)
  const retryState = retryStateByMessage(dto)
  const runErrors = runtimeErrorsByRunID(dto)
  const members = mapDiscussionMembers(dto)
  const namesByMemberID = discussionMemberNamesByID(members)
  const directTarget = discussionDirectTarget(dto)
  const directAgentId = directTarget.kind === 'agent' ? directTarget.id : ''
  const directAgentName = directAgentId ? namesByMemberID.get(directAgentId) || dto.title || directAgentId : ''
  const directTargetName = directTarget.id ? namesByMemberID.get(directTarget.id) || dto.title || directTarget.id : ''
  const acknowledgements = acknowledgementsByMessage(dto, namesByMemberID)
  const baseRuns = runs.map((run) => ({
      ...mapDiscussionRun(run, runErrors.get(run.run_id), namesByMemberID),
      mode: run.mode || dto.conversation_kind || 'group'
    }))
  const messages = orderDiscussionMessagesByOccurrence((dto.messages ?? []).map((message) => applyRuntimeMessageIdentity(
    mapDiscussionMessage(message, dto.created_by, retryState, acknowledgements),
    baseRuns
  )))
  const mappedRuns = applyPersistedMessagePlans(
    baseRuns,
    messages
  )
  const activeResponders = activeResponderNames(mappedRuns, dto.status)
  const activeRunsAllowed = discussionStatusAllowsActiveRuns(dto.status)
  return {
    id: dto.discussion_id,
    revision: normalizedRevision(dto.revision),
    kind: dto.conversation_kind || 'group',
    directAgentId,
    directAgentName,
    directTargetKind: directTarget.kind,
    directTargetId: directTarget.id,
    directTargetName,
    title: dto.title || dto.discussion_id,
    status: dto.status,
    createdBy: dto.created_by_name || dto.created_by,
    createdById: dto.created_by,
    runtimeStatus: activeRunsAllowed ? (latestRun?.status || dto.status) : dto.status,
    runtimeError: latestDiscussionRuntimeError(dto),
    activeResponders,
    updatedAt: formatDate(dto.updated_at),
    workspaceRootDir: dto.workspace_root_dir || undefined,
    hilMode: normalizeDiscussionHILMode(dto.hil_mode),
    personalAssistantRuntime: dto.personal_assistant_runtime === true,
    messages,
    messagePage: mapDiscussionMessagePageMeta(dto.message_page, messages.length),
    members,
    runs: mappedRuns,
    timeline: buildDiscussionTimeline(dto, messages, mappedRuns),
    linkedMissions: uniqueLinkedMissions(dto.linked_missions ?? []),
    attachments: (dto.attachments ?? []).map(mapDiscussionAttachment)
  }
}

export function mapDiscussionMessagePageView(dto: DiscussionMessagePageDTO): {
  messages: DiscussionMessageViewModel[]
  page: DiscussionMessagePageViewModel
} {
  const messages = orderDiscussionMessagesByOccurrence((dto.messages ?? []).map((message) => mapDiscussionMessage(message)))
  return {
    messages,
    page: mapDiscussionMessagePageMeta(dto.page, messages.length)
  }
}

export function prependDiscussionMessagePage(
  discussion: DiscussionViewModel,
  page: {
    messages: DiscussionMessageViewModel[]
    page: DiscussionMessagePageViewModel
  }
): DiscussionViewModel {
  const byID = new Map<string, DiscussionMessageViewModel>()
  const olderMessages = page.messages.map((message) => applyRuntimeMessageIdentity(message, discussion.runs))
  for (const message of [...olderMessages, ...discussion.messages]) {
    if (!message.id || byID.has(message.id)) continue
    byID.set(message.id, message)
  }
  const messages = orderDiscussionMessagesByOccurrence([...byID.values()])
  const rawMessageTimes = new Map(messages.map((message) => [message.id, message.sortAt || message.createdAt]))
  const messageItems = discussionMessageTimelineItems(messages, discussion.runs, rawMessageTimes, discussion.kind)
  const otherTimeline = removeTimelineItemsRepresentedByDisplayItems(
    discussion.timeline.filter((item) => item.kind !== 'message'),
    messageItems
  )
  return {
    ...discussion,
    messages,
    messagePage: page.page,
    timeline: orderSnapshotTimelineByOccurrence([...messageItems, ...otherTimeline])
  }
}

export function mapDiscussionMessageRequest(form: DiscussionMessageFormViewModel): DiscussionMessageRequest {
  const directTargetKind = normalizeDirectTargetKind(form.directTargetKind || (form.directTargetId || form.directAgentId ? 'agent' : ''))
  const directTargetId = (form.directTargetId || form.directAgentId || '').trim()

  const request: DiscussionMessageRequest = { content: form.content }
  if (form.conversationKind) {
    request.conversation_kind = form.conversationKind
  }
  if (form.forceNewConversation) {
    request.force_new_conversation = true
  }
  if (form.directAgentId) {
    request.direct_agent_id = form.directAgentId
  }
  if (form.directTargetKind && directTargetKind) {
    request.direct_target_kind = directTargetKind
  }
  if (directTargetId) {
    request.direct_target_id = directTargetId
  }
  if (form.expectedTurnId?.trim()) {
    request.expected_turn_id = form.expectedTurnId.trim()
  }
  if (form.expectedRunId?.trim()) {
    request.expected_run_id = form.expectedRunId.trim()
  }
  const attachmentIds = (form.attachmentIds ?? []).map((id) => id.trim()).filter(Boolean)
  if (attachmentIds.length > 0) {
    request.attachment_ids = attachmentIds
  }
  const mentions = (form.mentions ?? [])
    .map((mention) => ({
      target_kind: mention.target_kind,
      target_id: mention.target_id.trim(),
      display_name: mention.display_name?.trim() || undefined
    }))
    .filter((mention) => mention.target_id && (mention.target_kind === 'agent' || mention.target_kind === 'user'))
  if (mentions.length > 0) {
    request.mentions = mentions
  }
  return request
}

export function mapDiscussionAttachment(attachment: DiscussionAttachmentDTO) {
  const downloadUrl = attachment.download_url || ''
  return {
    id: attachment.attachment_id,
    discussionId: attachment.discussion_id,
    messageId: attachment.message_id || '',
    fileName: attachment.file_name || attachment.attachment_id,
    contentType: attachment.content_type || '',
    sizeBytes: Math.max(0, attachment.size_bytes ?? 0),
    uploadedBy: attachment.uploaded_by || '',
    uploadedByKind: attachment.uploaded_by_kind || '',
    uploadedByName: attachment.uploaded_by_name || attachment.uploaded_by || '',
    storagePath: '',
    downloadUrl,
    previewUrl: attachment.preview_url || (isHTMLAttachment(attachment.content_type || '', attachment.file_name || '')
      ? downloadUrl.replace(/\/download(?:\?.*)?$/, '/preview')
      : undefined),
    createdAt: formatDate(attachment.created_at)
  }
}

function retryStateByMessage(dto: DiscussionDTO) {
  const state = new Map<string, { pending: boolean }>()
  for (const event of dto.events ?? []) {
    if (event.kind !== 'message_retry_requested') continue
    const messageId = event.metadata?.message_id
    if (!messageId) continue
    state.set(messageId, { pending: event.status === 'pending' })
  }
  return state
}

export function mapDiscussionRunDebugView(dto: DiscussionRunDebugDTO): DiscussionRunDebugViewModel {
  return {
    discussionId: dto.discussion_id,
    runId: dto.run_id,
    outboundPrompt: dto.outbound_prompt,
    runtimeResponse: dto.runtime_response,
    providerMetadata: dto.provider_metadata ?? {},
    selected: {
      agentId: dto.selected.agent_id,
      runtimeKind: dto.selected.runtime_kind,
      model: dto.selected.model || '',
      configRef: dto.selected.config_ref || ''
    },
    createdAt: formatDate(dto.created_at),
    redacted: dto.redacted
  }
}

export function applyDiscussionStreamEvent(discussion: DiscussionViewModel | undefined, event: DiscussionStreamEventDTO): DiscussionViewModel | undefined {
  if (event.kind === 'snapshot' && event.discussion) {
    const snapshot = mapDiscussionView(event.discussion)
    return applyAuthoritativeDiscussionSnapshot(discussion, {
      ...snapshot,
      revision: Math.max(normalizedRevision(snapshot.revision), normalizedRevision(event.revision))
    })
  }
  if (!discussion) return undefined
  if (!discussionEventTargetsView(discussion, event) || discussionEventWasApplied(discussion, event)) return discussion

  let next = discussion
  switch (event.kind) {
    case 'run_started':
      next = event.run ? applyRunStartedEvent(discussion, event.run) : discussion
      break
    case 'token_delta':
      next = applyTokenDelta(discussion, event.delta ?? '', event.run_id, event.sequence)
      break
    case 'thinking_delta':
      next = applyThinkingDelta(discussion, event.delta ?? '', event.run_id, event.sequence)
      break
    case 'tool_call':
    case 'tool_result':
      next = event.tool_call ? applyToolEvent(discussion, event.tool_call, event.run_id, discussionEventRequiresStableCorrelation(event)) : discussion
      break
    case 'plan':
      next = event.plan ? applyPlanEvent(discussion, event.plan, event.run_id, discussionEventRequiresStableCorrelation(event)) : discussion
      break
    case 'hil_mode_updated':
      next = event.hil_mode
        ? applyDiscussionHILModeUpdate(discussion, {
            discussion_id: event.discussion_id || discussion.id,
            revision: normalizedRevision(event.revision),
            hil_mode: event.hil_mode,
            updated_at: event.updated_at || ''
          })
        : discussion
      break
    case 'message_completed':
      next = event.message
        ? replaceStreamingMessage(
            discussion,
            applyRuntimeMessageIdentity(mapDiscussionMessage(event.message), discussion.runs),
            event.message.run_id || event.run_id,
            discussionEventRequiresStableCorrelation(event)
          )
        : discussion
      break
    case 'run_completed':
    case 'run_failed':
      next = event.run
        ? upsertRun(
            discussion,
            mapDiscussionRun(event.run, undefined, discussionMemberNamesByID(discussion.members)),
            true
          )
        : discussion
      break
    case 'error':
      next = markRunError(
        {
          ...discussion,
          status: 'failed',
          runtimeStatus: 'failed',
          runtimeError: event.error || discussion.runtimeError
        },
        event.error,
        event.run_id,
        discussionEventRequiresStableCorrelation(event)
      )
      break
    default:
      return recordDiscussionEventState(discussion, event)
  }
  return recordDiscussionEventState(next, event)
}

export function applyAuthoritativeDiscussionSnapshot(
  current: DiscussionViewModel | undefined,
  snapshot: DiscussionViewModel
): DiscussionViewModel {
  if (!current || current.id !== snapshot.id) return snapshot
  const currentRevision = normalizedRevision(current.revision)
  const snapshotRevision = normalizedRevision(snapshot.revision)
  if (currentRevision > 0 && snapshotRevision > 0 && snapshotRevision < currentRevision) return current
  return retainActiveDiscussionStreamState(current, {
    ...snapshot,
    runtimeError: snapshot.runtimeError || (snapshot.status === 'failed' ? current.runtimeError : undefined),
    streamState: current.streamState
  })
}

export function applyDiscussionHILModeUpdate(
  current: DiscussionViewModel,
  update: DiscussionHILModeUpdateDTO
): DiscussionViewModel {
  if (current.id !== update.discussion_id) return current
  const currentRevision = normalizedRevision(current.revision)
  const updateRevision = normalizedRevision(update.revision)
  if (currentRevision > 0 && updateRevision > 0 && updateRevision < currentRevision) return current
  return {
    ...current,
    revision: Math.max(currentRevision, updateRevision),
    hilMode: normalizeDiscussionHILMode(update.hil_mode),
    updatedAt: update.updated_at ? formatDate(update.updated_at) : current.updatedAt
  }
}

function retainActiveDiscussionStreamState(current: DiscussionViewModel, snapshot: DiscussionViewModel) {
  const currentRuns = new Map(current.runs.map((run) => [run.id, run]))
  const activeRunIDs = new Set(
    snapshot.runs
      .filter((run) => runtimeRunAcceptsLiveEvents(run.status) && runtimeRunAcceptsLiveEvents(currentRuns.get(run.id)?.status ?? ''))
      .map((run) => run.id)
  )
  if (activeRunIDs.size === 0) return snapshot

  const runs = snapshot.runs.map((run) => {
    if (!activeRunIDs.has(run.id)) return run
    const live = currentRuns.get(run.id)
    if (!live) return run
    return {
      ...run,
      hasPlan: run.hasPlan || live.hasPlan,
      planId: run.planId || live.planId,
      planStatus: run.planStatus || live.planStatus,
      planSteps: run.planSteps?.length ? run.planSteps : live.planSteps,
      thinking: run.thinking ?? live.thinking,
      preview: run.preview ?? live.preview,
      toolCalls: mergeToolCalls(live.toolCalls, run.toolCalls)
    }
  })
  const retainedMessages = current.messages.filter((message) => {
    if (!message.id.startsWith('streaming-')) return false
    return [...activeRunIDs].some((runID) => streamingItemBelongsToRun(message.id, `streaming-${runID}`))
  })
  const messages = orderDiscussionMessagesByOccurrence([
    ...snapshot.messages,
    ...retainedMessages.filter((message) => !snapshot.messages.some((item) => item.id === message.id))
  ])

  let timeline = [...snapshot.timeline]
  for (const run of runs) {
    if (!activeRunIDs.has(run.id) || !runHasStructuredProgress(run, snapshot.kind)) continue
    const liveItem = [...current.timeline]
      .reverse()
      .find((item) => item.kind === 'run' && !item.displaySegment && runTimelineItemBelongsToRun(item.id, run.id))
    timeline = timeline.filter((item) => item.kind !== 'run' || item.displaySegment || !runTimelineItemBelongsToRun(item.id, run.id))
    timeline = appendTimelineItem(timeline, {
      id: liveItem?.id || run.id,
      kind: 'run',
      sortAt: liveItem?.sortAt || isoSortValue(run.sortAt || run.createdAt),
      run
    })
  }
  for (const message of retainedMessages) {
    const liveItem = current.timeline.find((item) => item.kind === 'message' && item.id === message.id)
    timeline = appendTimelineItem(timeline.filter((item) => item.id !== message.id), {
      id: message.id,
      kind: 'message',
      sortAt: liveItem?.sortAt || isoSortValue(message.sortAt || message.createdAt),
      message
    })
  }
  const next = {
    ...snapshot,
    messages,
    runs,
    timeline: orderSnapshotTimelineByOccurrence(timeline)
  }
  return { ...next, activeResponders: activeResponderNames(next.runs, next.status) }
}

export function appendPendingDiscussionMessage(discussion: DiscussionViewModel | undefined, content: string, attachments: NonNullable<DiscussionMessageViewModel['attachments']> = []): DiscussionViewModel {
  const now = new Date().toISOString()
  const pending: DiscussionMessageViewModel = {
    id: `pending-${Date.now()}`,
    role: 'user',
    authorId: session.actorId,
    authorName: session.displayName || session.actorId,
    isCurrentViewer: true,
    content,
    attachments: attachments.length > 0 ? attachments : undefined,
    sortAt: now,
    createdAt: formatDate(now)
  }
  if (!discussion) {
    return {
      id: '',
      revision: 0,
      kind: 'group',
      directAgentId: '',
      directAgentName: '',
      directTargetKind: '',
      directTargetId: '',
      directTargetName: '',
      title: renderPlainMentions(content).slice(0, 80) || 'New chat',
      status: 'running',
      createdBy: session.displayName || session.actorId,
      createdById: session.actorId,
      runtimeStatus: 'running',
      runtimeError: undefined,
      activeResponders: [],
      updatedAt: formatDate(now),
      messages: [pending],
      messagePage: undefined,
      members: [],
      runs: [],
      timeline: [{ id: pending.id, kind: 'message', sortAt: now, message: pending }],
      linkedMissions: []
    }
  }
  const messages = orderDiscussionMessagesByOccurrence([...discussion.messages, pending])
  return {
    ...discussion,
    status: 'running',
    runtimeStatus: 'running',
    runtimeError: undefined,
    messages,
    timeline: orderSnapshotTimelineByOccurrence(
      appendTimelineItem(discussion.timeline, {
        id: pending.id,
        kind: 'message',
        sortAt: now,
        message: pending
      })
    )
  }
}

function latestDiscussionRuntimeError(dto: DiscussionDTO) {
  const events = dto.events ?? []
  for (let i = events.length - 1; i >= 0; i -= 1) {
    const event = events[i]
    if (event.kind === 'discussion_orchestration_incomplete' && event.summary) return event.summary
    if (event.status === 'failed' && event.summary) return event.summary
  }
  return undefined
}

function runtimeErrorsByRunID(dto: DiscussionDTO) {
  const errors = new Map<string, string>()
  for (const event of dto.events ?? []) {
    if (event.status !== 'failed' || !event.summary) continue
    const runID = event.metadata?.run_id
    if (!runID) continue
    errors.set(runID, event.summary)
  }
  return errors
}

function mapDiscussionMessage(message: DiscussionMessageDTO, discussionCreatorId = '', retryState: Map<string, { pending: boolean }> = new Map(), acknowledgements: Map<string, DiscussionMessageViewModel['acknowledgements']> = new Map()): DiscussionMessageViewModel {
  const pendingRetry = retryState.get(message.message_id)?.pending ?? false
  const isHumanUserMessage = message.role === 'user'
  const messageAcknowledgements = acknowledgements.get(message.message_id)
  const resultAgentId = message.metadata?.kind === 'agent_result' ? message.metadata.agent_id?.trim() : ''
  const resultAgentName = message.metadata?.kind === 'agent_result' ? message.metadata.agent_name?.trim() : ''
  return {
    id: message.message_id,
    turnId: message.turn_id || undefined,
    runId: message.run_id || undefined,
    role: message.role,
    authorId: resultAgentId || message.author_id,
    authorName: resultAgentName || message.author_name || '',
    isCurrentViewer: message.is_current_viewer,
    content: message.content,
    orchestrationWarning: message.metadata?.orchestration_warning?.trim() || undefined,
    delivery: message.metadata?.delivery?.trim() || undefined,
    humanTodoId: message.role === 'system' && message.metadata?.kind === 'human_todo' ? message.metadata.todo_id : undefined,
    displayTimeline: parseDisplayTimeline(message.metadata?.display_timeline_json, message.content),
    attachments: (message.attachments ?? []).map(mapDiscussionAttachment),
    sortAt: message.created_at,
    createdAt: formatDate(message.created_at),
    acknowledgements: messageAcknowledgements && messageAcknowledgements.length > 0 ? messageAcknowledgements : undefined,
    canRequestRetry: isHumanUserMessage && isCurrentSessionUserIdentity(message.author_id) && !pendingRetry,
    canApproveRetry: isHumanUserMessage && isCurrentSessionUserIdentity(discussionCreatorId) && pendingRetry,
    retryPending: pendingRetry
  }
}

function parseDisplayTimeline(raw: string | undefined, content = ''): DiscussionDisplayTimelineSegmentViewModel[] | undefined {
  if (!raw) return undefined
  let decoded: unknown
  try {
    decoded = JSON.parse(raw)
  } catch {
    return undefined
  }
  if (!Array.isArray(decoded)) return undefined
  const segments = decoded
    .map((segment): DiscussionDisplayTimelineSegmentViewModel | undefined => {
      if (!segment || typeof segment !== 'object') return undefined
      const values = segment as Record<string, unknown>
      const kind = String(values.kind ?? '').trim()
      if (kind === 'text') {
        const text = String(values.text ?? '').trim()
        return text ? { kind, text } : undefined
      }
      if (kind === 'tool') {
        const runId = String(values.run_id ?? values.runId ?? '').trim()
        const toolCallId = String(values.tool_call_id ?? values.toolCallId ?? '').trim()
        return toolCallId ? { kind, runId, toolCallId } : undefined
      }
      if (kind === 'plan') {
        const runId = String(values.run_id ?? values.runId ?? '').trim()
        const plan = parseDisplayPlan(values.plan)
        return plan ? { kind, runId, plan } : undefined
      }
      return undefined
    })
    .filter((segment): segment is DiscussionDisplayTimelineSegmentViewModel => Boolean(segment))
  return segments.length > 0 ? segments : undefined
}

function parseDisplayPlan(raw: unknown) {
  if (!raw || typeof raw !== 'object') return undefined
  const values = raw as Record<string, unknown>
  const id = String(values.plan_id ?? values.planId ?? values.id ?? '').trim()
  if (!id) return undefined
  const status = String(values.status ?? '').trim()
  const rawItems = Array.isArray(values.items) ? values.items : []
  const items = rawItems
    .map((item) => {
      if (!item || typeof item !== 'object') return undefined
      const itemValues = item as Record<string, unknown>
      const itemId = String(itemValues.id ?? '').trim()
      const text = String(itemValues.text ?? '').trim()
      const itemStatus = String(itemValues.status ?? '').trim()
      return itemId && text && itemStatus ? { id: itemId, text, status: itemStatus } : undefined
    })
    .filter((item): item is { id: string; text: string; status: string } => Boolean(item))
  return { id, status, items }
}

function applyPersistedMessagePlans(runs: DiscussionRunViewModel[], messages: DiscussionMessageViewModel[]) {
  const byId = new Map(runs.map((run) => [run.id, run]))
  const toolOrderByRun = new Map<string, string[]>()
  for (const message of messages) {
    let activeRunId = ''
    for (const segment of message.displayTimeline ?? []) {
      if (segment.kind === 'plan' && segment.runId && segment.plan) {
        activeRunId = segment.runId
        const run = byId.get(activeRunId)
        if (!run) continue
        byId.set(activeRunId, {
          ...run,
          hasPlan: true,
          planId: segment.plan.id,
          planStatus: segment.plan.status,
          planSteps: segment.plan.items
        })
        continue
      }
      if (segment.kind === 'tool' && segment.runId && segment.toolCallId) {
        activeRunId = segment.runId
        const order = toolOrderByRun.get(activeRunId) ?? []
        if (!order.includes(segment.toolCallId)) order.push(segment.toolCallId)
        toolOrderByRun.set(activeRunId, order)
        continue
      }
      if (segment.kind !== 'text' || !activeRunId || !segment.text) continue
      const run = byId.get(activeRunId)
      if (!run) continue
      byId.set(activeRunId, { ...run, preview: appendRunPreview(run.preview, segment.text) })
    }
  }
  return runs.map((run) => {
    const mapped = byId.get(run.id) ?? run
    return {
      ...mapped,
      toolCalls: orderToolCallsByOccurrence(mapped.toolCalls, toolOrderByRun.get(run.id))
    }
  })
}

export function discussionStreamEventIsCorrelated(event: DiscussionStreamEventDTO, discussionId = '') {
  if (event.kind === 'snapshot') return Boolean(event.discussion?.discussion_id)
  const eventId = (event.event_id || '').trim()
  const conversationId = (event.discussion_id || '').trim()
  if (!eventId || !conversationId || normalizedRevision(event.revision) <= 0) return false
  if (discussionId && conversationId !== discussionId) return false

  switch (event.kind) {
    case 'run_started':
    case 'run_completed':
    case 'run_failed':
      return Boolean(event.turn_id?.trim() && event.run?.run_id.trim())
    case 'turn_started':
    case 'turn_completed':
    case 'turn_failed':
      return Boolean(event.turn_id?.trim())
    case 'token_delta':
    case 'thinking_delta':
      return Boolean(event.turn_id?.trim() && event.run_id?.trim() && normalizedSequence(event.sequence) > 0)
    case 'tool_call':
    case 'tool_result':
    case 'plan':
      return Boolean(event.turn_id?.trim() && event.run_id?.trim())
    case 'hil_mode_updated':
      return event.hil_mode === 'auto' || event.hil_mode === 'manual'
    case 'message_completed':
      if (!event.message?.message_id.trim()) return false
      if (event.message.role !== 'assistant') return Boolean(event.message.turn_id?.trim() || event.turn_id?.trim())
      return Boolean(event.message.run_id?.trim() && (event.message.turn_id?.trim() || event.turn_id?.trim()))
    case 'error':
      return Boolean(event.error?.trim())
    default:
      return false
  }
}

function discussionEventRequiresStableCorrelation(event: DiscussionStreamEventDTO) {
  return Boolean(event.event_id || event.discussion_id || normalizedRevision(event.revision) > 0)
}

function discussionEventTargetsView(discussion: DiscussionViewModel, event: DiscussionStreamEventDTO) {
  const conversationId = (event.discussion_id || event.run?.discussion_id || event.message?.discussion_id || '').trim()
  if (conversationId && discussion.id && conversationId !== discussion.id) return false
  const revision = normalizedRevision(event.revision)
  const currentRevision = normalizedRevision(discussion.revision)
  return revision <= 0 || currentRevision <= 0 || revision >= currentRevision
}

function discussionEventWasApplied(discussion: DiscussionViewModel, event: DiscussionStreamEventDTO) {
  const eventId = (event.event_id || '').trim()
  return Boolean(eventId && discussion.streamState?.seenEventIds.includes(eventId))
}

function discussionEventSequenceIsStale(discussion: DiscussionViewModel, runId: string, sequence: number | undefined) {
  const normalized = normalizedSequence(sequence)
  if (!runId || normalized <= 0) return false
  return normalized <= (discussion.streamState?.tokenSequenceByRun[runId] ?? 0)
}

function recordDiscussionEventState(discussion: DiscussionViewModel, event: DiscussionStreamEventDTO): DiscussionViewModel {
  const eventId = (event.event_id || '').trim()
  const runId = (event.run_id || '').trim()
  const sequence = normalizedSequence(event.sequence)
  const previous = discussion.streamState ?? { seenEventIds: [], tokenSequenceByRun: {} }
  const seenEventIds = eventId && !previous.seenEventIds.includes(eventId)
    ? [...previous.seenEventIds, eventId].slice(-512)
    : previous.seenEventIds
  const tokenSequenceByRun = runId && sequence > 0
    ? { ...previous.tokenSequenceByRun, [runId]: Math.max(previous.tokenSequenceByRun[runId] ?? 0, sequence) }
    : previous.tokenSequenceByRun
  return {
    ...discussion,
    revision: Math.max(normalizedRevision(discussion.revision), normalizedRevision(event.revision)),
    streamState: { seenEventIds, tokenSequenceByRun }
  }
}

function normalizedRevision(value: unknown) {
  const revision = Number(value)
  return Number.isSafeInteger(revision) && revision > 0 ? revision : 0
}

function normalizedSequence(value: unknown) {
  const sequence = Number(value)
  return Number.isSafeInteger(sequence) && sequence > 0 ? sequence : 0
}

function mapDiscussionMessagePageMeta(page: DiscussionDTO['message_page'] | DiscussionMessagePageDTO['page'] | undefined, fallbackReturned: number): DiscussionMessagePageViewModel {
  return {
    limit: Math.max(1, page?.limit ?? 20),
    offset: Math.max(0, page?.offset ?? 0),
    total: Math.max(0, page?.total ?? fallbackReturned),
    returned: Math.max(0, page?.returned ?? fallbackReturned),
    hasMore: page?.has_more === true
  }
}

function acknowledgementsByMessage(dto: DiscussionDTO, namesByMemberID: Map<string, string>) {
  const out = new Map<string, DiscussionMessageViewModel['acknowledgements']>()
  for (const event of dto.events ?? []) {
    if (event.kind !== 'discussion_agent_acknowledged') continue
    const messageId = event.metadata?.source_message_id
    const agentId = event.metadata?.target_agent_id
    if (!messageId || !agentId) continue
    const list = out.get(messageId) ?? []
    list.push({
      id: event.event_id,
      agentId,
      agentName: event.metadata?.target_name || namesByMemberID.get(agentId) || agentId,
      emoji: event.metadata?.emoji || '👍',
      createdAt: formatDate(event.created_at)
    })
    out.set(messageId, list)
  }
  return out
}

function mapDiscussionMembers(dto: DiscussionDTO) {
  return (dto.members ?? []).map((member) => ({
    kind: member.kind,
    id: member.id,
    name: member.display_name || '',
    role: member.role || ''
  }))
}

function discussionDirectTarget(
  dto:
    | DiscussionDTO
    | {
        direct_agent_id?: string
        direct_target_kind?: string
        direct_target_id?: string
      }
) {
  const directTargetId = (dto.direct_target_id || dto.direct_agent_id || '').trim()
  if (!directTargetId) return { kind: '', id: '' }
  return {
    kind: normalizeDirectTargetKind(dto.direct_target_kind || (dto.direct_agent_id ? 'agent' : '')),
    id: directTargetId
  }
}

function discussionDirectTargetSummary(dto: { direct_agent_id?: string; direct_target_kind?: string; direct_target_id?: string }) {
  const target = discussionDirectTarget(dto)
  const directAgentId = target.kind === 'agent' ? target.id : ''
  return {
    directAgentId,
    directTargetKind: target.kind,
    directTargetId: target.id
  }
}

function normalizeDirectTargetKind(kind: string | undefined) {
  const normalized = (kind || '').trim().toLowerCase()
  if (!normalized) return ''
  if (normalized === 'human' || normalized === 'person') return 'user'
  if (normalized === 'ai') return 'agent'
  return normalized
}

function discussionMemberNamesByID(members: DiscussionViewModel['members']) {
  const names = new Map<string, string>()
  for (const member of members) {
    if (member.id && member.name) names.set(member.id, member.name)
  }
  return names
}

function activeResponderNames(runs: DiscussionRunViewModel[], discussionStatus: string) {
  if (!discussionStatusAllowsActiveRuns(discussionStatus)) return []
  const names: string[] = []
  const seen = new Set<string>()
  for (const run of runs) {
    const status = run.status.trim().toLowerCase()
    if (status !== 'running' && status !== 'started') continue
    const name = run.agentName
    if (!name || seen.has(name)) continue
    seen.add(name)
    names.push(name)
  }
  return names
}

function discussionStatusAllowsActiveRuns(status: string) {
  switch (status.trim().toLowerCase()) {
    case 'completed':
    case 'succeeded':
    case 'done':
    case 'failed':
    case 'cancelled':
    case 'canceled':
      return false
    default:
      return true
  }
}

function mapDiscussionRun(run: DiscussionRunDTO, error?: string, namesByMemberID: Map<string, string> = new Map()): DiscussionRunViewModel {
  const toolCalls = orderToolCallsByOccurrence((run.tool_calls ?? []).map((tool) => {
    const displayCreatedAt = firstMeaningfulTime(tool.created_at, run.created_at)
    return {
      id: tool.tool_call_id,
      name: tool.name,
      status: tool.status,
      arguments: tool.arguments,
      result: tool.result,
      createdAt: formatDate(displayCreatedAt),
      sortAt: displayCreatedAt
    }
  }))
  const plan = parseDisplayPlan(run.plan)
  const physicalAgentName = run.logical_agent_id && run.logical_agent_id !== run.agent_id
    ? namesByMemberID.get(run.agent_id)
    : undefined
  return {
    id: run.run_id,
    turnId: run.turn_id || undefined,
    mode: run.mode || undefined,
    hasPlan: run.has_plan === true || Boolean(plan),
    agentId: run.agent_id,
    logicalAgentId: run.logical_agent_id || undefined,
    agentName: physicalAgentName || run.agent_name?.trim() || namesByMemberID.get(run.agent_id) || 'Agent',
    status: run.status,
    runtimeKind: run.runtime_kind,
    error,
    toolCalls,
    planId: plan?.id,
    planStatus: plan?.status,
    planSteps: plan?.items,
    thinking: run.thinking_summary?.trim() || undefined,
    usage: run.usage,
    durationMs: run.duration_ms,
    createdAt: formatDate(run.created_at),
    sortAt: discussionRunSortAt(run, toolCalls.length > 0)
  }
}

function applyRuntimeMessageIdentity(message: DiscussionMessageViewModel, runs: DiscussionRunViewModel[]) {
  const run = message.runId ? runs.find((item) => item.id === message.runId) : undefined
  if (!run?.logicalAgentId || run.logicalAgentId === run.agentId || message.authorId !== run.logicalAgentId) return message
  const hostName = run.agentName.trim()
  const authorName = message.authorName.trim()
  if (!hostName || hostName.toLocaleLowerCase() === authorName.toLocaleLowerCase()) return message
  return {
    ...message,
    authorName: [authorName, hostName].filter(Boolean).join(' / ')
  }
}

function isHTMLAttachment(contentType: string, fileName: string) {
  const normalizedType = contentType.trim().toLocaleLowerCase().split(';', 1)[0]
  return normalizedType === 'text/html' || /\.html?$/i.test(fileName.trim())
}

function markRunError(discussion: DiscussionViewModel, error?: string, runId?: string, strictCorrelation = false) {
  const targetRunId = (runId || '').trim()
  if (strictCorrelation && !targetRunId) return discussion
  const withoutStreaming = targetRunId ? removeStreamingMessagesForRun(discussion, targetRunId) : removeStreamingMessages(discussion)
  if (!error) return withoutStreaming
  const target = targetRunId ? withoutStreaming.runs.find((run) => run.id === targetRunId) : withoutStreaming.runs.at(-1)
  if (!target) return withoutStreaming
  const runs = [...withoutStreaming.runs]
  const targetIndex = runs.findIndex((run) => run.id === target.id)
  runs[targetIndex] = { ...target, status: 'failed', error }
  if (!runHasStructuredProgress(runs[targetIndex], withoutStreaming.kind)) {
    return {
      ...withoutStreaming,
      runs,
      timeline: withoutStreaming.timeline.filter((item) => item.kind !== 'run' || item.displaySegment || !runTimelineItemBelongsToRun(item.id, target.id))
    }
  }
  return {
    ...withoutStreaming,
    runs,
    timeline: appendTimelineItem(withoutStreaming.timeline, {
      id: target.id,
      kind: 'run',
      sortAt: isoSortValue(target.sortAt || target.createdAt),
      run: runs[targetIndex]
    })
  }
}

function removeStreamingMessages(discussion: DiscussionViewModel) {
  return {
    ...discussion,
    messages: discussion.messages.filter((message) => !message.id.startsWith('streaming-')),
    timeline: discussion.timeline.filter((item) => !item.id.startsWith('streaming-'))
  }
}

function removeStreamingMessagesForRun(discussion: DiscussionViewModel, runId: string) {
  const prefix = `streaming-${runId}`
  return {
    ...discussion,
    messages: discussion.messages.filter((message) => !streamingItemBelongsToRun(message.id, prefix)),
    timeline: discussion.timeline.filter((item) => !streamingItemBelongsToRun(item.id, prefix))
  }
}

function applyRunStartedEvent(discussion: DiscussionViewModel, runDto: DiscussionRunDTO) {
  const run = {
    ...mapDiscussionRun(runDto, undefined, discussionMemberNamesByID(discussion.members)),
    mode: runDto.mode || discussion.kind
  }
  const existing = discussion.runs.find((item) => item.id === run.id)
  if (existing && !runtimeRunAcceptsLiveEvents(existing.status) && runtimeRunAcceptsLiveEvents(run.status)) {
    return discussion
  }
  return upsertRun({ ...discussion, status: 'running', runtimeStatus: run.status }, run, Boolean(existing))
}

function applyTokenDelta(discussion: DiscussionViewModel, delta: string, runId?: string, sequence?: number) {
  if (!delta) return discussion
  const targetRunId = (runId || '').trim()
  if (discussionEventSequenceIsStale(discussion, targetRunId, sequence)) return discussion
  const latestRun = targetRunId ? discussion.runs.find((run) => run.id === targetRunId) : discussion.runs.at(-1)
  if (!latestRun || !runtimeRunAcceptsLiveEvents(latestRun.status)) return discussion
  if (!isOneToOneDiscussionKind(discussion.kind) && latestRun.hasPlan) {
    return upsertRun(discussion, { ...latestRun, preview: appendRunPreview(latestRun.preview, delta) })
  }
  const now = streamingTokenSortAt(discussion, latestRun)
  const messageId = nextStreamingMessageId(discussion, latestRun)
  const existing = discussion.messages.find((message) => message.id === messageId)
  const message: DiscussionMessageViewModel = {
    id: messageId,
    role: 'assistant',
    authorId: latestRun.logicalAgentId || latestRun.agentId,
    authorName: latestRun.agentName || latestRun.agentId,
    content: `${existing?.content ?? ''}${delta}`,
    sortAt: existing?.sortAt || now,
    createdAt: existing?.createdAt || formatDate(now)
  }
  if (existing) {
    const messages = [...discussion.messages]
    const messageIndex = messages.findIndex((item) => item.id === messageId)
    messages[messageIndex] = message
    const timeline = [...discussion.timeline]
    const timelineIndex = timeline.findIndex((item) => item.id === messageId)
    if (timelineIndex >= 0) {
      timeline[timelineIndex] = { ...timeline[timelineIndex], message }
    } else {
      timeline.push({ id: message.id, kind: 'message', sortAt: message.sortAt || now, message })
    }
    return { ...discussion, messages, timeline }
  }
  const messages = orderDiscussionMessagesByOccurrence([...discussion.messages, message])
  return {
    ...discussion,
    messages,
    timeline: orderSnapshotTimelineByOccurrence(
      appendTimelineItem(discussion.timeline, {
        id: message.id,
        kind: 'message',
        sortAt: message.sortAt || now,
        message
      })
    )
  }
}

function applyThinkingDelta(discussion: DiscussionViewModel, delta: string, runId?: string, sequence?: number) {
  if (!delta) return discussion
  const targetRunId = (runId || '').trim()
  if (discussionEventSequenceIsStale(discussion, targetRunId, sequence)) return discussion
  const latestRun = targetRunId ? discussion.runs.find((run) => run.id === targetRunId) : discussion.runs.at(-1)
  if (!latestRun || !runtimeRunAcceptsLiveEvents(latestRun.status)) return discussion
  return upsertRun(discussion, {
    ...latestRun,
    thinking: appendRunThinking(latestRun.thinking, delta)
  })
}

function upsertRun(discussion: DiscussionViewModel, run: DiscussionRunViewModel, partial = false) {
  const existingIndex = discussion.runs.findIndex((item) => item.id === run.id)
  const runs = [...discussion.runs]
  const existing = existingIndex >= 0 ? runs[existingIndex] : undefined
  const mergedRun = existing && partial
    ? {
        ...existing,
        ...run,
        runtimeKind: run.runtimeKind || existing.runtimeKind,
        turnId: run.turnId || existing.turnId,
        mode: run.mode || existing.mode,
        hasPlan: run.hasPlan || existing.hasPlan,
        toolCalls: run.toolCalls.length > 0 ? mergeToolCalls(existing.toolCalls, run.toolCalls) : existing.toolCalls,
        usage: run.usage ?? existing.usage,
        durationMs: run.durationMs ?? existing.durationMs,
        planId: run.planId || existing.planId,
        planStatus: run.planStatus || existing.planStatus,
        planSteps: run.planSteps?.length ? run.planSteps : existing.planSteps,
        thinking: run.thinking ?? existing.thinking,
        preview: run.preview ?? existing.preview
      }
    : existing && ((existing.planSteps?.length && !run.planSteps?.length) || (existing.thinking && !run.thinking))
      ? {
          ...run,
          hasPlan: run.hasPlan || existing.hasPlan,
          planId: existing.planId,
          planStatus: existing.planStatus,
          planSteps: existing.planSteps,
          thinking: run.thinking ?? existing.thinking,
          preview: existing.preview
        }
      : run
  if (existingIndex >= 0) {
    runs[existingIndex] = mergedRun
  } else {
    runs.push(mergedRun)
  }
  const next = {
    ...discussion,
    runs,
    timeline: upsertRunTimelineItems(discussion.timeline, mergedRun, discussion.kind)
  }
  return { ...next, activeResponders: activeResponderNames(next.runs, next.status) }
}

function replaceStreamingMessage(
  discussion: DiscussionViewModel,
  completed: DiscussionMessageViewModel,
  runId?: string,
  strictCorrelation = false
) {
  const targetRunId = (runId || completed.runId || '').trim()
  if (strictCorrelation && completed.role === 'assistant' && !targetRunId) return discussion
  const prepared = applyCompletedMessagePlans(discussion, completed)
  const projectedCompleted = strictCorrelation
    ? {
        ...completed,
        sortAt: afterLatestTimelineSortAt(prepared.timeline, completed.sortAt || completed.createdAt)
      }
    : completed
  const streamingPrefix = targetRunId ? `streaming-${targetRunId}` : 'streaming-'
  const messages = prepared.messages.filter((message) => !streamingItemBelongsToRun(message.id, streamingPrefix))
  const existingIndex = messages.findIndex((message) => message.id === projectedCompleted.id)
  if (existingIndex >= 0) {
    messages[existingIndex] = projectedCompleted
  } else {
    messages.push(projectedCompleted)
  }
  const orderedMessages = orderDiscussionMessagesByOccurrence(messages)
  const baseTimeline = prepared.timeline.filter((item) => !streamingItemBelongsToRun(item.id, streamingPrefix))
  const baseSortAt = isoSortValue(projectedCompleted.sortAt || projectedCompleted.createdAt)
  const displayItems = isOneToOneDiscussionKind(prepared.kind)
    ? completedMessageTimelineItems(projectedCompleted, prepared.runs, baseSortAt, undefined, prepared.kind)
    : []
  const timelineBase = displayItems.length > 0 ? removeTimelineItemsRepresentedByDisplayItems(baseTimeline, displayItems) : baseTimeline
  const timeline =
    displayItems.length > 0
      ? [...timelineBase, ...displayItems]
      : appendTimelineItem(baseTimeline, {
          id: projectedCompleted.id,
          kind: 'message',
          sortAt: baseSortAt,
          message: projectedCompleted
        })
  return {
    ...prepared,
    messages: orderedMessages,
    timeline: orderSnapshotTimelineByOccurrence(timeline)
  }
}

function applyCompletedMessagePlans(discussion: DiscussionViewModel, message: DiscussionMessageViewModel) {
  let next = discussion
  for (const segment of message.displayTimeline ?? []) {
    if (segment.kind !== 'plan' || !segment.runId || !segment.plan) continue
    next = applyPlanEvent(next, segment.plan, segment.runId)
  }
  return next
}

function applyToolEvent(
  discussion: DiscussionViewModel,
  tool: {
    tool_call_id: string
    name: string
    status: string
    arguments?: Record<string, string>
    result?: Record<string, string>
    created_at?: string
  },
  runId?: string,
  strictCorrelation = false
) {
  const targetRunId = (runId || '').trim()
  if (strictCorrelation && !targetRunId) return discussion
  const latestRun = (targetRunId ? discussion.runs.find((run) => run.id === targetRunId) : undefined) ?? discussion.runs.find((run) => run.toolCalls.some((item) => item.id === tool.tool_call_id)) ?? discussion.runs.at(-1)
  if (!latestRun) return discussion
  const toolCalls = [...latestRun.toolCalls]
  const existingIndex = toolCalls.findIndex((item) => item.id === tool.tool_call_id)
  const previous = existingIndex >= 0 ? toolCalls[existingIndex] : undefined
  const mapped = {
    id: tool.tool_call_id,
    name: tool.name || previous?.name || 'tool',
    status: tool.status,
    arguments: tool.arguments ?? previous?.arguments,
    result: tool.result ?? previous?.result,
    createdAt: previous?.createdAt || formatDate(tool.created_at || latestRun.createdAt),
    // Stream timestamps can come from different clocks. The event stream itself
    // is the authoritative execution order, so assign new calls monotonically
    // and keep that position when later status/result updates arrive.
    sortAt: previous?.sortAt || nextLiveToolSortAt(discussion, latestRun)
  }
  if (existingIndex >= 0) {
    toolCalls[existingIndex] = mapped
  } else {
    toolCalls.push(mapped)
  }
  const next = upsertRun(discussion, { ...latestRun, toolCalls: orderToolCallsByOccurrence(toolCalls) })
  const linkedMissions = linkedMissionsFromToolResult(mapped.result)
  if (linkedMissions.length === 0) return next
  return {
    ...next,
    linkedMissions: mergeLinkedMissionViews(next.linkedMissions, linkedMissions)
  }
}

function linkedMissionsFromToolResult(result: Record<string, string> | undefined) {
  const raw = result?.linked_missions_json?.trim()
  if (!raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.flatMap((value) => {
      if (!value || typeof value !== 'object' || Array.isArray(value)) return []
      const mission = value as Record<string, unknown>
      const id = String(mission.mission_id ?? '').trim()
      if (!id) return []
      return [{
        id,
        title: String(mission.title ?? '').trim() || id,
        status: String(mission.status ?? '').trim(),
        href: `/missions/${id}`
      }]
    })
  } catch {
    return []
  }
}

function mergeLinkedMissionViews(
  current: DiscussionViewModel['linkedMissions'],
  incoming: DiscussionViewModel['linkedMissions']
) {
  const byID = new Map(current.map((mission) => [mission.id, mission]))
  for (const mission of incoming) {
    const previous = byID.get(mission.id)
    byID.set(mission.id, previous ? { ...previous, ...mission } : mission)
  }
  return [...byID.values()]
}

function applyPlanEvent(discussion: DiscussionViewModel, rawPlan: unknown, runId?: string, strictCorrelation = false) {
  const plan = parseDisplayPlan(rawPlan)
  if (!plan) return discussion
  const targetRunId = (runId || '').trim()
  if (strictCorrelation && !targetRunId) return discussion
  const latestRun = targetRunId ? discussion.runs.find((run) => run.id === targetRunId) : discussion.runs.at(-1)
  if (!latestRun) return discussion
  const streaming = isOneToOneDiscussionKind(discussion.kind) ? discussion : removeStreamingMessagesForRun(discussion, latestRun.id)
  const preview = isOneToOneDiscussionKind(discussion.kind) ? latestRun.preview : streamingPreviewForRun(discussion, latestRun.id) || latestRun.preview
  const run = {
    ...latestRun,
    hasPlan: true,
    planId: plan.id,
    planStatus: plan.status,
    planSteps: plan.items,
    preview
  }
  const runs = streaming.runs.map((item) => (item.id === run.id ? run : item))
  const timeline = upsertLivePlanTimelineItem(streaming.timeline, run)
  const next = {
    ...streaming,
    runs,
    timeline: orderSnapshotTimelineByOccurrence(timeline)
  }
  return { ...next, activeResponders: activeResponderNames(next.runs, next.status) }
}

function streamingPreviewForRun(discussion: DiscussionViewModel, runId: string) {
  const prefix = `streaming-${runId}`
  return discussion.messages
    .filter((message) => streamingItemBelongsToRun(message.id, prefix))
    .map((message) => message.content)
    .join('')
}

function streamingItemBelongsToRun(itemId: string, prefix: string) {
  if (prefix === 'streaming-') return itemId.startsWith(prefix)
  return itemId === prefix || itemId.startsWith(`${prefix}:`)
}

function appendRunPreview(current: string | undefined, delta: string) {
  const next = `${current ?? ''}${delta}`
  return next.length > 1200 ? next.slice(-1200) : next
}

function appendRunThinking(current: string | undefined, delta: string) {
  const next = `${current ?? ''}${delta}`
  return next.length > 8000 ? next.slice(-8000) : next
}

function mergeToolCalls(current: DiscussionToolCallViewModel[], incoming: DiscussionToolCallViewModel[]) {
  const byId = new Map(current.map((tool) => [tool.id, tool]))
  for (const tool of incoming) {
    const existing = byId.get(tool.id)
    byId.set(tool.id, existing
      ? {
          ...existing,
          ...tool,
          arguments: tool.arguments ?? existing.arguments,
          result: tool.result ?? existing.result,
          createdAt: existing.createdAt || tool.createdAt,
          sortAt: existing.sortAt || tool.sortAt
        }
      : tool)
  }
  return orderToolCallsByOccurrence([...byId.values()])
}

function orderToolCallsByOccurrence(tools: DiscussionToolCallViewModel[], preferredOrder: string[] = []) {
  if (preferredOrder.length === 0 || tools.length < 2) return tools

  const toolsByID = new Map(tools.map((tool) => [tool.id, tool]))
  const preferred = preferredOrder
    .filter((id, index) => preferredOrder.indexOf(id) === index)
    .map((id) => toolsByID.get(id))
    .filter((tool): tool is DiscussionToolCallViewModel => Boolean(tool))
  if (preferred.length < 2) return tools

  const preferredIDs = new Set(preferred.map((tool) => tool.id))
  let preferredIndex = 0
  return tools.map((tool) => preferredIDs.has(tool.id) ? preferred[preferredIndex++] : tool)
}

function buildDiscussionTimeline(dto: DiscussionDTO, messages: DiscussionMessageViewModel[], runs: DiscussionRunViewModel[]): DiscussionTimelineItemViewModel[] {
  const rawMessageTimes = new Map((dto.messages ?? []).map((message) => [message.message_id, message.created_at]))
  const rawRuns = new Map((dto.runs ?? []).map((run) => [run.run_id, run]))
  const approvals = discussionRuntimeApprovals(dto)
  const messageItems = discussionMessageTimelineItems(messages, runs, rawMessageTimes, dto.conversation_kind || 'group')
  const runItems = runs.flatMap((run) => discussionRunTimelineItems(run, rawRuns.get(run.id), dto.conversation_kind || 'group'))
  const timeline: DiscussionTimelineItemViewModel[] = [
    ...messageItems,
    ...removeTimelineItemsRepresentedByDisplayItems(runItems, messageItems),
    ...approvals.map((approval) => ({
      id: `approval:${approval.id}`,
      kind: 'approval' as const,
      sortAt: approval.createdAtRaw || approval.createdAt,
      approval
    }))
  ]
  return orderSnapshotTimelineByOccurrence(timeline)
}

function discussionMessageTimelineItems(
  messages: DiscussionMessageViewModel[],
  runs: DiscussionRunViewModel[],
  rawMessageTimes: Map<string, string>,
  conversationKind: string
) {
  const runByID = new Map(runs.map((run) => [run.id, run]))
  return messages.flatMap((message) => {
    const baseSortAt = rawMessageTimes.get(message.id) || message.sortAt || message.createdAt
    const linkedRun = message.runId ? runByID.get(message.runId) : undefined
    const projectsRunProgress = Boolean(linkedRun && runHasStructuredProgress(linkedRun, conversationKind))
    if (!isOneToOneDiscussionKind(conversationKind) || ((!message.displayTimeline || message.displayTimeline.length === 0) && !projectsRunProgress)) {
      return [
        {
          id: message.id,
          kind: 'message' as const,
          sortAt: baseSortAt,
          message
        }
      ]
    }
    return completedMessageTimelineItems(message, runs, baseSortAt, runByID, conversationKind)
  })
}

function completedMessageTimelineItems(
  message: DiscussionMessageViewModel,
  runs: DiscussionRunViewModel[],
  baseSortAt: string,
  providedRunByID?: Map<string, DiscussionRunViewModel>,
  conversationKind?: string
): DiscussionTimelineItemViewModel[] {
  const runByID = providedRunByID ?? new Map(runs.map((run) => [run.id, run]))
  const representedRunIDs: string[] = []
  const latestPlanByRun = new Map<string, NonNullable<DiscussionDisplayTimelineSegmentViewModel['plan']>>()
  for (const segment of message.displayTimeline ?? []) {
    if ((segment.kind !== 'tool' && segment.kind !== 'plan') || !segment.runId) continue
    if (!representedRunIDs.includes(segment.runId)) representedRunIDs.push(segment.runId)
    if (segment.kind === 'plan' && segment.plan) latestPlanByRun.set(segment.runId, segment.plan)
  }
  if (representedRunIDs.length === 0 && message.runId && runByID.has(message.runId)) representedRunIDs.push(message.runId)

  const runItems = representedRunIDs.flatMap((runID, index): DiscussionTimelineItemViewModel[] => {
    const run = runByID.get(runID)
    if (!run || !runHasStructuredProgress(run, conversationKind)) return []
    const plan = latestPlanByRun.get(runID)
    const sortAt = adjacentSortAt(baseSortAt, index - representedRunIDs.length)
    return [{
      id: run.id,
      kind: 'run',
      sortAt,
      displaySegment: true,
      run: {
        ...run,
        sortAt,
        planId: plan?.id || run.planId,
        planStatus: plan?.status || run.planStatus,
        planSteps: plan?.items.length ? plan.items : run.planSteps
      }
    }]
  })
  return [
    ...runItems,
    {
      id: message.id,
      kind: 'message',
      sortAt: isoSortValue(baseSortAt),
      displaySegment: true,
      message
    }
  ]
}

function discussionRunTimelineItems(run: DiscussionRunViewModel, rawRun: DiscussionRunDTO | undefined, conversationKind?: string): DiscussionTimelineItemViewModel[] {
  if (!runHasStructuredProgress(run, conversationKind)) return []
  return [
    {
      id: run.id,
      kind: 'run',
      sortAt: discussionRunTimelineSortAt(run, rawRun),
      run
    }
  ]
}

function upsertRunTimelineItems(timeline: DiscussionTimelineItemViewModel[] | undefined, run: DiscussionRunViewModel, conversationKind?: string) {
  const existing = timeline ?? []
  if (!runHasStructuredProgress(run, conversationKind)) {
    return existing.filter((item) => item.kind !== 'run' || item.displaySegment || !runTimelineItemBelongsToRun(item.id, run.id))
  }
  const current = [...existing].reverse().find((item) => item.kind === 'run' && runTimelineItemBelongsToRun(item.id, run.id))
  return appendTimelineItem(existing.filter((item) => item.kind !== 'run' || !runTimelineItemBelongsToRun(item.id, run.id)), {
    id: run.id,
    kind: 'run',
    sortAt: current?.sortAt || existingTimelineSortAt(existing, run.id, 'run') || isoSortValue(run.sortAt || run.createdAt),
    displaySegment: current?.displaySegment,
    run
  })
}

function runHasStructuredProgress(run: DiscussionRunViewModel, conversationKind?: string) {
  return Boolean(run.hasPlan || run.planSteps?.length || run.thinking?.trim() || isOneToOneDiscussionKind(run.mode) || isOneToOneDiscussionKind(conversationKind))
}

function isOneToOneDiscussionKind(kind: string | undefined) {
  return kind === 'direct' || kind === 'personal_assistant'
}

function upsertLivePlanTimelineItem(timeline: DiscussionTimelineItemViewModel[] | undefined, run: DiscussionRunViewModel) {
  const existing = timeline ?? []
  const current = [...existing].reverse().find((item) => item.kind === 'run' && runTimelineItemBelongsToRun(item.id, run.id))
  return appendTimelineItem(
    existing.filter((item) => item.kind !== 'run' || !runTimelineItemBelongsToRun(item.id, run.id)),
    {
      id: run.id,
      kind: 'run',
      sortAt: current?.sortAt || existingTimelineSortAt(existing, run.id, 'run') || nextTimelineSortAt(existing, run.sortAt || run.createdAt),
      displaySegment: current?.displaySegment,
      run
    }
  )
}

function runTimelineItemBelongsToRun(itemId: string, runId: string) {
  return itemId === runId || itemId.startsWith(`${runId}:tool:`) || itemId.startsWith(`${runId}:plan:`)
}

function runtimeRunAcceptsLiveEvents(status: string) {
  const normalized = status.trim().toLowerCase()
  return normalized === 'running' || normalized === 'started'
}

function removeTimelineItemsRepresentedByDisplayItems(timeline: DiscussionTimelineItemViewModel[], displayItems: DiscussionTimelineItemViewModel[]) {
  const representedRuns = representedTimelineRuns(displayItems)
  if (representedRuns.size === 0) return timeline
  return timeline.filter((item) => item.kind !== 'run' || !item.run || !representedRuns.has(item.run.id))
}

function representedTimelineRuns(timeline: DiscussionTimelineItemViewModel[] | undefined) {
  const represented = new Set<string>()
  for (const item of timeline ?? []) {
    if (item.kind !== 'run' || !item.run) continue
    represented.add(item.run.id)
  }
  return represented
}

function nextStreamingMessageId(discussion: DiscussionViewModel, run: DiscussionRunViewModel) {
  const base = `streaming-${run.id}`
  const lastMessage = [...discussion.timeline].reverse().find((item) => item.kind === 'message' && item.id.startsWith(base))
  if (!lastMessage) return base
  if (run.toolCalls.length === 0) return lastMessage.id

  const lastRunIndex = discussion.timeline.findLastIndex((item) => item.kind === 'run' && runTimelineItemBelongsToRun(item.id, run.id))
  if (lastRunIndex < 0 || discussion.timeline.indexOf(lastMessage) > lastRunIndex) return lastMessage.id

  const used = new Set(discussion.messages.map((message) => message.id))
  let index = 2
  while (used.has(`${base}:${index}`)) index += 1
  return `${base}:${index}`
}

function streamingTokenSortAt(discussion: DiscussionViewModel, run: DiscussionRunViewModel) {
  if (run.toolCalls.length === 0) return afterLatestTimelineSortAt(discussion.timeline, new Date().toISOString())
  const runItem = [...discussion.timeline].reverse().find((item) => item.kind === 'run' && runTimelineItemBelongsToRun(item.id, run.id))
  const runSortAt = runItem?.sortAt || run.sortAt || run.createdAt
  return nextTimelineSortAt(discussion.timeline, adjacentSortAt(runSortAt, 1))
}

function adjacentSortAt(value: string, offsetMs: number) {
  const parsed = Date.parse(isoSortValue(value))
  if (Number.isNaN(parsed)) return new Date().toISOString()
  return new Date(parsed + offsetMs).toISOString()
}

function nextLiveToolSortAt(discussion: DiscussionViewModel, run: DiscussionRunViewModel) {
  const previous = run.toolCalls.at(-1)
  const previousTime = Date.parse(isoSortValue(previous?.sortAt || ''))
  if (!Number.isNaN(previousTime)) return new Date(previousTime + 1).toISOString()
  return nextTimelineSortAt(discussion.timeline, run.sortAt || run.createdAt)
}

function afterLatestTimelineSortAt(timeline: DiscussionTimelineItemViewModel[] | undefined, fallback: string) {
  const latest = latestTimelineTime(timeline)
  if (latest === undefined) return isoSortValue(fallback)
  return new Date(latest + 1).toISOString()
}

function nextTimelineSortAt(timeline: DiscussionTimelineItemViewModel[] | undefined, fallback: string) {
  const latest = latestTimelineTime(timeline)
  if (latest === undefined) return isoSortValue(fallback)
  const fallbackTime = Date.parse(isoSortValue(fallback))
  return new Date(Math.max(latest + 1, Number.isNaN(fallbackTime) ? latest + 1 : fallbackTime)).toISOString()
}

function latestTimelineTime(timeline: DiscussionTimelineItemViewModel[] | undefined) {
  return [...(timeline ?? [])]
    .map((item) => Date.parse(isoSortValue(item.sortAt)))
    .filter((value) => !Number.isNaN(value))
    .sort((a, b) => a - b)
    .at(-1)
}

function discussionRuntimeApprovals(dto: DiscussionDTO) {
  const approvalEvents = (dto.events ?? []).filter((event) => event.kind === 'discussion_runtime_approval_requested')
  const items = approvalEvents.map((event) => discussionRuntimeApprovalDTO(dto, event)).filter((item): item is ApprovalItemDTO => Boolean(item))
  if (items.length === 0) return []
  const mapped = mapApprovalCenterView({ items } satisfies ApprovalPageDTO).items
  return mapped.map((approval, index) => {
    const metadata = approvalEvents[index]?.metadata ?? {}
    return {
      ...approval,
      hilDecisionId: metadata.hil_decision_id?.trim() || undefined,
      runtimeRunId: metadata.approval_run_id?.trim() || approval.taskId,
      runtimeToolCallId: metadata.approval_tool_use_id?.trim() || '',
      runtimeRequestId: metadata.approval_request_id?.trim() || ''
    }
  })
}

function discussionRuntimeApprovalDTO(dto: DiscussionDTO, event: DiscussionEventDTO): ApprovalItemDTO | undefined {
  if (event.kind !== 'discussion_runtime_approval_requested') return undefined
  const metadata = event.metadata ?? {}
  const requestID = (metadata.approval_request_id || event.event_id).trim()
  if (!requestID) return undefined
  const approvalID = (event.event_id || requestID).trim()
  if (!approvalID) return undefined

  const discussionID = dto.discussion_id
  const approvalType = metadata.approval_type || metadata.approval_kind || 'permission_request'
  const displayKind = approvalDisplayKind(approvalType)
  const hilDecisionID = metadata.hil_decision_id?.trim() || ''
  const path = hilDecisionID
    ? `/api/hil/decisions/${encodeURIComponent(hilDecisionID)}/decision`
    : `/api/discussions/${encodeURIComponent(discussionID)}/approvals/${encodeURIComponent(approvalID)}/decision`
  const toolCall = discussionApprovalToolCall(dto, metadata)
  const toolName = metadata.approval_tool_name || metadata.approval_native_tool_name || toolCall?.name || 'runtime'
  const status = event.status || 'pending'
  const fallbackTitle = approvalTitle(displayKind, toolName)
  return {
    approval_id: approvalID,
    approval_type: approvalType,
    display_kind: displayKind,
    status,
    priority: approvalPriority(displayKind),
    mission_id: discussionID,
    task_id: metadata.approval_run_id || '',
    source_type: 'discussion',
    source_id: discussionID,
    provider: metadata.approval_provider || metadata.approval_source || 'runtime',
    tool_name: toolName,
    title: firstNonBlank(metadata.approval_title, metadata.approval_display_name, fallbackTitle),
    summary: firstNonBlank(metadata.approval_summary, metadata.approval_description, metadata.summary, event.summary, fallbackTitle),
    details: firstNonBlank(metadata.approval_details, metadata.details, metadata.decision_message, metadata.reason, metadata.approval_reason),
    risk_summary: runtimeApprovalRiskSummary(metadata),
    requested_by: metadata.approval_agent_id || dto.direct_agent_id || toolName,
    requested_by_actor: { actor_type: 'discussion', actor_id: discussionID },
    target_actor: metadata.approval_agent_id ? { actor_type: 'agent', actor_id: metadata.approval_agent_id } : undefined,
    payload_preview: approvalPayloadPreview(displayKind, metadata, toolCall?.arguments),
    available_actions: approvalIsPending(status)
      ? hilDecisionID
        ? discussionHILDecisionActions(path)
        : discussionRuntimeApprovalActions(event.available_actions, path)
      : [],
    created_at: event.created_at
  }
}

function runtimeApprovalRiskSummary(metadata: Record<string, string>) {
  const base = metadata.risk_summary?.trim() || ''
  const source = metadata.approval_decision_source?.trim() || ''
  const parts: string[] = []
  if (source === 'allowlist') {
    parts.push('Allowlist')
    if (metadata.approval_rule_id?.trim()) parts.push(`rule ${metadata.approval_rule_id.trim()}`)
  } else if (source === 'full') {
    parts.push('Full-trust policy')
  } else if (source === 'ai_reviewer') {
    parts.push('AI reviewer')
    if (metadata.approval_reviewer_risk?.trim()) parts.push(`${metadata.approval_reviewer_risk.trim()} risk`)
    const rationale = firstNonBlank(metadata.approval_reviewer_rationale, metadata.approval_reviewer_reason)
    if (rationale) parts.push(rationale)
  } else if (source === 'forced_human') {
    parts.push('Forced human review')
  } else if (source === 'policy') {
    parts.push('Runtime approval policy')
  } else if (source === 'human') {
    parts.push('Human review')
  }
  if (parts.length > 0 && source !== 'ai_reviewer' && metadata.approval_decision_reason?.trim()) {
    parts.push(metadata.approval_decision_reason.trim())
  }
  const provenance = parts.join(' | ')
  if (!base) return provenance
  if (!provenance || base.includes(provenance)) return base
  return `${base} | ${provenance}`
}

function approvalDisplayKind(value: string) {
  if (value === 'mcp_elicitation') return 'user_input'
  if (value === 'tool_access') return 'tool_use'
  if (value === 'permission_request') return 'permission'
  return value || 'permission'
}

function approvalPriority(kind: string) {
  if (kind === 'command_execution' || kind === 'file_change') return 'high'
  if (kind === 'user_input') return 'normal'
  return 'normal'
}

function approvalTitle(kind: string, toolName: string) {
  if (kind === 'command_execution') return `Command approval: ${toolName}`
  if (kind === 'user_input') return 'Runtime question'
  if (kind === 'file_change') return `File approval: ${toolName}`
  return `Runtime approval: ${toolName}`
}

function discussionApprovalToolCall(dto: DiscussionDTO, metadata: Record<string, string>) {
  const runID = (metadata.approval_run_id || '').trim()
  const toolCallID = firstNonBlank(metadata.approval_tool_use_id, metadata.approval_request_id)
  const runs = runID ? (dto.runs ?? []).filter((run) => run.run_id === runID) : (dto.runs ?? [])
  if (toolCallID) {
    const exact = runs.flatMap((run) => run.tool_calls ?? []).find((tool) => tool.tool_call_id === toolCallID)
    if (exact) return exact
  }
  const toolName = firstNonBlank(metadata.approval_tool_name, metadata.approval_native_tool_name)
  if (!runID || !toolName) return undefined
  const named = runs.flatMap((run) => run.tool_calls ?? []).filter((tool) => tool.name === toolName)
  return named.length === 1 ? named[0] : undefined
}

function normalizeDiscussionHILMode(mode: unknown): 'auto' | 'manual' | 'none' {
  if (mode === 'auto' || mode === 'none') return mode
  return 'manual'
}

const approvalIntentFieldKeys = [
  'title',
  'final_goal',
  'root_goal',
  'goal',
  'task_goal',
  'business_direction',
  'plan_summary',
  'query',
  'prompt',
  'command',
  'path',
  'url',
  'content',
  'summary',
  'description',
  'message',
  'reason',
  'role',
  'capability'
] as const

function approvalPayloadPreview(kind: string, metadata: Record<string, string>, toolArguments?: Record<string, string>): ApprovalPayloadDTO {
  const safeArguments = {
    ...approvalSafeToolArguments(metadata),
    ...approvalSafeToolArguments(toolArguments)
  }
  const content = approvalPayloadContentForKind(kind, metadata, safeArguments)
  const fieldKeys = ['sandbox_permissions', 'scope', 'path', 'url', 'server', 'prompt', 'approval_display_name', 'approval_description', 'approval_reason']
  return {
    kind,
    language: kind === 'command_execution' ? 'shell' : '',
    title: firstNonBlank(metadata.approval_display_name, metadata.approval_title, approvalTitle(kind, metadata.approval_tool_name || metadata.approval_native_tool_name || 'runtime')),
    content,
    fields: {
      ...Object.fromEntries(fieldKeys.map((key) => [key, metadata[key]] as const).filter(([, value]) => approvalDisplayValueIsSafe(value))),
      ...safeArguments
    }
  }
}

function approvalSafeToolArguments(toolArguments?: Record<string, string>) {
  if (!toolArguments) return {}
  return Object.fromEntries(
    approvalIntentFieldKeys
      .map((key) => [key, toolArguments[key]] as const)
      .filter(([, value]) => approvalDisplayValueIsSafe(value))
  )
}

function approvalPayloadContentForKind(kind: string, metadata: Record<string, string>, toolArguments: Record<string, string>) {
  if (kind === 'command_execution') return firstSafeApprovalValue(metadata.command, metadata.payload_preview, metadata.prompt, metadata.approval_summary, toolArguments.command)
  if (kind === 'file_change') return firstSafeApprovalValue(metadata.diff, metadata.payload_preview, metadata.prompt, metadata.path, metadata.approval_summary, toolArguments.content, toolArguments.path)
  if (kind === 'network_access' || metadata.url || toolArguments.url) return firstSafeApprovalValue(metadata.prompt, metadata.payload_preview, metadata.approval_summary, metadata.url, toolArguments.query, toolArguments.prompt, toolArguments.url)
  return firstSafeApprovalValue(metadata.payload_preview, metadata.prompt, metadata.question, metadata.command, metadata.path, metadata.url, metadata.approval_summary, ...approvalIntentFieldKeys.map((key) => toolArguments[key]))
}

function firstSafeApprovalValue(...values: Array<string | undefined>) {
  return values.map((value) => (value || '').trim()).find(approvalDisplayValueIsSafe) || ''
}

function approvalDisplayValueIsSafe(value: string | undefined): value is string {
  if (!value?.trim()) return false
  const normalized = value.trim()
  if (/^(?:call|toolu|tool_call|request|req)[_-][a-z0-9_-]{8,}$/i.test(normalized)) return false
  return !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(normalized)
}

function approvalIsPending(status: string) {
  const normalized = status.trim().toLowerCase()
  return normalized === '' || normalized === 'pending' || normalized === 'open'
}

function discussionHILDecisionActions(path: string): ApprovalActionDTO[] {
  return [
    discussionRuntimeApprovalAction('approve', 'Approve', 'primary', 'approved', 'once', 'message', path),
    discussionRuntimeApprovalAction('reject', 'Reject', 'danger', 'denied', 'deny', 'reason', path, { reasonRequired: true })
  ]
}

function discussionRuntimeApprovalActions(actions: ApprovalActionDTO[] | undefined, path: string): ApprovalActionDTO[] {
  if (actions && actions.length > 0 && runtimeApprovalHasUnifiedActions(actions)) return actions
  return [discussionRuntimeApprovalAction('approve_once', 'Approve once', 'primary', 'approved', 'once', 'message', path), discussionRuntimeApprovalAction('approve_session', 'Approve for session', 'primary', 'approved', 'session', 'message', path, { rememberAllowed: true }), discussionRuntimeApprovalAction('deny', 'Deny', 'danger', 'denied', 'deny', 'message', path), discussionRuntimeApprovalAction('other', 'Other', 'secondary', 'approved', 'once', 'message', path)]
}

function runtimeApprovalHasUnifiedActions(actions: ApprovalActionDTO[]) {
  const ids = actions.map((action) => action.action_id)
  return ids.length === 4 && ids[0] === 'approve_once' && ids[1] === 'approve_session' && ids[2] === 'deny' && ids[3] === 'other'
}

function discussionRuntimeApprovalAction(
  actionID: string,
  label: string,
  style: string,
  decision: string,
  effect: string,
  inputKind: string,
  path: string,
  options: {
    reasonRequired?: boolean
    allowsModification?: boolean
    rememberAllowed?: boolean
  } = {}
): ApprovalActionDTO {
  return {
    action_id: actionID,
    label,
    style,
    decision,
    effect,
    input_kind: inputKind,
    method: 'POST',
    path,
    requires_body: true,
    confirmation_required: true,
    reason_required: options.reasonRequired === true,
    allows_modification: options.allowsModification === true,
    remember_allowed: options.rememberAllowed === true
  }
}

function appendTimelineItem(timeline: DiscussionTimelineItemViewModel[] | undefined, item: DiscussionTimelineItemViewModel) {
  const next = [...(timeline ?? [])]
  const existingIndex = next.findIndex((entry) => entry.id === item.id && entry.kind === item.kind)
  if (existingIndex >= 0) {
    next[existingIndex] = item
  } else {
    next.push(item)
  }
  return next
}

function existingTimelineSortAt(timeline: DiscussionTimelineItemViewModel[] | undefined, id: string, kind: DiscussionTimelineItemViewModel['kind']) {
  return timeline?.find((entry) => entry.id === id && entry.kind === kind)?.sortAt ?? ''
}

function orderSnapshotTimelineByOccurrence(timeline: DiscussionTimelineItemViewModel[]) {
  return timeline
    .map((item, index) => ({
      item,
      index,
      time: Date.parse(isoSortValue(item.sortAt))
    }))
    .sort((a, b) => {
      if (!Number.isNaN(a.time) && !Number.isNaN(b.time) && a.time !== b.time) return a.time - b.time
      return a.index - b.index
    })
    .map((entry) => entry.item)
}

function orderDiscussionMessagesByOccurrence<T extends { sortAt?: string; createdAt?: string }>(messages: T[]) {
  return messages
    .map((message, index) => ({
      message,
      index,
      time: Date.parse(isoSortValue(message.sortAt || message.createdAt || ''))
    }))
    .sort((a, b) => {
      if (!Number.isNaN(a.time) && !Number.isNaN(b.time) && a.time !== b.time) return a.time - b.time
      return a.index - b.index
    })
    .map((entry) => entry.message)
}

function discussionRunSortAt(run: DiscussionRunDTO, hasToolCalls: boolean) {
  const runStart = firstMeaningfulTime(run.started_at, run.created_at)
  if (!hasToolCalls) return firstMeaningfulTime(run.completed_at, runStart)
  return firstMeaningfulTime(run.completed_at) || latestToolCallTime(run.tool_calls ?? []) || runStart
}

function discussionRunTimelineSortAt(run: DiscussionRunViewModel, rawRun: DiscussionRunDTO | undefined) {
  const base = run.sortAt || run.createdAt
  if (!rawRun || run.toolCalls.length === 0) return base
  return base || rawRun.created_at
}

function latestToolCallTime(tools: NonNullable<DiscussionRunDTO['tool_calls']>) {
  return tools
    .map((tool) => tool.created_at)
    .filter((value): value is string => Boolean(value) && isMeaningfulTime(value))
    .sort()
    .at(-1)
}

function firstMeaningfulTime(...values: Array<string | undefined>) {
  return values.find(isMeaningfulTime) || ''
}

function uniqueLinkedMissions(missions: NonNullable<DiscussionDTO['linked_missions']>) {
  const byID = new Map<string, NonNullable<DiscussionDTO['linked_missions']>[number]>()
  for (const mission of missions) {
    if (!mission.mission_id || byID.has(mission.mission_id)) continue
    byID.set(mission.mission_id, mission)
  }
  return [...byID.values()].map((mission) => ({
    id: mission.mission_id,
    title: mission.title || mission.mission_id,
    status: mission.status,
    href: `/missions/${mission.mission_id}`
  }))
}

function renderPlainMentions(value: string) {
  return renderPlainDiscussionMentions(value)
}

function isoSortValue(value: string) {
  if (value === 'Streaming') return new Date().toISOString()
  return value
}

function firstNonBlank(...values: Array<string | undefined>) {
  return values.map((value) => (value || '').trim()).find(Boolean) || ''
}

function isMeaningfulTime(value: string | undefined): boolean {
  if (!value) return false
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return false
  // Filter out zero timestamps like "0001-01-01T00:00:00Z" (year 1 or less)
  return parsed.getFullYear() > 1
}

function formatDate(value: string | undefined) {
  if (!value) return 'Not provided'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return new Intl.DateTimeFormat('en', {
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  }).format(parsed)
}
