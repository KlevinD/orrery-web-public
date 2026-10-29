<script lang="ts">
  import { onMount } from 'svelte'
  import { Database, Plus, RefreshCw, Search, Trash2, X } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Tabs from '#lib/components/ui/Tabs.svelte'
  import ResourceRelationshipGraph from '#lib/components/graph/ResourceRelationshipGraph.svelte'
  import { buildKnowledgeRelationshipGraph } from '#lib/graph/resourceRelationships'
  import { locale, createTranslator } from '#lib/i18n'
  import { knowledgeAccessLabel } from '#lib/knowledge/accessPresentation'
  import { deleteKnowledgeBase, listKnowledgeBases, loadKnowledgeGraphDocuments, searchKnowledge } from '#lib/queries/knowledge'
  import type { KnowledgeBaseDTO, KnowledgeDocumentDTO, KnowledgeSearchResultDTO } from '#lib/types/api/knowledge'

  const t = $derived(createTranslator($locale))

  let bases = $state<KnowledgeBaseDTO[]>([])
  let loading = $state(true)
  let deletingBaseId = $state('')
  let error = $state('')
  let notice = $state('')
  let baseSearchQuery = $state('')
  let contentSearchQuery = $state('')
  let contentSearchResults = $state<KnowledgeSearchResultDTO[]>([])
  let contentSearchLoading = $state(false)
  let contentSearchError = $state('')
  let resourceView = $state<'list' | 'graph'>('list')
  let documentsByBase = $state<Record<string, KnowledgeDocumentDTO[]>>({})
  let graphLoading = $state(false)
  let graphLoaded = $state(false)
  let graphError = $state('')

  const visibleBases = $derived.by(() => {
    const query = baseSearchQuery.trim().toLocaleLowerCase()
    if (!query) return bases
    return bases.filter((base) => searchableBaseText(base).includes(query))
  })
  const relationshipGraph = $derived(buildKnowledgeRelationshipGraph(visibleBases, documentsByBase))
  const resourceViewTabs = $derived([
    { value: 'list', label: t('resources.graph.listView') },
    { value: 'graph', label: t('resources.graph.graphView') }
  ])

  onMount(() => {
    void loadBases()
  })

  $effect(() => {
    if (resourceView === 'graph' && !loading && !error && !graphLoaded && !graphLoading && !graphError) void loadGraphDocuments()
  })

  async function loadBases() {
    loading = true
    error = ''
    notice = ''
    graphLoaded = false
    graphError = ''
    documentsByBase = {}
    try {
      const page = await listKnowledgeBases()
      bases = page.items ?? []
    } catch (err) {
      error = errorMessage(err)
    } finally {
      loading = false
    }
  }

  async function loadGraphDocuments() {
    graphLoading = true
    graphError = ''
    try {
      documentsByBase = await loadKnowledgeGraphDocuments(bases.map((base) => base.id))
      graphLoaded = true
    } catch (err) {
      graphError = errorMessage(err)
    } finally {
      graphLoading = false
    }
  }

  async function deleteBase(event: MouseEvent, base: KnowledgeBaseDTO) {
    event.preventDefault()
    event.stopPropagation()
    if (deletingBaseId) {
      return
    }
    const name = base.name || t('nav.knowledge')
    if (!confirm(t('resources.knowledge.deleteConfirm', { name }))) {
      return
    }
    deletingBaseId = base.id
    error = ''
    notice = ''
    try {
      await deleteKnowledgeBase(base.id)
      bases = bases.filter((item) => item.id !== base.id)
      const remainingDocuments = { ...documentsByBase }
      delete remainingDocuments[base.id]
      documentsByBase = remainingDocuments
      notice = t('resources.knowledge.deleted', { name })
    } catch (err) {
      error = errorMessage(err)
    } finally {
      deletingBaseId = ''
    }
  }

  function providerLabel(base: KnowledgeBaseDTO) {
    return base.provider ?? 'local'
  }

  function providerName(base: KnowledgeBaseDTO) {
    if (base.provider === 'mcp') return base.provider_display_name || 'MCP'
    return base.provider === 'pageindex' ? t('resources.knowledge.provider.pageindex') : t('resources.knowledge.provider.local')
  }

  function visibilityLabel(value?: string) {
    return t(value === 'private' ? 'resources.knowledge.visibility.personal' : 'resources.knowledge.visibility.public')
  }

  function statusLabel(value?: string) {
    if (value === 'disabled') return t('resources.knowledge.status.disabled')
    if (value === 'deleting') return t('resources.knowledge.status.deleting')
    return t('resources.knowledge.status.active')
  }

  function statusTone(base: KnowledgeBaseDTO) {
    if (base.status === 'disabled' || base.status === 'deleting') return 'warning'
    return 'success'
  }

  function displayField(value?: string) {
    return !value || value === 'Not provided' ? t('workspace.notProvided') : value
  }

  function searchableBaseText(base: KnowledgeBaseDTO) {
    return [
      base.id,
      base.name,
      base.description,
      providerName(base),
      visibilityLabel(base.visibility),
      statusLabel(base.status)
    ]
      .filter(Boolean)
      .join(' ')
      .toLocaleLowerCase()
  }

  async function runContentSearch(event: SubmitEvent) {
    event.preventDefault()
    const query = contentSearchQuery.trim()
    contentSearchError = ''
    if (!query) {
      contentSearchResults = []
      return
    }
    contentSearchLoading = true
    try {
      const page = await searchKnowledge({ query, limit: 20 })
      contentSearchResults = page.results ?? []
    } catch (err) {
      contentSearchResults = []
      contentSearchError = errorMessage(err)
    } finally {
      contentSearchLoading = false
    }
  }

  function searchBaseName(knowledgeBaseId: string) {
    return bases.find((base) => base.id === knowledgeBaseId)?.name ?? knowledgeBaseId
  }

  function errorMessage(err: unknown) {
    if (err instanceof Error) {
      return err.message
    }
    return String(err)
  }
</script>

<svelte:head>
  <title>{t('nav.knowledge')} · Orrery</title>
</svelte:head>

<section class="page-shell max-w-[1040px] space-y-5">
  <div class="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
    <div>
      <h1 class="page-title">{t('nav.knowledge')}</h1>
      <p class="page-description">{t('resources.knowledge.listDescription')}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <Button variant="outline" onclick={loadBases} disabled={loading}>
        <RefreshCw class="h-4 w-4" />
        {t('resources.knowledge.reloading')}
      </Button>
      <Button href="/resources/knowledge/new">
        <Plus class="h-4 w-4" />
        {t('resources.knowledge.createBase')}
      </Button>
    </div>
  </div>

  {#if error}
    <ErrorState title={t('resources.knowledge.requestFailed')} message={error} />
  {/if}

  {#if notice}
    <div class="rounded-md border border-status-done/30 bg-status-done-bg px-3 py-2 text-sm text-status-done">{notice}</div>
  {/if}

  <section class="rounded-xl border border-border bg-card p-4">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <h2 class="text-sm font-semibold text-foreground">内容检索</h2>
        <p class="mt-1 text-xs text-muted-foreground">搜索已索引文档的正文内容</p>
      </div>
      <form class="flex min-w-0 flex-1 gap-2 sm:max-w-xl" onsubmit={runContentSearch}>
        <label class="relative min-w-0 flex-1">
          <span class="sr-only">搜索知识内容</span>
          <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <input
            bind:value={contentSearchQuery}
            class="h-9 w-full rounded-md border border-border bg-input-background pl-9 pr-3 text-sm text-foreground outline-none placeholder:text-muted-foreground focus-visible:border-ring"
            type="search"
            aria-label="搜索知识内容"
          />
        </label>
        <button type="submit" class="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:pointer-events-none disabled:opacity-50" disabled={contentSearchLoading}>
          <Search class="h-4 w-4" />
          <span>{contentSearchLoading ? '检索中' : '检索'}</span>
        </button>
      </form>
    </div>
    {#if contentSearchQuery.trim()}
      <div class="mt-4 border-t border-border pt-3" aria-live="polite">
        {#if contentSearchError}
          <p class="text-sm text-destructive">{contentSearchError}</p>
        {:else if contentSearchLoading}
          <p class="text-sm text-muted-foreground">正在检索文档...</p>
        {:else if contentSearchResults.length === 0}
          <p class="text-sm text-muted-foreground">没有匹配的文档内容</p>
        {:else}
          <div class="divide-y divide-border">
            {#each contentSearchResults as result}
              <a href={`/resources/knowledge/${encodeURIComponent(result.knowledge_base_id)}`} class="block py-3 first:pt-0 last:pb-0 hover:bg-muted/40">
                <div class="flex items-center justify-between gap-3">
                  <span class="min-w-0 truncate text-sm font-medium text-foreground">{result.document_name || result.document_id}</span>
                  <span class="shrink-0 text-xs text-muted-foreground">{searchBaseName(result.knowledge_base_id)}</span>
                </div>
                {#if result.snippet}<p class="mt-1 line-clamp-2 text-xs leading-5 text-muted-foreground">{result.snippet}</p>{/if}
              </a>
            {/each}
          </div>
        {/if}
      </div>
    {/if}
  </section>

  {#if loading}
    <LoadingState title={t('nav.knowledge')} description={t('resources.knowledge.listDescription')} />
  {:else if bases.length === 0}
    <section class="rounded-xl border border-border bg-card p-6">
      <EmptyState title={t('resources.knowledge.empty')} description={t('resources.knowledge.empty.description')} />
      <div class="mt-4 flex justify-center">
        <Button href="/resources/knowledge/new">
          <Plus class="h-4 w-4" />
          {t('resources.knowledge.createBase')}
        </Button>
      </div>
    </section>
  {:else}
    <section class="overflow-hidden rounded-xl border border-border bg-card">
      <div class="flex flex-col gap-2 border-b border-border p-3 sm:flex-row sm:items-center sm:justify-between">
        <label class="relative min-w-0 flex-1">
          <span class="sr-only">{t('filter.search')}</span>
          <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <input
            bind:value={baseSearchQuery}
            class="h-9 w-full rounded-md border border-border bg-input-background pl-9 pr-9 text-sm text-foreground outline-none placeholder:text-muted-foreground focus-visible:border-ring"
            type="search"
            placeholder={t('filter.search')}
            aria-label={t('filter.search')}
          />
          {#if baseSearchQuery}
            <button
              type="button"
              class="absolute right-1 top-1/2 grid h-7 w-7 -translate-y-1/2 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground"
              aria-label={t('resources.memory.clearFilters')}
              title={t('resources.memory.clearFilters')}
              onclick={() => (baseSearchQuery = '')}
            >
              <X class="h-3.5 w-3.5" />
            </button>
          {/if}
        </label>
        <Tabs bind:value={resourceView} tabs={resourceViewTabs} selectionMaterial ariaLabel={t('resources.graph.viewMode')} />
      </div>
      {#if graphError && resourceView === 'graph'}
        <div class="border-b border-destructive/20 bg-destructive/5 px-4 py-3 text-xs text-destructive">{graphError}</div>
      {/if}
      {#if resourceView === 'graph'}
        <ResourceRelationshipGraph
          embedded
          title={t('resources.knowledge.graphTitle')}
          description={t('resources.knowledge.graphDescription')}
          nodes={relationshipGraph.nodes}
          links={relationshipGraph.links}
          legend={[
            { label: t('resources.knowledge.graphOwner'), color: '#cf6679' },
            { label: t('resources.knowledge.graphBase'), color: '#74a65d' },
            { label: t('resources.knowledge.graphDocument'), color: '#9bbf82' }
          ]}
          ariaLabel={t('resources.knowledge.graphTitle')}
          emptyLabel={t('resources.knowledge.graphEmpty')}
          openLabel={t('resources.graph.open')}
          selectHint={t('resources.graph.selectHint')}
          nodeCountLabel={t('resources.graph.nodes', { count: relationshipGraph.nodes.length })}
          relationCountLabel={t('resources.graph.relations', { count: relationshipGraph.links.length })}
          loading={graphLoading}
          loadingLabel={t('resources.graph.loading')}
        />
      {:else if visibleBases.length === 0}
        <div class="p-6 text-center text-sm text-muted-foreground">{t('resources.knowledge.noResults')}</div>
      {:else}
        <div class="hidden grid-cols-[minmax(260px,1fr)_120px_120px_110px_44px] gap-3 border-b border-border px-4 py-2 text-[11px] font-medium uppercase text-muted-foreground lg:grid">
          <div>{t('resources.knowledge.baseName')}</div>
          <div>{t('resources.knowledge.visibility')}</div>
          <div>{t('resources.knowledge.provider')}</div>
          <div>{t('resources.editor.status')}</div>
          <div class="text-right">{t('resources.action.delete')}</div>
        </div>
        <div class="divide-y divide-border">
        {#each visibleBases as base}
          <div
            class="grid gap-3 px-4 py-4 transition-colors hover:bg-muted/50 lg:grid-cols-[minmax(260px,1fr)_120px_120px_110px_44px] lg:items-center"
          >
            <a
              href={`/resources/knowledge/${encodeURIComponent(base.id)}`}
              class="contents group"
            >
              <div class="min-w-0 rounded-sm group-focus-visible:outline-none group-focus-visible:ring-1 group-focus-visible:ring-ring">
                <div class="flex min-w-0 items-center gap-2">
                  <Database class="h-4 w-4 shrink-0 text-brand" />
                  <span class="truncate text-[13px] font-medium text-foreground">{base.name}</span>
                  {#if base.is_default}
                    <Badge tone="info">{t('resources.knowledge.default')}</Badge>
                  {/if}
                </div>
                <div class="mt-1 line-clamp-1 text-xs text-muted-foreground">
                  {displayField(base.description)}
                </div>
              </div>
              <div>
                <span class="mr-2 text-xs font-medium text-muted-foreground lg:hidden">{t('resources.knowledge.visibility')}</span>
                <Badge tone={base.visibility === 'private' ? 'amber' : 'teal'}>
                  {knowledgeAccessLabel(base)}
                </Badge>
              </div>
              <div>
                <span class="mr-2 text-xs font-medium text-muted-foreground lg:hidden">{t('resources.knowledge.provider')}</span>
                <Badge tone={providerLabel(base) === 'pageindex' ? 'teal' : 'neutral'}>
                  {providerName(base)}
                </Badge>
              </div>
              <div>
                <span class="mr-2 text-xs font-medium text-muted-foreground lg:hidden">{t('resources.editor.status')}</span>
                <Badge tone={statusTone(base)}>{statusLabel(base.status)}</Badge>
              </div>
            </a>
            <div class="flex justify-end">
              <button
                type="button"
                class="inline-flex h-9 w-9 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50"
                aria-label={t('resources.knowledge.deleteNamed', { name: base.name || t('nav.knowledge') })}
                title={t('resources.knowledge.deleteNamed', { name: base.name || t('nav.knowledge') })}
                disabled={deletingBaseId !== '' && deletingBaseId !== base.id}
                aria-busy={deletingBaseId === base.id}
                onclick={(event) => deleteBase(event, base)}
              >
                <Trash2 class="h-4 w-4" />
              </button>
            </div>
          </div>
        {/each}
        </div>
      {/if}
    </section>
  {/if}
</section>
