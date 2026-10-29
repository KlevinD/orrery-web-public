<script lang="ts">
  import WorkInbox from '#lib/components/operations/WorkInbox.svelte'
  import HistoryWindowFilter from '#lib/components/shared/HistoryWindowFilter.svelte'
  import type { HistoryWindow } from '#lib/queries/history-window'
  import { Bell } from 'lucide-svelte'
  import { AlertTriangle, Boxes, CalendarClock, CheckCircle2, ChevronRight, CircleAlert, ClipboardList, ExternalLink, FolderKanban, ListChecks, ListTodo, LoaderCircle, MessageSquare, Users, Workflow, X } from 'lucide-svelte'
  import ObsidianForceGraph, { type ObsidianGraphLink, type ObsidianGraphNode } from '#lib/components/graph/ObsidianForceGraph.svelte'
  import ConversationSwitchPanel from '#lib/components/layout/ConversationSwitchPanel.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { objectTypeLabel, ontologyValueLabel } from '#lib/ontology-workbench/display-labels'
  import {
    useNotificationApprovalBadgeCountsQuery,
    useNotificationDiscussionBadgeCountsQuery
  } from '#lib/queries/notification-badges'
  import { useMissionListQuery } from '#lib/queries/missions'
  import { useOntologyContextCompanyOverviewQuery, useOntologyContextNeighborhoodQuery } from '#lib/queries/ontology-context'
  import { useScheduledRunInboxQuery, useScheduledTasksQuery } from '#lib/queries/scheduler'
  import { useTodoListQuery } from '#lib/queries/todos'
  import { useWorkforceMembersQuery } from '#lib/queries/workforce'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { hasRouteAccess } from '#lib/routes/navigation'
  import { currentSessionUserIdentityIds, session } from '#lib/stores/session.svelte'
  import { discussionWorkspaceContext } from '#lib/discussion-workspace-context.svelte'
  import { conversationSwitchContext } from '#lib/conversation-switch-context.svelte'
  import type { ScheduledRunInboxItemDTO, ScheduledTaskSummaryDTO } from '#lib/types/scheduler'
  import type { TodoItemDTO, TodoStatus } from '#lib/types/todo'
  import type { ApprovalItemViewModel } from '#lib/types/view-models'
  import type { TranslationKey } from '#lib/i18n'
  import {
    buildMissionHistoryList,
    buildMissionHistorySections,
    filterCompletedMissionHistory,
    type MissionHistoryItem,
    type MissionHistorySectionKey
  } from './mission-history-list'

  type ContextView = 'notifications' | 'results' | 'missions' | 'automation' | 'people' | 'conversations'
  interface Props {
    onClose?: () => void
    showClose?: boolean
  }

  let { onClose = () => {}, showClose = false }: Props = $props()
  let activeView = $state<ContextView>('notifications')
  let historyWindow = $state<HistoryWindow>('today')
  let historyNow = $state(Date.now())
  let lastDiscussionId = $state('')
  let lastAssistantMode = $state(false)
  let lastConversationSwitchAvailable = $state(false)
  let ontologyFooterOpen = $state(true)
  let t = $derived(createTranslator($locale))
  let canReadTodos = $derived(hasPermissionScope(session.permissionScope, 'todo.read'))
  let canReadMissions = $derived(hasRouteAccess('/missions', session.permissionScope))
  let canReadScheduler = $derived(hasRouteAccess('/scheduled-tasks', session.permissionScope))
  let canReadPeople = $derived(hasRouteAccess('/people', session.permissionScope))
  let canReadDiscussions = $derived(hasRouteAccess('/discussions', session.permissionScope))
  let canReadApprovals = $derived(hasRouteAccess('/approvals', session.permissionScope))
  let canReadOntology = $derived(hasRouteAccess('/resources/ontology', session.permissionScope))
  const approvalBadgeCountsQuery = useNotificationApprovalBadgeCountsQuery(() => canReadApprovals && activeView === 'missions')
  const discussionBadgeCountsQuery = useNotificationDiscussionBadgeCountsQuery(() => false)
  const todosQuery = useTodoListQuery(
    () => ({ mine: true, includeCompleted: false, limit: 20, offset: 0 }),
    () => false
  )
  const missionsQuery = useMissionListQuery(
    () => ({ limit: 100, offset: 0 }),
    () => canReadMissions && activeView === 'missions',
    () => activeView === 'missions' ? 15_000 : false
  )
  const scheduledTasksQuery = useScheduledTasksQuery(
    () => ({ sort: 'next_run_asc', limit: 20, offset: 0 }),
    () => canReadScheduler && activeView === 'automation'
  )
  const scheduledRunAlertsQuery = useScheduledRunInboxQuery(
    () => ({ attention: true, readState: 'unread', limit: 3, offset: 0 }),
    () => false
  )
  const resultNotificationsQuery = useScheduledRunInboxQuery(
    () => ({ readState: 'unread', limit: 5, offset: 0 }),
    () => false,
    () => 30_000
  )
  let resultUnreadCount = $derived(canReadScheduler ? resultNotificationsQuery.data?.unread_total ?? 0 : 0)
  const workforceQuery = useWorkforceMembersQuery(
    () => ({ enabledOnly: true, includeDetails: false, limit: 8, offset: 0 }),
    () => canReadPeople && activeView === 'people'
  )
  const ontologyOverviewQuery = useOntologyContextCompanyOverviewQuery(
    () => canReadOntology && ontologyFooterOpen
  )
  let ontologyOverview = $derived(ontologyOverviewQuery.data?.data)
  let ontologyDomains = $derived((ontologyOverview?.domains ?? []).filter((domain) => domain.objects.status !== 'exact' || (domain.objects.value ?? 0) > 0))
  let ontologyRecentObjects = $derived(ontologyOverview?.recent_objects ?? [])
  let ontologyGraphRoot = $derived(ontologyRecentObjects.find((item) => Boolean(item.tenant_id?.trim() && item.id.trim())))
  const ontologyGraphQuery = useOntologyContextNeighborhoodQuery(
    () => ({
      tenantId: ontologyGraphRoot?.tenant_id ?? '',
      rootId: ontologyGraphRoot?.id ?? '',
      depth: 2,
      maxNodes: 28,
      maxEdges: 48
    }),
    () => canReadOntology && ontologyFooterOpen && Boolean(ontologyGraphRoot)
  )
  let ontologyGraphNodes = $derived(ontologyGraphQuery.data?.nodes ?? [])
  let ontologyGraphEdges = $derived(ontologyGraphQuery.data?.edges ?? [])
  let ontologyForceNodes = $derived<ObsidianGraphNode[]>(ontologyGraphNodes.map((node) => ({
    id: node.id,
    label: node.label,
    group: node.object_type,
    color: node.id === ontologyGraphRoot?.id ? '#353b45' : undefined,
    value: Math.max(1, node.degree),
    disabled: node.status === 'disabled',
    meta: { objectType: node.object_type, degree: node.degree }
  })))
  let ontologyForceLinks = $derived<ObsidianGraphLink[]>(ontologyGraphEdges.map((edge) => ({
    id: edge.id,
    source: edge.source_id,
    target: edge.target_id,
    label: edge.link_type_label || edge.link_type
  })))
  let hasDiscussionWorkspace = $derived(Boolean(discussionWorkspaceContext.discussionId || discussionWorkspaceContext.assistantMode))
  let approvalCount = $derived(approvalBadgeCountsQuery.data?.approvals ?? 0)
  let hilApprovalCount = $derived(approvalBadgeCountsQuery.data?.hilApprovals ?? 0)
  let councilCount = $derived(approvalBadgeCountsQuery.data?.councils ?? 0)
  let discussionCount = $derived(discussionBadgeCountsQuery.data?.discussions ?? 0)
  let approvalItems = $derived(approvalBadgeCountsQuery.data?.approvalItems ?? [])
  let councilItems = $derived(approvalBadgeCountsQuery.data?.councilItems ?? [])
  let missionHistoryItems = $derived(buildMissionHistoryList(
    missionsQuery.data?.missions ?? [],
    canReadApprovals ? approvalBadgeCountsQuery.data?.allApprovalItems ?? [] : [],
    canReadApprovals ? approvalBadgeCountsQuery.data?.allCouncilItems ?? [] : [],
    currentSessionUserIdentityIds()
  ))
  let visibleMissionHistory = $derived(filterCompletedMissionHistory(missionHistoryItems, historyWindow, historyNow))
  let missionHistorySections = $derived(buildMissionHistorySections(visibleMissionHistory))
  let missionNeedsYouCount = $derived(missionHistoryItems.filter((item) => item.state === 'attention').length)
  let discussionItems = $derived(discussionBadgeCountsQuery.data?.discussionItems ?? [])
  let activeTodos = $derived((todosQuery.data?.todos ?? []).filter(isActiveTodo))
  let failedAutomationTodos = $derived(activeTodos.filter(hasAutomationFailure))
  let myTodoItems = $derived(activeTodos.filter((todo) => !hasAutomationFailure(todo)).slice(0, 3))
  let myTodoCount = $derived(activeTodos.length - failedAutomationTodos.length)
  let failedScheduledRuns = $derived(scheduledRunAlertsQuery.data?.items ?? [])
  let automationItems = $derived(
    [...(scheduledTasksQuery.data?.tasks ?? [])]
      .sort((left, right) => {
        const leftTime = automationRunTime(left.next_run_at)
        const rightTime = automationRunTime(right.next_run_at)
        if (leftTime !== rightTime) return leftTime - rightTime
        return left.title.localeCompare(right.title, $locale)
      })
      .slice(0, 8)
  )
  let automationTotal = $derived(scheduledTasksQuery.data?.total ?? 0)
  let enabledAutomationCount = $derived((scheduledTasksQuery.data?.tasks ?? []).filter((task) => task.status === 'enabled').length)
  let failedAutomationCount = $derived((scheduledTasksQuery.data?.tasks ?? []).filter(hasScheduledFailure).length)
  let runtimeAlertCount = $derived(failedAutomationTodos.length + (scheduledRunAlertsQuery.data?.total ?? 0))
  let runtimeAlerts = $derived<Array<
    | { kind: 'todo'; item: TodoItemDTO }
    | { kind: 'schedule'; item: ScheduledRunInboxItemDTO }
  >>([
    ...failedAutomationTodos.map((item) => ({ kind: 'todo' as const, item })),
    ...failedScheduledRuns.map((item) => ({ kind: 'schedule' as const, item }))
  ].slice(0, 3))

  let views = $derived<Array<{ id: ContextView; label: TranslationKey; icon: typeof FolderKanban }>>([
    { id: 'notifications', label: 'workInbox.title', icon: ListChecks },
    { id: 'missions', label: 'context.missions', icon: FolderKanban },
    { id: 'automation', label: 'context.automation', icon: Workflow },
    { id: 'people', label: 'context.people', icon: Users },
    ...(conversationSwitchContext.available
      ? [{ id: 'conversations' as const, label: 'context.conversations' as TranslationKey, icon: MessageSquare }]
      : [])
  ])
  $effect(() => {
    const discussionId = discussionWorkspaceContext.discussionId
    const assistantMode = discussionWorkspaceContext.assistantMode
    if (activeView !== 'conversations' && ((discussionId && discussionId !== lastDiscussionId) || (assistantMode && !lastAssistantMode))) activeView = 'missions'
    lastDiscussionId = discussionId
    lastAssistantMode = assistantMode
  })

  $effect(() => {
    const available = conversationSwitchContext.available
    if (available && !lastConversationSwitchAvailable) activeView = 'conversations'
    if (!available && activeView === 'conversations') activeView = hasDiscussionWorkspace ? 'missions' : 'notifications'
    lastConversationSwitchAvailable = available
  })

  function availabilityClass(status: string) {
    if (status === 'available') return 'bg-status-done'
    if (status === 'busy') return 'bg-status-blocked'
    return 'bg-status-skipped'
  }

  function ontologyCountLabel() {
    if (ontologyOverviewQuery.isPending) return t('context.ontologyLoading')
    if (ontologyOverviewQuery.isError || ontologyOverview?.objects.status !== 'exact') return t('context.ontologyUnavailable')
    return t('context.ontologyObjectCount', { count: ontologyMetricValue(ontologyOverview.objects) })
  }

  function ontologyMetricValue(metric?: { status: string; value?: number }) {
    if (metric?.status !== 'exact' || typeof metric.value !== 'number') return '—'
    return new Intl.NumberFormat($locale).format(metric.value)
  }

  function ontologyGraphCountLabel() {
    if (ontologyGraphRoot && ontologyGraphQuery.isPending) return t('context.ontologyLoading')
    if (ontologyGraphQuery.isError) return t('context.ontologyUnavailable')
    return t('context.ontologyGraphCounts', {
      nodes: ontologyGraphQuery.data?.visible_nodes ?? ontologyGraphNodes.length,
      edges: ontologyGraphQuery.data?.visible_edges ?? ontologyGraphEdges.length
    })
  }

  function ontologyDomainLabel(domainID: string) {
    return t(`context.ontologyDomain.${domainID}` as TranslationKey)
  }

  function ontologyObjectHref(item: { tenant_id?: string; id: string }) {
    const tenantID = item.tenant_id?.trim() ?? ''
    if (!tenantID) return '/resources/ontology/context'
    const params = new URLSearchParams({ tenant_id: tenantID, object_id: item.id, source_tenant_id: tenantID })
    return `/resources/ontology/context?${params.toString()}`
  }

  function ontologyGraphHref(item?: { tenant_id?: string; id: string }) {
    const tenantID = item?.tenant_id?.trim() ?? ''
    const objectID = item?.id.trim() ?? ''
    if (!tenantID || !objectID) return '/resources/ontology/context'
    const params = new URLSearchParams({
      tenant_id: tenantID,
      view: 'graph',
      object_id: objectID,
      source_tenant_id: tenantID,
      root_id: objectID,
      depth: '2'
    })
    return `/resources/ontology/context?${params.toString()}`
  }

  function missionStatusLabel(status: string) {
    const key = ({
      active: 'mission.active',
      pending: 'mission.pending',
      blocked: 'mission.blocked',
      completed: 'mission.done',
      done: 'mission.done'
    } as const)[status.toLowerCase() as 'active' | 'pending' | 'blocked' | 'completed' | 'done']
    return key ? t(key) : status
  }

  function missionHistoryTitle(mission: MissionHistoryItem) {
    if (mission.state === 'attention') return `${mission.title} · ${missionDecisionRelations(mission)}`
    if (mission.state === 'running') return `${mission.title} · ${t('context.missionRunning')}`
    return mission.lifecycleStatusVisible
      ? `${mission.title} · ${missionStatusLabel(mission.status)}`
      : mission.title
  }

  function missionDecisionRelations(group: MissionHistoryItem) {
    return [
      group.hilCount > 0 ? t('context.missionDecisionHil', { count: group.hilCount }) : '',
      group.approvalCount > 0 ? t('context.missionDecisionApproval', { count: group.approvalCount }) : '',
      group.councilCount > 0 ? t('context.missionDecisionCouncil', { count: group.councilCount }) : ''
    ].filter(Boolean).join(' · ')
  }

  function missionHistorySectionLabel(key: MissionHistorySectionKey) {
    if (key === 'needsYou') return t('context.missionHistory.needsYou')
    if (key === 'running') return t('context.missionHistory.running')
    return t('context.missionHistory.recent')
  }

  function missionIsComplete(status: string) {
    return ['completed', 'done', 'approved', 'succeeded'].includes(status.trim().toLowerCase())
  }

  function contextDate(value: string, fallback = '') {
    if (!value) return fallback
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return fallback || value
    return new Intl.DateTimeFormat($locale, {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit'
    }).format(parsed)
  }

  function isActiveTodo(todo: TodoItemDTO) {
    return todo.status !== 'done' && todo.status !== 'canceled'
  }

  function hasAutomationFailure(todo: TodoItemDTO) {
    return todo.automation?.status === 'planning_failed' || todo.automation?.status === 'execution_failed' || todo.automation?.status === 'stale'
  }

  function hasScheduledFailure(task: ScheduledTaskSummaryDTO) {
    return task.last_run_status === 'failed' || Boolean(task.failure_reason?.trim())
  }

  function approvalNotificationHref(item: ApprovalItemViewModel) {
    return item.sourceLabel === 'hil:hil' ? '/approvals?mode=hil' : '/approvals?mode=approval'
  }

  function approvalQueueHref() {
    return hilApprovalCount > 0 ? '/approvals?mode=hil' : '/approvals?mode=approval'
  }

  function automationStatusLabel(status: ScheduledTaskSummaryDTO['status']) {
    return t(`context.automationStatus.${status}` as TranslationKey)
  }

  function automationStatusClass(status: ScheduledTaskSummaryDTO['status']) {
    if (status === 'enabled') return 'bg-emerald-50 text-emerald-700'
    if (status === 'disabled') return 'bg-amber-50 text-amber-700'
    return 'bg-[#eeeeee] text-[#777777]'
  }

  function automationTargetLabel(mode: ScheduledTaskSummaryDTO['target_mode']) {
    return t(`context.automationTarget.${mode}` as TranslationKey)
  }

  function automationScheduleLabel(type: ScheduledTaskSummaryDTO['schedule_type']) {
    return t(`context.automationSchedule.${type}` as TranslationKey)
  }

  function automationRunLabel(status?: ScheduledTaskSummaryDTO['last_run_status']) {
    return status
      ? t(`context.automationRun.${status}` as TranslationKey)
      : t('context.automationRun.none')
  }

  function automationRunTime(value?: string) {
    if (!value) return Number.POSITIVE_INFINITY
    const parsed = new Date(value).getTime()
    return Number.isNaN(parsed) ? Number.POSITIVE_INFINITY : parsed
  }

  function automationTimelineDate(value?: string) {
    const timestamp = automationRunTime(value)
    if (!Number.isFinite(timestamp)) return '--'
    return new Intl.DateTimeFormat($locale, {
      month: '2-digit',
      day: '2-digit'
    }).format(timestamp)
  }

  function automationTimelineTime(value?: string) {
    const timestamp = automationRunTime(value)
    if (!Number.isFinite(timestamp)) return '--:--'
    return new Intl.DateTimeFormat($locale, {
      hour: '2-digit',
      minute: '2-digit',
      hour12: false
    }).format(timestamp)
  }

  function automationTimelineDotClass(task: ScheduledTaskSummaryDTO) {
    if (hasScheduledFailure(task)) return 'border-rose-500 bg-rose-50'
    if (task.status === 'enabled') return 'border-violet-600 bg-white'
    if (task.status === 'disabled') return 'border-amber-500 bg-amber-50'
    return 'border-[#b8b8b8] bg-[#f2f2f2]'
  }

  function todoStatusLabel(status: TodoStatus) {
    return t(`workhub.status.${status}` as TranslationKey)
  }

  function todoStatusClass(status: TodoStatus) {
    if (status === 'blocked') return 'text-rose-700'
    if (status === 'review') return 'text-amber-700'
    if (status === 'in_progress') return 'text-blue-700'
    return 'text-[#7f7f7f]'
  }

</script>

<aside
  class="right-sidebar-shell right-sidebar-rail"
  aria-label={t('context.toggle')}
  data-workspace-context-panel
>
  <header class="right-sidebar-header">
    <span class="min-w-0 truncate text-[13px] font-medium">{t(views.find((view) => view.id === activeView)?.label ?? 'context.todos')}</span>
    <div class="flex shrink-0 items-center gap-0.5">
      {#each views as view}
        {@const Icon = view.icon}
        <button
          class="relative grid h-7 w-7 place-items-center rounded-md transition-colors {activeView === view.id
            ? 'selection-wash text-sidebar-accent-foreground'
            : 'text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground'}"
          type="button"
          aria-label={t(view.label)}
          title={t(view.label)}
          aria-pressed={activeView === view.id}
          onclick={() => (activeView = view.id)}
        >
          <Icon class="h-[15px] w-[15px]" />
          {#if view.id === 'results' && resultUnreadCount > 0}
            <span class="absolute -right-1 -top-1 min-w-3.5 rounded-full bg-red-600 px-0.5 text-center text-[9px] font-semibold leading-[14px] text-white" data-result-notification-badge title={String(resultUnreadCount)}>{resultUnreadCount > 99 ? '99+' : resultUnreadCount}</span>
          {/if}
        </button>
      {/each}
      {#if showClose}
        <button
          class="ml-1 grid h-7 w-7 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
          type="button"
          aria-label={t('context.close')}
          title={t('context.close')}
          onclick={onClose}
        >
          <X class="h-[15px] w-[15px]" />
        </button>
      {/if}
    </div>
  </header>

  <div class={activeView === 'conversations'
    ? 'right-sidebar-body overflow-hidden'
    : 'right-sidebar-body right-sidebar-scroll subtle-scrollbar px-4 pb-4 pt-3'}>
    {#if activeView === 'conversations' && conversationSwitchContext.available}
      <ConversationSwitchPanel
        embedded
        conversations={conversationSwitchContext.conversations}
        selectedConversationId={conversationSwitchContext.selectedConversationId}
        onSelect={conversationSwitchContext.onSelect}
      />
    {:else if activeView === 'results' && canReadScheduler}
      <section data-context-result-notifications aria-label={t('context.resultNotifications')}>
        <div class="mb-3 flex items-center justify-between gap-2 text-[12px]">
          <span role="status">{t('context.resultUnread', { count: resultUnreadCount })}</span>
          <a class="shrink-0 text-muted-foreground hover:text-foreground" href="/scheduled-tasks?view=results">{t('context.viewAll')}</a>
        </div>
        {#if resultNotificationsQuery.isLoading}
          <div class="h-20 animate-pulse bg-muted" aria-label={t('context.resultNotifications')}></div>
        {:else if resultNotificationsQuery.isError}
          <p class="py-3 text-sm text-destructive">{t('context.resultNotificationsError')}</p>
          <button type="button" class="text-sm underline" onclick={() => resultNotificationsQuery.refetch()}>{t('context.resultRetry')}</button>
        {:else if resultNotificationsQuery.data?.items.length}
          <div class="divide-y divide-border">
            {#each resultNotificationsQuery.data.items as item (item.run.scheduled_run_id)}
              <a
                class="block min-w-0 py-3 hover:bg-muted/50"
                data-result-notification={item.run.scheduled_run_id}
                href={`/scheduled-tasks?view=results&task=${encodeURIComponent(item.run.scheduled_task_id)}&run=${encodeURIComponent(item.run.scheduled_run_id)}`}
              >
                <div class="flex items-start gap-2">
                  <span class="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-blue-600"></span>
                  <span class="min-w-0 break-words text-[13px] font-medium leading-5 text-foreground">{item.task_title}</span>
                </div>
                <p class="mt-1 line-clamp-2 break-words pl-3.5 text-[12px] leading-5 text-muted-foreground">{item.run.result?.summary || item.run.failure_reason || ''}</p>
                <div class="mt-1 flex flex-wrap gap-x-2 pl-3.5 text-[11px] leading-5">
                  <span class={item.attention_required ? 'text-red-700' : 'text-muted-foreground'}>{t(item.attention_required ? 'context.resultAttention' : item.run.status === 'skipped' ? 'context.resultSkipped' : 'context.resultCompleted')}</span>
                  <span class="text-muted-foreground">{contextDate(item.run.completed_at || item.run.created_at)}</span>
                </div>
              </a>
            {/each}
          </div>
        {:else}
          <p class="py-6 text-center text-sm text-muted-foreground">{t('context.resultNoUnread')}</p>
        {/if}
      </section>
    {:else if activeView === 'missions'}
      <div class="space-y-3" data-context-mission-history>
        <div class="flex h-8 min-w-0 items-center justify-between gap-2 px-1">
          <div class="flex min-w-0 items-center gap-2">
            <h2 class="truncate text-[12px] font-medium text-[#555]">{t('context.missionHistory')}</h2>
            <span class="min-w-5 rounded-full bg-[#eeeeee] px-1.5 text-center text-[10px] font-medium leading-5 text-[#777]">{missionHistoryItems.length}</span>
            {#if missionNeedsYouCount > 0}
              <span class="shrink-0 rounded-full bg-amber-50 px-1.5 text-[10px] font-medium leading-5 text-amber-700" data-mission-needs-you-count={missionNeedsYouCount}>
                {t('context.missionNeedsYouCount', { count: missionNeedsYouCount })}
              </span>
            {/if}
            {#if canReadApprovals && approvalBadgeCountsQuery.isError}
              <span class="text-amber-600" aria-label={t('context.missionDecisionsLoadFailed')} title={t('context.missionDecisionsLoadFailed')}>
                <AlertTriangle class="h-3.5 w-3.5" />
              </span>
            {/if}
          </div>
          <a class="grid h-7 w-7 shrink-0 place-items-center rounded-md text-[#8a8a8a] transition-colors hover:bg-[#eeeeee] hover:text-black" href="/missions" aria-label={t('context.viewAllMissions')} title={t('context.viewAllMissions')}>
            <FolderKanban class="h-3.5 w-3.5" />
          </a>
        </div>

        <HistoryWindowFilter bind:range={historyWindow} bind:now={historyNow} hiddenCount={missionHistoryItems.length - visibleMissionHistory.length} />
        {#if !canReadMissions}
          <p class="rounded-md border border-dashed border-[#dedede] bg-white px-3 py-5 text-center text-[11px] leading-5 text-[#9a9a9a]">{t('context.missionsUnavailable')}</p>
        {:else if missionsQuery.isLoading}
          <div class="space-y-1" aria-label={t('context.loadingMissions')}>
            {#each Array(6) as _}<div class="h-9 animate-pulse rounded-md bg-[#eeeeee]"></div>{/each}
          </div>
        {:else if missionsQuery.isError}
          <p class="rounded-md border border-rose-200 bg-rose-50 px-3 py-4 text-center text-[11px] text-rose-700">{t('context.missionsLoadFailed')}</p>
        {:else if visibleMissionHistory.length > 0}
          <div class="space-y-4" data-mission-history-list>
            {#each missionHistorySections as section (section.key)}
              <section data-mission-history-section={section.key}>
                <div class="mb-1 flex h-6 items-center justify-between px-2 text-[10.5px] font-medium text-[#999]">
                  <span>{missionHistorySectionLabel(section.key)}</span>
                  <span class="tabular-nums">{section.missions.length}</span>
                </div>
                <div class="space-y-0.5">
                  {#each section.missions as mission (mission.missionId)}
                    <div class="group flex h-9 min-w-0 items-center rounded-md transition-colors hover:bg-[#f1f1f1]" data-mission-history-item={mission.missionId} data-mission-history-state={mission.state}>
                      <a
                        class="flex h-full min-w-0 flex-1 items-center gap-2 px-2 text-left"
                        href={mission.primaryHref}
                        title={missionHistoryTitle(mission)}
                      >
                        {#if mission.state === 'attention'}
                          <CircleAlert class="h-4 w-4 shrink-0 text-amber-600" data-mission-needs-you={mission.missionId} />
                        {:else if mission.state === 'running'}
                          <LoaderCircle class="h-4 w-4 shrink-0 animate-spin text-blue-600" data-mission-running={mission.missionId} />
                        {:else if missionIsComplete(mission.status)}
                          <CheckCircle2 class="h-4 w-4 shrink-0 text-emerald-600" />
                        {:else}
                          <FolderKanban class="h-4 w-4 shrink-0 text-[#999]" />
                        {/if}
                        <span class="min-w-0 flex-1 truncate text-[12px] text-[#333]">{mission.title}</span>
                        {#if mission.state === 'attention'}
                          <span class="shrink-0 text-[10.5px] font-medium text-amber-700">{t('context.missionNeedsYou')}{mission.total > 1 ? ` ${mission.total}` : ''}</span>
                        {:else if mission.state === 'running'}
                          <span class="sr-only">{t('context.missionRunning')}</span>
                        {:else if mission.lifecycleStatusVisible}
                          <span class="max-w-16 shrink-0 truncate text-[10px] text-[#999]">{missionStatusLabel(mission.status)}</span>
                        {/if}
                      </a>
                      <a
                        class="mr-1 grid h-7 w-7 shrink-0 place-items-center rounded-md text-[#aaa] transition-colors hover:bg-white hover:text-black"
                        href={mission.missionHref}
                        aria-label={t('context.openMission', { title: mission.title })}
                        title={t('context.openMission', { title: mission.title })}
                      >
                        <ExternalLink class="h-3.5 w-3.5" />
                      </a>
                    </div>
                  {/each}
                </div>
              </section>
            {/each}
          </div>
        {:else}
          <div class="rounded-md border border-dashed border-[#dedede] bg-white px-4 py-6 text-center" data-mission-history-empty>
            <FolderKanban class="mx-auto h-5 w-5 text-[#999]" />
            <p class="mt-2 text-[12px] font-medium text-[#555]">{t('context.noMissions')}</p>
          </div>
        {/if}
      </div>
    {:else if activeView === 'notifications'}
      <WorkInbox compact />
      <!-- Legacy markup preserved in web/legacy-backup/context-notifications.svelte.txt. -->
    {:else if activeView === 'automation'}
      {#if !canReadScheduler}
        <p class="rounded-lg border border-dashed border-[#dedede] bg-white px-3 py-5 text-center text-[11px] text-[#9a9a9a]">{t('context.automationUnavailable')}</p>
      {:else if scheduledTasksQuery.isLoading}
        <div class="space-y-3" aria-label={t('context.loadingAutomation')}>
          <div class="grid grid-cols-3 gap-px overflow-hidden rounded-lg border border-[#e8e8e8] bg-[#e8e8e8]">
            {#each Array(3) as _}<div class="h-14 animate-pulse bg-[#eeeeee]"></div>{/each}
          </div>
          {#each Array(4) as _}<div class="h-[72px] animate-pulse rounded-lg bg-[#eeeeee]"></div>{/each}
        </div>
      {:else if scheduledTasksQuery.isError}
        <p class="rounded-lg border border-rose-200 bg-rose-50 px-3 py-4 text-center text-[11px] text-rose-700">{t('context.automationLoadFailed')}</p>
      {:else}
        <div class="space-y-4" data-context-automation>
          <div class="grid grid-cols-3 gap-px overflow-hidden rounded-lg border border-[#e8e8e8] bg-[#e8e8e8]">
            <div class="bg-white px-2 py-2.5 text-center">
              <strong class="block text-[15px] font-semibold leading-5">{automationTotal}</strong>
              <span class="text-[9.5px] text-[#999]">{t('context.automationTotal')}</span>
            </div>
            <div class="bg-white px-2 py-2.5 text-center">
              <strong class="block text-[15px] font-semibold leading-5 text-emerald-700">{enabledAutomationCount}</strong>
              <span class="text-[9.5px] text-[#999]">{t('context.automationEnabled')}</span>
            </div>
            <div class="bg-white px-2 py-2.5 text-center">
              <strong class="block text-[15px] font-semibold leading-5 {failedAutomationCount > 0 ? 'text-rose-700' : ''}">{failedAutomationCount}</strong>
              <span class="text-[9.5px] text-[#999]">{t('context.automationFailures')}</span>
            </div>
          </div>

          <section>
            <div class="mb-2 flex h-6 items-center justify-between gap-2 px-0.5">
              <div class="flex min-w-0 items-center gap-1.5">
                <CalendarClock class="h-3.5 w-3.5 shrink-0 text-violet-600" />
                <h2 class="truncate text-[12px] font-medium">{t('context.automationTasks')}</h2>
              </div>
              <a class="flex shrink-0 items-center gap-0.5 text-[10.5px] text-[#8a8a8a] hover:text-black" href="/scheduled-tasks">
                {t('context.viewAll')}<ChevronRight class="h-3 w-3" />
              </a>
            </div>
            {#if automationItems.length > 0}
              <div class="pt-1" data-automation-timeline>
                {#each automationItems as task, index (task.scheduled_task_id)}
                  <article class="grid grid-cols-[42px_14px_minmax(0,1fr)] gap-x-2" data-automation-timeline-node>
                    <div class="pt-0.5 text-right tabular-nums">
                      <span class="block text-[9px] leading-3 text-[#a0a0a0]">{automationTimelineDate(task.next_run_at)}</span>
                      <strong class="mt-0.5 block text-[10.5px] font-medium leading-4 text-[#565656]">{automationTimelineTime(task.next_run_at)}</strong>
                    </div>
                    <div class="relative flex justify-center">
                      {#if index < automationItems.length - 1}
                        <span class="absolute -bottom-1 top-3 w-px bg-[#dedede]" aria-hidden="true" data-automation-timeline-line></span>
                      {/if}
                      <span class="relative mt-1 h-2.5 w-2.5 rounded-full border-2 {automationTimelineDotClass(task)}" aria-hidden="true"></span>
                    </div>
                    <a class="group min-w-0 pb-5" href="/scheduled-tasks">
                      <div class="flex items-start gap-2">
                        <span class="min-w-0 flex-1 break-words text-[12px] font-medium leading-[18px] text-[#282828] group-hover:text-violet-700">{task.title}</span>
                        <span class="shrink-0 rounded-full px-2 py-0.5 text-[9.5px] font-medium {automationStatusClass(task.status)}">{automationStatusLabel(task.status)}</span>
                      </div>
                      <p class="mt-1 truncate text-[9.5px] leading-4 text-[#929292]">{automationTargetLabel(task.target_mode)} / {automationScheduleLabel(task.schedule_type)}</p>
                      <div class="mt-1 flex items-center justify-between gap-2 text-[9.5px] leading-4">
                        <span class="min-w-0 truncate text-[#999]">{task.next_run_at ? task.target_summary : t('context.automationNoNextRun')}</span>
                        <span class="shrink-0 {task.last_run_status === 'failed' ? 'text-rose-700' : 'text-[#999]'}">{automationRunLabel(task.last_run_status)}</span>
                      </div>
                    </a>
                  </article>
                {/each}
              </div>
            {:else}
              <div class="rounded-lg border border-dashed border-[#dedede] bg-white px-3 py-5 text-center text-[11px] text-[#9a9a9a]">{t('context.noAutomation')}</div>
            {/if}
          </section>
        </div>
      {/if}
    {:else if activeView === 'people'}
      {#if !canReadPeople}
        <p class="rounded-xl border border-dashed border-[#e0e0e0] bg-white p-5 text-center text-xs text-[#8a8a8a]">{t('context.peopleUnavailable')}</p>
      {:else if workforceQuery.isLoading}
        <div class="space-y-2" aria-label={t('context.loadingPeople')}>
          {#each Array(5) as _}<div class="h-11 animate-pulse rounded-lg bg-[#eeeeee]"></div>{/each}
        </div>
      {:else if workforceQuery.isError}
        <p class="rounded-lg border border-rose-200 bg-rose-50 p-3 text-xs text-rose-700">{t('context.peopleLoadFailed')}</p>
      {:else}
        <div class="space-y-0.5" data-context-people>
          {#each workforceQuery.data?.members ?? [] as member}
            <div class="flex items-center gap-2.5 rounded-lg px-1 py-1.5">
              <span class="relative h-10 w-10 shrink-0">
                <WorkforceAvatar
                  profileId={member.profileId}
                  name={member.displayName || member.realName || member.profileId}
                  human={member.agentKind.toLowerCase() === 'human'}
                  class="h-10 w-10 rounded-full"
                  textClass="text-[11px]"
                />
                <span class="absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-[#fafafa] {availabilityClass(member.availabilityStatus)}"></span>
              </span>
              <span class="min-w-0 leading-tight">
                <span class="block truncate text-[13px] font-medium">{member.displayName || member.realName || member.profileId}</span>
                <span class="mt-0.5 block truncate text-[11px] text-[#a0a0a0]">{member.positionDisplayName || member.primaryRoleLabel || member.agentKind}</span>
              </span>
            </div>
          {:else}
            <p class="rounded-xl border border-dashed border-[#e0e0e0] bg-white p-5 text-center text-xs text-[#8a8a8a]">{t('context.noPeople')}</p>
          {/each}
        </div>
      {/if}
    {/if}
  </div>

  {#if canReadOntology}
    <section class="shrink-0 border-t border-[#ececec] bg-white" data-ontology-footer>
      <button
        class="flex h-10 w-full items-center gap-2 px-4 text-left transition-colors hover:bg-[#fafafa]"
        type="button"
        aria-expanded={ontologyFooterOpen}
        aria-controls="context-ontology-mini"
        aria-label={ontologyFooterOpen ? t('context.collapseOntology') : t('context.expandOntology')}
        onclick={() => (ontologyFooterOpen = !ontologyFooterOpen)}
      >
        <Boxes class="h-4 w-4 shrink-0 text-[#8a8a8a]" />
        <span class="min-w-0 flex-1 truncate text-[12px] font-medium text-black">{t('context.companyOntology')}</span>
        <span class="max-w-28 truncate text-[10px] text-[#a0a0a0]">{ontologyCountLabel()}</span>
        <ChevronRight class="h-3.5 w-3.5 shrink-0 text-[#a0a0a0] transition-transform {ontologyFooterOpen ? 'rotate-90' : ''}" />
      </button>

      {#if ontologyFooterOpen}
        <div class="subtle-scrollbar max-h-[320px] overflow-y-auto px-4 pb-3" id="context-ontology-mini">
          {#if ontologyOverviewQuery.isPending}
            <div class="h-[180px] animate-pulse bg-[#eeeeee]" aria-label={t('context.ontologyLoading')}></div>
          {:else if ontologyOverviewQuery.isError || !ontologyOverview}
            <a class="grid h-[72px] place-items-center border-y border-dashed border-[#d8d8d8] text-[11px] text-[#8a8a8a] hover:border-[#bdbdbd]" href="/resources/ontology/context">
              {t('context.ontologyUnavailable')}
            </a>
          {:else}
            <div data-ontology-company-overview>
              <section class="pb-3" data-ontology-relationship-graph>
                <div class="flex items-center justify-between gap-2 pb-1.5">
                  <h2 class="text-[10px] font-medium uppercase text-[#8a8a8a]">{t('context.actualDataRelationships')}</h2>
                  <span class="truncate text-[10px] tabular-nums text-[#a0a0a0]">{ontologyGraphCountLabel()}</span>
                </div>
                {#if ontologyGraphRoot && ontologyGraphQuery.isPending}
                  <div class="h-[154px] animate-pulse bg-[#eeeeee]" aria-label={t('context.ontologyLoading')}></div>
                {:else if ontologyGraphQuery.isError}
                  <a class="grid h-[72px] place-items-center border-y border-dashed border-[#d8d8d8] text-[11px] text-[#8a8a8a] hover:border-[#bdbdbd]" href={ontologyGraphHref(ontologyGraphRoot)}>
                    {t('context.ontologyUnavailable')}
                  </a>
                {:else if ontologyGraphNodes.length}
                  <a class="block overflow-hidden border-y border-[#ececec] bg-[#fcfcfc] transition-colors hover:border-[#d5d5d5]" href={ontologyGraphHref(ontologyGraphRoot)} title={t('context.openOntology')} data-ontology-footer-graph>
                    <ObsidianForceGraph
                      nodes={ontologyForceNodes}
                      links={ontologyForceLinks}
                      ariaLabel={t('context.actualDataRelationships')}
                      height={154}
                      compact
                      controls={false}
                    />
                  </a>
                {:else}
                  <a class="grid h-[72px] place-items-center border-y border-dashed border-[#d8d8d8] text-[11px] text-[#8a8a8a]" href={ontologyGraphHref(ontologyGraphRoot)}>
                    {t('context.ontologyEmpty')}
                  </a>
                {/if}
              </section>

              <div class="grid grid-cols-2 gap-px border-y border-[#e8e8e8] bg-[#e8e8e8]">
                <div class="bg-white px-2 py-2 text-center"><strong class="block text-[14px] font-semibold tabular-nums">{ontologyMetricValue(ontologyOverview.objects)}</strong><span class="text-[9.5px] text-[#999]">{t('context.ontologyObjects')}</span></div>
                <div class="bg-white px-2 py-2 text-center"><strong class="block text-[14px] font-semibold tabular-nums">{ontologyMetricValue(ontologyOverview.facts)}</strong><span class="text-[9.5px] text-[#999]">{t('context.ontologyFacts')}</span></div>
                <div class="bg-white px-2 py-2 text-center"><strong class="block text-[14px] font-semibold tabular-nums">{ontologyMetricValue(ontologyOverview.relations)}</strong><span class="text-[9.5px] text-[#999]">{t('context.ontologyRelations')}</span></div>
                <div class="bg-white px-2 py-2 text-center"><strong class="block text-[14px] font-semibold tabular-nums">{ontologyMetricValue(ontologyOverview.evidence)}</strong><span class="text-[9.5px] text-[#999]">{t('context.ontologyEvidence')}</span></div>
              </div>

              <section class="mt-3">
                <div class="flex items-center justify-between gap-2">
                  <h2 class="text-[10px] font-medium uppercase text-[#8a8a8a]">{t('context.ontologyDomains')}</h2>
                  <span class="text-[10px] tabular-nums text-[#a0a0a0]">{ontologyMetricValue(ontologyOverview.domain_count)}</span>
                </div>
                {#if ontologyDomains.length}
                  <dl class="mt-1 divide-y divide-[#eeeeee] border-y border-[#eeeeee] text-[11px]">
                    {#each ontologyDomains as domain (domain.id)}
                      <div class="flex items-center justify-between gap-3 py-1.5">
                        <dt class="truncate text-[#555]">{ontologyDomainLabel(domain.id)}</dt>
                        <dd class="shrink-0 tabular-nums text-[#888]">{ontologyMetricValue(domain.objects)}</dd>
                      </div>
                    {/each}
                  </dl>
                {/if}
              </section>

              <section class="mt-3">
                <div class="flex items-center justify-between gap-2">
                  <h2 class="text-[10px] font-medium uppercase text-[#8a8a8a]">{t('context.ontologyRecentObjects')}</h2>
                  <a class="flex items-center gap-0.5 text-[10px] text-[#8a8a8a] hover:text-black" href="/resources/ontology/context">{t('context.viewAll')}<ChevronRight class="h-3 w-3" /></a>
                </div>
                {#if ontologyRecentObjects.length}
                  <div class="mt-1 divide-y divide-[#eeeeee] border-y border-[#eeeeee]">
                    {#each ontologyRecentObjects as item (`${item.tenant_id ?? ''}:${item.id}`)}
                      <a class="group flex min-w-0 items-center gap-2 py-2" href={ontologyObjectHref(item)}>
                        <span class="min-w-0 flex-1">
                          <span class="block truncate text-[11.5px] font-medium text-[#333] group-hover:underline">{item.label}</span>
                          <span class="mt-0.5 block truncate text-[9.5px] text-[#999]">{objectTypeLabel(item.object_type)}{#if item.status} · {ontologyValueLabel(item.status)}{/if}</span>
                        </span>
                        <ChevronRight class="h-3 w-3 shrink-0 text-[#b0b0b0]" />
                      </a>
                    {/each}
                  </div>
                {:else}
                  <p class="mt-1 border-y border-dashed border-[#dedede] py-3 text-center text-[10.5px] text-[#999]">{t('context.ontologyEmpty')}</p>
                {/if}
              </section>
            </div>
          {/if}
        </div>
      {/if}
    </section>
  {/if}
</aside>
