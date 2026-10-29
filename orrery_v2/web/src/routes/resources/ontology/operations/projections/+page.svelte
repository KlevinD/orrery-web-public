<script lang="ts">
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  import { ChevronLeft, ChevronRight } from 'lucide-svelte'
  import OntologyProjectionAuditInspector from '#lib/components/ontology/OntologyProjectionAuditInspector.svelte'
  import OntologyOperationsOverview from '#lib/components/ontology/OntologyOperationsOverview.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'
  import { useOntologyProjectionAuditsQuery } from '#lib/queries/ontology-projections'
  import { untrack } from 'svelte'

  const routeAuditId = () => ($page.url.searchParams.get('audit_id') ?? '').trim()
  const routeStatus = () => ($page.url.searchParams.get('status') ?? '').trim()
  const routeReasonCode = () => ($page.url.searchParams.get('reason_code') ?? '').trim()
  const routeCursor = () => ($page.url.searchParams.get('cursor') ?? '').trim()
  const tenantId = () => ontologyScopeContext.mode === 'scope' ? ontologyScopeContext.activeScope?.tenant_id ?? '' : ''
  const scopeKey = () => `${ontologyScopeContext.mode}:${tenantId()}`

  let status = $state(routeStatus())
  let reasonCodeDraft = $state(routeReasonCode())
  let reasonCode = $state(routeReasonCode())
  let cursor = $state(routeCursor())
  let cursorHistory = $state<string[]>([])
  let selectedAuditId = $state(routeAuditId())
  let selectedTenantId = $state(($page.url.searchParams.get('source_tenant_id') ?? '').trim())
  let boundTenantId = $state('')

  const audits = useOntologyProjectionAuditsQuery(() => ({
    tenantId: tenantId(),
    status: status || undefined,
    reasonCode: reasonCode || undefined,
    limit: 30,
    cursor: cursor || undefined
  }))
  $effect(() => {
    const activeTenant = tenantId().trim()
    const activeScopeKey = scopeKey()
    const url = $page.url
    untrack(() => syncRouteState(activeScopeKey, activeTenant, url))
  })

  function syncRouteState(activeScopeKey: string, activeTenant: string, url: URL) {
    const restoreRoute = activeTenant === (url.searchParams.get('tenant_id') ?? '').trim()
    const nextAuditId = restoreRoute ? (url.searchParams.get('audit_id') ?? '').trim() : ''
    const nextSelectedTenantId = restoreRoute && nextAuditId ? (url.searchParams.get('source_tenant_id') ?? activeTenant).trim() : ''
    const nextStatus = restoreRoute ? (url.searchParams.get('status') ?? '').trim() : ''
    const nextReasonCode = restoreRoute ? (url.searchParams.get('reason_code') ?? '').trim() : ''
    const nextCursor = restoreRoute ? (url.searchParams.get('cursor') ?? '').trim() : ''
    const unchanged = boundTenantId === activeScopeKey && selectedAuditId === nextAuditId && selectedTenantId === nextSelectedTenantId && status === nextStatus && reasonCode === nextReasonCode && cursor === nextCursor
    boundTenantId = activeScopeKey
    if (unchanged) return
    selectedAuditId = nextAuditId
    selectedTenantId = nextSelectedTenantId
    status = nextStatus
    reasonCodeDraft = nextReasonCode
    reasonCode = nextReasonCode
    cursor = nextCursor
    cursorHistory = []
  }

  function updateRoute() {
    const params = new URLSearchParams()
    if (tenantId()) params.set('tenant_id', tenantId())
    if (selectedAuditId) { params.set('audit_id', selectedAuditId); params.set('source_tenant_id', selectedTenantId) }
    if (status) params.set('status', status)
    if (reasonCode) params.set('reason_code', reasonCode)
    if (cursor) params.set('cursor', cursor)
    void goto(`/resources/ontology/operations/projections?${params.toString()}`, { invalidateAll: false })
  }

  function selectAudit(id: string, sourceTenantId: string) {
    selectedAuditId = id
    selectedTenantId = sourceTenantId
    updateRoute()
  }

  function clearSelection() { selectedAuditId = ''; selectedTenantId = '' }

  function setStatus(nextStatus: string) {
    status = nextStatus
    cursor = ''
    cursorHistory = []
    clearSelection()
    updateRoute()
  }

  function applyReasonCode() {
    reasonCode = reasonCodeDraft.trim()
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
</script>

<section class="space-y-4">
  <header>
    <h2 class="text-xl font-semibold">Projection 运维</h2>
  </header>

  <OntologyOperationsOverview />

  <div class="flex flex-wrap items-end gap-3">
      <label class="w-48 space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">状态</span>
        <Select
          value={status}
          options={[
            { label: '全部状态', value: '' },
            { label: '跳过', value: 'skipped' },
            { label: '失败', value: 'failed' },
            { label: '已重放', value: 'replayed' },
            { label: '已应用', value: 'applied' },
            { label: '已确认', value: 'acknowledged' }
          ]}
          onchange={(event) => setStatus(event.currentTarget.value)}
        />
      </label>
      <label class="min-w-[220px] flex-1 space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">原因代码</span>
        <Input
          value={reasonCodeDraft}
          ariaLabel="Reason code"
          oninput={(event) => (reasonCodeDraft = event.currentTarget.value)}
        />
      </label>
      <Button onclick={applyReasonCode}>筛选</Button>
    </div>

    <div class="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(320px,0.8fr)]">
      <div>
        {#if audits.isPending}
          <LoadingState />
        {:else if audits.error}
          <ErrorState message={audits.error.message} />
        {:else if !audits.data || audits.data.items.length === 0}
          <EmptyState title="当前无异常审计" description="投影产出正常，当前筛选下没有需要处置的审计记录。" />
        {:else}
          <div class="overflow-x-auto rounded-xl border border-border bg-card">
            <table class="w-full min-w-[680px] table-fixed text-left text-sm">
              <thead class="text-[11px] font-medium uppercase text-muted-foreground">
                <tr>
                  <th class="w-[34%] px-4 py-2">事件类型</th>
                  <th class="w-[16%] px-4 py-2">状态</th>
                  <th class="w-[25%] px-4 py-2">原因</th>
                  <th class="w-[25%] px-4 py-2">时间</th>
                </tr>
              </thead>
              <tbody>
                {#each audits.data.items as item (`${item.tenant_id ?? tenantId()}:${item.id}`)}
                  <tr class="border-t border-border" class:bg-muted={selectedAuditId === item.id && selectedTenantId === (item.tenant_id ?? tenantId())}>
                    <td class="px-4 py-3">
                      <button
                        class="block w-full truncate text-left font-medium hover:underline"
                        aria-pressed={selectedAuditId === item.id && selectedTenantId === (item.tenant_id ?? tenantId())}
                        title={item.source_event_type ?? item.id}
                        onclick={() => selectAudit(item.id, item.tenant_id ?? tenantId())}
                      >
                        {item.source_event_type ?? '—'}
                      </button>
                      <span class="block truncate font-mono text-[10px] text-muted-foreground">{item.tenant_id ?? tenantId()}</span>
                    </td>
                    <td class="truncate px-4 py-3">{item.status ?? '—'}</td>
                    <td class="truncate px-4 py-3" title={item.reason_code ?? ''}>{item.reason_code ?? '—'}</td>
                    <td class="truncate px-4 py-3 text-xs text-muted-foreground">{item.observed_at ?? '—'}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
          <div class="mt-3 flex gap-2">
            {#if cursorHistory.length > 0}
              <Button variant="outline" onclick={previousPage}>
                <ChevronLeft size={16} aria-hidden="true" />
                上一页
              </Button>
            {/if}
            {#if audits.data.page.has_more && audits.data.page.next_cursor}
              <Button onclick={() => nextPage(audits.data?.page.next_cursor ?? '')}>
                下一页
                <ChevronRight size={16} aria-hidden="true" />
              </Button>
            {/if}
          </div>
        {/if}
      </div>
      <aside class="min-w-0 border-l border-border pl-4">
        {#if selectedAuditId && selectedTenantId}
          <OntologyProjectionAuditInspector tenantId={selectedTenantId} auditId={selectedAuditId} />
        {:else}
          <EmptyState title="选择一条审计查看详情" description="有异常记录时，可在这里查看原因、重放历史并执行处置。" />
        {/if}
      </aside>
    </div>
</section>
