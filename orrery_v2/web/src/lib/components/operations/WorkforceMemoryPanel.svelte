<script lang="ts">
  import { AlertTriangle, Brain, Clock3, Database, Link2 } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Tabs from '#lib/components/ui/Tabs.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { isMemoryDiaryRecord, memoryDiaryDate } from '#lib/memory/diary'
  import { useMemoryRecordsByProfilesQuery, useMemoryRecordsQuery } from '#lib/queries/memory'
  import { useMemoryProfileQuery } from '#lib/queries/resources'
  import { useSettingsQuery } from '#lib/queries/settings'
  import type { MemoryRecordDTO } from '#lib/types/memory'

  type MemoryOwnership = 'personal' | 'collective'
  type MemoryLayerFilter = 'all' | 'diary' | 'fact' | 'experience' | 'world'

  interface Props {
    memberName: string
    ownerId: string
    personalProfileId?: string
    collectiveProfileIds?: string[]
    oncountchange?: (count: number) => void
  }

  let {
    memberName,
    ownerId,
    personalProfileId = '',
    collectiveProfileIds = [],
    oncountchange
  }: Props = $props()
  let t = $derived(createTranslator($locale))
  let reportedCount = $state(-1)
  let activeOwnership = $state<MemoryOwnership>('personal')
  let activeLayer = $state<MemoryLayerFilter>('all')
  let previousOwnership = $state<MemoryOwnership>('personal')

  const settingsQuery = useSettingsQuery()
  const personalProfileQuery = useMemoryProfileQuery(() => personalProfileId)
  const personalRecordsQuery = useMemoryRecordsQuery(
    () => ({
      profileId: personalProfileId || undefined,
      scopeKind: personalProfileId ? undefined : 'agent',
      scopeId: personalProfileId ? undefined : ownerId,
      lifecycleStatus: 'active',
      includeArchived: false,
      limit: 30
    }),
    () => Boolean(personalProfileId.trim() || ownerId.trim())
  )
  const collectiveRecordsQuery = useMemoryRecordsByProfilesQuery(
    () => collectiveProfileIds,
    () => ({ lifecycleStatus: 'active', includeArchived: false, limit: 30 }),
    () => collectiveProfileIds.some((id) => id.trim())
  )
  const personalRecords = $derived(sortRecords(personalRecordsQuery.data?.records ?? []))
  const collectiveRecords = $derived(sortRecords(collectiveRecordsQuery.data?.records ?? []))
  const activeRecords = $derived(activeOwnership === 'personal' ? personalRecords : collectiveRecords)
  const visibleRecords = $derived(activeRecords.filter((record) => matchesLayer(record, activeLayer)))
  const activeQuery = $derived(activeOwnership === 'personal' ? personalRecordsQuery : collectiveRecordsQuery)
  const ownershipTabs = $derived([
    { value: 'personal', label: `${t('people.personalMemory')} ${personalRecords.length}` },
    { value: 'collective', label: `${t('people.collectiveMemory')} ${collectiveRecords.length}` }
  ])
  const layerFilters = $derived<Array<{ value: MemoryLayerFilter; label: string; count: number }>>([
    { value: 'all', label: t('people.memoryAllLayers'), count: activeRecords.length },
    { value: 'diary', label: t('people.memoryDiary'), count: countLayer(activeRecords, 'diary') },
    { value: 'fact', label: layerLabel('fact'), count: countLayer(activeRecords, 'fact') },
    { value: 'experience', label: layerLabel('experience'), count: countLayer(activeRecords, 'experience') },
    { value: 'world', label: layerLabel('world'), count: countLayer(activeRecords, 'world') }
  ])

  $effect(() => {
    if (!personalRecordsQuery.data && !collectiveRecordsQuery.data) return
    const count = new Set([...personalRecords, ...collectiveRecords].map((record) => record.memory_id)).size
    if (reportedCount === count) return
    reportedCount = count
    oncountchange?.(count)
  })

  $effect(() => {
    if (activeOwnership !== previousOwnership) {
      previousOwnership = activeOwnership
      activeLayer = 'all'
    }
    if (activeOwnership === 'personal' && !personalProfileId.trim() && !ownerId.trim() && collectiveProfileIds.some((id) => id.trim())) {
      activeOwnership = 'collective'
    }
  })

  function layerLabel(layer: string) {
    switch (layer) {
      case 'fact': return t('memory.value.layer.fact')
      case 'experience': return t('memory.value.layer.experience')
      case 'world': return t('memory.value.layer.world')
      default: return layer || t('people.memoryUncategorized')
    }
  }

  function recordTitle(record: MemoryRecordDTO) {
    if (isMemoryDiaryRecord(record)) {
      return t('people.memoryDiaryForDate', { date: diaryDateLabel(record) })
    }
    return record.topic?.trim() || t('people.memoryUntitled')
  }

  function sortRecords(items: MemoryRecordDTO[]) {
    return [...items].sort((left, right) => Date.parse(right.updated_at) - Date.parse(left.updated_at))
  }

  function countLayer(items: MemoryRecordDTO[], layer: MemoryLayerFilter) {
    return items.filter((record) => matchesLayer(record, layer)).length
  }

  function matchesLayer(record: MemoryRecordDTO, layer: MemoryLayerFilter) {
    if (layer === 'all') return true
    if (layer === 'diary') return isMemoryDiaryRecord(record)
    if (layer === 'fact') return record.layer === 'fact' && !isMemoryDiaryRecord(record)
    return record.layer === layer
  }

  function diaryDateLabel(record: MemoryRecordDTO) {
    const date = memoryDiaryDate(record)
    const parsed = new Date(`${date}T12:00:00Z`)
    return Number.isNaN(parsed.getTime())
      ? date
      : parsed.toLocaleDateString($locale, { year: 'numeric', month: 'short', day: 'numeric' })
  }

  function recordBody(record: MemoryRecordDTO) {
    return [record.narrative, record.content]
      .map((value) => value?.trim() ?? '')
      .find((value) => value && value !== record.summary.trim()) ?? ''
  }

  function highlights(record: MemoryRecordDTO) {
    return [
      ...(record.facts ?? []),
      ...(record.lessons ?? []),
      ...(record.preferences ?? []),
      ...(record.decisions ?? []),
      ...(record.warnings ?? [])
    ].map((value) => value.trim()).filter(Boolean).slice(0, 5)
  }

  function sourceItems(record: MemoryRecordDTO) {
    const citations = (record.citation_evidence ?? []).map((item) => item.title?.trim() || item.id.trim())
    return [...new Set([
      ...citations,
      ...(record.source_evidence_refs ?? []),
      ...(record.source_memory_refs ?? []),
      ...(record.lineage_refs ?? [])
    ].map((value) => value.trim()).filter(Boolean))].slice(0, 3)
  }

  function confidenceLabel(confidence: number) {
    const normalized = confidence <= 1 ? confidence * 100 : confidence
    return `${Math.max(0, Math.min(100, Math.round(normalized)))}%`
  }

  function lifecycleLabel(status: string) {
    switch (status) {
      case 'active': return t('people.memoryStatusActive')
      case 'settled': return t('people.memoryStatusSettled')
      case 'distilled': return t('people.memoryStatusDistilled')
      case 'archived': return t('people.memoryStatusArchived')
      default: return status || t('people.memoryStatusUnknown')
    }
  }

  function scopeLabel(record: MemoryRecordDTO) {
    const kind = record.scope?.kind?.trim() ?? ''
    const id = record.scope?.id?.trim() ?? ''
    const labels: Record<string, string> = {
      organization: t('memory.value.scope.organization'),
      department: t('memory.value.scope.department'),
      project: t('memory.value.scope.project'),
      mission: t('memory.value.scope.mission'),
      discussion: t('memory.value.scope.discussion')
    }
    return [labels[kind] || kind, id].filter(Boolean).join(' · ')
  }

  function layerTone(layer: string): 'blue' | 'teal' | 'violet' | 'neutral' {
    switch (layer) {
      case 'fact': return 'blue'
      case 'experience': return 'teal'
      case 'world': return 'violet'
      default: return 'neutral'
    }
  }

  function timeLabel(value: string) {
    if (!value) return ''
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return date.toLocaleString($locale, { year: 'numeric', month: 'short', day: 'numeric' })
  }
</script>

<section data-workforce-memory-panel>
  <header class="flex flex-wrap items-start justify-between gap-3">
    <div>
      <h3 class="flex items-center gap-2 text-sm font-semibold text-foreground">
        <Brain class="h-4 w-4 text-brand" />
        {t('people.memoryContent')}
      </h3>
      <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('people.memoryContentDescription', { name: memberName })}</p>
    </div>
    <Button href="/resources/memory" variant="outline" size="sm">
      <Database class="h-3.5 w-3.5" />
      {t('people.manageMemory')}
    </Button>
  </header>

  {#if settingsQuery.data?.memory.enabled === false}
    <div data-memory-capture-disabled class="mt-5 flex flex-wrap items-start justify-between gap-3 border-y border-status-blocked/25 bg-status-blocked-bg/45 px-3 py-3">
      <div class="flex min-w-0 flex-1 items-start gap-2.5">
        <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0 text-status-blocked" />
        <div class="min-w-0">
          <p class="text-sm font-medium text-foreground">{t('people.memoryCaptureDisabled')}</p>
          <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('people.memoryCaptureDisabledDescription')}</p>
        </div>
      </div>
      <Button href="/settings" variant="outline" size="sm">{t('people.openMemorySettings')}</Button>
    </div>
  {:else if activeOwnership === 'personal' && personalProfileId.trim() && personalProfileQuery.data?.enabled === false}
    <div data-personal-memory-disabled class="mt-5 flex flex-wrap items-start justify-between gap-3 border-y border-status-blocked/25 bg-status-blocked-bg/45 px-3 py-3">
      <div class="flex min-w-0 flex-1 items-start gap-2.5">
        <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0 text-status-blocked" />
        <div class="min-w-0">
          <p class="text-sm font-medium text-foreground">{t('people.personalMemoryDisabled')}</p>
          <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('people.personalMemoryDisabledDescription')}</p>
        </div>
      </div>
      <Button href="/resources/memory" variant="outline" size="sm">{t('people.openMemorySettings')}</Button>
    </div>
  {/if}

  <div class="mt-5 flex flex-wrap items-end justify-between gap-x-5 gap-y-3 border-b border-border">
    <Tabs
      bind:value={activeOwnership}
      tabs={ownershipTabs}
      selectionMaterial
      ariaLabel={t('people.memoryOwnership')}
      class="mb-2 shrink-0"
    />
    <Tabs
      bind:value={activeLayer}
      tabs={layerFilters.map((filter) => ({ value: filter.value, label: `${filter.label} ${filter.count}` }))}
      variant="underline"
      ariaLabel={t('people.memoryLayer')}
      class="!w-auto !border-b-0"
    />
  </div>

  {#if activeQuery.isLoading}
    <div class="mt-5 space-y-4" aria-label={t('people.memoryLoading')}>
      {#each [1, 2, 3] as item (item)}
        <div class="animate-pulse border-t border-border pt-4 first:border-t-0 first:pt-0">
          <div class="h-3 w-36 rounded bg-muted"></div>
          <div class="mt-3 h-3 w-full rounded bg-muted/75"></div>
          <div class="mt-2 h-3 w-2/3 rounded bg-muted/60"></div>
        </div>
      {/each}
    </div>
  {:else if activeQuery.isError}
    <div class="mt-5 border-y border-destructive/20 bg-destructive/5 px-3 py-4 text-sm text-destructive">
      {t('people.memoryLoadFailed')}
    </div>
  {:else if activeRecords.length === 0}
    <div class="mt-5 border-y border-border py-10 text-center">
      <Brain class="mx-auto h-6 w-6 text-muted-foreground/45" />
      <p class="mt-3 text-sm font-medium text-foreground">{t('people.memoryEmpty')}</p>
      <p class="mx-auto mt-1 max-w-lg text-xs leading-5 text-muted-foreground">
        {activeOwnership === 'personal' ? t('people.personalMemoryEmptyDescription') : t('people.collectiveMemoryEmptyDescription')}
      </p>
    </div>
  {:else if visibleRecords.length === 0}
    <div class="mt-5 border-y border-border py-8 text-center text-xs text-muted-foreground">
      {t('people.memoryLayerEmpty')}
    </div>
  {:else}
    <div class="mt-5 divide-y divide-border" data-workforce-memory-records>
      {#each visibleRecords as record (record.memory_id)}
        {@const body = recordBody(record)}
        {@const recordHighlights = highlights(record)}
        {@const sources = sourceItems(record)}
        <article class="py-4 first:pt-0">
          <div class="flex flex-wrap items-center gap-2">
            <h4 class="min-w-0 flex-1 text-[13.5px] font-semibold text-foreground">{recordTitle(record)}</h4>
            {#if activeOwnership === 'collective' && scopeLabel(record)}
              <Badge tone="neutral" class="text-[10px]">{scopeLabel(record)}</Badge>
            {/if}
            <Badge tone={layerTone(record.layer)} class="text-[10px]">
              {isMemoryDiaryRecord(record) ? t('people.memoryDiary') : layerLabel(record.layer)}
            </Badge>
            <Badge tone="success" class="text-[10px]">{lifecycleLabel(record.lifecycle_status)}</Badge>
          </div>
          <p class="mt-2 whitespace-pre-wrap text-[13px] leading-6 text-foreground/85">{record.summary}</p>
          {#if body}
            <p class="mt-2 whitespace-pre-wrap text-xs leading-5 text-muted-foreground">{body}</p>
          {/if}
          {#if recordHighlights.length > 0}
            <ul class="mt-3 grid gap-1.5 text-xs leading-5 text-muted-foreground sm:grid-cols-2">
              {#each recordHighlights as highlight}
                <li class="flex items-start gap-2"><span class="mt-2 h-1 w-1 shrink-0 rounded-full bg-source-agent"></span><span>{highlight}</span></li>
              {/each}
            </ul>
          {/if}
          <footer class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1.5 text-[10.5px] text-muted-foreground">
            {#if sources.length > 0}
              <span class="inline-flex min-w-0 items-center gap-1.5">
                <Link2 class="h-3 w-3 shrink-0" />
                <span class="shrink-0">{t('people.memorySource')}</span>
                <span class="max-w-[28rem] truncate text-foreground/65" title={sources.join(' · ')}>{sources.join(' · ')}</span>
              </span>
            {/if}
            <span>{t('people.memoryConfidence')} {confidenceLabel(record.confidence)}</span>
            {#if record.updated_at}
              <span class="inline-flex items-center gap-1"><Clock3 class="h-3 w-3" />{t('people.memoryUpdatedAt')} {timeLabel(record.updated_at)}</span>
            {/if}
          </footer>
        </article>
      {/each}
    </div>
  {/if}
</section>
