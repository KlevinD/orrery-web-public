<script lang="ts">
  import { BookOpen, CalendarDays, ChevronDown, Database, FileText, Search, SlidersHorizontal } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import MemoryCitationEvidenceList from '#lib/components/memory/MemoryCitationEvidenceList.svelte'
  import KpiCard from '#lib/components/shared/KpiCard.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import { groupMemoryDiaryRecords, memoryDiaryDate, memoryDiaryPath } from '#lib/memory/diary'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { TranslationKey } from '#lib/i18n'
  import { useMemoryCandidatesQuery, useMemoryRecordQuery, useMemoryRecordsQuery } from '#lib/queries/memory'
  import type { ApiClientError } from '#lib/api/client'
  import type { MemoryCandidateDTO, MemoryRecordDTO } from '#lib/types/memory'

  interface Props {
    embedded?: boolean
  }

  let { embedded = false }: Props = $props()

  type MemoryView = 'diary' | 'records' | 'candidates' | 'raw'

  const memoryValueKeys = {
    agent: 'memory.value.scope.agent',
    mission: 'memory.value.scope.mission',
    discussion: 'memory.value.scope.discussion',
    project: 'memory.value.scope.project',
    organization: 'memory.value.scope.organization',
    fact: 'memory.value.layer.fact',
    experience: 'memory.value.layer.experience',
    world: 'memory.value.layer.world',
    active: 'memory.value.lifecycle.active',
    settled: 'memory.value.lifecycle.settled',
    distilled: 'memory.value.lifecycle.distilled',
    archived: 'memory.value.lifecycle.archived',
    proposed: 'memory.value.candidate.proposed',
    accepted: 'memory.value.candidate.accepted',
    rejected: 'memory.value.candidate.rejected',
    merged: 'memory.value.candidate.merged',
    owner_only: 'memory.value.visibility.owner_only',
    private: 'memory.value.visibility.private',
    shared: 'memory.value.visibility.shared',
    scoped_members: 'memory.value.visibility.scoped_members',
    public: 'memory.value.visibility.public',
    low: 'memory.value.risk.low',
    medium: 'memory.value.risk.medium',
    high: 'memory.value.risk.high'
  } as const satisfies Record<string, TranslationKey>

  let profileId = $state('')
  let scopeKind = $state('')
  let scopeId = $state('')
  let layer = $state('')
  let topic = $state('')
  let lifecycleStatus = $state('active')
  let includeArchived = $state(false)
  let candidateStatus = $state('')
  let selectedDiaryDate = $state('')
  let selectedMemoryId = $state('')
  let activeMemoryView = $state<MemoryView>('diary')
  let mobileFiltersOpen = $state(false)
  let t = $derived(createTranslator($locale))

  const recordsQuery = useMemoryRecordsQuery(() => ({
    profileId,
    scopeKind,
    scopeId,
    layer,
    topic,
    lifecycleStatus,
    includeArchived,
    limit: 80
  }))
  const candidatesQuery = useMemoryCandidatesQuery(() => ({
    scopeKind,
    scopeId,
    layer,
    topic,
    status: candidateStatus,
    limit: 50
  }))
  const recordQuery = useMemoryRecordQuery(() => selectedMemoryId)
  const records = $derived(recordsQuery.data?.records ?? [])
  const diaryDays = $derived(groupMemoryDiaryRecords(records))
  const selectedDiaryDay = $derived(
    diaryDays.find((day) => day.date === selectedDiaryDate) ?? diaryDays[0]
  )
  const candidates = $derived(candidatesQuery.data?.candidates ?? [])
  const selectedRecord = $derived(recordQuery.data)

  function selectRecord(record: MemoryRecordDTO) {
    selectedMemoryId = record.memory_id
    activeMemoryView = 'raw'
  }

  function selectDiary(date: string) {
    selectedDiaryDate = date
    selectedMemoryId = ''
  }

  function scopeLabel(scope: { kind?: string; id?: string } | undefined) {
    if (!scope || (!scope.kind && !scope.id)) return t('workspace.notProvided')
    return [memoryValueLabel(scope.kind), scope.id].filter(Boolean).join(' · ')
  }

  function field(value: string | number | undefined | null) {
    const text = String(value ?? '').trim()
    return text || t('workspace.notProvided')
  }

  function memoryValueLabel(value: string | undefined | null) {
    const text = String(value ?? '').trim()
    const key = memoryValueKeys[text as keyof typeof memoryValueKeys]
    return key ? t(key) : field(text)
  }

  function timeLabel(value: string | undefined) {
    if (!value) return t('workspace.notProvided')
    const date = new Date(value)
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
  }

  function toneForStatus(status: string) {
    switch (status) {
      case 'active':
      case 'accepted':
        return 'success'
      case 'proposed':
      case 'settled':
        return 'info'
      case 'archived':
      case 'rejected':
      case 'deleted':
        return 'neutral'
      default:
        return 'warning'
    }
  }
</script>

<svelte:head>
  <title>{embedded ? t('nav.memoryResources') : t('nav.memory')} | Orrery</title>
</svelte:head>

<div data-memory-admin-workspace class="{embedded ? 'space-y-5' : 'page-shell max-w-[920px] space-y-5'}">
  {#if !embedded}
    <header class="page-header shrink-0">
      <div>
        <h1 class="page-title">{t('nav.memory')}</h1>
        <p class="page-description">{t('nav.memory.description')}</p>
      </div>
      <Button href="/resources/memory" variant="outline">
        <Database class="h-4 w-4" />
        {t('memory.profileMenu')}
      </Button>
    </header>
  {/if}

  <nav class="flex shrink-0 items-center gap-0 overflow-x-auto border-b border-border subtle-scrollbar sm:gap-1" aria-label={t('nav.memory')}>
    {#each [
      { id: 'diary' as MemoryView, label: t('memory.diaryFiles'), icon: CalendarDays },
      { id: 'records' as MemoryView, label: t('memory.records'), icon: BookOpen },
      { id: 'candidates' as MemoryView, label: t('memory.candidates'), icon: FileText },
      { id: 'raw' as MemoryView, label: t('memory.rawDetail'), icon: Database }
    ] as view}
      <button
        type="button"
        class="-mb-px inline-flex h-10 shrink-0 items-center gap-1 border-b-2 px-2 text-[13px] transition-colors sm:gap-1.5 sm:px-3 {activeMemoryView === view.id ? 'border-brand font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
        onclick={() => (activeMemoryView = view.id)}
      >
        <view.icon class="h-3.5 w-3.5" />{view.label}
      </button>
    {/each}
  </nav>

  <section class="grid grid-cols-2 gap-3 md:grid-cols-4">
    <KpiCard label={t('memory.diaryDays')} value={diaryDays.length} />
    <KpiCard label={t('memory.records')} value={recordsQuery.data?.total ?? records.length} />
    <KpiCard label={t('memory.candidates')} value={candidatesQuery.data?.total ?? candidates.length} />
    <KpiCard label={t('memory.selectedDiary')} value={selectedDiaryDay?.path ?? t('workspace.notProvided')} />
  </section>

  <section class="shrink-0 rounded-xl border border-border bg-card p-3">
    <button
      type="button"
      class="flex h-9 w-full items-center justify-between text-sm font-medium text-foreground md:hidden"
      aria-expanded={mobileFiltersOpen}
      onclick={() => (mobileFiltersOpen = !mobileFiltersOpen)}
    >
      <span class="inline-flex items-center gap-2"><SlidersHorizontal class="h-4 w-4 text-brand" />{t('memory.filters')}</span>
      <ChevronDown class="h-4 w-4 text-muted-foreground transition-transform {mobileFiltersOpen ? 'rotate-180' : ''}" />
    </button>
    <div class="{mobileFiltersOpen ? 'grid' : 'hidden'} gap-3 pt-3 md:grid md:grid-cols-6 md:pt-0">
    <label class="space-y-1 md:col-span-2">
      <span class="text-xs font-medium text-muted-foreground">{t('memory.profileId')}</span>
      <div class="relative">
        <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <Input bind:value={profileId} class="pl-9" />
      </div>
    </label>
    <label class="space-y-1">
      <span class="text-xs font-medium text-muted-foreground">{t('memory.scopeKind')}</span>
      <Select bind:value={scopeKind} options={[
        { label: t('memory.all'), value: '' },
        { label: t('memory.value.scope.agent'), value: 'agent' },
        { label: t('memory.value.scope.mission'), value: 'mission' },
        { label: t('memory.value.scope.discussion'), value: 'discussion' },
        { label: t('memory.value.scope.project'), value: 'project' },
        { label: t('memory.value.scope.organization'), value: 'organization' }
      ]} />
    </label>
    <label class="space-y-1">
      <span class="text-xs font-medium text-muted-foreground">{t('memory.scopeId')}</span>
      <Input bind:value={scopeId} />
    </label>
    <label class="space-y-1">
      <span class="text-xs font-medium text-muted-foreground">{t('memory.layer')}</span>
      <Select bind:value={layer} options={[
        { label: t('memory.all'), value: '' },
        { label: t('memory.value.layer.fact'), value: 'fact' },
        { label: t('memory.value.layer.experience'), value: 'experience' },
        { label: t('memory.value.layer.world'), value: 'world' }
      ]} />
    </label>
    <label class="space-y-1">
      <span class="text-xs font-medium text-muted-foreground">{t('memory.topic')}</span>
      <Input bind:value={topic} />
    </label>
    <label class="space-y-1">
      <span class="text-xs font-medium text-muted-foreground">{t('memory.lifecycle')}</span>
      <Select bind:value={lifecycleStatus} options={[
        { label: t('memory.all'), value: '' },
        { label: t('memory.value.lifecycle.active'), value: 'active' },
        { label: t('memory.value.lifecycle.settled'), value: 'settled' },
        { label: t('memory.value.lifecycle.distilled'), value: 'distilled' },
        { label: t('memory.value.lifecycle.archived'), value: 'archived' }
      ]} />
    </label>
    <label class="space-y-1">
      <span class="text-xs font-medium text-muted-foreground">{t('memory.candidateStatus')}</span>
      <Select bind:value={candidateStatus} options={[
        { label: t('memory.all'), value: '' },
        { label: t('memory.value.candidate.proposed'), value: 'proposed' },
        { label: t('memory.value.candidate.accepted'), value: 'accepted' },
        { label: t('memory.value.candidate.rejected'), value: 'rejected' },
        { label: t('memory.value.candidate.merged'), value: 'merged' }
      ]} />
    </label>
    <label class="flex items-end gap-2 pb-2">
      <input class="h-4 w-4 rounded border-border" type="checkbox" bind:checked={includeArchived} />
      <span class="text-xs font-medium text-muted-foreground">{t('memory.includeArchived')}</span>
    </label>
    </div>
  </section>

  <section class="grid gap-4 {activeMemoryView === 'diary' && diaryDays.length > 0 ? 'xl:grid-cols-[260px_minmax(0,1fr)]' : 'grid-cols-1'}">
    <aside class="panel p-3 {activeMemoryView === 'diary' && diaryDays.length > 0 ? '' : 'hidden'}">
      <div class="mb-3 flex items-center gap-2">
        <CalendarDays class="h-4 w-4 text-brand" />
        <h2 class="text-sm font-semibold text-foreground">{t('memory.diaryFiles')}</h2>
      </div>
      <div class="grid gap-2">
        {#each diaryDays as day (day.date)}
          <button
            type="button"
            onclick={() => selectDiary(day.date)}
            class="w-full rounded-md border p-3 text-left transition-colors {selectedDiaryDay?.date === day.date ? 'border-brand bg-brand/10' : 'border-border bg-background hover:border-brand/50'}"
          >
            <span class="block truncate font-mono text-xs font-semibold text-foreground">{day.path}</span>
            <span class="mt-1 flex items-center justify-between gap-2 text-xs text-muted-foreground">
              <span>{day.date}</span>
              <span>{day.records.length} {t('memory.entries')}</span>
            </span>
          </button>
        {/each}
      </div>
    </aside>

    <div class="{activeMemoryView === 'raw' ? 'hidden' : ''}">
      <section class="{activeMemoryView === 'candidates' ? 'hidden' : ''}">
        {#if recordsQuery.isLoading}
          <LoadingState title={t('memory.dailyDiary')} description={t('state.loadingMemoryProfilesDescription')} />
        {:else if recordsQuery.isError}
          {@const error = recordsQuery.error as ApiClientError}
          <ErrorState title={t('memory.dailyDiary')} message={error.message} correlationId={error.correlationId} />
        {:else if !selectedDiaryDay}
          <EmptyState title={t('memory.emptyDiary')} description={t('memory.emptyDiary.desc')} />
        {:else}
          <div class="panel p-5">
            <header class="border-b border-border pb-4">
              <div class="flex flex-wrap items-center gap-2">
                <BookOpen class="h-4 w-4 text-brand" />
                <h2 class="font-mono text-lg font-semibold text-foreground">{selectedDiaryDay.path}</h2>
                <Badge tone="info">{selectedDiaryDay.records.length} {t('memory.entries')}</Badge>
              </div>
              <p class="mt-2 text-sm text-muted-foreground">{selectedDiaryDay.date} · {t('memory.dailyDiary')}</p>
            </header>

            <div class="mt-5 space-y-5">
              {#each selectedDiaryDay.records as record (record.memory_id)}
                <article class="group rounded-xl border border-border bg-background p-4 transition-colors hover:border-brand/50">
                <div class="flex flex-wrap items-start justify-between gap-3">
                  <div class="min-w-0">
                    <div class="flex flex-wrap items-center gap-2">
                      <span class="font-mono text-xs text-muted-foreground">{timeLabel(record.created_at)}</span>
                      <Badge tone={toneForStatus(record.lifecycle_status)}>{memoryValueLabel(record.lifecycle_status)}</Badge>
                      <Badge tone="info">{memoryValueLabel(record.layer)}</Badge>
                      <Badge>{scopeLabel(record.scope)}</Badge>
                    </div>
                    <h3 class="mt-3 text-base font-semibold leading-6 text-foreground">{record.summary}</h3>
                  </div>
                  <Button variant="outline" size="sm" onclick={() => selectRecord(record)}>{t('memory.viewRaw')}</Button>
                </div>
                {#if record.content || record.narrative}
                  <pre class="mt-4 whitespace-pre-wrap rounded-md border border-border bg-muted/30 p-3 text-sm leading-6 text-foreground">{record.content || record.narrative}</pre>
                {/if}
                {#if (record.facts?.length ?? 0) > 0}
                  <div class="mt-4">
                    <div class="text-xs font-medium text-muted-foreground">{t('memory.facts')}</div>
                    <ul class="mt-2 list-disc space-y-1 pl-5 text-sm leading-6 text-foreground">
                      {#each record.facts ?? [] as fact}
                        <li>{fact}</li>
                      {/each}
                    </ul>
                  </div>
                {/if}
                </article>
              {/each}
            </div>
          </div>
        {/if}
      </section>

      <section class="{activeMemoryView === 'candidates' ? '' : 'hidden'}">
        {#if candidatesQuery.isLoading}
          <LoadingState title={t('memory.candidates')} description={t('memory.loadingCandidates')} />
        {:else if candidatesQuery.isError}
          {@const error = candidatesQuery.error as ApiClientError}
          <ErrorState title={t('memory.candidates')} message={error.message} correlationId={error.correlationId} />
        {:else if candidates.length === 0}
          <EmptyState title={t('memory.emptyCandidates')} description={t('memory.emptyCandidates.desc')} />
        {:else}
          <div class="panel p-4">
            <div class="mb-3 flex items-center gap-2">
              <FileText class="h-4 w-4 text-brand" />
              <h2 class="text-sm font-semibold text-foreground">{t('memory.candidates')}</h2>
            </div>
            <div class="grid gap-3">
              {#each candidates as candidate (candidate.candidate_id)}
                <article class="rounded-xl border border-border bg-card p-3">
                  <div class="flex flex-wrap items-center gap-2">
                    <h3 class="min-w-0 truncate text-sm font-semibold text-foreground">{candidate.summary}</h3>
                    <Badge tone={toneForStatus(candidate.status)}>{memoryValueLabel(candidate.status)}</Badge>
                    <Badge>{memoryValueLabel(candidate.source_type)}</Badge>
                  </div>
                  <div class="mt-2 grid gap-1 text-xs text-muted-foreground md:grid-cols-3">
                    <span>{candidate.candidate_id}</span>
                    <span>{scopeLabel(candidate.proposed_scope)}</span>
                    <span>{timeLabel(candidate.created_at)}</span>
                  </div>
                </article>
              {/each}
            </div>
          </div>
        {/if}
      </section>
    </div>

    <aside class="min-h-0 overflow-y-auto {activeMemoryView === 'raw' ? '' : 'hidden'}">
      <section class="panel p-4">
        <h2 class="text-sm font-semibold text-foreground">{t('memory.rawDetail')}</h2>
        {#if selectedMemoryId && recordQuery.isLoading}
          <LoadingState title={t('memory.rawDetail')} description={selectedMemoryId} />
        {:else if recordQuery.isError}
          {@const error = recordQuery.error as ApiClientError}
          <ErrorState title={t('memory.rawDetail')} message={error.message} correlationId={error.correlationId} />
        {:else if selectedRecord}
          <div class="mt-4 space-y-4">
            <div>
              <div class="flex flex-wrap gap-2">
                <Badge tone={toneForStatus(selectedRecord.lifecycle_status)}>{memoryValueLabel(selectedRecord.lifecycle_status)}</Badge>
                <Badge tone="info">{memoryValueLabel(selectedRecord.layer)}</Badge>
                <Badge>{memoryValueLabel(selectedRecord.sensitivity)}</Badge>
              </div>
              <p class="mt-3 text-sm leading-6 text-foreground">{selectedRecord.summary}</p>
            </div>
            <dl class="grid gap-2 text-sm">
              {@render detailRow('memory.diaryFile', memoryDiaryPath(selectedRecord, memoryDiaryDate(selectedRecord)))}
              {@render detailRow('memory.memoryId', selectedRecord.memory_id)}
              {@render detailRow('memory.profileId', selectedRecord.profile_id)}
              {@render detailRow('memory.candidateId', selectedRecord.candidate_id)}
              {@render detailRow('memory.scope', scopeLabel(selectedRecord.scope))}
              {@render detailRow('memory.visibility', memoryValueLabel(selectedRecord.visibility.mode))}
              {@render detailRow('memory.createdBy', selectedRecord.created_by)}
              {@render detailRow('memory.createdAt', timeLabel(selectedRecord.created_at))}
            </dl>
            {@render textBlock('memory.content', selectedRecord.content || selectedRecord.narrative)}
            {@render listBlock('memory.facts', selectedRecord.facts ?? [])}
            {@render listBlock('memory.lessons', selectedRecord.lessons ?? [])}
            {@render listBlock('memory.warnings', selectedRecord.warnings ?? [])}
            <MemoryCitationEvidenceList label={t('memory.citationEvidence')} evidence={selectedRecord.citation_evidence ?? []} />
            {@render listBlock('memory.sources', [...(selectedRecord.source_memory_refs ?? []), ...(selectedRecord.source_evidence_refs ?? [])])}
          </div>
        {:else}
          <p class="mt-3 text-sm leading-6 text-muted-foreground">{t('memory.selectRecord')}</p>
        {/if}
      </section>
    </aside>
  </section>
</div>

{#snippet detailRow(label: TranslationKey, value: string)}
  <div class="flex items-start justify-between gap-3 border-b border-border pb-2 last:border-b-0">
    <dt class="text-muted-foreground">{t(label)}</dt>
    <dd class="max-w-56 break-words text-right font-medium text-foreground">{field(value)}</dd>
  </div>
{/snippet}

{#snippet textBlock(label: TranslationKey, value: string | undefined)}
  {#if value}
    <div class="rounded-md border border-border bg-muted/30 p-3">
      <div class="text-xs font-medium text-muted-foreground">{t(label)}</div>
      <p class="mt-2 whitespace-pre-wrap text-sm leading-6 text-foreground">{value}</p>
    </div>
  {/if}
{/snippet}

{#snippet listBlock(label: TranslationKey, values: string[])}
  {#if values.length > 0}
    <div class="rounded-md border border-border bg-muted/30 p-3">
      <div class="text-xs font-medium text-muted-foreground">{t(label)}</div>
      <ul class="mt-2 space-y-1 text-sm leading-6 text-foreground">
        {#each values as value}
          <li>{value}</li>
        {/each}
      </ul>
    </div>
  {/if}
{/snippet}
