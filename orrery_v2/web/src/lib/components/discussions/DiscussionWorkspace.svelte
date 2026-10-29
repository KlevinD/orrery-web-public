<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte'
  import { Bot, ChevronLeft, ChevronRight, MessageCircle, MoreHorizontal, Plus, Save, Search, Settings2, Sparkles, Trash2, User, Users, X } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import DiscussionPanel from '#lib/components/discussions/DiscussionPanel.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import { approvalApi } from '#lib/api/approvals'
  import { discussionApi } from '#lib/api/discussions'
  import { applyAuthoritativeDiscussionSnapshot, applyDiscussionHILModeUpdate, applyDiscussionStreamEvent, mapDiscussionMessageRequest, mapDiscussionView, prependDiscussionMessagePage } from '#lib/mappers/discussions'
  import { mapApprovalDecisionRequest } from '#lib/mappers/approval'
  import { mapHILDecisionRequest } from '#lib/mappers/hil'
  import { createTranslator, locale } from '#lib/i18n'
  import {
    approvalQueryKeys,
    useApprovalListQuery
  } from '#lib/queries/approvals'
  import {
    combineDiscussionListPages,
    discussionQueryKeys,
    fetchDiscussionMessagePage,
    retryDiscussionMessageStream,
    sendDiscussionMessageStream,
    subscribeDiscussionStream,
    useDiscussionAbortMutation,
    useDiscussionAddMembersMutation,
    useDiscussionAttachmentUploadMutation,
    useDiscussionDeleteMutation,
    useDiscussionHILModeMutation,
    useDiscussionListInfiniteQuery,
    useDiscussionQuery,
    useDiscussionRunDebugQuery,
    useDiscussionWorkspaceMutation
  } from '#lib/queries/discussions'
  import { createMissionConversation, missionQueryKeys, resolveMissionConversationId, subscribeMissionStream, useMissionControlQuery, useMissionOpsQuery } from '#lib/queries/missions'
  import { notificationBadgeQueryKeys } from '#lib/queries/notification-badges'
  import { usePersonalAssistantQuery } from '#lib/queries/profile'
  import { createScheduledMission as createScheduledMissionRecord, schedulerQueryKeys } from '#lib/queries/scheduler'
  import { useWorkforceMembersQuery } from '#lib/queries/workforce'
  import { isCurrentSessionUserIdentity, session } from '#lib/stores/session.svelte'
  import { createIdempotencyKey } from '#lib/runtime/ids'
  import { clearDiscussionWorkspaceContext, updateDiscussionWorkspaceContext } from '#lib/discussion-workspace-context.svelte'
  import { useQueryClient } from '@tanstack/svelte-query'
  import { ApiClientError } from '#lib/api/client'
  import type {
    ApprovalActionViewModel,
    ApprovalCenterViewModel,
    ApprovalDecisionFormViewModel,
    ApprovalItemViewModel,
  } from '#lib/types/view-models'
  import type {
    DiscussionDTO,
    DiscussionHILMode,
    DiscussionMessageDTO,
    DiscussionStreamEventDTO,
    DiscussionListViewModel,
    DiscussionMessageFormViewModel,
    DiscussionSummaryViewModel,
    DiscussionViewModel
  } from '#lib/types/discussions'
  import type { PersonalAssistantOwnedAgentViewModel, WorkforceMemberViewModel } from '#lib/types/view-models'

  type DirectChatTargetOption = {
    id: string
    kind: 'agent' | 'user'
    name: string
    role: string
    runtimeKind: string
  }
  type WorkforceAvatarIdentity = {
    profileId: string
    human: boolean
  }
  type AgentSessionTarget =
    | { kind: 'personal_assistant' }
    | { kind: 'agent'; id: string; name: string; role: string; runtimeKind: string }
  type ConversationFilter = 'all' | 'agent' | 'group' | 'person'
  type GroupDraftKind = 'group' | 'assistant'
  type MissionComposerRequest = {
    prompt: string
    launchMode: 'immediate' | 'scheduled'
    scheduledAt: string
  }
  type MissionLaunchAttempt = {
    fingerprint: string
    idempotencyKey: string
    missionId: string
  }
  type PendingInterjection = {
    discussionId: string
    turnId: string
    runId: string
    content: string
  }
  const discussionPrewarmThrottleMs = 60_000
  const missionCoordinatorAgentId = 'A000010'

  interface Props {
    initialDiscussionId?: string
    initialDraft?: string
    initialSendImmediately?: boolean
    initialDirectTargetId?: string
    initialDirectTargetKind?: 'agent' | 'user'
    initialDirectTargetName?: string
    initialAssistant?: boolean
  }

  let {
    initialDiscussionId = '',
    initialDraft = '',
    initialSendImmediately = false,
    initialDirectTargetId = '',
    initialDirectTargetKind,
    initialDirectTargetName = '',
    initialAssistant = false
  }: Props = $props()
  let t = $derived(createTranslator($locale))
  let selectedDiscussionId = $state('')
  let appliedInitialDiscussionId = $state('')
  let appliedInitialComposerKey = $state('')
  let draft = $state('')
  let draftMentions = $state<NonNullable<DiscussionMessageFormViewModel['mentions']>>([])
  let streamingDiscussion = $state<DiscussionViewModel | undefined>(undefined)
  let streamBusy = $state(false)
  let missionLaunchBusy = $state(false)
  let missionLaunchAttempt = $state<MissionLaunchAttempt | undefined>(undefined)
  let interjectBusy = $state(false)
  let pendingInterjection = $state<PendingInterjection | undefined>(undefined)
  let streamError = $state('')
  let debugOpen = $state(false)
  let selectedDebugRunId = $state('')
  let activeMissionId = $state('')
  let newDiscussionMode = $state(false)
  let forceNewConversation = $state(false)
  let personalAssistantMode = $state(false)
  let appliedInitialAssistant = $state(false)
  let autoOpenedEmptyAssistant = $state(false)
  let directPickerOpen = $state(false)
  let memberPickerOpen = $state(false)
  let selectedDirectTarget = $state<DirectChatTargetOption | undefined>(undefined)
  let discussionListCollapsed = $state(false)
  let taskTimelineCollapsed = $state(true)
  let workspaceSettingsOpen = $state(false)
  let openActionMenuDiscussionId = $state('')
  let discussionSearch = $state('')
  let conversationFilter = $state<ConversationFilter>('all')
  let draftDiscussionSummary = $state<DiscussionSummaryViewModel | undefined>(undefined)
  let olderMessagesLoading = $state(false)
  let olderMessagesError = $state('')
  let pendingAttachments = $state<NonNullable<DiscussionViewModel['attachments']>>([])
  let attachmentError = $state('')
  let workspaceDraft = $state('')
  let workspaceDraftDiscussionId = $state('')
  let workspaceError = $state('')
  let locallyConvertedDiscussionIds = $state<string[]>([])
  let streamAbortController = $state<AbortController | undefined>(undefined)
  let activeHttpStreamEventIds = $state<Set<string>>(new Set())
  let approvalBusyIds = $state<string[]>([])
  let lastQueryDiscussion: DiscussionViewModel | undefined
  let lastBadgeRefreshDiscussionId = ''
  let prewarmEffectDiscussionId = ''
  let forbiddenSnapshotRefreshDiscussionId = $state('')
  const prewarmedNewDiscussionIds: Record<GroupDraftKind, string> = { group: '', assistant: '' }
  const newDiscussionPrewarmPromises: Partial<Record<GroupDraftKind, Promise<string | undefined>>> = {}
  const discussionPrewarmRequestedAt = new Map<string, number>()
  const discussionPageSize = 20

  const listQuery = useDiscussionListInfiniteQuery(() => discussionPageSize)
  const discussionQuery = useDiscussionQuery(() => selectedDiscussionId)
  const debugQuery = useDiscussionRunDebugQuery(() => selectedDiscussionId, () => selectedDebugRunId, () => debugOpen)
  const deleteDiscussionMutation = useDiscussionDeleteMutation()
  const abortDiscussionMutation = useDiscussionAbortMutation()
  const addMembersMutation = useDiscussionAddMembersMutation()
  const workspaceMutation = useDiscussionWorkspaceMutation()
  const hilModeMutation = useDiscussionHILModeMutation()
  const uploadAttachmentMutation = useDiscussionAttachmentUploadMutation()
  const personalAssistantQuery = usePersonalAssistantQuery()
  const workforceMembersQuery = useWorkforceMembersQuery(
    () => ({ enabledOnly: true, includeDetails: false, limit: 100 }),
    () => directPickerOpen || memberPickerOpen || initialComposerNeedsWorkforceLookup()
  )
  const queryClient = useQueryClient()

  onMount(() => {
    if (typeof window.matchMedia !== 'function') return
    const compact = window.matchMedia('(max-width: 767px)')
    const collapseCompactPanels = (matches: boolean) => {
      if (!matches) return
      discussionListCollapsed = true
      taskTimelineCollapsed = true
    }
    collapseCompactPanels(compact.matches)
    const listener = (event: MediaQueryListEvent) => collapseCompactPanels(event.matches)
    compact.addEventListener('change', listener)
    return () => compact.removeEventListener('change', listener)
  })

  const discussionListData = $derived(combineDiscussionListPages(listQuery.data?.pages ?? []))
  const discussions = $derived(discussionListData.discussions)
  const listPage = $derived(discussionListData.page)
  const personalAssistantDiscussions = $derived(discussions.filter((discussion) => isPersonalAssistantDiscussion(discussion)))
  const personalAssistantDiscussion = $derived(personalAssistantDiscussions[0])
  const recentDiscussions = $derived(discussions.filter((discussion) => !isPersonalAssistantDiscussion(discussion)))
  const visibleDiscussions = $derived(collapseDiscussionListSessions(
    draftDiscussionSummary ? [draftDiscussionSummary, ...recentDiscussions] : recentDiscussions
  ))
  const filteredVisibleDiscussions = $derived(filteredDiscussionSummaries())
  const activeDiscussion = $derived(newDiscussionMode ? streamingDiscussion : streamingDiscussion ?? discussionQuery.data)
  const activeAgentSessionTarget = $derived(resolveActiveAgentSessionTarget())
  const activeAgentSessions = $derived(agentSessionsForTarget(activeAgentSessionTarget))
  const activeAgentSessionValue = $derived(newDiscussionMode ? '__new_session__' : selectedDiscussionId)
  const activeDiscussionMemberIDs = $derived(new Set((activeDiscussion?.members ?? []).map((member) => member.id).filter(Boolean)))
  const directChatTargetOptions = $derived(
    (workforceMembersQuery.data?.members ?? [])
      .map(directChatTargetOptionFromMember)
      .filter((target): target is DirectChatTargetOption => Boolean(target))
  )
  const avatarIdentityById = $derived(buildWorkforceAvatarIdentityIndex(workforceMembersQuery.data?.members ?? []))
  const groupConversionTargetOptions = $derived(
    directChatTargetOptions.filter((target) => !activeDiscussionMemberIDs.has(target.id))
  )
  const olderMessagesHasMore = $derived(Boolean(activeDiscussion?.messagePage?.hasMore))
  const workspaceEditable = $derived(Boolean(activeDiscussion?.id && activeDiscussion.kind !== 'direct' && !newDiscussionMode))
  const missionContextQuery = useMissionControlQuery(() => activeMissionId)
  const missionOpsQuery = useMissionOpsQuery(() => activeMissionId)
  const missionApprovalsQuery = useApprovalListQuery(
    () => ({ missionId: activeMissionId, status: 'pending', limit: 20 }),
    () => activeMissionId.length > 0
  )
  const personalAssistantOwnedAgent = $derived(selectedPersonalAssistantOwnedAgent())
  const personalAssistantRuntime = $derived(personalAssistantRuntimeLabel())
  const personalAssistantAvatarUrl = $derived(personalAssistantQuery.data?.avatarUrl ?? '')
  const personalAssistantEnabled = $derived(personalAssistantQuery.data?.enabled ?? true)
  const personalAssistantMissionCount = $derived(
    personalAssistantMode
      ? activeDiscussion?.linkedMissions.length ?? personalAssistantDiscussion?.linkedMissionCount ?? 0
      : personalAssistantDiscussion?.linkedMissionCount ?? 0
  )
  const newAgentSessionEnabled = $derived(Boolean(activeDiscussion?.id) && (
    personalAssistantMode
      || (activeDiscussion?.kind === 'direct' && (activeDiscussion.directTargetKind === 'agent' || Boolean(activeDiscussion.directAgentId)))
  ))

  $effect(() => {
    const linkedMissions = activeDiscussion?.linkedMissions ?? []
    if (linkedMissions.some((mission) => mission.id === activeMissionId)) return
    activeMissionId = linkedMissions[0]?.id ?? ''
  })

  $effect(() => {
    const missionError = missionContextQuery.error as ApiClientError | null
    const missionUnavailable = missionContextQuery.isError && missionError?.status === 404
    updateDiscussionWorkspaceContext(
      activeDiscussion,
      missionContextQuery.data,
      Boolean(activeMissionId) && missionContextQuery.isLoading,
      missionContextQuery.isError && !missionUnavailable ? (missionError?.message ?? t('discussionSession.loadFailed')) : '',
      personalAssistantMode,
      activeMissionId,
      (missionId) => (activeMissionId = missionId),
      beginMissionCreate,
      missionUnavailable
    )
  })

  onDestroy(() => {
    clearDiscussionWorkspaceContext()
  })

  $effect(() => {
    if (initialComposerPending() || initialAssistant || newDiscussionMode || selectedDiscussionId || discussions.length === 0) return
    const firstDiscussion = recentDiscussions.find((discussion) => !isPersonalAssistantDiscussion(discussion)) ?? personalAssistantDiscussion ?? recentDiscussions[0]
    if (firstDiscussion) selectDiscussion(firstDiscussion.id, isPersonalAssistantDiscussion(firstDiscussion), false)
  })

  $effect(() => {
    if (!initialAssistant) {
      appliedInitialAssistant = false
      return
    }
    if (appliedInitialAssistant || personalAssistantQuery.isPending || listQuery.isLoading) return
    appliedInitialAssistant = true
    startPersonalAssistantDiscussion()
  })

  $effect(() => {
    if (
      autoOpenedEmptyAssistant
      || initialAssistant
      || initialDiscussionId.trim()
      || initialComposerPending()
      || personalAssistantQuery.isPending
      || listQuery.isLoading
      || discussions.length > 0
      || selectedDiscussionId
      || newDiscussionMode
    ) return
    autoOpenedEmptyAssistant = true
    startPersonalAssistantDiscussion()
  })

  $effect(() => {
    const discussionId = initialDiscussionId.trim()
    if (!discussionId || appliedInitialDiscussionId === discussionId) return
    appliedInitialDiscussionId = discussionId
    selectDiscussion(discussionId)
  })

  $effect(() => {
    const discussionId = discussionQuery.data?.id ?? ''
    if (!discussionId || discussionId === lastBadgeRefreshDiscussionId) return
    lastBadgeRefreshDiscussionId = discussionId
    void queryClient.invalidateQueries({ queryKey: notificationBadgeQueryKeys.discussions })
  })

  $effect(() => {
    const content = initialDraft.trim()
    const targetID = initialDirectTargetId.trim()
    const targetKind = initialDirectTargetKind
    const targetName = initialDirectTargetName.trim()
    const key = initialComposerKey(content, targetID, targetKind, targetName)
    if (!content || appliedInitialComposerKey === key) return
    if (targetID && listQuery.isPending) return
    if (targetID && !hasInitialDirectTargetMetadata() && workforceMembersQuery.isPending) return

    const target = targetID
      ? directChatTargetOptions.find((candidate) => candidate.id === targetID) ?? initialDirectTargetOption(targetID)
      : undefined
    if (target) startDirectDiscussion(target)
    else startNewDiscussion()
    draft = content
    appliedInitialComposerKey = key
    if (initialSendImmediately) void sendInitialComposerMessage(key, content)
  })

  async function sendInitialComposerMessage(key: string, content: string) {
    await tick()
    if (appliedInitialComposerKey !== key || draft.trim() !== content || streamBusy) return
    await sendMessage()
  }

  $effect(() => {
    const discussionId = selectedDiscussionId
    if (!discussionId) return
    return subscribeDiscussionStream(
      discussionId,
      () => {
        void refreshDiscussionSnapshot(discussionId)
      },
      (event) => {
        if (eventNeedsApprovalRefresh(event)) refreshActiveMissionApprovals()
        if (streamBusy && realtimeEventIsCoveredByActiveHttpStream(event)) return
        const next = applyDiscussionStreamEvent(currentDiscussionForRealtimeEvent(discussionId), event)
        if (!next) return
        streamingDiscussion = next
      }
    )
  })

  $effect(() => {
    const missionId = activeMissionId
    if (!missionId) return
    return subscribeMissionStream(missionId, () => Promise.all([
      queryClient.invalidateQueries({ queryKey: missionQueryKeys.control(missionId) }),
      queryClient.invalidateQueries({ queryKey: missionQueryKeys.ops(missionId) }),
      queryClient.invalidateQueries({ queryKey: approvalQueryKeys.list({ missionId, status: 'pending', limit: 20 }) })
    ]))
  })

  $effect(() => {
    if (forbiddenSnapshotRefreshDiscussionId && forbiddenSnapshotRefreshDiscussionId !== selectedDiscussionId) {
      forbiddenSnapshotRefreshDiscussionId = ''
    }
  })

  async function refreshDiscussionSnapshot(discussionId: string) {
    if (!discussionId || selectedDiscussionId !== discussionId) return
    if (forbiddenSnapshotRefreshDiscussionId === discussionId) return
    try {
      const fetched = mapDiscussionView(await discussionApi.getDiscussion(discussionId, { limit: 20 }) as DiscussionDTO)
      if (selectedDiscussionId !== discussionId) return
      if (forbiddenSnapshotRefreshDiscussionId === discussionId) forbiddenSnapshotRefreshDiscussionId = ''
      const current = currentDiscussionForRealtimeEvent(fetched.id)
      const next = preserveMonotonicApprovalDecisions(current, applyAuthoritativeDiscussionSnapshot(current, fetched))
      streamingDiscussion = next
      lastQueryDiscussion = next
      queryClient.setQueryData(discussionQueryKeys.detail(next.id), next)
      rememberDiscussionInList(next)
    } catch (error) {
      if (selectedDiscussionId !== discussionId) return
      if (error instanceof ApiClientError && error.status === 403) {
        forbiddenSnapshotRefreshDiscussionId = discussionId
      }
      streamError = error instanceof Error ? error.message : t('discussionSession.sendFailed')
    }
  }

  $effect(() => {
    if (!selectedDiscussionId || newDiscussionMode) return
    const latest = discussionQuery.data
    if (!latest || latest.id !== selectedDiscussionId) return
    if (latest.kind === 'direct' && locallyConvertedDiscussionIds.includes(latest.id)) return
    if (latest === lastQueryDiscussion) return
    lastQueryDiscussion = latest
    streamingDiscussion = preserveMonotonicApprovalDecisions(
      streamingDiscussion,
      applyAuthoritativeDiscussionSnapshot(streamingDiscussion, latest)
    )
  })

  $effect(() => {
    const discussionId = activeDiscussion?.id ?? ''
    if (workspaceDraftDiscussionId === discussionId) return
    workspaceDraftDiscussionId = discussionId
    workspaceDraft = activeDiscussion?.workspaceRootDir ?? ''
    workspaceError = ''
    workspaceSettingsOpen = false
  })

  $effect(() => {
    const discussionId = activeDiscussion?.id ?? ''
    if (newDiscussionMode || !discussionId || activeDiscussion?.kind === 'direct') {
      prewarmEffectDiscussionId = ''
      return
    }
    if (prewarmEffectDiscussionId === discussionId) return
    prewarmEffectDiscussionId = discussionId
    void prewarmExistingDiscussion(discussionId)
  })

  function startNewDiscussion() {
    startGroupDiscussion(false)
  }

  function startPersonalAssistantDiscussion() {
    if (!personalAssistantEnabled) {
      if (typeof window !== 'undefined') window.location.assign('/profile#personal-assistant')
      return
    }
    if (personalAssistantDiscussion) {
      selectDiscussion(personalAssistantDiscussion.id, true)
      return
    }
    startGroupDiscussion(true)
  }

  function startFreshPersonalAssistantDiscussion() {
    if (!personalAssistantEnabled) return
    startGroupDiscussion(true)
    forceNewConversation = true
  }

  function startGroupDiscussion(asPersonalAssistant: boolean) {
    selectedDiscussionId = ''
    draft = ''
    personalAssistantMode = asPersonalAssistant
    streamingDiscussion = createDraftDiscussion(asPersonalAssistant)
    draftDiscussionSummary = asPersonalAssistant ? undefined : createDraftDiscussionSummary()
    selectedDirectTarget = undefined
    directPickerOpen = false
    memberPickerOpen = false
    streamError = ''
    pendingAttachments = []
    attachmentError = ''
    olderMessagesError = ''
    olderMessagesLoading = false
    streamAbortController?.abort()
    streamAbortController = undefined
    debugOpen = false
    selectedDebugRunId = ''
    workspaceSettingsOpen = false
    openActionMenuDiscussionId = ''
    newDiscussionMode = true
    forceNewConversation = false
    syncDiscussionSelectionURL('', asPersonalAssistant)
    collapseDiscussionListForCompactViewport()
    void ensureNewDiscussionPrewarm()
  }

  function toggleDirectPicker() {
    directPickerOpen = !directPickerOpen
    if (directPickerOpen) memberPickerOpen = false
  }

  function openMemberPicker() {
    if (!activeDiscussion?.id || activeDiscussion.kind !== 'direct') return
    memberPickerOpen = true
    directPickerOpen = false
  }

  function startDirectDiscussion(target: DirectChatTargetOption) {
    const existing = existingDirectDiscussionForTarget(target)
    if (existing) {
      selectDiscussion(existing.id)
      return
    }
    startDirectDiscussionDraft(target, false)
  }

  function startFreshAgentSession() {
    if (personalAssistantMode) {
      startFreshPersonalAssistantDiscussion()
      return
    }
    const target = activeDirectAgentTarget()
    if (!target) return
    startDirectDiscussionDraft(target, true)
  }

  function activeDirectAgentTarget(): DirectChatTargetOption | undefined {
    const discussion = activeDiscussion
    if (!discussion || discussion.kind !== 'direct') return undefined
    if (discussion.directTargetKind !== 'agent' && !discussion.directAgentId) return undefined
    const id = (discussion.directTargetId || discussion.directAgentId).trim()
    if (!id) return undefined
    return directChatTargetOptions.find((target) => target.kind === 'agent' && target.id === id) ?? {
      id,
      kind: 'agent',
      name: discussion.directTargetName || discussion.directAgentName || discussion.title,
      role: discussion.members.find((member) => member.kind === 'agent' && member.id === id)?.role ?? '',
      runtimeKind: ''
    }
  }

  function startDirectDiscussionDraft(target: DirectChatTargetOption, forceNew: boolean) {
    selectedDiscussionId = ''
    draft = ''
    personalAssistantMode = false
    selectedDirectTarget = target
    streamingDiscussion = createDraftDirectDiscussion(target)
    draftDiscussionSummary = forceNew ? undefined : createDraftDirectDiscussionSummary(target)
    streamError = ''
    pendingAttachments = []
    attachmentError = ''
    olderMessagesError = ''
    olderMessagesLoading = false
    streamAbortController?.abort()
    streamAbortController = undefined
    debugOpen = false
    selectedDebugRunId = ''
    workspaceSettingsOpen = false
    openActionMenuDiscussionId = ''
    newDiscussionMode = true
    forceNewConversation = forceNew
    directPickerOpen = false
    memberPickerOpen = false
    syncDiscussionSelectionURL('')
    collapseDiscussionListForCompactViewport()
  }

  function selectDiscussion(
    discussionId: string,
    asPersonalAssistant = isPersonalAssistantDiscussionId(discussionId),
    collapseCompactList = true
  ) {
    selectedDiscussionId = discussionId
    syncDiscussionSelectionURL(discussionId)
    personalAssistantMode = asPersonalAssistant
    streamingDiscussion = undefined
    draftDiscussionSummary = undefined
    selectedDirectTarget = undefined
    directPickerOpen = false
    memberPickerOpen = false
    streamError = ''
    pendingAttachments = []
    attachmentError = ''
    olderMessagesError = ''
    olderMessagesLoading = false
    streamAbortController?.abort()
    streamAbortController = undefined
    debugOpen = false
    selectedDebugRunId = ''
    workspaceSettingsOpen = false
    openActionMenuDiscussionId = ''
    newDiscussionMode = false
    forceNewConversation = false
    if (collapseCompactList) collapseDiscussionListForCompactViewport()
  }

  function collapseDiscussionListForCompactViewport() {
    if (typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia('(max-width: 767px)').matches) {
      discussionListCollapsed = true
    }
  }

  function syncDiscussionSelectionURL(discussionId: string, assistantDraft = false) {
    if (typeof window === 'undefined' || window.location.pathname.replace(/\/+$/, '') !== '/discussions') return
    const url = new URL(window.location.href)
    if (discussionId.trim()) url.searchParams.set('discussion', discussionId.trim())
    else url.searchParams.delete('discussion')
    if (assistantDraft && !discussionId.trim()) url.searchParams.set('assistant', '1')
    else url.searchParams.delete('assistant')
    window.history.replaceState(window.history.state, '', url)
  }

  function initialComposerPending() {
    const content = initialDraft.trim()
    if (!content) return false
    return appliedInitialComposerKey !== initialComposerKey(
      content,
      initialDirectTargetId.trim(),
      initialDirectTargetKind,
      initialDirectTargetName.trim()
    )
  }

  function initialComposerNeedsWorkforceLookup() {
    return initialComposerPending() && Boolean(initialDirectTargetId.trim()) && !hasInitialDirectTargetMetadata()
  }

  function hasInitialDirectTargetMetadata() {
    return Boolean(initialDirectTargetId.trim() && initialDirectTargetKind && initialDirectTargetName.trim())
  }

  function initialDirectTargetOption(targetID: string): DirectChatTargetOption {
    return {
      id: targetID,
      kind: initialDirectTargetKind === 'user' ? 'user' : 'agent',
      name: initialDirectTargetName.trim() || targetID,
      role: '',
      runtimeKind: ''
    }
  }

  function initialComposerKey(
    content: string,
    targetID: string,
    targetKind: 'agent' | 'user' | undefined,
    targetName: string
  ) {
    return `${targetKind ?? ''}\u0000${targetID}\u0000${targetName}\u0000${content}`
  }

  function selectDiscussionSummary(discussion: DiscussionSummaryViewModel) {
    if (discussion.id === draftDiscussionSummary?.id) {
      selectedDiscussionId = ''
      newDiscussionMode = true
      collapseDiscussionListForCompactViewport()
      return
    }
    if (discussion.kind !== 'direct') void prewarmExistingDiscussion(discussion.id)
    selectDiscussion(discussion.id)
  }

  function anchorCreatedDiscussion(discussionId: string) {
    const id = discussionId.trim()
    if (!id) return
    selectedDiscussionId = id
    syncDiscussionSelectionURL(id)
    draftDiscussionSummary = undefined
    newDiscussionMode = false
    forceNewConversation = false
    for (const kind of ['group', 'assistant'] as const) {
      if (prewarmedNewDiscussionIds[kind] !== id) continue
      prewarmedNewDiscussionIds[kind] = ''
      newDiscussionPrewarmPromises[kind] = undefined
    }
  }

  function ensureNewDiscussionPrewarm(): Promise<string | undefined> {
    if (selectedDirectTarget) return Promise.resolve(undefined)
    const kind: GroupDraftKind = personalAssistantMode ? 'assistant' : 'group'
    const prewarmedDiscussionId = prewarmedNewDiscussionIds[kind]
    if (prewarmedDiscussionId) {
      void prewarmExistingDiscussion(prewarmedDiscussionId)
      return Promise.resolve(prewarmedDiscussionId)
    }
    if (!newDiscussionPrewarmPromises[kind]) {
      newDiscussionPrewarmPromises[kind] = (async () => {
        try {
          const dto = await discussionApi.prewarmDiscussion(
            undefined,
            {
              title: kind === 'assistant' ? t('discussionSession.personalAssistant') : t('discussionSession.newDiscussion'),
              conversation_kind: kind === 'assistant' ? 'personal_assistant' : 'group'
            },
            { idempotencyKey: createIdempotencyKey() }
          ) as DiscussionDTO
          const discussionId = dto?.discussion_id?.trim() ?? ''
          if (!discussionId) return undefined
          prewarmedNewDiscussionIds[kind] = discussionId
          discussionPrewarmRequestedAt.set(discussionId, Date.now())
          return discussionId
        } catch {
          return undefined
        } finally {
          newDiscussionPrewarmPromises[kind] = undefined
        }
      })()
    }
    return newDiscussionPrewarmPromises[kind]!
  }

  async function prewarmExistingDiscussion(discussionId: string) {
    const id = discussionId.trim()
    if (!id) return
    const lastRequestedAt = discussionPrewarmRequestedAt.get(id) ?? 0
    if (Date.now() - lastRequestedAt < discussionPrewarmThrottleMs) return
    discussionPrewarmRequestedAt.set(id, Date.now())
    try {
      await discussionApi.prewarmDiscussion(id, {}, { idempotencyKey: createIdempotencyKey() })
    } catch {
      discussionPrewarmRequestedAt.delete(id)
    }
  }

  function prewarmActiveDiscussion() {
    if (newDiscussionMode) {
      if (!selectedDirectTarget) void ensureNewDiscussionPrewarm()
      return
    }
    if (activeDiscussion?.id && activeDiscussion.kind !== 'direct') {
      void prewarmExistingDiscussion(activeDiscussion.id)
    }
  }

  function toggleDiscussionActionMenu(event: MouseEvent, discussionId: string) {
    event.stopPropagation()
    openActionMenuDiscussionId = openActionMenuDiscussionId === discussionId ? '' : discussionId
  }

  function openDebug(runId = '') {
    if (!activeDiscussion?.id) return
    if (activeDiscussion.runs.length === 0) return
    const requestedRunId = runId.trim()
    const fallbackRunId = selectedDebugRunId || activeDiscussion.runs.at(-1)?.id || ''
    selectedDebugRunId = requestedRunId || fallbackRunId
    debugOpen = true
    queryClient.invalidateQueries({ queryKey: discussionQueryKeys.debug(activeDiscussion.id, selectedDebugRunId || 'latest') })
  }

  function selectDebugRun(runId: string) {
    selectedDebugRunId = runId
    if (!activeDiscussion?.id) return
    queryClient.invalidateQueries({ queryKey: discussionQueryKeys.debug(activeDiscussion.id, runId || 'latest') })
  }

  function updateDraft(value: string) {
    draft = value
    draftMentions = draftMentions.filter((mention) => !mention.display_name || value.includes(`@${mention.display_name}`))
    if (value.trim()) prewarmActiveDiscussion()
  }

  function addDraftMention(member: DiscussionViewModel['members'][number]) {
    const targetKind = member.kind === 'agent' ? 'agent' : 'user'
    if (draftMentions.some((mention) => mention.target_kind === targetKind && mention.target_id === member.id)) return
    draftMentions = [...draftMentions, { target_kind: targetKind, target_id: member.id, display_name: member.name }]
  }

  async function sendMessage(contentOverride = '') {
    const content = (contentOverride || draft).trim()
    if ((!content && pendingAttachments.length === 0) || interjectBusy) return
    if (activeDiscussion?.id && (streamBusy || discussionIsRunning(activeDiscussion))) {
      await interjectActiveDiscussion(content)
      return
    }
    if (streamBusy) return
    const message: DiscussionMessageFormViewModel = {
      content,
      conversationKind: newDiscussionMode && personalAssistantMode && !selectedDirectTarget ? 'personal_assistant' : undefined,
      forceNewConversation: newDiscussionMode && forceNewConversation,
      directTargetId: newDiscussionMode ? selectedDirectTarget?.id : undefined,
      directTargetKind: newDiscussionMode ? selectedDirectTarget?.kind : undefined,
      attachmentIds: pendingAttachments.map((attachment) => attachment.id),
      attachments: pendingAttachments,
      mentions: draftMentions
    }
    draft = ''
    draftMentions = []
    const sentAttachments = pendingAttachments
    pendingAttachments = []
    streamBusy = true
    activeHttpStreamEventIds = new Set()
    streamError = ''
    const controller = new AbortController()
    streamAbortController = controller
    try {
      const targetDiscussionId = newDiscussionMode
        ? (selectedDirectTarget ? undefined : await ensureNewDiscussionPrewarm())
        : selectedDiscussionId
      const completed = await sendDiscussionMessageStream({
        discussionId: targetDiscussionId,
        form: message,
        initialDiscussion: activeDiscussion,
        signal: controller.signal,
        onDiscussion: (discussion) => {
          streamingDiscussion = discussion
          if (discussion.id) {
            anchorCreatedDiscussion(discussion.id)
            rememberDiscussionInList(discussion)
          }
        },
        onDiscussionId: (discussionId) => {
          anchorCreatedDiscussion(discussionId)
        },
        onEvent: rememberActiveHttpStreamEvent
      })
      if (completed.id) {
        lastQueryDiscussion = completed
        queryClient.setQueryData(discussionQueryKeys.detail(completed.id), completed)
        await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.detail(completed.id) })
      }
      await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.all })
      rememberDiscussionInList(completed)
    } catch (error) {
      if (!isAbortError(error)) {
        streamError = error instanceof Error ? error.message : t('discussionSession.sendFailed')
      }
      if (streamingDiscussion?.id) {
        anchorCreatedDiscussion(streamingDiscussion.id)
        rememberDiscussionInList(streamingDiscussion)
        await refreshDiscussionSnapshot(streamingDiscussion.id)
      }
      pendingAttachments = sentAttachments
    } finally {
      if (streamAbortController === controller) {
        streamAbortController = undefined
      }
      streamBusy = false
      activeHttpStreamEventIds = new Set()
    }
  }

  async function retryMessage(messageId: string, approve: boolean) {
    if (!activeDiscussion?.id || streamBusy) return
    streamBusy = true
    activeHttpStreamEventIds = new Set()
    streamError = ''
    const controller = new AbortController()
    streamAbortController = controller
    try {
      const completed = await retryDiscussionMessageStream({
        discussion: activeDiscussion,
        messageId,
        approve,
        signal: controller.signal,
        onDiscussion: (discussion) => {
          streamingDiscussion = discussion
          rememberDiscussionInList(discussion)
        },
        onEvent: rememberActiveHttpStreamEvent
      })
      lastQueryDiscussion = completed
      queryClient.setQueryData(discussionQueryKeys.detail(completed.id), completed)
      await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.detail(completed.id) })
      await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.all })
      rememberDiscussionInList(completed)
    } catch (error) {
      if (!isAbortError(error)) {
        streamError = error instanceof Error ? error.message : t('discussionSession.sendFailed')
      }
      if (activeDiscussion?.id) await refreshDiscussionSnapshot(activeDiscussion.id)
    } finally {
      if (streamAbortController === controller) {
        streamAbortController = undefined
      }
      streamBusy = false
      activeHttpStreamEventIds = new Set()
    }
  }

  async function abortActiveDiscussion() {
    if (!activeDiscussion?.id || abortDiscussionMutation.isPending) return
    const current = activeDiscussion
    const discussionId = activeDiscussion.id
    streamAbortController?.abort()
    streamAbortController = undefined
    streamError = ''
    try {
      const aborted = mapDiscussionView(await abortDiscussionMutation.mutateAsync(discussionId))
      const next = applyAuthoritativeDiscussionSnapshot(current, aborted)
      streamingDiscussion = next
      lastQueryDiscussion = next
      queryClient.setQueryData(discussionQueryKeys.detail(next.id), next)
      rememberDiscussionInList(next)
      await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.detail(discussionId) })
      await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.all })
    } catch (error) {
      streamError = error instanceof Error ? error.message : t('discussionSession.sendFailed')
    } finally {
      streamBusy = false
    }
  }

  async function saveWorkspaceRoot(value = workspaceDraft) {
    if (!activeDiscussion?.id || !workspaceEditable || workspaceMutation.isPending) return
    const current = activeDiscussion
    workspaceError = ''
    try {
      const updated = await workspaceMutation.mutateAsync({
        discussionId: current.id,
        workspaceRootDir: value.trim()
      })
      const next = applyAuthoritativeDiscussionSnapshot(current, updated)
      streamingDiscussion = next
      workspaceDraft = next.workspaceRootDir ?? ''
      lastQueryDiscussion = next
      queryClient.setQueryData(discussionQueryKeys.detail(next.id), next)
      rememberDiscussionInList(next)
      await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.detail(next.id) })
      await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.all })
    } catch (error) {
      workspaceError = error instanceof Error ? error.message : t('discussionSession.workspaceSaveFailed')
    }
  }

  function openApprovalDecision(item: ApprovalItemViewModel, action: ApprovalActionViewModel, form?: Pick<ApprovalDecisionFormViewModel, 'message' | 'modifiedInput' | 'choiceId' | 'remember'>) {
    void submitApprovalDecision(item, action, form ?? { message: '' })
  }

  function refreshActiveMissionApprovals() {
    queryClient.invalidateQueries({ queryKey: approvalQueryKeys.all })
    if (activeMissionId) {
      queryClient.invalidateQueries({ queryKey: approvalQueryKeys.list({ missionId: activeMissionId, status: 'pending', limit: 20 }) })
    }
  }

  function currentDiscussionForRealtimeEvent(discussionId: string) {
    if (streamingDiscussion?.id === discussionId) return streamingDiscussion
    if (discussionQuery.data?.id === discussionId) return discussionQuery.data
    return streamingDiscussion ?? discussionQuery.data
  }

  function eventNeedsApprovalRefresh(event: DiscussionStreamEventDTO) {
    return (event.kind === 'run_completed' || event.kind === 'run_failed') && ['blocked', 'pending_approval', 'needs_approval', 'awaiting_approval'].includes(event.run?.status ?? '')
  }

  function realtimeEventIsCoveredByActiveHttpStream(event: DiscussionStreamEventDTO) {
    const eventId = event.event_id?.trim()
    return Boolean(eventId && activeHttpStreamEventIds.has(eventId))
  }

  function rememberActiveHttpStreamEvent(event: DiscussionStreamEventDTO) {
    const eventId = event.event_id?.trim()
    if (!eventId || activeHttpStreamEventIds.has(eventId)) return
    activeHttpStreamEventIds = new Set(activeHttpStreamEventIds).add(eventId)
  }

  async function submitApprovalDecision(item: ApprovalItemViewModel, action: ApprovalActionViewModel, form: { message: string; modifiedInput?: string; choiceId?: string; remember?: boolean }) {
    if (approvalBusyIds.includes(item.id)) return
    const defaultActionPath = `/api/approvals/${item.id}/decision`
    const decision: ApprovalDecisionFormViewModel = {
      approvalId: item.id,
      actionPath: action.path && action.path !== defaultActionPath ? action.path : undefined,
      actionId: action.id,
      taskId: item.taskId,
      message: form.message,
      modifiedInput: form.modifiedInput,
      choiceId: form.choiceId,
      remember: form.remember
    }
    approvalBusyIds = [...approvalBusyIds, item.id]
    streamError = ''
    const previousDiscussion = activeDiscussion
    const previousApprovalCaches = queryClient.getQueriesData<ApprovalCenterViewModel>({ queryKey: approvalQueryKeys.all })
    const optimisticDiscussion = activeDiscussion ? applyApprovalDecisionToDiscussion(activeDiscussion, item, action, form) : undefined
    if (optimisticDiscussion) {
      streamingDiscussion = optimisticDiscussion
      rememberDiscussionInList(optimisticDiscussion)
    }
    patchApprovalDecisionCaches(item, action, form)
    try {
      let response: unknown
      if (item.hilDecisionId) {
        const command = mapHILDecisionRequest({ ...decision, approvalId: item.hilDecisionId })
        response = await approvalApi.decideHILDecision(
          command.decisionId,
          command.body,
          { idempotencyKey: createIdempotencyKey() }
        )
        await queryClient.invalidateQueries({ queryKey: approvalQueryKeys.hilRoot })
      } else {
        const command = mapApprovalDecisionRequest(decision)
        response = await approvalApi.decideApproval(
          command.approvalId,
          command.body,
          { idempotencyKey: createIdempotencyKey() },
          command.actionPath
        )
      }
      const updatedDiscussion = mapApprovalDecisionDiscussion(response)
      const next = updatedDiscussion
        ? preserveMonotonicApprovalDecisions(
            currentDiscussionForDecision(updatedDiscussion.id),
            applyAuthoritativeDiscussionSnapshot(currentDiscussionForDecision(updatedDiscussion.id), updatedDiscussion)
          )
        : optimisticDiscussion
      if (next) {
        streamingDiscussion = next
        lastQueryDiscussion = updatedDiscussion ? next : lastQueryDiscussion
        queryClient.setQueryData(discussionQueryKeys.detail(next.id), next)
        rememberDiscussionInList(next)
      }
      patchApprovalDecisionCaches(item, action, form)
      if (item.hilDecisionId && next?.id) {
        await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.detail(next.id) })
        await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.all })
      }
    } catch (error) {
      streamError = error instanceof Error ? error.message : t('approvals.decisionFailed')
      if (previousDiscussion) {
        streamingDiscussion = previousDiscussion
        queryClient.setQueryData(discussionQueryKeys.detail(previousDiscussion.id), previousDiscussion)
        rememberDiscussionInList(previousDiscussion)
      }
      for (const [queryKey, cached] of previousApprovalCaches) queryClient.setQueryData(queryKey, cached)
      if (previousDiscussion?.id) void refreshDiscussionSnapshot(previousDiscussion.id)
    } finally {
      approvalBusyIds = approvalBusyIds.filter((id) => id !== item.id)
    }
  }

  function mapApprovalDecisionDiscussion(response: unknown): DiscussionViewModel | undefined {
    if (!response || typeof response !== 'object') return undefined
    const dto = response as Partial<DiscussionDTO>
    if (!dto.discussion_id) return undefined
    return mapDiscussionView(dto as DiscussionDTO)
  }

  function currentDiscussionForDecision(discussionId: string) {
    if (streamingDiscussion?.id === discussionId) return streamingDiscussion
    if (discussionQuery.data?.id === discussionId) return discussionQuery.data
    return activeDiscussion
  }

  function preserveMonotonicApprovalDecisions(current: DiscussionViewModel | undefined, next: DiscussionViewModel) {
    if (!current || current.id !== next.id) return next
    const decided = current.timeline
      .filter((entry) => entry.kind === 'approval' && entry.approval && !approvalStatusIsPending(entry.approval.status))
      .map((entry) => entry.approval!)
    if (decided.length === 0) return next
    return {
      ...next,
      timeline: next.timeline.map((entry) => {
        if (entry.kind !== 'approval' || !entry.approval || !approvalStatusIsPending(entry.approval.status)) return entry
        const local = decided.find((item) => approvalItemsShareDecisionIdentity(item, entry.approval!))
        return local ? { ...entry, approval: local } : entry
      })
    }
  }

  function applyApprovalDecisionToDiscussion(
    discussion: DiscussionViewModel,
    item: ApprovalItemViewModel,
    action: ApprovalActionViewModel,
    form: { message: string; modifiedInput?: string; choiceId?: string; remember?: boolean }
  ) {
    let replaced = false
    const timeline = discussion.timeline.map((entry) => {
      if (entry.kind !== 'approval' || !entry.approval || !approvalItemsShareDecisionIdentity(entry.approval, item)) return entry
      replaced = true
      const decided = decidedApprovalItem(mergeApprovalItemForDecision(item, entry.approval), action, form)
      return {
        ...entry,
        sortAt: entry.sortAt || decided.createdAtRaw || decided.createdAt,
        approval: decided
      }
    })
    if (!replaced) {
      const decided = decidedApprovalItem(item, action, form)
      timeline.push({
        id: `approval:${decided.id}`,
        kind: 'approval',
        sortAt: decided.createdAtRaw || decided.createdAt || new Date().toISOString(),
        approval: decided
      })
    }
    const decisionStatus = approvalDecisionStatus(action)
    const shouldResolveApprovalWait = !timeline.some((entry) => entry.kind === 'approval' && entry.approval && approvalStatusIsPending(entry.approval.status))
      && approvalRuntimeStatus(discussion.runtimeStatus || discussion.status)
      && decisionStatus !== 'needs_input'
    const nextRuntimeStatus = shouldResolveApprovalWait
      ? approvalDecisionContinuesRuntime(decisionStatus) ? 'running' : 'completed'
      : discussion.runtimeStatus
    return {
      ...discussion,
      status: shouldResolveApprovalWait ? nextRuntimeStatus : discussion.status,
      runtimeStatus: nextRuntimeStatus,
      timeline: orderByOccurrence(timeline, (entry) => entry.sortAt)
    }
  }

  function mergeApprovalItemForDecision(requested: ApprovalItemViewModel, existing: ApprovalItemViewModel) {
    return {
      ...requested,
      ...existing,
      actions: existing.actions.length > 0 ? existing.actions : requested.actions,
      choices: existing.choices.length > 0 ? existing.choices : requested.choices,
      payloadPreview: existing.payloadPreview.content || existing.payloadPreview.fields.length > 0 ? existing.payloadPreview : requested.payloadPreview
    }
  }

  function patchApprovalDecisionCaches(
    item: ApprovalItemViewModel,
    action: ApprovalActionViewModel,
    form: { message: string; modifiedInput?: string; choiceId?: string; remember?: boolean }
  ) {
    const cached = queryClient.getQueriesData<ApprovalCenterViewModel>({ queryKey: approvalQueryKeys.all })
    for (const [queryKey] of cached) {
      queryClient.setQueryData(queryKey, (current: ApprovalCenterViewModel | undefined) => patchApprovalCenterView(current, queryKey, item, action, form))
    }
  }

  function patchApprovalCenterView(
    current: ApprovalCenterViewModel | undefined,
    queryKey: readonly unknown[],
    item: ApprovalItemViewModel,
    action: ApprovalActionViewModel,
    form: { message: string; modifiedInput?: string; choiceId?: string; remember?: boolean }
  ) {
    if (!current) return current
    const index = current.items.findIndex((candidate) => approvalItemsShareDecisionIdentity(candidate, item))
    if (index < 0) return current
    const filters = approvalQueryFilters(queryKey)
    const decided = decidedApprovalItem(mergeApprovalItemForDecision(item, current.items[index]), action, form)
    const nextItems = current.items
      .map((candidate, candidateIndex) => candidateIndex === index ? decided : candidate)
      .filter((candidate) => approvalMatchesQueryFilters(candidate, filters))
    return {
      ...current,
      items: nextItems,
      summary: summarizeApprovalItems(nextItems)
    }
  }

  function decidedApprovalItem(
    item: ApprovalItemViewModel,
    action: ApprovalActionViewModel,
    form: { message: string; modifiedInput?: string; choiceId?: string; remember?: boolean }
  ): ApprovalItemViewModel {
    const status = approvalDecisionStatus(action)
    return {
      ...item,
      status,
      statusTone: approvalStatusTone(status),
      decisionEffect: action.effect || item.decisionEffect,
      details: form.message.trim() || item.details,
      actions: []
    }
  }

  function approvalDecisionStatus(action: ApprovalActionViewModel) {
    const decision = action.decision.trim().toLowerCase()
    if (decision) return decision
    const actionId = action.id.trim().toLowerCase()
    if (['deny', 'reject', 'dismiss'].includes(actionId)) return 'denied'
    if (['answer'].includes(actionId)) return 'answered'
    if (['needs_context', 'needs_input'].includes(actionId)) return 'needs_input'
    return 'approved'
  }

  function approvalDecisionContinuesRuntime(status: string) {
    return ['approved', 'answered'].includes(status.trim().toLowerCase())
  }

  function approvalStatusTone(status: string): ApprovalItemViewModel['statusTone'] {
    const normalized = status.trim().toLowerCase()
    if (normalized === 'approved' || normalized === 'answered' || normalized === 'closed') return 'success'
    if (normalized === 'denied' || normalized === 'rejected') return 'danger'
    if (approvalStatusIsPending(normalized)) return 'warning'
    return 'neutral'
  }

  function approvalStatusIsPending(status: string) {
    const normalized = status.trim().toLowerCase()
    return normalized === '' || normalized === 'pending' || normalized === 'open'
  }

  function summarizeApprovalItems(items: ApprovalItemViewModel[]) {
    return {
      total: items.length,
      pending: items.filter((item) => approvalStatusIsPending(item.status)).length,
      decided: items.filter((item) => !approvalStatusIsPending(item.status)).length,
      highPriority: items.filter((item) => item.priority === 'high' || item.priority === 'critical').length
    }
  }

  function approvalQueryFilters(queryKey: readonly unknown[]) {
    const query = typeof queryKey[1] === 'string' ? queryKey[1] : ''
    const params = new URLSearchParams(query.startsWith('?') ? query.slice(1) : query)
    return {
      missionId: params.get('mission_id') ?? '',
      status: params.get('status') ?? ''
    }
  }

  function approvalMatchesQueryFilters(item: ApprovalItemViewModel, filters: { missionId: string; status: string }) {
    if (filters.missionId && item.missionId !== filters.missionId) return false
    const status = filters.status.trim().toLowerCase()
    if (!status) return true
    if (status === 'closed') return !approvalStatusIsPending(item.status)
    return item.status.trim().toLowerCase() === status
  }

  function approvalItemsShareDecisionIdentity(left: ApprovalItemViewModel, right: ApprovalItemViewModel) {
    if (left.id && right.id && left.id === right.id) return true
    const leftPathId = approvalActionPathApprovalId(left)
    const rightPathId = approvalActionPathApprovalId(right)
    if (leftPathId && rightPathId && leftPathId === rightPathId) return true
    const leftRequestId = approvalFieldValue(left, approvalRuntimeFieldKey('request'))
    const rightRequestId = approvalFieldValue(right, approvalRuntimeFieldKey('request'))
    if (!leftRequestId || !rightRequestId || leftRequestId !== rightRequestId) return false
    const leftConversation = firstNonEmptyApprovalValue(approvalFieldValue(left, approvalRuntimeFieldKey('conversation')), discussionSourceConversationId(left))
    const rightConversation = firstNonEmptyApprovalValue(approvalFieldValue(right, approvalRuntimeFieldKey('conversation')), discussionSourceConversationId(right))
    return !leftConversation || !rightConversation || leftConversation === rightConversation
  }

  function approvalActionPathApprovalId(approval: ApprovalItemViewModel) {
    for (const action of approval.actions) {
      const clean = action.path.trim().split('?')[0]
      const match = /^\/api\/discussions\/[^/]+\/approvals\/([^/]+)\/decision$/.exec(clean)
      if (match?.[1]) return decodeURIComponent(match[1])
    }
    return ''
  }

  function approvalFieldValue(approval: ApprovalItemViewModel, key: string) {
    const target = normalizeApprovalFieldKey(key)
    return approval.payloadPreview.fields.find((field) => normalizeApprovalFieldKey(field.key) === target)?.value.trim() ?? ''
  }

  function approvalRuntimeFieldKey(segment: string) {
    return ['approval', segment, 'id'].join('_')
  }

  function normalizeApprovalFieldKey(key: string) {
    return key.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '')
  }

  function discussionSourceConversationId(approval: ApprovalItemViewModel) {
    const [sourceType, ...sourceParts] = approval.sourceLabel.split(':')
    if (sourceType.trim().toLowerCase() !== 'discussion') return ''
    return sourceParts.join(':').trim()
  }

  function firstNonEmptyApprovalValue(...values: string[]) {
    return values.find((value) => value.trim())?.trim() ?? ''
  }

  function rememberDiscussionInList(discussion: DiscussionViewModel) {
    if (!discussion.id) return
    const summary = summarizeDiscussion(discussion)
    queryClient.setQueryData(['discussions', 'list', 'infinite', discussionPageSize], (current: { pages?: DiscussionListViewModel[]; pageParams?: unknown[] } | undefined) => {
      if (!current?.pages) return current
      const pages = [...current.pages]
      const first = pages[0] ?? { discussions: [], page: listPage }
      pages[0] = { ...first, discussions: upsertDiscussionSummary(first.discussions, summary) }
      return { ...current, pages }
    })
  }

  function approvalRuntimeStatus(status: string) {
    return ['blocked', 'pending_approval', 'needs_approval', 'awaiting_approval'].includes(status.trim().toLowerCase())
  }

  function orderByOccurrence<T>(items: T[], sortValue: (item: T) => string | undefined) {
    return items
      .map((item, index) => ({
        item,
        index,
        time: Date.parse(sortValue(item) || '')
      }))
      .sort((left, right) => {
        if (!Number.isNaN(left.time) && !Number.isNaN(right.time) && left.time !== right.time) return left.time - right.time
        return left.index - right.index
      })
      .map((entry) => entry.item)
  }

  function removeDiscussionFromList(discussionId: string) {
    queryClient.setQueryData(['discussions', 'list', 'infinite', discussionPageSize], (current: { pages?: DiscussionListViewModel[]; pageParams?: unknown[] } | undefined) => {
      if (!current?.pages) return current
      return {
        ...current,
        pages: current.pages.map((page) => ({
          ...page,
          discussions: page.discussions.filter((item) => item.id !== discussionId),
          page: { ...page.page, total: Math.max(0, page.page.total - 1) }
        }))
      }
    })
  }

  async function setHILMode(mode: DiscussionHILMode) {
    if (!activeDiscussion?.id || !isCurrentSessionUserIdentity(activeDiscussion.createdById) || hilModeMutation.isPending || (activeDiscussion.hilMode ?? 'manual') === mode) return
    const previous = activeDiscussion
    const optimistic = { ...previous, hilMode: mode }
    streamingDiscussion = optimistic
    queryClient.setQueryData(discussionQueryKeys.detail(optimistic.id), optimistic)
    rememberDiscussionInList(optimistic)
    streamError = ''
    try {
      const updated = await hilModeMutation.mutateAsync({ discussionId: previous.id, mode })
      const next = applyDiscussionHILModeUpdate(optimistic, updated)
      streamingDiscussion = next
      lastQueryDiscussion = next
      queryClient.setQueryData(discussionQueryKeys.detail(next.id), next)
      rememberDiscussionInList(next)
    } catch (error) {
      streamingDiscussion = previous
      queryClient.setQueryData(discussionQueryKeys.detail(previous.id), previous)
      rememberDiscussionInList(previous)
      streamError = error instanceof Error ? error.message : t('discussionSession.hilModeSaveFailed')
    }
  }

  async function interjectActiveDiscussion(content: string) {
    const current = activeDiscussion
    if (!current?.id || interjectBusy) return
    const agentMentions = draftMentions.filter((mention) => mention.target_kind === 'agent')
    if (current.kind !== 'direct' && agentMentions.length !== 1) {
      streamError = t('discussionSession.interjectMentionRequired')
      return
    }
    const targetAgentID = current.kind === 'direct'
      ? current.directAgentId || current.directTargetId || current.runs.findLast((run) => discussionRunIsActive(run.status))?.agentId || ''
      : agentMentions[0]?.target_id ?? ''
    const activeRun = [...current.runs].reverse().find((run) => discussionRunIsActive(run.status) && (!targetAgentID || run.agentId === targetAgentID))
    if (!activeRun) {
      streamError = t('discussionSession.interjectRunChanged')
      return
    }
    const form: DiscussionMessageFormViewModel = {
      content,
      mentions: draftMentions,
      expectedTurnId: activeRun.turnId,
      expectedRunId: activeRun.id
    }
    interjectBusy = true
    pendingInterjection = {
      discussionId: current.id,
      turnId: activeRun.turnId ?? '',
      runId: activeRun.id,
      content
    }
    streamError = ''
    try {
      const message = await discussionApi.interjectDiscussion(
        current.id,
        mapDiscussionMessageRequest(form),
        { idempotencyKey: createIdempotencyKey() }
      ) as DiscussionMessageDTO
      const base = currentDiscussionForRealtimeEvent(current.id)
      const next = applyDiscussionStreamEvent(base, {
        kind: 'message_completed',
        discussion_id: current.id,
        turn_id: message.turn_id,
        run_id: message.run_id,
        revision: base?.revision,
        message
      })
      if (next) {
        streamingDiscussion = next
        queryClient.setQueryData(discussionQueryKeys.detail(next.id), next)
        rememberDiscussionInList(next)
      }
      draft = ''
      draftMentions = []
    } catch (error) {
      streamError = error instanceof Error ? error.message : t('discussionSession.interjectFailed')
    } finally {
      interjectBusy = false
      pendingInterjection = undefined
    }
  }

  function discussionIsRunning(discussion: DiscussionViewModel) {
    return ['running', 'started'].includes(discussion.runtimeStatus.trim().toLowerCase())
  }

  function discussionRunIsActive(status: string) {
    return ['running', 'started'].includes(status.trim().toLowerCase())
  }

  function discussionFilterOptions(): Array<{ value: ConversationFilter; label: string; icon: typeof Sparkles }> {
    return [
      { value: 'all', label: t('discussionSession.filter.all'), icon: Sparkles },
      { value: 'agent', label: t('discussionSession.filter.agents'), icon: Bot },
      { value: 'group', label: t('discussionSession.filter.groups'), icon: Users },
      { value: 'person', label: t('discussionSession.filter.people'), icon: User }
    ]
  }

  function filteredDiscussionSummaries() {
    const query = discussionSearch.trim().toLocaleLowerCase($locale)
    return visibleDiscussions.filter((discussion) => {
      if (conversationFilter !== 'all' && discussionConversationKind(discussion) !== conversationFilter) return false
      if (!query) return true
      return `${discussion.title} ${discussion.lastMessage} ${discussion.status}`.toLocaleLowerCase($locale).includes(query)
    })
  }

  function discussionConversationKind(discussion: DiscussionSummaryViewModel): ConversationFilter {
    if (discussion.kind !== 'direct') return 'group'
    return discussion.directTargetKind === 'user' ? 'person' : 'agent'
  }

  function discussionInitial(discussion: DiscussionSummaryViewModel) {
    return discussion.title.trim().slice(0, 1).toUpperCase() || 'D'
  }

  function discussionAvatarClass(discussion: DiscussionSummaryViewModel) {
    const kind = discussionConversationKind(discussion)
    if (kind === 'agent') return 'bg-amber-500'
    if (kind === 'group') return 'bg-indigo-500'
    return 'bg-brand'
  }

  function buildWorkforceAvatarIdentityIndex(members: WorkforceMemberViewModel[]) {
    const index: Record<string, WorkforceAvatarIdentity> = {}
    for (const member of members) {
      const identity = {
        profileId: member.profileId,
        human: (member.agentKind || '').toLowerCase() === 'human'
      }
      const aliases = [member.profileId, member.id, member.agentInstanceId, member.userAccount.username]
      for (const alias of aliases) {
        const normalized = alias.trim()
        if (normalized) index[normalized] = identity
      }
    }
    return index
  }

  function discussionAvatarIdentity(discussion: DiscussionSummaryViewModel) {
    if (discussion.kind !== 'direct') return undefined
    const targetId = (discussion.directTargetId || discussion.directAgentId || '').trim()
    if (!targetId) return undefined
    return avatarIdentityById[targetId] ?? {
      profileId: targetId,
      human: discussion.directTargetKind === 'user'
    }
  }

  async function convertDirectDiscussionToGroup(target: DirectChatTargetOption) {
    if (!activeDiscussion?.id || addMembersMutation.isPending) return
    streamError = ''
    try {
      const converted = await addMembersMutation.mutateAsync({ discussionId: activeDiscussion.id, targetIds: [target.id] })
      if (!locallyConvertedDiscussionIds.includes(converted.id)) {
        locallyConvertedDiscussionIds = [...locallyConvertedDiscussionIds, converted.id]
      }
      streamingDiscussion = converted
      lastQueryDiscussion = converted
      memberPickerOpen = false
      queryClient.setQueryData(discussionQueryKeys.detail(converted.id), converted)
      rememberDiscussionInList(converted)
      await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.all })
    } catch (error) {
      streamError = error instanceof Error ? error.message : t('discussionSession.convertToGroupFailed')
    }
  }

  function loadMoreDiscussions() {
    if (!listQuery.hasNextPage || listQuery.isFetchingNextPage) return
    listQuery.fetchNextPage()
  }

  function onDiscussionListScroll(event: Event) {
    const target = event.currentTarget as HTMLElement
    if (target.scrollTop + target.clientHeight >= target.scrollHeight - 120) {
      loadMoreDiscussions()
    }
  }

  async function loadOlderMessages() {
    const discussion = activeDiscussion
    if (!discussion?.id || olderMessagesLoading || !discussion.messagePage?.hasMore) return
    olderMessagesLoading = true
    olderMessagesError = ''
    try {
      const returned = discussion.messagePage.returned || discussion.messages.length || discussion.messagePage.limit
      const offset = discussion.messagePage.offset + returned
      const page = await fetchDiscussionMessagePage(discussion.id, discussion.messagePage.limit || discussionPageSize, offset)
      const next = prependDiscussionMessagePage(discussion, page)
      streamingDiscussion = next
      queryClient.setQueryData(discussionQueryKeys.detail(next.id), next)
    } catch (error) {
      olderMessagesError = error instanceof Error ? error.message : t('discussionSession.loadOlderFailed')
    } finally {
      olderMessagesLoading = false
    }
  }

  async function uploadAttachment(file: File) {
    if (!activeDiscussion || uploadAttachmentMutation.isPending) return
    attachmentError = ''
    try {
      const existingDiscussionId = activeDiscussion.id || (newDiscussionMode && !selectedDirectTarget
        ? await ensureNewDiscussionPrewarm()
        : undefined)
      const uploaded = await uploadAttachmentMutation.mutateAsync({
        discussionId: existingDiscussionId || undefined,
        file,
        directTargetId: newDiscussionMode ? selectedDirectTarget?.id : undefined,
        directTargetKind: newDiscussionMode ? selectedDirectTarget?.kind : undefined,
        forceNewConversation: newDiscussionMode && forceNewConversation
      })
      const attachment = uploaded.attachment
      const baseDiscussion = uploaded.discussion ?? (existingDiscussionId
        ? { ...activeDiscussion, id: existingDiscussionId }
        : activeDiscussion)
      const next = upsertDiscussionAttachment(baseDiscussion, attachment)
      pendingAttachments = upsertPendingAttachment(pendingAttachments, attachment)
      streamingDiscussion = next
      if (next.id) {
        anchorCreatedDiscussion(next.id)
        queryClient.setQueryData(discussionQueryKeys.detail(next.id), next)
      }
      rememberDiscussionInList(next)
      await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.all })
    } catch (error) {
      attachmentError = error instanceof Error ? error.message : t('discussionSession.uploadAttachmentFailed')
    }
  }

  async function removeDiscussion(event: MouseEvent, discussion: DiscussionSummaryViewModel) {
    event.stopPropagation()
    if (!discussion.id || discussion.id === draftDiscussionSummary?.id) return
    openActionMenuDiscussionId = ''
    if (!confirm(t('discussionSession.confirmDelete', { title: discussion.title }))) return
    const deletingActiveDiscussion = selectedDiscussionId === discussion.id
    const nextDiscussionId = deletingActiveDiscussion ? discussions.find((item) => item.id !== discussion.id)?.id ?? '' : selectedDiscussionId
    try {
      await deleteDiscussionMutation.mutateAsync(discussion.id)
      removeDiscussionFromList(discussion.id)
      queryClient.removeQueries({ queryKey: discussionQueryKeys.detail(discussion.id) })
      if (deletingActiveDiscussion) {
        if (nextDiscussionId) {
          selectDiscussion(nextDiscussionId)
        } else {
          selectedDiscussionId = ''
          streamingDiscussion = undefined
          newDiscussionMode = false
          forceNewConversation = false
        }
      }
      await queryClient.invalidateQueries({ queryKey: discussionQueryKeys.all })
    } catch (error) {
      streamError = error instanceof Error ? error.message : t('discussionSession.deleteFailed')
    }
  }

  function createDraftDiscussionSummary(): DiscussionSummaryViewModel {
    return {
      id: 'draft-new-discussion',
      kind: 'group',
      directAgentId: '',
      directTargetKind: '',
      directTargetId: '',
      title: t('discussionSession.newDiscussion'),
      status: t('discussionSession.draft'),
      lastMessage: t('discussionSession.empty'),
      linkedMissionCount: 0,
      updatedAt: t('discussionSession.ready'),
      unreadCompleted: false,
      hilMode: 'manual'
    }
  }

  function createDraftDiscussion(asPersonalAssistant = false): DiscussionViewModel {
    const userName = session.displayName || session.actorId
    const members = userName
      ? [{ kind: 'user', id: session.actorId, name: userName, role: t('discussionSession.privateChatUser') }]
      : []

    return {
      id: '',
      kind: asPersonalAssistant ? 'personal_assistant' : 'group',
      directAgentId: '',
      directAgentName: '',
      directTargetKind: '',
      directTargetId: '',
      directTargetName: '',
      title: asPersonalAssistant ? t('discussionSession.personalAssistant') : t('discussionSession.newDiscussion'),
      status: t('discussionSession.draft'),
      createdBy: userName,
      createdById: session.actorId,
      runtimeStatus: t('discussionSession.ready'),
      runtimeError: undefined,
      activeResponders: [],
      updatedAt: t('discussionSession.ready'),
      workspaceRootDir: undefined,
      hilMode: 'manual',
      messages: [],
      messagePage: undefined,
      members,
      runs: [],
      timeline: [],
      linkedMissions: []
    }
  }

  function createDraftDirectDiscussionSummary(target: DirectChatTargetOption): DiscussionSummaryViewModel {
    return {
      id: 'draft-direct-discussion',
      kind: 'direct',
      directAgentId: '',
      directTargetKind: target.kind,
      directTargetId: target.id,
      title: target.name,
      status: t('discussionSession.draft'),
      lastMessage: t('discussionSession.directDraft'),
      linkedMissionCount: 0,
      updatedAt: t('discussionSession.ready'),
      unreadCompleted: false,
      hilMode: 'manual'
    }
  }

  function createDraftDirectDiscussion(target: DirectChatTargetOption): DiscussionViewModel {
    const userName = session.displayName || session.actorId
    const members = [
      { kind: target.kind, id: target.id, name: target.name, role: t('discussionSession.privateChatTarget') },
      userName ? { kind: 'user', id: session.actorId, name: userName, role: t('discussionSession.privateChatUser') } : undefined
    ].filter((member): member is DiscussionViewModel['members'][number] => Boolean(member))

    return {
      id: '',
      kind: 'direct',
      directAgentId: '',
      directAgentName: '',
      directTargetKind: target.kind,
      directTargetId: target.id,
      directTargetName: target.name,
      title: target.name,
      status: t('discussionSession.draft'),
      createdBy: userName,
      createdById: session.actorId,
      runtimeStatus: t('discussionSession.ready'),
      runtimeError: undefined,
      activeResponders: [],
      updatedAt: t('discussionSession.ready'),
      workspaceRootDir: undefined,
      hilMode: 'manual',
      messages: [],
      messagePage: undefined,
      members,
      runs: [],
      timeline: [],
      linkedMissions: []
    }
  }

  function summarizeDiscussion(discussion: DiscussionViewModel): DiscussionSummaryViewModel {
    return {
      id: discussion.id,
      kind: discussion.kind,
      directAgentId: discussion.directAgentId,
      directTargetKind: discussion.directTargetKind,
      directTargetId: discussion.directTargetId,
      title: discussion.title,
      status: discussion.status,
      lastMessage: discussion.messages.at(-1)?.content ?? '',
      linkedMissionCount: discussion.linkedMissions.length,
      attachmentCount: discussion.attachments?.length ?? 0,
      updatedAt: discussion.updatedAt,
      workspaceRootDir: discussion.workspaceRootDir,
      hilMode: discussion.hilMode,
      unreadCompleted: false
    }
  }

  function upsertDiscussionAttachment(
    discussion: DiscussionViewModel,
    attachment: NonNullable<DiscussionViewModel['attachments']>[number]
  ): DiscussionViewModel {
    const attachments = [...(discussion.attachments ?? [])]
    const existingIndex = attachments.findIndex((item) => item.id === attachment.id)
    if (existingIndex >= 0) {
      attachments[existingIndex] = attachment
    } else {
      attachments.push(attachment)
    }
    return { ...discussion, attachments }
  }

  function upsertPendingAttachment(
    attachments: NonNullable<DiscussionViewModel['attachments']>,
    attachment: NonNullable<DiscussionViewModel['attachments']>[number]
  ) {
    const next = [...attachments]
    const existingIndex = next.findIndex((item) => item.id === attachment.id)
    if (existingIndex >= 0) {
      next[existingIndex] = attachment
    } else {
      next.push(attachment)
    }
    return next
  }

  function upsertDiscussionSummary(
    discussions: DiscussionSummaryViewModel[],
    summary: DiscussionSummaryViewModel
  ) {
    return [summary, ...discussions.filter((discussion) => discussion.id !== summary.id)]
  }

  function existingDirectDiscussionForTarget(target: DirectChatTargetOption): DiscussionSummaryViewModel | undefined {
    const normalizedTargetID = target.id.trim()
    if (!normalizedTargetID) return undefined
    return discussions.find((discussion) => discussion.kind === 'direct' && discussionDirectTargetMatches(discussion, target))
  }

  async function startMissionConversation(request: MissionComposerRequest) {
    if (missionLaunchBusy || streamBusy) return
    const missionTitle = missionTitleFromPrompt(request.prompt)
    const attempt = missionLaunchAttemptFor(request)
    missionLaunchBusy = true
    streamError = ''
    try {
      const sourcePersonalAssistantConversationId = request.launchMode === 'immediate' && personalAssistantMode
        ? (selectedDiscussionId.trim() || await ensureNewDiscussionPrewarm() || '')
        : ''
      const missionId = attempt.missionId || (request.launchMode === 'scheduled'
        ? await createScheduledMission(request, missionTitle, attempt.idempotencyKey)
        : await createImmediateMission(request, missionTitle, attempt.idempotencyKey, sourcePersonalAssistantConversationId))
      missionLaunchAttempt = { ...attempt, missionId }
      const discussion = await loadMissionConversation(missionId)
      activateMissionConversation(discussion, missionId)

      if (request.launchMode === 'scheduled') {
        draft = ''
        draftMentions = []
        pendingAttachments = []
        missionLaunchAttempt = undefined
        return
      }

      missionLaunchBusy = false
      await sendMessage(request.prompt)
      missionLaunchAttempt = undefined
    } catch (error) {
      streamError = error instanceof Error && error.message
        ? error.message
        : t('discussionSession.missionLaunchFailed')
      throw error
    } finally {
      missionLaunchBusy = false
    }
  }

  async function createImmediateMission(request: MissionComposerRequest, title: string, idempotencyKey: string, sourcePersonalAssistantConversationId = '') {
    const missionId = await createMissionConversation({
      title,
      finalGoal: request.prompt.trim(),
      idempotencyKey,
      sourcePersonalAssistantConversationId
    })
    if (!missionId) throw new Error(t('discussionSession.missionLaunchFailed'))
    await queryClient.invalidateQueries({ queryKey: missionQueryKeys.all })
    return missionId
  }

  async function createScheduledMission(request: MissionComposerRequest, title: string, idempotencyKey: string) {
    const runAt = new Date(request.scheduledAt)
    if (Number.isNaN(runAt.getTime()) || runAt.getTime() <= Date.now()) {
      throw new Error(t('discussionSession.missionScheduleInvalid'))
    }
    const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
    const missionId = await createScheduledMissionRecord({
      title,
      description: request.prompt.trim(),
      runAt: runAt.toISOString(),
      timezone,
      agentId: missionCoordinatorAgentId,
      taskGoal: request.prompt.trim(),
      prompt: request.prompt.trim(),
      idempotencyKey
    })
    if (!missionId) throw new Error(t('discussionSession.missionDiscussionUnavailable'))
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: schedulerQueryKeys.root }),
      queryClient.invalidateQueries({ queryKey: missionQueryKeys.all })
    ])
    return missionId
  }

  async function loadMissionConversation(missionId: string) {
    const conversationId = await resolveMissionConversationId(missionId)
    if (!conversationId) throw new Error(t('discussionSession.missionDiscussionUnavailable'))
    const dto = await discussionApi.getDiscussion(conversationId, { limit: discussionPageSize }) as DiscussionDTO
    return mapDiscussionView(dto)
  }

  function activateMissionConversation(discussion: DiscussionViewModel, missionId: string) {
    selectDiscussion(discussion.id, false)
    streamingDiscussion = discussion
    activeMissionId = missionId
    lastQueryDiscussion = discussion
    queryClient.setQueryData(discussionQueryKeys.detail(discussion.id), discussion)
    rememberDiscussionInList(discussion)
  }

  function missionLaunchAttemptFor(request: MissionComposerRequest) {
    const fingerprint = [
      request.launchMode,
      request.prompt.trim(),
      request.launchMode === 'scheduled' ? request.scheduledAt.trim() : ''
    ].join('\u0000')
    if (missionLaunchAttempt?.fingerprint === fingerprint) return missionLaunchAttempt
    const attempt: MissionLaunchAttempt = {
      fingerprint,
      idempotencyKey: createIdempotencyKey(),
      missionId: ''
    }
    missionLaunchAttempt = attempt
    return attempt
  }

  function missionTitleFromPrompt(prompt: string) {
    const normalized = prompt.replace(/\s+/g, ' ').trim()
    const firstClause = normalized.split(/[。！？!?；;]/, 1)[0] || normalized
    return firstClause.length > 20 ? `${firstClause.slice(0, 20)}…` : firstClause
  }

  function beginMissionCreate() {
    const prompt = t('discussionSession.createMissionPrompt')
    updateDraft(draft.trim() ? `${draft.trimEnd()}\n${prompt}` : prompt)
  }

  function resolveActiveAgentSessionTarget(): AgentSessionTarget | undefined {
    if (personalAssistantMode) return { kind: 'personal_assistant' }

    const summary = discussions.find((discussion) => discussion.id === selectedDiscussionId)
    const directTargetKind = selectedDirectTarget?.kind
      || activeDiscussion?.directTargetKind
      || summary?.directTargetKind
      || (activeDiscussion?.directAgentId || summary?.directAgentId ? 'agent' : '')
    const directTargetId = (
      selectedDirectTarget?.id
      || activeDiscussion?.directTargetId
      || activeDiscussion?.directAgentId
      || summary?.directTargetId
      || summary?.directAgentId
      || ''
    ).trim()
    if (directTargetKind !== 'agent' || !directTargetId) return undefined

    const workforceTarget = directChatTargetOptions.find((target) => target.kind === 'agent' && target.id === directTargetId)
    if (workforceTarget) {
      return {
        kind: 'agent',
        id: workforceTarget.id,
        name: workforceTarget.name,
        role: workforceTarget.role,
        runtimeKind: workforceTarget.runtimeKind
      }
    }
    return {
      kind: 'agent',
      id: directTargetId,
      name: selectedDirectTarget?.name || activeDiscussion?.directTargetName || activeDiscussion?.directAgentName || summary?.title || directTargetId,
      role: selectedDirectTarget?.role || '',
      runtimeKind: selectedDirectTarget?.runtimeKind || ''
    }
  }

  function agentSessionsForTarget(target: AgentSessionTarget | undefined) {
    if (!target) return []
    if (target.kind === 'personal_assistant') return personalAssistantDiscussions
    return discussions.filter((discussion) => {
      if (discussion.kind !== 'direct') return false
      const directTargetId = (discussion.directTargetId || discussion.directAgentId || '').trim()
      const directTargetKind = discussion.directTargetKind || (discussion.directAgentId ? 'agent' : '')
      return directTargetKind === 'agent' && directTargetId === target.id
    })
  }

  function selectAgentSession(discussionId: string, select?: HTMLSelectElement) {
    if (discussionId === '__load_more_sessions__') {
      if (select) select.value = activeAgentSessionValue
      if (listQuery.hasNextPage && !listQuery.isFetchingNextPage) void listQuery.fetchNextPage()
      return
    }
    if (!discussionId || discussionId === '__new_session__') return
    const discussion = activeAgentSessions.find((candidate) => candidate.id === discussionId)
    if (discussion) selectDiscussionSummary(discussion)
  }

  function agentSessionOptionLabel(discussion: DiscussionSummaryViewModel, index: number) {
    const preview = discussion.lastMessage.replace(/\s+/g, ' ').trim()
    const label = preview ? (preview.length > 24 ? `${preview.slice(0, 24)}…` : preview) : `${t('discussionSession.session')} ${index + 1}`
    return `${label} · ${discussion.updatedAt}`
  }

  function isPersonalAssistantDiscussion(discussion: Pick<DiscussionSummaryViewModel, 'title' | 'kind'>) {
    if (discussion.kind === 'direct') return false
    if (discussion.kind === 'personal_assistant') return true
    const title = discussion.title.trim().toLocaleLowerCase()
    return new Set([
      t('discussionSession.personalAssistant').trim().toLocaleLowerCase(),
      'my assistant',
      'personal assistant',
      '我的助理',
      '个人助理'
    ]).has(title)
  }

  function isPersonalAssistantDiscussionId(discussionId: string) {
    return discussions.some((discussion) => discussion.id === discussionId && isPersonalAssistantDiscussion(discussion))
  }

  function discussionListIdentity(
    discussion: Pick<DiscussionSummaryViewModel, 'id' | 'kind' | 'directAgentId' | 'directTargetKind' | 'directTargetId'>
  ) {
    if (discussion.kind !== 'direct') return `discussion:${discussion.id}`
    const targetId = (discussion.directTargetId || discussion.directAgentId || '').trim()
    if (!targetId) return `discussion:${discussion.id}`
    const targetKind = discussion.directTargetKind || (discussion.directAgentId ? 'agent' : 'unknown')
    return `direct:${targetKind}:${targetId}`
  }

  function collapseDiscussionListSessions(items: DiscussionSummaryViewModel[]) {
    const seen = new Set<string>()
    return items.filter((discussion) => {
      const identity = discussionListIdentity(discussion)
      if (seen.has(identity)) return false
      seen.add(identity)
      return true
    })
  }

  function discussionListItemSelected(discussion: DiscussionSummaryViewModel) {
    if (newDiscussionMode && draftDiscussionSummary?.id === discussion.id) return true
    const selected = newDiscussionMode
      ? activeDiscussion
      : discussions.find((candidate) => candidate.id === selectedDiscussionId)
    return Boolean(selected && discussionListIdentity(selected) === discussionListIdentity(discussion))
  }

  function selectedPersonalAssistantOwnedAgent(): PersonalAssistantOwnedAgentViewModel | undefined {
    const ownedAgentId = personalAssistantQuery.data?.ownedAgentId.trim()
    if (!ownedAgentId) return undefined
    return personalAssistantQuery.data?.ownedAgents.find((agent) => agent.id === ownedAgentId)
  }

  function personalAssistantRuntimeLabel() {
    if (personalAssistantQuery.isPending) return t('discussionSession.personalAssistantSyncing')
    if (!personalAssistantEnabled) return t('discussionSession.personalAssistantDisabled')
    if (personalAssistantQuery.data?.hostingMode !== 'owned_agent') return t('discussionSession.personalAssistantManaged')
    if (!personalAssistantOwnedAgent) return t('discussionSession.personalAssistantUnavailable')
    return personalAssistantOwnedAgent.displayName || personalAssistantOwnedAgent.runtimeKind
  }

  function isAbortError(error: unknown) {
    return error instanceof Error && error.name === 'AbortError'
  }

  function discussionDirectTargetMatches(discussion: DiscussionSummaryViewModel, target: DirectChatTargetOption) {
    const targetId = target.id.trim()
    const discussionTargetId = (discussion.directTargetId || discussion.directAgentId || '').trim()
    return targetId !== '' && discussionTargetId === targetId
  }

  function directChatTargetOptionFromMember(member: WorkforceMemberViewModel): DirectChatTargetOption | undefined {
    if (!member.enabled) return undefined
    const agentKind = (member.agentKind || '').toLowerCase()
    if (agentKind !== 'human' && agentKind !== 'ai' && agentKind !== 'agent') return undefined
    const id = (member.profileId || member.id).trim()
    if (!id || isCurrentUserWorkforceMember(member)) return undefined
    return {
      id,
      kind: agentKind === 'human' ? 'user' : 'agent',
      name: member.displayName || member.realName || id,
      role: member.primaryRoleLabel || member.roleName || member.positionDisplayName || '',
      runtimeKind: member.runtimeKind || ''
    }
  }

  function isCurrentUserWorkforceMember(member: WorkforceMemberViewModel) {
    if ((member.agentKind || '').toLowerCase() !== 'human') return false
    return [member.profileId, member.id, member.agentInstanceId, member.userAccount.username]
      .map((value) => value.trim())
      .some((value) => isCurrentSessionUserIdentity(value))
  }
</script>

<div class="relative flex h-full min-h-0 overflow-hidden bg-white" data-discussion-workspace data-visual-reference="figma-chat">
  {#if discussionListCollapsed}
    <button
      type="button"
      class="flex w-8 shrink-0 items-center justify-center border-r border-border bg-muted text-muted-foreground hover:bg-muted/80"
      onclick={() => (discussionListCollapsed = false)}
      aria-label={t('sidebar.open')}
      title={t('sidebar.open')}
    >
      <ChevronRight class="h-4 w-4" />
    </button>
  {:else}
    <aside class="absolute inset-y-0 left-0 z-30 flex w-[280px] max-w-full shrink-0 flex-col overflow-hidden border-r border-[#ececec] bg-white shadow-xl md:static md:w-[280px] md:z-auto md:shadow-none" aria-label={t('nav.discussions')} data-discussion-list-panel>
      <div class="shrink-0 bg-white">
        <div class="flex items-center justify-between px-4 pb-2 pt-4">
          <div class="flex min-w-0 items-center gap-2">
            <button type="button" class="text-[#8a8a8a] hover:text-black md:hidden" onclick={() => (discussionListCollapsed = true)} aria-label={t('sidebar.collapse')}>
              <ChevronLeft class="h-4 w-4" />
            </button>
            <h1 class="min-w-0 truncate text-[15px] font-semibold text-black">{t('nav.discussions')}</h1>
          </div>
          <div class="flex items-center gap-1">
            <button type="button" class="flex h-7 w-7 items-center justify-center rounded-lg text-[#8a8a8a] hover:bg-[#f0f0f0]" onclick={toggleDirectPicker} aria-label={t('discussionSession.newDirectChat')} title={t('discussionSession.newDirectChat')}>
              <MessageCircle class="h-4 w-4" />
            </button>
            <button type="button" class="flex h-7 w-7 items-center justify-center rounded-lg text-[#8a8a8a] hover:bg-[#f0f0f0]" onclick={startNewDiscussion} aria-label={t('discussionSession.newDiscussion')} title={t('discussionSession.newDiscussion')}>
              <Plus class="h-4 w-4" />
            </button>
          </div>
        </div>

        <div class="px-3 pb-3" data-personal-assistant-entry>
          <div class="group relative overflow-hidden rounded-lg border border-emerald-200/80 bg-emerald-50/70 {personalAssistantMode ? 'ring-1 ring-emerald-300' : ''}">
            <button
              type="button"
              class="flex w-full items-center gap-2.5 px-2.5 py-2.5 pr-12 text-left transition-colors hover:bg-emerald-50 {personalAssistantEnabled ? '' : 'opacity-65'}"
              onclick={startPersonalAssistantDiscussion}
              aria-label={t('discussionSession.personalAssistant')}
            >
              <span class="relative grid h-10 w-10 shrink-0 place-items-center overflow-hidden rounded-full bg-emerald-600 text-white">
                {#if personalAssistantAvatarUrl}
                  <img class="h-full w-full object-cover" src={personalAssistantAvatarUrl} alt={t('discussionSession.personalAssistant')} />
                {:else}
                  <Bot class="h-5 w-5" />
                {/if}
                <span class="absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-white {personalAssistantEnabled ? 'bg-emerald-400' : 'bg-[#b0b0b0]'}"></span>
              </span>
              <span class="min-w-0 flex-1">
                <span class="flex min-w-0 items-center gap-1.5">
                  <span class="truncate text-[13.5px] font-semibold text-[#163a32]">{t('discussionSession.personalAssistant')}</span>
                  {#if personalAssistantMissionCount > 0}
                    <span class="shrink-0 rounded bg-white/80 px-1.5 py-0.5 text-[10px] font-medium text-emerald-700">
                      {t('discussionSession.personalAssistantMissionCount', { count: personalAssistantMissionCount })}
                    </span>
                  {/if}
                </span>
                <span class="mt-0.5 block truncate text-[11.5px] text-emerald-800/70">
                  {personalAssistantDiscussion?.lastMessage || personalAssistantRuntime}
                </span>
              </span>
            </button>
            <div class="absolute right-1.5 top-3">
              <a
                href="/profile#personal-assistant"
                class="grid h-7 w-7 place-items-center rounded-md text-emerald-800/65 transition hover:bg-white/80 hover:text-emerald-800"
                aria-label={t('discussionSession.personalAssistantSettings')}
                title={t('discussionSession.personalAssistantSettings')}
              >
                <Settings2 class="h-3.5 w-3.5" />
              </a>
            </div>
          </div>
        </div>

        <div class="flex items-center gap-2 border-t border-[#ececec] px-4 pb-2 pt-3 text-[11px] font-medium text-[#8a8a8a]">
          <span>{t('discussionSession.recentConversations')}</span>
        </div>

        <div class="px-3 pb-2">
          <label class="flex items-center gap-2 rounded-lg border border-[#ececec] bg-[#fafafa] px-2.5 py-1.5 text-[#8a8a8a]">
            <Search class="h-3.5 w-3.5" />
            <input
              bind:value={discussionSearch}
              type="search"
              aria-label={t('discussionSession.searchContacts')}
              placeholder={t('discussionSession.searchContacts')}
              class="w-full bg-transparent text-[13px] text-black outline-none placeholder:text-[#a0a0a0]"
            />
          </label>
        </div>

        <div class="flex gap-1 px-3 pb-2">
          {#each discussionFilterOptions() as option}
            {@const Icon = option.icon}
            <button
              type="button"
              class="flex flex-1 items-center justify-center gap-1 rounded-lg py-1.5 text-[12px] transition-colors {conversationFilter === option.value ? 'bg-brand text-white' : 'text-[#6a6a6a] hover:bg-[#f0f0f0]'}"
              aria-label={option.label}
              title={option.label}
              onclick={() => (conversationFilter = option.value)}
            >
              <Icon class="h-3.5 w-3.5" />
            </button>
          {/each}
        </div>
      </div>

      {#if directPickerOpen}
        <div class="border-b border-border bg-card p-2">
          <div class="mb-2 flex items-center justify-between gap-2 px-1">
            <span class="truncate text-xs font-medium text-foreground">{t('discussionSession.chooseDirectAgent')}</span>
            <button
              type="button"
              class="grid h-6 w-6 shrink-0 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground"
              onclick={() => (directPickerOpen = false)}
              aria-label={t('discussionSession.closeDirectPicker')}
            >
              <X class="h-3.5 w-3.5" />
            </button>
          </div>
          {#if workforceMembersQuery.isLoading}
            <div class="rounded-md border border-border px-2 py-2 text-xs text-muted-foreground">{t('discussionSession.agentPickerLoading')}</div>
          {:else if workforceMembersQuery.isError}
            <div class="rounded-md border border-destructive/30 bg-destructive/10 px-2 py-2 text-xs text-destructive">{t('discussionSession.agentPickerFailed')}</div>
          {:else if directChatTargetOptions.length === 0}
            <div class="rounded-md border border-dashed border-border px-2 py-2 text-xs text-muted-foreground">{t('discussionSession.agentPickerEmpty')}</div>
          {:else}
            <div class="subtle-scrollbar max-h-64 space-y-1 overflow-auto">
              {#each directChatTargetOptions as target (target.id)}
                <button
                  type="button"
                  class="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left hover:bg-muted"
                  onclick={() => startDirectDiscussion(target)}
                >
                  <WorkforceAvatar profileId={target.id} name={target.name} human={target.kind === 'user'} class="h-10 w-10 rounded-full" textClass="text-[11px]" />
                  <span class="min-w-0 flex-1">
                    <span class="block truncate text-sm font-medium text-foreground">{target.name}</span>
                    <span class="block truncate text-[10px] text-muted-foreground">{target.role || target.runtimeKind}</span>
                  </span>
                </button>
              {/each}
            </div>
          {/if}
        </div>
      {/if}

      {#if listQuery.isLoading}
        <LoadingState title={t('discussionSession.loading')} description="" />
      {:else if listQuery.isError}
        {@const error = listQuery.error as ApiClientError}
        <ErrorState title={t('discussionSession.loadFailed')} message={error.message} correlationId={error.correlationId} />
      {:else}
        <div class="subtle-scrollbar min-h-0 flex-1 overflow-auto bg-white px-2" data-discussion-list-scroller onscroll={onDiscussionListScroll}>
          {#each filteredVisibleDiscussions as discussion}
            {@const avatarIdentity = discussionAvatarIdentity(discussion)}
            <div
              class="group relative mb-1 overflow-visible rounded-lg transition-colors {discussionListItemSelected(discussion) ? 'selection-wash' : 'hover:bg-[#f5f5f5]'}"
              data-discussion-list-item={discussion.id}
              data-personal-assistant-history={isPersonalAssistantDiscussion(discussion) ? 'true' : undefined}
            >
              <button class="flex w-full items-center gap-2.5 px-2 py-2 pr-9 text-left" type="button" onclick={() => selectDiscussionSummary(discussion)}>
                <span class="relative h-10 w-10 shrink-0">
                  {#if avatarIdentity}
                    <WorkforceAvatar
                      profileId={avatarIdentity.profileId}
                      name={discussion.title}
                      human={avatarIdentity.human}
                      class="h-10 w-10 rounded-full"
                      textClass="text-[13px]"
                    />
                  {:else if isPersonalAssistantDiscussion(discussion)}
                    <span class="flex h-10 w-10 items-center justify-center rounded-full bg-emerald-600 text-white">
                      <Bot class="h-5 w-5" />
                    </span>
                  {:else}
                    <span class="flex h-10 w-10 items-center justify-center rounded-full text-[13px] font-medium text-white {discussionAvatarClass(discussion)}">
                      {discussionInitial(discussion)}
                    </span>
                  {/if}
                  {#if discussion.status === 'running' || discussion.status === t('discussionSession.draft')}
                    <span class="absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-white bg-emerald-400"></span>
                  {/if}
                </span>
                <div class="min-w-0 flex-1" data-discussion-list-title-row>
                  <div class="flex items-center justify-between gap-2">
                    <span class="min-w-0 truncate text-[13.5px] font-medium text-black">{discussion.title}</span>
                    <span class="shrink-0 text-[11px] text-[#a0a0a0]">{discussion.updatedAt}</span>
                  </div>
                  <div class="flex items-center justify-between gap-2">
                    <span class="min-w-0 truncate text-[12px] text-[#8a8a8a]">{discussion.lastMessage || t('discussionSession.empty')}</span>
                    {#if discussion.unreadCompleted}
                      <span class="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-brand px-1 text-[10px] font-medium text-white">1</span>
                    {/if}
                  </div>
                </div>
              </button>
              {#if discussion.id !== draftDiscussionSummary?.id}
                <button
                  type="button"
                  class="absolute right-1.5 top-2 flex h-7 w-7 items-center justify-center rounded-lg text-[#a0a0a0] opacity-100 transition hover:bg-white hover:text-black md:opacity-0 md:group-hover:opacity-100 md:focus:opacity-100"
                  onclick={(event) => toggleDiscussionActionMenu(event, discussion.id)}
                  aria-label={t('discussionSession.openActions')}
                  title={t('discussionSession.openActions')}
                >
                  <MoreHorizontal class="h-4 w-4" />
                </button>
                {#if openActionMenuDiscussionId === discussion.id}
                  <div class="absolute right-1.5 top-9 z-20 w-36 rounded-lg border border-[#ececec] bg-white p-1 shadow-lg">
                    <button
                      type="button"
                      class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12px] text-rose-600 hover:bg-rose-50 disabled:opacity-50"
                      onclick={(event) => removeDiscussion(event, discussion)}
                      disabled={deleteDiscussionMutation.isPending}
                      aria-label={t('discussionSession.deleteDiscussion')}
                    >
                      <Trash2 class="h-3.5 w-3.5" />
                      <span>{t('discussionSession.deleteDiscussion')}</span>
                    </button>
                  </div>
                {/if}
              {/if}
            </div>
          {:else}
            <div class="rounded-md border border-dashed border-[#e0e0e0] p-3 text-sm text-[#8a8a8a]">{t('discussionSession.empty')}</div>
          {/each}
          {#if filteredVisibleDiscussions.length > 0}
            {#if listQuery.isFetchingNextPage}
              <div class="py-3 text-center text-xs text-muted-foreground">{t('discussionSession.loadingMore')}</div>
            {:else if !listQuery.hasNextPage}
              <div class="py-3 text-center text-xs text-muted-foreground">{t('discussionSession.noMore')}</div>
            {/if}
          {/if}
        </div>
      {/if}
    </aside>
  {/if}

  <section class="min-h-0 min-w-0 flex-1" data-discussion-message-panel>
    {#if selectedDiscussionId && discussionQuery.isLoading && !activeDiscussion}
      <LoadingState title={t('discussionSession.loading')} description="" />
    {:else if selectedDiscussionId && discussionQuery.isError && !activeDiscussion}
      {@const error = discussionQuery.error as ApiClientError}
      <ErrorState title={t('discussionSession.loadFailed')} message={error.message} correlationId={error.correlationId} />
    {:else}
      {#if workspaceEditable && workspaceSettingsOpen}
        <div class="border-b border-border bg-card px-3 py-2">
          <div class="flex min-w-0 items-center gap-2">
            <div class="flex min-w-0 items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <span class="shrink-0">{t('discussionSession.workspace')}</span>
            </div>
            <Input
              value={workspaceDraft}
              disabled={workspaceMutation.isPending}
              ariaLabel={t('discussionSession.workspacePath')}
              placeholder={t('discussionSession.workspacePlaceholder')}
              oninput={(event) => (workspaceDraft = event.currentTarget.value)}
              class="h-8 min-w-0 flex-1 font-mono text-xs"
            />
            <Button
              variant="outline"
              size="icon"
              disabled={workspaceMutation.isPending}
              onclick={() => void saveWorkspaceRoot()}
              ariaLabel={t('discussionSession.saveWorkspace')}
              class="h-8 w-8 shrink-0"
              title={t('discussionSession.saveWorkspace')}
            >
              <Save class="h-3.5 w-3.5" />
            </Button>
            <button
              type="button"
              class="grid h-8 w-8 shrink-0 place-items-center rounded border border-border text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
              disabled={workspaceMutation.isPending || (workspaceDraft.trim().length === 0 && !activeDiscussion?.workspaceRootDir)}
              onclick={() => {
                workspaceDraft = ''
                void saveWorkspaceRoot('')
              }}
              aria-label={t('discussionSession.clearWorkspace')}
              title={t('discussionSession.clearWorkspace')}
            >
              <X class="h-3.5 w-3.5" />
            </button>
          </div>
          {#if workspaceError}
            <div class="mt-2 rounded-md border border-destructive/30 bg-destructive/10 px-2 py-1.5 text-xs text-destructive">{workspaceError}</div>
          {/if}
        </div>
      {/if}
      {#if memberPickerOpen}
        <div class="border-b border-border bg-card px-3 py-2">
          <div class="mb-2 flex items-center justify-between gap-2">
            <span class="truncate text-xs font-medium text-foreground">{t('discussionSession.chooseGroupMember')}</span>
            <button
              type="button"
              class="grid h-6 w-6 shrink-0 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground"
              onclick={() => (memberPickerOpen = false)}
              aria-label={t('discussionSession.closeDirectPicker')}
            >
              <X class="h-3.5 w-3.5" />
            </button>
          </div>
          {#if workforceMembersQuery.isLoading}
            <div class="rounded-md border border-border px-2 py-2 text-xs text-muted-foreground">{t('discussionSession.agentPickerLoading')}</div>
          {:else if workforceMembersQuery.isError}
            <div class="rounded-md border border-destructive/30 bg-destructive/10 px-2 py-2 text-xs text-destructive">{t('discussionSession.agentPickerFailed')}</div>
          {:else if groupConversionTargetOptions.length === 0}
            <div class="rounded-md border border-dashed border-border px-2 py-2 text-xs text-muted-foreground">{t('discussionSession.groupMemberPickerEmpty')}</div>
          {:else}
            <div class="subtle-scrollbar flex max-h-24 gap-1 overflow-auto">
              {#each groupConversionTargetOptions as target (target.id)}
                <button
                  type="button"
                  class="flex min-w-44 items-center gap-2 rounded-md px-2 py-2 text-left hover:bg-muted disabled:opacity-50"
                  disabled={addMembersMutation.isPending}
                  onclick={() => convertDirectDiscussionToGroup(target)}
                >
                  <WorkforceAvatar profileId={target.id} name={target.name} human={target.kind === 'user'} class="h-10 w-10 rounded-full" textClass="text-[11px]" />
                  <span class="min-w-0 flex-1">
                    <span class="block truncate text-sm font-medium text-foreground">{target.name}</span>
                    <span class="block truncate text-[10px] text-muted-foreground">{target.role || target.runtimeKind}</span>
                  </span>
                </button>
              {/each}
            </div>
          {/if}
        </div>
      {/if}
      <DiscussionPanel
        discussion={activeDiscussion}
        assistantMode={personalAssistantMode}
        assistantEnabled={personalAssistantEnabled}
        assistantAvatarUrl={personalAssistantAvatarUrl}
        assistantRuntimeLabel={personalAssistantRuntime}
        assistantMissionCount={personalAssistantMissionCount}
        assistantPendingApprovalCount={missionApprovalsQuery.data?.items.length ?? 0}
        {avatarIdentityById}
        {draft}
        busy={streamBusy || missionLaunchBusy}
        errorMessage={streamError}
        debugOpen={debugOpen}
        debug={debugQuery.data}
        debugRuns={activeDiscussion?.runs ?? []}
        selectedDebugRunId={selectedDebugRunId}
        debugLoading={debugQuery.isLoading}
        debugError={debugQuery.isError ? ((debugQuery.error as ApiClientError).message ?? t('discussionSession.debugLoadFailed')) : ''}
        missionContext={missionContextQuery.data}
        missionOps={missionOpsQuery.data}
        bind:activeMissionId
        missionContextLoading={Boolean(activeMissionId) && missionContextQuery.isLoading}
        missionContextError={missionContextQuery.isError ? ((missionContextQuery.error as ApiClientError).message ?? t('discussionSession.loadFailed')) : ''}
        missionOpsLoading={Boolean(activeMissionId) && missionOpsQuery.isLoading}
        missionOpsError={missionOpsQuery.isError ? ((missionOpsQuery.error as ApiClientError).message ?? t('discussionSession.loadFailed')) : ''}
        pendingApprovals={missionApprovalsQuery.data?.items ?? []}
        pendingAttachments={pendingAttachments}
        attachmentEnabled={Boolean(activeDiscussion)}
        attachmentUploading={uploadAttachmentMutation.isPending}
        attachmentError={attachmentError}
        olderMessagesLoading={olderMessagesLoading}
        olderMessagesHasMore={olderMessagesHasMore}
        olderMessagesError={olderMessagesError}
        approvalBusyIds={approvalBusyIds}
        taskTimelineCollapsed={taskTimelineCollapsed}
        workspaceSettingsAvailable={workspaceEditable}
        hilModeBusy={hilModeMutation.isPending}
        hilModeEditable={isCurrentSessionUserIdentity(activeDiscussion?.createdById)}
        onDraft={updateDraft}
        onComposerFocus={prewarmActiveDiscussion}
        onMention={addDraftMention}
        onSend={sendMessage}
        onRetryMessage={retryMessage}
        onDebugOpen={openDebug}
        onDebugRunSelect={selectDebugRun}
        onDebugClose={() => (debugOpen = false)}
        onTaskTimelineToggle={() => (taskTimelineCollapsed = !taskTimelineCollapsed)}
        onMissionStart={startMissionConversation}
        onNewSession={newAgentSessionEnabled ? startFreshAgentSession : undefined}
        onWorkspaceSettingsOpen={() => (workspaceSettingsOpen = !workspaceSettingsOpen)}
        onHILModeChange={setHILMode}
        onConvertToGroup={openMemberPicker}
        onApprovalAction={openApprovalDecision}
        onAttachmentUpload={uploadAttachment}
        onLoadOlderMessages={loadOlderMessages}
        onAbort={abortActiveDiscussion}
        abortBusy={abortDiscussionMutation.isPending}
        {interjectBusy}
        pendingInterjection={pendingInterjection?.discussionId === activeDiscussion?.id ? pendingInterjection : undefined}
      >
        {#snippet headerLeadingActions()}
          {#if activeAgentSessionTarget}
            <label class="flex h-8 items-center gap-1.5 rounded-md border border-[#dedede] bg-white px-2 text-[11px] text-[#777] transition-colors focus-within:border-brand/35" data-agent-session-select>
              <span class="font-medium text-[#555]">{t('discussionSession.session')}</span>
              <select
                value={activeAgentSessionValue}
                aria-label={t('discussionSession.switchSession')}
                title={t('discussionSession.switchSession')}
                class="h-full max-w-40 cursor-pointer bg-transparent pr-1 text-xs text-black outline-none"
                onchange={(event) => selectAgentSession(event.currentTarget.value, event.currentTarget)}
              >
                {#if newDiscussionMode}
                  <option value="__new_session__">{t('discussionSession.newSessionDraft')}</option>
                {:else if activeDiscussion?.id && !activeAgentSessions.some((discussion) => discussion.id === activeDiscussion.id)}
                  <option value={activeDiscussion.id}>{activeDiscussion.title} · {activeDiscussion.updatedAt}</option>
                {/if}
                {#each activeAgentSessions as discussion, index (discussion.id)}
                  <option value={discussion.id}>{agentSessionOptionLabel(discussion, index)}</option>
                {/each}
                {#if listQuery.hasNextPage}
                  <option value="__load_more_sessions__">{t('discussionSession.sessionLoadMore')}</option>
                {/if}
              </select>
            </label>
          {/if}
        {/snippet}
      </DiscussionPanel>
    {/if}
  </section>

</div>
