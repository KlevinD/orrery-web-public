<script lang="ts">
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  import { ChevronLeft, ChevronRight, Search } from 'lucide-svelte'
  import OntologyGovernanceWorkflow from '#lib/components/ontology/OntologyGovernanceWorkflow.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { useOntologyGovernanceProposalsQuery, useOntologyGovernanceSummaryQuery } from '#lib/queries/ontology-governance'
  import { useOntologySchemaGraphQuery, useOntologySchemaSummaryQuery } from '#lib/queries/ontology-schema'
  import { session } from '#lib/stores/session.svelte'
  import type { components } from '#lib/types/api.generated'
  import { untrack } from 'svelte'

  type CountMetric = components['schemas']['OntologyCountMetricDTO']

  let queue = $state<'actionable' | 'all'>($page.url.searchParams.get('queue') === 'all' ? 'all' : 'actionable')
  let status = $state($page.url.searchParams.get('status') ?? '')
  let searchDraft = $state($page.url.searchParams.get('search') ?? '')
  let search = $state($page.url.searchParams.get('search') ?? '')
  let selectedId = $state($page.url.searchParams.get('proposal_id') ?? '')
  let selectedTenantId = $state($page.url.searchParams.get('source_tenant_id') ?? '')
  let cursor = $state($page.url.searchParams.get('cursor') ?? '')
  let cursorHistory = $state<string[]>([])
  let boundTenant = $state('')
  const tenantId = () => ontologyScopeContext.mode === 'scope' ? ontologyScopeContext.activeScope?.tenant_id ?? '' : ''
  const scopeKey = () => `${ontologyScopeContext.mode}:${tenantId()}`
  const canManage = $derived(ontologyScopeContext.mode === 'scope' && Boolean(ontologyScopeContext.activeScope?.capabilities.includes('ontology.manage')) && hasPermissionScope(session.permissionScope, 'operations.manage'))
  const governanceSummary = useOntologyGovernanceSummaryQuery(tenantId)
  const proposals = useOntologyGovernanceProposalsQuery(() => ({ tenantId: tenantId(), queue, status: status || undefined, search: search || undefined, limit: 30, cursor: cursor || undefined }))
  const schemaSummary = useOntologySchemaSummaryQuery(tenantId)
  const schemaGraph = useOntologySchemaGraphQuery(tenantId, () => ({ view: 'semantic', maxNodes: 24 }))
  const schemaMetrics = $derived<Array<[string, CountMetric]>>(schemaSummary.data ? [
    ['对象类型', schemaSummary.data.data.object_types],
    ['关系类型', schemaSummary.data.data.link_types],
    ['属性类型', schemaSummary.data.data.property_types],
    ['映射规则', schemaSummary.data.data.mapping_rules],
    ['动作类型', schemaSummary.data.data.action_types]
  ] : [])

  $effect(() => {
    const activeTenant = tenantId()
    const activeScopeKey = scopeKey()
    const url = $page.url
    untrack(() => syncRouteState(activeScopeKey, activeTenant, url))
  })

  function syncRouteState(activeScopeKey: string, activeTenant: string, url: URL) {
    const restoreRoute = activeTenant === (url.searchParams.get('tenant_id') ?? '').trim()
    const nextSelectedId = restoreRoute ? (url.searchParams.get('proposal_id') ?? '') : ''
    const nextSelectedTenantId = restoreRoute && nextSelectedId ? (url.searchParams.get('source_tenant_id') ?? activeTenant) : ''
    const nextCursor = restoreRoute ? (url.searchParams.get('cursor') ?? '') : ''
    const nextQueue = restoreRoute && url.searchParams.get('queue') === 'all' ? 'all' : 'actionable'
    const nextStatus = restoreRoute ? (url.searchParams.get('status') ?? '') : ''
    const nextSearch = restoreRoute ? (url.searchParams.get('search') ?? '') : ''
    const unchanged = boundTenant === activeScopeKey && selectedId === nextSelectedId && selectedTenantId === nextSelectedTenantId && cursor === nextCursor && queue === nextQueue && status === nextStatus && search === nextSearch
    boundTenant = activeScopeKey
    if (unchanged) return
    selectedId = nextSelectedId
    selectedTenantId = nextSelectedTenantId
    cursor = nextCursor
    cursorHistory = []
    queue = nextQueue
    status = nextStatus
    searchDraft = nextSearch
    search = nextSearch
  }

  function selectProposal(id: string, sourceTenantId: string) {
    selectedId = id
    selectedTenantId = sourceTenantId
    updateRoute()
  }

  function updateRoute() { const params = new URLSearchParams({ queue }); if (tenantId()) params.set('tenant_id', tenantId()); if (selectedId) { params.set('proposal_id', selectedId); params.set('source_tenant_id', selectedTenantId) } if (status) params.set('status', status); if (search) params.set('search', search); if (cursor) params.set('cursor', cursor); void goto(`/resources/ontology/governance?${params.toString()}`, { invalidateAll: false }) }

  function clearSelection() { selectedId = ''; selectedTenantId = '' }

  function applyFilters() {
    search = searchDraft.trim()
    cursor = ''
    cursorHistory = []
    clearSelection()
    updateRoute()
  }

  function nextPage(nextCursor: string) {
    cursorHistory = [...cursorHistory, cursor]
    cursor = nextCursor
    clearSelection()
    updateRoute()
  }

  function previousPage() {
    cursor = cursorHistory.at(-1) ?? ''
    cursorHistory = cursorHistory.slice(0, -1)
    clearSelection()
    updateRoute()
  }

  function metricValue(metric: CountMetric) {
    return metric.status === 'exact' ? metric.value ?? 0 : '—'
  }

  function kindLabel(kind: components['schemas']['OntologySchemaGraphNodeDTO']['kind']) {
    return ({ object: '对象', link: '关系', property: '属性', mapping: '映射', action: '动作', event: '事件' } as const)[kind]
  }

  function formatDate(value: string) {
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? value : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(parsed)
  }
</script>

<section class="space-y-4">
  <header><h2 class="text-xl font-semibold">治理</h2><p class="mt-1 text-sm text-muted-foreground">查看已生效的本体基线，并审核进入队列的定义变更。</p></header>

  <section class="space-y-4 border-y border-border py-4" aria-labelledby="governance-baseline-title">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div><h3 id="governance-baseline-title" class="text-sm font-semibold">治理基线</h3><p class="mt-1 text-xs text-muted-foreground">当前范围内已纳管并生效的定义资产。</p></div>
      {#if schemaSummary.data?.data.latest_updated_at}<p class="text-xs text-muted-foreground">最近变更 {formatDate(schemaSummary.data.data.latest_updated_at)}</p>{/if}
    </div>
    {#if schemaSummary.isPending}<LoadingState />{:else if schemaSummary.error}<ErrorState message={schemaSummary.error.message} />{:else if schemaSummary.data}
      <dl class="grid gap-4 sm:grid-cols-2 lg:grid-cols-5">
        {#each schemaMetrics as metric}<div><dt class="text-xs text-muted-foreground">{metric[0]}</dt><dd class="mt-1 text-2xl font-semibold">{metricValue(metric[1])}</dd></div>{/each}
      </dl>
    {/if}

    <div>
      <h4 class="text-sm font-medium">已生效定义</h4>
      {#if schemaGraph.isPending}<div class="mt-3"><LoadingState /></div>{:else if schemaGraph.error}<div class="mt-3"><ErrorState message={schemaGraph.error.message} /></div>{:else if schemaGraph.data?.nodes.length}
        <div class="mt-3 overflow-x-auto border-y border-border">
          <table class="w-full min-w-[620px] table-fixed text-left text-sm">
            <thead class="text-[11px] font-medium uppercase text-muted-foreground"><tr><th class="w-[38%] px-3 py-2">定义</th><th class="w-[16%] px-3 py-2">类别</th><th class="w-[18%] px-3 py-2">来源</th><th class="w-[28%] px-3 py-2">范围</th></tr></thead>
            <tbody>{#each schemaGraph.data.nodes.slice(0, 10) as item (item.id)}<tr class="border-t border-border"><td class="px-3 py-3"><span class="block truncate font-medium" title={item.label}>{item.label}</span><span class="block truncate font-mono text-[10px] text-muted-foreground">{item.definition_id ?? item.id}</span></td><td class="px-3 py-3">{kindLabel(item.kind)}</td><td class="px-3 py-3">{item.origin === 'system' ? '内置基线' : '企业定义'}</td><td class="truncate px-3 py-3 font-mono text-[10px] text-muted-foreground">{item.tenant_id ?? tenantId()}</td></tr>{/each}</tbody>
          </table>
        </div>
        {#if schemaGraph.data.truncated}<p class="mt-2 text-xs text-muted-foreground">当前列表截断为前 10 条已生效定义。</p>{/if}
      {:else}<p class="mt-3 text-sm text-muted-foreground">当前范围尚无已生效定义。</p>{/if}
    </div>
  </section>

  <div class="grid gap-4 lg:grid-cols-[220px_minmax(0,1fr)]">
      <aside class="border-r border-border pr-4">
        <p class="text-xs text-muted-foreground">可处理提案</p>
        {#if governanceSummary.isPending}<div class="mt-3"><LoadingState /></div>{:else if governanceSummary.error}<div class="mt-3"><ErrorState message={governanceSummary.error.message} /></div>{:else}<p class="mt-2 text-3xl font-semibold">{governanceSummary.data?.data.actionable_proposals.status === 'exact' ? governanceSummary.data.data.actionable_proposals.value : '—'}</p><p class="mt-2 text-xs text-muted-foreground">统计于 {governanceSummary.data?.meta.as_of ?? '—'}</p>{/if}
      </aside>
      <div class="grid gap-3 sm:grid-cols-[160px_160px_minmax(0,1fr)_auto] sm:items-end">
        <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">队列</span><Select value={queue} options={[{ label: '待处理', value: 'actionable' }, { label: '全部提案', value: 'all' }]} onchange={(event) => { queue = event.currentTarget.value as typeof queue; applyFilters() }} /></label>
        <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">状态</span><Select value={status} options={[{ label: '全部状态', value: '' }, { label: '待审核', value: 'proposed' }, { label: '已接受', value: 'accepted' }, { label: '已拒绝', value: 'rejected' }]} onchange={(event) => { status = event.currentTarget.value; applyFilters() }} /></label>
        <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">搜索</span><div class="relative"><Search class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" /><Input value={searchDraft} class="pl-9" ariaLabel="Search governance proposals" oninput={(event) => (searchDraft = event.currentTarget.value)} /></div></label>
        <Button onclick={applyFilters}>应用筛选</Button>
      </div>
    </div>

    <div class="grid gap-4 xl:grid-cols-[minmax(0,0.9fr)_minmax(360px,1.1fr)]">
      <div>
        {#if proposals.isPending}<LoadingState />{:else if proposals.error}<ErrorState message={proposals.error.message} />{:else if !proposals.data?.items.length}<EmptyState title={queue === 'actionable' ? '当前无待处理提案' : '当前筛选没有提案'} description={queue === 'actionable' ? '现有定义基线已生效，没有需要人工审核的变更。' : '调整状态或搜索条件后查看其他提案。'} />{:else}
          <div class="overflow-x-auto border-y border-border">
            <table class="w-full min-w-[620px] table-fixed text-left text-sm">
              <thead class="text-[11px] font-medium uppercase text-muted-foreground"><tr><th class="w-[38%] px-3 py-2">目标</th><th class="w-[20%] px-3 py-2">来源</th><th class="w-[17%] px-3 py-2">状态</th><th class="w-[25%] px-3 py-2">更新时间</th></tr></thead>
              <tbody>{#each proposals.data.items as item (`${item.tenant_id ?? tenantId()}:${item.id}`)}<tr class="border-t border-border" class:bg-muted={selectedId === item.id && selectedTenantId === (item.tenant_id ?? tenantId())}><td class="px-3 py-3"><button class="block w-full truncate text-left font-medium hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-pressed={selectedId === item.id && selectedTenantId === (item.tenant_id ?? tenantId())} onclick={() => selectProposal(item.id, item.tenant_id ?? tenantId())}>{item.target_kind ?? '—'}:{item.target_id ?? '—'}</button></td><td class="truncate px-3 py-3">{item.source_kind ?? '—'}<span class="block truncate font-mono text-[10px] text-muted-foreground">{item.tenant_id ?? tenantId()}</span></td><td class="px-3 py-3">{item.status ?? '—'}</td><td class="truncate px-3 py-3 text-xs text-muted-foreground">{item.updated_at ?? '—'}</td></tr>{/each}</tbody>
            </table>
          </div>
          <div class="mt-3 flex gap-2">{#if cursorHistory.length}<Button variant="outline" onclick={previousPage}><ChevronLeft class="size-4" aria-hidden="true" />上一页</Button>{/if}{#if proposals.data.page.has_more && proposals.data.page.next_cursor}<Button onclick={() => nextPage(proposals.data?.page.next_cursor ?? '')}>下一页<ChevronRight class="size-4" aria-hidden="true" /></Button>{/if}</div>
        {/if}
      </div>
      <aside class="min-w-0 border-l border-border pl-4">{#if selectedId && selectedTenantId}<OntologyGovernanceWorkflow tenantId={selectedTenantId} proposalId={selectedId} {canManage} />{:else}<EmptyState title="审核详情" description="有提案进入队列后，可在这里查看证据、生成计划并应用变更。" />{/if}</aside>
    </div>
</section>
