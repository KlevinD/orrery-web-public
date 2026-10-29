<script lang="ts">
  import {
    ArrowDownUp,
    ChevronDown,
    CheckCircle2,
    ChevronRight,
    ClipboardCheck,
    Lightbulb,
    ListTodo,
    LoaderCircle,
    Rocket,
    Search
  } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import type {
    WorkCenterItem,
    WorkCenterItemKind,
    WorkCenterPhase
  } from './work-center'

  interface Props {
    items: WorkCenterItem[]
    loading?: boolean
    errorMessage?: string
    kindFilter?: WorkCenterItemKind | 'all'
    embedded?: boolean
    searchQuery?: string
    serverSearch?: boolean
    hasMore?: boolean
    loadingMore?: boolean
    total?: number
    onLoadMore?: () => void
  }

  type PhaseFilter = WorkCenterPhase | 'all'
  type KindFilter = WorkCenterItemKind | 'all'
  type SortMode = 'updated' | 'progress' | 'priority'

  let {
    items, loading = false, errorMessage = '', kindFilter, embedded = false,
    searchQuery = $bindable(''), serverSearch = false, hasMore = false,
    loadingMore = false, total, onLoadMore
  }: Props = $props()
  let t = $derived(createTranslator($locale))
  let selectedPhase = $state<PhaseFilter>('all')
  let selectedKind = $state<KindFilter>('all')
  const effectiveKind = $derived(kindFilter ?? selectedKind)
  let sortMode = $state<SortMode>('updated')

  $effect(() => {
    if (serverSearch) {
      searchQuery
      selectedPhase = 'all'
    }
  })

  const stages = [
    { phase: 'explore', label: 'workcenter.stage.explore', icon: Lightbulb },
    { phase: 'plan', label: 'workcenter.stage.plan', icon: ListTodo },
    { phase: 'mission', label: 'workcenter.stage.mission', icon: Rocket },
    { phase: 'review', label: 'workcenter.stage.review', icon: ClipboardCheck },
    { phase: 'done', label: 'workcenter.stage.done', icon: CheckCircle2 }
  ] as const

  const kinds = [
    { kind: 'all', label: 'workcenter.source.all' },
    { kind: 'idea', label: 'workcenter.source.idea' },
    { kind: 'todo', label: 'workcenter.source.todo' },
    { kind: 'mission', label: 'workcenter.source.mission' }
  ] as const

  const stageCounts = $derived.by(() => {
    const counts: Record<WorkCenterPhase, number> = { explore: 0, plan: 0, mission: 0, review: 0, done: 0 }
    for (const item of items) {
      if (effectiveKind === 'all' || item.kind === effectiveKind) counts[item.phase] += 1
    }
    return counts
  })

  const filteredItems = $derived.by(() => {
    const query = searchQuery.trim().toLocaleLowerCase($locale)
    const result = items.filter((item) => {
      if (selectedPhase !== 'all' && item.phase !== selectedPhase) return false
      if (effectiveKind !== 'all' && item.kind !== effectiveKind) return false
      if (serverSearch || !query) return true
      return `${item.title} ${item.summary} ${item.owner} ${item.missionId ?? ''}`
        .toLocaleLowerCase($locale)
        .includes(query)
    })
    return result.toSorted((left, right) => {
      if (sortMode === 'progress') return right.progress - left.progress
      if (sortMode === 'priority') return priorityWeight(right.priority) - priorityWeight(left.priority)
      return parsedDate(right.updatedAt) - parsedDate(left.updatedAt)
    })
  })

  function selectPhase(phase: WorkCenterPhase) {
    selectedPhase = selectedPhase === phase ? 'all' : phase
  }

  function statusLabel(item: WorkCenterItem) {
    if (item.kind === 'idea') return t(`workhub.ideaStatus.${item.status}` as TranslationKey)
    if (item.kind === 'todo') return t(`workhub.status.${item.status}` as TranslationKey)
    return item.status.replaceAll('_', ' ')
  }

  function itemDate(value: string) {
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return new Intl.DateTimeFormat($locale, { month: 'short', day: 'numeric' }).format(date)
  }

  function phaseTone(phase: WorkCenterPhase) {
    if (phase === 'done') return 'success'
    if (phase === 'review') return 'warning'
    if (phase === 'mission') return 'violet'
    if (phase === 'plan') return 'blue'
    return 'teal'
  }

  function kindTone(kind: WorkCenterItemKind) {
    if (kind === 'mission') return 'violet'
    if (kind === 'todo') return 'blue'
    return 'teal'
  }

  function priorityTone(priority: WorkCenterItem['priority']) {
    if (priority === 'urgent') return 'danger'
    if (priority === 'high') return 'warning'
    return 'neutral'
  }

  function priorityWeight(priority: WorkCenterItem['priority']) {
    return ({ low: 1, medium: 2, high: 3, urgent: 4 })[priority]
  }

  function parsedDate(value: string) {
    const parsed = Date.parse(value)
    return Number.isFinite(parsed) ? parsed : 0
  }
</script>

<svelte:head><title>{t('workcenter.title')} · Orrery</title></svelte:head>

<div class="{embedded ? '' : 'page-shell'} mx-auto w-full min-w-0 max-w-[1440px] space-y-5" data-testid="work-items-center" aria-busy={loading}>
  {#if !embedded}<header>
    <h1 class="text-xl font-semibold text-foreground">{t('workcenter.title')}</h1>
    <p class="mt-1 text-sm text-muted-foreground">{t('workcenter.description')}</p>
  </header>{/if}

  <section class="overflow-hidden {embedded ? 'border-y border-border' : 'panel'}" aria-label={t('workcenter.title')}>
    <div class="grid min-h-[560px] lg:grid-cols-[220px_minmax(0,1fr)]">
      <aside class="border-b border-border bg-muted/25 p-4 lg:border-b-0 lg:border-r" aria-label={t('workcenter.progress.aria')}>
        <div class="flex items-center justify-between gap-3">
          <h2 class="text-xs font-semibold text-foreground">{t(hasMore ? 'workcenter.progress.loaded' : 'workcenter.progress')}</h2>
          {#if selectedPhase !== 'all'}
            <button class="text-xs text-brand hover:underline" type="button" onclick={() => (selectedPhase = 'all')}>
              {t('workcenter.stage.all')}
            </button>
          {/if}
        </div>

        <div class="relative mt-3 grid grid-cols-5 gap-1 lg:block lg:space-y-1.5 before:absolute before:bottom-6 before:left-5 before:top-6 before:hidden before:w-px before:bg-border lg:before:block">
          {#each stages as stage}
            {@const Icon = stage.icon}
            {@const active = selectedPhase === stage.phase}
            <button
              class="relative flex h-12 w-full flex-col items-center justify-center gap-1 rounded-md border px-1 text-left transition-colors lg:flex-row lg:justify-start lg:gap-3 lg:px-3 {active
                ? 'border-brand/30 bg-brand/10 text-foreground shadow-sm'
                : 'border-transparent bg-transparent text-muted-foreground hover:border-border hover:bg-card hover:text-foreground'}"
              type="button"
              aria-pressed={active}
              onclick={() => selectPhase(stage.phase)}
            >
              <span class="relative z-10 hidden h-7 w-7 shrink-0 place-items-center rounded-md border lg:grid {active ? 'border-brand/30 bg-card text-brand' : 'border-border bg-card'}">
                <Icon class="h-3.5 w-3.5" />
              </span>
              <span class="min-w-0 truncate text-xs font-medium lg:flex-1 lg:text-[13px]">{t(stage.label)}</span>
              <span class="text-xs tabular-nums">{stageCounts[stage.phase]}</span>
            </button>
          {/each}
        </div>

        {#if kindFilter === undefined}<div class="mt-6 border-t border-border pt-4">
          <div class="text-xs font-semibold text-foreground">{t('workcenter.source')}</div>
          <div class="mt-2 grid grid-cols-2 gap-1" role="group" aria-label={t('workcenter.source')}>
            {#each kinds as source}
              <button
                class="h-8 rounded-md px-2 text-xs transition-colors {selectedKind === source.kind
                  ? 'bg-foreground text-background'
                  : 'bg-card text-muted-foreground hover:bg-muted hover:text-foreground'}"
                type="button"
                aria-pressed={selectedKind === source.kind}
                onclick={() => (selectedKind = source.kind)}
              >{t(source.label)}</button>
            {/each}
          </div>
        </div>{/if}
      </aside>

      <div class="min-w-0">
        <div class="flex flex-col gap-3 border-b border-border p-4 sm:flex-row sm:items-center">
          <label class="relative min-w-0 flex-1">
            <span class="sr-only">{t('workcenter.search')}</span>
            <Search class="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
            <input
              bind:value={searchQuery}
              class="h-9 w-full rounded-md border border-border bg-input-background pl-9 pr-3 text-sm outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
              placeholder={t('workcenter.search')}
              type="search"
            />
          </label>
          <label class="relative shrink-0">
            <span class="sr-only">{t('workcenter.sort.updated')}</span>
            <ArrowDownUp class="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
            <select bind:value={sortMode} class="h-9 min-w-40 appearance-none rounded-md border border-border bg-card pl-9 pr-8 text-sm outline-none focus-visible:ring-1 focus-visible:ring-ring">
              <option value="updated">{t('workcenter.sort.updated')}</option>
              <option value="progress">{t('workcenter.sort.progress')}</option>
              <option value="priority">{t('workcenter.sort.priority')}</option>
            </select>
          </label>
        </div>

        <div class="flex h-10 items-center justify-between border-b border-border bg-muted/20 px-4 text-xs text-muted-foreground">
          <span>{serverSearch && hasMore && total !== undefined
            ? t('workcenter.items.loaded', { count: filteredItems.length, total })
            : t('workcenter.items', { count: filteredItems.length })}</span>
          {#if selectedPhase !== 'all'}<Badge tone={phaseTone(selectedPhase)}>{t(`workcenter.stage.${selectedPhase}` as TranslationKey)}</Badge>{/if}
        </div>

        {#if errorMessage && items.length > 0}
          <div class="border-b border-border bg-status-blocked-bg px-4 py-2 text-xs text-status-blocked">
            {t('workcenter.error.partial')}
          </div>
        {/if}

        {#if loading}
          <div class="p-4"><LoadingState title={t('workcenter.loading.title')} description={t('workcenter.loading.description')} /></div>
        {:else if errorMessage && items.length === 0}
          <div class="p-4"><ErrorState title={t('workcenter.error.title')} message={errorMessage} /></div>
        {:else if filteredItems.length === 0}
          <div class="p-4"><EmptyState title={t(hasMore ? 'workcenter.empty.loaded' : 'workcenter.empty.title')} description={t('workcenter.empty.description')} /></div>
        {:else}
          <div class="divide-y divide-border" data-testid="work-items-list">
            {#each filteredItems as item (item.kind + item.id)}
              <a class="group block px-4 py-4 transition-colors hover:bg-muted/30 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring" href={item.href}>
                <div class="flex min-w-0 items-start gap-3">
                  <span class="mt-0.5 grid h-8 w-8 shrink-0 place-items-center rounded-md border border-border bg-card {item.kind === 'mission' ? 'text-badge-violet' : item.kind === 'todo' ? 'text-badge-blue' : 'text-badge-teal'}">
                    {#if item.kind === 'idea'}<Lightbulb class="h-4 w-4" />{:else if item.kind === 'todo'}<ListTodo class="h-4 w-4" />{:else}<Rocket class="h-4 w-4" />{/if}
                  </span>
                  <div class="min-w-0 flex-1">
                    <div class="flex min-w-0 flex-wrap items-center gap-2">
                      <h3 class="min-w-0 truncate text-sm font-medium text-foreground group-hover:text-brand">{item.title}</h3>
                      <Badge tone={kindTone(item.kind)}>{t(`workcenter.source.${item.kind}` as TranslationKey)}</Badge>
                      <Badge tone={phaseTone(item.phase)}>{t(`workcenter.stage.${item.phase}` as TranslationKey)}</Badge>
                      <Badge tone={priorityTone(item.priority)}>{t(`workcenter.priority.${item.priority}` as TranslationKey)}</Badge>
                    </div>
                    {#if item.summary}<p class="mt-1 line-clamp-2 text-xs leading-5 text-muted-foreground">{item.summary}</p>{/if}
                    <div class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
                      <span>{statusLabel(item)}</span>
                      <span>{t('workcenter.owner')}: {item.owner || t('workcenter.unassigned')}</span>
                      {#if item.missionId}<span class="min-w-0 break-all font-mono">{t('workcenter.missionId', { id: item.missionId })}</span>{/if}
                      <span>{t('workcenter.updated', { date: itemDate(item.updatedAt) })}</span>
                    </div>
                    <div class="mt-3 flex items-center gap-2">
                      <div class="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-muted">
                        <div class="h-full rounded-full bg-brand transition-[width]" style={`width: ${item.progress}%`}></div>
                      </div>
                      <span class="w-9 text-right text-xs tabular-nums text-muted-foreground">{item.progress}%</span>
                    </div>
                  </div>
                  <ChevronRight class="mt-2 h-4 w-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:text-foreground" />
                </div>
              </a>
            {/each}
          </div>
        {/if}
        {#if hasMore && onLoadMore}
          <div class="flex justify-center border-t border-border p-3">
            <button
              type="button"
              class="inline-flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm hover:bg-muted disabled:cursor-default disabled:opacity-50"
              onclick={onLoadMore}
              disabled={loading || loadingMore}
              aria-busy={loadingMore}
            >
              {#if loadingMore}<LoaderCircle class="h-4 w-4 animate-spin" />{:else}<ChevronDown class="h-4 w-4" />{/if}
              {t('workcenter.loadMore')}
            </button>
          </div>
        {/if}
      </div>
    </div>
  </section>
</div>
