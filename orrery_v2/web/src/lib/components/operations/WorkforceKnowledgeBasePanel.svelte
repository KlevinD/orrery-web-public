<script lang="ts">
  import { AlertCircle, ArrowUpRight, Database, FileText, LoaderCircle } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import {
    getKnowledgeBase,
    listKnowledgeBases,
    listKnowledgeBindings,
    listKnowledgeDocuments
  } from '#lib/queries/knowledge'
  import type {
    KnowledgeBaseDTO,
    KnowledgeBindingPageDTO,
    KnowledgeDocumentDTO,
    KnowledgeDocumentPageDTO
  } from '#lib/types/api/knowledge'

  interface Props {
    knowledgeBaseId?: string
    memberName: string
    ownerId: string
    ownerType?: 'agent' | 'human'
    oncountchange?: (count: number) => void
  }

  let {
    knowledgeBaseId = '',
    memberName,
    ownerId,
    ownerType = 'agent',
    oncountchange
  }: Props = $props()
  let bases = $state<KnowledgeBaseDTO[]>([])
  let selectedBaseId = $state('')
  let documents = $state<KnowledgeDocumentPageDTO | undefined>()
  let bindings = $state<KnowledgeBindingPageDTO | undefined>()
  let catalogLoading = $state(false)
  let detailLoading = $state(false)
  let catalogError = $state('')
  let detailError = $state('')
  let t = $derived(createTranslator($locale))
  let normalizedId = $derived(knowledgeBaseId.trim())
  let normalizedOwnerId = $derived(ownerId.trim())
  let selectedBase = $derived(bases.find((base) => base.id === selectedBaseId))

  $effect(() => {
    const currentOwnerId = normalizedOwnerId
    const currentId = normalizedId
    const currentOwnerType = ownerType
    const controller = new AbortController()

    bases = []
    selectedBaseId = ''
    documents = undefined
    bindings = undefined
    catalogError = ''
    detailError = ''
    oncountchange?.(0)
    if (!currentOwnerId && !currentId) return

    catalogLoading = true
    const catalogRequest = currentOwnerId
      ? listKnowledgeBases({ ownerType: currentOwnerType, ownerId: currentOwnerId }, { signal: controller.signal })
      : Promise.resolve({ items: [], total: 0 })
    const directRequest = currentId
      ? getKnowledgeBase(currentId, { signal: controller.signal })
      : Promise.resolve(undefined)

    void Promise.all([catalogRequest, directRequest]).then(([catalog, direct]) => {
      if (controller.signal.aborted) return
      const unique = new Map((catalog.items ?? []).map((base) => [base.id, base]))
      if (direct) unique.set(direct.id, direct)
      const nextBases = Array.from(unique.values())
      bases = nextBases
      selectedBaseId = nextBases.some((base) => base.id === currentId) ? currentId : (nextBases[0]?.id ?? '')
      oncountchange?.(nextBases.length)
    }).catch((reason: unknown) => {
      if (!controller.signal.aborted) catalogError = reason instanceof Error ? reason.message : String(reason)
    }).finally(() => {
      if (!controller.signal.aborted) catalogLoading = false
    })

    return () => controller.abort()
  })

  $effect(() => {
    const currentId = selectedBaseId
    documents = undefined
    bindings = undefined
    detailError = ''
    if (!currentId) return

    const controller = new AbortController()
    detailLoading = true
    void Promise.all([
      listKnowledgeDocuments(currentId, { signal: controller.signal }),
      listKnowledgeBindings(currentId, { signal: controller.signal })
    ]).then(([nextDocuments, nextBindings]) => {
      if (controller.signal.aborted) return
      documents = nextDocuments
      bindings = nextBindings
    }).catch((reason: unknown) => {
      if (!controller.signal.aborted) detailError = reason instanceof Error ? reason.message : String(reason)
    }).finally(() => {
      if (!controller.signal.aborted) detailLoading = false
    })

    return () => controller.abort()
  })

  function statusLabel(value?: string) {
    if (value === 'disabled') return t('resources.knowledge.status.disabled')
    if (value === 'deleting') return t('resources.knowledge.status.deleting')
    return t('resources.knowledge.status.active')
  }

  function statusTone(value?: string): 'success' | 'warning' {
    return value === 'disabled' || value === 'deleting' ? 'warning' : 'success'
  }

  function documentStatusLabel(value?: string) {
    switch (value) {
      case 'uploaded': return t('resources.knowledge.documentStatus.uploaded')
      case 'parsing': return t('resources.knowledge.documentStatus.parsing')
      case 'chunking': return t('resources.knowledge.documentStatus.chunking')
      case 'embedding': return t('resources.knowledge.documentStatus.embedding')
      case 'indexed': return t('resources.knowledge.documentStatus.indexed')
      case 'failed': return t('resources.knowledge.documentStatus.failed')
      case 'deleting': return t('resources.knowledge.documentStatus.deleting')
      default: return value || t('people.notConfigured')
    }
  }

  function documentTone(value?: string): 'success' | 'warning' | 'danger' | 'neutral' {
    if (value === 'indexed') return 'success'
    if (value === 'failed') return 'danger'
    if (value === 'parsing' || value === 'chunking' || value === 'embedding' || value === 'deleting') return 'warning'
    return 'neutral'
  }

  function providerLabel(base: KnowledgeBaseDTO) {
    if (base.provider === 'pageindex') return t('resources.knowledge.provider.pageindex')
    if (base.provider === 'local' || !base.provider) return t('resources.knowledge.provider.local')
    return base.provider_display_name || base.provider
  }

  function visibilityLabel(value?: string) {
    if (value === 'private') return t('resources.knowledge.visibility.personal')
    if (value === 'public') return t('resources.knowledge.visibility.public')
    return value || t('people.notConfigured')
  }

  function filename(document: KnowledgeDocumentDTO) {
    return document.filename || document.id
  }

  function formatBytes(value?: number) {
    if (!value) return ''
    if (value < 1024) return `${value} B`
    if (value < 1024 * 1024) return `${Math.round(value / 1024)} KB`
    return `${(value / 1024 / 1024).toFixed(1)} MB`
  }
</script>

<section data-workforce-knowledge-base>
  {#if catalogLoading}
    <div class="flex min-h-40 items-center justify-center gap-2 text-sm text-muted-foreground">
      <LoaderCircle class="h-4 w-4 animate-spin" />
      {t('people.knowledgeBaseLoading')}
    </div>
  {:else if catalogError}
    <div class="flex min-h-40 flex-col items-center justify-center text-center">
      <AlertCircle class="h-6 w-6 text-destructive/75" />
      <h3 class="mt-3 text-sm font-medium text-foreground">{t('people.knowledgeBaseLoadFailed')}</h3>
      <p class="mt-1 max-w-md text-xs leading-5 text-muted-foreground">{catalogError}</p>
    </div>
  {:else if bases.length === 0}
    <div class="flex min-h-40 flex-col items-center justify-center text-center">
      <Database class="h-6 w-6 text-muted-foreground/55" />
      <h3 class="mt-3 text-sm font-medium text-foreground">{t('people.knowledgeBaseUnbound')}</h3>
      <p class="mt-1 text-xs text-muted-foreground">{t('people.knowledgeBaseUnboundDescription', { name: memberName })}</p>
    </div>
  {:else}
    <div class="grid gap-5 lg:grid-cols-[210px_minmax(0,1fr)]">
      <aside class="min-w-0 lg:border-r lg:border-border lg:pr-4">
        <div class="mb-2 flex items-center justify-between gap-2">
          <h3 class="text-[12px] font-medium text-foreground">{t('people.knowledgeBaseBound')}</h3>
          <span class="text-[10.5px] tabular-nums text-muted-foreground">{bases.length}</span>
        </div>
        <div class="grid gap-1 sm:grid-cols-2 lg:grid-cols-1">
          {#each bases as base (base.id)}
            <button
              type="button"
              data-knowledge-base-id={base.id}
              aria-pressed={selectedBaseId === base.id}
              class="min-w-0 rounded-lg px-2.5 py-2.5 text-left transition-colors {selectedBaseId === base.id ? 'selection-wash' : 'hover:bg-muted/35'}"
              onclick={() => (selectedBaseId = base.id)}
            >
              <span class="flex min-w-0 items-center gap-2">
                <span class="h-1.5 w-1.5 shrink-0 rounded-full {base.status === 'active' || !base.status ? 'bg-status-done' : 'bg-muted-foreground/45'}"></span>
                <span class="truncate text-[12.5px] font-medium text-foreground">{base.name || base.id}</span>
              </span>
              <span class="mt-1 block truncate pl-3.5 text-[10.5px] text-muted-foreground">{base.description || providerLabel(base)}</span>
            </button>
          {/each}
        </div>
      </aside>

      <div class="min-w-0" data-selected-knowledge-base={selectedBase?.id}>
        {#if selectedBase}
          <div class="flex flex-col gap-5">
            <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
              <div class="flex min-w-0 gap-3">
                <span class="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-brand/10 text-brand">
                  <Database class="h-4 w-4" />
                </span>
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-2">
                    <h3 class="truncate text-sm font-semibold text-foreground">{selectedBase.name || selectedBase.id}</h3>
                    <Badge tone={statusTone(selectedBase.status)}>{statusLabel(selectedBase.status)}</Badge>
                    {#if selectedBase.is_default}<Badge tone="info">{t('resources.knowledge.default')}</Badge>{/if}
                  </div>
                  <p class="mt-1 line-clamp-2 text-xs leading-5 text-muted-foreground">{selectedBase.description || t('people.notConfigured')}</p>
                  <p class="mt-1 truncate font-mono text-[10.5px] text-muted-foreground/65">{selectedBase.id}</p>
                </div>
              </div>
              <Button href={`/resources/knowledge/${encodeURIComponent(selectedBase.id)}`} variant="outline" size="sm" class="shrink-0">
                {t('people.manageKnowledgeBase')}
                <ArrowUpRight class="h-3.5 w-3.5" />
              </Button>
            </div>

            {#if detailLoading}
              <div class="flex min-h-24 items-center justify-center gap-2 text-xs text-muted-foreground">
                <LoaderCircle class="h-4 w-4 animate-spin" />
                {t('people.knowledgeBaseLoading')}
              </div>
            {:else if detailError}
              <p class="rounded-md bg-destructive/5 px-3 py-2 text-xs leading-5 text-destructive">{detailError}</p>
            {:else}
              <dl class="grid grid-cols-2 gap-x-4 gap-y-3 rounded-xl bg-muted/55 px-4 py-3 sm:grid-cols-4">
                <div><dt class="text-[10.5px] text-muted-foreground">{t('resources.knowledge.documents')}</dt><dd class="mt-0.5 text-sm font-semibold tabular-nums text-foreground">{documents?.total ?? 0}</dd></div>
                <div><dt class="text-[10.5px] text-muted-foreground">{t('people.knowledgeBaseBindings')}</dt><dd class="mt-0.5 text-sm font-semibold tabular-nums text-foreground">{bindings?.total ?? 0}</dd></div>
                <div><dt class="text-[10.5px] text-muted-foreground">{t('resources.knowledge.provider')}</dt><dd class="mt-0.5 truncate text-[12.5px] text-foreground">{providerLabel(selectedBase)}</dd></div>
                <div><dt class="text-[10.5px] text-muted-foreground">{t('resources.knowledge.visibility')}</dt><dd class="mt-0.5 truncate text-[12.5px] text-foreground">{visibilityLabel(selectedBase.visibility)}</dd></div>
              </dl>

              <section>
                <div class="flex items-center justify-between gap-3 border-b border-border pb-2.5">
                  <h4 class="text-[12.5px] font-medium text-foreground">{t('people.knowledgeBaseRecentDocuments')}</h4>
                  <span class="text-[10.5px] text-muted-foreground">{t('people.knowledgeBaseDocumentCount', { count: documents?.total ?? 0 })}</span>
                </div>
                {#if documents?.items.length}
                  <div class="divide-y divide-border/70">
                    {#each documents.items.slice(0, 5) as document (document.id)}
                      <a
                        href={`/resources/knowledge/${encodeURIComponent(selectedBase.id)}/documents/${encodeURIComponent(document.id)}`}
                        class="flex min-w-0 items-center gap-3 py-3 transition-colors hover:bg-muted/25"
                      >
                        <FileText class="h-4 w-4 shrink-0 text-muted-foreground" />
                        <span class="min-w-0 flex-1">
                          <span class="block truncate text-[12.5px] text-foreground">{filename(document)}</span>
                          <span class="mt-0.5 block text-[10.5px] text-muted-foreground">{formatBytes(document.size_bytes)}</span>
                        </span>
                        <Badge tone={documentTone(document.status)}>{documentStatusLabel(document.status)}</Badge>
                      </a>
                    {/each}
                  </div>
                {:else}
                  <p class="py-6 text-center text-xs text-muted-foreground">{t('resources.knowledge.noDocuments')}</p>
                {/if}
              </section>
            {/if}
          </div>
        {/if}
      </div>
    </div>
  {/if}
</section>
