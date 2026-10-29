<script lang="ts">
  import {
    Activity,
    AlertTriangle,
    BookOpen,
    CheckCircle2,
    ClipboardList,
    Gauge,
    Link2,
    ListChecks,
    Search,
    ShieldCheck,
    Sparkles
  } from 'lucide-svelte'
  import { useEvaluationListQuery } from '#lib/queries/operations'
  import { useWorkforceMembersQuery } from '#lib/queries/workforce'
  import { memberType } from '#lib/components/operations/workforceMemberForm'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { EvaluationItemViewModel, WorkforceMemberViewModel } from '#lib/types/view-models'
  import AgentMemorySheet from './AgentMemorySheet.svelte'

  type Tone = 'blue' | 'teal' | 'violet' | 'orange' | 'slate'
  type AgentStatus = '需优化' | '运行稳定' | '托管运行' | '有任务记录' | '等待数据' | '已停用'

  interface AgentProfile {
    id: string
    name: string
    role: string
    category: string
    department: string
    runMode: string
    taskCount: number
    completedTasks: number
    score: number
    quality: number
    stability: number
    tone: Tone
    status: AgentStatus[]
    summary: string
    skillTags: Array<{ label: string; tone: Tone }>
    evaluation?: EvaluationItemViewModel
  }

  interface Capability {
    name: string
    score: number
  }

  let searchKeyword = $state('')
  let selectedAgentId = $state('')
  let memorySheetOpen = $state(false)
  const t = $derived(createTranslator($locale))

  const workforceQuery = useWorkforceMembersQuery(() => ({
    enabledOnly: undefined,
    includeDetails: true,
    limit: 50
  }))
  const evaluationQuery = useEvaluationListQuery(() => ({ entityType: 'agent' }))
  const evaluations = $derived(evaluationQuery.data?.items ?? [])
  const evaluationByEntityId = $derived(new Map(evaluations.map((item) => [item.entityId, item])))
  const workforceAgents = $derived(
    (workforceQuery.data?.members ?? [])
      .filter((member) => memberType(member) === 'agent')
      .map((member, index) => {
        const evaluation = evaluationForMember(member, evaluationByEntityId)
        return agentFromWorkforceMember(member, index, evaluation)
      })
  )
  const filteredAgents = $derived(workforceAgents.filter((agent) => agentMatchesSearch(agent, searchKeyword)))
  const selectedAgent = $derived(
    filteredAgents.find((agent) => agent.id === selectedAgentId) ?? filteredAgents[0] ?? emptyAgent()
  )
  const selectedAgentHasId = $derived(selectedAgent.id.trim().length > 0)
  const capabilities = $derived(capabilitiesForAgent(selectedAgent))
  const taskHistory = $derived(selectedAgent.evaluation?.taskHistory ?? [])
  const investment = $derived(selectedAgent.evaluation?.investment)
  const relatedServices = $derived(
    Array.from(
      new Set([
        selectedAgent.category,
        selectedAgent.runMode,
        selectedAgent.department,
        ...selectedAgent.skillTags.map((tag) => tag.label)
      ].filter((value) => value && !value.startsWith('未')))
    ).slice(0, 8)
  )
  const radarPoints = $derived(buildRadarPoints(capabilities, 130, 96, 62))

  function agentFromWorkforceMember(
    member: WorkforceMemberViewModel,
    index: number,
    evaluation?: EvaluationItemViewModel
  ): AgentProfile {
    const name = member.displayName || member.realName || member.profileId
    const category = member.primaryRoleLabel || evaluation?.roleName || member.positionDisplayName || member.roleName || '智能体'
    const tone = (['blue', 'teal', 'violet', 'orange'] as Tone[])[index % 4]
    return {
      id: member.profileId,
      name,
      role: `${category} · ${member.departmentDisplayName || evaluation?.department || member.departmentId || '未归属部门'}`,
      category,
      department: member.departmentDisplayName || evaluation?.department || member.departmentId || '未归属部门',
      runMode: runtimeLabel(evaluation?.runtimeKind || member.runtimeKind),
      taskCount: evaluation?.assignedTasks ?? 0,
      completedTasks: evaluation?.completedTasks ?? 0,
      score: scoreFromEvaluation(evaluation),
      quality: evaluation?.metrics.quality ?? 0,
      stability: evaluation?.metrics.stability ?? 0,
      tone,
      status: statusesForAgent(member, evaluation),
      summary: evaluation?.summary || evaluationSummaryFromMember(member),
      skillTags: tagLabelsForAgent(member, evaluation).map((label, tagIndex) => ({
        label,
        tone: (['blue', 'violet', 'teal', 'orange'] as Tone[])[tagIndex % 4]
      })),
      evaluation
    }
  }

  function evaluationSummaryFromMember(member: WorkforceMemberViewModel) {
    const department = member.departmentDisplayName || member.departmentId || '当前部门'
    return `来自人员目录的智能体，当前归属 ${department}。暂无稳定评估投影时，先展示人员目录基础信息并等待任务运行数据。`
  }

  function evaluationForMember(
    member: WorkforceMemberViewModel,
    byEntityId: Map<string, EvaluationItemViewModel>
  ) {
    return byEntityId.get(member.profileId) ?? byEntityId.get(member.id) ?? byEntityId.get(member.agentInstanceId)
  }

  function agentMatchesSearch(agent: AgentProfile, keyword: string) {
    const query = keyword.trim().toLowerCase()
    if (!query) return true
    return [agent.name, agent.category, agent.department, agent.role].some((value) =>
      value.toLowerCase().includes(query)
    )
  }

  function emptyAgent(): AgentProfile {
    return {
      id: '',
      name: '暂无人员目录 Agent',
      role: '等待人员目录数据',
      category: '暂无数据',
      department: '未归属部门',
      runMode: '未配置运行方式',
      taskCount: 0,
      completedTasks: 0,
      score: 0,
      quality: 0,
      stability: 0,
      tone: 'slate',
      status: ['等待数据'],
      summary: '暂无人员目录 Agent 可评估。',
      skillTags: [{ label: '待评估', tone: 'slate' }]
    }
  }

  function scoreFromEvaluation(evaluation: EvaluationItemViewModel | undefined) {
    if (!evaluation) return 0
    const metrics = evaluation.metrics
    return Math.round(averageNumber([metrics.delivery, metrics.quality, metrics.efficiency, metrics.stability]))
  }

  function statusesForAgent(
    member: WorkforceMemberViewModel,
    evaluation: EvaluationItemViewModel | undefined
  ): AgentStatus[] {
    const statuses: AgentStatus[] = []
    if ((evaluation?.blockerCount ?? 0) > 0 || (evaluation?.reworkCount ?? 0) > 0) statuses.push('需优化')
    if ((evaluation?.metrics.stability ?? 0) >= 75) statuses.push('运行稳定')
    if (evaluation?.runtimeKind || member.runtimeKind) statuses.push('托管运行')
    if ((evaluation?.assignedTasks ?? 0) > 0) statuses.push('有任务记录')
    if (!evaluation) statuses.push('等待数据')
    if (!member.enabled) statuses.push('已停用')
    return statuses.length > 0 ? statuses.slice(0, 3) : ['等待数据']
  }

  function tagLabelsForAgent(
    member: WorkforceMemberViewModel,
    evaluation: EvaluationItemViewModel | undefined
  ) {
    const labels = [
      ...member.capabilityTags,
      ...member.roleLabels,
      ...(evaluation?.capabilities.map((item) => item.name) ?? [])
    ]
      .map((item) => item.trim())
      .filter(Boolean)
    return Array.from(new Set(labels)).slice(0, 8)
  }

  function capabilitiesForAgent(agent: AgentProfile): Capability[] {
    const source = agent.evaluation?.capabilities.length
      ? agent.evaluation.capabilities
      : [
          { name: '任务完成', score: agent.taskCount > 0 ? Math.round((agent.completedTasks / agent.taskCount) * 100) : 0 },
          { name: '质量稳定', score: agent.quality },
          { name: '协作响应', score: agent.evaluation?.metrics.collaboration ?? 0 },
          { name: '运行效率', score: agent.evaluation?.metrics.efficiency ?? 0 },
          { name: '稳定性', score: agent.stability }
        ]
    return source.slice(0, 6).map((item) => ({
      name: item.name,
      score: Math.max(0, Math.min(100, Math.round(item.score)))
    }))
  }

  function buildRadarPoints(items: Capability[], centerX: number, centerY: number, radius: number) {
    if (items.length === 0) return ''
    return items
      .map((item, index) => {
        const angle = -Math.PI / 2 + (index * Math.PI * 2) / items.length
        const valueRadius = (item.score / 100) * radius
        return `${centerX + Math.cos(angle) * valueRadius},${centerY + Math.sin(angle) * valueRadius}`
      })
      .join(' ')
  }

  function polygonPoints(count: number, centerX: number, centerY: number, radius: number) {
    if (count === 0) return ''
    return Array.from({ length: count }, (_, index) => {
      const angle = -Math.PI / 2 + (index * Math.PI * 2) / count
      return `${centerX + Math.cos(angle) * radius},${centerY + Math.sin(angle) * radius}`
    }).join(' ')
  }

  function statusClasses(status: AgentStatus) {
    switch (status) {
      case '需优化':
        return 'bg-rose-50 text-rose-600'
      case '运行稳定':
        return 'bg-emerald-50 text-emerald-700'
      case '托管运行':
        return 'bg-blue-50 text-blue-700'
      case '已停用':
        return 'bg-slate-100 text-slate-500'
      case '等待数据':
        return 'bg-orange-50 text-orange-600'
      default:
        return 'bg-violet-50 text-violet-700'
    }
  }

  function resultIsSuccessful(result: string) {
    return result === '通过' || result === '完成' || result === '已完成'
  }

  function runtimeLabel(kind: string | undefined) {
    if (!kind || kind === 'Not provided') return '未配置运行方式'
    if (kind === 'codex') return 'Codex 运行'
    if (kind === 'openclaw') return 'OpenClaw 运行'
    return kind
  }

  function averageNumber(values: number[]) {
    if (values.length === 0) return 0
    return values.reduce((sum, value) => sum + value, 0) / values.length
  }

  function formatInteger(value: number | undefined) {
    if (value === undefined || !Number.isFinite(value)) return '--'
    return Math.max(0, Math.trunc(value)).toLocaleString($locale)
  }

  function formatHumanDays(value: number | undefined) {
    if (value === undefined || !Number.isFinite(value)) return '--'
    return Math.max(0, value).toLocaleString($locale, { maximumFractionDigits: 1 })
  }
</script>

<svelte:head>
  <title>评估中心 | Orrery</title>
</svelte:head>

<div class="page-shell min-h-[calc(100vh-7.5rem)] overflow-hidden rounded-xl border border-border bg-card text-foreground">
  <div class="grid min-h-[calc(100vh-7.5rem)] lg:grid-cols-[240px_minmax(0,1fr)]">
    <aside class="border-b border-border bg-card lg:border-b-0 lg:border-r">
      <div class="p-3 lg:sticky lg:top-0 lg:max-h-[calc(100vh-7.5rem)] lg:overflow-y-auto">
        <h1 class="px-1 pb-3 pt-1 text-[15px] font-semibold text-foreground">评估中心</h1>

        <label class="flex h-8 items-center gap-2 rounded-lg border border-border bg-muted/45 px-2.5 text-muted-foreground">
          <Search class="h-3.5 w-3.5 shrink-0" />
          <input
            bind:value={searchKeyword}
            aria-label={t('operations.evaluation.searchAgent')}
            placeholder={t('operations.evaluation.searchAgent')}
            class="min-w-0 flex-1 bg-transparent text-[13px] text-foreground outline-none placeholder:text-muted-foreground"
          />
        </label>

        <div class="mt-2 max-h-60 space-y-0.5 overflow-y-auto pr-1 lg:max-h-none lg:overflow-visible lg:pr-0">
          {#if workforceQuery.isLoading}
            <div class="rounded-lg bg-muted/40 p-3 text-xs text-muted-foreground">正在加载人员目录 Agent</div>
          {:else if workforceQuery.isError}
            <div class="rounded-lg bg-muted/40 p-3 text-xs text-muted-foreground">人员目录加载失败，暂无法显示 Agent</div>
          {:else if filteredAgents.length === 0}
            <div class="rounded-lg bg-muted/40 p-3 text-xs text-muted-foreground">未找到匹配的人员目录 Agent</div>
          {/if}

          {#each filteredAgents as agent}
            <button
              type="button"
              onclick={() => (selectedAgentId = agent.id)}
              class="flex w-full items-center gap-2.5 rounded-lg px-2 py-2 text-left transition-colors {agent.id === selectedAgent.id ? 'selection-wash' : 'hover:bg-muted/60'}"
              aria-pressed={agent.id === selectedAgent.id}
            >
              <WorkforceAvatar
                profileId={agent.id}
                name={agent.name}
                human={false}
                class="h-8 w-8 rounded-full"
                textClass="text-xs"
              />
              <span class="min-w-0 flex-1">
                <span class="block truncate text-[13px] font-medium text-foreground">{agent.name}</span>
                <span class="block truncate text-[11px] text-muted-foreground">{agent.category}</span>
              </span>
              <span class="text-xs font-medium tabular-nums text-muted-foreground">{agent.score}</span>
            </button>
          {/each}
        </div>
      </div>
    </aside>

    <main class="min-w-0 bg-[#f4f5f7] p-4 sm:p-5 2xl:p-6">
      <div class="mx-auto max-w-[760px] space-y-4">
        <section class="rounded-xl border border-border bg-card p-4 sm:p-5">
          <div class="flex flex-col items-start justify-between gap-4 sm:flex-row">
            <div class="flex min-w-0 items-center gap-3">
              <WorkforceAvatar
                profileId={selectedAgent.id}
                name={selectedAgent.name}
                human={false}
                class="h-14 w-14 rounded-xl"
                textClass="text-xl"
              />
              <div class="min-w-0">
                <h2 class="truncate text-lg font-semibold text-foreground">{selectedAgent.name}</h2>
                <p class="mt-0.5 truncate text-xs text-muted-foreground">{selectedAgent.role}</p>
                <div class="mt-2 flex flex-wrap gap-1.5">
                  {#each selectedAgent.status as status}
                    <span class="rounded-md px-2 py-0.5 text-[11px] {statusClasses(status)}">{status}</span>
                  {/each}
                </div>
              </div>
            </div>

            <button
              type="button"
              disabled={!selectedAgentHasId}
              onclick={() => (memorySheetOpen = true)}
              class="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-lg border border-brand/20 bg-brand/10 px-3 text-xs font-medium text-brand transition-colors hover:bg-brand/15 disabled:pointer-events-none disabled:opacity-40"
            >
              <BookOpen class="h-3.5 w-3.5" />
              查看记忆
            </button>
          </div>
        </section>

        <section class="grid grid-cols-2 gap-3 sm:grid-cols-4" aria-label="Agent 核心指标">
          {@render metricCard(Gauge, '综合评分', selectedAgent.score)}
          {@render metricCard(ListChecks, '任务量', selectedAgent.taskCount)}
          {@render metricCard(ShieldCheck, '质量分', selectedAgent.quality)}
          {@render metricCard(Activity, '稳定性', selectedAgent.stability)}
        </section>

        <section class="rounded-xl border border-brand/15 bg-brand/[0.035] p-4 sm:p-5">
          <h3 class="flex items-center gap-2 text-[13px] font-medium text-foreground">
            <Sparkles class="h-4 w-4 text-brand" />
            综合评估
          </h3>
          <p class="mt-2 line-clamp-3 break-words text-[13px] leading-6 text-muted-foreground">{selectedAgent.summary}</p>
        </section>

        <section class="rounded-xl border border-border bg-card p-4 sm:p-5">
          <h3 class="text-[13px] font-medium text-foreground">投入与产出</h3>
          <div class="mt-3 grid grid-cols-3 gap-3">
            <div class="min-w-0 rounded-md bg-muted/45 p-3">
              <div class="text-[11px] text-muted-foreground">运行Token</div>
              <div class="mt-1 truncate text-base font-semibold tabular-nums text-foreground">{formatInteger(investment?.tokenConsumed)}</div>
            </div>
            <div class="min-w-0 rounded-md bg-muted/45 p-3">
              <div class="text-[11px] text-muted-foreground">估算人天</div>
              <div class="mt-1 truncate text-base font-semibold tabular-nums text-foreground">{formatHumanDays(investment?.estimatedHumanDays)}</div>
            </div>
            <div class="min-w-0 rounded-md bg-muted/45 p-3">
              <div class="text-[11px] text-muted-foreground">评价Token</div>
              <div class="mt-1 truncate text-base font-semibold tabular-nums text-foreground">{formatInteger(investment?.evaluationTokenConsumed)}</div>
            </div>
          </div>
          <p class="mt-3 line-clamp-2 break-words text-xs leading-5 text-muted-foreground">
            {investment?.summary ?? '暂无投入与产出评估记录。'}
          </p>
        </section>

        <section class="grid gap-4 md:grid-cols-2">
          <article class="rounded-xl border border-border bg-card p-4 sm:p-5">
            <h3 class="text-[13px] font-medium text-foreground">核心能力</h3>
            <div class="mt-4 space-y-3">
              {#each capabilities as capability}
                <div>
                  <div class="mb-1 flex items-center justify-between text-xs">
                    <span class="text-muted-foreground">{capability.name}</span>
                    <span class="tabular-nums text-muted-foreground">{capability.score}</span>
                  </div>
                  <div class="h-1.5 overflow-hidden rounded-full bg-muted">
                    <div class="h-full rounded-full bg-brand" style={`width:${capability.score}%`}></div>
                  </div>
                </div>
              {/each}
            </div>
          </article>

          <article class="rounded-xl border border-border bg-card p-4 sm:p-5">
            <h3 class="text-[13px] font-medium text-foreground">能力雷达</h3>
            <div class="mt-1 flex h-[200px] justify-center overflow-hidden">
              <svg viewBox="0 0 260 200" class="h-full w-full max-w-[300px]" role="img" aria-label="能力雷达图">
                {#each [62, 42, 22] as radius}
                  <polygon points={polygonPoints(capabilities.length, 130, 96, radius)} fill="none" stroke="var(--border)" stroke-width="1" />
                {/each}
                {#each capabilities as capability, index}
                  {@const angle = -Math.PI / 2 + (index * Math.PI * 2) / capabilities.length}
                  {@const labelX = 130 + Math.cos(angle) * 82}
                  {@const labelY = 99 + Math.sin(angle) * 80}
                  <line x1="130" y1="96" x2={130 + Math.cos(angle) * 62} y2={96 + Math.sin(angle) * 62} stroke="var(--border)" stroke-width="1" />
                  <text x={labelX} y={labelY} text-anchor={labelX > 138 ? 'start' : labelX < 122 ? 'end' : 'middle'} class="fill-muted-foreground text-[10px]">
                    {capability.name}
                  </text>
                {/each}
                <polygon points={radarPoints} fill="var(--brand)" fill-opacity=".1" stroke="var(--brand)" stroke-width="1.5" />
              </svg>
            </div>
          </article>
        </section>

        <section class="rounded-xl border border-border bg-card p-4 sm:p-5">
          <h3 class="flex items-center gap-2 text-[13px] font-medium text-foreground">
            <Link2 class="h-4 w-4 text-muted-foreground" />
            关联服务
          </h3>
          <div class="mt-3 flex flex-wrap gap-2">
            {#if relatedServices.length > 0}
              {#each relatedServices as service}
                <span class="rounded-lg border border-border bg-muted/35 px-3 py-1.5 text-xs text-foreground">{service}</span>
              {/each}
            {:else}
              <span class="text-xs text-muted-foreground">暂无关联服务</span>
            {/if}
          </div>
        </section>

        <section class="rounded-xl border border-border bg-card p-4 sm:p-5">
          <h3 class="flex items-center gap-2 text-[13px] font-medium text-foreground">
            <ClipboardList class="h-4 w-4 text-muted-foreground" />
            近期任务记录
          </h3>
          <div class="mt-3 divide-y divide-border/60">
            {#if taskHistory.length === 0}
              <div class="py-8 text-center text-xs text-muted-foreground">暂无任务运行记录</div>
            {:else}
              {#each taskHistory.slice(0, 5) as task}
                <div class="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between">
                  <div class="flex min-w-0 items-start gap-2.5">
                    {#if resultIsSuccessful(task.result)}
                      <CheckCircle2 class="mt-0.5 h-4 w-4 shrink-0 text-emerald-500" />
                    {:else}
                      <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
                    {/if}
                    <div class="min-w-0">
                      <div class="line-clamp-2 break-words text-[13px] text-foreground">
                        <span class="text-muted-foreground">总体目标：</span>{task.goal}
                      </div>
                      <div class="mt-0.5 line-clamp-2 break-words text-[11px] text-muted-foreground">
                        当前Agent负责工作：{task.responsibility}
                      </div>
                    </div>
                  </div>
                  <div class="flex shrink-0 items-center gap-3 pl-6 text-[11px] text-muted-foreground sm:pl-0">
                    <span>{task.result}</span>
                    <span>{task.time === 'Not provided' ? '未记录' : task.time}</span>
                  </div>
                </div>
              {/each}
            {/if}
          </div>
        </section>
      </div>
    </main>
  </div>
</div>

<AgentMemorySheet
  open={memorySheetOpen}
  agentId={selectedAgent.id}
  agentName={selectedAgent.name}
  onclose={() => (memorySheetOpen = false)}
/>

{#snippet metricCard(Icon: typeof Gauge, label: string, value: number)}
  <article class="rounded-xl border border-border bg-card p-3 sm:p-4">
    <span class="mb-2 grid h-8 w-8 place-items-center rounded-lg bg-brand/10 text-brand">
      <Icon class="h-[18px] w-[18px]" />
    </span>
    <div class="text-2xl font-semibold leading-none tabular-nums text-foreground">{value}</div>
    <div class="mt-1.5 text-xs text-muted-foreground">{label}</div>
  </article>
{/snippet}
