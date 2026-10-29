<script lang="ts">
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  import { ChevronLeft, ChevronRight, Network, Search } from 'lucide-svelte'
  import OntologyNeighborhoodGraph from '#lib/components/ontology/OntologyNeighborhoodGraph.svelte'
  import OntologyObjectInspector from '#lib/components/ontology/OntologyObjectInspector.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'
  import { objectTypeLabel, ontologyValueLabel } from '#lib/ontology-workbench/display-labels'
  import { useOntologyContextNeighborhoodQuery, useOntologyContextObjectsQuery } from '#lib/queries/ontology-context'
  import { untrack } from 'svelte'

  type View = 'objects' | 'graph'
  let view = $state<View>($page.url.searchParams.get('view') === 'graph' ? 'graph' : 'objects')
  let searchDraft = $state($page.url.searchParams.get('search') ?? '')
  let search = $state($page.url.searchParams.get('search') ?? '')
  let objectTypeDraft = $state($page.url.searchParams.get('object_type') ?? '')
  let objectType = $state($page.url.searchParams.get('object_type') ?? '')
  let statusDraft = $state($page.url.searchParams.get('status') ?? '')
  let status = $state($page.url.searchParams.get('status') ?? '')
  let sourceDraft = $state($page.url.searchParams.get('source') ?? '')
  let source = $state($page.url.searchParams.get('source') ?? '')
  let selectedId = $state($page.url.searchParams.get('object_id') ?? '')
  let selectedTenantId = $state($page.url.searchParams.get('source_tenant_id') ?? '')
  let rootId = $state($page.url.searchParams.get('root_id') ?? $page.url.searchParams.get('object_id') ?? '')
  let linkTypeDraft = $state($page.url.searchParams.get('link_type') ?? '')
  let linkType = $state($page.url.searchParams.get('link_type') ?? '')
  let depth = $state<1 | 2>($page.url.searchParams.get('depth') === '2' ? 2 : 1)
  let cursor = $state($page.url.searchParams.get('cursor') ?? '')
  let cursorHistory = $state<string[]>([])
  let boundTenant = $state('')
  const tenantId = () => ontologyScopeContext.mode === 'scope' ? ontologyScopeContext.activeScope?.tenant_id ?? '' : ''
  const scopeKey = () => `${ontologyScopeContext.mode}:${tenantId()}`
  const objects = useOntologyContextObjectsQuery(
    () => ({ tenantId: tenantId(), search: search || undefined, objectType: objectType || undefined, status: status || undefined, source: source || undefined, limit: 50, cursor: cursor || undefined }),
    () => Boolean(tenantId())
  )
  const firstGraphObject = $derived(objects.data?.items.find((item) => item.label) ?? objects.data?.items[0])
  const graphRootId = $derived(rootId || selectedId || firstGraphObject?.id || '')
  const graphTenantId = $derived(selectedTenantId || tenantId())
  const graphSelectedId = $derived(selectedId || graphRootId)
  const neighborhood = useOntologyContextNeighborhoodQuery(() => ({ tenantId: graphTenantId, rootId: graphRootId, linkType: linkType || undefined, depth, maxNodes: 120, maxEdges: 240 }), () => view === 'graph')

  $effect(() => {
    const activeTenant = tenantId()
    const activeScopeKey = scopeKey()
    const url = $page.url
    untrack(() => syncRouteState(activeScopeKey, activeTenant, url))
  })

  function syncRouteState(activeScopeKey: string, activeTenant: string, url: URL) {
    const restoreRoute = activeTenant === (url.searchParams.get('tenant_id') ?? '').trim()
    const nextSelectedId = restoreRoute ? (url.searchParams.get('object_id') ?? '') : ''
    const nextSelectedTenantId = restoreRoute && nextSelectedId ? (url.searchParams.get('source_tenant_id') ?? activeTenant) : ''
    const nextView: View = restoreRoute && url.searchParams.get('view') === 'graph' ? 'graph' : 'objects'
    const nextRootId = nextView === 'graph' ? (url.searchParams.get('root_id') ?? nextSelectedId) : ''
    const nextLinkType = restoreRoute && nextView === 'graph' ? (url.searchParams.get('link_type') ?? '').trim().toLowerCase() : ''
    const nextSearch = restoreRoute ? (url.searchParams.get('search') ?? '') : ''
    const nextObjectType = restoreRoute ? (url.searchParams.get('object_type') ?? '') : ''
    const nextStatus = restoreRoute ? (url.searchParams.get('status') ?? '') : ''
    const nextSource = restoreRoute ? (url.searchParams.get('source') ?? '') : ''
    const nextCursor = restoreRoute ? (url.searchParams.get('cursor') ?? '') : ''
    const nextDepth: 1 | 2 = restoreRoute && url.searchParams.get('depth') === '2' ? 2 : 1
    const unchanged = boundTenant === activeScopeKey && selectedId === nextSelectedId && selectedTenantId === nextSelectedTenantId && rootId === nextRootId && search === nextSearch &&
      objectType === nextObjectType && status === nextStatus && source === nextSource && linkType === nextLinkType && cursor === nextCursor && view === nextView && depth === nextDepth
    boundTenant = activeScopeKey
    if (unchanged) return
    selectedId = nextSelectedId
    selectedTenantId = nextSelectedTenantId
    rootId = nextRootId
    linkTypeDraft = nextLinkType
    linkType = nextLinkType
    searchDraft = nextSearch
    search = nextSearch
    objectTypeDraft = nextObjectType
    objectType = nextObjectType
    statusDraft = nextStatus
    status = nextStatus
    sourceDraft = nextSource
    source = nextSource
    cursor = nextCursor
    cursorHistory = []
    view = nextView
    depth = nextDepth
  }

  function updateRoute() { const params = new URLSearchParams({ view }); if (tenantId()) params.set('tenant_id', tenantId()); if (selectedId) { params.set('object_id', selectedId); params.set('source_tenant_id', selectedTenantId) } if (view === 'graph' && rootId) params.set('root_id', rootId); if (view === 'graph' && linkType) params.set('link_type', linkType); if (search) params.set('search', search); if (objectType) params.set('object_type', objectType); if (status) params.set('status', status); if (source) params.set('source', source); if (cursor) params.set('cursor', cursor); if (view === 'graph') params.set('depth', String(depth)); void goto(`/resources/ontology/context?${params.toString()}`, { invalidateAll: false }) }
  function selectObject(id: string, sourceTenantId: string) { selectedId = id; selectedTenantId = sourceTenantId; updateRoute() }
  function selectGraphObject(id: string) { selectedId = id; selectedTenantId = graphTenantId; updateRoute() }
  function changeView(next: View) { view = next; if (next === 'graph' && !rootId) rootId = selectedId; updateRoute() }
  function applyGraphFilter() { linkType = linkTypeDraft.trim().toLowerCase(); updateRoute() }
  function expandFromObject(id: string) { selectedId = id; rootId = id; view = 'graph'; updateRoute() }
  function clearSelection() { selectedId = ''; selectedTenantId = ''; rootId = '' }
  function applyFilters() { search = searchDraft.trim(); objectType = objectTypeDraft.trim(); status = statusDraft.trim(); source = sourceDraft.trim(); cursor = ''; cursorHistory = []; clearSelection(); updateRoute() }
  function nextPage(nextCursor: string) { cursorHistory = [...cursorHistory, cursor]; cursor = nextCursor; clearSelection(); updateRoute() }
  function previousPage() { cursor = cursorHistory.at(-1) ?? ''; cursorHistory = cursorHistory.slice(0, -1); clearSelection(); updateRoute() }
</script>

<section class="space-y-4">
  <header><h2 class="text-xl font-semibold">实际数据</h2><p class="mt-1 text-sm text-muted-foreground">浏览已投影对象、事实、关系和证据。</p></header>
  <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-[minmax(220px,1fr)_minmax(140px,0.5fr)_minmax(130px,0.45fr)_minmax(140px,0.5fr)_auto] xl:items-end"><label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">搜索对象</span><div class="relative"><Search class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" /><Input value={searchDraft} class="pl-9" ariaLabel="Search objects" oninput={(event) => (searchDraft = event.currentTarget.value)} /></div></label><label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">对象类型</span><Input value={objectTypeDraft} ariaLabel="Object type" oninput={(event) => (objectTypeDraft = event.currentTarget.value)} /></label><label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">状态</span><Input value={statusDraft} ariaLabel="Object status" oninput={(event) => (statusDraft = event.currentTarget.value)} /></label><label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">来源</span><Input value={sourceDraft} ariaLabel="Object source" oninput={(event) => (sourceDraft = event.currentTarget.value)} /></label><Button onclick={applyFilters}>应用筛选</Button></div>
      <div class="flex flex-wrap items-end justify-between gap-3 border-b border-border"><div role="tablist" aria-label="实际数据视图"><button class="border-b-2 px-3 py-2 text-sm" class:border-primary={view === 'objects'} class:border-transparent={view !== 'objects'} role="tab" aria-selected={view === 'objects'} onclick={() => changeView('objects')}>对象目录</button><button class="border-b-2 px-3 py-2 text-sm" class:border-primary={view === 'graph'} class:border-transparent={view !== 'graph'} role="tab" aria-selected={view === 'graph'} onclick={() => changeView('graph')}><Network class="mr-1 inline size-4" aria-hidden="true" />实际数据关系图</button></div>{#if view === 'graph'}<div class="flex flex-wrap items-end gap-2 pb-2"><label class="grid gap-1"><span class="text-xs text-muted-foreground">关系类型</span><Input value={linkTypeDraft} ariaLabel="关系类型" oninput={(event) => (linkTypeDraft = event.currentTarget.value)} /></label><Button size="sm" variant="outline" onclick={applyGraphFilter}>筛选关系</Button><label class="grid gap-1 text-xs text-muted-foreground"><span>深度</span><Select value={String(depth)} options={[{ label: '1', value: '1' }, { label: '2', value: '2' }]} onchange={(event) => { depth = Number(event.currentTarget.value) as 1 | 2; updateRoute() }} /></label></div>{/if}</div>
    {#if view === 'graph'}
      <div class="grid gap-4 xl:grid-cols-[minmax(0,1.4fr)_minmax(320px,0.6fr)]"><div>{#if !tenantId()}<EmptyState title="请选择授权范围后查看实际数据关系图" />{:else if objects.isPending}<LoadingState />{:else if objects.error}<ErrorState message={objects.error.message} />{:else if !graphRootId}<EmptyState title="当前范围没有可见对象" />{:else if neighborhood.isPending}<LoadingState />{:else if neighborhood.error}<ErrorState message={neighborhood.error.message} />{:else if neighborhood.data}<OntologyNeighborhoodGraph graph={neighborhood.data} selectedId={graphSelectedId} onselect={selectGraphObject} />{/if}</div><aside class="border-l border-border pl-4">{#if graphSelectedId && graphTenantId}<OntologyObjectInspector tenantId={graphTenantId} objectId={graphSelectedId} onexpand={expandFromObject} />{:else}<EmptyState title="选择图中对象查看详情" />{/if}</aside></div>
    {:else}
      <div class="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(340px,0.7fr)]"><div>{#if !tenantId()}<EmptyState title="请选择授权范围后查看实际数据" />{:else if objects.isPending}<LoadingState />{:else if objects.error}<ErrorState message={objects.error.message} />{:else if !objects.data?.items.length}<EmptyState title="当前范围没有可见对象" />{:else}<div class="overflow-x-auto border-y border-border"><table class="w-full min-w-[640px] table-fixed text-left text-[13px]"><thead class="text-[11px] font-medium uppercase text-muted-foreground"><tr><th class="w-[38%] px-3 py-2">对象</th><th class="w-[22%] px-3 py-2">类型</th><th class="w-[16%] px-3 py-2">状态</th><th class="w-[24%] px-3 py-2">更新时间</th></tr></thead><tbody>{#each objects.data.items as item (`${item.tenant_id ?? tenantId()}:${item.id}`)}<tr class="border-t border-border" class:bg-muted={selectedId === item.id && selectedTenantId === (item.tenant_id ?? tenantId())}><td class="px-3 py-3"><button class="block w-full truncate text-left font-medium hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-pressed={selectedId === item.id && selectedTenantId === (item.tenant_id ?? tenantId())} onclick={() => selectObject(item.id, item.tenant_id ?? tenantId())}>{item.label}</button><span class="block truncate font-mono text-[11px] text-muted-foreground">{item.id}</span><span class="block truncate font-mono text-[10px] text-muted-foreground">{item.tenant_id ?? tenantId()}</span></td><td class="truncate px-3 py-3">{objectTypeLabel(item.object_type)}</td><td class="px-3 py-3">{item.status ? ontologyValueLabel(item.status) : '—'}</td><td class="truncate px-3 py-3 text-xs text-muted-foreground">{item.updated_at ?? '—'}</td></tr>{/each}</tbody></table></div><div class="mt-3 flex gap-2">{#if cursorHistory.length}<Button variant="outline" onclick={previousPage}><ChevronLeft class="size-4" aria-hidden="true" />上一页</Button>{/if}{#if objects.data.page.has_more && objects.data.page.next_cursor}<Button onclick={() => nextPage(objects.data?.page.next_cursor ?? '')}>下一页<ChevronRight class="size-4" aria-hidden="true" /></Button>{/if}</div>{/if}</div><aside class="min-w-0 border-l border-border pl-4">{#if selectedId && selectedTenantId}<OntologyObjectInspector tenantId={selectedTenantId} objectId={selectedId} onexpand={expandFromObject} />{:else}<EmptyState title="选择对象查看详情" />{/if}</aside></div>
    {/if}
</section>
