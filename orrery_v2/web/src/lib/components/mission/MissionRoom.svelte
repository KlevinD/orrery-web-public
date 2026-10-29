<script lang="ts">
  import {
    Activity,
    AlertTriangle,
    ArrowRight,
    CheckCircle2,
    Clock,
    Cpu,
    FileCheck,
    Hash,
    Layers3,
    ListChecks,
    MessageSquare,
    RotateCcw,
    ShieldCheck,
    Target,
    Users,
    Wrench,
    XCircle
  } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import AgentRunRecoveryPanel from './AgentRunRecoveryPanel.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import { isActiveTaskStatus, isDoneTaskStatus } from '#lib/mission/taskStatus'
  import {
    missionQueryKeys,
    subscribeMissionStream,
    useAgentRunCancelMutation,
    useAgentRunRetryMutation,
    useMissionControlQuery,
    useMissionDiscussionQuery,
    useMissionOpsQuery
  } from '#lib/queries/missions'
  import { queryClient } from '#lib/query-client'
  import type { ApiClientError } from '#lib/api/client'
  import type {
    ActionCardViewModel,
    AgentLaneViewModel,
    ApprovalGateViewModel,
    DiscussionMessageViewModel,
    EvidenceGateViewModel,
    MissionTaskViewModel,
    OpsFocusViewModel,
    RuntimeSessionViewModel
  } from '#lib/types/view-models'

  interface Props {
    missionId: string
  }

  let { missionId }: Props = $props()
  let t = $derived(createTranslator($locale))

  const effectiveMissionId = $derived(missionId)
  const controlQuery = useMissionControlQuery(() => effectiveMissionId)
  const opsQuery = useMissionOpsQuery(() => effectiveMissionId)
  const discussionQuery = useMissionDiscussionQuery(() => effectiveMissionId)
  const cancelRunMutation = useAgentRunCancelMutation(() => effectiveMissionId)
  const retryRunMutation = useAgentRunRetryMutation(() => effectiveMissionId)
  let lastRetriedRunId = $state('')

  const view = $derived(controlQuery.data)
  const ops = $derived(opsQuery.data)
  const discussion = $derived(discussionQuery.data)
  const mission = $derived(view?.mission)
  const tasks = $derived(view?.tasks ?? [])
  const activeTasks = $derived(tasks.filter((task) => isActiveTaskStatus(task.status) || task.status === 'blocked'))
  const doneTasks = $derived(tasks.filter((task) => isDoneTaskStatus(task.status)))
  const blockedTasks = $derived(tasks.filter((task) => task.status === 'blocked' || task.blockedReason))
  const taskById = $derived(new Map(tasks.map((task) => [task.id, task])))
  const operationalFocus = $derived((ops?.currentFocus ?? []).filter((focus) => focus.kind !== 'recovery_advice'))
  const recoveryAdvice = $derived((ops?.currentFocus ?? []).filter((focus) => focus.kind === 'recovery_advice'))
  const primaryFocus = $derived(operationalFocus[0])
  const activeSessions = $derived((ops?.runtimeSessions ?? []).filter((session) => session.canCancel))
  const interruptedSessions = $derived((ops?.runtimeSessions ?? []).filter((session) => session.canRetry || session.isStale))
  const latestEvents = $derived((discussion?.messages?.length ? discussion.messages : view?.events ?? []).filter(isMeaningfulEvent).slice(-8))

  $effect(() => {
    if (!effectiveMissionId) return
    const missionId = effectiveMissionId
    return subscribeMissionStream(missionId, () => Promise.all([
      queryClient.invalidateQueries({ queryKey: missionQueryKeys.control(missionId) }),
      queryClient.invalidateQueries({ queryKey: missionQueryKeys.ops(missionId) }),
      queryClient.invalidateQueries({ queryKey: missionQueryKeys.discussion(missionId) })
    ]))
  })

  function cancelRuntime(runId: string) {
    if (!runId || cancelRunMutation.isPending) return
    cancelRunMutation.mutate({
      runId,
      body: { reason: 'operator requested cancellation from mission room' }
    })
  }

  function retryRuntime(runId: string) {
    if (!runId || retryRunMutation.isPending) return
    lastRetriedRunId = runId
    retryRunMutation.mutate({ runId })
  }

  function tx(key: string) {
    return t(key as TranslationKey)
  }

  function display(value: string | undefined) {
    if (!value || value === 'Not provided' || value === 'Not assigned') return '未提供'
    return value
  }

  function statusLabel(status: string) {
    const key = `status.${status}` as TranslationKey
    const label = t(key)
    return label === key ? display(status) : label
  }

  function riskLabel(risk: string | undefined) {
    const normalized = risk || 'normal'
    if (normalized === 'normal') return '常规风险'
    const key = `risk.${normalized}` as TranslationKey
    const translated = t(key)
    return translated === key ? display(normalized) : translated
  }

  function priorityLabel(priority: string) {
    if (priority === 'critical') return '关键'
    if (priority === 'high') return '高优'
    if (priority === 'low') return '低优'
    return '常规'
  }

  function taskTitle(taskId: string) {
    return taskById.get(taskId)?.title || taskId || '未绑定任务'
  }

  function taskMeta(taskId: string) {
    const task = taskById.get(taskId)
    if (!task) return ''
    return [display(task.assigneeName), statusLabel(task.status)].filter(Boolean).join(' · ')
  }

  function laneWorkload(lane: AgentLaneViewModel) {
    const active = lane.activeTaskIds.length
    const waiting = lane.waitingTaskIds.length
    if (active === 0 && waiting === 0) return '无任务'
    return `${active} 活跃 · ${waiting} 等待`
  }

  function runtimeSummary(session: RuntimeSessionViewModel) {
    return [session.runtimeKind, session.queueId, session.lastEvent].filter(Boolean).join(' · ') || session.runId
  }

  function runtimeHealthLabel(session: RuntimeSessionViewModel) {
    if (session.isStale) return 'stale'
    if (session.laneHealth) return session.laneHealth
    if (session.isAlive) return 'alive'
    return session.status
  }

  function runtimeHealthTone(session: RuntimeSessionViewModel) {
    const health = runtimeHealthLabel(session)
    if (health === 'healthy' || health === 'alive' || health === 'completed') return 'success'
    if (health === 'unknown' || health === 'queued') return 'warning'
    if (health === 'stale' || health === 'failed' || health === 'cancelled' || health === 'blocked' || health === 'timed_out') return 'danger'
    return session.statusTone
  }

  function resourceTone(resourceType: string) {
    if (resourceType === 'tool') return 'blue'
    if (resourceType === 'skill') return 'violet'
    if (resourceType === 'mcp_server') return 'teal'
    if (resourceType === 'memory_profile') return 'amber'
    if (resourceType === 'knowledge_base') return 'rose'
    return 'neutral'
  }

  function focusIcon(kind: string) {
    if (kind === 'approval') return ShieldCheck
    if (kind === 'blocker') return AlertTriangle
    if (kind === 'inbox') return MessageSquare
    return Target
  }

  function actionIcon(kind: string) {
    if (kind.includes('approve')) return ShieldCheck
    if (kind.includes('blocker')) return AlertTriangle
    if (kind.includes('assign')) return Users
    return MessageSquare
  }

  function actorTone(type: string | undefined) {
    const normalized = (type ?? '').toLowerCase()
    if (normalized.includes('human') || normalized.includes('user')) return 'source-human'
    if (normalized.includes('agent') || normalized.includes('ai')) return 'source-agent'
    return 'neutral'
  }

  function eventIcon(kind: string) {
    const normalized = kind.replaceAll('_', '-')
    if (normalized.includes('tool')) return Wrench
    if (normalized.includes('status')) return ArrowRight
    if (normalized.includes('assignment')) return ListChecks
    if (normalized.includes('milestone')) return CheckCircle2
    return MessageSquare
  }

  function eventLabel(kind: string) {
    const normalized = kind.replaceAll('_', '-')
    if (normalized.includes('tool')) return '工具调用'
    if (normalized.includes('status')) return '状态变化'
    if (normalized.includes('assignment')) return '任务委派'
    if (normalized.includes('milestone')) return '阶段里程碑'
    if (normalized.includes('reply')) return '回复'
    return '消息'
  }

  function isMeaningfulEvent(event: DiscussionMessageViewModel) {
    const kind = event.kind.toLowerCase().replaceAll('_', '-')
    if (kind.includes('joined') || kind.includes('member-joined')) return false
    return Boolean(event.body?.trim())
  }

  function taskTone(task: MissionTaskViewModel) {
    if (task.status === 'blocked' || task.blockedReason) return 'danger'
    if (task.qaRef || task.qaStatus === 'pending') return 'warning'
    if (isDoneTaskStatus(task.status)) return 'success'
    return 'info'
  }
</script>

<svelte:head>
  <title>{t('nav.missionRoom')} | Orrery</title>
</svelte:head>

{#if controlQuery.isLoading || opsQuery.isLoading || discussionQuery.isLoading}
  <LoadingState title="正在打开任务作战室" description="同步运行态、聊天和任务上下文。" />
{:else if controlQuery.isError || opsQuery.isError || discussionQuery.isError}
  {@const error = (controlQuery.error || opsQuery.error || discussionQuery.error) as ApiClientError}
  <ErrorState title="任务作战室无法加载" message={error.message} correlationId={error.correlationId} />
{:else if ops && mission && view}
  <div class="grid min-h-[48rem] grid-cols-[minmax(0,1fr)] overflow-hidden rounded-lg border border-border bg-background xl:grid-cols-[minmax(0,1fr)_18.75rem]" data-mission-operations>

    <main class="grid min-h-0 min-w-0 grid-rows-[auto_auto_minmax(0,1fr)_auto] border-r border-border bg-background">
      <header class="border-b border-border bg-card px-6 py-4">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="min-w-0">
            <div class="flex items-center gap-2 text-xs text-muted-foreground">
              <Hash class="h-3.5 w-3.5" />
              <span>{ops.missionId}</span>
              <span>v{ops.version}</span>
            </div>
            <h1 class="mt-1 truncate text-[22px] font-semibold tracking-normal text-foreground">{ops.title}</h1>
            <p class="mt-1 max-w-3xl text-sm leading-relaxed text-muted-foreground">{display(mission.description)}</p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <Badge tone={ops.statusTone}>{statusLabel(ops.status)}</Badge>
            <Badge tone="neutral">{display(ops.phase)}</Badge>
            <Badge tone={mission.riskTone}>{riskLabel(mission.riskLevel)}</Badge>
          </div>
        </div>
      </header>

      <section class="border-b border-border bg-card/65 px-6 py-3">
        <div class="grid gap-3 md:grid-cols-3 2xl:grid-cols-6">
          <div class="rounded-xl border border-border bg-background px-3 py-2">
            <div class="flex items-center gap-2 text-[10px] font-semibold uppercase text-muted-foreground"><Users class="h-3.5 w-3.5" />Agent</div>
            <div class="mt-1 text-lg font-semibold text-foreground">{ops.summary.agentCount}</div>
          </div>
          <div class="rounded-xl border border-border bg-background px-3 py-2">
            <div class="flex items-center gap-2 text-[10px] font-semibold uppercase text-muted-foreground"><Activity class="h-3.5 w-3.5" />运行中</div>
            <div class="mt-1 text-lg font-semibold text-status-active">{ops.summary.runningSessions}</div>
          </div>
          <div class="rounded-xl border border-border bg-background px-3 py-2">
            <div class="flex items-center gap-2 text-[10px] font-semibold uppercase text-muted-foreground"><AlertTriangle class="h-3.5 w-3.5" />中断</div>
            <div class="mt-1 text-lg font-semibold text-destructive">{ops.summary.blockedSessions}</div>
          </div>
          <div class="rounded-xl border border-border bg-background px-3 py-2">
            <div class="flex items-center gap-2 text-[10px] font-semibold uppercase text-muted-foreground"><ShieldCheck class="h-3.5 w-3.5" />待处理</div>
            <div class="mt-1 text-lg font-semibold text-status-blocked">{ops.summary.openActions}</div>
          </div>
          <div class="rounded-xl border border-border bg-background px-3 py-2">
            <div class="flex items-center gap-2 text-[10px] font-semibold uppercase text-muted-foreground"><FileCheck class="h-3.5 w-3.5" />缺证据</div>
            <div class="mt-1 text-lg font-semibold text-status-blocked">{ops.summary.missingEvidence}</div>
          </div>
          <div class="rounded-xl border border-border bg-background px-3 py-2">
            <div class="flex items-center gap-2 text-[10px] font-semibold uppercase text-muted-foreground"><Clock class="h-3.5 w-3.5" />审批</div>
            <div class="mt-1 text-lg font-semibold text-foreground">{ops.summary.pendingApprovals}</div>
          </div>
        </div>
      </section>

      <section class="subtle-scrollbar min-h-0 overflow-auto px-6 py-5">
        <div class="mx-auto max-w-6xl space-y-7">
          {#if primaryFocus}
            <section>
              <div class="mb-3 flex items-center justify-between gap-3">
                <h2 class="text-sm font-semibold text-foreground">当前焦点</h2>
                <Badge tone={primaryFocus.tone}>{priorityLabel(primaryFocus.priority)}</Badge>
              </div>
              <div class="grid gap-3 lg:grid-cols-2">
                {#each operationalFocus.slice(0, 4) as focus (focus.id)}
                  {@render FocusCard(focus)}
                {/each}
              </div>
            </section>
          {/if}

          {#if recoveryAdvice.length}
            <section aria-label="失败核查建议">
              <h2 class="mb-3 text-sm font-semibold text-foreground">失败核查建议</h2>
              <p class="mb-3 text-xs text-muted-foreground">建议需要结合原始证据核实。执行结果未知时，先核对下游状态。</p>
              <div class="grid gap-3 lg:grid-cols-2">
                {#each recoveryAdvice as focus (focus.id)}
                  {@render FocusCard(focus)}
                {/each}
              </div>
            </section>
          {/if}

          <section>
            <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
              <h2 class="flex items-center gap-2 text-sm font-semibold text-foreground"><Layers3 class="h-4 w-4 text-brand" />执行泳道</h2>
              <Badge tone="info">{ops.agentLanes.length} 个 Agent</Badge>
            </div>
            <div class="grid gap-3 lg:grid-cols-2">
              {#each ops.agentLanes as lane (lane.agentId)}
                <article class="rounded-xl border border-border bg-card p-4">
                  <div class="flex items-start justify-between gap-3">
                    <div class="flex min-w-0 items-start gap-3">
                      <WorkforceAvatar profileId={lane.profileId || lane.agentId} name={lane.displayName} human={(lane.agentKind || '').toLowerCase() === 'human'} class="h-10 w-10 rounded-full" textClass="text-[11px]" />
                      <div class="min-w-0">
                        <div class="flex min-w-0 flex-wrap items-center gap-2">
                          <h3 class="truncate text-sm font-semibold text-foreground">{display(lane.displayName)}</h3>
                          <Badge tone={lane.statusTone}>{display(lane.status)}</Badge>
                        </div>
                        <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
                          <span>{display(lane.missionRole)}</span>
                          <span>{display(lane.agentKind)}</span>
                          <span>{laneWorkload(lane)}</span>
                        </div>
                      </div>
                    </div>
                    {#if lane.inboxOpenCount > 0}
                      <Badge tone="warning">{lane.inboxOpenCount} 提及</Badge>
                    {/if}
                  </div>

                  <div class="mt-3 space-y-2">
                    {#each lane.activeTaskIds.slice(0, 3) as taskId}
                      <div class="rounded-md bg-muted/45 px-3 py-2">
                        <div class="truncate text-xs font-semibold text-foreground">{taskTitle(taskId)}</div>
                        {#if taskMeta(taskId)}
                          <div class="mt-1 truncate text-[11px] text-muted-foreground">{taskMeta(taskId)}</div>
                        {/if}
                      </div>
                    {/each}
                  </div>

                  <div class="mt-3 flex flex-wrap gap-1.5">
                    {#each lane.resourceBindings.slice(0, 8) as resource}
                      <Badge tone={resourceTone(resource.resourceType)}>{resource.label}</Badge>
                    {:else}
                      <span class="text-xs text-muted-foreground">无绑定资源</span>
                    {/each}
                  </div>
                </article>
              {:else}
                <div class="rounded-xl border border-dashed border-border p-6 text-center text-sm text-muted-foreground">当前 Mission 还没有 Agent 泳道。</div>
              {/each}
            </div>
          </section>

          <section>
            <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
              <h2 class="flex items-center gap-2 text-sm font-semibold text-foreground"><Cpu class="h-4 w-4 text-brand" />运行会话</h2>
              <div class="flex items-center gap-2">
                {#if activeSessions.length > 0}
                  <Badge tone="info">{activeSessions.length} active</Badge>
                {/if}
                {#if interruptedSessions.length > 0}
                  <Badge tone="danger">{interruptedSessions.length} interrupted</Badge>
                {/if}
              </div>
            </div>
            <div class="space-y-2">
              {#each ops.runtimeSessions as session (session.runId)}
                <article class="rounded-xl border border-border bg-card px-4 py-3">
                  <div class="grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto]">
                    <div class="min-w-0">
                      <div class="flex flex-wrap items-center gap-2">
                        <span class="truncate text-sm font-semibold text-foreground">{session.runId}</span>
                        <Badge tone={session.statusTone}>{display(session.status)}</Badge>
                        <Badge tone={runtimeHealthTone(session)}>{runtimeHealthLabel(session)}</Badge>
                        {#if session.runtimeKind}
                          <Badge tone="neutral">{session.runtimeKind}</Badge>
                        {/if}
                      </div>
                      <div class="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
                        <span>Agent: {display(session.agentId)}</span>
                        <span>任务: {taskTitle(session.taskId)}</span>
                        <span>更新: {session.updatedAt}</span>
                      </div>
                      <p class="mt-2 text-xs leading-relaxed text-muted-foreground">{display(session.errorSummary || runtimeSummary(session))}</p>
                    </div>
                    <div class="flex items-center gap-2 lg:justify-end">
                      {#if session.canCancel}
                        <Button
                          variant="outline"
                          size="sm"
                          ariaLabel={`取消运行 ${session.runId}`}
                          disabled={cancelRunMutation.isPending}
                          onclick={() => cancelRuntime(session.runId)}
                        >
                          <XCircle class="h-3.5 w-3.5" />
                          取消
                        </Button>
                      {/if}
                      {#if session.canRetry && session.runtimeKind !== 'managed'}
                        <Button
                          variant="outline"
                          size="sm"
                          ariaLabel={`重试运行 ${session.runId}`}
                          disabled={retryRunMutation.isPending}
                          onclick={() => retryRuntime(session.runId)}
                        >
                          <RotateCcw class="h-3.5 w-3.5" />
                          重试
                        </Button>
                      {/if}
                    </div>
                  </div>
                  {#if session.runtimeKind === 'managed' && session.taskId}
                    <AgentRunRecoveryPanel
                      missionId={effectiveMissionId}
                      runId={session.runId}
                      active={session.canCancel}
                      retryPending={retryRunMutation.isPending}
                      retryError={lastRetriedRunId === session.runId ? retryRunMutation.error : undefined}
                      onRetry={() => retryRuntime(session.runId)}
                    />
                  {/if}
                </article>
              {:else}
                <div class="rounded-xl border border-dashed border-border p-6 text-center text-sm text-muted-foreground">还没有运行会话。</div>
              {/each}
            </div>
          </section>

          {#if ops.actionCards.length > 0}
            <section>
              <div class="mb-3 flex items-center justify-between gap-3">
                <h2 class="text-sm font-semibold text-foreground">行动卡</h2>
                <Badge tone="warning">{ops.actionCards.length} open</Badge>
              </div>
              <div class="grid gap-3 lg:grid-cols-2">
                {#each ops.actionCards as card (card.actionId)}
                  {@render ActionCard(card)}
                {/each}
              </div>
            </section>
          {/if}
        </div>
      </section>

      <footer class="border-t border-border bg-card px-6 py-3">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
            <span>{ops.agentLanes.length} Agent</span>
            <span>{ops.runtimeSessions.length} runs</span>
            <span>{activeTasks.length} 活跃任务</span>
            <span>{doneTasks.length} 完成</span>
          </div>
          <div class="flex items-center gap-2">
            {#if view.discussionConversationId}
              <Button href={`/discussions/${encodeURIComponent(view.discussionConversationId)}`} size="sm">聊天</Button>
            {/if}
          </div>
        </div>
      </footer>
    </main>

    <aside class="right-sidebar-shell right-sidebar-rail hidden xl:flex">
      <header class="right-sidebar-header">
        <span class="truncate text-[13px] font-medium">{t('workspace.operations')}</span>
      </header>
      <div class="right-sidebar-body right-sidebar-scroll subtle-scrollbar space-y-5 p-4">
        <section>
          <h2 class="mb-3 flex items-center gap-2 text-xs font-semibold uppercase text-muted-foreground"><ShieldCheck class="h-3.5 w-3.5" />Autonomy</h2>
          <div class="rounded-xl border border-border bg-card p-4">
            <div class="flex items-center justify-between gap-3">
              <span class="text-sm font-semibold text-foreground">{display(ops.autonomyPolicy.mode)}</span>
              <Badge tone={ops.autonomyPolicy.autoStartAllowed ? 'success' : 'neutral'}>{ops.autonomyPolicy.autoStartAllowed ? 'auto' : 'manual'}</Badge>
            </div>
            <div class="mt-3 text-xs text-muted-foreground">风险阈值: {display(ops.autonomyPolicy.maxRiskWithoutApproval)}</div>
            <div class="mt-3 flex flex-wrap gap-1.5">
              {#each ops.autonomyPolicy.requiredApprovals as approval}
                <Badge tone="neutral">{approval}</Badge>
              {:else}
                <span class="text-xs text-muted-foreground">无额外审批要求</span>
              {/each}
            </div>
          </div>
        </section>

        <section>
          <h2 class="mb-3 flex items-center gap-2 text-xs font-semibold uppercase text-muted-foreground"><FileCheck class="h-3.5 w-3.5" />Evidence gates</h2>
          <div class="space-y-2">
            {#each ops.evidenceGates as gate (gate.taskId)}
              {@render EvidenceGate(gate)}
            {:else}
              <p class="text-sm text-muted-foreground">暂无证据闸口。</p>
            {/each}
          </div>
        </section>

        <section>
          <h2 class="mb-3 flex items-center gap-2 text-xs font-semibold uppercase text-muted-foreground"><Clock class="h-3.5 w-3.5" />Approval gates</h2>
          <div class="space-y-2">
            {#each ops.approvalGates as gate (gate.approvalId)}
              {@render ApprovalGate(gate)}
            {:else}
              <p class="text-sm text-muted-foreground">暂无待审批事项。</p>
            {/each}
          </div>
        </section>

        <section>
          <h2 class="mb-3 flex items-center gap-2 text-xs font-semibold uppercase text-muted-foreground"><MessageSquare class="h-3.5 w-3.5" />讨论信号</h2>
          <div class="space-y-2">
            {#each latestEvents as event (event.id)}
              {@const Icon = eventIcon(event.kind)}
              <div class="rounded-xl border border-border bg-card p-3">
                <div class="flex items-center gap-2">
                  <span class="grid h-7 w-7 place-items-center rounded-full bg-muted text-muted-foreground">
                    <Icon class="h-3.5 w-3.5" />
                  </span>
                  <div class="min-w-0 flex-1">
                    <div class="flex min-w-0 items-center gap-2">
                      <span class="truncate text-xs font-semibold text-foreground">{display(event.authorName)}</span>
                      <Badge tone={actorTone(event.authorType)}>{eventLabel(event.kind)}</Badge>
                    </div>
                    <div class="mt-0.5 text-[10px] text-muted-foreground">{event.postedAt}</div>
                  </div>
                </div>
                <p class="mt-2 line-clamp-3 whitespace-pre-wrap text-xs leading-relaxed text-muted-foreground">{event.body}</p>
              </div>
            {:else}
              <p class="text-sm text-muted-foreground">暂无讨论信号。</p>
            {/each}
          </div>
        </section>
      </div>
    </aside>
  </div>
{/if}

{#snippet FocusCard(focus: OpsFocusViewModel)}
  {@const Icon = focusIcon(focus.kind)}
  <article class="rounded-xl border border-border bg-card p-4">
    <div class="flex items-start gap-3">
      <span class="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-brand/10 text-brand">
        <Icon class="h-4 w-4" />
      </span>
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-2">
          <h3 class="truncate text-sm font-semibold text-foreground">{display(focus.title)}</h3>
          <Badge tone={focus.tone}>{priorityLabel(focus.priority)}</Badge>
        </div>
        <p class="mt-1 line-clamp-2 text-xs leading-relaxed text-muted-foreground">{display(focus.summary)}</p>
        <div class="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
          <span>{display(focus.kind)}</span>
          <span>{taskTitle(focus.taskId)}</span>
          <span>{focus.createdAt}</span>
        </div>
      </div>
    </div>
  </article>
{/snippet}

{#snippet ActionCard(card: ActionCardViewModel)}
  {@const Icon = actionIcon(card.kind)}
  <article class="rounded-xl border border-border bg-card p-4">
    <div class="flex items-start gap-3">
      <span class="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-status-blocked-bg text-status-blocked">
        <Icon class="h-4 w-4" />
      </span>
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-2">
          <h3 class="truncate text-sm font-semibold text-foreground">{display(card.title)}</h3>
          <Badge tone={card.tone}>{priorityLabel(card.priority)}</Badge>
          <Badge tone="neutral">{display(card.status)}</Badge>
        </div>
        <p class="mt-1 line-clamp-2 text-xs leading-relaxed text-muted-foreground">{display(card.summary)}</p>
        <div class="mt-3 flex flex-wrap gap-1.5">
          {#each card.commands as command}
            <Badge tone="blue">{command.label}</Badge>
          {/each}
        </div>
      </div>
    </div>
  </article>
{/snippet}

{#snippet EvidenceGate(gate: EvidenceGateViewModel)}
  <div class="rounded-xl border border-border bg-card p-3">
    <div class="flex items-center justify-between gap-3">
      <span class="truncate text-xs font-semibold text-foreground">{taskTitle(gate.taskId)}</span>
      <Badge tone={gate.tone}>{display(gate.status)}</Badge>
    </div>
    <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-muted">
      <div class="h-full rounded-full bg-status-done" style={`width:${Math.min(100, Math.round((gate.submittedCount / Math.max(1, gate.requiredCount)) * 100))}%`}></div>
    </div>
    <div class="mt-2 text-[11px] text-muted-foreground">{gate.submittedCount}/{gate.requiredCount} submitted</div>
  </div>
{/snippet}

{#snippet ApprovalGate(gate: ApprovalGateViewModel)}
  <div class="rounded-xl border border-border bg-card p-3">
    <div class="flex items-center justify-between gap-3">
      <span class="truncate text-xs font-semibold text-foreground">{display(gate.title)}</span>
      <Badge tone={gate.tone}>{display(gate.status)}</Badge>
    </div>
    <p class="mt-1 line-clamp-2 text-xs text-muted-foreground">{display(gate.summary)}</p>
    <div class="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
      <span>{display(gate.approvalType)}</span>
      <span>{taskTitle(gate.taskId)}</span>
    </div>
  </div>
{/snippet}
