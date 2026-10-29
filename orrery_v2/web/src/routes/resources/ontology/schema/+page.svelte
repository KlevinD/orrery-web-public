<script lang="ts">
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  import { ChevronLeft, ChevronRight, Search } from 'lucide-svelte'
  import OntologyDefinitionInspector from '#lib/components/ontology/OntologyDefinitionInspector.svelte'
  import OntologySchemaGraph from '#lib/components/ontology/OntologySchemaGraph.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import type { SchemaDefinitionKind } from '#lib/api/ontology-schema'
  import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'
  import { definitionKindLabel, domainLabel, ontologyValueLabel } from '#lib/ontology-workbench/display-labels'
  import { useOntologySchemaDefinitionsQuery, useOntologySchemaGraphQuery } from '#lib/queries/ontology-schema'
  import { untrack } from 'svelte'

  type View = 'catalog' | 'semantic-map' | 'projection-map'
  let kind = $state<SchemaDefinitionKind | ''>(($page.url.searchParams.get('filter_kind') as SchemaDefinitionKind) || '')
  let origin = $state<'system' | 'tenant' | ''>(($page.url.searchParams.get('origin') as 'system' | 'tenant') || '')
  let status = $state<'active' | 'disabled' | ''>(($page.url.searchParams.get('status') as 'active' | 'disabled') || '')
  let searchDraft = $state($page.url.searchParams.get('search') ?? '')
  let search = $state($page.url.searchParams.get('search') ?? '')
  let view = $state<View>($page.url.searchParams.get('view') === 'semantic-map' ? 'semantic-map' : $page.url.searchParams.get('view') === 'projection-map' ? 'projection-map' : 'catalog')
  let selectedKind = $state<SchemaDefinitionKind>(($page.url.searchParams.get('kind') as SchemaDefinitionKind) || 'object')
  let selectedId = $state($page.url.searchParams.get('id') ?? '')
  let selectedTenantId = $state($page.url.searchParams.get('source_tenant_id') ?? '')
  let cursor = $state($page.url.searchParams.get('cursor') ?? '')
  let cursorHistory = $state<string[]>([])
  let boundTenant = $state('')
  const tenantId = () => ontologyScopeContext.mode === 'scope' ? ontologyScopeContext.activeScope?.tenant_id ?? '' : ''
  const scopeKey = () => `${ontologyScopeContext.mode}:${tenantId()}`
  const definitions = useOntologySchemaDefinitionsQuery(() => ({ tenantId: tenantId(), kind: kind || undefined, origin: origin || undefined, status: status || undefined, search: search || undefined, limit: 50, cursor: cursor || undefined }), () => view === 'catalog')
  const graph = useOntologySchemaGraphQuery(tenantId, () => ({ view: view === 'projection-map' ? 'projection' : 'semantic', search: search || undefined, maxNodes: 180 }), () => view !== 'catalog')

  $effect(() => {
    const activeTenant = tenantId()
    const activeScopeKey = scopeKey()
    const url = $page.url
    untrack(() => syncRouteState(activeScopeKey, activeTenant, url))
  })

  function syncRouteState(activeScopeKey: string, activeTenant: string, url: URL) {
    const restoreRoute = activeTenant === (url.searchParams.get('tenant_id') ?? '').trim()
    const nextView: View = restoreRoute && url.searchParams.get('view') === 'semantic-map' ? 'semantic-map' : restoreRoute && url.searchParams.get('view') === 'projection-map' ? 'projection-map' : 'catalog'
    const nextSelectedId = restoreRoute ? (url.searchParams.get('id') ?? '') : ''
    const nextSelectedTenantId = restoreRoute && nextSelectedId ? (url.searchParams.get('source_tenant_id') ?? activeTenant) : ''
    const nextSelectedKind = restoreRoute ? ((url.searchParams.get('kind') as SchemaDefinitionKind) || 'object') : 'object'
    const nextSearch = restoreRoute ? (url.searchParams.get('search') ?? '') : ''
    const nextKind = restoreRoute ? ((url.searchParams.get('filter_kind') as SchemaDefinitionKind) || '') : ''
    const nextOrigin = restoreRoute ? ((url.searchParams.get('origin') as typeof origin) || '') : ''
    const nextStatus = restoreRoute ? ((url.searchParams.get('status') as typeof status) || '') : ''
    const nextCursor = restoreRoute ? (url.searchParams.get('cursor') ?? '') : ''
    const unchanged = boundTenant === activeScopeKey && view === nextView && selectedId === nextSelectedId && selectedTenantId === nextSelectedTenantId && selectedKind === nextSelectedKind &&
      search === nextSearch && kind === nextKind && origin === nextOrigin && status === nextStatus && cursor === nextCursor
    boundTenant = activeScopeKey
    if (unchanged) return
    view = nextView
    selectedId = nextSelectedId
    selectedTenantId = nextSelectedTenantId
    selectedKind = nextSelectedKind
    searchDraft = nextSearch
    search = nextSearch
    kind = nextKind
    origin = nextOrigin
    status = nextStatus
    cursor = nextCursor
    cursorHistory = []
  }

  function updateRoute() {
    const params = new URLSearchParams({ view })
    if (tenantId()) params.set('tenant_id', tenantId())
    if (selectedId) { params.set('kind', selectedKind); params.set('id', selectedId); params.set('source_tenant_id', selectedTenantId) }
    if (kind) params.set('filter_kind', kind)
    if (origin) params.set('origin', origin)
    if (status) params.set('status', status)
    if (search) params.set('search', search)
    if (cursor) params.set('cursor', cursor)
    void goto(`/resources/ontology/schema?${params.toString()}`, { invalidateAll: false })
  }

  function selectDefinition(nextKind: SchemaDefinitionKind, id: string, sourceTenantId = selectedTenantId || tenantId()) { selectedKind = nextKind; selectedId = id; selectedTenantId = sourceTenantId; updateRoute() }
  function clearSelection() { selectedId = ''; selectedTenantId = '' }
  function applyFilters() { search = searchDraft.trim(); cursor = ''; cursorHistory = []; clearSelection(); updateRoute() }
  function changeView(next: View) { view = next; clearSelection(); cursor = ''; cursorHistory = []; updateRoute() }
  function nextPage(nextCursor: string) { cursorHistory = [...cursorHistory, cursor]; cursor = nextCursor; clearSelection(); updateRoute() }
  function previousPage() { cursor = cursorHistory.at(-1) ?? ''; cursorHistory = cursorHistory.slice(0, -1); clearSelection(); updateRoute() }
</script>

<section class="space-y-4">
  <header><h2 class="text-xl font-semibold">本体定义目录</h2><p class="mt-1 text-sm text-muted-foreground">检索定义、约束和领域事件投影关系。</p></header>
  <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-[minmax(220px,1fr)_150px_150px_150px_auto] xl:items-end">
      <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">搜索</span><div class="relative"><Search class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" /><Input value={searchDraft} class="pl-9" ariaLabel="Search definitions" oninput={(event) => (searchDraft = event.currentTarget.value)} /></div></label>
      <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">定义类型</span><Select value={kind} options={[{ label: '全部', value: '' }, { label: '对象', value: 'object' }, { label: '关系', value: 'link' }, { label: '属性', value: 'property' }, { label: '映射', value: 'mapping' }, { label: '动作', value: 'action' }]} onchange={(event) => { kind = event.currentTarget.value as typeof kind; applyFilters() }} /></label>
      <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">来源</span><Select value={origin} options={[{ label: '全部', value: '' }, { label: '系统', value: 'system' }, { label: '当前 Scope', value: 'tenant' }]} onchange={(event) => { origin = event.currentTarget.value as typeof origin; applyFilters() }} /></label>
      <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">状态</span><Select value={status} options={[{ label: '全部', value: '' }, { label: '启用', value: 'active' }, { label: '停用', value: 'disabled' }]} onchange={(event) => { status = event.currentTarget.value as typeof status; applyFilters() }} /></label>
      <Button onclick={applyFilters}>应用筛选</Button>
    </div>
    <div class="flex flex-wrap gap-1 border-b border-border" role="tablist" aria-label="本体定义视图">{#each [{ label: '目录', value: 'catalog' }, { label: '模型关系', value: 'semantic-map' }, { label: '数据映射', value: 'projection-map' }] as option}<button class="border-b-2 px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" class:border-primary={view === option.value} class:border-transparent={view !== option.value} role="tab" aria-selected={view === option.value} onclick={() => changeView(option.value as View)}>{option.label}</button>{/each}</div>

    {#if view !== 'catalog'}
      <div class="grid gap-4 xl:grid-cols-[minmax(0,1.4fr)_minmax(320px,0.6fr)]"><div>{#if graph.isPending}<LoadingState />{:else if graph.error}<ErrorState message={graph.error.message} />{:else if !graph.data?.nodes.length}<EmptyState title="暂无可见定义" />{:else}<OntologySchemaGraph graph={graph.data} tenantId={tenantId()} {selectedId} {selectedKind} {selectedTenantId} onselect={selectDefinition} />{/if}</div><aside class="min-w-0 border-l border-border pl-4">{#if selectedId && selectedTenantId}<OntologyDefinitionInspector tenantId={selectedTenantId} kind={selectedKind} definitionId={selectedId} onselect={selectDefinition} />{:else}<EmptyState title="选择定义查看详情" />{/if}</aside></div>
    {:else}
      <div class="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(320px,0.65fr)]">
        <div>{#if definitions.isPending}<LoadingState />{:else if definitions.error}<ErrorState message={definitions.error.message} />{:else if !definitions.data?.items.length}<EmptyState title="暂无可见定义" />{:else}<div class="overflow-x-auto border-y border-border"><table class="w-full min-w-[720px] table-fixed text-left text-[13px]"><thead class="text-[11px] font-medium uppercase text-muted-foreground"><tr><th class="w-[32%] px-3 py-2">名称</th><th class="w-[13%] px-3 py-2">类型</th><th class="w-[16%] px-3 py-2">来源</th><th class="w-[20%] px-3 py-2">所属域</th><th class="w-[19%] px-3 py-2">版本 / 状态</th></tr></thead><tbody>{#each definitions.data.items as item (`${item.tenant_id ?? tenantId()}:${item.kind}:${item.id}`)}<tr class="border-t border-border" class:bg-muted={selectedId === item.id && selectedTenantId === (item.tenant_id ?? tenantId())}><td class="px-3 py-3"><button class="block w-full truncate text-left font-medium hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-pressed={selectedId === item.id && selectedTenantId === (item.tenant_id ?? tenantId())} onclick={() => selectDefinition(item.kind, item.id, item.tenant_id ?? tenantId())}>{item.label}</button><span class="block truncate font-mono text-[11px] text-muted-foreground">{item.id}</span></td><td class="px-3 py-3">{definitionKindLabel(item.kind)}</td><td class="px-3 py-3">{ontologyValueLabel(item.origin)}<span class="block truncate font-mono text-[10px] text-muted-foreground">{item.tenant_id ?? tenantId()}</span></td><td class="truncate px-3 py-3">{item.owner_domain ? domainLabel(item.owner_domain) : '—'}</td><td class="px-3 py-3">{item.version} · {ontologyValueLabel(item.status)}</td></tr>{/each}</tbody></table></div><div class="mt-3 flex gap-2">{#if cursorHistory.length}<Button variant="outline" onclick={previousPage}><ChevronLeft class="size-4" aria-hidden="true" />上一页</Button>{/if}{#if definitions.data.page.has_more && definitions.data.page.next_cursor}<Button onclick={() => nextPage(definitions.data?.page.next_cursor ?? '')}>下一页<ChevronRight class="size-4" aria-hidden="true" /></Button>{/if}</div>{/if}</div>
        <aside class="min-w-0 border-l border-border pl-4">{#if selectedId && selectedTenantId}<OntologyDefinitionInspector tenantId={selectedTenantId} kind={selectedKind} definitionId={selectedId} onselect={selectDefinition} />{:else}<EmptyState title="选择定义查看详情" />{/if}</aside>
      </div>
    {/if}
</section>
