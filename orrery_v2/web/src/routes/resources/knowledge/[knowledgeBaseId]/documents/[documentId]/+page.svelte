<script lang="ts">
  import { onMount, tick } from 'svelte'
  import { ArrowLeft, FileText, RefreshCw } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Progress from '#lib/components/ui/Progress.svelte'
  import { locale, createTranslator } from '#lib/i18n'
  import { getKnowledgeBase, getKnowledgeDocumentProcessingResult } from '#lib/queries/knowledge'
  import type { DocumentPagePreviewDTO, KnowledgeBaseDTO, KnowledgeDocumentProcessingResultDTO } from '#lib/types/api/knowledge'

  interface Props {
    data: { knowledgeBaseId: string; documentId: string }
  }

  let { data }: Props = $props()
  const t = $derived(createTranslator($locale))
  const knowledgeBaseId = $derived(data.knowledgeBaseId)
  const documentId = $derived(data.documentId)

  let base = $state<KnowledgeBaseDTO | undefined>()
  let preview = $state<KnowledgeDocumentProcessingResultDTO | undefined>()
  let loading = $state(true)
  let refreshing = $state(false)
  let polling = $state(false)
  let error = $state('')

  onMount(() => {
    void loadPreview()
    const timer = window.setInterval(() => {
      if (shouldPollPreview()) {
        void refreshPreview({ silent: true })
      }
    }, 4000)
    return () => {
      window.clearInterval(timer)
    }
  })

  async function loadPreview() {
    loading = true
    error = ''
    try {
      const [nextBase, nextPreview] = await Promise.all([
        getKnowledgeBase(knowledgeBaseId),
        getKnowledgeDocumentProcessingResult(knowledgeBaseId, documentId, { pages: previewRequestPages() })
      ])
      base = nextBase
      preview = nextPreview
      void scrollAnchorIntoView()
    } catch (err) {
      error = errorMessage(err)
    } finally {
      loading = false
    }
  }

  async function refreshPreview(options: { silent?: boolean } = {}) {
    if (refreshing || polling) {
      return
    }
    if (options.silent) {
      polling = true
    } else {
      refreshing = true
      error = ''
    }
    try {
      preview = await getKnowledgeDocumentProcessingResult(knowledgeBaseId, documentId, { pages: previewRequestPages() })
      if (!options.silent) {
        void scrollAnchorIntoView()
      }
    } catch (err) {
      if (!options.silent) {
        error = errorMessage(err)
      }
    } finally {
      if (options.silent) {
        polling = false
      } else {
        refreshing = false
      }
    }
  }

  function documentName() {
    return preview?.document?.filename || t('resources.knowledge.unnamedDocument')
  }

  function documentStatusLabel(value?: string) {
    if (value === 'parsing') return t('resources.knowledge.documentStatus.parsing')
    if (value === 'chunking') return t('resources.knowledge.documentStatus.chunking')
    if (value === 'embedding') return t('resources.knowledge.documentStatus.embedding')
    if (value === 'indexed') return t('resources.knowledge.documentStatus.indexed')
    if (value === 'failed') return t('resources.knowledge.documentStatus.failed')
    if (value === 'deleting') return t('resources.knowledge.documentStatus.deleting')
    return t('resources.knowledge.documentStatus.uploaded')
  }

  function processingStageLabel(value?: string) {
    if (value === 'queued') return t('resources.knowledge.stage.queued')
    if (value === 'processing') return t('resources.knowledge.stage.processing')
    if (value === 'parsing') return t('resources.knowledge.stage.parsing')
    if (value === 'chunking') return t('resources.knowledge.stage.chunking')
    if (value === 'embedding') return t('resources.knowledge.stage.embedding')
    if (value === 'indexed') return t('resources.knowledge.stage.indexed')
    if (value === 'failed') return t('resources.knowledge.stage.failed')
    return value ?? t('workspace.notProvided')
  }

  function progressValue(value?: number) {
    if (!value || value < 0) {
      return 0
    }
    if (value > 100) {
      return 100
    }
    return value
  }

  function shouldPollPreview() {
    const status = preview?.status || preview?.document?.status || ''
    const progress = progressValue(preview?.progress ?? preview?.document?.processing_progress)
    return ['uploaded', 'parsing', 'chunking', 'embedding'].includes(status) || (progress > 0 && progress < 100)
  }

  function previewPages() {
    return preview?.page_previews?.length ? preview.page_previews : (preview?.pages ?? [])
  }

  function previewRequestPages() {
    const pages = searchParam('pages')
    return pages || '1'
  }

  function anchorQuery() {
    return searchParam('anchor')
  }

  function searchParam(name: string) {
    if (typeof window === 'undefined') {
      return ''
    }
    return new URLSearchParams(window.location.search).get(name)?.trim() ?? ''
  }

  function isAnchorTarget(page: Record<string, unknown> | DocumentPagePreviewDTO) {
    const anchor = anchorQuery()
    return anchor !== '' && pageProviderNodeId(page) === anchor
  }

  function pageProviderNodeId(page: Record<string, unknown> | DocumentPagePreviewDTO) {
    const rawPage = page as Record<string, unknown>
    return firstStringValue(
      rawPage.provider_node_id,
      rawPage.providerNodeID,
      rawPage.providerNodeId,
      rawPage.node_id,
      rawPage.nodeId,
      rawPage.id
    )
  }

  async function scrollAnchorIntoView() {
    if (!anchorQuery() || typeof document === 'undefined') {
      return
    }
    await tick()
    document.querySelector('[data-anchor-highlighted="true"]')?.scrollIntoView({ block: 'center' })
  }

  function pageTitle(page: Record<string, unknown> | DocumentPagePreviewDTO, index: number) {
    const rawPage = page as Record<string, unknown>
    const raw = page.page_number || rawPage.title || rawPage.heading || rawPage.name || rawPage.page || rawPage.page_number
    return raw ? String(raw) : t('resources.knowledge.previewPage', { number: index + 1 })
  }

  function pageText(page: Record<string, unknown> | DocumentPagePreviewDTO) {
    const rawPage = page as Record<string, unknown>
    const raw = page.text ?? rawPage.content ?? rawPage.markdown ?? rawPage.body
    return typeof raw === 'string' ? raw.trim() : ''
  }

  function firstStringValue(...values: unknown[]) {
    for (const value of values) {
      if (typeof value === 'string' && value.trim() !== '') {
        return value.trim()
      }
      if (typeof value === 'number' && Number.isFinite(value)) {
        return String(value)
      }
    }
    return ''
  }

  function sanitizedJson(value: unknown) {
    return JSON.stringify(sanitizeProcessingValue(value), null, 2)
  }

  function sanitizeProcessingValue(value: unknown): unknown {
    if (Array.isArray(value)) {
      return value.map((item) => sanitizeProcessingValue(item))
    }
    if (!value || typeof value !== 'object') {
      return value
    }
    const out: Record<string, unknown> = {}
    for (const [key, next] of Object.entries(value as Record<string, unknown>)) {
      if (isInternalIdentifierKey(key)) {
        continue
      }
      out[key] = sanitizeProcessingValue(next)
    }
    return out
  }

  function isInternalIdentifierKey(key: string) {
    return key === 'id' || key.endsWith('_id') || key.endsWith('ID') || key.endsWith('Id')
  }

  function errorMessage(err: unknown) {
    if (err instanceof Error) {
      return err.message
    }
    return String(err)
  }
</script>

<svelte:head>
  <title>{documentName()} · {t('resources.knowledge.documentPreview')} · Orrery</title>
</svelte:head>

<section class="page-shell max-w-[1040px] space-y-5">
  <header class="flex flex-col gap-3 border-b border-border pb-4 lg:flex-row lg:items-end lg:justify-between">
    <div class="min-w-0">
      <Button href={`/resources/knowledge/${encodeURIComponent(knowledgeBaseId)}`} variant="ghost" size="sm" class="mb-2">
        <ArrowLeft class="h-3.5 w-3.5" />
        {t('resources.knowledge.backToKnowledgeBase')}
      </Button>
      <div class="flex min-w-0 flex-wrap items-center gap-2">
        <h1 class="page-title truncate">{documentName()}</h1>
        {#if preview?.status}
          <Badge tone={preview.status === 'indexed' ? 'success' : preview.status === 'failed' ? 'rose' : 'neutral'}>
            {documentStatusLabel(preview.status)}
          </Badge>
        {/if}
      </div>
      <p class="mt-1 max-w-3xl text-[13px] leading-5 text-muted-foreground">
        {base?.name ?? t('nav.knowledge')}
      </p>
    </div>
    <Button variant="outline" onclick={() => refreshPreview()} disabled={loading || refreshing}>
      <RefreshCw class={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />
      {t('resources.knowledge.reloading')}
    </Button>
  </header>

  {#if error}
    <ErrorState title={t('resources.knowledge.requestFailed')} message={error} />
  {/if}

  {#if loading}
    <LoadingState title={t('resources.knowledge.documentPreview')} description={t('resources.knowledge.previewLoading')} />
  {:else if preview}
    {#if preview.status !== 'indexed' || progressValue(preview.progress) < 100}
      <section class="rounded-xl border border-border bg-card p-4">
        <div class="mb-2 flex items-center justify-between text-sm text-muted-foreground">
          <span>{processingStageLabel(preview.stage || preview.status)}</span>
          <span>{progressValue(preview.progress)}%</span>
        </div>
        <Progress value={progressValue(preview.progress)} />
        {#if preview.error_message}
          <div class="mt-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {preview.error_message}
          </div>
        {/if}
      </section>
    {/if}

    {@const pages = previewPages()}
    {#if pages.length}
      <main class="space-y-4">
        {#each pages as page, index}
          <article
            data-anchor-highlighted={isAnchorTarget(page) ? 'true' : undefined}
            class={`rounded-xl border p-4 ${isAnchorTarget(page) ? 'border-brand bg-brand/5 ring-1 ring-brand/30' : 'border-border bg-card'}`}
          >
            <div class="mb-3 flex items-center gap-2 text-sm font-semibold text-foreground">
              <FileText class="h-4 w-4 text-brand" />
              {pageTitle(page, index)}
            </div>
            {#if pageText(page)}
              <div class="whitespace-pre-wrap text-sm leading-7 text-foreground">{pageText(page)}</div>
            {:else}
              <pre class="subtle-scrollbar max-h-[32rem] overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/40 p-3 font-mono text-xs leading-5 text-foreground">{sanitizedJson(page)}</pre>
            {/if}
          </article>
        {/each}
      </main>
    {:else}
      <EmptyState title={t('resources.knowledge.documentPreviewEmpty')} description={t('resources.knowledge.documentPreviewEmptyDescription')} />
    {/if}

    {#if preview.structure?.length}
      <section class="rounded-xl border border-border bg-card p-4">
        <div class="mb-3 text-sm font-semibold text-foreground">{t('resources.knowledge.processingStructure')}</div>
        <pre class="subtle-scrollbar max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/40 p-3 font-mono text-xs leading-5 text-foreground">{sanitizedJson(preview.structure)}</pre>
      </section>
    {/if}
  {/if}
</section>
