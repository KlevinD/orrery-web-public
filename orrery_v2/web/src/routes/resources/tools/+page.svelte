<script lang="ts">
  import {
    Search,
    Wrench,
    ShieldAlert,
    CheckCircle2,
    XCircle,
    BarChart3,
    Server,
    Layers,
    Play
  } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import ToolCallDialog from '#lib/components/resources/ToolCallDialog.svelte'
  import KpiCard from '#lib/components/shared/KpiCard.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { combineToolPages, useResourceToolsInfiniteQuery, useToolCallMutation } from '#lib/queries/resources'
  import { mapToolGatewayView } from '#lib/mappers/resource'
  import type { ApiClientError } from '#lib/api/client'
  import type { ToolCallResultDTO } from '#lib/types/api'
  import type { ToolViewModel } from '#lib/types/view-models'

  let search = $state('')
  let category = $state('')
  let riskLevel = $state('')
  let enabled = $state('')
  let resourceId = $state('')
  let loadMoreMarker = $state<HTMLElement | undefined>()
  let selectedToolId = $state('')
  let selectedToolForCall = $state<ToolViewModel | undefined>()
  let toolCallResult = $state<ToolCallResultDTO | undefined>()
  let toolCallError = $state('')
  let t = $derived(createTranslator($locale))

  const toolsQuery = useResourceToolsInfiniteQuery(() => ({
    search,
    category,
    riskLevel,
    enabled: enabled === '' ? undefined : enabled,
    resourceId
  }))
  const toolCallMutation = useToolCallMutation()
  const toolsData = $derived(mapToolGatewayView(combineToolPages(toolsQuery.data?.pages ?? [])))
  const currentTools = $derived(toolsData.tools)
  const selectedTool = $derived(currentTools.find((tool) => tool.id === selectedToolId) ?? currentTools[0])

  function displayField(value: string | number | string[] | undefined) {
    if (Array.isArray(value)) return value.length > 0 ? value.join(', ') : t('workspace.notProvided')
    if (typeof value === 'number') return String(value)
    return value && value !== 'Not provided' ? value : t('workspace.notProvided')
  }

  function loadMore() {
    if (toolsQuery.hasNextPage && !toolsQuery.isFetchingNextPage) {
      toolsQuery.fetchNextPage()
    }
  }

  function openToolCall(tool: ToolViewModel) {
    selectedToolForCall = tool
    toolCallResult = undefined
    toolCallError = ''
  }

  function schemaPreview(schema: Record<string, unknown> | undefined) {
    if (!schema || Object.keys(schema).length === 0) return t('workspace.notProvided')
    return JSON.stringify(schema, null, 2)
  }

  function closeToolCall() {
    selectedToolForCall = undefined
    toolCallResult = undefined
    toolCallError = ''
  }

  async function submitToolCall(argumentsValue: Record<string, unknown>) {
    if (!selectedToolForCall) return
    toolCallError = ''
    try {
      toolCallResult = await toolCallMutation.mutateAsync({
        tool_id: selectedToolForCall.id,
        arguments: argumentsValue
      })
    } catch (err) {
      const error = err as ApiClientError
      toolCallError = error.message || t('resources.message.toolCallFailed')
    }
  }

  $effect(() => {
    if (!loadMoreMarker || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) loadMore()
    }, { rootMargin: '240px' })
    observer.observe(loadMoreMarker)
    return () => observer.disconnect()
  })
</script>

<svelte:head>
  <title>{t('nav.tools')} | Orrery</title>
</svelte:head>

<div class="page-shell max-w-[1040px] space-y-5">
  <header class="page-header">
    <div>
      <h1 class="page-title">{t('nav.tools')}</h1>
      <p class="page-description">
        {t('nav.tools.description')}
      </p>
    </div>
  </header>

  {#if toolsQuery.isLoading}
    <LoadingState title={t('state.loadingTools')} description={t('state.loadingToolsDescription')} />
  {:else if toolsQuery.isError}
    {@const error = toolsQuery.error as ApiClientError}
    <ErrorState title={t('state.errorTools')} message={error.message} correlationId={error.correlationId} />
  {:else if toolsQuery.data}
    <section class="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
      <KpiCard label={t('kpi.total')} value={toolsData.summary.total} icon={Wrench} />
      <KpiCard label={t('kpi.enabled')} value={toolsData.summary.enabled} icon={CheckCircle2} />
      <KpiCard label={t('kpi.disabled')} value={toolsData.summary.disabled} icon={XCircle} />
      <KpiCard label={t('resources.kpi.highRisk')} value={toolsData.summary.highRisk} icon={ShieldAlert} />
      <KpiCard label={t('resources.filter.usage')} value={toolsData.summary.usageCount} icon={BarChart3} />
    </section>

    <section class="flex flex-wrap items-center gap-3">
      <div class="relative min-w-[180px] flex-1 sm:max-w-xs">
        <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground/60" />
        <Input value={search} placeholder={t('resources.filter.searchTools')} oninput={(e) => (search = e.currentTarget.value)} class="pl-9" />
      </div>
      <div class="w-36">
        <Input value={category} placeholder={t('resources.filter.category')} oninput={(e) => (category = e.currentTarget.value)} />
      </div>
      <div class="w-36">
        <Select
          value={riskLevel}
          options={[
            { label: t('resources.filter.allRisk'), value: '' },
            { label: t('risk.high'), value: 'high' },
            { label: t('risk.medium'), value: 'medium' },
            { label: t('risk.low'), value: 'low' }
          ]}
          onchange={(e) => (riskLevel = e.currentTarget.value)}
        />
      </div>
      <div class="w-36">
        <Select
          value={enabled}
          options={[
            { label: t('resources.filter.allStatus'), value: '' },
            { label: t('kpi.enabled'), value: 'true' },
            { label: t('kpi.disabled'), value: 'false' }
          ]}
          onchange={(e) => (enabled = e.currentTarget.value)}
        />
      </div>
      <div class="w-36">
        <Input value={resourceId} placeholder={t('filter.resourceId')} oninput={(e) => (resourceId = e.currentTarget.value)} />
      </div>
    </section>

    <section>
      {#if currentTools.length === 0}
        <EmptyState title={t('resources.tools.empty')} description={t('resources.tools.empty.description')} />
      {:else}
        <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
          <div class="self-start overflow-hidden rounded-xl border border-border bg-card">
            <div class="divide-y divide-border/70">
              {#each currentTools as tool (tool.id)}
                <button
                  type="button"
                  class="flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors {selectedTool?.id === tool.id ? 'selection-wash' : 'hover:bg-muted/30'}"
                  onclick={() => (selectedToolId = tool.id)}
                >
                  <span class="flex min-w-0 items-center gap-2.5">
                    <span class="grid h-8 w-8 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground"><Wrench class="h-4 w-4" /></span>
                    <span class="min-w-0">
                      <span class="block truncate font-mono text-[13px] text-foreground">{tool.name}</span>
                      <span class="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11.5px] text-muted-foreground">
                        {#if tool.serverToolId}<Server class="h-3 w-3 shrink-0" />{:else}<Layers class="h-3 w-3 shrink-0" />{/if}
                        <span class="truncate">{displayField(tool.serverToolId || tool.source || tool.kind)}</span>
                      </span>
                    </span>
                  </span>
                  <span class="flex shrink-0 items-center gap-1.5 text-xs {tool.enabled ? 'text-status-done' : 'text-muted-foreground'}">
                    <span class="h-2 w-2 rounded-full bg-current"></span>{tool.statusLabel}
                  </span>
                </button>
              {/each}
            </div>
          </div>

          {#if selectedTool}
            <aside class="self-start rounded-xl border border-border bg-card p-4">
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0">
                  <h2 class="truncate font-mono text-sm font-medium text-foreground">{selectedTool.name}</h2>
                  <p class="mt-0.5 truncate text-[11.5px] text-muted-foreground">{displayField(selectedTool.kind)} · {displayField(selectedTool.category)}</p>
                </div>
                <Badge tone={selectedTool.riskTone}>{selectedTool.riskLevel}</Badge>
              </div>
              <p class="mt-3 border-t border-border pt-3 text-[12.5px] leading-5 text-muted-foreground">{displayField(selectedTool.description)}</p>

              <div class="mt-3">
                <p class="text-[11px] text-muted-foreground">Input</p>
                <pre class="subtle-scrollbar mt-1.5 max-h-40 overflow-auto whitespace-pre-wrap rounded-md bg-muted/45 p-2.5 font-mono text-[11px] leading-5 text-foreground">{schemaPreview(selectedTool.inputSchema)}</pre>
              </div>
              <div class="mt-3">
                <p class="text-[11px] text-muted-foreground">Output</p>
                <pre class="subtle-scrollbar mt-1.5 max-h-32 overflow-auto whitespace-pre-wrap rounded-md bg-muted/45 p-2.5 font-mono text-[11px] leading-5 text-foreground">{schemaPreview(selectedTool.outputSchema)}</pre>
              </div>

              <dl class="mt-3 grid grid-cols-2 gap-3 border-t border-border pt-3 text-xs">
                <div><dt class="text-muted-foreground">{t('resources.tools.usage')}</dt><dd class="mt-0.5 font-medium text-foreground">{selectedTool.usageCount}</dd></div>
                <div><dt class="text-muted-foreground">{t('resources.tools.boundAgents')}</dt><dd class="mt-0.5 font-medium text-foreground">{selectedTool.boundAgentCount}</dd></div>
              </dl>

              <Button class="mt-4 w-full" onclick={() => openToolCall(selectedTool)}>
                <Play class="h-3.5 w-3.5" />{t('resources.tools.call')}
              </Button>
            </aside>
          {/if}
        </div>
        <div bind:this={loadMoreMarker} class="h-px"></div>
        {#if toolsQuery.isFetchingNextPage}
          <div class="py-4 text-center text-xs text-muted-foreground">
            {t('state.loadingTools')}
          </div>
        {:else if !toolsQuery.hasNextPage}
          <div class="py-4 text-center text-xs text-muted-foreground">
            {t('state.noMoreItems')}
          </div>
        {/if}
      {/if}
    </section>
  {/if}
</div>

<ToolCallDialog
  open={Boolean(selectedToolForCall)}
  tool={selectedToolForCall}
  busy={toolCallMutation.isPending}
  result={toolCallResult}
  error={toolCallError}
  onclose={closeToolCall}
  onsubmit={submitToolCall}
/>
