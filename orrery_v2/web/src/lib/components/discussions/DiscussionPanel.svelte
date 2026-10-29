<script lang="ts">
  import { onDestroy, onMount, type Snippet } from 'svelte'
  import { ArrowUp, Bot, Bug, CalendarClock, Check, CheckCircle2, ChevronDown, Circle, ChevronLeft, ChevronRight, Clipboard, CornerDownRight, Download, ExternalLink, FileDiff, FileText, FolderCog, FolderKanban, HelpCircle, ListTodo, LoaderCircle, Mic, MoreHorizontal, Network, Paperclip, RotateCcw, Settings2, ShieldAlert, Sparkles, Square, SquareTerminal, Target, Users, Wrench, X, Zap } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { approvalActionLabel } from '#lib/components/approvals/approvalActionLabels'
  import DiscussionToolCallList from '#lib/components/discussions/DiscussionToolCallList.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import { renderPlainDiscussionMentions, replaceDiscussionMentionLinks } from '#lib/discussionMentions'
  import { discussionRunTokenUsage } from '#lib/discussions/tokenUsage'
  import { createTranslator, locale } from '#lib/i18n'
  import { renderSafeMarkdown } from '#lib/markdown'
  import { isActiveTaskStatus, isDoneTaskStatus } from '#lib/mission/taskStatus'
  import { isCurrentSessionUserIdentity, session } from '#lib/stores/session.svelte'
  import type {
    DiscussionMessageViewModel,
    DiscussionHILMode,
    DiscussionRunDebugViewModel,
    DiscussionRunViewModel,
    DiscussionTimelineItemViewModel,
    DiscussionToolCallViewModel,
    DiscussionViewModel
  } from '#lib/types/discussions'
  import type { ApprovalActionViewModel, ApprovalDecisionFormViewModel, ApprovalItemViewModel, MissionControlViewModel, MissionOpsViewModel, MissionTaskViewModel, RuntimeSessionViewModel } from '#lib/types/view-models'

  type InlineApprovalForm = Pick<ApprovalDecisionFormViewModel, 'message' | 'modifiedInput' | 'choiceId' | 'remember'>
  type ApprovalPayloadField = ApprovalItemViewModel['payloadPreview']['fields'][number]
  type WorkforceAvatarIdentity = { profileId: string; human: boolean }
  type PendingInterjection = { turnId: string; runId: string; content: string }
  type MissionComposerRequest = {
    prompt: string
    launchMode: 'immediate' | 'scheduled'
    scheduledAt: string
  }
  interface Props {
    discussion?: DiscussionViewModel
    assistantMode?: boolean
    assistantEnabled?: boolean
    assistantAvatarUrl?: string
    assistantRuntimeLabel?: string
    assistantMissionCount?: number
    assistantPendingApprovalCount?: number
    headerLeadingActions?: Snippet
    avatarIdentityById?: Record<string, WorkforceAvatarIdentity>
    draft: string
    busy?: boolean
    errorMessage?: string
    debug?: DiscussionRunDebugViewModel
    debugRuns?: DiscussionRunViewModel[]
    selectedDebugRunId?: string
    debugOpen?: boolean
    debugLoading?: boolean
    debugError?: string
    missionContext?: MissionControlViewModel
    missionContextLoading?: boolean
    missionContextError?: string
    missionOps?: MissionOpsViewModel
    missionOpsLoading?: boolean
    missionOpsError?: string
    activeMissionId?: string
    pendingApprovals?: ApprovalItemViewModel[]
    pendingAttachments?: NonNullable<DiscussionViewModel['attachments']>
    attachmentEnabled?: boolean
    attachmentUploading?: boolean
    attachmentError?: string
    olderMessagesLoading?: boolean
    olderMessagesHasMore?: boolean
    olderMessagesError?: string
    approvalBusyIds?: string[]
    taskTimelineCollapsed?: boolean
    workspaceSettingsAvailable?: boolean
    hilModeBusy?: boolean
    hilModeEditable?: boolean
    onDraft?: (value: string) => void
    onComposerFocus?: () => void
    onMention?: (member: DiscussionViewModel['members'][number]) => void
    onSend?: () => void
    onRetryMessage?: (messageId: string, approve: boolean) => void
    onDebugOpen?: (runId?: string) => void
    onDebugRunSelect?: (runId: string) => void
    onDebugClose?: () => void
    onTaskTimelineToggle?: () => void
    onMissionStart?: (request: MissionComposerRequest) => void | Promise<void>
    onNewSession?: () => void
    onWorkspaceSettingsOpen?: () => void
    onHILModeChange?: (mode: DiscussionHILMode) => void
    onConvertToGroup?: () => void
    onApprovalAction?: (item: ApprovalItemViewModel, action: ApprovalActionViewModel, form?: InlineApprovalForm) => void
    onAttachmentUpload?: (file: File) => void | Promise<void>
    onLoadOlderMessages?: () => void
    onAbort?: () => void
    abortBusy?: boolean
    interjectBusy?: boolean
    pendingInterjection?: PendingInterjection
  }

  let {
    discussion,
    assistantMode = false,
    assistantEnabled = true,
    assistantAvatarUrl = '',
    assistantRuntimeLabel = '',
    assistantMissionCount = 0,
    assistantPendingApprovalCount = 0,
    headerLeadingActions,
    avatarIdentityById = {},
    draft,
    busy = false,
    errorMessage = '',
    debug = undefined,
    debugRuns = [],
    selectedDebugRunId = '',
    debugOpen = false,
    debugLoading = false,
    debugError = '',
    missionContext = undefined,
    missionContextLoading = false,
    missionContextError = '',
    missionOps = undefined,
    missionOpsLoading = false,
    missionOpsError = '',
    activeMissionId = $bindable(''),
    pendingApprovals = [],
    pendingAttachments = [],
    attachmentEnabled = undefined,
    attachmentUploading = false,
    attachmentError = '',
    olderMessagesLoading = false,
    olderMessagesHasMore = false,
    olderMessagesError = '',
    approvalBusyIds = [],
    taskTimelineCollapsed = true,
    workspaceSettingsAvailable = false,
    hilModeBusy = false,
    hilModeEditable = true,
    onDraft,
    onComposerFocus,
    onMention,
    onSend,
    onRetryMessage,
    onDebugOpen,
    onDebugRunSelect,
    onDebugClose,
    onTaskTimelineToggle,
    onMissionStart,
    onNewSession,
    onWorkspaceSettingsOpen,
    onHILModeChange,
    onConvertToGroup,
    onApprovalAction,
    onAttachmentUpload,
    onLoadOlderMessages,
    onAbort,
    abortBusy = false,
    interjectBusy = false,
    pendingInterjection = undefined
  }: Props = $props()
  let t = $derived(createTranslator($locale))
  let headerMenuOpen = $state(false)
  let composerMode = $state<'message' | 'mission'>('message')
  let missionLaunchMode = $state<'immediate' | 'scheduled'>('immediate')
  let missionScheduleAt = $state('')
  let composerDiscussionId = $state('')
  let voiceSupported = $state(false)
  let voiceRecording = $state(false)
  let voiceStarting = $state(false)
  let voiceProcessing = $state(false)
  let voiceElapsedSeconds = $state(0)
  let voiceError = $state('')
  let voiceRecorder: MediaRecorder | undefined
  let voiceStream: MediaStream | undefined
  let voiceChunks: Blob[] = []
  let voiceTimer: ReturnType<typeof setInterval> | undefined
  let discardVoiceRecording = false
  let voiceRequestVersion = 0
  let voiceDisposed = false
  const maxVoiceRecordingSeconds = 300
  const isRunning = $derived(discussion?.runtimeStatus === 'running' || discussion?.runtimeStatus === 'started')
  const isFailed = $derived(discussion?.runtimeStatus === 'failed')
  const isDirectDiscussion = $derived(discussion?.kind === 'direct')
  const hasDebugRuns = $derived((discussion?.runs.length ?? 0) > 0 || debugRuns.length > 0)
  const missionComposerActive = $derived(Boolean(onMissionStart) && composerMode === 'mission')
  const hasPendingDiscussionApproval = $derived(
    ['blocked', 'pending_approval', 'needs_approval', 'awaiting_approval'].includes((discussion?.runtimeStatus ?? '').trim().toLowerCase())
      && (
        (discussion?.timeline.some((item) => item.kind === 'approval' && ['', 'pending', 'open'].includes((item.approval?.status ?? '').trim().toLowerCase())) ?? false)
        || pendingApprovals.some((approval) => approval.discussionId === discussion?.id && ['', 'pending', 'open'].includes(approval.status.trim().toLowerCase()))
      )
  )
  const missionModeBlockedByAttachments = $derived(!missionComposerActive && pendingAttachments.length > 0)
  const attachmentUploadEnabled = $derived((attachmentEnabled ?? Boolean(discussion?.id)) && !missionComposerActive)
  const voiceInputEnabled = $derived(
    voiceSupported
      && attachmentUploadEnabled
      && Boolean(onAttachmentUpload)
      && !attachmentUploading
      && !isRunning
      && !busy
      && !hasPendingDiscussionApproval
      && !missionComposerActive
  )
  const voiceButtonDisabled = $derived(voiceStarting || voiceProcessing || (!voiceRecording && !voiceInputEnabled))
  const voiceButtonLabel = $derived(
    voiceRecording
      ? t('discussionSession.stopVoiceRecording')
      : voiceStarting || voiceProcessing
        ? t('discussionSession.processingVoiceRecording')
        : voiceSupported
          ? t('discussionSession.startVoiceRecording')
          : t('discussionSession.voiceRecordingUnsupported')
  )
  const composerSubmitDisabled = $derived(
    interjectBusy
      || voiceRecording
      || voiceStarting
      || voiceProcessing
      || (draft.trim().length === 0 && (missionComposerActive || pendingAttachments.length === 0))
      || (missionComposerActive && busy)
      || (missionComposerActive && pendingAttachments.length > 0)
      || (missionComposerActive && missionLaunchMode === 'scheduled' && !missionScheduleAt)
      || (!missionComposerActive && busy && !discussion?.id)
      || (!missionComposerActive && hasPendingDiscussionApproval)
  )
  const respondingLabel = $derived(formatRespondingLabel(discussion?.activeResponders ?? []))
  const runtimeLabel = $derived(isRunning || busy ? respondingLabel : discussion?.runtimeStatus || t('discussionSession.ready'))
  const primaryMission = $derived(
    discussion?.linkedMissions.find((mission) => mission.id === activeMissionId)
      ?? discussion?.linkedMissions[0]
  )
  const selectedMissionContext = $derived(
    missionContext && (!primaryMission || missionContext.mission.id === primaryMission.id)
      ? missionContext
      : undefined
  )
  const selectedMissionOps = $derived(
    missionOps && (!primaryMission || missionOps.missionId === primaryMission.id)
      ? missionOps
      : undefined
  )
  const timelineItems = $derived(discussion ? mergeApprovalTimeline(discussion.timeline.length > 0 ? discussion.timeline : fallbackTimeline(discussion), pendingApprovals) : [])
  const unifiedTimelineItems = $derived(unifyRunProgressTimelineItems(timelineItems))
  const absorbedMessageIds = $derived(messageIdsAbsorbedByWorkCards())
  const renderedTimelineItems = $derived(groupContinuousToolTimelineItems(filterEmptyRunTimelineItems(filterAbsorbedTimelineMessages(unifiedTimelineItems))))
  const visibleMissionContextError = $derived(isMissingResourceError(missionContextError) ? '' : missionContextError)
  const visibleMissionOpsError = $derived(isMissingResourceError(missionOpsError) ? '' : missionOpsError)
  const failedRuntimeSetupError = $derived(isDiscussionRuntimeSetupError(discussion?.runtimeError ?? ''))
  const failedMessage = $derived(displayDiscussionRuntimeError(discussion?.runtimeError ?? '') || t('discussionSession.sendFailed'))
  const visibleRuntimeSetupError = $derived(isDiscussionRuntimeSetupError(errorMessage))
  const displayErrorMessage = $derived(displayDiscussionRuntimeError(errorMessage))
  const visibleErrorMessage = $derived(displayErrorMessage && displayErrorMessage.trim() !== failedMessage.trim() && errorMessage.trim() !== (discussion?.runtimeError ?? '').trim() ? displayErrorMessage : '')
  const contextGoals = $derived(selectedMissionContext
    ? selectedMissionContext.mission.goals.length > 0
      ? selectedMissionContext.mission.goals
      : [selectedMissionContext.mission.description].filter(Boolean)
    : [primaryMission?.title ?? discussion?.title ?? ''].filter(Boolean))
  const teamMembers = $derived(discussion?.members ?? [])
  const teamMembersLabel = $derived(isDirectDiscussion ? t('discussionSession.privateChat') : t('discussion.members'))
  const panelSubtitle = $derived(
    assistantMode
      ? t('discussionSession.personalAssistantRole')
      : discussion?.kind === 'direct'
      ? directPanelRoleLabel(discussion)
      : t('discussion.memberCount', { count: teamMembers.length })
  )
  const taskTimeline = $derived(selectedMissionContext?.tasks ?? [])
  const missionRuntimeSessions = $derived(selectedMissionOps?.runtimeSessions ?? [])
  const currentTaskId = $derived(currentTask(taskTimeline)?.id ?? '')
  const mentionQuery = $derived(activeMentionQuery(draft))
  const mentionCandidates = $derived(filterMentionCandidates(discussion?.members ?? [], mentionQuery))
  const selectedDebugRuntime = $derived(debug
    ? [debug.selected.agentId, debug.selected.runtimeKind, debug.selected.model].filter(Boolean).join(' / ')
    : '')
  const selectedDebugRun = $derived(debugRuns.find((run) => run.id === (debug?.runId || selectedDebugRunId)) ?? debugRuns.at(-1))
  const panelTitle = $derived(assistantMode ? t('discussionSession.personalAssistant') : discussion?.title || t('nav.discussions'))
  const panelInitial = $derived(panelTitle.trim().slice(0, 1).toUpperCase() || 'I')
  const panelAvatarClass = $derived(
    !isDirectDiscussion
      ? 'bg-indigo-500'
      : discussion?.directTargetKind === 'user'
        ? 'bg-brand'
        : 'bg-amber-500'
  )
  const panelAvatarIdentity = $derived(directPanelAvatarIdentity())
  const renderedProcessRuns = $derived(renderedTimelineItems.map((item) => item.run).filter((run): run is DiscussionRunViewModel => {
    if (!run) return false
    return runHasStructuredProgress(run)
  }))
  const hiddenToolProcessItems = $derived(groupContinuousToolTimelineItems(unifiedTimelineItems).filter((item) => toolTimelineItemCanGroup(item)))
  const selectedProcessRun = $derived(processRunForDebugSelection())
  const selectedDebugError = $derived(selectedDebugRun?.error || (selectedDebugRun?.status === 'failed' ? discussion?.runtimeError : '') || '')
  const debugProviderMetadataEntries = $derived(Object.entries(debug?.providerMetadata ?? {}).sort(([left], [right]) => left.localeCompare(right)))
  const debugResponseBlockClass = 'subtle-scrollbar max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/30 p-3 font-mono text-xs text-foreground'
  const debugErrorBlockClass = 'subtle-scrollbar max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md border border-destructive/30 bg-destructive/10 p-3 font-mono text-xs text-destructive'
  const debugPromptBlockClass = 'subtle-scrollbar max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/30 p-3 font-mono text-xs text-foreground'
  const latestMessageScrollVersion = $derived(JSON.stringify(discussion?.messages.at(-1) ?? null))
  const latestTimelineScrollVersion = $derived(JSON.stringify(renderedTimelineItems.at(-1) ?? null))
  let timelineScroller = $state<HTMLDivElement | undefined>()
  let composerInput = $state<HTMLTextAreaElement | undefined>()
  let attachmentInput = $state<HTMLInputElement | undefined>()
  let activeApproval = $state<ApprovalItemViewModel | undefined>()
  let activeApprovalAction = $state<ApprovalActionViewModel | undefined>()
  let otherApprovalMode = $state(false)
  let otherApprovalInput = $state('')
  let expandedWorkCardIds = $state<string[]>([])
  let collapsedActiveWorkCardIds = $state<string[]>([])
  let copiedMessageId = $state('')

  onMount(() => {
    voiceSupported = typeof MediaRecorder !== 'undefined' && Boolean(navigator.mediaDevices?.getUserMedia)
  })

  onDestroy(() => {
    voiceDisposed = true
    voiceRequestVersion += 1
    discardVoiceRecording = true
    if (voiceRecorder && voiceRecorder.state !== 'inactive') {
      voiceRecorder.onstop = null
      voiceRecorder.stop()
    }
    clearVoiceRecordingResources()
  })

  $effect(() => {
    const nextDiscussionId = discussion?.id ?? ''
    if (nextDiscussionId === composerDiscussionId) return
    cancelVoiceRecording()
    composerDiscussionId = nextDiscussionId
    composerMode = 'message'
    missionLaunchMode = 'immediate'
    missionScheduleAt = ''
  })

  $effect(() => {
    latestMessageScrollVersion
    latestTimelineScrollVersion
    if (timelineScroller) {
      timelineScroller.scrollTop = timelineScroller.scrollHeight
    }
  })

  const hiddenApprovalFieldKeys = new Set([
    'approval_conversation_id',
    'approval_native_tool_name',
    'approval_session_id',
    'approval_request_id',
    'approval_run_id',
    'approval_tool_name',
    'approval_turn_id',
    'session_id',
    'request_id',
    'run_id',
    'turn_id'
  ])

  function fallbackTimeline(source: DiscussionViewModel): DiscussionTimelineItemViewModel[] {
    return [
      ...source.messages.map((message) => ({ id: message.id, kind: 'message' as const, sortAt: message.createdAt, message })),
      ...source.runs.filter(runHasStructuredProgress).map((run) => ({ id: run.id, kind: 'run' as const, sortAt: run.createdAt, run }))
    ]
  }

  function mergeApprovalTimeline(timeline: DiscussionTimelineItemViewModel[], approvals: ApprovalItemViewModel[]) {
    const timelineApprovalKeys = new Set(timeline
      .filter((item) => item.kind === 'approval' && item.approval)
      .flatMap((item) => approvalIdentityKeys(item.approval as ApprovalItemViewModel)))
    const approvalItems: DiscussionTimelineItemViewModel[] = approvals
      .filter((approval) => !approvalSharesIdentity(approval, timelineApprovalKeys))
      .map((approval) => ({
      id: `approval:${approval.id}`,
      kind: 'approval' as const,
      sortAt: approval.createdAtRaw || approval.createdAt,
      approval
    }))
    let merged = dedupeApprovalTimeline(timeline)
    for (const approval of approvalItems) {
      const approvalTime = Date.parse(approval.sortAt)
      const insertAt = merged.findIndex((item) => {
        const itemTime = Date.parse(item.sortAt)
        return !Number.isNaN(approvalTime) && !Number.isNaN(itemTime) && itemTime > approvalTime
      })
      if (insertAt < 0) {
        merged = [...merged, approval]
      } else {
        merged = [...merged.slice(0, insertAt), approval, ...merged.slice(insertAt)]
      }
    }
    return merged
  }

  function groupContinuousToolTimelineItems(timeline: DiscussionTimelineItemViewModel[]) {
    const rendered: DiscussionTimelineItemViewModel[] = []
    let group: DiscussionTimelineItemViewModel[] = []

    const flush = () => {
      if (group.length === 0) return
      if (group.length <= 2 && group.every((item) => item.kind === 'approval')) rendered.push(...group)
      else rendered.push(groupedToolTimelineItem(group))
      group = []
    }

    for (const item of timeline) {
      if (timelineItemCanJoinToolGroup(item)) {
        group.push(item)
        continue
      }
      flush()
      rendered.push(item)
    }
    flush()
    return rendered
  }

  function unifyRunProgressTimelineItems(timeline: DiscussionTimelineItemViewModel[]) {
    const groups = new Map<string, Array<{ item: DiscussionTimelineItemViewModel; index: number }>>()
    for (let index = 0; index < timeline.length; index++) {
      const item = timeline[index]
      if (item.kind !== 'run' || !item.run || !runHasStructuredProgress(item.run)) continue
      const entries = groups.get(item.run.id) ?? []
      entries.push({ item, index })
      groups.set(item.run.id, entries)
    }
    if (groups.size === 0) return timeline

    const approvalsByRun = new Map<string, ApprovalItemViewModel[]>()
    const absorbedApprovalIDs = new Set<string>()
    for (const item of timeline) {
      if (item.kind !== 'approval' || !item.approval || approvalIsPending(item.approval)) continue
      const runID = (item.approval.runtimeRunId || item.approval.taskId).trim()
      if (!runID || !groups.has(runID)) continue
      const approvals = approvalsByRun.get(runID) ?? []
      approvals.push(item.approval)
      approvalsByRun.set(runID, approvals)
      absorbedApprovalIDs.add(item.id)
    }

    const replacements = new Map<number, DiscussionTimelineItemViewModel>()
    const omittedRunIndexes = new Set<number>()
    for (const [runID, entries] of groups) {
      const target = entries.filter(({ item }) => item.displaySegment).at(-1) ?? entries[0]
      let merged = entries[0].item.run as DiscussionRunViewModel
      for (const { item } of entries.slice(1)) {
        merged = mergeProgressRuns(merged, item.run as DiscussionRunViewModel)
      }
      const durable = discussion?.runs.find((run) => run.id === runID)
      if (durable) merged = mergeProgressRuns(merged, durable)

      const tools: TimelineDisplayToolCall[] = [...merged.toolCalls]
      for (const approval of approvalsByRun.get(runID) ?? []) {
        if (!approvalRepresentsToolCall(approval, tools)) {
          insertTimelineToolCallByOccurrence(tools, approvalDisplayToolCall(approval))
        }
      }
      merged = { ...merged, toolCalls: mergeTimelineToolCalls([], tools) }
      replacements.set(target.index, { ...target.item, id: runID, kind: 'run', run: merged })
      for (const { index } of entries) {
        if (index !== target.index) omittedRunIndexes.add(index)
      }
    }

    return timeline.flatMap((item, index) => {
      if (absorbedApprovalIDs.has(item.id) || omittedRunIndexes.has(index)) return []
      return [replacements.get(index) ?? item]
    })
  }

  function mergeProgressRuns(current: DiscussionRunViewModel, next: DiscussionRunViewModel) {
    return {
      ...current,
      ...next,
      mode: next.mode || current.mode,
      hasPlan: next.hasPlan || current.hasPlan,
      agentId: next.agentId || current.agentId,
      agentName: next.agentName || current.agentName,
      runtimeKind: next.runtimeKind || current.runtimeKind,
      error: next.error || current.error,
      toolCalls: mergeTimelineToolCalls(current.toolCalls, next.toolCalls),
      planId: next.planId || current.planId,
      planStatus: next.planStatus || current.planStatus,
      planSteps: next.planSteps?.length ? next.planSteps : current.planSteps,
      thinking: next.thinking?.trim() ? next.thinking : current.thinking,
      preview: next.preview?.trim() ? next.preview : current.preview,
      usage: next.usage ?? current.usage,
      durationMs: next.durationMs ?? current.durationMs,
      sortAt: current.sortAt || next.sortAt,
      createdAt: current.createdAt || next.createdAt
    }
  }

  function mergeTimelineToolCalls(current: TimelineDisplayToolCall[], incoming: TimelineDisplayToolCall[]) {
    const merged = new Map(current.map((tool) => [tool.id, tool]))
    for (const tool of incoming) {
      const previous = merged.get(tool.id)
      merged.set(tool.id, previous ? {
        ...previous,
        ...tool,
        arguments: tool.arguments ?? previous.arguments,
        result: tool.result ?? previous.result,
        sortAt: previous.sortAt || tool.sortAt,
        createdAt: previous.createdAt || tool.createdAt
      } : tool)
    }
    return [...merged.values()]
  }

  function insertTimelineToolCallByOccurrence(tools: TimelineDisplayToolCall[], tool: TimelineDisplayToolCall) {
    const time = parseDebugSortTime(tool.sortAt || tool.createdAt)
    if (Number.isNaN(time)) {
      tools.push(tool)
      return
    }
    const index = tools.findIndex((candidate) => {
      const candidateTime = parseDebugSortTime(candidate.sortAt || candidate.createdAt)
      return !Number.isNaN(candidateTime) && candidateTime > time
    })
    if (index < 0) tools.push(tool)
    else tools.splice(index, 0, tool)
  }

  function approvalRepresentsToolCall(approval: ApprovalItemViewModel, tools: TimelineDisplayToolCall[]) {
    const ids = [approval.runtimeToolCallId, approval.runtimeRequestId, approvalRequestID(approval)]
      .map((id) => id?.trim() || '')
      .filter(Boolean)
    if (ids.some((id) => tools.some((tool) => tool.id === id))) return true
    const name = approval.toolName.trim().toLocaleLowerCase()
    if (!name) return false
    return tools.filter((tool) => tool.name.trim().toLocaleLowerCase() === name).length === 1
  }

  function toolTimelineItemCanGroup(item: DiscussionTimelineItemViewModel) {
    return item.kind === 'run' && Boolean(item.run) && !runUsesProcessTranscript(item.run as DiscussionRunViewModel) && (item.run?.toolCalls.length ?? 0) > 0
  }

  function timelineItemCanJoinToolGroup(item: DiscussionTimelineItemViewModel) {
    return toolTimelineItemCanGroup(item) || (item.kind === 'approval' && Boolean(item.approval) && !approvalIsPending(item.approval as ApprovalItemViewModel))
  }

  type TimelineDisplayToolCall = DiscussionToolCallViewModel & {
    displayText?: string
    statusText?: string
    running?: boolean
    failed?: boolean
    showDetails?: boolean
  }

  const stableToolCallLists = new Map<string, TimelineDisplayToolCall[]>()

  function stableTimelineToolCalls(key: string, tools: TimelineDisplayToolCall[]) {
    const current = stableToolCallLists.get(key)
    if (current && toolCallListsEqual(current, tools)) return current
    stableToolCallLists.set(key, tools)
    return tools
  }

  function toolCallListsEqual(left: TimelineDisplayToolCall[], right: TimelineDisplayToolCall[]) {
    if (left === right) return true
    if (left.length !== right.length) return false
    return left.every((tool, index) => toolCallDisplayEqual(tool, right[index]))
  }

  function toolCallDisplayEqual(left: TimelineDisplayToolCall, right: TimelineDisplayToolCall | undefined) {
    if (!right) return false
    return left.id === right.id
      && left.name === right.name
      && left.status === right.status
      && left.createdAt === right.createdAt
      && left.sortAt === right.sortAt
      && left.displayText === right.displayText
      && left.statusText === right.statusText
      && left.running === right.running
      && left.failed === right.failed
      && left.showDetails === right.showDetails
      && toolCallRecordEqual(left.arguments, right.arguments)
      && toolCallRecordEqual(left.result, right.result)
  }

  function toolCallRecordEqual(left: Record<string, string> | undefined, right: Record<string, string> | undefined) {
    if (left === right) return true
    if (!left || !right) return false
    const leftKeys = Object.keys(left)
    const rightKeys = Object.keys(right)
    return leftKeys.length === rightKeys.length && leftKeys.every((key) => left[key] === right[key])
  }

  function timelineItemToolCalls(item: DiscussionTimelineItemViewModel): TimelineDisplayToolCall[] {
    if (item.kind === 'run' && item.run) return item.run.toolCalls
    if (item.kind === 'approval' && item.approval) return [approvalDisplayToolCall(item.approval)]
    return []
  }

  function approvalDisplayToolCall(approval: ApprovalItemViewModel): TimelineDisplayToolCall {
    const argumentsRecord = Object.fromEntries(approval.payloadPreview.fields.map((field) => [field.key, field.value]))
    const resultRecord = Object.fromEntries([
      ['status', approval.status],
      ['summary', approval.summary],
      ['details', approval.details]
    ].filter(([, value]) => value.trim()))
    return {
      id: `approval-tool:${approval.id}`,
      name: approval.toolName.trim() || approvalToolLabel(approval),
      displayText: approvalCompactText(approval),
      status: approval.status,
      statusText: approvalStatusText(approval.status),
      running: approvalExecutionRunning(approval),
      failed: approvalCompactFailed(approval),
      showDetails: Object.keys(argumentsRecord).length > 0 || Object.keys(resultRecord).length > 0,
      arguments: Object.keys(argumentsRecord).length > 0 ? argumentsRecord : undefined,
      result: Object.keys(resultRecord).length > 0 ? resultRecord : undefined,
      createdAt: approval.createdAt,
      sortAt: approval.createdAtRaw || approval.createdAt
    }
  }

  function groupedToolTimelineItem(group: DiscussionTimelineItemViewModel[]) {
    if (group.length === 1) return group[0]
    const first = group[0]
    const runs = group.map((item) => item.run).filter((run): run is DiscussionRunViewModel => Boolean(run))
    const firstRun = runs[0]
    const firstApproval = group.find((item) => item.approval)?.approval
    const toolCalls = group.flatMap(timelineItemToolCalls)
    if (!firstRun && !firstApproval) return first
    const approvalAgentId = firstApproval?.triggeredBy || firstApproval?.requestedBy || ''
    const approvalAgentName = firstApproval?.triggeredByName || firstApproval?.triggeredBy || firstApproval?.requestedByActorLabel || ''
    const baseRun: DiscussionRunViewModel = firstRun ?? {
      id: `approval-run:${firstApproval!.id}`,
      agentId: approvalActorValue(approvalAgentId),
      agentName: approvalActorValue(approvalAgentName),
      status: firstApproval!.status,
      runtimeKind: firstApproval!.provider,
      toolCalls: [],
      createdAt: firstApproval!.createdAt,
      sortAt: firstApproval!.createdAtRaw || firstApproval!.createdAt
    }
    return {
      ...first,
      id: `${first.id}:tool-group`,
      kind: 'run' as const,
      displaySegment: true,
      approval: undefined,
      run: {
        ...baseRun,
        status: groupedToolRunStatus(runs, toolCalls),
        toolCalls
      }
    }
  }

  function approvalActorValue(value: string) {
    const normalized = value.trim()
    return normalized && normalized !== 'Not provided' ? normalized : ''
  }

  function attachedToolTimelineItemForMessage(index: number) {
    const message = renderedTimelineItems[index]?.message
    const toolItem = renderedTimelineItems[index - 1]
    if (!message || message.role !== 'assistant' || !toolItem || !toolTimelineItemCanGroup(toolItem) || !toolItem.run) return undefined
    return toolRunMatchesMessageAuthor(toolItem.run, message) ? toolItem : undefined
  }

  function toolTimelineItemAttachesToNextMessage(index: number) {
    const item = renderedTimelineItems[index]
    return Boolean(item && attachedToolTimelineItemForMessage(index + 1)?.id === item.id)
  }

  function toolRunMatchesMessageAuthor(run: DiscussionRunViewModel, message: DiscussionMessageViewModel) {
    const runAgentID = run.agentId.trim()
    const messageAuthorID = message.authorId.trim()
    if (runAgentID && messageAuthorID) return runAgentID === messageAuthorID

    const runAgentName = run.agentName.trim().toLocaleLowerCase()
    const messageAuthorName = message.authorName.trim().toLocaleLowerCase()
    if (runAgentName && messageAuthorName) return runAgentName === messageAuthorName

    return true
  }

  function groupedToolRunStatus(runs: DiscussionRunViewModel[], tools: TimelineDisplayToolCall[]) {
    const statuses = [...runs.map((run) => run.status), ...tools.map((tool) => tool.status)].map((status) => status.trim().toLowerCase())
    if (tools.some((tool) => tool.running)) return 'running'
    if (statuses.some((status) => ['running', 'started', 'pending', 'todo'].includes(status))) return 'running'
    if (tools.some((tool) => tool.failed)) return 'failed'
    if (statuses.some((status) => ['failed', 'error', 'timeout', 'cancelled', 'canceled'].includes(status))) return 'failed'
    if (statuses.length > 0 && statuses.every((status) => ['completed', 'succeeded', 'done', 'approved', 'answered'].includes(status))) return 'completed'
    return runs[0]?.status ?? tools[0]?.status ?? ''
  }

  function dedupeApprovalTimeline(timeline: DiscussionTimelineItemViewModel[]) {
    const merged: DiscussionTimelineItemViewModel[] = []
    for (const item of timeline) {
      if (item.kind !== 'approval' || !item.approval) {
        merged.push(item)
        continue
      }
      const keys = new Set(approvalIdentityKeys(item.approval))
      const existingIndex = merged.findIndex((existing) => existing.kind === 'approval' && existing.approval && approvalSharesIdentity(existing.approval, keys))
      if (existingIndex < 0) {
        merged.push(item)
        continue
      }
      const existing = merged[existingIndex]
      if (!existing.approval || approvalShouldReplace(item.approval, existing.approval)) {
        merged[existingIndex] = item
      }
    }
    return merged
  }

  function approvalShouldReplace(next: ApprovalItemViewModel, current: ApprovalItemViewModel) {
    const nextPending = approvalIsPending(next)
    const currentPending = approvalIsPending(current)
    if (!nextPending && currentPending) return true
    if (nextPending && !currentPending) return false
    const nextTime = Date.parse(next.createdAtRaw || next.createdAt)
    const currentTime = Date.parse(current.createdAtRaw || current.createdAt)
    if (Number.isNaN(nextTime) || Number.isNaN(currentTime)) return true
    return nextTime >= currentTime
  }

  function approvalSharesIdentity(approval: ApprovalItemViewModel, keys: Set<string>) {
    return approvalIdentityKeys(approval).some((key) => keys.has(key))
  }

  function approvalIdentityKeys(approval: ApprovalItemViewModel) {
    const keys = new Set<string>()
    const add = (value: string) => {
      const key = value.trim()
      if (key) keys.add(key)
    }
    const conversationId = firstNonEmptyApprovalValue(approvalFieldValue(approval, 'approval_conversation_id'), discussionSourceConversationId(approval))
    const requestId = firstNonEmptyApprovalValue(approval.runtimeRequestId || '', approvalFieldValue(approval, 'approval_request_id'))
    const runId = firstNonEmptyApprovalValue(approval.runtimeRunId || '', approvalFieldValue(approval, 'approval_run_id'), approval.taskId)
    const toolCallId = approval.runtimeToolCallId?.trim() || ''
    add(`id:${approval.id}`)
    if (conversationId && approval.id) add(`discussion:${conversationId}:approval:${approval.id}`)
    if (conversationId && requestId) add(`discussion:${conversationId}:request:${requestId}`)
    if (conversationId && runId && requestId) add(`discussion:${conversationId}:run:${runId}:request:${requestId}`)
    if (conversationId && runId && toolCallId) add(`discussion:${conversationId}:run:${runId}:tool:${toolCallId}`)
    for (const action of approval.actions) {
      const pathParts = discussionApprovalActionPathParts(action.path)
      if (!pathParts) continue
      add(`discussion:${pathParts.conversationId}:approval:${pathParts.approvalId}`)
      add(`discussion:${pathParts.conversationId}:request:${pathParts.approvalId}`)
      if (runId) add(`discussion:${pathParts.conversationId}:run:${runId}:request:${pathParts.approvalId}`)
      if (requestId) add(`discussion:${pathParts.conversationId}:request:${requestId}`)
    }
    return [...keys]
  }

  function approvalFieldValue(approval: ApprovalItemViewModel, key: string) {
    const target = normalizeApprovalFieldKey(key)
    return approval.payloadPreview.fields.find((field) => normalizeApprovalFieldKey(field.key) === target)?.value.trim() ?? ''
  }

  function discussionSourceConversationId(approval: ApprovalItemViewModel) {
    const [sourceType, ...sourceParts] = approval.sourceLabel.split(':')
    if (sourceType.trim().toLowerCase() !== 'discussion') return ''
    return sourceParts.join(':').trim()
  }

  function discussionApprovalActionPathParts(path: string) {
    const clean = path.trim().split('?')[0]
    const match = /^\/api\/discussions\/([^/]+)\/approvals\/([^/]+)\/decision$/.exec(clean)
    if (!match) return undefined
    return { conversationId: decodeURIComponent(match[1]), approvalId: decodeURIComponent(match[2]) }
  }

  function firstNonEmptyApprovalValue(...values: string[]) {
    return values.map((value) => value.trim()).find(Boolean) ?? ''
  }

  function isMissingResourceError(message: string) {
    const normalized = message.trim().toLowerCase()
    return normalized.includes('resource not found') || normalized.includes('not found') || normalized.includes('404')
  }

  function displayDiscussionRuntimeError(message: string) {
    if (isDiscussionRuntimeSetupError(message)) {
      return t('discussionSession.runtimeSetupRequired')
    }
    return message.trim()
  }

  function isDiscussionRuntimeSetupError(message: string) {
    const normalized = message.trim().toLowerCase()
    if (!normalized) return false
    if (isDiscussionRuntimeExecutionDiagnostic(normalized)) return false
    if (normalized === 'runtime_unavailable' || normalized === 'discussion runtime unavailable') return true
    return [
      'discussion runtime is not configured',
      'configure a discussion runtime',
      'runtime adapter',
      'runtime spec',
      'bound runtime instance',
      'runtime profile',
      'discussion agent profile',
      'requires llm profile settings',
      'llm profile',
      'api key',
      'secret resolver',
      'llm base url',
      'model factory',
      'executable file not found',
      'executable not found',
      'not recognized as an internal or external command'
    ].some((needle) => normalized.includes(needle))
  }

  function isDiscussionRuntimeExecutionDiagnostic(normalized: string) {
    return [
      'discussion runtime execution failed',
      '[noderunerror]',
      'tool run_terminal not found',
      'toolsnode',
      'runtime finished with status failed'
    ].some((needle) => normalized.includes(needle))
  }

  function currentTask(tasks: MissionTaskViewModel[]) {
    return tasks.find((task) => !isDoneTaskStatus(task.status) && (isActiveTaskStatus(task.status) || task.status === 'blocked'))
      ?? tasks.find((task) => !isDoneTaskStatus(task.status))
  }

  function taskStepState(task: MissionTaskViewModel) {
    if (isDoneTaskStatus(task.status)) return 'done'
    if (task.id === currentTaskId) return 'current'
    return 'pending'
  }

  function runStepState(run: DiscussionRunViewModel) {
    const status = run.status.trim().toLowerCase()
    if (status === 'failed' || status === 'blocked' || status === 'timeout' || status === 'cancelled') return 'failed'
    if (status === 'running' || status === 'started') return 'current'
    if (status === 'completed') return 'done'
    return 'pending'
  }

  function filterAbsorbedTimelineMessages(timeline: DiscussionTimelineItemViewModel[]) {
    if (absorbedMessageIds.size === 0) return timeline
    return timeline.filter((item) => item.kind !== 'message' || !item.message || !absorbedMessageIds.has(item.message.id))
  }

  function filterEmptyRunTimelineItems(timeline: DiscussionTimelineItemViewModel[]) {
    return timeline.filter((item) => item.kind !== 'run' || !item.run || toolTimelineItemCanGroup(item) || runHasStructuredProgress(item.run))
  }

  function messageIdsAbsorbedByWorkCards() {
    const ids = new Set<string>()
    if (!discussion) return ids
    if (discussion.kind === 'direct' || discussion.kind === 'personal_assistant') return ids
    const plannedTurnIds = new Set(
      discussionRunsForWorkCards()
        .filter((run) => run.hasPlan || run.planSteps?.length)
        .map((run) => run.turnId?.trim())
        .filter((turnId): turnId is string => Boolean(turnId))
    )
    for (const turnId of plannedTurnIds) {
      const messages = discussionMessagesForWorkCards()
        .filter((message) => message.turnId === turnId && message.role === 'assistant')
        .sort((left, right) => parseDebugSortTime(left.sortAt || left.createdAt) - parseDebugSortTime(right.sortAt || right.createdAt))
      for (const message of messages.slice(0, -1)) {
        if (messageCanBeAbsorbedByWorkCard(message)) ids.add(message.id)
      }
    }
    return ids
  }

  function messageCanBeAbsorbedByWorkCard(message: DiscussionMessageViewModel) {
    return message.role === 'assistant' &&
      !message.canApproveRetry &&
      !message.canRequestRetry &&
      !message.retryPending &&
      (message.attachments?.length ?? 0) === 0 &&
      (message.acknowledgements?.length ?? 0) === 0 &&
      message.content.trim().length > 0
  }

  function isActiveRunStatus(status: string) {
    const normalized = status.trim().toLowerCase()
    return normalized === 'running' || normalized === 'started' || normalized === 'pending' || normalized === 'todo' || isApprovalWaitRunStatus(normalized)
  }

  function isApprovalWaitRunStatus(status: string) {
    const normalized = status.trim().toLowerCase()
    return normalized === 'pending_approval' || normalized === 'needs_approval' || normalized === 'awaiting_approval'
  }

  function isFailedRunStatus(status: string) {
    const normalized = status.trim().toLowerCase()
    return normalized === 'failed' || normalized === 'error' || normalized === 'timeout' || normalized === 'cancelled' || normalized === 'canceled'
  }

  function isDoneRunStatus(status: string) {
    const normalized = status.trim().toLowerCase()
    return normalized === 'completed' || normalized === 'succeeded' || normalized === 'done' || normalized === 'approved'
  }

  function runStatusLabel(run: DiscussionRunViewModel) {
    if (isApprovalWaitRunStatus(run.status)) return t('discussionSession.agentWorkAwaitingApproval')
    if (isActiveRunStatus(run.status)) return t('discussionSession.agentWorkInProgress')
    if (isFailedRunStatus(run.status)) return t('discussionSession.agentWorkFailed')
    if (isDoneRunStatus(run.status)) return t('discussionSession.agentWorkComplete')
    return run.status || t('discussionSession.ready')
  }

  function runProcessAutoExpands(run: DiscussionRunViewModel) {
    const runtimeKind = run.runtimeKind.trim().toLowerCase()
    const oneToOne = run.mode === 'direct' || run.mode === 'personal_assistant' || discussion?.kind === 'direct' || discussion?.kind === 'personal_assistant'
    const hasCodexReasoningTrace = runtimeKind === 'codex' && Boolean(run.thinking?.trim() || run.hasPlan || run.planSteps?.length)
    return oneToOne || hasCodexReasoningTrace
  }

  function runProcessHeadline(run: DiscussionRunViewModel) {
    if (isDoneRunStatus(run.status) && run.durationMs) {
      return t('discussionSession.agentWorkWorkedFor', { duration: formatRunDuration(run.durationMs) })
    }
    return runStatusLabel(run)
  }

  function compactRunProcessSummary(run: DiscussionRunViewModel) {
    if (run.thinking?.trim()) return plainTextSummary(run.thinking)
    const activeStep = run.planSteps?.find((step) => planStepRunning(step.status))
    if (activeStep?.text) return activeStep.text
    const lastStep = run.planSteps?.at(-1)?.text
    if (lastStep) return lastStep
    if (run.toolCalls.length > 0) {
      const latestTool = run.toolCalls.at(-1)
      return latestTool ? `${latestTool.name} · ${latestTool.status}` : runStatusLabel(run)
    }
    return runStatusLabel(run)
  }

  function formatRunDuration(durationMs: number) {
    if (durationMs < 1000) return `${Math.max(1, Math.round(durationMs))}ms`
    if (durationMs < 60_000) return `${Math.round(durationMs / 100) / 10}s`
    const minutes = Math.floor(durationMs / 60_000)
    const seconds = Math.round((durationMs % 60_000) / 1000)
    return `${minutes}m ${seconds}s`
  }

  function runTokenUsage(run: DiscussionRunViewModel) {
    return discussionRunTokenUsage(run.usage)
  }

  function tokenDetailLabel(count: number, known: boolean) {
    if (known) return formatTokenCount(count)
    if (count > 0) return `${formatTokenCount(count)} (${t('discussionSession.agentWorkPartialUsage')})`
    return t('discussionSession.agentWorkUsageNotReported')
  }

  function formatTokenCount(count: number) {
    return new Intl.NumberFormat($locale, { notation: count >= 10_000 ? 'compact' : 'standard', maximumFractionDigits: 1 }).format(count)
  }

  async function copyMessage(message: DiscussionMessageViewModel) {
    if (typeof navigator === 'undefined' || !navigator.clipboard) return
    await navigator.clipboard.writeText(renderPlainDiscussionMentions(message.content, discussionMentionDisplayLabel))
    copiedMessageId = message.id
    window.setTimeout(() => {
      if (copiedMessageId === message.id) copiedMessageId = ''
    }, 1600)
  }

  function processRunForDebugSelection() {
    const selectedId = debug?.runId || selectedDebugRunId
    return renderedProcessRunByItemId(selectedId)
      ?? hiddenToolProcessRunByItemId(selectedId)
      ?? renderedProcessRuns.find((run) => run.id === selectedId)
      ?? discussionRunsForWorkCards().find((run) => run.id === selectedId)
      ?? discussion?.runs.find((run) => run.id === selectedId && runHasDebuggableProgress(run))
      ?? debugRuns.find((run) => run.id === selectedId)
      ?? debugRuns.at(-1)
      ?? discussion?.runs.at(-1)
  }

  function renderedProcessRunByItemId(selectedId: string) {
    if (!selectedId) return undefined
    return renderedTimelineItems.find((item) => item.id === selectedId && item.run)?.run
  }

  function hiddenToolProcessRunByItemId(selectedId: string) {
    if (!selectedId) return undefined
    return hiddenToolProcessItems.find((item) => item.id === selectedId && item.run)?.run
  }

  function runProgress(run: DiscussionRunViewModel) {
    if (run.planSteps?.length) {
      const total = run.planSteps.length
      const failed = run.planSteps.filter((step) => planStepFailed(step.status)).length
      const running = run.planSteps.filter((step) => planStepRunning(step.status)).length
      const completed = run.planSteps.filter((step) => planStepDone(step.status)).length
      const percent = total > 0 ? Math.round((completed / total) * 100) : 0
      return {
        total,
        completed,
        failed,
        running,
        percent: Math.max(0, Math.min(100, percent))
      }
    }
    const total = run.toolCalls.length
    const failed = run.toolCalls.filter((tool) => toolCallFailed(tool)).length
    const running = run.toolCalls.filter((tool) => toolCallRunning(tool)).length
    const completed = total - failed - running
    const percent = total > 0 ? Math.round((Math.max(0, completed) / total) * 100) : (isDoneRunStatus(run.status) ? 100 : isActiveRunStatus(run.status) ? 50 : 0)
    return {
      total,
      completed: Math.max(0, completed),
      failed,
      running,
      percent: Math.max(0, Math.min(100, percent))
    }
  }

  function toolCallRunning(tool: DiscussionToolCallViewModel) {
    const status = tool.status.trim().toLowerCase()
    return status === 'running' || status === 'started' || status === 'pending' || status === 'todo'
  }

  function toolCallFailed(tool: DiscussionToolCallViewModel) {
    const status = tool.status.trim().toLowerCase()
    return status === 'failed' || status === 'error' || status === 'timeout' || status === 'cancelled' || status === 'canceled'
  }

  function planStepDone(status: string) {
    const normalized = status.trim().toLowerCase()
    return normalized === 'completed' || normalized === 'succeeded' || normalized === 'done'
  }

  function planStepRunning(status: string) {
    const normalized = status.trim().toLowerCase()
    return normalized === 'running' || normalized === 'started' || normalized === 'pending' || normalized === 'todo' || normalized === 'in_progress'
  }

  function planStepFailed(status: string) {
    const normalized = status.trim().toLowerCase()
    return normalized === 'failed' || normalized === 'error' || normalized === 'timeout' || normalized === 'cancelled' || normalized === 'canceled'
  }

  function planStepStatusLabel(status: string) {
    if (planStepDone(status)) return t('status.completed')
    if (planStepFailed(status)) return t('status.failed')
    if (planStepRunning(status)) return t('workspace.inProgress')
    return status || t('status.pending')
  }

  function workCardExpanded(run: DiscussionRunViewModel) {
    if (expandedWorkCardIds.includes(run.id)) return true
    return runProcessAutoExpands(run) && isActiveRunStatus(run.status) && !collapsedActiveWorkCardIds.includes(run.id)
  }

  function toggleWorkCard(run: DiscussionRunViewModel) {
    if (workCardExpanded(run)) {
      expandedWorkCardIds = expandedWorkCardIds.filter((id) => id !== run.id)
      if (isActiveRunStatus(run.status) && !collapsedActiveWorkCardIds.includes(run.id)) {
        collapsedActiveWorkCardIds = [...collapsedActiveWorkCardIds, run.id]
      }
      return
    }
    collapsedActiveWorkCardIds = collapsedActiveWorkCardIds.filter((id) => id !== run.id)
    if (!expandedWorkCardIds.includes(run.id)) expandedWorkCardIds = [...expandedWorkCardIds, run.id]
  }

  function openWorkProcess(run: DiscussionRunViewModel) {
    openDebugPanel(run.id)
  }

  function runSummary(run: DiscussionRunViewModel) {
    if (run.error) return run.error
    if (run.preview?.trim()) return plainTextSummary(run.preview)
    if (run.planSteps?.length) {
      const active = run.planSteps.find((step) => planStepRunning(step.status))
      return active?.text || run.planSteps.at(-1)?.text || t('discussionSession.agentWorkInProgress')
    }
    const message = assistantMessageAfterRun(run)
    if (message?.content) return plainTextSummary(message.content)
    const toolResult = [...run.toolCalls].reverse().map((tool) => recordPreview(tool.result)).find(Boolean)
    if (toolResult) return toolResult
    if (isFailedRunStatus(run.status)) return t('discussionSession.agentWorkFailed')
    if (isActiveRunStatus(run.status)) return t('discussionSession.agentWorkInProgress')
    return t('discussionSession.agentWorkComplete')
  }

  function assistantMessageAfterRun(run: DiscussionRunViewModel) {
    const runTime = parseDebugSortTime(run.sortAt || run.createdAt)
    const messages = discussionMessagesForWorkCards()
    const assistantMessages = messages.filter((message) => message.role === 'assistant' && (!run.agentId || message.authorId === run.agentId))
    if (Number.isNaN(runTime)) return assistantMessages.at(-1)
    const nextRunTime = nextRunSortTime(run)
    return assistantMessages.find((message) => {
      const messageTime = parseDebugSortTime(message.sortAt || message.createdAt)
      return (Number.isNaN(messageTime) || messageTime >= runTime) &&
        (Number.isNaN(nextRunTime) || Number.isNaN(messageTime) || messageTime < nextRunTime)
    })
  }

  function nextRunSortTime(run: DiscussionRunViewModel) {
    const runTime = parseDebugSortTime(run.sortAt || run.createdAt)
    if (Number.isNaN(runTime)) return Number.NaN
    const next = discussionRunsForWorkCards()
      .filter((candidate) => candidate.id !== run.id && (!run.agentId || candidate.agentId === run.agentId))
      .map((candidate) => parseDebugSortTime(candidate.sortAt || candidate.createdAt))
      .filter((time) => !Number.isNaN(time) && time > runTime)
      .sort((left, right) => left - right)[0]
    return next ?? Number.NaN
  }

  function discussionRunsForWorkCards() {
    if (!discussion) return []
    const runs = discussion.runs.filter(runHasStructuredProgress)
    const seen = new Set(runs.map((run) => run.id))
    for (const item of discussion.timeline) {
      if (item.kind === 'run' && item.run && runHasStructuredProgress(item.run) && !seen.has(item.run.id)) {
        runs.push(item.run)
        seen.add(item.run.id)
      }
    }
    return runs
  }

  function runHasStructuredProgress(run: DiscussionRunViewModel) {
    return runUsesProcessTranscript(run)
  }

  function runHasMeaningfulProcess(run: DiscussionRunViewModel) {
    return Boolean(run.hasPlan || run.planSteps?.length || run.thinking?.trim())
  }

  function runUsesProcessTranscript(run: DiscussionRunViewModel) {
    const oneToOne = run.mode === 'direct' || run.mode === 'personal_assistant' || discussion?.kind === 'direct' || discussion?.kind === 'personal_assistant'
    return runHasMeaningfulProcess(run) || (oneToOne && run.toolCalls.length > 0)
  }

  function planStepCountLabel(run: DiscussionRunViewModel) {
    const count = run.planSteps?.length ?? 0
    return run.planId === 'claude-tasks'
      ? t('discussionSession.agentWorkTaskCount', { count })
      : t('discussionSession.agentWorkPlanStepCount', { count })
  }

  function planSectionLabel(run: DiscussionRunViewModel) {
    return run.planId === 'claude-tasks'
      ? t('discussionSession.agentWorkTasks')
      : t('discussionSession.agentWorkPlanSteps')
  }

  function runHasDebuggableProgress(run: DiscussionRunViewModel) {
    return Boolean(run.planSteps?.length || run.toolCalls.length > 0)
  }

  function discussionMessagesForWorkCards() {
    if (!discussion) return []
    const messages = [...discussion.messages]
    const seen = new Set(messages.map((message) => message.id))
    for (const item of discussion.timeline) {
      if (item.kind === 'message' && item.message && !seen.has(item.message.id)) {
        messages.push(item.message)
        seen.add(item.message.id)
      }
    }
    return messages
  }

  function plainTextSummary(value: string) {
    const plain = renderPlainDiscussionMentions(value)
      .replace(/\s+/g, ' ')
      .trim()
    return plain.length > 180 ? `${plain.slice(0, 180)}...` : plain
  }

  function recordPreview(record: Record<string, string> | undefined) {
    if (!record || Object.keys(record).length === 0) return ''
    const preview = Object.entries(record)
      .slice(0, 3)
      .map(([key, value]) => `${key}: ${String(value)}`)
      .join(', ')
    return preview.length > 180 ? `${preview.slice(0, 180)}...` : preview
  }

  function renderContent(body: string): string {
    const mentions: string[] = []
    let tokenPrefix = 'ORRERY_DISCUSSION_MENTION_TOKEN'
    while (body.includes(tokenPrefix)) tokenPrefix += '_'
    const markedBody = replaceDiscussionMentionLinks(body, (link, index) => {
      const token = `${tokenPrefix}_${index}`
      mentions.push(`@${discussionMentionDisplayLabel(link)}`)
      return token
    })
    let html = renderSafeMarkdown(markedBody, resolveDiscussionContentUrl)
    mentions.forEach((name, index) => {
      html = html.replace(
        `${tokenPrefix}_${index}`,
        `<span class="discussion-mention">${escapeHtml(name)}</span>`
      )
    })
    return html
  }

  function discussionMentionDisplayLabel(link: { kind: string; id: string; label: string }) {
    const memberName = discussion?.members.find((member) => member.kind === link.kind && member.id === link.id)?.name
    if (memberName) return memberName
    const runName = link.kind === 'agent'
      ? discussion?.runs.find((run) => run.agentId === link.id)?.agentName
      : ''
    if (runName) return runName
    if (link.label !== link.id) return link.label
    return link.kind === 'agent' ? 'Agent' : link.label
  }

  function escapeHtml(value: string) {
    return value
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;')
  }

  function activeMentionQuery(value: string): string | undefined {
    const lastAt = value.lastIndexOf('@')
    if (lastAt < 0) return undefined
    const query = value.slice(lastAt + 1)
    if (/\s/.test(query) || /[\[\]\(\)\/]/.test(query)) return undefined
    return query
  }

  function filterMentionCandidates(members: DiscussionViewModel['members'], query: string | undefined) {
    if (query === undefined) return []
    const normalizedQuery = query.trim().toLowerCase()
    return members
      .filter((member) => member.id && member.name)
      .filter((member) => {
        if (!normalizedQuery) return true
        return [member.name, member.role, member.id].some((value) => value.toLowerCase().includes(normalizedQuery))
      })
      .slice(0, 6)
  }

  function insertMention(member: DiscussionViewModel['members'][number]) {
    const query = mentionQuery
    if (query === undefined) return
    const start = draft.lastIndexOf('@')
    if (start < 0) return
    const end = start + query.length + 1
    onDraft?.(`${draft.slice(0, start)}@${member.name} ${draft.slice(end)}`)
    onMention?.(member)
    composerInput?.focus()
  }

  function handleComposerKeydown(event: KeyboardEvent) {
    if (event.key !== 'Enter' || event.isComposing) return
    if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    if (composerSubmitDisabled) return
    void submitComposer()
  }

  function toggleMissionComposer() {
    if (!missionComposerActive && pendingAttachments.length > 0) return
    composerMode = missionComposerActive ? 'message' : 'mission'
    if (composerMode === 'message') {
      missionLaunchMode = 'immediate'
      missionScheduleAt = ''
    }
    requestAnimationFrame(() => composerInput?.focus())
  }

  async function submitComposer() {
    if (!missionComposerActive) {
      onSend?.()
      return
    }
    if (composerSubmitDisabled) return
    const request: MissionComposerRequest = {
      prompt: draft.trim(),
      launchMode: missionLaunchMode,
      scheduledAt: missionLaunchMode === 'scheduled' ? missionScheduleAt : ''
    }
    try {
      await onMissionStart?.(request)
    } catch {
      return
    }
    composerMode = 'message'
    missionLaunchMode = 'immediate'
    missionScheduleAt = ''
  }

  function openDebugPanel(runId = '') {
    if (!discussion?.id || !hasDebugRuns) return
    onDebugOpen?.(runId)
  }

  function openMessageDebug(message: DiscussionMessageViewModel) {
    const runId = debugRunIdForMessage(message)
    if (!runId) return
    openDebugPanel(runId)
  }

  function directPanelRoleLabel(current: DiscussionViewModel) {
    const targetIds = new Set(
      [current.directTargetId, current.directAgentId]
        .map((value) => value?.trim())
        .filter((value): value is string => Boolean(value))
    )
    const target = current.members.find((member) => targetIds.has(member.id))
      ?? current.members.find((member) => member.kind === 'agent')
      ?? current.members.find((member) => !isCurrentSessionUserIdentity(member.id))
    return target?.role || target?.name || t('discussionSession.privateChat')
  }

  function directPanelAvatarIdentity() {
    if (!isDirectDiscussion) return undefined
    const ids = [discussion?.directTargetId, discussion?.directAgentId]
      .map((value) => value?.trim() ?? '')
      .filter(Boolean)
    for (const id of ids) {
      if (avatarIdentityById[id]) return avatarIdentityById[id]
    }
    return ids[0]
      ? { profileId: ids[0], human: discussion?.directTargetKind === 'user' }
      : undefined
  }

  function messageAvatarIdentity(message: DiscussionMessageViewModel) {
    const authorId = message.authorId.trim()
    const currentViewer = message.isCurrentViewer ?? isCurrentSessionUserIdentity(authorId)
    if (currentViewer) {
      const aliases = [authorId, session.user.agentProfileId, session.actorId, session.user.userId, session.username]
      for (const alias of aliases) {
        const identity = avatarIdentityById[alias.trim()]
        if (identity) return identity
      }
      return {
        profileId: session.user.agentProfileId.trim() || authorId,
        human: true
      }
    }
    return avatarIdentityById[authorId] ?? {
      profileId: authorId,
      human: message.role === 'user'
    }
  }

  function debugRunIdForMessage(message: DiscussionMessageViewModel) {
    const timelineRunId = message.displayTimeline?.find((segment) => segment.runId)?.runId?.trim()
    if (timelineRunId && debugRuns.some((run) => run.id === timelineRunId)) return timelineRunId
    if (debugRuns.length === 0) return ''

    const authorRuns = message.role === 'assistant' && message.authorId
      ? debugRuns.filter((run) => run.agentId === message.authorId)
      : []
    const candidates = authorRuns.length > 0 ? authorRuns : debugRuns
    const messageTime = parseDebugSortTime(message.sortAt || message.createdAt)
    if (Number.isNaN(messageTime)) return candidates.at(-1)?.id || ''

    if (message.role === 'user') {
      const nextRun = candidates.find((run) => {
        const runTime = parseDebugSortTime(run.sortAt || run.createdAt)
        return Number.isNaN(runTime) || runTime >= messageTime
      })
      return nextRun?.id || candidates.at(-1)?.id || ''
    }

    const previousRun = [...candidates].reverse().find((run) => {
      const runTime = parseDebugSortTime(run.sortAt || run.createdAt)
      return Number.isNaN(runTime) || runTime <= messageTime
    })
    return previousRun?.id || candidates.at(-1)?.id || ''
  }

  function parseDebugSortTime(value: string | undefined) {
    const time = Date.parse(value || '')
    return Number.isFinite(time) ? time : Number.NaN
  }

  function messageDayDivider(index: number) {
    const message = renderedTimelineItems[index]?.message
    const date = messageDate(message)
    if (!message || !date) return undefined

    const key = localDayKey(date)
    for (let previousIndex = index - 1; previousIndex >= 0; previousIndex -= 1) {
      const previousMessage = renderedTimelineItems[previousIndex]?.message
      if (!previousMessage) continue
      const previousDate = messageDate(previousMessage)
      if (previousDate && localDayKey(previousDate) === key) return undefined
      break
    }
    return { key, label: messageDayLabel(date, key) }
  }

  function messageDate(message: DiscussionMessageViewModel | undefined) {
    if (!message) return undefined
    const timestamp = Date.parse(message.sortAt || message.createdAt)
    return Number.isFinite(timestamp) ? new Date(timestamp) : undefined
  }

  function localDayKey(date: Date) {
    const year = date.getFullYear()
    const month = String(date.getMonth() + 1).padStart(2, '0')
    const day = String(date.getDate()).padStart(2, '0')
    return `${year}-${month}-${day}`
  }

  function messageDayLabel(date: Date, key: string) {
    const today = new Date()
    if (key === localDayKey(today)) return t('discussionSession.today')
    const options: Intl.DateTimeFormatOptions = {
      month: 'short',
      day: 'numeric',
      ...(date.getFullYear() === today.getFullYear() ? {} : { year: 'numeric' })
    }
    return new Intl.DateTimeFormat($locale, options).format(date)
  }

  function formatRespondingLabel(names: string[]) {
    const unique = [...new Set(names.map((name) => name.trim()).filter(Boolean))]
    if (unique.length === 0) return t('discussionSession.running')
    if ($locale === 'zh-CN') {
      if (unique.length <= 2) return `${unique.join('、')} 正在响应`
      return `${unique.slice(0, 2).join('、')} 等人正在响应`
    }
    if (unique.length === 1) return `${unique[0]} is responding`
    if (unique.length === 2) return `${unique.join(', ')} are responding`
    return `${unique.slice(0, 2).join(', ')} and others are responding`
  }

  function approvalCardClass(kind: string) {
    switch (kind) {
      case 'command_execution':
        return 'border-l-red-500/65'
      case 'file_change':
        return 'border-l-sky-500/65'
      case 'user_input':
        return 'border-l-amber-500/70'
      case 'tool_use':
      case 'network_access':
        return 'border-l-violet-500/65'
      default:
        return 'border-l-amber-500/70'
    }
  }

  function onTimelineScroll(event: Event) {
    const target = event.currentTarget as HTMLElement
    if (target.scrollTop <= 96) {
      onLoadOlderMessages?.()
    }
  }

  function approvalToolLabel(approval: ApprovalItemViewModel) {
    const toolName = approval.toolName.trim()
    switch (toolName.toLowerCase()) {
      case 'mission_prepare':
        return t('approvals.tools.missionPrepare.name')
      case 'mission_coordinator_plan_parallel':
        return t('approvals.tools.planParallel.name')
      case 'discussion_talent_list':
        return t('approvals.tools.talentList.name')
      case 'discussion_member_add':
        return t('approvals.tools.memberAdd.name')
      case 'discussion_ack':
        return t('approvals.tools.acknowledge.name')
      case 'discussion_close':
        return t('approvals.tools.discussionClose.name')
      case 'exec_command':
        return t('approvals.tools.execCommand.name')
      case 'patch_apply':
        return t('approvals.tools.patchApply.name')
      case 'webfetch':
        return t('approvals.tools.webFetch.name')
      case 'websearch':
        return t('approvals.tools.webSearch.name')
    }
    if (toolName && toolName !== 'Not provided') return toolName
    return approval.typeLabel || approval.type || t('approvals.tool')
  }

  function approvalToolPurpose(approval: ApprovalItemViewModel) {
    switch (approval.toolName.trim().toLowerCase()) {
      case 'mission_prepare':
        return t('approvals.tools.missionPrepare.purpose')
      case 'mission_coordinator_plan_parallel':
        return t('approvals.tools.planParallel.purpose')
      case 'discussion_talent_list':
        return t('approvals.tools.talentList.purpose')
      case 'discussion_member_add':
        return t('approvals.tools.memberAdd.purpose')
      case 'discussion_ack':
        return t('approvals.tools.acknowledge.purpose')
      case 'discussion_close':
        return t('approvals.tools.discussionClose.purpose')
      case 'exec_command':
        return t('approvals.tools.execCommand.purpose')
      case 'patch_apply':
        return t('approvals.tools.patchApply.purpose')
      case 'webfetch':
        return t('approvals.tools.webFetch.purpose')
      case 'websearch':
        return t('approvals.tools.webSearch.purpose')
      default:
        return ''
    }
  }

  function approvalCompactText(approval: ApprovalItemViewModel) {
    const purpose = approvalToolPurpose(approval).trim()
    if (!purpose) return approval.toolName.trim() || approvalToolLabel(approval)
    const text = purpose.replace(/^Agent\s*/i, '').replace(/^The agent\s+/i, '')
    if ($locale !== 'en-US' || !text) return text
    return `${text.charAt(0).toUpperCase()}${text.slice(1)}`
  }

  function approvalRequestID(approval: ApprovalItemViewModel) {
    return approval.runtimeRequestId?.trim()
      || approval.payloadPreview.fields.find((field) => normalizeApprovalFieldKey(field.key) === 'approval_request_id')?.value.trim()
      || ''
  }

  function approvalExecutionRunning(approval: ApprovalItemViewModel) {
    if (approval.status.trim().toLowerCase() !== 'approved') return false
    const runs = discussion?.runs ?? []
    const requestID = approvalRequestID(approval)
    const runID = (approval.runtimeRunId || approval.taskId).trim()
    const matchingRun = runID ? runs.find((run) => run.id === runID) : undefined
    const candidateRuns = matchingRun ? [matchingRun] : runs
    const toolCallID = approval.runtimeToolCallId?.trim() || requestID
    if (toolCallID) {
      const exactTool = candidateRuns.flatMap((run) => run.toolCalls).find((tool) => tool.id === toolCallID)
      if (exactTool) return toolCallRunning(exactTool)
    }
    const toolName = approval.toolName.trim().toLowerCase()
    if (toolName) {
      const matchingTools = candidateRuns.flatMap((run) => run.toolCalls).filter((tool) => tool.name.trim().toLowerCase() === toolName)
      if (matchingTools.length === 1) return toolCallRunning(matchingTools[0])
    }
    return Boolean(matchingRun && isActiveRunStatus(matchingRun.status))
  }

  function approvalCompactFailed(approval: ApprovalItemViewModel) {
    const status = approval.status.trim().toLowerCase()
    return status === 'denied' || status === 'rejected' || status === 'failed' || status === 'error'
  }

  function approvalOperationTypeText(approval: ApprovalItemViewModel) {
    if (approval.choices.length > 0 || approval.displayKind === 'user_input') return t('approvals.operation.question')
    if (approval.displayKind === 'command_execution') return t('approvals.operation.command')
    if (approval.displayKind === 'file_change') return t('approvals.operation.fileChange')
    if (approval.displayKind === 'network_access' || approvalRequestUrl(approval)) return t('approvals.operation.network')
    return t('approvals.operation.tool')
  }

  function approvalStatusText(status: string) {
    switch (status.toLowerCase()) {
      case 'pending':
        return t('approvals.status.pending')
      case 'approved':
        return t('approvals.status.approved')
      case 'denied':
      case 'rejected':
        return t('approvals.status.denied')
      case 'unavailable':
        return t('approvals.status.unavailable')
      default:
        return status
    }
  }

  function approvalPriorityText(priority: string) {
    switch (priority.toLowerCase()) {
      case 'normal':
        return t('approvals.priority.normal')
      case 'high':
        return t('approvals.priority.high')
      case 'critical':
        return t('approvals.priority.critical')
      case 'medium':
        return t('approvals.priority.medium')
      case 'low':
        return t('approvals.priority.low')
      default:
        return priority
    }
  }

  function approvalRequestUrl(approval: ApprovalItemViewModel) {
    const urlField = approval.payloadPreview.fields.find((field) => normalizeApprovalFieldKey(field.key) === 'url')
    if (urlField?.value) return urlField.value
    const content = approval.payloadPreview.content.trim()
    if (/^https?:\/\//i.test(content)) return content
    return ''
  }

  function approvalIsPending(approval: ApprovalItemViewModel) {
    const status = approval.status.trim().toLowerCase()
    return status === '' || status === 'pending' || status === 'open'
  }

  function approvalExplanationLines(approval: ApprovalItemViewModel) {
    const hasRequestContent = Boolean(approvalToolPurpose(approval) || approval.payloadPreview.content.trim() || visibleApprovalFields(approval.payloadPreview.fields).length || approval.choices.length)
    const skip = new Set([
      normalizeApprovalText(approval.title),
      normalizeApprovalText(approval.payloadPreview.content)
    ])
    const lines: string[] = []
    for (const value of [hasRequestContent ? '' : approval.summary, approval.details, approval.riskSummary]) {
      const text = value.trim()
      if (!text || text === 'Not provided') continue
      const normalized = normalizeApprovalText(text)
      if (skip.has(normalized) || lines.some((line) => normalizeApprovalText(line) === normalized)) continue
      lines.push(text)
    }
    return lines
  }

  function normalizeApprovalText(value: string) {
    return value.trim().replace(/\s+/g, ' ').toLowerCase()
  }

  function normalizeApprovalFieldKey(key: string) {
    return key.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '')
  }

  function isHiddenApprovalField(key: string) {
    const normalized = normalizeApprovalFieldKey(key)
    return hiddenApprovalFieldKeys.has(normalized)
      || normalized === 'id'
      || normalized.endsWith('_id')
      || normalized.endsWith('_ids')
      || normalized === 'idempotency_key'
      || /(?:^|_)(?:token|secret|password|passwd|credential|authorization|api_key|apikey|auth)(?:_|$)/.test(normalized)
  }

  function isOpaqueApprovalIdentifier(value: string) {
    const normalized = value.trim()
    return /^(?:call|toolu|tool_call|request|req)[_-][a-z0-9_-]{8,}$/i.test(normalized)
      || /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(normalized)
  }

  function visibleApprovalFields(fields: ApprovalPayloadField[]) {
    return fields.filter((field) => field.value.trim() && !isHiddenApprovalField(field.key) && !isOpaqueApprovalIdentifier(field.value))
  }

  function approvalPayloadContent(approval: ApprovalItemViewModel) {
    const content = approval.payloadPreview.content.trim()
    if (!content || isOpaqueApprovalIdentifier(content)) return ''
    const duplicateField = visibleApprovalFields(approval.payloadPreview.fields).some((field) => field.value.trim() === content || normalizeApprovalText(field.value) === normalizeApprovalText(content))
    return duplicateField ? '' : content
  }

  function approvalFieldLabel(key: string) {
    switch (normalizeApprovalFieldKey(key)) {
      case 'url':
        return t('approvals.field.url')
      case 'command':
      case 'cmd':
        return t('approvals.field.command')
      case 'file':
      case 'file_path':
      case 'path':
        return t('approvals.field.path')
      case 'scope':
        return t('approvals.field.scope')
      case 'title':
        return t('approvals.field.title')
      case 'final_goal':
        return t('approvals.field.finalGoal')
      case 'root_goal':
        return t('approvals.field.rootGoal')
      case 'goal':
      case 'task_goal':
        return t('approvals.field.goal')
      case 'business_direction':
        return t('approvals.field.businessDirection')
      case 'plan_summary':
        return t('approvals.field.planSummary')
      case 'query':
        return t('approvals.field.query')
      case 'content':
        return t('approvals.field.content')
      case 'summary':
        return t('approvals.field.summary')
      case 'description':
        return t('approvals.field.description')
      case 'message':
        return t('approvals.field.message')
      case 'reason':
        return t('approvals.field.reason')
      case 'role':
        return t('approvals.field.role')
      case 'capability':
        return t('approvals.field.capability')
      case 'prompt':
      case 'question':
        return t('approvals.field.prompt')
      case 'tool':
      case 'tool_name':
        return t('approvals.field.tool')
      default:
        return key.replace(/[_-]+/g, ' ')
    }
  }

  function approvalFieldValueClass(key: string) {
    const normalized = normalizeApprovalFieldKey(key)
    return ['command', 'cmd', 'file', 'file_path', 'path', 'url'].includes(normalized) ? 'font-mono' : ''
  }

  function openOtherApproval(approval: ApprovalItemViewModel, action: ApprovalActionViewModel | undefined) {
    activeApproval = approval
    activeApprovalAction = action
    otherApprovalMode = true
    otherApprovalInput = approvalInputDefault(approval, action)
  }

  function approvalInputDefault(approval: ApprovalItemViewModel, action: ApprovalActionViewModel | undefined) {
    if (!action) return ''
    if (action.inputKind === 'modified_input' || action.allowsModification) {
      return action.defaultMessage || approval.payloadPreview.content || ''
    }
    return action.defaultMessage ?? ''
  }

  function closeInlineApproval() {
    activeApproval = undefined
    activeApprovalAction = undefined
    otherApprovalMode = false
    otherApprovalInput = ''
  }

  function isOtherApprovalMode(approval: ApprovalItemViewModel) {
    return activeApproval?.id === approval.id && otherApprovalMode
  }

  function submitApproval(approval: ApprovalItemViewModel, action: ApprovalActionViewModel | undefined, form: InlineApprovalForm = { message: '' }) {
    if (!action) return
    onApprovalAction?.(approval, action, form)
    if (activeApproval?.id === approval.id) closeInlineApproval()
  }

  function submitChoice(approval: ApprovalItemViewModel, action: ApprovalActionViewModel | undefined, choiceId: string) {
    submitApproval(approval, action, { message: '', choiceId })
  }

  function submitOtherApproval(approval: ApprovalItemViewModel, action: ApprovalActionViewModel | undefined) {
    const value = otherApprovalInput.trim()
    if (!value || !action) return
    const form: InlineApprovalForm = { message: value }
    if (action.inputKind === 'modified_input' || action.allowsModification) {
      form.modifiedInput = value
    }
    submitApproval(approval, action, form)
  }

  function answerAction(approval: ApprovalItemViewModel) {
    return approval.actions.find((action) => action.inputKind === 'choice_or_text' || action.id === 'answer') ?? approval.actions[0]
  }

  function actionNeedsInlineInput(action: ApprovalActionViewModel) {
    return action.id === 'other' || action.reasonRequired || action.inputKind === 'reason' || action.inputKind === 'modified_input' || action.inputKind === 'choice_or_text' || action.allowsModification
  }

  function submitInlineAction(approval: ApprovalItemViewModel, action: ApprovalActionViewModel) {
    if (actionNeedsInlineInput(action)) {
      openOtherApproval(approval, action)
      return
    }
    submitApproval(approval, action, { message: '', remember: action.rememberAllowed })
  }

  function approvalIsBusy(approval: ApprovalItemViewModel) {
    return approvalBusyIds.includes(approval.id)
  }

  function inlineApprovalInputLabel(action: ApprovalActionViewModel | undefined) {
    if (!action) return t('approvals.otherResponse')
    if (action.inputKind === 'modified_input' || action.allowsModification) return t('approvals.modifiedInput')
    if (action.reasonRequired || action.inputKind === 'reason') return t('approvals.reasonRequired')
    if (action.inputKind === 'choice_or_text') return t('approvals.otherResponse')
    return t('approvals.message')
  }

  function approvalActionVariant(action: ApprovalActionViewModel): 'default' | 'destructive' | 'outline' {
    if (action.style === 'danger' || action.decision === 'denied') return 'destructive'
    if (action.style === 'secondary' || action.decision === 'needs_input') return 'outline'
    return 'default'
  }

  function chooseAttachment() {
    if (!attachmentUploadEnabled) return
    attachmentInput?.click()
  }

  async function onAttachmentChange(event: Event) {
    const input = event.currentTarget as HTMLInputElement
    if (missionComposerActive) {
      input.value = ''
      return
    }
    const file = input.files?.[0]
    if (!file) return
    await onAttachmentUpload?.(file)
    input.value = ''
  }

  function preferredVoiceMimeType() {
    if (typeof MediaRecorder === 'undefined' || typeof MediaRecorder.isTypeSupported !== 'function') return ''
    return ['audio/webm;codecs=opus', 'audio/webm', 'audio/mp4', 'audio/ogg;codecs=opus']
      .find((contentType) => MediaRecorder.isTypeSupported(contentType)) ?? ''
  }

  async function toggleVoiceRecording() {
    if (voiceRecording) {
      stopVoiceRecording()
      return
    }
    await startVoiceRecording()
  }

  async function startVoiceRecording() {
    if (!voiceInputEnabled || voiceStarting || voiceProcessing) return
    const requestVersion = ++voiceRequestVersion
    voiceStarting = true
    voiceError = ''
    discardVoiceRecording = false
    voiceElapsedSeconds = 0
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      if (voiceDisposed || requestVersion !== voiceRequestVersion) {
        stream.getTracks().forEach((track) => track.stop())
        return
      }
      const mimeType = preferredVoiceMimeType()
      const recorder = mimeType ? new MediaRecorder(stream, { mimeType }) : new MediaRecorder(stream)
      voiceStream = stream
      voiceRecorder = recorder
      voiceChunks = []
      recorder.ondataavailable = (event) => {
        if (event.data.size > 0) voiceChunks.push(event.data)
      }
      recorder.onerror = () => {
        voiceError = t('discussionSession.voiceRecordingFailed')
        cancelVoiceRecording()
      }
      recorder.onstop = () => {
        void finishVoiceRecording(recorder)
      }
      recorder.start(250)
      voiceRecording = true
      const startedAt = Date.now()
      voiceTimer = setInterval(() => {
        voiceElapsedSeconds = Math.min(maxVoiceRecordingSeconds, Math.floor((Date.now() - startedAt) / 1000))
        if (voiceElapsedSeconds >= maxVoiceRecordingSeconds) stopVoiceRecording()
      }, 250)
    } catch (error) {
      const name = error instanceof DOMException ? error.name : ''
      voiceError = name === 'NotAllowedError' || name === 'SecurityError'
        ? t('discussionSession.voiceRecordingPermissionDenied')
        : t('discussionSession.voiceRecordingFailed')
      clearVoiceRecordingResources()
    } finally {
      if (!voiceDisposed && requestVersion === voiceRequestVersion) voiceStarting = false
    }
  }

  function stopVoiceRecording() {
    if (!voiceRecorder || voiceRecorder.state === 'inactive') return
    discardVoiceRecording = false
    voiceRecording = false
    voiceProcessing = true
    clearVoiceTimer()
    voiceRecorder.stop()
  }

  function cancelVoiceRecording() {
    voiceRequestVersion += 1
    discardVoiceRecording = true
    voiceRecording = false
    voiceStarting = false
    voiceProcessing = false
    voiceElapsedSeconds = 0
    clearVoiceTimer()
    if (voiceRecorder && voiceRecorder.state !== 'inactive') {
      voiceRecorder.stop()
      return
    }
    clearVoiceRecordingResources()
  }

  async function finishVoiceRecording(recorder: MediaRecorder) {
    const shouldDiscard = discardVoiceRecording || voiceDisposed
    const chunks = voiceChunks
    const contentType = recorder.mimeType || chunks.find((chunk) => chunk.type)?.type || 'audio/webm'
    clearVoiceRecordingResources()
    if (shouldDiscard) return
    const recording = new Blob(chunks, { type: contentType })
    if (recording.size === 0) {
      voiceProcessing = false
      voiceError = t('discussionSession.voiceRecordingEmpty')
      return
    }
    try {
      await onAttachmentUpload?.(new File(
        [recording],
        `voice-${voiceRecordingTimestamp(new Date())}.${voiceFileExtension(contentType)}`,
        { type: contentType, lastModified: Date.now() }
      ))
    } catch {
      voiceError = t('discussionSession.uploadAttachmentFailed')
    } finally {
      voiceProcessing = false
      voiceElapsedSeconds = 0
    }
  }

  function clearVoiceRecordingResources() {
    clearVoiceTimer()
    voiceStream?.getTracks().forEach((track) => track.stop())
    voiceStream = undefined
    voiceRecorder = undefined
    voiceChunks = []
    if (!voiceDisposed) {
      voiceRecording = false
      voiceElapsedSeconds = 0
    }
  }

  function clearVoiceTimer() {
    if (voiceTimer === undefined) return
    clearInterval(voiceTimer)
    voiceTimer = undefined
  }

  function voiceRecordingTimestamp(value: Date) {
    const parts = [
      value.getFullYear(),
      String(value.getMonth() + 1).padStart(2, '0'),
      String(value.getDate()).padStart(2, '0'),
      String(value.getHours()).padStart(2, '0'),
      String(value.getMinutes()).padStart(2, '0'),
      String(value.getSeconds()).padStart(2, '0')
    ]
    return `${parts.slice(0, 3).join('')}-${parts.slice(3).join('')}`
  }

  function voiceFileExtension(contentType: string) {
    const normalized = contentType.toLowerCase()
    if (normalized.includes('mp4')) return 'm4a'
    if (normalized.includes('ogg')) return 'ogg'
    return 'webm'
  }

  function formatVoiceDuration(seconds: number) {
    const minutes = Math.floor(seconds / 60)
    return `${String(minutes).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`
  }

  function isAudioAttachment(attachment: { contentType?: string }) {
    return attachment.contentType?.trim().toLowerCase().startsWith('audio/') ?? false
  }

  function isPreviewableAttachment(attachment: { contentType?: string; fileName?: string; previewUrl?: string }) {
    return Boolean(attachment.previewUrl) && (
      attachment.contentType?.trim().toLowerCase().split(';', 1)[0] === 'text/html' ||
      /\.html?$/i.test(attachment.fileName?.trim() ?? '')
    )
  }

  function resolveDiscussionContentUrl(raw: string) {
    const attachments = discussionAttachments().filter(isPreviewableAttachment)
    const normalized = decodeUrl(raw).toLowerCase()
    const matchedById = attachments.find((attachment) => normalized.includes(attachment.id.toLowerCase()))
    if (matchedById?.previewUrl) return matchedById.previewUrl
    if (!isRuntimeOnlyUrl(raw)) return raw
    const matchedByFileName = attachments.find((attachment) =>
      normalized.endsWith(`/${attachment.fileName.toLowerCase()}`) ||
      normalized.endsWith(`-${attachment.fileName.toLowerCase()}`)
    )
    return matchedByFileName?.previewUrl || ''
  }

  function discussionAttachments() {
    const byId = new Map((discussion?.attachments ?? []).map((attachment) => [attachment.id, attachment]))
    for (const message of discussion?.messages ?? []) {
      for (const attachment of message.attachments ?? []) byId.set(attachment.id, attachment)
    }
    return [...byId.values()]
  }

  function isRuntimeOnlyUrl(raw: string) {
    const trimmed = raw.trim()
    if (/^[a-z]:[\\/]/i.test(trimmed) || trimmed.startsWith('/data/') || trimmed.startsWith('/home/')) return true
    try {
      const url = new URL(trimmed, 'https://orrery.invalid')
      return ['127.0.0.1', 'localhost', '[::1]', '0.0.0.0'].includes(url.hostname.toLowerCase())
    } catch {
      return false
    }
  }

  function decodeUrl(raw: string) {
    try {
      return decodeURIComponent(raw)
    } catch {
      return raw
    }
  }

  function missionRuntimeAgentName(session: RuntimeSessionViewModel) {
    const lane = selectedMissionOps?.agentLanes.find((item) =>
      [item.agentId, item.profileId, item.instanceId].includes(session.agentId) ||
      item.runtimeSessions.some((runtime) => runtime.runId === session.runId)
    )
    return lane?.displayName || session.agentId
  }

  function missionRuntimeTaskTitle(session: RuntimeSessionViewModel) {
    return taskTimeline.find((task) => task.id === session.taskId)?.title || session.taskId
  }

  function missionRuntimeStatusDotClass(status: string) {
    const normalized = status.trim().toLowerCase()
    if (['running', 'started', 'queued'].includes(normalized)) return 'bg-blue-500'
    if (['completed', 'approved', 'succeeded'].includes(normalized)) return 'bg-emerald-500'
    if (['failed', 'blocked', 'timeout', 'cancelled', 'canceled'].includes(normalized)) return 'bg-rose-500'
    return 'bg-[#a0a0a0]'
  }

  function memberAvatarIdentity(member: DiscussionViewModel['members'][number]) {
    return avatarIdentityById[member.id] ?? {
      profileId: member.id,
      human: ['human', 'user'].includes(member.kind.toLowerCase())
    }
  }

  function useAssistantPrompt(value: string) {
    onDraft?.(value)
    requestAnimationFrame(() => composerInput?.focus())
  }

  function linkedMissionStatusLabel(status: string) {
    const key = ({
      active: 'mission.active',
      pending: 'mission.pending',
      blocked: 'mission.blocked',
      completed: 'mission.done',
      done: 'mission.done'
    } as const)[status.toLowerCase() as 'active' | 'pending' | 'blocked' | 'completed' | 'done']
    return key ? t(key) : status
  }

  function linkedMissionStatusDotClass(status: string) {
    if (status === 'active' || status === 'completed' || status === 'done') return 'bg-emerald-500'
    if (status === 'blocked') return 'bg-rose-500'
    return 'bg-amber-500'
  }

  function formatBytes(size: number) {
    if (!Number.isFinite(size) || size <= 0) return '0 B'
    if (size < 1024) return `${size} B`
    const units = ['KB', 'MB', 'GB'] as const
    let value = size / 1024
    let unitIndex = 0
    while (value >= 1024 && unitIndex < units.length - 1) {
      value /= 1024
      unitIndex += 1
    }
    return `${value >= 10 ? value.toFixed(0) : value.toFixed(1)} ${units[unitIndex]}`
  }
</script>

<div class="relative flex h-full overflow-hidden bg-[#fafafa]">
  <div class="flex min-w-0 flex-1 flex-col">
    <header class="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-[#ececec] bg-white px-5 py-3 sm:flex-nowrap sm:gap-3">
      <div class="flex min-w-0 flex-1 items-center gap-3">
        {#if assistantMode}
          {#if assistantAvatarUrl}
            <img class="h-11 w-11 shrink-0 rounded-full object-cover" src={assistantAvatarUrl} alt={panelTitle} />
          {:else}
            <span class="grid h-11 w-11 shrink-0 place-items-center rounded-full bg-emerald-600 text-white">
              <Bot class="h-5 w-5" />
            </span>
          {/if}
        {:else if panelAvatarIdentity}
          <WorkforceAvatar
            profileId={panelAvatarIdentity.profileId}
            name={panelTitle}
            human={panelAvatarIdentity.human}
            class="h-11 w-11 rounded-full"
            textClass="text-[13px]"
          />
        {:else if !isDirectDiscussion && teamMembers.length > 0}
          <div
            class="flex h-11 shrink-0 items-center pl-1"
            role="group"
            aria-label={t('discussion.memberCount', { count: teamMembers.length })}
            data-group-avatar-stack
          >
            {#each teamMembers.slice(0, 4) as member, index (member.id)}
              {@const identity = memberAvatarIdentity(member)}
              <span class={index === 0 ? '' : '-ml-2.5'} style={`z-index: ${teamMembers.length - index}`}>
                <WorkforceAvatar
                  profileId={identity.profileId}
                  name={member.name}
                  human={identity.human}
                  class="h-9 w-9 rounded-full ring-2 ring-card"
                  textClass="text-[10px]"
                />
              </span>
            {/each}
            {#if teamMembers.length > 4}
              <span class="-ml-2.5 grid h-9 w-9 place-items-center rounded-full bg-muted text-[10px] font-semibold text-muted-foreground ring-2 ring-card">
                +{teamMembers.length - 4}
              </span>
            {/if}
          </div>
        {:else}
          <span class="flex h-11 w-11 shrink-0 items-center justify-center rounded-full text-[13px] font-medium text-white {panelAvatarClass}">
            {panelInitial}
          </span>
        {/if}
        <div class="min-w-0 leading-tight">
          <h1 class="min-w-0 truncate text-[14px] font-medium text-black">{panelTitle}</h1>
          <div class="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11.5px] text-[#8a8a8a]">
            {#if isRunning || busy}
              <span class="h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-400"></span>
            {/if}
            <span class="truncate">{panelSubtitle}</span>
            {#if isDirectDiscussion}
              <span class="sr-only">{t('discussionSession.privateChat')}</span>
            {/if}
            <span aria-hidden="true">·</span>
            <span class="sr-only">{t('discussionSession.runtimeStatus')}: {runtimeLabel}</span>
            <span class="shrink-0" data-discussion-runtime-status aria-hidden="true">{runtimeLabel}</span>
          </div>
        </div>
      </div>
      <div class="flex w-full min-w-0 items-center justify-end gap-1 overflow-x-auto text-[#8a8a8a] sm:w-auto sm:shrink-0 sm:overflow-visible" data-discussion-header-actions>
        {#if headerLeadingActions}
          {@render headerLeadingActions()}
        {/if}
        <div class="relative">
          <button
            type="button"
            class="grid h-8 w-8 place-items-center rounded-lg hover:bg-[#f0f0f0]"
            aria-label={t('discussionSession.moreActions')}
            title={t('discussionSession.moreActions')}
            aria-expanded={headerMenuOpen}
            onclick={() => (headerMenuOpen = !headerMenuOpen)}
          >
            <MoreHorizontal class="h-4 w-4" />
          </button>
          {#if headerMenuOpen}
            <div class="absolute right-0 top-9 z-30 min-w-52 overflow-hidden rounded-lg border border-[#e4e4e4] bg-white p-1 text-black shadow-lg" role="menu">
            {#if workspaceSettingsAvailable}
              <button
                type="button"
                class="flex h-9 w-full items-center gap-2 rounded-md px-2.5 text-left text-xs hover:bg-[#f5f5f5]"
                onclick={() => { headerMenuOpen = false; onWorkspaceSettingsOpen?.() }}
              >
                <FolderCog class="h-4 w-4 text-[#8a8a8a]" />
                {t('discussionSession.openWorkspaceSettings')}
              </button>
            {/if}
            {#if primaryMission}
              <a class="flex h-9 w-full items-center gap-2 rounded-md px-2.5 text-xs hover:bg-[#f5f5f5]" href={primaryMission.href}>
                <FolderKanban class="h-4 w-4 text-[#8a8a8a]" />
                <span class="min-w-0 truncate">{t('discussionSession.openMission')}: {primaryMission.title}</span>
              </a>
            {/if}
            {#if !isDirectDiscussion}
              <button
                type="button"
                class="flex h-9 w-full items-center gap-2 rounded-md px-2.5 text-left text-xs hover:bg-[#f5f5f5]"
                onclick={() => { headerMenuOpen = false; onTaskTimelineToggle?.() }}
              >
                <Target class="h-4 w-4 text-[#8a8a8a]" />
                {t('discussionSession.taskTimeline')}
              </button>
            {/if}
            {#if isDirectDiscussion && discussion?.id}
              <button
                type="button"
                class="flex h-9 w-full items-center gap-2 rounded-md px-2.5 text-left text-xs hover:bg-[#f5f5f5]"
                onclick={() => { headerMenuOpen = false; onConvertToGroup?.() }}
              >
                <Users class="h-4 w-4 text-[#8a8a8a]" />
                {t('discussionSession.convertToGroup')}
              </button>
            {/if}
            <button
              type="button"
              disabled={!discussion?.id || !hasDebugRuns}
              class="flex h-9 w-full items-center gap-2 rounded-md px-2.5 text-left text-xs hover:bg-[#f5f5f5] disabled:cursor-not-allowed disabled:opacity-40"
              onclick={() => { headerMenuOpen = false; openDebugPanel() }}
            >
              <Bug class="h-4 w-4 text-[#8a8a8a]" />
              {t('discussionSession.debugPrompt')}
            </button>
            </div>
          {/if}
        </div>
      </div>
    </header>

    {#if assistantMode}
      <div class="flex min-h-10 shrink-0 flex-wrap items-center justify-between gap-2 border-b border-emerald-100 bg-emerald-50/60 px-5 py-2 text-[11.5px]" data-personal-assistant-status>
        <div class="flex min-w-0 items-center gap-2 text-emerald-900/75">
          <span class="h-1.5 w-1.5 shrink-0 rounded-full {assistantEnabled ? 'bg-emerald-500' : 'bg-[#a0a0a0]'}"></span>
          <span class="truncate font-medium">{assistantRuntimeLabel}</span>
        </div>
        <div class="flex shrink-0 items-center gap-1.5 text-emerald-900/65">
          <span class="inline-flex h-6 items-center gap-1 rounded-md bg-white/75 px-2">
            <ListTodo class="h-3.5 w-3.5" />
            {t('discussionSession.personalAssistantMissionCount', { count: assistantMissionCount })}
          </span>
          {#if assistantPendingApprovalCount > 0}
            <span class="inline-flex h-6 items-center gap-1 rounded-md bg-amber-50 px-2 text-amber-700">
              {t('discussionSession.personalAssistantApprovalCount', { count: assistantPendingApprovalCount })}
            </span>
          {/if}
          <a
            href="/profile#personal-assistant"
            class="grid h-6 w-6 place-items-center rounded-md hover:bg-white"
            aria-label={t('discussionSession.personalAssistantSettings')}
            title={t('discussionSession.personalAssistantSettings')}
          >
            <Settings2 class="h-3.5 w-3.5" />
          </a>
        </div>
      </div>
    {/if}

    {#if !isDirectDiscussion && (discussion?.linkedMissions.length ?? 0) > 0}
      <div class="shrink-0 border-b border-[#ececec] bg-white px-5" data-discussion-mission-strip>
        <div class="flex h-12 min-w-0 items-stretch gap-2">
          <div class="flex shrink-0 items-center gap-1.5 text-[11px] font-medium text-[#666]">
            <FolderKanban class="h-3.5 w-3.5 text-blue-600" />
            <span>Mission</span>
            <span class="grid h-5 min-w-5 place-items-center rounded-full bg-[#f0f0f0] px-1 text-[9.5px] text-[#777]">{discussion?.linkedMissions.length ?? 0}</span>
          </div>
          <div class="subtle-scrollbar flex min-w-0 flex-1 overflow-x-auto" role="tablist" aria-label={t('discussionSession.selectMission')}>
            {#each discussion?.linkedMissions ?? [] as mission (mission.id)}
              {@const selected = mission.id === primaryMission?.id}
              {@const progress = selected && selectedMissionContext?.mission.id === mission.id ? selectedMissionContext.mission.deliveryPercent : undefined}
              <button
                type="button"
                role="tab"
                class="relative flex h-12 min-w-24 max-w-40 items-center gap-1.5 overflow-hidden px-3 text-left text-[11.5px] transition-[color,background-color] duration-200 {selected ? 'bg-blue-50/45 font-medium text-blue-700' : 'text-[#666] hover:bg-[#f7f7f7] hover:text-black'}"
                aria-selected={selected}
                aria-label={`${mission.title} · ${linkedMissionStatusLabel(mission.status)}`}
                onclick={() => (activeMissionId = mission.id)}
              >
                <span class="h-1.5 w-1.5 shrink-0 rounded-full {linkedMissionStatusDotClass(mission.status)}"></span>
                <span class="min-w-0 flex-1 truncate">{mission.title}</span>
                {#if progress !== undefined}<span class="shrink-0 text-[9.5px] font-medium text-blue-600">{progress}%</span>{/if}
                <span class="absolute inset-x-2 bottom-0 h-0.5 origin-center bg-blue-600 transition-transform duration-200 ease-out {selected ? 'scale-x-100' : 'scale-x-0'}"></span>
              </button>
            {/each}
          </div>
        </div>
      </div>
    {/if}

    <div bind:this={timelineScroller} class="subtle-scrollbar flex-1 overflow-auto bg-background px-5 py-7 sm:px-8" data-discussion-timeline-scroller onscroll={onTimelineScroll}>
      <div class="mx-auto max-w-[50rem] space-y-7" data-discussion-timeline data-codex-transcript>
        {#if discussion && discussion.messages.length > 0}
          {#if olderMessagesLoading}
            <div class="py-1 text-center text-xs text-muted-foreground">{t('discussionSession.loadingOlderMessages')}</div>
          {:else if olderMessagesHasMore}
            <button
              type="button"
              class="mx-auto block rounded border border-border px-3 py-1 text-xs text-muted-foreground hover:bg-muted"
              onclick={onLoadOlderMessages}
            >
              {t('discussionSession.loadOlderMessages')}
            </button>
          {:else}
            <div class="py-1 text-center text-xs text-muted-foreground">{t('discussionSession.noMore')}</div>
          {/if}
        {/if}
        {#if olderMessagesError}
          <div class="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">{olderMessagesError}</div>
        {/if}
        {#each renderedTimelineItems as item, index (item.id)}
          {#if !toolTimelineItemAttachesToNextMessage(index)}
          <div data-discussion-timeline-item data-discussion-timeline-id={item.id}>
            {#if item.kind === 'message' && item.message}
              {@const message = item.message}
              {@const attachedToolItem = attachedToolTimelineItemForMessage(index)}
              {@const dayDivider = messageDayDivider(index)}
              {@const own = message.role === 'user' && (message.isCurrentViewer ?? isCurrentSessionUserIdentity(message.authorId))}
              {@const messageIdentity = messageAvatarIdentity(message)}
              {@const messageDebugRunId = debugRunIdForMessage(message)}
              {@const isPAOriginMissionHostMessage =
                discussion?.kind === 'group' &&
                discussion?.personalAssistantRuntime === true &&
                message.role !== 'user' &&
                message.authorId.startsWith('personal-assistant-')}
              {#if dayDivider}
                <div class="mb-4 flex justify-center py-1" data-discussion-day-divider data-day-key={dayDivider.key}>
                  <span class="px-2.5 py-1 text-[11px] text-[#9a9a9a]">{dayDivider.label}</span>
                </div>
              {/if}
              <article class="group flex items-start gap-2.5" class:justify-end={own} data-transcript-role={own ? 'user' : 'agent'}>
                {#if !own && message.canApproveRetry}
                  <Button
                    variant="ghost"
                    size="icon"
                    disabled={busy || isRunning}
                    onclick={() => onRetryMessage?.(message.id, true)}
                    ariaLabel={t('discussionSession.approveRetryMessage')}
                  >
                    <RotateCcw class="h-4 w-4" />
                  </Button>
                {/if}
                {#if !own}
                  <span class="mt-0.5 h-8 w-8 shrink-0" data-message-avatar data-message-avatar-side="other">
                    {#if assistantMode && assistantAvatarUrl && message.role !== 'user'}
                      <img class="h-8 w-8 rounded-full object-cover" src={assistantAvatarUrl} alt={panelTitle} />
                    {:else if isPAOriginMissionHostMessage}
                      {#if assistantAvatarUrl}
                        <img
                          class="h-8 w-8 rounded-full object-cover"
                          src={assistantAvatarUrl}
                          alt={panelTitle}
                          data-personal-assistant-message-avatar
                        />
                      {:else}
                        <span
                          class="grid h-8 w-8 place-items-center rounded-full bg-emerald-600 text-white"
                          role="img"
                          aria-label={panelTitle}
                          data-personal-assistant-message-avatar
                        >
                          <Bot class="h-4 w-4" />
                        </span>
                      {/if}
                    {:else}
                      <WorkforceAvatar
                        profileId={messageIdentity.profileId}
                        name={message.authorName || panelTitle}
                        human={messageIdentity.human}
                        class="h-8 w-8 rounded-full"
                        textClass="text-[11px]"
                      />
                    {/if}
                  </span>
                {/if}
                <div class={`flex min-w-0 flex-col ${own ? 'max-w-[76%] items-end' : 'flex-1 items-start'}`}>
                  {#if !own && !isDirectDiscussion && message.authorName}
                    <span class="mb-1 px-1 text-[11px] font-medium text-[#707070]" data-message-author>{message.authorName}</span>
                  {/if}
                  {#if attachedToolItem?.run}
                    {@const attachedRun = attachedToolItem.run}
                    {@const attachedTools = stableTimelineToolCalls(attachedToolItem.id, attachedRun.toolCalls)}
                    <div class="mb-1.5 w-full" data-message-tool-calls={attachedToolItem.id}>
                      <DiscussionToolCallList
                        drawerId={attachedToolItem.id}
                        toolCalls={attachedTools}
                        isStreaming={isActiveRunStatus(attachedRun.status)}
                        collapsible={attachedRun.toolCalls.length > 2}
                        defaultExpanded={false}
                      />
                    </div>
                  {/if}
                  <div
                    class={`discussion-message-bubble relative tracking-normal ${own
                      ? 'rounded-xl bg-[#f1f1f1] px-4 py-2.5 text-[13.5px] leading-[1.6] text-[#202124] dark:bg-[#2b2b2b] dark:text-foreground'
                      : 'w-full bg-transparent px-0 py-0 text-[14px] leading-[1.72] text-[#202124] dark:text-foreground'}`}
                    data-message-own={own}
                  >
                    {#if message.humanTodoId}
                      <a class="mb-2 flex min-w-0 items-center justify-between gap-2 border-y border-border py-2 text-sm font-medium hover:underline" href={`/todos/${encodeURIComponent(message.humanTodoId)}`} data-human-todo-card>
                        <span>{t('handoff.title')}</span><span>{t('handoff.open')}</span>
                      </a>
                    {/if}
                    {#if hasDebugRuns}
                      <button
                        type="button"
                        class={`absolute right-0 top-0 inline-flex h-5 w-5 shrink-0 items-center justify-center rounded border border-transparent opacity-0 transition group-hover:opacity-55 hover:opacity-100 focus-visible:opacity-100 disabled:pointer-events-none disabled:opacity-25 ${own ? 'hover:border-border hover:bg-background/70' : 'hover:border-border hover:bg-muted/70'}`}
                        disabled={!messageDebugRunId}
                        data-testid={`discussion-message-debug-${message.id}`}
                        data-run-id={messageDebugRunId}
                        aria-label={t('discussionSession.debugMessagePrompt')}
                        title={t('discussionSession.debugMessagePrompt')}
                        onclick={() => openMessageDebug(message)}
                      >
                        <Bug class="h-3 w-3" />
                      </button>
                    {/if}
                    {#if message.id.startsWith('streaming-')}
                      <div
                        class="discussion-message-content whitespace-pre-wrap break-words {hasDebugRuns ? 'pr-5' : ''}"
                        data-message-streaming
                        aria-busy="true"
                        aria-live="polite"
                      >{renderPlainDiscussionMentions(message.content, discussionMentionDisplayLabel)}<span class="ml-0.5 inline-block h-3.5 w-px bg-current align-[-2px] motion-safe:animate-pulse" data-streaming-cursor aria-hidden="true"></span></div>
                    {:else}
                      <div class="discussion-message-content {hasDebugRuns ? 'pr-5' : ''}">{@html renderContent(message.content)}</div>
                    {/if}
                    {#if message.attachments?.length}
                      <div class="mt-2 flex flex-wrap gap-2 text-foreground">
                        {#each message.attachments as attachment (attachment.id)}
                          {#if isAudioAttachment(attachment)}
                            <div class="flex w-64 max-w-full items-center gap-2 rounded-md border border-border bg-background px-2 py-1" data-audio-attachment={attachment.id}>
                              <Mic class="h-3.5 w-3.5 shrink-0 opacity-70" aria-hidden="true" />
                              <audio class="h-8 min-w-0 flex-1" controls preload="metadata" src={attachment.downloadUrl} aria-label={`${t('discussionSession.playVoiceMessage')} ${attachment.fileName}`}></audio>
                              <a href={attachment.downloadUrl} download class="grid h-7 w-7 shrink-0 place-items-center rounded hover:bg-muted" aria-label={`${t('discussionSession.downloadAttachment')} ${attachment.fileName}`}>
                                <Download class="h-3.5 w-3.5 opacity-70" />
                              </a>
                            </div>
                          {:else if isPreviewableAttachment(attachment)}
                            <div class="inline-flex min-w-0 max-w-80 items-center rounded-md border border-border bg-background text-xs hover:bg-muted" data-preview-attachment={attachment.id}>
                              <a
                                href={attachment.previewUrl}
                                target="_blank"
                                rel="noopener noreferrer"
                                class="flex min-w-0 items-center gap-2 px-2.5 py-1.5"
                                aria-label={`${t('discussionSession.previewAttachment')} ${attachment.fileName}`}
                              >
                                <FileText class="h-3.5 w-3.5 shrink-0 opacity-70" />
                                <span class="min-w-0 truncate font-medium">{attachment.fileName}</span>
                                <span class="shrink-0 opacity-70">{formatBytes(attachment.sizeBytes)}</span>
                                <ExternalLink class="h-3.5 w-3.5 shrink-0 opacity-70" />
                              </a>
                              <a
                                href={attachment.downloadUrl}
                                download
                                class="grid h-7 w-7 shrink-0 place-items-center border-l border-border hover:bg-muted"
                                aria-label={`${t('discussionSession.downloadAttachment')} ${attachment.fileName}`}
                              >
                                <Download class="h-3.5 w-3.5 opacity-70" />
                              </a>
                            </div>
                          {:else}
                            <a
                              href={attachment.downloadUrl}
                              download
                              class="inline-flex min-w-0 max-w-72 items-center gap-2 rounded-md border border-border bg-background px-2.5 py-1.5 text-xs hover:bg-muted"
                              aria-label={`${t('discussionSession.downloadAttachment')} ${attachment.fileName}`}
                            >
                              <FileText class="h-3.5 w-3.5 shrink-0 opacity-70" />
                              <span class="min-w-0 truncate font-medium">{attachment.fileName}</span>
                              <span class="shrink-0 opacity-70">{formatBytes(attachment.sizeBytes)}</span>
                              {#if attachment.uploadedByName}
                                <span class="shrink-0 opacity-70">{attachment.uploadedByName}</span>
                              {/if}
                              <Download class="h-3.5 w-3.5 shrink-0 opacity-70" />
                            </a>
                          {/if}
                        {/each}
                      </div>
                    {/if}
                    {#if message.acknowledgements?.length}
                      <div class="mt-2 flex flex-wrap gap-1.5">
                        {#each message.acknowledgements as ack (ack.id)}
                          <span class="inline-flex items-center rounded border border-border bg-background/70 px-2 py-0.5 text-[10px] text-muted-foreground">
                            {ack.emoji} {ack.agentName} 已收到
                          </span>
                        {/each}
                      </div>
                    {/if}
                    {#if message.delivery === 'runtime_steer'}
                      <div class="mt-1.5 flex items-center gap-1 text-[10px] text-muted-foreground" data-runtime-steer-delivery>
                        <CornerDownRight class="h-3 w-3" aria-hidden="true" />
                        <span>{t('discussionSession.interjectDelivered')}</span>
                      </div>
                    {/if}
                    {#if message.retryPending}
                      <div class="mt-1 text-[10px] opacity-70">{t('discussionSession.retryPending')}</div>
                    {/if}
                    {#if message.canRequestRetry}
                      <div class={`mt-2 flex justify-end pt-2 ${own ? 'border-t border-border/60' : ''}`}>
                        <button
                          type="button"
                          class="inline-flex items-center opacity-50 hover:opacity-100"
                          disabled={busy || isRunning}
                          onclick={() => onRetryMessage?.(message.id, false)}
                          aria-label={t('discussionSession.retryMessage')}
                        >
                          <RotateCcw class="h-3.5 w-3.5" />
                        </button>
                      </div>
                    {/if}
                  </div>
                  {#if message.orchestrationWarning}
                    <div class="mt-1.5 flex max-w-full items-start gap-1.5 px-1 text-xs leading-relaxed text-amber-700" data-testid="discussion-orchestration-warning">
                      <ShieldAlert class="mt-0.5 h-3.5 w-3.5 shrink-0" />
                      <span>{message.orchestrationWarning}</span>
                    </div>
                  {/if}
                  <div class="mt-1 flex min-h-4 items-center gap-1 px-1 text-[10.5px] text-[#a0a0a0] opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100" class:flex-row-reverse={own}>
                    <span data-message-time>{message.createdAt}</span>
                    <button
                      type="button"
                      class="grid h-5 w-5 place-items-center rounded opacity-0 transition-opacity hover:bg-muted group-hover:opacity-100 focus-visible:opacity-100"
                      aria-label={copiedMessageId === message.id ? t('discussionSession.messageCopied') : t('discussionSession.copyMessage')}
                      title={copiedMessageId === message.id ? t('discussionSession.messageCopied') : t('discussionSession.copyMessage')}
                      data-testid={`discussion-message-copy-${message.id}`}
                      onclick={() => { void copyMessage(message) }}
                    >
                      {#if copiedMessageId === message.id}<Check class="h-3 w-3" />{:else}<Clipboard class="h-3 w-3" />{/if}
                    </button>
                  </div>
                </div>
                {#if own}
                  <span class="mt-0.5 h-8 w-8 shrink-0" data-message-avatar data-message-avatar-side="own">
                    <WorkforceAvatar
                      profileId={messageIdentity.profileId}
                      name={message.authorName || session.displayName || session.username || '?'}
                      human={messageIdentity.human}
                      class="h-8 w-8 rounded-full"
                      textClass="text-[11px]"
                    />
                  </span>
                {/if}
              </article>
            {:else if item.kind === 'run' && item.run}
              {@const run = item.run}
              {@const progress = runProgress(run)}
              {@const tokenUsage = runTokenUsage(run)}
              {@const expanded = workCardExpanded(run)}
              {#if toolTimelineItemCanGroup(item)}
                {@const stableTools = stableTimelineToolCalls(item.id, run.toolCalls)}
                <div class="w-full max-w-[46rem] pl-[2.625rem]">
                  {#if run.agentName || run.agentId}
                    <div class="mb-1 px-1 text-[11px] font-medium text-[#707070]" data-tool-agent-name>{run.agentName || run.agentId}</div>
                  {/if}
                  <DiscussionToolCallList
                    drawerId={item.id}
                    toolCalls={stableTools}
                    isStreaming={isActiveRunStatus(run.status)}
                    collapsible={run.toolCalls.length > 2}
                    defaultExpanded={index === renderedTimelineItems.length - 1}
                  />
                </div>
              {:else if runUsesProcessTranscript(run)}
                <article
                  class="w-full max-w-[46rem] pl-[2.625rem] text-sm text-foreground"
                  data-testid={`discussion-agent-work-card-${run.id}`}
                  data-discussion-process
                  aria-label={t('discussionSession.agentWorkCard')}
                >
                  <button
                    type="button"
                    class="group flex min-h-7 w-full items-center gap-2 rounded-md px-1 py-1 text-left text-xs text-muted-foreground transition hover:bg-muted/45 hover:text-foreground"
                    aria-expanded={expanded}
                    aria-label={expanded ? t('discussionSession.agentWorkCollapse') : t('discussionSession.agentWorkExpand')}
                    onclick={() => toggleWorkCard(run)}
                  >
                    <span
                      class={`grid h-5 w-5 shrink-0 place-items-center ${isFailedRunStatus(run.status) ? 'text-destructive' : isActiveRunStatus(run.status) ? 'text-brand' : 'text-muted-foreground'}`}
                      aria-hidden="true"
                    >
                      {#if isActiveRunStatus(run.status)}
                        <LoaderCircle class="h-3.5 w-3.5 animate-spin" />
                      {:else if isFailedRunStatus(run.status)}
                        <ShieldAlert class="h-3.5 w-3.5" />
                      {:else}
                        <CheckCircle2 class="h-3.5 w-3.5" />
                      {/if}
                    </span>
                    <span class="shrink-0 font-medium text-foreground/80" data-discussion-process-headline>{runProcessHeadline(run)}</span>
                    {#if !expanded || !run.thinking?.trim()}
                      <span class="min-w-0 flex-1 truncate text-muted-foreground" data-discussion-process-summary>{compactRunProcessSummary(run)}</span>
                    {:else}
                      <span class="min-w-0 flex-1"></span>
                    {/if}
                    {#if tokenUsage}
                      <span class="shrink-0 tabular-nums text-[11px] opacity-70" title={t('discussionSession.agentWorkTokenBreakdown', { input: formatTokenCount(tokenUsage.input), output: formatTokenCount(tokenUsage.output) })}>
                        {formatTokenCount(tokenUsage.total)} {t('discussionSession.agentWorkTokens')}
                      </span>
                    {/if}
                    {#if expanded}
                      <ChevronDown class="h-3.5 w-3.5 shrink-0 opacity-60" />
                    {:else}
                      <ChevronRight class="h-3.5 w-3.5 shrink-0 opacity-60" />
                    {/if}
                  </button>

                  {#if expanded}
                    <div class="ml-3 border-l border-border/80 pb-1 pl-4 pt-2.5" data-testid={`discussion-process-details-${run.id}`}>
                      {#if !isDirectDiscussion && (run.agentName || run.agentId || run.runtimeKind)}
                        <div class="mb-2 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[10.5px] text-muted-foreground" data-run-agent-context>
                          <span class="font-medium text-foreground/75">{run.agentName || run.agentId || t('discussionSession.agentWorkProcess')}</span>
                          {#if run.runtimeKind}<span>{run.runtimeKind}</span>{/if}
                          <span>{run.status}</span>
                          {#if run.planSteps?.length}<span>{planStepCountLabel(run)}</span>{/if}
                          {#if run.planSteps?.length}<span>{progress.completed}/{Math.max(progress.total, progress.completed)} · {progress.percent}%</span>{/if}
                        </div>
                      {/if}
                      {#if run.thinking?.trim()}
                        <section
                          class="space-y-1.5"
                          data-testid={`discussion-thinking-${run.id}`}
                          data-reasoning-summary
                          aria-live="polite"
                          aria-busy={isActiveRunStatus(run.status)}
                        >
                          <div class="flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground">
                            <Sparkles class={`h-3.5 w-3.5 ${isActiveRunStatus(run.status) ? 'text-brand' : ''}`} aria-hidden="true" />
                            <span>{t('discussionSession.agentWorkThinking')}</span>
                            {#if isApprovalWaitRunStatus(run.status)}
                              <span class="text-amber-700">{t('discussionSession.agentWorkAwaitingApproval')}</span>
                            {:else if isActiveRunStatus(run.status)}
                              <span class="text-brand">{t('discussionSession.agentWorkInProgress')}</span>
                            {/if}
                          </div>
                          <div class="max-w-[42rem] whitespace-pre-wrap break-words text-[13px] leading-5 text-foreground/80">
                            {run.thinking}{#if isActiveRunStatus(run.status) && !isApprovalWaitRunStatus(run.status)}<span class="ml-1 inline-block h-3 w-0.5 animate-pulse bg-brand align-[-1px]" data-reasoning-cursor aria-hidden="true"></span>{/if}
                          </div>
                        </section>
                      {/if}
                      {#if run.planSteps?.length}
                        <div class="mt-3 space-y-1.5">
                          {#each run.planSteps as step (step.id)}
                            <div class="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 text-xs" data-testid={`discussion-plan-step-${step.id}`}>
                              <span
                                class={`grid h-3.5 w-3.5 place-items-center ${planStepDone(step.status) ? 'text-status-done' : planStepFailed(step.status) ? 'text-destructive' : 'text-brand'}`}
                                aria-label={planStepStatusLabel(step.status)}
                                title={planStepStatusLabel(step.status)}
                              >
                                {#if planStepDone(step.status)}
                                  <CheckCircle2 class="h-3.5 w-3.5" />
                                {:else if planStepFailed(step.status)}
                                  <ShieldAlert class="h-3.5 w-3.5" />
                                {:else if planStepRunning(step.status)}
                                  <LoaderCircle class="h-3.5 w-3.5 animate-spin" />
                                {:else}
                                  <Circle class="h-3 w-3" />
                                {/if}
                              </span>
                              <span class="min-w-0 break-words text-foreground/85">{step.text}</span>
                              <span class="text-[10.5px] text-muted-foreground">{planStepStatusLabel(step.status)}</span>
                            </div>
                          {/each}
                        </div>
                      {/if}
                      {#if run.toolCalls.length > 0}
                        {@const stableProcessTools = stableTimelineToolCalls(`process:${run.id}`, run.toolCalls)}
                        <div class="mt-2" data-testid={`discussion-work-tools-${run.id}`}>
                          <DiscussionToolCallList
                            drawerId={`process:${run.id}`}
                            toolCalls={stableProcessTools}
                            isStreaming={isActiveRunStatus(run.status)}
                            collapsible={run.toolCalls.length > 2}
                            defaultExpanded={true}
                          />
                        </div>
                      {/if}
                      {#if tokenUsage}
                        <div class="mt-3 flex flex-wrap gap-x-3 gap-y-1 text-[10.5px] text-muted-foreground" data-run-token-usage>
                          <span>{t('discussionSession.agentWorkInputTokens')}: {formatTokenCount(tokenUsage.input)}</span>
                          <span>{t('discussionSession.agentWorkOutputTokens')}: {formatTokenCount(tokenUsage.output)}</span>
                          <span data-run-cached-input>{t('discussionSession.agentWorkCachedInputTokens')}: {tokenDetailLabel(tokenUsage.cachedInput, tokenUsage.cachedInputKnown)}</span>
                          <span data-run-reasoning-tokens>{t('discussionSession.agentWorkReasoningTokens')}: {tokenDetailLabel(tokenUsage.reasoning, tokenUsage.reasoningKnown)}</span>
                        </div>
                      {/if}
                      {#if !run.thinking?.trim() && !run.planSteps?.length && run.toolCalls.length === 0}
                        <div class="text-xs leading-5 text-muted-foreground">{runStatusLabel(run)}</div>
                      {/if}
                    </div>
                  {/if}
                  {#if hasDebugRuns}
                    <button
                      type="button"
                      class="ml-1 mt-1 inline-flex items-center gap-1 rounded px-1 py-1 text-[10.5px] text-muted-foreground opacity-70 transition hover:bg-muted hover:text-foreground hover:opacity-100"
                      onclick={() => openWorkProcess(run)}
                      aria-label={t('discussionSession.agentWorkViewProcess')}
                    >
                      <Bug class="h-3 w-3" />
                      <span>{t('discussionSession.agentWorkViewProcess')}</span>
                    </button>
                  {/if}
                </article>
              {/if}
            {:else if item.kind === 'approval' && item.approval}
              {@const approval = item.approval}
              {@const hasChoices = approval.choices.length > 0}
              {@const answer = answerAction(approval)}
              {@const payloadContent = approvalPayloadContent(approval)}
              {@const payloadFields = visibleApprovalFields(approval.payloadPreview.fields)}
              {@const showOtherInput = isOtherApprovalMode(approval)}
              {@const activeAction = showOtherInput ? activeApprovalAction : undefined}
              {@const pendingApproval = approvalIsPending(approval)}
              {@const explanationLines = approvalExplanationLines(approval)}
              {@const toolPurpose = approvalToolPurpose(approval)}
              {@const showApprovalDetails = pendingApproval}
              {@const compactText = approvalCompactText(approval)}
              {@const executionRunning = approvalExecutionRunning(approval)}
              {@const compactFailed = approvalCompactFailed(approval)}
              {#if !pendingApproval}
                <article
                  class="my-0.5 ml-[2.625rem] flex h-7 w-[calc(100%_-_2.625rem)] max-w-[43.375rem] items-center gap-1.5 px-1 text-xs"
                  data-testid={`discussion-approval-card-${approval.id}`}
                  data-approval-compact
                >
                  <span
                    class="discussion-tool-dot"
                    class:discussion-tool-dot-running={executionRunning}
                    class:discussion-tool-dot-failed={compactFailed}
                    class:discussion-tool-dot-done={!executionRunning && !compactFailed}
                  ></span>
                  <span
                    class="min-w-0 truncate font-medium text-muted-foreground"
                    class:discussion-tool-text-running={executionRunning}
                    class:text-destructive={compactFailed}
                    data-testid="discussion-approval-compact-action"
                  >{compactText}</span>
                  <span
                    class="ml-auto shrink-0 text-[11px] text-muted-foreground"
                    class:text-destructive={compactFailed}
                    data-testid="discussion-approval-compact-status"
                  >{approvalStatusText(approval.status)}</span>
                </article>
              {:else}
              <article
                class={`ml-[2.625rem] w-[calc(100%_-_2.625rem)] max-w-[43.375rem] border-l-2 py-1.5 pl-4 pr-1 text-sm text-foreground ${approvalCardClass(approval.displayKind)}`}
                data-testid={`discussion-approval-card-${approval.id}`}
              >
                <div class="flex items-start gap-2.5">
                  <span class="grid h-6 w-6 shrink-0 place-items-center text-muted-foreground">
                    {#if approval.displayKind === 'command_execution'}
                      <SquareTerminal class="h-3.5 w-3.5" />
                    {:else if approval.displayKind === 'file_change'}
                      <FileDiff class="h-3.5 w-3.5" />
                    {:else if approval.displayKind === 'user_input'}
                      <HelpCircle class="h-3.5 w-3.5" />
                    {:else if approval.displayKind === 'network_access'}
                      <Network class="h-3.5 w-3.5" />
                    {:else if approval.displayKind === 'tool_use'}
                      <Wrench class="h-3.5 w-3.5" />
                    {:else}
                      <ShieldAlert class="h-3.5 w-3.5" />
                    {/if}
                  </span>
                  <div class="min-w-0 flex-1">
                    <div class="flex min-w-0 flex-wrap items-center gap-1.5">
                      <h2 class="min-w-0 break-words text-sm font-semibold text-foreground">{t('discussionSession.approvalRequired')}</h2>
                      <span class="text-xs font-medium text-foreground/80">{approvalToolLabel(approval)}</span>
                      <span class="text-[10px] leading-none text-muted-foreground" data-testid="discussion-approval-operation-type">{approvalOperationTypeText(approval)}</span>
                      {#if approval.priority === 'high' || approval.priority === 'critical'}
                        <span class="text-[10px] font-medium leading-none text-amber-700">{approvalPriorityText(approval.priority)}</span>
                      {/if}
                    </div>
                    {#if toolPurpose}
                      <p class="mt-1 whitespace-pre-wrap break-words text-xs leading-5 text-foreground/80" data-testid="discussion-approval-purpose">{toolPurpose}</p>
                    {/if}
                    {#if showApprovalDetails}
                      {#if explanationLines.length}
                        <div class="mt-1.5 space-y-1 text-xs leading-5 text-muted-foreground">
                          {#each explanationLines as line}
                            <p class="whitespace-pre-wrap break-words">{line}</p>
                          {/each}
                        </div>
                      {/if}
                      {#if payloadContent || payloadFields.length}
                        <div class="mt-2 space-y-1.5">
                          {#if payloadContent}
                            <pre class="subtle-scrollbar max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/55 px-3 py-2.5 font-mono text-xs leading-5 text-foreground">{payloadContent}</pre>
                          {/if}
                          {#if payloadFields.length}
                            <dl class="divide-y divide-border/70 text-xs">
                              {#each payloadFields as field}
                                <div class="grid grid-cols-[auto_minmax(0,1fr)] gap-2 py-1.5 first:pt-0 last:pb-0">
                                  <dt class="text-muted-foreground">{approvalFieldLabel(field.key)}</dt>
                                  <dd class={`break-words text-foreground ${approvalFieldValueClass(field.key)}`}>{field.value}</dd>
                                </div>
                              {/each}
                            </dl>
                          {/if}
                        </div>
                      {/if}
                      {#if pendingApproval && approval.actions.length > 0}
                        <div class="mt-3 space-y-2.5" data-testid="discussion-inline-approval-panel">
                          {#if hasChoices}
                            <div class="space-y-2" data-testid="discussion-choice-approval-prompt">
                              <div class="text-xs font-medium text-muted-foreground">{t('approvals.chooseOption')}</div>
                              <div class="grid gap-2">
                                {#each approval.choices as choice (choice.id)}
                                  <button
                                    type="button"
                                    aria-label={choice.label}
                                    class="rounded-md border border-border bg-background px-3 py-2 text-left hover:bg-muted disabled:opacity-50"
                                    disabled={approvalIsBusy(approval)}
                                    onclick={() => submitChoice(approval, answer, choice.id)}
                                  >
                                    <span class="block text-sm font-medium text-foreground">{choice.label}</span>
                                    {#if choice.description}
                                      <span class="mt-0.5 block text-xs text-muted-foreground">{choice.description}</span>
                                    {/if}
                                  </button>
                                {/each}
                              </div>
                            </div>
                          {:else if !showOtherInput}
                            <div class="flex flex-wrap gap-2" data-testid="discussion-confirmation-approval-prompt">
                              {#each approval.actions as action (action.id)}
                                <Button
                                  size="sm"
                                  variant={approvalActionVariant(action)}
                                  disabled={approvalIsBusy(approval)}
                                  onclick={() => submitInlineAction(approval, action)}
                                >
                                  {approvalActionLabel(action, t)}
                                </Button>
                              {/each}
                            </div>
                          {/if}

                          {#if showOtherInput}
                            <div class="space-y-2">
                              <label class="text-xs font-medium text-muted-foreground" for={`discussion-approval-other-${approval.id}`}>{inlineApprovalInputLabel(activeAction)}</label>
                              <textarea
                                id={`discussion-approval-other-${approval.id}`}
                                class="min-h-20 w-full rounded-md border border-border bg-input-background px-3 py-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/20"
                                value={otherApprovalInput}
                                oninput={(event) => (otherApprovalInput = event.currentTarget.value)}
                              ></textarea>
                              <div class="flex justify-end gap-2">
                                <Button size="sm" variant="outline" onclick={closeInlineApproval}>{t('approvals.cancelOther')}</Button>
                                <Button size="sm" disabled={approvalIsBusy(approval) || !otherApprovalInput.trim() || !activeAction} onclick={() => submitOtherApproval(approval, activeAction)}>
                                  {activeAction && !hasChoices ? approvalActionLabel(activeAction, t) : t('approvals.submitOtherResponse')}
                                </Button>
                              </div>
                            </div>
                          {/if}

                          {#if hasChoices && !showOtherInput}
                            <div class="flex justify-end">
                              <Button size="sm" variant="outline" disabled={approvalIsBusy(approval) || !answer} onclick={() => openOtherApproval(approval, answer)}>{t('approvals.other')}</Button>
                            </div>
                          {/if}
                          </div>
                        {/if}
                    {/if}
                  </div>
                </div>
              </article>
              {/if}
            {/if}
          </div>
          {/if}
        {:else}
          {#if assistantMode}
            <div class="grid h-full min-h-64 place-items-center px-4 py-10" data-personal-assistant-empty-state>
              <div class="w-full max-w-xl text-center">
                {#if assistantAvatarUrl}
                  <img class="mx-auto h-12 w-12 rounded-full object-cover" src={assistantAvatarUrl} alt={panelTitle} />
                {:else}
                  <div class="mx-auto grid h-12 w-12 place-items-center rounded-full bg-emerald-100 text-emerald-700">
                    <Bot class="h-5 w-5" />
                  </div>
                {/if}
                <h2 class="mt-4 text-base font-semibold text-foreground">{t('discussionSession.personalAssistantWelcome')}</h2>
                <div class="mt-5 grid gap-2 sm:grid-cols-3">
                  <button
                    type="button"
                    class="flex min-h-16 items-center gap-2 rounded-lg border border-[#e4e4e4] bg-white px-3 py-2 text-left text-xs font-medium text-foreground transition-colors hover:border-emerald-200 hover:bg-emerald-50/50"
                    onclick={() => useAssistantPrompt(t('discussionSession.personalAssistantPromptToday'))}
                  >
                    <ListTodo class="h-4 w-4 shrink-0 text-emerald-600" />
                    <span>{t('discussionSession.personalAssistantPromptToday')}</span>
                  </button>
                  <button
                    type="button"
                    class="flex min-h-16 items-center gap-2 rounded-lg border border-[#e4e4e4] bg-white px-3 py-2 text-left text-xs font-medium text-foreground transition-colors hover:border-emerald-200 hover:bg-emerald-50/50"
                    onclick={() => useAssistantPrompt(t('discussionSession.personalAssistantPromptImmediate'))}
                  >
                    <Zap class="h-4 w-4 shrink-0 text-amber-500" />
                    <span>{t('discussionSession.personalAssistantPromptImmediate')}</span>
                  </button>
                  <button
                    type="button"
                    class="flex min-h-16 items-center gap-2 rounded-lg border border-[#e4e4e4] bg-white px-3 py-2 text-left text-xs font-medium text-foreground transition-colors hover:border-emerald-200 hover:bg-emerald-50/50"
                    onclick={() => useAssistantPrompt(t('discussionSession.personalAssistantPromptScheduled'))}
                  >
                    <CalendarClock class="h-4 w-4 shrink-0 text-sky-600" />
                    <span>{t('discussionSession.personalAssistantPromptScheduled')}</span>
                  </button>
                </div>
              </div>
            </div>
          {:else}
            <div class="grid h-full min-h-64 place-items-center text-sm text-muted-foreground">{t('discussionSession.empty')}</div>
          {/if}
        {/each}
        {#if isRunning || busy}
          <div class="text-xs text-muted-foreground">{respondingLabel}</div>
        {/if}
        {#if isFailed}
          <div class="flex flex-wrap items-center gap-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
            <span>{failedMessage}</span>
            {#if failedRuntimeSetupError}
              <a class="font-medium underline underline-offset-2 hover:text-destructive" href="/settings">{t('discussionSession.openRuntimeSettings')}</a>
            {/if}
          </div>
        {/if}
        {#if visibleErrorMessage}
          <div class="flex flex-wrap items-center gap-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
            <span>{visibleErrorMessage}</span>
            {#if visibleRuntimeSetupError}
              <a class="font-medium underline underline-offset-2 hover:text-destructive" href="/settings">{t('discussionSession.openRuntimeSettings')}</a>
            {/if}
          </div>
        {/if}
      </div>
    </div>

    <footer class="shrink-0 bg-background px-5 pb-5 pt-2 sm:px-8">
      {#if pendingInterjection}
        <div class="mx-auto mb-2 flex max-w-[50rem] items-start gap-2 rounded-md border border-blue-100 bg-blue-50/60 px-3 py-2 text-xs text-blue-900" data-pending-interjection aria-live="polite">
          <LoaderCircle class="mt-0.5 h-3.5 w-3.5 shrink-0 animate-spin text-blue-600" aria-hidden="true" />
          <div class="min-w-0 flex-1">
            <div class="font-medium">{t('discussionSession.interjectPending')}</div>
            <div class="mt-0.5 line-clamp-2 whitespace-pre-wrap break-words text-blue-900/70">{pendingInterjection.content}</div>
          </div>
        </div>
      {/if}
      {#if pendingAttachments.length > 0}
        <div class="mx-auto mb-2 flex max-w-[50rem] flex-wrap gap-2" aria-label={t('discussionSession.attachments')}>
          {#each pendingAttachments as attachment (attachment.id)}
            {#if isAudioAttachment(attachment)}
              <div class="flex w-64 max-w-full items-center gap-2 rounded-md border border-border bg-muted/30 px-2 py-1 text-foreground" data-pending-audio-attachment={attachment.id}>
                <Mic class="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                <audio class="h-8 min-w-0 flex-1" controls preload="metadata" src={attachment.downloadUrl} aria-label={`${t('discussionSession.playVoiceMessage')} ${attachment.fileName}`}></audio>
                <a href={attachment.downloadUrl} download class="grid h-7 w-7 shrink-0 place-items-center rounded hover:bg-muted" aria-label={`${t('discussionSession.downloadAttachment')} ${attachment.fileName}`}>
                  <Download class="h-3.5 w-3.5 text-muted-foreground" />
                </a>
              </div>
            {:else if isPreviewableAttachment(attachment)}
              <div class="inline-flex min-w-0 max-w-80 items-center rounded-md border border-border bg-background text-xs" data-preview-attachment={attachment.id}>
                <a
                  href={attachment.previewUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  class="flex min-w-0 items-center gap-2 px-2.5 py-1.5"
                  aria-label={`${t('discussionSession.previewAttachment')} ${attachment.fileName}`}
                >
                  <FileText class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                  <span class="min-w-0 truncate font-medium">{attachment.fileName}</span>
                  <span class="shrink-0 text-muted-foreground">{formatBytes(attachment.sizeBytes)}</span>
                  <ExternalLink class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                </a>
                <a href={attachment.downloadUrl} download class="grid h-7 w-7 shrink-0 place-items-center border-l border-border" aria-label={`${t('discussionSession.downloadAttachment')} ${attachment.fileName}`}>
                  <Download class="h-3.5 w-3.5 text-muted-foreground" />
                </a>
              </div>
            {:else}
              <a
                href={attachment.downloadUrl}
                download
                class="inline-flex min-w-0 max-w-72 items-center gap-2 rounded-md border border-border bg-muted/30 px-2.5 py-1.5 text-xs text-foreground hover:bg-muted"
                aria-label={`${t('discussionSession.downloadAttachment')} ${attachment.fileName}`}
              >
                <FileText class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                <span class="min-w-0 truncate font-medium">{attachment.fileName}</span>
                <span class="shrink-0 text-muted-foreground">{formatBytes(attachment.sizeBytes)}</span>
                {#if attachment.uploadedByName}
                  <span class="shrink-0 text-muted-foreground">{attachment.uploadedByName}</span>
                {/if}
                <Download class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              </a>
            {/if}
          {/each}
        </div>
      {/if}
      {#if attachmentError}
        <div class="mb-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">{attachmentError}</div>
      {/if}
      {#if voiceError}
        <div class="mb-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">{voiceError}</div>
      {/if}
      {#if mentionQuery !== undefined && mentionCandidates.length > 0}
        <div class="mb-2 max-w-xs rounded-md border border-border bg-card p-1 shadow-md">
          {#each mentionCandidates as member}
            <button
              type="button"
              class="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm text-foreground hover:bg-muted"
              aria-label={`Mention ${member.name}`}
              onclick={() => insertMention(member)}
            >
              <WorkforceAvatar
                profileId={avatarIdentityById[member.id]?.profileId || member.id}
                name={member.name}
                human={avatarIdentityById[member.id]?.human ?? ['human', 'user'].includes(member.kind.toLowerCase())}
                class="h-5 w-5 rounded-full"
                textClass="text-[8px]"
              />
              <span class="min-w-0 flex-1 truncate font-medium">{member.name}</span>
              <span class="shrink-0 truncate text-[10px] text-muted-foreground">{member.role}</span>
            </button>
          {/each}
        </div>
      {/if}
      <form
        class="mx-auto max-w-[50rem] rounded-2xl border bg-[#f7f7f7] px-3 pb-2.5 pt-3 shadow-[0_8px_30px_rgba(0,0,0,0.06)] transition-[border-color,box-shadow,background-color] duration-200 dark:bg-card {missionComposerActive ? 'border-blue-300 bg-blue-50/20 shadow-blue-100/60 focus-within:border-blue-400' : 'border-[#dedede] focus-within:border-[#c6c6c6] focus-within:shadow-[0_10px_36px_rgba(0,0,0,0.09)] dark:border-border dark:focus-within:border-[#3a3a3a]'}"
        data-discussion-composer
        data-composer-mode={missionComposerActive ? 'mission' : 'message'}
        onsubmit={(event) => { event.preventDefault(); void submitComposer() }}
      >
        <label class="sr-only" for="discussion-message">{t('discussionSession.messageInput')}</label>
        <label class="sr-only" for="discussion-attachment-file">{t('discussionSession.uploadAttachmentFile')}</label>
        <input
          id="discussion-attachment-file"
          bind:this={attachmentInput}
          type="file"
          class="sr-only"
          aria-label={t('discussionSession.uploadAttachmentFile')}
          onchange={onAttachmentChange}
        />
        <textarea
          id="discussion-message"
          bind:this={composerInput}
          value={draft}
          rows="2"
          onfocus={onComposerFocus}
          oninput={(event) => onDraft?.(event.currentTarget.value)}
          onkeydown={handleComposerKeydown}
          aria-label={missionComposerActive ? t('discussionSession.missionPromptInput') : t('discussionSession.messageInput')}
          placeholder={missionComposerActive ? t('discussionSession.missionPromptPlaceholder') : isRunning || busy ? t('discussionSession.interjectPlaceholder') : assistantMode ? t('discussionSession.personalAssistantPlaceholder') : t('discussionSession.placeholder')}
          class="max-h-40 min-h-14 w-full resize-none bg-transparent px-1.5 py-1 text-[14px] leading-6 text-black outline-none placeholder:text-[#929292] dark:text-foreground"
        ></textarea>
        <div class="mt-1 flex min-w-0 items-end justify-between gap-2">
          <div class="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
            <Button variant="ghost" size="icon" disabled={!attachmentUploadEnabled || attachmentUploading || isRunning || busy} onclick={chooseAttachment} ariaLabel={t('discussionSession.uploadAttachment')} class="h-8 w-8 shrink-0 rounded-full text-[#777] hover:bg-[#f0f0f0]">
              <Paperclip class="h-4 w-4" />
            </Button>
            {#if discussion?.id}
              <label
                class="relative inline-flex h-8 shrink-0 items-center rounded-md px-2 text-[11.5px] font-medium text-[#666] transition-colors hover:bg-[#f1f1f1] dark:text-muted-foreground dark:hover:bg-muted"
                data-discussion-hil-mode
                aria-busy={hilModeBusy}
                title={hilModeEditable ? t('discussionSession.hilMode') : t('discussionSession.hilModeOwnerOnly')}
              >
                <span class="shrink-0">HIL</span>
                <select
                  class="h-7 appearance-none bg-transparent pl-1.5 pr-4 text-[11.5px] font-medium text-[#555] outline-none disabled:cursor-not-allowed disabled:opacity-50 dark:text-foreground"
                  value={discussion.hilMode ?? 'manual'}
                  disabled={hilModeBusy || !hilModeEditable}
                  aria-label={t('discussionSession.hilMode')}
                  onchange={(event) => onHILModeChange?.(event.currentTarget.value as DiscussionHILMode)}
                >
                  <option value="auto">{t('discussionSession.hilModeAuto')}</option>
                  <option value="manual">{t('discussionSession.hilModeManual')}</option>
                  <option value="none">{t('discussionSession.hilModeNone')}</option>
                </select>
                <ChevronDown class="pointer-events-none absolute right-1.5 h-3 w-3 text-[#888]" />
              </label>
            {/if}
            {#if onNewSession}
              <button
                type="button"
                onclick={onNewSession}
                aria-label={t('discussionSession.newAgentSession')}
                title={t('discussionSession.newAgentSession')}
                class="inline-flex h-8 shrink-0 items-center rounded-full border border-transparent px-2.5 text-[11.5px] font-medium text-[#666] transition-[background-color,color,transform] hover:bg-[#f1f1f1] hover:text-black active:scale-95"
              >
                {t('discussionSession.newAgentSessionShort')}
              </button>
            {/if}
            {#if onMissionStart}
              <button
                type="button"
                class="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-full border px-2.5 text-[11.5px] font-medium transition-[background-color,border-color,color,transform] duration-200 active:scale-95 disabled:pointer-events-none disabled:opacity-45 {missionComposerActive ? 'border-blue-200 bg-blue-100 text-blue-700' : 'border-transparent text-[#666] hover:bg-[#f1f1f1] hover:text-black'}"
                disabled={busy || missionModeBlockedByAttachments || voiceRecording || voiceStarting || voiceProcessing}
                aria-pressed={missionComposerActive}
                aria-label={t('discussionSession.toggleMissionMode')}
                title={missionModeBlockedByAttachments ? t('discussionSession.missionAttachmentsPending') : t('discussionSession.toggleMissionMode')}
                onclick={toggleMissionComposer}
              >
                <Target class="h-3.5 w-3.5" />
                Mission
              </button>
            {/if}
            {#if missionComposerActive}
              <div class="flex min-w-0 flex-wrap items-center gap-1.5 animate-in fade-in-0 slide-in-from-left-1 duration-200" data-mission-composer-options>
                <label class="relative inline-flex h-8 shrink-0 items-center gap-1 rounded-full border border-blue-100 bg-white pl-2 pr-1 text-[11px] text-[#666]">
                  <CalendarClock class="h-3.5 w-3.5 text-blue-600" />
                  <span class="sr-only">{t('discussionSession.missionLaunchMode')}</span>
                  <select
                    class="h-7 appearance-none bg-transparent pl-0.5 pr-4 text-[11px] font-medium text-[#555] outline-none"
                    value={missionLaunchMode}
                    aria-label={t('discussionSession.missionLaunchMode')}
                    onchange={(event) => {
                      missionLaunchMode = event.currentTarget.value as 'immediate' | 'scheduled'
                      if (missionLaunchMode === 'immediate') missionScheduleAt = ''
                    }}
                  >
                    <option value="immediate">{t('discussionSession.missionImmediate')}</option>
                    <option value="scheduled">{t('discussionSession.missionScheduled')}</option>
                  </select>
                  <ChevronDown class="pointer-events-none absolute right-1.5 h-3 w-3 text-[#888]" />
                </label>
                {#if missionLaunchMode === 'scheduled'}
                  <input
                    type="datetime-local"
                    class="h-8 w-40 rounded-full border border-blue-100 bg-white px-2.5 text-[10.5px] text-[#555] outline-none animate-in fade-in-0 duration-200 focus:border-blue-300"
                    bind:value={missionScheduleAt}
                    aria-label={t('discussionSession.missionScheduleAt')}
                  />
                {/if}
              </div>
            {/if}
          </div>
          <div class="flex shrink-0 items-center gap-1">
            {#if voiceRecording}
              <span class="w-10 text-center font-mono text-[10px] tabular-nums text-rose-600" data-voice-recording-duration aria-live="polite">{formatVoiceDuration(voiceElapsedSeconds)}</span>
              <Button variant="ghost" size="icon" onclick={cancelVoiceRecording} ariaLabel={t('discussionSession.cancelVoiceRecording')} title={t('discussionSession.cancelVoiceRecording')} class="h-8 w-8 shrink-0 rounded-full text-[#777] hover:bg-[#f0f0f0]">
                <X class="h-4 w-4" />
              </Button>
            {/if}
            <Button
              variant="ghost"
              size="icon"
              disabled={voiceButtonDisabled}
              onclick={() => { void toggleVoiceRecording() }}
              ariaLabel={voiceButtonLabel}
              title={voiceButtonLabel}
              class="h-8 w-8 shrink-0 rounded-full transition-[background-color,color,transform] active:scale-95 disabled:opacity-25 {voiceRecording ? 'bg-rose-50 text-rose-600 hover:bg-rose-100' : 'text-[#777] hover:bg-[#f0f0f0]'}"
            >
              {#if voiceStarting || voiceProcessing}
                <LoaderCircle class="h-4 w-4 animate-spin" />
              {:else if voiceRecording}
                <Square class="h-3.5 w-3.5 fill-current" />
              {:else}
                <Mic class="h-4 w-4" />
              {/if}
            </Button>
            {#if (isRunning || busy) && draft.trim().length === 0}
              <Button size="icon" disabled={abortBusy || !discussion?.id} onclick={onAbort} ariaLabel={t('discussionSession.abortDiscussion')} title={t('discussionSession.abortDiscussion')} class="h-8 w-8 shrink-0 rounded-full bg-black text-white transition-opacity disabled:opacity-25">
                <Square class="h-3.5 w-3.5" />
              </Button>
            {:else}
              <Button size="icon" disabled={composerSubmitDisabled} onclick={() => { void submitComposer() }} ariaLabel={missionComposerActive ? t('discussionSession.startMission') : isRunning || busy ? t('discussionSession.interject') : t('discussionSession.send')} title={missionComposerActive ? t('discussionSession.startMission') : isRunning || busy ? t('discussionSession.interject') : t('discussionSession.send')} class="h-8 w-8 shrink-0 rounded-full bg-black text-white transition-[opacity,transform] hover:scale-105 active:scale-95 disabled:opacity-25">
                {#if interjectBusy || (missionComposerActive && busy)}
                  <LoaderCircle class="h-4 w-4 animate-spin" />
                {:else}
                  <ArrowUp class="h-4 w-4" />
                {/if}
              </Button>
            {/if}
          </div>
        </div>
      </form>
    </footer>
  </div>

  {#if !isDirectDiscussion && !taskTimelineCollapsed}
    <aside class="right-sidebar-shell right-sidebar-rail absolute inset-y-0 right-0 z-20 shadow-xl md:static md:z-auto md:shadow-none" data-discussion-task-panel>
      <div class="right-sidebar-header">
        <div class="flex min-w-0 items-center gap-1.5">
          <Target class="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          <h2 class="truncate text-xs font-semibold text-foreground">{primaryMission?.title || t('discussionSession.taskTimeline')}</h2>
        </div>
        <button type="button" class="text-muted-foreground hover:text-foreground" onclick={onTaskTimelineToggle} aria-label={t('sidebar.collapse')}>
          <ChevronRight class="h-4 w-4" />
        </button>
      </div>

      <div class="right-sidebar-body right-sidebar-scroll subtle-scrollbar p-3">
        {#if visibleMissionContextError}
          <div class="mb-4 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">{visibleMissionContextError}</div>
        {:else if missionContextLoading}
          <p class="mb-4 text-xs text-muted-foreground">{t('discussionSession.loading')}</p>
        {/if}

        <div class="space-y-5">
          <section>
            <h3 class="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><Target class="h-3 w-3" />{t('workspace.goals')}</h3>
            <ul class="space-y-1.5">
              {#each contextGoals as goal}
                <li class="flex items-start gap-2 text-xs text-foreground"><span class="mt-1.5 h-1 w-1 shrink-0 rounded-full bg-brand"></span>{goal}</li>
              {/each}
            </ul>
          </section>

          <section class="border-t border-border pt-4" data-mission-runtime-history>
            <h3 class="mb-3 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <SquareTerminal class="h-3 w-3" />
              <span>{t('discussionSession.executionHistory')}</span>
              <span class="ml-auto tabular-nums">{missionRuntimeSessions.length}</span>
            </h3>
            {#if visibleMissionOpsError}
              <p class="text-xs text-destructive">{visibleMissionOpsError}</p>
            {:else if missionOpsLoading}
              <p class="text-xs text-muted-foreground">{t('discussionSession.loading')}</p>
            {:else}
              <div class="space-y-2">
                {#each missionRuntimeSessions as runtime (runtime.runId)}
                  <div class="border-l border-border pl-2.5 text-xs" data-mission-runtime-run={runtime.runId} data-agent-id={runtime.agentId} data-task-id={runtime.taskId}>
                    <div class="flex min-w-0 items-center gap-1.5">
                      <span class={`h-1.5 w-1.5 shrink-0 rounded-full ${missionRuntimeStatusDotClass(runtime.status)}`}></span>
                      <span class="min-w-0 flex-1 truncate font-medium text-foreground">{missionRuntimeAgentName(runtime)}</span>
                      <span class="shrink-0 text-[10px] text-muted-foreground">{runtime.status}</span>
                    </div>
                    <div class="mt-0.5 truncate text-[10px] text-muted-foreground" title={missionRuntimeTaskTitle(runtime)}>{missionRuntimeTaskTitle(runtime)}</div>
                    {#if runtime.errorSummary || runtime.lastEvent}
                      <div class={`mt-1 line-clamp-3 break-words text-[10px] ${runtime.errorSummary ? 'text-destructive' : 'text-muted-foreground'}`}>{runtime.errorSummary || runtime.lastEvent}</div>
                    {/if}
                    <div class="mt-1 truncate font-mono text-[9px] text-muted-foreground/75" title={runtime.runId}>{runtime.runId}</div>
                  </div>
                {:else}
                  <p class="text-xs text-muted-foreground">{t('discussionSession.noExecutionHistory')}</p>
                {/each}
              </div>
            {/if}
          </section>

          <section class="border-t border-border pt-4">
            <h3 class="mb-3 flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><CheckCircle2 class="h-3 w-3" />{t('discussionSession.taskTimeline')}</h3>
            <div class="space-y-0">
              {#each taskTimeline as task, index}
                {@const state = taskStepState(task)}
                <div
                  class="relative grid grid-cols-[18px_minmax(0,1fr)] gap-2 pb-3 text-xs"
                  data-testid={`discussion-task-step-${task.id}`}
                  data-step-state={state}
                >
                  {#if index < taskTimeline.length - 1}
                    <span class="absolute left-[8px] top-5 h-[calc(100%-0.25rem)] w-px bg-border"></span>
                  {/if}
                  <span
                    class="relative z-10 mt-0.5 grid h-4 w-4 shrink-0 place-items-center rounded-full border bg-background"
                    class:border-status-done={state === 'done'}
                    class:bg-status-done={state === 'done'}
                    class:text-white={state === 'done'}
                    class:border-brand={state === 'current'}
                    class:bg-brand={state === 'current'}
                    class:text-brand-foreground={state === 'current'}
                    class:border-muted-foreground={state === 'pending'}
                    class:text-muted-foreground={state === 'pending'}
                  >
                    {#if state === 'done'}
                      <CheckCircle2 class="h-3 w-3" />
                    {:else if state === 'current'}
                      <LoaderCircle class="h-3 w-3" />
                    {:else}
                      <Circle class="h-2 w-2 fill-current" />
                    {/if}
                  </span>
                  <div class={`min-w-0 rounded-md border px-2 py-1.5 ${state === 'current' ? 'border-brand/40 bg-brand/10' : 'border-border bg-muted/20'}`}>
                    <div class="flex items-center gap-2">
                      <span class="min-w-0 truncate font-medium text-foreground">{task.title}</span>
                      <span class="ml-auto shrink-0 text-[10px] text-muted-foreground">{task.status}</span>
                    </div>
                    <div class="mt-0.5 flex min-w-0 items-center gap-2 text-[10px] text-muted-foreground">
                      <span class="truncate">{task.assigneeName}</span>
                      {#if state === 'current'}
                        <span class="shrink-0 text-brand">{t('discussionSession.currentTask')}</span>
                      {/if}
                    </div>
                    {#if task.blockedReason}
                      <div class="mt-1 text-[10px] text-destructive">{task.blockedReason}</div>
                    {/if}
                  </div>
                </div>
              {:else}
                <p class="text-xs text-muted-foreground">{t('discussionSession.noTasksYet')}</p>
              {/each}
            </div>
          </section>

          <section class="border-t border-border pt-4">
            <h3 class="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <Users class="h-3 w-3" />
              {teamMembersLabel} ({teamMembers.length})
            </h3>
            <div class="space-y-1.5">
              {#each teamMembers as member}
                <div class="flex items-center gap-2">
                  <WorkforceAvatar
                    profileId={avatarIdentityById[member.id]?.profileId || member.id}
                    name={member.name}
                    human={avatarIdentityById[member.id]?.human ?? ['human', 'user'].includes(member.kind.toLowerCase())}
                    class="h-6 w-6 rounded-full"
                    textClass="text-[8px]"
                  />
                  <span class="truncate text-xs font-medium text-foreground">{member.name}</span>
                  <span class="ml-auto truncate text-[10px] text-muted-foreground">{member.role}</span>
                </div>
              {/each}
            </div>
          </section>
        </div>
      </div>
    </aside>
  {/if}

  {#if debugOpen}
    <aside
      class="right-sidebar-shell right-sidebar-drawer fixed inset-y-0 right-0 z-40 max-w-xl"
      aria-label={t('discussionSession.agentWorkPanel')}
    >
      <header class="right-sidebar-header right-sidebar-header-comfortable">
        <div class="min-w-0">
          <h2 class="truncate text-sm font-semibold text-foreground">{t('discussionSession.agentWorkPanel')}</h2>
          <p class="text-xs text-muted-foreground">{debug ? `${debug.runId} - ${debug.createdAt}` : t('discussionSession.debugLoading')}</p>
        </div>
        <Button variant="ghost" size="icon" onclick={onDebugClose} ariaLabel={t('discussionSession.debugClose')}>
          <X class="h-4 w-4" />
        </Button>
      </header>
      <div class="right-sidebar-body right-sidebar-scroll subtle-scrollbar space-y-4 p-4 text-sm">
        {#if debugRuns.length > 0}
          <section class="space-y-2">
            <h3 class="text-xs font-semibold uppercase text-muted-foreground">{t('discussionSession.debugRunTimeline')}</h3>
            <div class="space-y-1">
              {#each debugRuns as run}
                {@const state = runStepState(run)}
                {@const selected = run.id === (debug?.runId || selectedDebugRunId)}
                <button
                  type="button"
                  class={`grid w-full grid-cols-[14px_minmax(0,1fr)] items-start gap-2 rounded-md border px-2 py-2 text-left text-xs ${selected ? 'border-brand/50 bg-brand/10' : 'border-border bg-muted/20 hover:bg-muted/40'}`}
                  data-testid={`discussion-debug-run-${run.id}`}
                  data-run-state={state}
                  onclick={() => onDebugRunSelect?.(run.id)}
                >
                  <span
                    class={`mt-1 h-2.5 w-2.5 rounded-full ${state === 'done' ? 'bg-status-done' : state === 'current' ? 'bg-brand' : state === 'failed' ? 'bg-destructive' : 'bg-muted-foreground/40'}`}
                  ></span>
                  <span class="min-w-0">
                    <span class="flex items-center gap-2">
                      <span class="truncate font-medium text-foreground">{run.agentName}</span>
                      <span class="ml-auto shrink-0 text-[10px] text-muted-foreground">{run.status}</span>
                    </span>
                    <span class="mt-0.5 block truncate text-[10px] text-muted-foreground">{run.createdAt} / {run.runtimeKind || t('discussionSession.ready')}</span>
                    {#if run.error}
                      <span class="mt-1 line-clamp-2 block text-[10px] text-destructive">{run.error}</span>
                    {/if}
                  </span>
                </button>
              {/each}
            </div>
          </section>
        {/if}

        {#if selectedProcessRun}
          {#if selectedProcessRun.thinking?.trim()}
            <section class="space-y-2">
              <h3 class="text-xs font-semibold uppercase text-muted-foreground">{t('discussionSession.agentWorkThinking')}</h3>
              <div class="subtle-scrollbar max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/30 p-3 text-xs leading-5 text-foreground" data-testid="discussion-debug-thinking">{selectedProcessRun.thinking}</div>
            </section>
          {/if}

          {#if selectedProcessRun.planSteps?.length}
            <section class="space-y-2" data-testid="discussion-debug-tasks">
              <h3 class="text-xs font-semibold uppercase text-muted-foreground">{planSectionLabel(selectedProcessRun)}</h3>
              <div class="space-y-1.5">
                {#each selectedProcessRun.planSteps as step (step.id)}
                  <div class="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 rounded border border-border bg-muted/25 px-2 py-1.5 text-xs">
                    <span class={`h-2 w-2 rounded-full ${planStepDone(step.status) ? 'bg-status-done' : planStepFailed(step.status) ? 'bg-destructive' : 'bg-brand'}`}></span>
                    <span class="min-w-0 break-words text-foreground">{step.text}</span>
                    <span class="text-muted-foreground">{step.status}</span>
                  </div>
                {/each}
              </div>
            </section>
          {/if}

          <section class="space-y-2">
            <h3 class="text-xs font-semibold uppercase text-muted-foreground">{t('discussionSession.agentWorkToolCalls')}</h3>
            {#if selectedProcessRun.toolCalls.length > 0}
              {@const stableDebugTools = stableTimelineToolCalls(`debug:${selectedProcessRun.id}`, selectedProcessRun.toolCalls)}
              <DiscussionToolCallList
                drawerId={`debug:${selectedProcessRun.id}`}
                toolCalls={stableDebugTools}
                isStreaming={isActiveRunStatus(selectedProcessRun.status)}
                collapsible={selectedProcessRun.toolCalls.length > 2}
                defaultExpanded={true}
              />
            {:else}
              <p class="rounded-md border border-border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">{t('discussionSession.agentWorkNoTools')}</p>
            {/if}
          </section>
        {/if}

        {#if debugLoading}
          <p class="text-muted-foreground">{t('discussionSession.debugLoading')}</p>
        {:else if debugError}
          <div class="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">{debugError}</div>
        {:else if debug}
          <section class="space-y-2">
            <div class="flex items-center justify-between gap-2">
              <h3 class="text-xs font-semibold uppercase text-muted-foreground">{t('discussionSession.debugSelected')}</h3>
              {#if debug.redacted}
                <span class="rounded border border-border px-2 py-0.5 text-[10px] text-muted-foreground">{t('discussionSession.debugRedacted')}</span>
              {/if}
            </div>
            <p class="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs text-foreground">{selectedDebugRuntime}</p>
          </section>

          <section class="space-y-2">
            <h3 class="text-xs font-semibold uppercase text-muted-foreground">{t('discussionSession.debugOutboundPrompt')}</h3>
            <pre class={debugPromptBlockClass} data-testid="discussion-debug-outbound-prompt">{debug.outboundPrompt || t('discussionSession.debugEmpty')}</pre>
          </section>

          {#if debugProviderMetadataEntries.length > 0}
            <section class="space-y-2">
              <h3 class="text-xs font-semibold uppercase text-muted-foreground">{t('discussionSession.debugProviderMetadata')}</h3>
              <div class="space-y-1" data-testid="discussion-debug-provider-metadata">
                {#each debugProviderMetadataEntries as [key, value]}
                  <div class="rounded-md border border-border bg-muted/20 px-3 py-2 text-xs">
                    <div class="font-mono text-[10px] text-muted-foreground">{key}</div>
                    <div class="mt-1 break-all font-mono text-foreground">{value}</div>
                  </div>
                {/each}
              </div>
            </section>
          {/if}

          <section class="space-y-2">
            <h3 class="text-xs font-semibold uppercase text-muted-foreground">{t('discussionSession.debugRuntimeResponse')}</h3>
            <pre class={debugResponseBlockClass} data-testid="discussion-debug-runtime-response">{debug.runtimeResponse || t('discussionSession.debugEmpty')}</pre>
          </section>

          {#if selectedDebugError}
            <section class="space-y-2">
              <h3 class="text-xs font-semibold uppercase text-muted-foreground">{t('discussionSession.debugError')}</h3>
              <pre class={debugErrorBlockClass} data-testid="discussion-debug-error">{selectedDebugError}</pre>
            </section>
          {/if}
        {:else}
          <p class="text-muted-foreground">{t('discussionSession.debugEmpty')}</p>
        {/if}
      </div>
  </aside>
  {/if}

</div>
