<script lang="ts">
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  import { Activity, AlertTriangle, ChevronRight, Clock, FileCheck, ListChecks, Target, Users } from 'lucide-svelte'
  import MissionRoom from '#lib/components/mission/MissionRoom.svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import ActivityFeed from '#lib/components/shared/ActivityFeed.svelte'
  import ProfileCard from '#lib/components/shared/ProfileCard.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import { isActiveTaskStatus, isDoneTaskStatus } from '#lib/mission/taskStatus'
  import { formatList, formatTaskEvidenceLabel } from '#lib/mission/workspaceDisplay'
  import {
    missionQueryKeys,
    subscribeMissionStream,
    useQADecisionMutation,
    useMissionControlQuery,
    useTaskAssignMutation,
    useTaskCreateMutation,
    useTaskSubmitResultMutation
  } from '#lib/queries/missions'
  import { queryClient } from '#lib/query-client'
  import type { ApiClientError } from '#lib/api/client'

  interface Props {
    data: { missionId: string }
  }

  type WorkspaceTab = 'overview' | 'team' | 'tasks' | 'evidence' | 'operations' | 'timeline'

  const phaseKeys = ['intake', 'understand', 'staff', 'contract', 'accept', 'coordinate', 'execute', 'verify', 'deliver']
  const workspaceTabs: Array<{ id: WorkspaceTab; label: TranslationKey; icon: typeof Target }> = [
    { id: 'overview', label: 'workspace.tab.overview', icon: Target },
    { id: 'team', label: 'workspace.tab.team', icon: Users },
    { id: 'tasks', label: 'workspace.tab.tasks', icon: ListChecks },
    { id: 'evidence', label: 'workspace.tab.evidence', icon: FileCheck },
    { id: 'operations', label: 'workspace.operations', icon: Activity },
    { id: 'timeline', label: 'workspace.tab.timeline', icon: Clock }
  ]
  const priorityLabelKeys: Record<string, TranslationKey> = {
    critical: 'workspace.priority.critical',
    high: 'workspace.priority.high',
    medium: 'workspace.priority.medium',
    low: 'workspace.priority.low',
    normal: 'workspace.priority.normal'
  }
  const priorityValues = ['normal', 'low', 'medium', 'high', 'critical']

  function workspaceTabFromURL(url: URL): WorkspaceTab {
    const tab = url.searchParams.get('tab')
    return workspaceTabs.some((item) => item.id === tab) ? tab as WorkspaceTab : 'overview'
  }

  let { data }: Props = $props()
  const missionId = $derived(data.missionId)
  let activeTab = $state<WorkspaceTab>(workspaceTabFromURL($page.url))
  let taskGoal = $state('')
  let taskAssigneeId = $state('')
  let taskScope = $state('')
  let taskPriority = $state('normal')
  let taskDueAt = $state('')
  let taskExpectedDeliverables = $state('')
  let selectedTaskId = $state('')
  let selectedAssigneeId = $state('')
  let assignmentReason = $state('')
  let resultSummary = $state('')
  let resultAgentId = $state('')
  let resultRunId = $state('')
  let qaMessage = $state('')
  let mutationMessage = $state('')
  let showOperations = $state(false)

  const resolvedMissionId = $derived(missionId)
  const controlQuery = useMissionControlQuery(() => resolvedMissionId)
  const createTask = useTaskCreateMutation(() => resolvedMissionId)
  const assignTask = useTaskAssignMutation(() => resolvedMissionId)
  const submitResult = useTaskSubmitResultMutation(() => resolvedMissionId)
  const qaDecision = useQADecisionMutation(() => resolvedMissionId)
  let t = $derived(createTranslator($locale))
  const view = $derived(controlQuery.data)
  const canCreateTask = $derived(taskGoal.trim().length > 0 && !createTask.isPending)
  const canAssignTask = $derived(selectedTaskId.length > 0 && selectedAssigneeId.trim().length > 0 && !assignTask.isPending)
  const canSubmitResult = $derived(selectedTaskId.length > 0 && resultSummary.trim().length > 0 && !submitResult.isPending)
  const selectedTask = $derived(view?.tasks.find((task) => task.id === selectedTaskId))
  const priorityOptions = $derived(priorityValues.map((value) => ({ value, label: priorityLabel(value) })))
  const taskOptions = $derived([
    { value: '', label: t('workspace.noTasks') },
    ...(view?.tasks.map((task) => ({ value: task.id, label: task.title })) ?? [])
  ])
  const qaActionDisabled = $derived(!selectedTask?.qaRef || qaDecision.isPending)
  const discussionHref = $derived(
    view?.discussionConversationId ? `/discussions/${encodeURIComponent(view.discussionConversationId)}` : ''
  )
  const activityItems = $derived(
    view?.events.map((event) => ({
      id: event.id,
      type: event.kind,
      label: eventLabel(event.kind),
      author: event.authorName,
      authorType: event.authorType,
      body: event.body,
      time: event.postedAt,
      metadata: {
        mentionedUsers: event.mentions,
        toolName: event.toolName,
        oldStatus: event.oldStatus,
        newStatus: event.newStatus
      }
    })) ?? []
  )

  $effect(() => {
    activeTab = workspaceTabFromURL($page.url)
  })

  $effect(() => {
    const missionId = resolvedMissionId
    return subscribeMissionStream(missionId, () => Promise.all([
      queryClient.invalidateQueries({ queryKey: missionQueryKeys.control(missionId) }),
      queryClient.invalidateQueries({ queryKey: missionQueryKeys.team(missionId) }),
      queryClient.invalidateQueries({ queryKey: missionQueryKeys.tasks(missionId) }),
      queryClient.invalidateQueries({ queryKey: missionQueryKeys.progress(missionId) }),
      queryClient.invalidateQueries({ queryKey: missionQueryKeys.evidence(missionId) }),
      queryClient.invalidateQueries({ queryKey: missionQueryKeys.qa(missionId) })
    ]))
  })

  function tx(key: string) {
    return t(key as TranslationKey)
  }

  function selectWorkspaceTab(tab: WorkspaceTab) {
    activeTab = tab
    const nextURL = new URL($page.url)
    if (tab === 'overview') nextURL.searchParams.delete('tab')
    else nextURL.searchParams.set('tab', tab)
    void goto(`${nextURL.pathname}${nextURL.search}${nextURL.hash}`, { replaceState: false, reset: false })
  }

  function displayField(value: string) {
    return value === 'Not provided' || value === '' ? t('workspace.notProvided') : value
  }

  function missionMemberForIdentity(id: string) {
    return view?.team.find((member) => [member.id, member.profileId].includes(id))
  }

  function missionTaskProfileId(id: string) {
    return missionMemberForIdentity(id)?.profileId || id
  }

  function missionTaskIsHuman(id: string, assigneeType: string) {
    const member = missionMemberForIdentity(id)
    return member ? (member.type || '').toLowerCase() === 'human' : (assigneeType || '').toLowerCase() === 'human'
  }

  function priorityTone(priority: string) {
    if (priority === 'critical' || priority === 'high') return 'danger'
    if (priority === 'medium') return 'warning'
    return 'neutral'
  }

  function priorityLabel(priority: string) {
    const key = priorityLabelKeys[priority]
    return key ? t(key) : displayField(priority)
  }

  function eventLabel(kind: string) {
    const normalized = kind.replaceAll('_', '-')
    if (normalized.includes('tool')) return t('discussion.toolCall')
    if (normalized.includes('status')) return t('discussion.statusChange')
    if (normalized.includes('milestone')) return t('discussion.milestone')
    if (normalized.includes('assignment')) return t('discussion.assignment')
    if (normalized.includes('mention')) return t('discussion.mention')
    if (normalized.includes('reply')) return t('discussion.replyType')
    return t('discussion.message')
  }

  function setMutationError(error: unknown, fallback: TranslationKey) {
    mutationMessage = error instanceof Error ? error.message : t(fallback)
  }

  function submitTask() {
    mutationMessage = ''
    createTask.mutate(
      { goal: taskGoal, assigneeId: taskAssigneeId, scope: taskScope, priority: taskPriority, dueAt: taskDueAt, expectedDeliverables: taskExpectedDeliverables },
      {
        onSuccess: () => {
          taskGoal = ''
          taskAssigneeId = ''
          taskScope = ''
          taskPriority = 'normal'
          taskDueAt = ''
          taskExpectedDeliverables = ''
        },
        onError: (error) => setMutationError(error, 'workspace.createTaskFailed')
      }
    )
  }

  function submitAssignment() {
    mutationMessage = ''
    assignTask.mutate(
      { taskId: selectedTaskId, assigneeId: selectedAssigneeId, reason: assignmentReason },
      {
        onSuccess: () => {
          selectedAssigneeId = ''
          assignmentReason = ''
        },
        onError: (error) => setMutationError(error, 'workspace.assignTaskFailed')
      }
    )
  }

  function submitTaskResult() {
    mutationMessage = ''
    submitResult.mutate(
      {
        taskId: selectedTaskId,
        agentId: resultAgentId || selectedTask?.assigneeId || 'manual',
        runId: resultRunId,
        summary: resultSummary
      },
      {
        onSuccess: () => {
          resultSummary = ''
          resultAgentId = ''
          resultRunId = ''
        },
        onError: (error) => setMutationError(error, 'workspace.submitResultFailed')
      }
    )
  }

  function decideQa(decision: 'approved' | 'rejected') {
    if (!selectedTask?.qaRef) return
    mutationMessage = ''
    qaDecision.mutate(
      { qaId: selectedTask.qaRef, taskId: selectedTask.id, decision, message: qaMessage },
      {
        onSuccess: () => {
          qaMessage = ''
        },
        onError: (error) => setMutationError(error, 'workspace.qaDecisionFailed')
      }
    )
  }
</script>

<svelte:head>
  <title>{t('nav.missionWorkspace')} | Orrery</title>
</svelte:head>

{#snippet textField(label: TranslationKey, value: string, setValue: (value: string) => void)}
  <label class="flex min-h-9 overflow-hidden rounded-md border border-input bg-input-background">
    <span class="grid w-32 shrink-0 place-items-center border-r border-border bg-muted px-3 text-xs font-medium text-muted-foreground">
      {t(label)}
    </span>
    <Input value={value} oninput={(event) => setValue(event.currentTarget.value)} class="h-9 rounded-none border-0 bg-transparent shadow-none focus-visible:ring-0" />
  </label>
{/snippet}

{#snippet selectField(label: TranslationKey, value: string, setValue: (value: string) => void, options: Array<{ label: string; value: string }>)}
  <label class="flex min-h-9 overflow-hidden rounded-md border border-input bg-input-background">
    <span class="grid w-32 shrink-0 place-items-center border-r border-border bg-muted px-3 text-xs font-medium text-muted-foreground">
      {t(label)}
    </span>
    <Select value={value} {options} onchange={(event) => setValue(event.currentTarget.value)} class="h-9 rounded-none border-0 bg-transparent shadow-none focus-visible:ring-0" />
  </label>
{/snippet}

{#if controlQuery.isLoading}
  <LoadingState title={t('state.loadingMissionDescription')} description="" />
{:else if controlQuery.isError}
  {@const error = controlQuery.error as ApiClientError}
  <ErrorState title={t('state.errorMission')} message={error.message} correlationId={error.correlationId} />
{:else if view}
  {@const mission = view.mission}
  {@const blockedTasks = view.tasks.filter((task) => task.status === 'blocked')}
  {@const doneTasks = view.tasks.filter((task) => isDoneTaskStatus(task.status))}
  {@const activeTasks = view.tasks.filter((task) => isActiveTaskStatus(task.status))}
  {@const currentPhaseIndex = Math.max(0, phaseKeys.indexOf(mission.phase))}

  <div class="-mx-3 -my-4 flex h-[calc(100vh-3rem)] flex-col overflow-hidden bg-background sm:-m-6">
    <header class="border-b border-border bg-card px-4 py-4 sm:px-6">
      <div class="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
        <span>{t('workspace.breadcrumb')}</span>
        <ChevronRight class="h-3 w-3" />
        <span>{mission.id}</span>
      </div>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex flex-wrap items-center gap-3">
          <h1 class="text-[22px] font-semibold leading-[33px] text-foreground">{mission.title}</h1>
          <Badge tone={mission.statusTone}>{tx(`status.${mission.status}`) || mission.status}</Badge>
          <Badge tone={mission.riskTone}>{tx(`risk.${mission.riskLevel}`) || mission.riskLevel}</Badge>
        </div>
        {#if view.discussionRestricted}
          <div class="flex max-w-md items-center gap-2 text-sm text-muted-foreground" role="status">
            <AlertTriangle class="h-4 w-4 shrink-0 text-status-blocked" />
            <span>{t('workspace.discussionRestricted')}</span>
          </div>
        {:else if discussionHref}
          <div class="flex items-center gap-2">
            <Button href={discussionHref}>{t('workspace.openDiscussion')}</Button>
          </div>
        {/if}
      </div>
      <div class="mt-3 flex flex-wrap items-center gap-6 text-sm text-muted-foreground">
        <div class="flex items-center gap-2">
          <span>{t('workspace.progress')}</span>
          <div class="h-2 w-32 overflow-hidden rounded-full bg-muted">
            <div class="h-full rounded-full bg-brand" style={`width:${mission.deliveryPercent}%`}></div>
          </div>
          <span class="font-medium text-foreground">{mission.deliveryPercent}%</span>
        </div>
        <div>{t('workspace.phase')}: <span class="font-medium text-foreground">{tx(`phase.${mission.phase}`) || mission.phase}</span></div>
        <div>{t('workspace.deadline')}: <span class="font-medium text-foreground">{mission.deadline}</span></div>
        <div>{t('workspace.team')}: <span class="font-medium text-foreground">{t('workspace.teamCount', { count: mission.teamSize })}</span></div>
        {#if mission.originTodoId}
          <a class="font-medium text-brand hover:underline" href={`/todos/${encodeURIComponent(mission.originTodoId)}`}>
            {t('workspace.originTodo', { todoId: mission.originTodoId })}
          </a>
          {#if mission.executionAttempt > 0}
            <div>{t('workspace.executionAttempt', { attempt: mission.executionAttempt })}</div>
          {/if}
        {/if}
      </div>
      <div class="mt-4 grid gap-3 md:grid-cols-5">
        <div class="rounded-xl border border-border bg-card/45 px-3 py-2">
          <div class="text-[10px] uppercase tracking-wide text-muted-foreground">{t('workspace.controlVersion', { version: view.summary.version })}</div>
          <div class="mt-1 text-sm font-semibold text-foreground">{t('workspace.summary.delivery')} {view.summary.deliveryPercent}%</div>
        </div>
        <div class="rounded-xl border border-border bg-card/45 px-3 py-2">
          <div class="text-[10px] uppercase tracking-wide text-muted-foreground">{t('workspace.activeTasks')}</div>
          <div class="mt-1 text-lg font-semibold text-status-active">{view.summary.activeTasks}</div>
        </div>
        <div class="rounded-xl border border-border bg-card/45 px-3 py-2">
          <div class="text-[10px] uppercase tracking-wide text-muted-foreground">{t('workspace.pendingApprovals')}</div>
          <div class="mt-1 text-lg font-semibold text-status-blocked">{view.summary.pendingApprovals}</div>
        </div>
        <div class="rounded-xl border border-border bg-card/45 px-3 py-2">
          <div class="text-[10px] uppercase tracking-wide text-muted-foreground">{t('workspace.riskCount')}</div>
          <div class="mt-1 text-lg font-semibold text-status-failed">{view.summary.risks}</div>
        </div>
        <div class="rounded-xl border border-border bg-card/45 px-3 py-2">
          <div class="text-[10px] uppercase tracking-wide text-muted-foreground">{t('workspace.blockerCount')}</div>
          <div class="mt-1 text-lg font-semibold text-status-failed">{view.summary.blockers}</div>
        </div>
      </div>
    </header>

    <div class="subtle-scrollbar overflow-x-auto border-b border-border bg-card px-4 sm:px-6">
      <div class="flex h-10 min-w-max items-center gap-1">
        {#each workspaceTabs as tab}
          {@const Icon = tab.icon}
          <button
            class="inline-flex h-8 items-center gap-1.5 rounded-md px-3 text-sm text-muted-foreground hover:bg-muted/50 data-[active=true]:bg-muted data-[active=true]:text-foreground"
            data-active={activeTab === tab.id}
            type="button"
            onclick={() => selectWorkspaceTab(tab.id)}
          >
            <Icon class="h-3.5 w-3.5" />
            {t(tab.label)}
          </button>
        {/each}
      </div>
    </div>

    <div class="subtle-scrollbar flex-1 overflow-auto p-4 sm:p-6">
      {#if activeTab === 'overview'}
        <div class="space-y-6">
          <section class="panel p-4">
            <h2 class="text-sm font-semibold text-foreground">{t('workspace.goals')}</h2>
            <ul class="mt-3 space-y-2">
              {#each mission.goals.length ? mission.goals : [mission.description] as goal}
                <li class="flex items-center gap-2 text-sm text-foreground"><span class="h-1.5 w-1.5 rounded-full bg-status-done"></span>{displayField(goal)}</li>
              {/each}
            </ul>
          </section>

          <section class="panel p-4">
            <h2 class="text-sm font-semibold text-foreground">{t('workspace.phaseProgress')}</h2>
            <div class="mt-4 grid grid-cols-9 gap-1">
              {#each phaseKeys as phase, index}
                <div class="min-w-0 text-center">
                  <div class="h-1.5 rounded-full" class:bg-status-done={index < currentPhaseIndex} class:bg-brand={index === currentPhaseIndex} class:bg-muted={index > currentPhaseIndex}></div>
                  <div class="mt-1 truncate text-[10px]" class:text-brand={index === currentPhaseIndex} class:text-muted-foreground={index !== currentPhaseIndex}>{tx(`phase.${phase}`)}</div>
                </div>
              {/each}
            </div>
            {#if view.progressFocus.length > 0}
              <div class="mt-4 flex flex-wrap gap-2">
                {#each view.progressFocus as focus}
                  <div class="rounded-md border border-border bg-card/40 px-2.5 py-1.5 text-xs">
                    <span class="font-medium text-foreground">{focus.label}</span>
                    <span class="ml-2 text-muted-foreground">{displayField(focus.status)}</span>
                    <span class="ml-2 text-muted-foreground">{focus.occurredAt}</span>
                    {#if focus.details}
                      <p class="mt-1 max-w-xl text-muted-foreground">{focus.details}</p>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}
          </section>

          <div class="grid gap-4 lg:grid-cols-2">
            <section class="panel p-4">
              <h2 class="text-sm font-semibold text-foreground">{t('workspace.risks')}</h2>
              <div class="mt-3 space-y-2">
                {#each blockedTasks as task}
                  <div class="flex items-center gap-2 text-sm text-foreground">
                    <AlertTriangle class="h-3.5 w-3.5 text-status-failed" />
                    <span>{task.title}</span>
                  </div>
                {:else}
                  <p class="text-sm text-muted-foreground">{t('workspace.noRisks')}</p>
                {/each}
              </div>
            </section>

            <section class="panel p-4">
              <h2 class="text-sm font-semibold text-foreground">{t('workspace.taskSummary')}</h2>
              <div class="mt-4 grid grid-cols-3 gap-3 text-center">
                <div><div class="text-xl font-semibold text-status-done">{doneTasks.length}</div><div class="text-xs text-muted-foreground">{t('workspace.completed')}</div></div>
                <div><div class="text-xl font-semibold text-status-active">{activeTasks.length}</div><div class="text-xs text-muted-foreground">{t('workspace.inProgress')}</div></div>
                <div><div class="text-xl font-semibold text-status-failed">{blockedTasks.length}</div><div class="text-xs text-muted-foreground">{t('discussion.blockers')}</div></div>
              </div>
            </section>
          </div>
        </div>
      {:else if activeTab === 'team'}
        <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {#each view.team as member}
            <ProfileCard
              profileId={member.profileId || member.id}
              name={member.name}
              subtitle={member.role}
              source={member.type}
              meta={t('workspace.departmentPosition', {
                department: displayField(member.department),
                position: displayField(member.position)
              })}
              status={`${t('workspace.memberStatus')}: ${displayField(member.status)}`}
            />
          {/each}
        </div>
      {:else if activeTab === 'tasks'}
        <div class="space-y-3">
          <section class="panel p-3">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div>
                <div class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{t('workspace.operations')}</div>
                <div class="mt-1 text-xs text-muted-foreground">{t('workspace.operationsDescription')}</div>
              </div>
              <Button variant="outline" size="sm" onclick={() => (showOperations = !showOperations)}>
                <ListChecks class="mr-2 h-3.5 w-3.5" />
                {t('workspace.toggleOperations')}
              </Button>
            </div>

            {#if showOperations}
              <div class="mt-3 grid gap-3 xl:grid-cols-[1.1fr_1fr_1fr]">
                <div class="grid gap-2">
                  <div class="text-xs font-semibold text-muted-foreground">{t('workspace.createTask')}</div>
                  {@render textField('workspace.taskGoal', taskGoal, (value) => (taskGoal = value))}
                  <div class="grid gap-2 sm:grid-cols-2">
                    {@render textField('workspace.assigneeId', taskAssigneeId, (value) => (taskAssigneeId = value))}
                    {@render textField('workspace.taskBoundary', taskScope, (value) => (taskScope = value))}
                  </div>
                  <div class="grid gap-2 sm:grid-cols-3">
                    {@render selectField('workspace.priority', taskPriority, (value) => (taskPriority = value), priorityOptions)}
                    {@render textField('workspace.due', taskDueAt, (value) => (taskDueAt = value))}
                    {@render textField('workspace.expectedDeliverables', taskExpectedDeliverables, (value) => (taskExpectedDeliverables = value))}
                  </div>
                  <Button disabled={!canCreateTask} onclick={submitTask}>{t('workspace.createTask')}</Button>
                </div>

                <div class="grid gap-2">
                  <div class="text-xs font-semibold text-muted-foreground">{t('workspace.assignTask')}</div>
                  {@render selectField('workspace.task', selectedTaskId, (value) => (selectedTaskId = value), taskOptions)}
                  {@render textField('workspace.assigneeId', selectedAssigneeId, (value) => (selectedAssigneeId = value))}
                  {@render textField('workspace.qaMessage', assignmentReason, (value) => (assignmentReason = value))}
                  <Button disabled={!canAssignTask} onclick={submitAssignment}>{t('workspace.assignTask')}</Button>
                </div>

                <div class="grid gap-2">
                  <div class="text-xs font-semibold text-muted-foreground">{t('workspace.submitResult')}</div>
                  {@render textField('workspace.resultSummary', resultSummary, (value) => (resultSummary = value))}
                  <div class="grid gap-2 sm:grid-cols-2">
                    {@render textField('workspace.assigneeId', resultAgentId, (value) => (resultAgentId = value))}
                    {@render textField('workspace.latestResult', resultRunId, (value) => (resultRunId = value))}
                  </div>
                  <div class="flex flex-wrap gap-2">
                    <Button disabled={!canSubmitResult} onclick={submitTaskResult}>{t('workspace.submitResult')}</Button>
                    <Button disabled={qaActionDisabled} onclick={() => decideQa('approved')}>{t('workspace.approveQa')}</Button>
                    <Button disabled={qaActionDisabled} onclick={() => decideQa('rejected')}>{t('workspace.rejectQa')}</Button>
                  </div>
                  {@render textField('workspace.qaMessage', qaMessage, (value) => (qaMessage = value))}
                </div>
              </div>
            {/if}
          </section>

          {#if mutationMessage}
            <div class="rounded-md border border-destructive/25 bg-destructive/10 px-3 py-2 text-xs text-destructive-foreground">
              {mutationMessage}
            </div>
          {/if}

          {#each view.tasks as task}
            <article class="panel p-4">
              <div class="flex items-start gap-3">
                {#if task.assigneeId}
                  {#if missionTaskProfileId(task.assigneeId)}
                    <WorkforceAvatar
                      profileId={missionTaskProfileId(task.assigneeId)}
                      name={displayField(task.assigneeName)}
                      human={missionTaskIsHuman(task.assigneeId, task.assigneeType)}
                      class="h-8 w-8 rounded-full"
                      textClass="text-[10px]"
                    />
                  {:else}
                    <span class="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-brand text-[10px] font-semibold text-white">
                      {displayField(task.assigneeName).slice(0, 2)}
                    </span>
                  {/if}
                {:else}
                  <span class="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-muted text-[10px] font-semibold text-muted-foreground">--</span>
                {/if}
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <h2 class="text-sm font-semibold text-foreground">{task.title}</h2>
                    <Badge tone={task.statusTone}>{tx(`status.${task.status}`) || task.status}</Badge>
                    <span class="ml-auto">
                      <Badge tone={priorityTone(task.priority)}>{priorityLabel(task.priority)}</Badge>
                    </span>
                  </div>
                  <p class="mt-1 text-xs text-muted-foreground">{task.description}</p>
                  <div class="mt-2 flex flex-wrap gap-3 text-xs text-muted-foreground">
                    <span>{t('workspace.assignee')}: {task.assigneeId ? displayField(task.assigneeName) : t('workspace.unassigned')}</span>
                    <span>{t('workspace.due')}: {task.dueAt}</span>
                    <span>{t('workspace.qaStatus')}: {displayField(task.qaStatus)}</span>
                    <span>{t('workspace.updatedAt')}: {task.updatedAt}</span>
                    {#if task.boundary !== 'Not provided'}
                      <span>{t('workspace.taskBoundary')}: {displayField(task.boundary)}</span>
                    {/if}
                  </div>
                  <div class="mt-3 grid gap-2 text-xs text-muted-foreground md:grid-cols-2 xl:grid-cols-4">
                    <div>
                      <span class="text-muted-foreground">{t('workspace.acceptanceCriteria')}</span>
                      <div class="mt-1 text-foreground">{formatList(task.acceptanceCriteria) || '-'}</div>
                    </div>
                    <div>
                      <span class="text-muted-foreground">{t('workspace.expectedDeliverables')}</span>
                      <div class="mt-1 text-foreground">{formatList(task.deliverables) || '-'}</div>
                    </div>
                    <div>
                      <span class="text-muted-foreground">{t('workspace.requiredEvidence')}</span>
                      <div class="mt-1 text-foreground">{formatList(task.evidenceRequirements) || '-'}</div>
                    </div>
                    <div>
                      <span class="text-muted-foreground">{t('workspace.qaRef')}</span>
                      <div class="mt-1 text-foreground">{displayField(task.qaRef)}</div>
                    </div>
                  </div>
                  {#if task.latestResultRef || task.qaMessage || task.blockedReason}
                    <div class="mt-3 rounded-md border border-border bg-card/40 p-3 text-xs text-muted-foreground">
                      {#if task.latestResultRef}
                        <div>{t('workspace.latestResult')}: <span class="text-foreground">{task.latestResultRef}</span></div>
                      {/if}
                      {#if task.qaMessage}
                        <div class="mt-1">{t('workspace.qaMessage')}: <span class="text-foreground">{task.qaMessage}</span></div>
                      {/if}
                      {#if task.blockedReason}
                        <div class="mt-1 text-status-failed">{t('workspace.blocked')}: {task.blockedReason}</div>
                      {/if}
                    </div>
                  {/if}
                </div>
              </div>
            </article>
          {:else}
            <div class="panel p-8 text-center text-sm text-muted-foreground">{t('state.noTasks')}</div>
          {/each}
        </div>
      {:else if activeTab === 'evidence'}
        <div class="space-y-3">
          {#each view.tasks.filter((task) => task.deliverables.length > 0 || task.evidenceRequirements.length > 0 || task.status === 'done' || task.status === 'completed') as task}
            <article class="panel p-4">
              <div class="flex items-center gap-3">
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <h2 class="text-sm font-semibold text-foreground">{task.title}</h2>
                    <Badge tone={task.statusTone}>{tx(`status.${task.status}`) || task.status}</Badge>
                  </div>
                  <div class="mt-2 text-xs text-muted-foreground">{t('workspace.deliverables')}: {formatList(task.deliverables) || '-'}</div>
                  <div class="mt-1 text-xs text-muted-foreground">
                    {t('workspace.evidence')}: {formatTaskEvidenceLabel(task.evidenceRequirements, view.evidenceCount, (count) =>
                      t('workspace.evidenceCount', { count })
                    )}
                  </div>
                  <div class="mt-3 grid gap-2 text-xs text-muted-foreground md:grid-cols-2">
                    <div class="rounded-md border border-border bg-card/40 p-3">
                      <div class="font-medium text-foreground">{t('workspace.evidenceSummary')}</div>
                      <div class="mt-1">{t('workspace.evidenceCount', { count: view.evidenceCount })}</div>
                    </div>
                    <div class="rounded-md border border-border bg-card/40 p-3">
                      <div class="font-medium text-foreground">{t('workspace.qaSummary')}</div>
                      <div class="mt-1">{task.qaMessage || t('workspace.notProvided')}</div>
                    </div>
                  </div>
                </div>
                {#if discussionHref}<Button href={discussionHref} variant="outline" size="sm">{t('workspace.viewDetails')}</Button>{/if}
              </div>
            </article>
          {:else}
            <div class="panel p-8 text-center text-sm text-muted-foreground">{t('workspace.noEvidenceTasks')}</div>
          {/each}
        </div>
      {:else if activeTab === 'operations'}
        <MissionRoom missionId={resolvedMissionId} />
      {:else if activeTab === 'timeline'}
        <ActivityFeed items={activityItems} emptyLabel={t('workspace.noTimelineEvents')} />
      {/if}
    </div>
  </div>
{/if}
