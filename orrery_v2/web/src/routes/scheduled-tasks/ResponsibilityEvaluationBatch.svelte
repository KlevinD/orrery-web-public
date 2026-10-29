<script lang="ts">
  import {
    AlertTriangle,
    ChevronDown,
    ChevronLeft,
    ChevronRight,
    ListChecks,
    RefreshCw
  } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import type { ApiClientError } from '#lib/api/client'
  import { createTranslator, locale } from '#lib/i18n'
  import { useResponsibilityEvaluationItemsQuery } from '#lib/queries/scheduler'
  import type {
    ResponsibilityEvaluationBatchResultDTO,
    ResponsibilityEvaluationItemDTO,
    ResponsibilityEvaluationItemStatus
  } from '#lib/types/scheduler'

  interface Props {
    scheduledTaskID: string
    scheduledRunID: string
    batch: ResponsibilityEvaluationBatchResultDTO
  }

  let { scheduledTaskID, scheduledRunID, batch }: Props = $props()
  let open = $state(false)
  let status = $state<ResponsibilityEvaluationItemStatus | ''>('')
  let cursorStack = $state<string[]>([''])
  let pageIndex = $state(0)
  let t = $derived(createTranslator($locale))
  let cursor = $derived(cursorStack[pageIndex] ?? '')

  const itemsQuery = useResponsibilityEvaluationItemsQuery(
    () => scheduledTaskID,
    () => scheduledRunID,
    () => ({ status, cursor, limit: 20 }),
    () => open
  )
  let items = $derived(itemsQuery.data?.items ?? [])

  function changeStatus(event: Event & { currentTarget: HTMLSelectElement }) {
    status = event.currentTarget.value as ResponsibilityEvaluationItemStatus | ''
    cursorStack = ['']
    pageIndex = 0
  }

  function previousPage() {
    if (pageIndex > 0) pageIndex -= 1
  }

  function nextPage() {
    const nextCursor = itemsQuery.data?.next_cursor?.trim() ?? ''
    if (!itemsQuery.data?.has_more || !nextCursor) return
    const nextIndex = pageIndex + 1
    cursorStack = [...cursorStack.slice(0, nextIndex), nextCursor]
    pageIndex = nextIndex
  }

  function itemStatusTone(status: string): 'neutral' | 'success' | 'warning' | 'danger' | 'info' {
    const normalized = status.trim().toLowerCase()
    if (normalized.includes('fail') || normalized.includes('error')) return 'danger'
    if (normalized.includes('skip') || normalized.includes('stale')) return 'warning'
    if (normalized.includes('run') || normalized.includes('pending')) return 'info'
    if (normalized.includes('complete') || normalized.includes('success') || normalized.includes('evaluated')) return 'success'
    return 'neutral'
  }

  function itemStatusLabel(status: string) {
    const normalized = status.trim().toLowerCase()
    if (normalized.includes('fail') || normalized.includes('error')) return t('context.responsibilityEvaluation.batch.statusFailed')
    if (normalized.includes('skip') || normalized.includes('stale')) return t('context.responsibilityEvaluation.batch.statusSkipped')
    if (normalized.includes('run') || normalized.includes('pending')) return t('context.responsibilityEvaluation.batch.statusRunning')
    if (normalized.includes('complete') || normalized.includes('success') || normalized.includes('evaluated')) return t('context.responsibilityEvaluation.batch.statusCompleted')
    return status || t('context.responsibilityEvaluation.batch.statusUnknown')
  }

  function decisionLabel(item: ResponsibilityEvaluationItemDTO) {
    switch (item.evaluation?.decision_kind) {
      case 'no_action':
        return t('context.responsibilityEvaluation.decision.no_action')
      case 'link_existing_mission':
        return t('context.responsibilityEvaluation.decision.link_existing_mission')
      case 'materialize_action':
        return t('context.responsibilityEvaluation.decision.materialize_action')
      default:
        return ''
    }
  }

  function formatDateTime(value?: string) {
    if (!value) return '-'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return new Intl.DateTimeFormat($locale, {
      dateStyle: 'medium',
      timeStyle: 'short'
    }).format(date)
  }
</script>

<section class="mt-3 border-y border-border/80 py-3" data-responsibility-evaluation-batch>
  <div class="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
    <h4 class="text-xs font-semibold text-foreground">{t('context.responsibilityEvaluation.batch.title')}</h4>
    <span class="min-w-0 truncate font-mono text-[11px] text-muted-foreground" title={batch.scope_id}>
      {t('context.responsibilityEvaluation.batch.scope')} {batch.scope_id}
    </span>
  </div>

  <dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-4 lg:grid-cols-6">
    <div><dt>{t('context.responsibilityEvaluation.batch.discovered')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.discovered_count}</dd></div>
    <div><dt>{t('context.responsibilityEvaluation.batch.evaluated')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.evaluated_count}</dd></div>
    <div><dt>{t('context.responsibilityEvaluation.batch.noAction')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.no_action_count}</dd></div>
    <div><dt>{t('context.responsibilityEvaluation.batch.linked')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.linked_existing_mission_count}</dd></div>
    <div><dt>{t('context.responsibilityEvaluation.batch.materialized')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.materialized_count}</dd></div>
    <div><dt>{t('context.responsibilityEvaluation.batch.failed')}</dt><dd class="mt-0.5 text-sm font-semibold" class:text-destructive={batch.failed_count > 0} class:text-foreground={batch.failed_count === 0}>{batch.failed_count}</dd></div>
    <div><dt>{t('context.responsibilityEvaluation.batch.skipped')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.skipped_count}</dd></div>
    <div><dt>{t('context.responsibilityEvaluation.batch.candidates')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.candidate_count}</dd></div>
    <div><dt>{t('context.responsibilityEvaluation.batch.actionable')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.actionable_count}</dd></div>
    <div><dt>{t('context.responsibilityEvaluation.batch.waiting')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.waiting_count}</dd></div>
    <div><dt>{t('context.responsibilityEvaluation.batch.review')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.review_count}</dd></div>
    <div><dt>{t('context.responsibilityEvaluation.batch.escalated')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.escalated_count}</dd></div>
    {#if batch.paused_count > 0}<div><dt>{t('context.responsibilityEvaluation.batch.paused')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.paused_count}</dd></div>{/if}
    {#if batch.invalid_count > 0}<div><dt>{t('context.responsibilityEvaluation.batch.invalid')}</dt><dd class="mt-0.5 text-sm font-semibold text-foreground">{batch.invalid_count}</dd></div>{/if}
  </dl>

  <details class="mt-3 border-t border-border/70 pt-1" bind:open>
    <summary class="flex min-h-9 cursor-pointer list-none items-center gap-2 py-2 font-medium text-foreground hover:text-primary">
      <ListChecks class="h-4 w-4" aria-hidden="true" />
      <span>{t('context.responsibilityEvaluation.batch.details')}</span>
      <ChevronDown class="ml-auto h-4 w-4 transition-transform {open ? 'rotate-180' : ''}" aria-hidden="true" />
    </summary>

    <div class="pb-1 pt-2" data-responsibility-evaluation-items>
      <div class="flex flex-wrap items-center justify-between gap-2 pb-2">
        <label class="w-40">
          <span class="sr-only">{t('context.responsibilityEvaluation.batch.filter')}</span>
          <Select
            value={status}
            options={[
              { label: t('context.responsibilityEvaluation.batch.filterAll'), value: '' },
              { label: t('context.responsibilityEvaluation.batch.statusCompleted'), value: 'completed' },
              { label: t('context.responsibilityEvaluation.batch.statusFailed'), value: 'failed' }
            ]}
            onchange={changeStatus}
          />
        </label>
        <Button
          variant="ghost"
          size="icon"
          ariaLabel={t('context.responsibilityEvaluation.batch.refresh')}
          title={t('context.responsibilityEvaluation.batch.refresh')}
          disabled={itemsQuery.isFetching}
          onclick={() => itemsQuery.refetch()}
        >
          <RefreshCw class="h-4 w-4 {itemsQuery.isFetching ? 'animate-spin' : ''}" />
        </Button>
      </div>

      {#if itemsQuery.isLoading}
        <div class="flex min-h-24 items-center gap-3 border-t border-border/70 py-4 text-muted-foreground">
          <span class="h-4 w-4 animate-spin rounded-full border-2 border-brand border-t-transparent"></span>
          <span>{t('context.responsibilityEvaluation.batch.loading')}</span>
        </div>
      {:else if itemsQuery.isError}
        {@const error = itemsQuery.error as ApiClientError}
        <div class="flex min-h-24 items-start gap-3 border-t border-destructive/30 py-4 text-destructive">
          <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" />
          <div class="min-w-0 flex-1">
            <div class="font-medium">{t('context.responsibilityEvaluation.batch.loadFailed')}</div>
            <div class="mt-1 break-words text-[11px]">{error.message}</div>
          </div>
          <Button variant="outline" size="sm" onclick={() => itemsQuery.refetch()}>{t('context.responsibilityEvaluation.batch.retry')}</Button>
        </div>
      {:else if items.length === 0}
        <div class="border-t border-border/70 py-6 text-center text-muted-foreground">
          {t('context.responsibilityEvaluation.batch.empty')}
        </div>
      {:else}
        <div>
          {#each items as item (item.item_id)}
            <section class="border-t border-border/70 py-3" data-responsibility-evaluation-item={item.item_id}>
              <div class="flex flex-wrap items-center justify-between gap-2">
                <div class="min-w-0">
                  <div class="truncate text-xs font-semibold text-foreground" title={item.agent_profile_id}>{item.agent_profile_id}</div>
                  <div class="mt-0.5 truncate font-mono text-[10px] text-muted-foreground" title={item.portfolio_binding_id}>{item.portfolio_binding_id}</div>
                </div>
                <div class="flex items-center gap-2">
                  <span class="text-[10px] text-muted-foreground">{t('context.responsibilityEvaluation.batch.attempt')} {item.attempt}</span>
                  <Badge tone={itemStatusTone(item.status)}>{itemStatusLabel(item.status)}</Badge>
                </div>
              </div>

              <div class="mt-2 grid gap-x-4 gap-y-1 text-[11px] text-muted-foreground sm:grid-cols-2 lg:grid-cols-4">
                <span class="truncate" title={item.portfolio_version_id}>{t('context.responsibilityEvaluation.batch.version')} <span class="font-mono text-foreground">{item.portfolio_version_id}</span></span>
                {#if item.evaluation}<span class="text-foreground">{decisionLabel(item)}</span>{/if}
                {#if item.evaluation?.reason}<span class="break-words">{item.evaluation.reason}</span>{/if}
                <span>{t('context.responsibilityEvaluation.batch.updated')} {formatDateTime(item.completed_at ?? item.updated_at)}</span>
              </div>

              {#if item.evaluation}
                <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
                  <span>{t('context.responsibilityEvaluation.candidates', { count: item.evaluation.candidate_count })}</span>
                  <span>{t('context.responsibilityEvaluation.runnable', { count: item.evaluation.actionable_count })}</span>
                  <span>{t('context.responsibilityEvaluation.waiting', { count: item.evaluation.waiting_count })}</span>
                  <span>{t('context.responsibilityEvaluation.review', { count: item.evaluation.review_count })}</span>
                </div>
              {/if}

              {#if item.failure_message || item.failure_code}
                <div class="mt-2 break-words border-l-2 border-destructive pl-2 text-[11px] text-destructive">
                  {#if item.failure_code}<span class="font-mono">{item.failure_code}</span>{/if}
                  {#if item.failure_message}<span>{item.failure_code ? ' / ' : ''}{item.failure_message}</span>{/if}
                </div>
              {/if}

              {#if item.mission_id || item.responsibility_id || item.task_id}
                <div class="mt-3 flex flex-wrap items-center gap-2">
                  {#if item.mission_id}<Button href={`/missions/${encodeURIComponent(item.mission_id)}`} variant="outline" size="sm">Mission</Button>{/if}
                  {#if item.responsibility_id}<Button href={`/responsibilities?responsibility=${encodeURIComponent(item.responsibility_id)}`} variant="outline" size="sm">Responsibility</Button>{/if}
                  {#if item.task_id}<span class="font-mono text-[10px] text-muted-foreground">Task {item.task_id}</span>{/if}
                </div>
              {/if}
            </section>
          {/each}
        </div>

        <div class="flex items-center justify-end gap-2 border-t border-border/70 pt-3">
          <Button
            variant="outline"
            size="icon"
            ariaLabel={t('context.responsibilityEvaluation.batch.previous')}
            title={t('context.responsibilityEvaluation.batch.previous')}
            disabled={pageIndex === 0 || itemsQuery.isFetching}
            onclick={previousPage}
          ><ChevronLeft class="h-4 w-4" /></Button>
          <span class="min-w-16 text-center text-[11px] text-muted-foreground">{t('context.responsibilityEvaluation.batch.page', { page: pageIndex + 1 })}</span>
          <Button
            variant="outline"
            size="icon"
            ariaLabel={t('context.responsibilityEvaluation.batch.next')}
            title={t('context.responsibilityEvaluation.batch.next')}
            disabled={!itemsQuery.data?.has_more || !itemsQuery.data?.next_cursor || itemsQuery.isFetching}
            onclick={nextPage}
          ><ChevronRight class="h-4 w-4" /></Button>
        </div>
      {/if}
    </div>
  </details>
</section>
