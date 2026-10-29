<script lang="ts">
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  import { ChevronLeft, ChevronRight, Network, Search } from 'lucide-svelte'
  import OntologyNeighborhoodGraph from '#lib/components/ontology/OntologyNeighborhoodGraph.svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'
  import { objectTypeLabel, ontologyValueLabel, propertyLabel } from '#lib/ontology-workbench/display-labels'
  import {
    useOntologyAgentRunLineageQuery,
    useOntologyAgentRunQuery,
    useOntologyAgentTaskQuery,
    useOntologyAgentTasksQuery
  } from '#lib/queries/ontology-executions'
  import type { AgentExecutionDetailDTO } from '#lib/api/ontology-executions'
  import { untrack } from 'svelte'

  let searchDraft = $state($page.url.searchParams.get('search') ?? '')
  let search = $state($page.url.searchParams.get('search') ?? '')
  let statusDraft = $state($page.url.searchParams.get('status') ?? '')
  let status = $state($page.url.searchParams.get('status') ?? '')
  let taskId = $state($page.url.searchParams.get('task_id') ?? '')
  let taskTenantId = $state($page.url.searchParams.get('source_tenant_id') ?? '')
  let runIdDraft = $state($page.url.searchParams.get('run_id') ?? '')
  let runId = $state($page.url.searchParams.get('run_id') ?? '')
  let selectedNodeId = $state('')
  let cursor = $state($page.url.searchParams.get('cursor') ?? '')
  let cursorHistory = $state<string[]>([])
  let boundTenant = $state('')
  const tenantId = () => ontologyScopeContext.mode === 'scope' ? ontologyScopeContext.activeScope?.tenant_id ?? '' : ''
  const scopeKey = () => `${ontologyScopeContext.mode}:${tenantId()}`
  const tasks = useOntologyAgentTasksQuery(() => ({ tenantId: tenantId(), status: status || undefined, search: search || undefined, limit: 50, cursor: cursor || undefined }))
  const task = useOntologyAgentTaskQuery(() => taskTenantId, () => taskId)
  const run = useOntologyAgentRunQuery(tenantId, () => runId)
  const lineage = useOntologyAgentRunLineageQuery(tenantId, () => runId)

  $effect(() => {
    const activeTenant = tenantId()
    const activeScopeKey = scopeKey()
    const url = $page.url
    untrack(() => syncRouteState(activeScopeKey, activeTenant, url))
  })

  function syncRouteState(activeScopeKey: string, activeTenant: string, url: URL) {
    const restoreRoute = activeTenant === (url.searchParams.get('tenant_id') ?? '').trim()
    const nextTaskId = restoreRoute ? (url.searchParams.get('task_id') ?? '') : ''
    const nextTaskTenantId = restoreRoute && nextTaskId ? (url.searchParams.get('source_tenant_id') ?? activeTenant) : ''
    const nextRunId = restoreRoute && activeTenant ? (url.searchParams.get('run_id') ?? '') : ''
    const nextSearch = restoreRoute ? (url.searchParams.get('search') ?? '') : ''
    const nextStatus = restoreRoute ? (url.searchParams.get('status') ?? '') : ''
    const nextCursor = restoreRoute ? (url.searchParams.get('cursor') ?? '') : ''
    const unchanged = boundTenant === activeScopeKey && taskId === nextTaskId && taskTenantId === nextTaskTenantId && runId === nextRunId && search === nextSearch && status === nextStatus && cursor === nextCursor
    boundTenant = activeScopeKey
    if (unchanged) return
    taskId = nextTaskId
    taskTenantId = nextTaskTenantId
    runIdDraft = nextRunId
    runId = nextRunId
    searchDraft = nextSearch
    search = nextSearch
    statusDraft = nextStatus
    status = nextStatus
    cursor = nextCursor
    cursorHistory = []
    selectedNodeId = nextRunId ? `agent-run:${nextRunId.replace(/^agent-run:/, '')}` : ''
  }

  function updateRoute() {
    const params = new URLSearchParams()
    if (tenantId()) params.set('tenant_id', tenantId())
    if (taskId) { params.set('task_id', taskId); params.set('source_tenant_id', taskTenantId) }
    if (runId) params.set('run_id', runId)
    if (search) params.set('search', search)
    if (status) params.set('status', status)
    if (cursor) params.set('cursor', cursor)
    void goto(`/resources/ontology/executions?${params.toString()}`, { invalidateAll: false })
  }

  function clearTask() { taskId = ''; taskTenantId = '' }
  function applyFilters() { search = searchDraft.trim(); status = statusDraft.trim(); cursor = ''; cursorHistory = []; clearTask(); updateRoute() }
  function selectTask(id: string, sourceTenantId: string) { taskId = id; taskTenantId = sourceTenantId; runId = ''; runIdDraft = ''; selectedNodeId = ''; updateRoute() }
  function openRun() { runId = runIdDraft.trim(); clearTask(); selectedNodeId = runId ? `agent-run:${runId.replace(/^agent-run:/, '')}` : ''; updateRoute() }
  function nextPage(nextCursor: string) { cursorHistory = [...cursorHistory, cursor]; cursor = nextCursor; clearTask(); updateRoute() }
  function previousPage() { cursor = cursorHistory.at(-1) ?? ''; cursorHistory = cursorHistory.slice(0, -1); clearTask(); updateRoute() }
  function displayValue(value: unknown) { return typeof value === 'string' ? value : JSON.stringify(value) ?? '—' }
</script>

<section class="space-y-4">
  <header><h2 class="text-xl font-semibold">智能体执行</h2><p class="mt-1 text-sm text-muted-foreground">查看 AgentTask、AgentRun、上下文、轨迹、证据和受控动作的关联结果。</p></header>
  <div class="grid gap-3 lg:grid-cols-[minmax(220px,1fr)_minmax(150px,0.55fr)_auto_minmax(240px,0.8fr)_auto] lg:items-end">
      <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">搜索任务</span><div class="relative"><Search class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" /><Input value={searchDraft} class="pl-9" ariaLabel="搜索智能体任务" oninput={(event) => (searchDraft = event.currentTarget.value)} /></div></label>
      <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">状态</span><Input value={statusDraft} ariaLabel="智能体任务状态" oninput={(event) => (statusDraft = event.currentTarget.value)} /></label>
      <Button onclick={applyFilters}>应用筛选</Button>
      <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">运行 ID</span><Input value={runIdDraft} ariaLabel="智能体运行 ID" oninput={(event) => (runIdDraft = event.currentTarget.value)} /></label>
      <Button variant="outline" disabled={!tenantId() || !runIdDraft.trim()} onclick={openRun}><Network class="size-4" aria-hidden="true" />查看链路</Button>
    </div>

    <div class="grid gap-5 xl:grid-cols-[minmax(300px,0.72fr)_minmax(0,1.28fr)]">
      <section class="min-w-0 border-r border-border pr-4" aria-label="智能体任务目录">
        {#if tasks.isPending}<LoadingState />{:else if tasks.error}<ErrorState message={tasks.error.message} />{:else if !tasks.data?.items.length}<EmptyState title="当前 Scope 没有智能体任务" />{:else}
          <div class="divide-y divide-border border-y border-border">
            {#each tasks.data.items as item (`${item.tenant_id ?? tenantId()}:${item.id}`)}
              <button class="block w-full px-3 py-3 text-left hover:bg-muted" class:bg-muted={taskId === item.id && taskTenantId === (item.tenant_id ?? tenantId())} aria-pressed={taskId === item.id && taskTenantId === (item.tenant_id ?? tenantId())} onclick={() => selectTask(item.id, item.tenant_id ?? tenantId())}>
                <span class="block truncate text-sm font-medium">{item.label}</span>
                <span class="mt-1 block truncate font-mono text-[11px] text-muted-foreground">{item.id}</span>
                <span class="mt-1 block truncate font-mono text-[10px] text-muted-foreground">{item.tenant_id ?? tenantId()}</span>
                <span class="mt-1 flex items-center gap-2 text-xs text-muted-foreground"><Badge tone={item.status === 'failed' ? 'danger' : item.status === 'completed' ? 'success' : 'info'}>{item.status ? ontologyValueLabel(item.status) : '未知'}</Badge>{item.updated_at ?? '—'}</span>
              </button>
            {/each}
          </div>
          <div class="mt-3 flex gap-2">{#if cursorHistory.length}<Button size="sm" variant="outline" onclick={previousPage}><ChevronLeft class="size-4" aria-hidden="true" />上一页</Button>{/if}{#if tasks.data.page.has_more && tasks.data.page.next_cursor}<Button size="sm" onclick={() => nextPage(tasks.data?.page.next_cursor ?? '')}>下一页<ChevronRight class="size-4" aria-hidden="true" /></Button>{/if}</div>
        {/if}
      </section>

      <section class="min-w-0" aria-label="智能体执行详情">
        {#if runId}
          {#if run.isPending}<LoadingState />{:else if run.error}<ErrorState message={run.error.message} />{:else if run.data}
            {@const detail = run.data}
            <header class="border-b border-border pb-3"><p class="text-xs text-muted-foreground">{objectTypeLabel(detail.object_type)}</p><h3 class="mt-1 break-words text-lg font-semibold">{detail.label}</h3><p class="mt-1 break-all font-mono text-xs text-muted-foreground">{detail.id}</p></header>
            {@render detailRows(detail)}
            <div class="mt-5 border-t border-border pt-4"><h4 class="mb-3 font-medium">两跳执行链路</h4>{#if lineage.isPending}<LoadingState />{:else if lineage.error}<ErrorState message={lineage.error.message} />{:else if lineage.data}<OntologyNeighborhoodGraph graph={lineage.data} selectedId={selectedNodeId} onselect={(id) => (selectedNodeId = id)} />{/if}</div>
          {/if}
        {:else if taskId}
          {#if task.isPending}<LoadingState />{:else if task.error}<ErrorState message={task.error.message} />{:else if task.data}<header class="border-b border-border pb-3"><p class="text-xs text-muted-foreground">{objectTypeLabel(task.data.object_type)}</p><h3 class="mt-1 break-words text-lg font-semibold">{task.data.label}</h3><p class="mt-1 break-all font-mono text-xs text-muted-foreground">{task.data.id}</p></header>{@render detailRows(task.data)}{/if}
        {:else}<EmptyState title="选择任务或输入运行 ID" />{/if}
      </section>
    </div>
</section>

{#snippet detailRows(detail: AgentExecutionDetailDTO)}
  <dl class="mt-3 divide-y divide-border border-y border-border text-sm">
    <div class="grid gap-1 py-2 sm:grid-cols-[130px_minmax(0,1fr)]"><dt class="text-muted-foreground">状态</dt><dd>{detail.status ? ontologyValueLabel(detail.status) : '—'}</dd></div>
    <div class="grid gap-1 py-2 sm:grid-cols-[130px_minmax(0,1fr)]"><dt class="text-muted-foreground">更新时间</dt><dd>{detail.updated_at ?? '—'}</dd></div>
    {#each Object.entries(detail.properties ?? {}) as property (property[0])}<div class="grid gap-1 py-2 sm:grid-cols-[130px_minmax(0,1fr)]"><dt class="text-muted-foreground">{propertyLabel(property[0])}</dt><dd class="break-words">{displayValue(property[1])}</dd></div>{/each}
  </dl>
{/snippet}
