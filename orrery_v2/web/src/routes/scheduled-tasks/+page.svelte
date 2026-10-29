<script lang="ts">
  import { page } from '$app/stores'
  import {
    AlertTriangle,
    CalendarClock,
    CalendarDays,
    ChevronLeft,
    ChevronRight,
    CirclePlay,
    FileText,
    Inbox,
    Pencil,
    Play,
    Plus,
    RefreshCw,
    Search,
    Trash2,
    UsersRound
  } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import KpiCard from '#lib/components/shared/KpiCard.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import { schedulerApi } from '#lib/api/scheduler'
  import type { ApiClientError } from '#lib/api/client'
  import { createTranslator, locale } from '#lib/i18n'
  import { useDiscussionListQuery } from '#lib/queries/discussions'
  import {
    useScheduledRunInboxQuery,
    useScheduledTaskStatsQuery,
    useScheduledTasksQuery,
    useSchedulerCommandMutations
  } from '#lib/queries/scheduler'
  import { combineToolPages, useResourceToolsInfiniteQuery } from '#lib/queries/resources'
  import { useWorkforceMembersQuery } from '#lib/queries/workforce'
  import { mapToolGatewayView } from '#lib/mappers/resource'
  import { memberType } from '#lib/components/operations/workforceMemberForm'
  import type { DiscussionSummaryViewModel } from '#lib/types/discussions'
  import type { WorkforceMemberViewModel } from '#lib/types/view-models'
  import type {
    RunStatus,
    ScheduledTaskSort,
    ScheduledTaskStatus,
    ScheduledTaskSummaryDTO
  } from '#lib/types/scheduler'
  import {
    createScheduledTaskForm,
    formFromScheduledTask,
    mapScheduledTaskFormToPayload,
    mapScheduledTaskFormToUpdatePayload,
    validateScheduledTaskForm,
    type ScheduledTaskForm,
    type ScheduledTaskSelectOption
  } from './scheduledTaskForm'
  import {
    layoutScheduledTasksForGantt,
    scheduledTasksForDay,
    startOfLocalWeek
  } from './scheduledTaskTimeline'
  import {
    scheduledTaskDisplayTitle,
    scheduledTaskTargetLabel
  } from './scheduledTaskDisplay'
  import {
    createScheduledTaskAgentIndex,
    groupScheduledTasksByAgent,
    scheduledTaskExecutor,
    type ScheduledTaskExecutor
  } from './scheduledTaskAgents'
  import SchedulerTaskFormDialog from './SchedulerTaskFormDialog.svelte'
  import ScheduledRunInbox from './ScheduledRunInbox.svelte'

  type TaskStatusFilter = ScheduledTaskStatus | ''

  const today = startOfLocalDay(new Date())
  const timelineHours = [0, 4, 8, 12, 16, 20, 24]

  let includeArchived = $state(false)
  let editingTaskID = $state('')
  let busyTaskID = $state('')
  let actionMessage = $state('')
  let actionError = $state('')
  let showEditor = $state(false)
  let workspaceView = $state<'tasks' | 'results'>(
    $page.url.searchParams.get('view') === 'results' || $page.url.searchParams.has('task') || $page.url.searchParams.has('run')
      ? 'results'
      : 'tasks'
  )
  let resultTaskID = $state('')
  let resultRunID = $state('')
  let resultFilter = $state<'all' | 'unread' | 'attention'>('all')
  let scheduleView = $state<'schedule' | 'plan'>('schedule')
  let form = $state<ScheduledTaskForm>(createScheduledTaskForm())
  let formErrors = $state<string[]>([])
  let searchTerm = $state('')
  let debouncedSearchTerm = $state('')
  let statusFilter = $state<TaskStatusFilter>('')
  let sortOrder = $state<ScheduledTaskSort>('created_desc')
  let currentPage = $state(1)
  let pageSize = $state(10)
  let timelineAnchor = $state(today)
  let selectedDayKey = $state(dateKey(today))
  let handledSourceTask = $state('')
  let t = $derived(createTranslator($locale))

  const pageOffset = $derived((currentPage - 1) * pageSize)
  const timelineStart = $derived(startOfLocalWeek(timelineAnchor))
  const timelineEnd = $derived(addDays(timelineStart, 7))
  const timelineDays = $derived(
    Array.from({ length: 7 }, (_, index) => {
      const date = addDays(timelineStart, index)
      return {
        date,
        key: dateKey(date),
        day: shortDateLabel(date),
        weekday: weekdayLabel(date),
        isToday: dateKey(date) === dateKey(today)
      }
    })
  )

  const taskStatsQuery = useScheduledTaskStatsQuery(() => workspaceView === 'tasks')
  const scheduleQuery = useScheduledTasksQuery(() => ({
    includeArchived,
    q: debouncedSearchTerm,
    status: statusFilter,
    from: timelineStart.toISOString(),
    to: timelineEnd.toISOString(),
    expandOccurrences: true,
    occurrenceTimezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    limit: 200,
    offset: 0,
    sort: 'next_run_asc'
  }), () => workspaceView === 'tasks')
  const tasksQuery = useScheduledTasksQuery(() => ({
    includeArchived,
    q: debouncedSearchTerm,
    status: statusFilter,
    sort: sortOrder,
    limit: pageSize,
    offset: pageOffset
  }), () => workspaceView === 'tasks')
  const inboxSummaryQuery = useScheduledRunInboxQuery(
    () => ({ limit: 1, offset: 0 }),
    () => workspaceView === 'tasks'
  )
  const discussionsQuery = useDiscussionListQuery(() => 1, () => 200, () => showEditor)
  // Include disabled members so historical tasks can still resolve the same personnel avatar.
  const workforceQuery = useWorkforceMembersQuery(
    () => ({ enabledOnly: undefined, includeDetails: false, limit: 500, offset: 0 }),
    () => workspaceView === 'tasks' || showEditor
  )
  const toolsQuery = useResourceToolsInfiniteQuery(() => ({ enabled: 'true', limit: 500 }), () => showEditor)
  const commands = useSchedulerCommandMutations()

  const scheduleTasks = $derived(scheduleQuery.data?.tasks ?? [])
  const tableTasks = $derived(tasksQuery.data?.tasks ?? [])
  const tableTotal = $derived(tasksQuery.data?.total ?? tableTasks.length)
  const totalPages = $derived(Math.max(1, Math.ceil(tableTotal / pageSize)))
  const workforceAgents = $derived(
    (workforceQuery.data?.members ?? []).filter((member) => memberType(member) === 'agent' && member.enabled)
  )
  const workforceAgentIndex = $derived(createScheduledTaskAgentIndex(workforceAgents))
  const selectedDayGanttItems = $derived(layoutScheduledTasksForGantt(selectedDayTasks()))
  const selectedDayAgentLanes = $derived(
    groupScheduledTasksByAgent(selectedDayGanttItems, workforceAgentIndex, $locale)
  )
  const summary = $derived(taskStatsQuery.data)
  const discussionOptions = $derived(
    uniqueOptions((discussionsQuery.data?.discussions ?? []).map(discussionOption))
  )
  const agentOptions = $derived(
    uniqueOptions(
      workforceAgents.map(agentOption)
    )
  )
  const memberOptions = $derived(
    uniqueOptions(
      (workforceQuery.data?.members ?? [])
        .filter((member) => member.enabled)
        .map(memberOption)
    )
  )
  const toolsData = $derived(mapToolGatewayView(combineToolPages(toolsQuery.data?.pages ?? [])))
  const toolOptions = $derived(uniqueOptions(toolsData.tools.map(toolOption)))

  $effect(() => {
    const value = searchTerm.trim()
    const timer = window.setTimeout(() => {
      debouncedSearchTerm = value
    }, 250)
    return () => window.clearTimeout(timer)
  })

  $effect(() => {
    if (currentPage > totalPages) currentPage = totalPages
    if (currentPage < 1) currentPage = 1
  })

  $effect(() => {
    const requestedTask = $page.url.searchParams.get('task')?.trim() ?? ''
    const requestedRun = $page.url.searchParams.get('run')?.trim() ?? ''
    const requestedView = $page.url.searchParams.get('view')?.trim() ?? ''
    const requestedFilter = $page.url.searchParams.get('filter')?.trim() ?? ''
    const signature = `${requestedView}\u0000${requestedTask}\u0000${requestedRun}\u0000${requestedFilter}`
    if (signature === handledSourceTask) return
    handledSourceTask = signature
    if (requestedView === 'results' || requestedTask || requestedRun) {
      workspaceView = 'results'
      resultTaskID = requestedTask
      resultRunID = requestedRun
      resultFilter = requestedFilter === 'attention' || requestedFilter === 'unread' ? requestedFilter : 'all'
    } else {
      workspaceView = 'tasks'
      resultTaskID = ''
      resultRunID = ''
      resultFilter = 'all'
    }
  })

  function openCreateTask() {
    editingTaskID = ''
    form = createScheduledTaskForm()
    formErrors = []
    actionError = ''
    showEditor = true
  }

  async function openEditTask(task: ScheduledTaskSummaryDTO) {
	if (task.target_mode === 'responsibility_evaluation' || task.target_mode === 'responsibility_reconcile') return
    busyTaskID = task.scheduled_task_id
    actionError = ''
    formErrors = []
    try {
      const fullTask = await schedulerApi.getTask(task.scheduled_task_id)
      editingTaskID = task.scheduled_task_id
      form = formFromScheduledTask(fullTask)
      showEditor = true
    } catch (error) {
      actionError = error instanceof Error ? error.message : '自动化任务加载失败。'
    } finally {
      busyTaskID = ''
    }
  }

  function saveTask() {
    const errors = validateScheduledTaskForm(form)
    formErrors = errors
    actionError = ''
    actionMessage = ''
    if (errors.length > 0) return

    if (editingTaskID) {
      commands.updateTask.mutate(
        { scheduledTaskID: editingTaskID, body: mapScheduledTaskFormToUpdatePayload(form) },
        {
          onSuccess: () => {
            actionMessage = '自动化任务已更新。'
            showEditor = false
            editingTaskID = ''
          },
          onError: handleActionError
        }
      )
      return
    }

    commands.createTask.mutate(mapScheduledTaskFormToPayload(form), {
      onSuccess: () => {
        actionMessage = '自动化任务已创建。'
        showEditor = false
        currentPage = 1
      },
      onError: handleActionError
    })
  }

  function runNow(task: ScheduledTaskSummaryDTO, force = false) {
    busyTaskID = task.scheduled_task_id
    actionError = ''
    actionMessage = ''
    commands.runNow.mutate(
      { scheduledTaskID: task.scheduled_task_id, force },
      {
        onSuccess: (run) => {
          actionMessage = run.status === 'skipped' ? '已有运行中的任务，本次已跳过。' : '已触发立即运行。'
          busyTaskID = ''
        },
        onError: (error) => {
          handleActionError(error)
          busyTaskID = ''
        }
      }
    )
  }

  function openRunHistory(task: ScheduledTaskSummaryDTO) {
    workspaceView = 'results'
    resultTaskID = task.scheduled_task_id
    resultRunID = ''
    resultFilter = 'all'
    actionError = ''
    actionMessage = ''
  }

  function deleteTask(task: ScheduledTaskSummaryDTO) {
    if (!confirm(`删除自动化任务「${displayTaskTitle(task)}」？任务将停止执行并移入归档记录。`)) return
    busyTaskID = task.scheduled_task_id
    actionError = ''
    actionMessage = ''
    commands.cancelTask.mutate(task.scheduled_task_id, {
      onSuccess: () => {
        actionMessage = '自动化任务已删除并归档。'
        busyTaskID = ''
      },
      onError: (error) => {
        handleActionError(error)
        busyTaskID = ''
      }
    })
  }

  function handleActionError(error: unknown) {
    actionError = error instanceof Error ? error.message : '操作失败。'
  }

  function resetTaskPage() {
    currentPage = 1
  }

  function goToPage(page: number) {
    currentPage = clamp(page, 1, totalPages)
  }

  function shiftTimeline(days: number) {
    timelineAnchor = addDays(timelineAnchor, days)
    selectedDayKey = dateKey(timelineAnchor)
  }

  function tasksForDay(day: Date) {
    return scheduledTasksForDay(scheduleTasks, day)
  }

  function selectedDayTasks() {
    const day = timelineDays.find((item) => item.key === selectedDayKey)?.date ?? timelineStart
    return tasksForDay(day)
  }

  function statusTone(status: ScheduledTaskStatus | RunStatus) {
    switch (status) {
      case 'enabled':
      case 'completed':
        return 'success'
      case 'disabled':
      case 'queued':
      case 'running':
        return 'info'
      case 'failed':
      case 'archived':
        return 'danger'
      case 'skipped':
        return 'warning'
      default:
        return 'neutral'
    }
  }

  function taskStatusLabel(status: ScheduledTaskStatus) {
    switch (status) {
      case 'enabled':
        return '启用'
      case 'disabled':
        return '停用'
      case 'archived':
        return '已归档'
    }
  }

  function runStatusLabel(status: RunStatus) {
    switch (status) {
      case 'queued':
        return '排队中'
      case 'running':
        return '运行中'
      case 'completed':
        return '完成'
      case 'failed':
        return '失败'
      case 'skipped':
        return '跳过'
    }
  }

  function targetLabel(task: ScheduledTaskSummaryDTO) {
    return scheduledTaskTargetLabel(task.target_mode)
  }

  function displayTaskTitle(task: ScheduledTaskSummaryDTO) {
    return scheduledTaskDisplayTitle(task, $locale)
  }

  function taskExecutor(task: ScheduledTaskSummaryDTO) {
    return scheduledTaskExecutor(task, workforceAgentIndex, $locale)
  }

  function executorAvatarClass(executor: ScheduledTaskExecutor) {
    if (executor.system) return 'bg-zinc-100 text-zinc-700'
    if (executor.tone === 0) return 'bg-sky-100 text-sky-700'
    if (executor.tone === 1) return 'bg-emerald-100 text-emerald-700'
    if (executor.tone === 2) return 'bg-amber-100 text-amber-700'
    return 'bg-rose-100 text-rose-700'
  }

  function executorTaskClass(executor: ScheduledTaskExecutor) {
    if (executor.system) return 'border-zinc-200 bg-zinc-100 text-zinc-800 hover:border-zinc-300'
    if (executor.tone === 0) return 'border-sky-200 bg-sky-50 text-sky-900 hover:border-sky-300'
    if (executor.tone === 1) return 'border-emerald-200 bg-emerald-50 text-emerald-900 hover:border-emerald-300'
    if (executor.tone === 2) return 'border-amber-200 bg-amber-50 text-amber-900 hover:border-amber-300'
    return 'border-rose-200 bg-rose-50 text-rose-900 hover:border-rose-300'
  }

  function scheduleTaskClass(task: ScheduledTaskSummaryDTO, executor: ScheduledTaskExecutor) {
    if (task.status === 'disabled') return 'border-zinc-200 bg-zinc-100 text-zinc-700 hover:border-zinc-300'
    return executorTaskClass(executor)
  }

  function lastRunLabel(task: ScheduledTaskSummaryDTO) {
    return task.last_run_status ? runStatusLabel(task.last_run_status) : '未运行'
  }

  function scheduleLabel(task: ScheduledTaskSummaryDTO) {
    return task.schedule_type === 'once' ? '一次性' : '周期性'
  }

  function formatDateTime(value: string | undefined) {
    if (!value) return '未安排'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return new Intl.DateTimeFormat($locale === 'en-US' ? 'en-US' : 'zh-CN', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit'
    }).format(date)
  }

  function formatTime(value: string | undefined) {
    if (!value) return '--:--'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return '--:--'
    return new Intl.DateTimeFormat($locale === 'en-US' ? 'en-US' : 'zh-CN', {
      hour: '2-digit',
      minute: '2-digit'
    }).format(date)
  }

  function timelineHourClass(hour: number) {
    if (hour === 0) return ''
    if (hour === 24) return '-translate-x-full'
    return '-translate-x-1/2'
  }

  function shortDateLabel(date: Date) {
    return new Intl.DateTimeFormat($locale === 'en-US' ? 'en-US' : 'zh-CN', {
      month: '2-digit',
      day: '2-digit'
    }).format(date)
  }

  function weekdayLabel(date: Date) {
    return new Intl.DateTimeFormat($locale === 'en-US' ? 'en-US' : 'zh-CN', {
      weekday: 'short'
    }).format(date)
  }

  function sortLabel(value: ScheduledTaskSort) {
    switch (value) {
      case 'next_run_asc':
        return '下次执行时间（最早）'
      case 'next_run_desc':
        return '下次执行时间（最晚）'
      case 'created_asc':
        return '创建时间（最早）'
      case 'created_desc':
        return '创建时间（最新）'
      case 'updated_asc':
        return '更新时间（最早）'
      case 'updated_desc':
        return '更新时间（最新）'
    }
  }

  function discussionOption(discussion: DiscussionSummaryViewModel): ScheduledTaskSelectOption {
    return {
      label: `${discussion.title} / ${discussion.id}`,
      value: discussion.id
    }
  }

  function agentOption(member: WorkforceMemberViewModel): ScheduledTaskSelectOption {
    return {
      label: `${member.displayName || member.realName || member.id} / ${member.profileId || member.id}`,
      value: member.id
    }
  }

  function memberOption(member: WorkforceMemberViewModel): ScheduledTaskSelectOption {
    const kind = memberType(member) === 'human' ? '真人' : 'Agent'
    return {
      label: `${member.displayName || member.realName || member.id} / ${kind} / ${member.profileId || member.id}`,
      value: member.profileId || member.id
    }
  }

  function toolOption(tool: { id: string; name: string; inputSchema?: Record<string, unknown> }): ScheduledTaskSelectOption {
    return {
      label: `${tool.name || tool.id} / ${tool.id}`,
      value: tool.id,
      inputSchema: tool.inputSchema
    }
  }

  function uniqueOptions(options: ScheduledTaskSelectOption[]) {
    const seen = new Set<string>()
    return options.filter((option) => {
      if (!option.value || seen.has(option.value)) return false
      seen.add(option.value)
      return true
    })
  }

  function startOfLocalDay(date: Date) {
    return new Date(date.getFullYear(), date.getMonth(), date.getDate())
  }

  function addDays(date: Date, days: number) {
    const next = new Date(date)
    next.setDate(next.getDate() + days)
    return next
  }

  function dateKey(date: Date) {
    const month = String(date.getMonth() + 1).padStart(2, '0')
    const day = String(date.getDate()).padStart(2, '0')
    return `${date.getFullYear()}-${month}-${day}`
  }

  function clamp(value: number, min: number, max: number) {
    return Math.min(max, Math.max(min, value))
  }
</script>

<svelte:head>
  <title>{t('nav.scheduler')} | Orrery</title>
</svelte:head>

<div class="page-shell max-w-[1480px] space-y-5" style="max-width: 1480px">
  <header class="page-header">
    <div class="min-w-0">
      <div class="flex items-center gap-3">
        <span class="grid h-9 w-9 shrink-0 place-items-center rounded-md bg-brand/10 text-brand">
          <CalendarClock class="h-5 w-5" />
        </span>
        <div>
          <h1 class="page-title">{t('nav.scheduler')}</h1>
          <p class="page-description">
            {t('nav.scheduler.description')}
          </p>
        </div>
      </div>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      {#if workspaceView === 'tasks'}
        <Button variant="outline" onclick={() => { taskStatsQuery.refetch(); scheduleQuery.refetch(); tasksQuery.refetch(); inboxSummaryQuery.refetch() }}>
          <RefreshCw class="h-4 w-4" />
          刷新
        </Button>
        <Button onclick={openCreateTask}>
          <Plus class="h-4 w-4" />
          新建自动化
        </Button>
      {/if}
    </div>
  </header>

  <div class="flex items-center gap-6 border-b border-border" role="tablist" aria-label="自动化工作区">
    <button
      type="button"
      role="tab"
      aria-selected={workspaceView === 'tasks'}
      class="-mb-px inline-flex h-10 items-center gap-2 border-b-2 px-0 text-sm font-medium transition-colors {workspaceView === 'tasks' ? 'border-brand text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
      onclick={() => (workspaceView = 'tasks')}
    ><CalendarClock class="h-4 w-4" />任务计划</button>
    <button
      type="button"
      role="tab"
      aria-selected={workspaceView === 'results'}
      class="-mb-px inline-flex h-10 items-center gap-2 border-b-2 px-0 text-sm font-medium transition-colors {workspaceView === 'results' ? 'border-brand text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
      onclick={() => { workspaceView = 'results'; resultTaskID = ''; resultRunID = ''; resultFilter = 'all' }}
    >
      <Inbox class="h-4 w-4" />结果收件箱
      {#if (inboxSummaryQuery.data?.unread_total ?? 0) > 0}
        <span class="min-w-5 rounded-full bg-brand/10 px-1.5 text-center text-[11px] font-semibold leading-5 text-brand">{inboxSummaryQuery.data?.unread_total}</span>
      {/if}
    </button>
  </div>

  {#if workspaceView === 'tasks'}
  <div class="mx-auto max-w-[920px] space-y-5">
  {#if actionMessage}
    <div class="rounded-md border border-brand/25 bg-brand/10 px-4 py-2.5 text-sm text-brand">
      {actionMessage}
    </div>
  {/if}
  {#if actionError}
    <div class="rounded-md border border-destructive/30 bg-destructive/10 px-4 py-2.5 text-sm text-destructive">
      {actionError}
    </div>
  {/if}

  <section class="grid grid-cols-2 gap-3 xl:grid-cols-4" aria-label="任务统计">
    <KpiCard label="任务总数" value={summary?.total ?? '-'} subtitle="未归档任务" icon={CalendarClock} />
    <KpiCard label="执行智能体" value={summary?.executors ?? '-'} subtitle={`${summary?.system ?? '-'} 项系统自动化`} icon={UsersRound} />
    <KpiCard label="启用" value={summary?.enabled ?? '-'} subtitle="未归档且启用" icon={CirclePlay} />
    <KpiCard label="失败任务" value={summary?.failed ?? '-'} subtitle="最近一次运行（含归档）" icon={AlertTriangle} />
  </section>

  {#if taskStatsQuery.isError}
    {@const error = taskStatsQuery.error as ApiClientError}
    <ErrorState title="任务统计加载失败" message={error.message} correlationId={error.correlationId} />
  {/if}

  {#if tasksQuery.isError}
    {@const error = tasksQuery.error as ApiClientError}
    <ErrorState title="自动化任务加载失败" message={error.message} correlationId={error.correlationId} />
  {:else}
    <div>
      <main class="min-w-0 space-y-5">
        <section class="overflow-hidden rounded-lg border border-border bg-card">
          <div class="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 class="flex items-center gap-2 text-sm font-semibold text-foreground">
                <CalendarDays class="h-4 w-4 text-brand" />
                自动化日程 / 执行计划
              </h2>
              <p class="mt-1 text-xs text-muted-foreground">{shortDateLabel(timelineStart)} - {shortDateLabel(addDays(timelineStart, 6))}</p>
            </div>
            <div class="flex items-center justify-between gap-2 sm:justify-end">
              <div class="inline-flex rounded-md bg-muted p-0.5" aria-label="日程视图切换">
                <button
                  type="button"
                  class="min-w-14 rounded px-2.5 py-1 text-xs font-medium transition-colors {scheduleView === 'schedule' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
                  onclick={() => (scheduleView = 'schedule')}
                >日程</button>
                <button
                  type="button"
                  class="min-w-14 rounded px-2.5 py-1 text-xs font-medium transition-colors {scheduleView === 'plan' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
                  onclick={() => (scheduleView = 'plan')}
                >执行计划</button>
              </div>
              <div class="flex items-center gap-1">
                <Button variant="ghost" size="icon" ariaLabel="上一周" onclick={() => shiftTimeline(-7)}>
                  <ChevronLeft class="h-4 w-4" />
                </Button>
                <Button variant="ghost" size="icon" ariaLabel="下一周" onclick={() => shiftTimeline(7)}>
                  <ChevronRight class="h-4 w-4" />
                </Button>
              </div>
            </div>
          </div>

          <div class="subtle-scrollbar overflow-x-auto border-t border-border bg-muted/15 px-4 py-3">
            <div class="grid min-w-[760px] grid-cols-7 gap-1">
              {#each timelineDays as day}
                <button
                  type="button"
                  class="min-h-12 rounded-md border px-2 py-1.5 text-center transition-colors {selectedDayKey === day.key ? 'border-brand/40 bg-brand/10 text-brand' : 'border-transparent text-muted-foreground hover:bg-muted/70 hover:text-foreground'}"
                  onclick={() => (selectedDayKey = day.key)}
                >
                  <div class="text-xs font-medium">{day.weekday}</div>
                  <div class="mt-0.5 text-sm font-semibold" class:text-brand={day.isToday}>{day.day}</div>
                </button>
              {/each}
            </div>
          </div>

          <div class="subtle-scrollbar overflow-x-auto border-t border-border bg-background">
            {#if scheduleQuery.isLoading}
              <div class="py-8"><LoadingState title="正在加载日程" description=" " /></div>
            {:else if selectedDayGanttItems.length === 0}
              <div class="py-9 text-center text-sm text-muted-foreground">当前日期暂无自动化任务</div>
            {:else if scheduleView === 'schedule'}
              <div class="min-w-[760px]" data-schedule-timeline>
                <div class="grid h-9 grid-cols-[166px_minmax(570px,1fr)] border-b border-border bg-muted/25 text-[11px] text-muted-foreground">
                  <div class="flex items-center px-4 font-medium">执行智能体 / 时间</div>
                  <div class="relative border-l border-border">
                    {#each timelineHours as hour}
                      <span
                        class={`absolute top-2.5 font-mono text-[10px] ${timelineHourClass(hour)}`}
                        style={`left:${(hour / 24) * 100}%`}
                      >{String(hour).padStart(2, '0')}:00</span>
                    {/each}
                  </div>
                </div>
                <div class="divide-y divide-border/70">
                  {#each selectedDayAgentLanes as lane (lane.executor.key)}
                    <article class="grid grid-cols-[166px_minmax(570px,1fr)]" data-agent-schedule-lane data-agent-id={lane.executor.id || 'system'}>
                      <div class="flex min-w-0 items-center gap-2.5 px-3 py-2">
                        {#if lane.executor.profileId}
                          <WorkforceAvatar profileId={lane.executor.profileId} name={lane.executor.name} human={false} class="h-8 w-8 rounded-md" textClass="text-xs" />
                        {:else}
                          <span class={`grid h-8 w-8 shrink-0 place-items-center rounded-md text-xs font-semibold ${executorAvatarClass(lane.executor)}`}>{lane.executor.initials}</span>
                        {/if}
                        <div class="min-w-0">
                          <div class="truncate text-xs font-medium text-foreground">{lane.executor.name}</div>
                          <div class="mt-0.5 truncate text-[10px] text-muted-foreground">{lane.items.length} 项自动化 · {lane.executor.subtitle}</div>
                        </div>
                      </div>
                      <div class="divide-y divide-border/50 border-l border-border">
                        {#each lane.items as item (item.task.scheduled_task_id)}
                          <div class="relative h-11 overflow-hidden">
                            {#each timelineHours as hour}
                              <span class="absolute inset-y-0 w-px bg-border/55" style={`left:${(hour / 24) * 100}%`}></span>
                            {/each}
                            <span class="absolute inset-y-0 w-px bg-foreground/20" style={`left:${item.timePercent}%`}></span>
                            <button
                              type="button"
                              class={`absolute top-1/2 flex h-7 w-44 -translate-x-1/2 -translate-y-1/2 items-center gap-2 rounded-md border px-2.5 text-left transition-colors ${scheduleTaskClass(item.task, lane.executor)}`}
                              style={`left:${item.cardPercent}%`}
                              title={`${formatTime(item.task.next_run_at)} · ${displayTaskTitle(item.task)}`}
                              onclick={() => openRunHistory(item.task)}
                              data-schedule-task-chip
                              data-task-status={item.task.status}
                            >
                              <span class="shrink-0 font-mono text-[10px] font-semibold">{formatTime(item.task.next_run_at)}</span>
                              <span class="min-w-0 truncate text-[11px]">{displayTaskTitle(item.task)}</span>
                            </button>
                          </div>
                        {/each}
                      </div>
                    </article>
                  {/each}
                </div>
              </div>
            {:else}
              <div class="min-w-[760px]" data-execution-plan>
                <div class="grid h-9 grid-cols-[38px_58px_146px_minmax(0,1fr)_68px_90px] items-center border-b border-border bg-muted/25 px-3 text-[11px] font-medium text-muted-foreground">
                  <span>节点</span>
                  <span>时间</span>
                  <span>执行智能体</span>
                  <span>执行内容</span>
                  <span>状态</span>
                  <span class="text-right">操作</span>
                </div>
                <div class="divide-y divide-border/70">
                  {#each selectedDayGanttItems as item, index (item.task.scheduled_task_id)}
                    {@const executor = taskExecutor(item.task)}
                    <article class="grid min-h-16 grid-cols-[38px_58px_146px_minmax(0,1fr)_68px_90px] items-center px-3" data-execution-plan-row>
                      <div class="relative self-stretch">
                        {#if index < selectedDayGanttItems.length - 1}<span class="absolute bottom-0 left-3 top-8 w-px bg-border"></span>{/if}
                        <span class="absolute left-0 top-1/2 grid h-6 w-6 -translate-y-1/2 place-items-center rounded-full border border-brand/30 bg-brand/10 text-[10px] font-semibold text-brand">{index + 1}</span>
                      </div>
                      <span class="font-mono text-[11px] font-semibold text-foreground">{formatTime(item.task.next_run_at)}</span>
                      <div class="flex min-w-0 items-center gap-2 pr-3">
                        {#if executor.profileId}
                          <WorkforceAvatar profileId={executor.profileId} name={executor.name} human={false} class="h-6 w-6 rounded" textClass="text-[10px]" />
                        {:else}
                          <span class={`grid h-6 w-6 shrink-0 place-items-center rounded text-[10px] font-semibold ${executorAvatarClass(executor)}`}>{executor.initials}</span>
                        {/if}
                        <span class="truncate text-[11px] font-medium text-foreground">{executor.name}</span>
                      </div>
                      <button type="button" class="min-w-0 py-2 pr-3 text-left" onclick={() => openRunHistory(item.task)}>
                        <span class="block truncate text-xs font-medium text-foreground">{displayTaskTitle(item.task)}</span>
                        <span class="mt-1 block truncate text-[10px] text-muted-foreground">{targetLabel(item.task)} · {scheduleLabel(item.task)}</span>
                      </button>
                      <Badge tone={statusTone(item.task.status)}>{taskStatusLabel(item.task.status)}</Badge>
                      <div class="flex justify-end gap-0.5">
                        <button type="button" class="grid h-7 w-7 place-items-center rounded text-muted-foreground hover:bg-accent hover:text-foreground" aria-label={`查看结果报告：${displayTaskTitle(item.task)}`} title="查看结果报告" onclick={() => openRunHistory(item.task)}><FileText class="h-3.5 w-3.5" /></button>
                        <button type="button" class="grid h-7 w-7 place-items-center rounded text-muted-foreground hover:bg-accent hover:text-foreground disabled:pointer-events-none disabled:opacity-50" aria-label="立即运行" title="立即运行" disabled={busyTaskID === item.task.scheduled_task_id || item.task.status === 'archived'} onclick={() => runNow(item.task)}><Play class="h-3.5 w-3.5" /></button>
                        <button type="button" class="grid h-7 w-7 place-items-center rounded text-muted-foreground hover:bg-accent hover:text-foreground disabled:pointer-events-none disabled:opacity-50" aria-label={`编辑任务：${displayTaskTitle(item.task)}`} title="编辑任务" disabled={busyTaskID === item.task.scheduled_task_id} onclick={() => openEditTask(item.task)}><Pencil class="h-3.5 w-3.5" /></button>
                      </div>
                    </article>
                  {/each}
                </div>
                <div class="border-t border-border bg-muted/15 px-4 py-2 text-right text-[11px] text-muted-foreground">共 {selectedDayGanttItems.length} 个执行节点</div>
              </div>
            {/if}
          </div>
        </section>

        <section class="rounded-xl border border-border bg-card p-5">
          <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
            <h2 class="text-base font-semibold text-foreground">自动化任务列表</h2>
            <div class="flex flex-1 flex-wrap items-center justify-end gap-3">
              <div class="relative min-w-56 flex-1 lg:max-w-72">
                <label for="scheduled-task-search" class="sr-only">搜索任务</label>
                <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground/60" />
                <Input
                  id="scheduled-task-search"
                  bind:value={searchTerm}
                  ariaLabel="搜索任务名称、类型或描述"
                  class="pl-9"
                  oninput={resetTaskPage}
                />
              </div>
              <div class="w-36 shrink-0">
                <Select
                  value={statusFilter}
                  options={[
                    { label: '状态：全部', value: '' },
                    { label: '启用', value: 'enabled' },
                    { label: '停用', value: 'disabled' },
                    { label: '已归档', value: 'archived' }
                  ]}
                  onchange={(event) => { statusFilter = event.currentTarget.value as TaskStatusFilter; resetTaskPage() }}
                />
              </div>
              <div class="w-48 shrink-0">
                <Select
                  value={sortOrder}
                  options={[
                    { label: sortLabel('created_desc'), value: 'created_desc' },
                    { label: sortLabel('next_run_asc'), value: 'next_run_asc' },
                    { label: sortLabel('next_run_desc'), value: 'next_run_desc' },
                    { label: sortLabel('updated_desc'), value: 'updated_desc' },
                    { label: sortLabel('created_asc'), value: 'created_asc' },
                    { label: sortLabel('updated_asc'), value: 'updated_asc' }
                  ]}
                  onchange={(event) => { sortOrder = event.currentTarget.value as ScheduledTaskSort; resetTaskPage() }}
                />
              </div>
              <label class="inline-flex h-9 items-center gap-2 rounded-md border border-border bg-input-background px-3 text-sm text-muted-foreground">
                <input type="checkbox" bind:checked={includeArchived} class="h-4 w-4 accent-brand" onchange={resetTaskPage} />
                显示归档
              </label>
            </div>
          </div>

          <div class="subtle-scrollbar mt-4 overflow-x-auto rounded-xl border border-border">
            <table class="w-full min-w-[800px] table-fixed border-collapse text-sm">
              <colgroup>
                <col class="w-[188px]" />
                <col class="w-[166px]" />
                <col class="w-[104px]" />
                <col />
                <col class="w-[88px]" />
                <col class="w-[128px]" />
              </colgroup>
              <thead class="bg-muted/45 text-xs text-muted-foreground">
                <tr>
                  <th class="px-3 py-3 text-left font-medium">任务名称</th>
                  <th class="px-2 py-3 text-left font-medium">执行智能体</th>
                  <th class="px-2 py-3 text-left font-medium">周期 / 状态</th>
                  <th class="px-2 py-3 text-left font-medium">下次执行</th>
                  <th class="px-2 py-3 text-left font-medium">最近运行</th>
                  <th class="px-2 py-3 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border">
                {#if tasksQuery.isLoading}
                  <tr>
                    <td colspan="6" class="px-4 py-8">
                      <LoadingState title="正在加载自动化任务" description=" " />
                    </td>
                  </tr>
                {:else if tableTasks.length === 0}
                  <tr>
                    <td colspan="6" class="px-4 py-8">
                      <EmptyState title="暂无自动化任务" description="调整筛选条件或新建一个自动化任务。" />
                    </td>
                  </tr>
                {:else}
                  {#each tableTasks as task}
                    {@const executor = taskExecutor(task)}
                    <tr class="cursor-pointer bg-card transition-colors hover:bg-muted/30" onclick={() => openRunHistory(task)}>
                      <td class="px-3 py-3">
                        <div class="flex items-center gap-3">
                          <span class="grid h-8 w-8 shrink-0 place-items-center rounded-md bg-brand/10 text-brand">
                            <CalendarClock class="h-4 w-4" />
                          </span>
                          <div class="min-w-0">
                            <div class="truncate font-medium text-foreground">{displayTaskTitle(task)}</div>
                            <div class="mt-0.5 truncate text-xs text-muted-foreground">{targetLabel(task)} · {task.scheduled_task_id}</div>
                          </div>
                        </div>
                      </td>
                      <td class="px-2 py-3">
                        <div class="flex min-w-0 items-center gap-2">
                          {#if executor.profileId}
                            <WorkforceAvatar profileId={executor.profileId} name={executor.name} human={false} class="h-7 w-7 rounded" textClass="text-[10px]" />
                          {:else}
                            <span class={`grid h-7 w-7 shrink-0 place-items-center rounded text-[10px] font-semibold ${executorAvatarClass(executor)}`}>{executor.initials}</span>
                          {/if}
                          <div class="min-w-0">
                            <div class="truncate text-xs font-medium text-foreground">{executor.name}</div>
                            <div class="mt-0.5 truncate text-[10px] text-muted-foreground">{executor.subtitle}</div>
                          </div>
                        </div>
                      </td>
                      <td class="px-2 py-3">
                        <div class="flex flex-wrap items-center gap-1">
                          <span class="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">{scheduleLabel(task)}</span>
                          <Badge tone={statusTone(task.status)}>{taskStatusLabel(task.status)}</Badge>
                        </div>
                      </td>
                      <td class="whitespace-nowrap px-2 py-3 text-xs text-muted-foreground">{formatDateTime(task.next_run_at)}</td>
                      <td class="px-2 py-3">
                        <Badge tone={task.last_run_status ? statusTone(task.last_run_status) : 'neutral'}>{lastRunLabel(task)}</Badge>
                        {#if task.failure_reason}
                          <div class="mt-1 truncate text-[10px] text-destructive" title={task.failure_reason}>{task.failure_reason}</div>
                        {/if}
                      </td>
                      <td class="px-1 py-3">
                        <div class="flex justify-end gap-0.5">
                          <button type="button" class="grid h-7 w-7 place-items-center rounded text-muted-foreground hover:bg-accent hover:text-foreground" aria-label={`查看结果报告：${displayTaskTitle(task)}`} title="查看结果报告" onclick={(event) => { event.stopPropagation(); openRunHistory(task) }}><FileText class="h-3.5 w-3.5" /></button>
                          <button type="button" class="grid h-7 w-7 place-items-center rounded text-muted-foreground hover:bg-accent hover:text-foreground disabled:pointer-events-none disabled:opacity-50" aria-label="立即运行" title="立即运行" disabled={busyTaskID === task.scheduled_task_id || task.status === 'archived'} onclick={(event) => { event.stopPropagation(); runNow(task) }}><Play class="h-3.5 w-3.5" /></button>
                          <button type="button" class="grid h-7 w-7 place-items-center rounded text-muted-foreground hover:bg-accent hover:text-foreground disabled:pointer-events-none disabled:opacity-50" aria-label="编辑" title="编辑" disabled={busyTaskID === task.scheduled_task_id} onclick={(event) => { event.stopPropagation(); openEditTask(task) }}><Pencil class="h-3.5 w-3.5" /></button>
                          <button type="button" class="grid h-7 w-7 place-items-center rounded text-destructive hover:bg-destructive/10 disabled:pointer-events-none disabled:opacity-50" aria-label="删除任务" title="删除任务" disabled={busyTaskID === task.scheduled_task_id || task.status === 'archived'} onclick={(event) => { event.stopPropagation(); deleteTask(task) }}><Trash2 class="h-3.5 w-3.5" /></button>
                        </div>
                      </td>
                    </tr>
                  {/each}
                {/if}
              </tbody>
            </table>
          </div>

          <div class="mt-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-end">
            <div class="text-sm text-muted-foreground">共 {tableTotal} 条</div>
            <div class="flex items-center justify-end gap-2">
              <Button variant="outline" size="icon" ariaLabel="上一页" disabled={currentPage <= 1} onclick={() => goToPage(currentPage - 1)}>
                <ChevronLeft class="h-4 w-4" />
              </Button>
              <span class="grid h-9 min-w-9 place-items-center rounded-md bg-brand px-3 text-sm font-medium text-brand-foreground">{currentPage}</span>
              <Button variant="outline" size="icon" ariaLabel="下一页" disabled={currentPage >= totalPages} onclick={() => goToPage(currentPage + 1)}>
                <ChevronRight class="h-4 w-4" />
              </Button>
              <div class="w-28 shrink-0">
                <Select
                  value={String(pageSize)}
                  options={[
                    { label: '10 条/页', value: '10' },
                    { label: '20 条/页', value: '20' },
                    { label: '50 条/页', value: '50' }
                  ]}
                  onchange={(event) => { pageSize = Number(event.currentTarget.value); currentPage = 1 }}
                />
              </div>
            </div>
          </div>
        </section>
      </main>

    </div>
  {/if}
  </div>
  {:else}
    <ScheduledRunInbox initialTaskID={resultTaskID} initialRunID={resultRunID} initialFilter={resultFilter} />
  {/if}

  <SchedulerTaskFormDialog
    bind:form
    open={showEditor}
    editing={editingTaskID.length > 0}
    {formErrors}
    {discussionOptions}
    {agentOptions}
    {memberOptions}
    {toolOptions}
    saving={commands.createTask.isPending || commands.updateTask.isPending}
    onClose={() => (showEditor = false)}
    onSubmit={saveTask}
  />

  <!-- Legacy modal report kept inactive during the inbox rollout. -->
  <!-- <Dialog
    open={showRunHistory}
    title={selectedTask ? `结果报告 · ${displayTaskTitle(selectedTask)}` : '结果报告'}
    size="wide"
    class="max-w-3xl"
    onClose={closeRunHistory}
  >
    <div class="space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-border pb-4">
        <div class="w-36 shrink-0">
          <Select
            value={runStatusFilter}
            options={[
              { label: '全部状态', value: '' },
              { label: '排队中', value: 'queued' },
              { label: '运行中', value: 'running' },
              { label: '完成', value: 'completed' },
              { label: '失败', value: 'failed' },
              { label: '跳过', value: 'skipped' }
            ]}
            onchange={(event) => { runStatusFilter = event.currentTarget.value as RunStatusFilter; resetRunPage() }}
          />
        </div>
        <div class="flex items-center gap-2">
          {#if selectedTask?.target_mode === 'agent_task' && selectedTaskTenantID}<a class="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-3 text-xs font-medium hover:bg-muted" href={ontologyExecutionHref()}><Network class="h-3.5 w-3.5" aria-hidden="true" />在本体中查看</a>{/if}
          <Button variant="outline" size="sm" disabled={!selectedTask || busyTaskID === selectedTask.scheduled_task_id || selectedTask.status === 'archived'} onclick={forceRunSelectedTask}><Play class="h-3.5 w-3.5" />强制运行</Button>
        </div>
      </div>

      {#if actionMessage}
        <div class="rounded-md border border-brand/25 bg-brand/10 px-3 py-2 text-sm text-brand">{actionMessage}</div>
      {/if}
      {#if actionError}
        <div class="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{actionError}</div>
      {/if}

      {#if runsQuery.isLoading}
        <div class="py-12"><LoadingState title="正在加载结果报告" description=" " /></div>
      {:else if runsQuery.isError}
        {@const error = runsQuery.error as ApiClientError}
        <ErrorState title="结果报告加载失败" message={error.message} correlationId={error.correlationId} />
      {:else if runs.length === 0}
        <div class="flex min-h-64 flex-col items-center justify-center text-center">
          <PackageOpen class="h-14 w-14 text-brand/30" />
          <div class="mt-4 text-sm font-semibold text-foreground">暂无结果报告</div>
          <div class="mt-2 text-xs text-muted-foreground">任务运行完成后，报告和通知状态会显示在这里</div>
        </div>
      {:else}
        <div class="max-h-[58vh] space-y-3 overflow-y-auto pr-1 subtle-scrollbar">
          {#each runs as run (run.scheduled_run_id)}
            <article
              class="rounded-md border p-4 text-xs {run.scheduled_run_id === sourceRunID ? 'border-brand bg-brand/5' : 'border-border bg-background'}"
              data-scheduled-run={run.scheduled_run_id}
              data-selected-run={run.scheduled_run_id === sourceRunID ? 'true' : undefined}
            >
              <div class="flex items-center justify-between gap-2">
                <Badge tone={statusTone(run.status)}>{runStatusLabel(run.status)}</Badge>
                <div class="flex items-center gap-2"><span class="text-muted-foreground">{formatDateTime(run.created_at)}</span>{#if run.target_mode === 'agent_task' && selectedTaskTenantID}<a class="text-primary hover:underline" href={ontologyExecutionHref(run)}>在本体中查看</a>{/if}</div>
              </div>
              <div class="mt-3 space-y-1 text-muted-foreground">
                <div>{run.trigger_type} / {targetLabel(run)} / attempt {run.attempt}</div>
                {#if run.command_id}
                  <div class="truncate font-mono">{run.command_id}</div>
                {/if}
                {#if run.failure_reason}
                  <div class="text-destructive">{run.failure_reason}</div>
                {/if}
				{#if run.responsibility_reconcile}
				  <div class="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 rounded-md border border-border bg-muted/30 p-2 sm:grid-cols-4" data-responsibility-reconcile-stats>
					<span>Candidates {run.responsibility_reconcile.candidates}</span>
					<span>Claimed {run.responsibility_reconcile.claimed}</span>
					<span>Dispatched {run.responsibility_reconcile.dispatched}</span>
					<span>Recovered {run.responsibility_reconcile.recovered_outcomes}</span>
					<span>Blockers {run.responsibility_reconcile.blockers_rechecked}</span>
					<span>Resolved {run.responsibility_reconcile.blockers_resolved}</span>
					<span>Timed out {run.responsibility_reconcile.timed_out}</span>
					<span class:text-destructive={run.responsibility_reconcile.failed > 0}>Failed {run.responsibility_reconcile.failed}</span>
				  </div>
				{/if}
                {#if run.responsibility_evaluation_batch}
                  <ResponsibilityEvaluationBatch
                    scheduledTaskID={run.scheduled_task_id}
                    scheduledRunID={run.scheduled_run_id}
                    batch={run.responsibility_evaluation_batch}
                  />
                {/if}
                {#if run.responsibility_evaluation}
                  <div class="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 rounded-md border border-border bg-muted/30 p-2 sm:grid-cols-4" data-responsibility-evaluation>
                    <span>{run.responsibility_evaluation.actionable ? t('context.responsibilityEvaluation.actionable') : t('context.responsibilityEvaluation.noAction')}</span>
                    <span>{t(`context.responsibilityEvaluation.decision.${run.responsibility_evaluation.decision_kind}`)}</span>
                    <span>{t('context.responsibilityEvaluation.sourceRevision', { revision: run.responsibility_evaluation.source_revision ?? '-' })}</span>
                    <span>{run.responsibility_evaluation.reason ?? 'evaluated'}</span>
                    <span>{t('context.responsibilityEvaluation.candidates', { count: run.responsibility_evaluation.candidate_count })}</span>
                    <span>{t('context.responsibilityEvaluation.runnable', { count: run.responsibility_evaluation.actionable_count })}</span>
                    <span>{t('context.responsibilityEvaluation.waiting', { count: run.responsibility_evaluation.waiting_count })}</span>
                    <span>{t('context.responsibilityEvaluation.review', { count: run.responsibility_evaluation.review_count })}</span>
                    {#if run.responsibility_evaluation.escalated_count > 0}<span>{t('context.responsibilityEvaluation.escalated', { count: run.responsibility_evaluation.escalated_count })}</span>{/if}
                    {#if run.responsibility_evaluation.paused_count > 0}<span>{t('context.responsibilityEvaluation.paused', { count: run.responsibility_evaluation.paused_count })}</span>{/if}
                    {#if run.responsibility_evaluation.invalid_count > 0}<span>{t('context.responsibilityEvaluation.invalid', { count: run.responsibility_evaluation.invalid_count })}</span>{/if}
                    {#if run.responsibility_evaluation.selected_assignment_state}
                      <span>{t('context.responsibilityEvaluation.selected', { state: run.responsibility_evaluation.selected_assignment_state, priority: run.responsibility_evaluation.selected_assignment_priority ?? 0 })}</span>
                    {/if}
                    {#if run.responsibility_evaluation.selected_due_at}<span>{t('context.responsibilityEvaluation.due', { date: formatDateTime(run.responsibility_evaluation.selected_due_at) })}</span>{/if}
                    {#if run.responsibility_evaluation.selected_assignment_source_kind}
                      <span>{t('context.responsibilityEvaluation.assignmentSource', { source: run.responsibility_evaluation.selected_assignment_source_kind })}</span>
                    {/if}
                  </div>
                {/if}
                {#if run.mission_id || run.responsibility_id}
                  <div class="mt-2 flex flex-wrap gap-2">
                    {#if run.mission_id}
                      <Button href={`/missions/${encodeURIComponent(run.mission_id)}`} variant="outline" size="sm">Mission</Button>
                    {/if}
                    {#if run.responsibility_id}
                      <Button href={`/responsibilities?responsibility=${encodeURIComponent(run.responsibility_id)}`} variant="outline" size="sm">Responsibility</Button>
                    {/if}
                    {#if run.task_id}<span class="inline-flex items-center font-mono text-[11px] text-muted-foreground">Task {run.task_id}</span>{/if}
                  </div>
                {/if}
                {#if run.result?.summary || (run.result?.artifacts?.length ?? 0) > 0 || run.status === 'completed'}
                  <div class="mt-3 border-t border-border pt-3 text-foreground" data-scheduled-run-report>
                    <div class="flex flex-wrap items-center justify-between gap-2">
                      <span class="inline-flex items-center gap-1.5 font-medium"><FileText class="h-3.5 w-3.5" />最终结果报告</span>
                      {#if run.delivery_status}
                        <Badge tone={deliveryStatusTone(run.delivery_status)}>{deliveryStatusLabel(run.delivery_status)}</Badge>
                      {/if}
                    </div>
                    {#if run.result?.summary}
                      <div class="mt-2 whitespace-pre-wrap break-words text-sm leading-6">{run.result.summary}</div>
                    {:else}
                      <div class="mt-2 text-muted-foreground">本次运行未生成可展示的摘要。</div>
                    {/if}
                    <ScheduledRunArtifacts
                      scheduledTaskID={run.scheduled_task_id}
                      scheduledRunID={run.scheduled_run_id}
                      artifacts={run.result?.artifacts ?? []}
                    />
                  </div>
                {:else if run.delivery_status}
                  <div class="mt-3 border-t border-border pt-3">
                    <Badge tone={deliveryStatusTone(run.delivery_status)}>{deliveryStatusLabel(run.delivery_status)}</Badge>
                  </div>
                {/if}
              </div>
            </article>
          {/each}
          {#if runTotal > runPageSize}
            <div class="flex items-center justify-end gap-2 pt-2">
              <Button variant="outline" size="icon" ariaLabel="上一页" disabled={runPage <= 1} onclick={() => goToRunPage(runPage - 1)}>
                <ChevronLeft class="h-4 w-4" />
              </Button>
              <span class="text-xs text-muted-foreground">{runPage} / {runTotalPages}</span>
              <Button variant="outline" size="icon" ariaLabel="下一页" disabled={runPage >= runTotalPages} onclick={() => goToRunPage(runPage + 1)}>
                <ChevronRight class="h-4 w-4" />
              </Button>
            </div>
          {/if}
        </div>
      {/if}
    </div>
  </Dialog> -->
</div>
