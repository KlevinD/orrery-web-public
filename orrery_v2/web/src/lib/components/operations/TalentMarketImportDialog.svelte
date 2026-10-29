<script lang="ts">
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type {
    TalentMarketImportCandidateDTO,
    TalentMarketImportPreviewItemDTO,
    TalentMarketImportPreviewResponseDTO
  } from '../../types/api/workforce'

  type ImportPhase = 'idle' | 'uploading' | 'previewing' | 'ready' | 'committing' | 'complete' | 'failed'
  type ImportLogLevel = 'info' | 'success' | 'warning' | 'error'
  type ImportItemState = 'pending' | 'parsing' | 'ready' | 'importing' | 'imported' | 'skipped' | 'conflict' | 'failed'
  type OverlapType = 'real_name' | 'display_name' | 'agent_profile_id'

  interface ImportLogEntry {
    level: ImportLogLevel | string
    message: string
    fileName?: string
    at?: string
  }

  interface ImportItemStatus {
    state: ImportItemState | string
    message?: string
  }

  interface PackageOverlap {
    type: OverlapType
    value: string
    files: string[]
  }

  interface Props {
    preview?: TalentMarketImportPreviewResponseDTO
    busy?: boolean
    phase?: ImportPhase | string
    currentFileName?: string
    importedCount?: number
    failedCount?: number
    skippedCount?: number
    logs?: ImportLogEntry[]
    itemStatuses?: Record<number, ImportItemStatus>
    oncancel?: () => void
    onsubmit?: (candidates: TalentMarketImportCandidateDTO[]) => void
  }

  let {
    preview,
    busy = false,
    phase = 'idle',
    currentFileName = '',
    importedCount = 0,
    failedCount = 0,
    skippedCount = 0,
    logs = [],
    itemStatuses = {},
    oncancel,
    onsubmit
  }: Props = $props()
  let resolutions = $state<Record<number, string>>({})
  let renamed = $state<Record<number, string>>({})
  let t = $derived(createTranslator($locale))
  const items = $derived(preview?.items ?? [])
  const conflictItems = $derived(items.filter((item) => item.status === 'conflict'))
  const conflictCount = $derived(preview?.conflicts?.length ?? 0)
  const packageOverlaps = $derived(buildPackageOverlaps(items))
  const completedCount = $derived(Math.min(items.length, importedCount + failedCount + skippedCount))
  const progressPercent = $derived(items.length > 0 ? Math.round((completedCount / items.length) * 100) : phase === 'previewing' || phase === 'uploading' ? 20 : 0)
  const currentFileLabel = $derived(currentFileName || activeItemFile() || t('people.importCurrentFileIdle'))

  $effect(() => {
    const nextResolutions: Record<number, string> = {}
    const nextRenamed: Record<number, string> = {}
    for (const item of items) {
      nextResolutions[item.candidate_index] = item.status === 'conflict' ? 'rename' : 'create'
      nextRenamed[item.candidate_index] = item.candidate.resolved_real_name || renameSuggestion(item)
    }
    resolutions = nextResolutions
    renamed = nextRenamed
  })

  function renameSuggestion(item: TalentMarketImportPreviewItemDTO) {
    const conflictIndex = conflictItems.findIndex((candidate) => candidate.candidate_index === item.candidate_index)
    const suffix = conflictIndex >= 0 ? conflictIndex + 2 : 2
    return `${item.candidate.real_name} ${suffix}`
  }

  function conflictsFor(item: TalentMarketImportPreviewItemDTO) {
    return item.conflicts ?? preview?.conflicts?.filter((conflict) => conflict.candidate_index === item.candidate_index) ?? []
  }

  function sourceLabel(item: TalentMarketImportPreviewItemDTO) {
    return item.candidate.source_path || item.candidate.source_slug || item.candidate.agent_profile_id || item.candidate.real_name
  }

  function activeItemFile() {
    const importingItem = items.find((item) => itemStatuses[item.candidate_index]?.state === 'importing')
    return importingItem ? sourceLabel(importingItem) : items[0] ? sourceLabel(items[0]) : ''
  }

  function normalized(value: string | undefined) {
    return (value ?? '').trim().toLowerCase()
  }

  function buildPackageOverlaps(sourceItems: TalentMarketImportPreviewItemDTO[]): PackageOverlap[] {
    const groups = new Map<string, { type: OverlapType; value: string; files: string[] }>()

    for (const item of sourceItems) {
      addOverlapCandidate(groups, 'real_name', item.candidate.real_name, item)
      addOverlapCandidate(groups, 'display_name', item.candidate.display_name, item)
      addOverlapCandidate(groups, 'agent_profile_id', item.candidate.agent_profile_id, item)
    }

    return Array.from(groups.values())
      .filter((group) => group.files.length > 1)
      .map((group) => ({
        type: group.type,
        value: group.value,
        files: Array.from(new Set(group.files))
      }))
  }

  function addOverlapCandidate(
    groups: Map<string, { type: OverlapType; value: string; files: string[] }>,
    type: OverlapType,
    value: string | undefined,
    item: TalentMarketImportPreviewItemDTO
  ) {
    const keyValue = normalized(value)
    if (!keyValue) return
    const key = `${type}:${keyValue}`
    const group = groups.get(key) ?? { type, value: value?.trim() ?? keyValue, files: [] }
    group.files.push(sourceLabel(item))
    groups.set(key, group)
  }

  function statusFor(item: TalentMarketImportPreviewItemDTO): ImportItemStatus {
    const explicit = itemStatuses[item.candidate_index]
    if (explicit) return explicit
    if (item.status === 'conflict') return { state: 'conflict', message: t('people.importStatusConflict') }
    if ((phase === 'previewing' || phase === 'uploading') && sourceLabel(item) === currentFileName) {
      return { state: 'parsing', message: t('people.importStatusParsing') }
    }
    if (phase === 'committing' && sourceLabel(item) === currentFileName) {
      return { state: 'importing', message: t('people.importStatusImporting') }
    }
    return { state: 'ready', message: t('people.importStatusReady') }
  }

  function labelForPhase(value: string) {
    const labels: Record<string, string> = {
      idle: t('people.importPhaseIdle'),
      uploading: t('people.importPhaseUploading'),
      previewing: t('people.importPhasePreviewing'),
      ready: t('people.importPhaseReady'),
      committing: t('people.importPhaseCommitting'),
      complete: t('people.importPhaseComplete'),
      failed: t('people.importPhaseFailed')
    }
    return labels[value] ?? value
  }

  function labelForImportState(value: string) {
    const labels: Record<string, string> = {
      pending: t('people.importStatusPending'),
      parsing: t('people.importStatusParsing'),
      ready: t('people.importStatusReady'),
      importing: t('people.importStatusImporting'),
      imported: t('people.importStatusImported'),
      skipped: t('people.importStatusSkipped'),
      conflict: t('people.importStatusConflict'),
      failed: t('people.importStatusFailed')
    }
    return labels[value] ?? value
  }

  function toneForImportState(value: string) {
    if (value === 'imported') return 'success'
    if (value === 'failed') return 'danger'
    if (value === 'conflict') return 'warning'
    if (value === 'importing' || value === 'parsing') return 'info'
    return 'neutral'
  }

  function logTone(level: string) {
    if (level === 'success') return 'success'
    if (level === 'warning') return 'warning'
    if (level === 'error') return 'danger'
    return 'info'
  }

  function labelForLogLevel(level: string) {
    const labels: Record<string, string> = {
      info: t('people.importLogLevelInfo'),
      success: t('people.importLogLevelSuccess'),
      warning: t('people.importLogLevelWarning'),
      error: t('people.importLogLevelError')
    }
    return labels[level] ?? level
  }

  function labelForOverlapType(type: OverlapType) {
    const labels: Record<OverlapType, string> = {
      real_name: t('people.importOverlapTypeRealName'),
      display_name: t('people.importOverlapTypeDisplayName'),
      agent_profile_id: t('people.importOverlapTypeProfileId')
    }
    return labels[type]
  }

  function applyConflictResolution(resolution: string) {
    const nextResolutions = { ...resolutions }
    const nextRenamed = { ...renamed }
    for (const item of conflictItems) {
      nextResolutions[item.candidate_index] = resolution
      if (resolution === 'rename') {
        nextRenamed[item.candidate_index] = nextRenamed[item.candidate_index] || renameSuggestion(item)
      }
    }
    resolutions = nextResolutions
    renamed = nextRenamed
  }

  function submit() {
    const candidates = items.map((item) => {
      const resolution = resolutions[item.candidate_index] || (item.status === 'conflict' ? 'rename' : 'create')
      return {
        ...item.candidate,
        conflict_resolution: resolution,
        resolved_real_name: resolution === 'rename' ? renamed[item.candidate_index]?.trim() : undefined
      }
    })
    onsubmit?.(candidates)
  }
</script>

<div class="space-y-4">
  <section class="space-y-3 rounded-xl border border-border bg-card p-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <div class="text-sm font-semibold text-foreground">{labelForPhase(phase)}</div>
        <div class="mt-1 text-xs text-muted-foreground">
          {t('people.importCurrentFile', { file: currentFileLabel })}
        </div>
      </div>
      <Badge tone={phase === 'failed' ? 'danger' : phase === 'complete' ? 'success' : 'info'}>{labelForPhase(phase)}</Badge>
    </div>

    <div class="h-2 overflow-hidden rounded-full bg-muted">
      <div class="h-full rounded-full bg-brand transition-all duration-300" style={`width: ${Math.max(0, Math.min(progressPercent, 100))}%`}></div>
    </div>

    <div class="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
      <div class="rounded-md border border-border/70 bg-background p-3">
        <div class="text-sm font-semibold text-foreground">{t('people.importProgressTotal', { count: items.length })}</div>
      </div>
      <div class="rounded-md border border-border/70 bg-background p-3">
        <div class="text-sm font-semibold text-status-done">{t('people.importProgressImported', { count: importedCount })}</div>
      </div>
      <div class="rounded-md border border-border/70 bg-background p-3">
        <div class="text-sm font-semibold text-destructive">{t('people.importProgressFailed', { count: failedCount })}</div>
      </div>
      <div class="rounded-md border border-border/70 bg-background p-3">
        <div class="text-sm font-semibold text-muted-foreground">{t('people.importProgressSkipped', { count: skippedCount })}</div>
      </div>
      <div class="rounded-md border border-border/70 bg-background p-3">
        <div class="text-sm font-semibold text-status-blocked">{t('people.importProgressConflicts', { count: conflictCount })}</div>
      </div>
      <div class="rounded-md border border-border/70 bg-background p-3">
        <div class="text-sm font-semibold text-status-active">{t('people.importProgressOverlaps', { count: packageOverlaps.length })}</div>
      </div>
    </div>
  </section>

  {#if conflictCount > 0 || packageOverlaps.length > 0}
    <div class="grid gap-3 lg:grid-cols-2">
      {#if conflictCount > 0}
        <section class="rounded-lg border border-status-blocked/25 bg-status-blocked-bg/40 p-4">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h3 class="text-sm font-semibold text-foreground">{t('people.importConflictPanel')}</h3>
            <div class="flex flex-wrap items-center gap-2">
              <Button type="button" variant="outline" size="sm" disabled={busy || conflictItems.length === 0} onclick={() => applyConflictResolution('overwrite')}>
                {t('people.importBulkOverwrite')}
              </Button>
              <Button type="button" variant="outline" size="sm" disabled={busy || conflictItems.length === 0} onclick={() => applyConflictResolution('rename')}>
                {t('people.importBulkRename')}
              </Button>
              <Button type="button" variant="outline" size="sm" disabled={busy || conflictItems.length === 0} onclick={() => applyConflictResolution('skip')}>
                {t('people.importBulkSkip')}
              </Button>
            </div>
          </div>
          <div class="mt-2 space-y-1 text-xs text-muted-foreground">
            {#each preview?.conflicts ?? [] as conflict}
              <div>
                {t('people.importConflictLine', {
                  name: conflict.existing_real_name,
                  placement: conflict.existing_placement_status ?? '',
                  type: conflict.conflict_type
                })}
              </div>
            {/each}
          </div>
        </section>
      {/if}

      {#if packageOverlaps.length > 0}
        <section class="rounded-lg border border-status-active/25 bg-status-active-bg/35 p-4">
          <h3 class="text-sm font-semibold text-foreground">{t('people.importOverlapPanel')}</h3>
          <div class="mt-2 space-y-1 text-xs text-muted-foreground">
            {#each packageOverlaps as overlap}
              <div>
                {t('people.importOverlapLine', {
                  type: labelForOverlapType(overlap.type),
                  value: overlap.value,
                  files: overlap.files.join(', ')
                })}
              </div>
            {/each}
          </div>
        </section>
      {/if}
    </div>
  {/if}

  <section class="rounded-xl border border-border bg-card p-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-foreground">{t('people.importLog')}</h3>
      <div class="text-xs text-muted-foreground">
        {t('people.importPreviewSummary', { count: items.length, conflicts: conflictCount })}
      </div>
    </div>
    {#if logs.length > 0}
      <div class="mt-3 max-h-28 space-y-2 overflow-y-auto pr-1">
        {#each logs as log, index (`${index}-${log.message}`)}
          <div class="flex flex-wrap items-center gap-2 rounded-md bg-muted/35 px-3 py-2 text-xs">
            <Badge tone={logTone(log.level)}>{labelForLogLevel(log.level)}</Badge>
            {#if log.fileName}
              <span class="font-medium text-foreground">{log.fileName}</span>
            {/if}
            <span class="min-w-0 flex-1 text-muted-foreground">{log.message}</span>
          </div>
        {/each}
      </div>
    {:else}
      <p class="mt-3 text-xs text-muted-foreground">{t('people.importLogEmpty')}</p>
    {/if}
  </section>

  <div class="max-h-[42vh] space-y-3 overflow-y-auto pr-1">
    {#each items as item (item.candidate_index)}
      {@const itemConflicts = conflictsFor(item)}
      {@const resolution = resolutions[item.candidate_index] || 'create'}
      {@const itemStatus = statusFor(item)}
      {@const fileName = sourceLabel(item)}
      <section class="rounded-xl border border-border bg-card p-4">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <h3 class="truncate text-sm font-semibold text-foreground">{item.candidate.real_name}</h3>
              {#if item.candidate.category}
                <Badge>{item.candidate.category}</Badge>
              {/if}
              {#if item.status === 'conflict'}
                <Badge tone="warning">{t('people.importConflict')}</Badge>
              {:else}
                <Badge tone="success">{t('people.importCreate')}</Badge>
              {/if}
              <Badge tone={toneForImportState(itemStatus.state)}>{labelForImportState(itemStatus.state)}</Badge>
            </div>
            <p class="mt-1 line-clamp-2 text-xs text-muted-foreground">{item.candidate.description || item.candidate.role_name}</p>
            <p class="mt-1 text-xs text-muted-foreground">{t('people.importFileLabel', { file: fileName })}</p>
            {#if itemStatus.message}
              <p class="mt-1 text-xs font-medium text-foreground">{itemStatus.message}</p>
            {/if}
          </div>
          <select
            class="h-8 rounded-md border border-input bg-background px-2 text-xs"
            bind:value={resolutions[item.candidate_index]}
            disabled={busy}
          >
            <option value="create">{t('people.importActionCreate')}</option>
            <option value="overwrite" disabled={item.status !== 'conflict'}>{t('people.importActionOverwrite')}</option>
            <option value="rename">{t('people.importActionRename')}</option>
            <option value="skip">{t('people.importActionSkip')}</option>
          </select>
        </div>

        {#if itemConflicts.length > 0}
          <div class="mt-3 space-y-1 rounded-md bg-muted/40 p-3 text-xs text-muted-foreground">
            {#each itemConflicts as conflict}
              <div>
                {t('people.importConflictLine', {
                  name: conflict.existing_real_name,
                  placement: conflict.existing_placement_status ?? '',
                  type: conflict.conflict_type
                })}
              </div>
            {/each}
          </div>
        {/if}

        {#if resolution === 'rename'}
          <div class="mt-3 max-w-sm">
            <Input bind:value={renamed[item.candidate_index]} placeholder={t('people.importRenamePlaceholder')} disabled={busy} />
          </div>
        {/if}
      </section>
    {/each}
  </div>

  <div class="flex items-center justify-end gap-2 border-t border-border pt-4">
    <Button type="button" variant="outline" disabled={busy} onclick={() => oncancel?.()}>
      {t('organization.cancel')}
    </Button>
    <Button type="button" disabled={busy || items.length === 0} onclick={submit}>
      {busy ? t('people.importing') : t('people.importCommit')}
    </Button>
  </div>
</div>
