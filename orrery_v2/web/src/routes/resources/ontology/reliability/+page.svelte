<script lang="ts">
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  import { ChevronLeft, ChevronRight, Search } from 'lucide-svelte'
  import { untrack } from 'svelte'
  import OntologyReliabilityWorkflow from '#lib/components/ontology/OntologyReliabilityWorkflow.svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { useOntologyContextObjectsQuery } from '#lib/queries/ontology-context'
  import { session } from '#lib/stores/session.svelte'

  let searchDraft = $state($page.url.searchParams.get('search') ?? '')
  let search = $state($page.url.searchParams.get('search') ?? '')
  let status = $state($page.url.searchParams.get('status') ?? '')
  let incidentId = $state($page.url.searchParams.get('incident_id') ?? '')
  let sourceTenantId = $state($page.url.searchParams.get('source_tenant_id') ?? '')
  let ruleId = $state($page.url.searchParams.get('rule_id') ?? '')
  let cursor = $state($page.url.searchParams.get('cursor') ?? '')
  let cursorHistory = $state<string[]>([])
  let boundScope = $state('')
  const tenantId = () => ontologyScopeContext.mode === 'scope' ? ontologyScopeContext.activeScope?.tenant_id ?? '' : ''
  const scopeKey = () => `${ontologyScopeContext.mode}:${tenantId()}`
  const canManage = $derived(hasPermissionScope(session.permissionScope, 'operations.manage'))
  const incidents = useOntologyContextObjectsQuery(() => ({ tenantId: tenantId(), objectType: 'failure_incident', status: status || undefined, search: search || undefined, limit: 50, cursor: cursor || undefined }))
  const visibleIncidents = $derived(incidents.data?.items ?? [])
  const unresolvedCount = $derived(visibleIncidents.filter((item) => item.status !== 'resolved').length)
  const activeRepairCount = $derived(visibleIncidents.filter((item) => item.status === 'repairing' || item.status === 'monitoring').length)
  const resolvedCount = $derived(visibleIncidents.filter((item) => item.status === 'resolved').length)

  $effect(() => {
    const activeTenant = tenantId()
    const activeScopeKey = scopeKey()
    const url = $page.url
    untrack(() => syncRouteState(activeScopeKey, activeTenant, url))
  })

  function syncRouteState(activeScopeKey: string, activeTenant: string, url: URL) {
    const restoreRoute = activeTenant === (url.searchParams.get('tenant_id') ?? '').trim()
    const nextIncident = restoreRoute ? (url.searchParams.get('incident_id') ?? '') : ''
    const nextSourceTenant = restoreRoute && nextIncident ? (url.searchParams.get('source_tenant_id') ?? activeTenant) : ''
    const nextRule = restoreRoute && nextIncident ? (url.searchParams.get('rule_id') ?? '') : ''
    const nextSearch = restoreRoute ? (url.searchParams.get('search') ?? '') : ''
    const nextStatus = restoreRoute ? (url.searchParams.get('status') ?? '') : ''
    const nextCursor = restoreRoute ? (url.searchParams.get('cursor') ?? '') : ''
    const unchanged = boundScope === activeScopeKey && incidentId === nextIncident && sourceTenantId === nextSourceTenant && ruleId === nextRule && search === nextSearch && status === nextStatus && cursor === nextCursor
    boundScope = activeScopeKey
    if (unchanged) return
    incidentId = nextIncident
    sourceTenantId = nextSourceTenant
    ruleId = nextRule
    searchDraft = nextSearch
    search = nextSearch
    status = nextStatus
    cursor = nextCursor
    cursorHistory = []
  }

  function updateRoute() {
    const params = new URLSearchParams()
    if (tenantId()) params.set('tenant_id', tenantId())
    if (incidentId) { params.set('incident_id', incidentId); params.set('source_tenant_id', sourceTenantId) }
    if (ruleId) params.set('rule_id', ruleId)
    if (search) params.set('search', search)
    if (status) params.set('status', status)
    if (cursor) params.set('cursor', cursor)
    void goto(`/resources/ontology/reliability?${params.toString()}`, { invalidateAll: false })
  }

  function selectIncident(id: string, tenant: string) { incidentId = id; sourceTenantId = tenant; ruleId = ''; updateRoute() }
  function selectRule(id: string) { ruleId = id; updateRoute() }
  function clearSelection() { incidentId = ''; sourceTenantId = ''; ruleId = '' }
  function applyFilters() { search = searchDraft.trim(); cursor = ''; cursorHistory = []; clearSelection(); updateRoute() }
  function nextPage(nextCursor: string) { cursorHistory = [...cursorHistory, cursor]; cursor = nextCursor; clearSelection(); updateRoute() }
  function previousPage() { cursor = cursorHistory.at(-1) ?? ''; cursorHistory = cursorHistory.slice(0, -1); clearSelection(); updateRoute() }
  function formatDate(value?: string) { if (!value) return '—'; const parsed = new Date(value); return Number.isNaN(parsed.getTime()) ? value : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'short' }).format(parsed) }
  function statusTone(value?: string): 'success' | 'warning' | 'danger' | 'info' | 'neutral' { if (value === 'resolved') return 'success'; if (value === 'quarantined') return 'danger'; if (value === 'escalated') return 'warning'; if (value === 'repairing' || value === 'monitoring') return 'info'; return 'neutral' }
  function statusLabel(value?: string) { return ({ open: '待处理', contained: '已控制', diagnosed: '已诊断', repairing: '修复中', monitoring: '观察中', resolved: '已解决', escalated: '已升级', quarantined: '已隔离' } as Record<string, string>)[value ?? ''] ?? value ?? '未知' }
</script>

<section class="space-y-4">
  <header><h2 class="text-xl font-semibold">可靠性</h2><p class="mt-1 text-sm text-muted-foreground">从失败事件进入规则、预检、人工审批、精确修复和结果验证。</p></header>

  <dl class="grid grid-cols-3 gap-4 border-y border-border py-4">
    <div><dt class="text-xs text-muted-foreground">当前页待处理</dt><dd class="mt-1 text-2xl font-semibold">{unresolvedCount}</dd></div>
    <div><dt class="text-xs text-muted-foreground">当前页修复与观察</dt><dd class="mt-1 text-2xl font-semibold">{activeRepairCount}</dd></div>
    <div><dt class="text-xs text-muted-foreground">当前页已解决</dt><dd class="mt-1 text-2xl font-semibold">{resolvedCount}</dd></div>
  </dl>

  <div class="grid gap-3 sm:grid-cols-[minmax(0,1fr)_180px_auto] sm:items-end">
    <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">搜索事件</span><div class="relative"><Search class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" /><Input value={searchDraft} class="pl-9" ariaLabel="搜索可靠性事件" oninput={(event) => (searchDraft = event.currentTarget.value)} /></div></label>
    <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">状态</span><Select value={status} options={[{ label: '全部状态', value: '' }, { label: '待处理', value: 'open' }, { label: '已诊断', value: 'diagnosed' }, { label: '修复中', value: 'repairing' }, { label: '观察中', value: 'monitoring' }, { label: '已升级', value: 'escalated' }, { label: '已隔离', value: 'quarantined' }, { label: '已解决', value: 'resolved' }]} onchange={(event) => { status = event.currentTarget.value; applyFilters() }} /></label>
    <Button onclick={applyFilters}>应用筛选</Button>
  </div>

  <div class="grid gap-4 xl:grid-cols-[minmax(240px,0.58fr)_minmax(0,1.72fr)]">
    <section class="min-w-0 border-b border-border pb-4 xl:border-b-0 xl:border-r xl:pb-0 xl:pr-4" aria-label="失败事件队列">
      <div class="mb-2 flex items-center justify-between"><h3 class="text-sm font-semibold">失败事件</h3><span class="text-xs text-muted-foreground">{visibleIncidents.length}</span></div>
      {#if incidents.isPending}<LoadingState />{:else if incidents.error}<ErrorState message={incidents.error.message} />{:else if !visibleIncidents.length}<EmptyState title="当前筛选没有失败事件" />{:else}
        <div class="divide-y divide-border border-y border-border">
          {#each visibleIncidents as item (`${item.tenant_id ?? tenantId()}:${item.id}`)}
            <button type="button" class="block w-full px-3 py-3 text-left transition-colors hover:bg-muted" class:bg-muted={incidentId === item.id && sourceTenantId === (item.tenant_id ?? tenantId())} aria-pressed={incidentId === item.id && sourceTenantId === (item.tenant_id ?? tenantId())} onclick={() => selectIncident(item.id, item.tenant_id ?? tenantId())}>
              <span class="block break-words text-sm font-medium">{item.label}</span>
              <span class="mt-2 flex flex-wrap items-center justify-between gap-2"><Badge tone={statusTone(item.status)}>{statusLabel(item.status)}</Badge><span class="text-[11px] text-muted-foreground">{formatDate(item.updated_at)}</span></span>
              <span class="mt-2 block truncate font-mono text-[10px] text-muted-foreground">{item.tenant_id ?? tenantId()}</span>
            </button>
          {/each}
        </div>
        <div class="mt-3 flex gap-2">{#if cursorHistory.length}<Button size="sm" variant="outline" onclick={previousPage}><ChevronLeft class="size-4" aria-hidden="true" />上一页</Button>{/if}{#if incidents.data?.page.has_more && incidents.data.page.next_cursor}<Button size="sm" onclick={() => nextPage(incidents.data?.page.next_cursor ?? '')}>下一页<ChevronRight class="size-4" aria-hidden="true" /></Button>{/if}</div>
      {/if}
    </section>

    <section class="min-w-0" aria-label="可靠性事件详情">
      {#if incidentId && sourceTenantId}
        <OntologyReliabilityWorkflow tenantId={sourceTenantId} {incidentId} {ruleId} {canManage} onrulechange={selectRule} />
      {:else}
        <EmptyState title="选择失败事件" description="事件选中后显示精确目标、学习规则和受控修复流。" />
      {/if}
    </section>
  </div>
</section>
