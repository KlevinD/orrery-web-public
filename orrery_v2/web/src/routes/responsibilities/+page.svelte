<script lang="ts">
  import { page } from '$app/stores'
  import { AlertTriangle, Clock3, RefreshCw, Search, ShieldCheck, TimerReset } from 'lucide-svelte'
  import ResponsibilityDetailPanel from '#lib/components/responsibility/ResponsibilityDetailPanel.svelte'
  import ResponsibilityInbox from '#lib/components/responsibility/ResponsibilityInbox.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import Tabs from '#lib/components/ui/Tabs.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { hasPermissionScope } from '#lib/permissions/access'
  import {
    useResponsibilityCommandMutations,
    useResponsibilityDetailQuery,
    useResponsibilityInboxQuery,
    useResponsibilityPolicyQuery
  } from '#lib/queries/responsibilities'
  import { durationLabel, filterResponsibilities, percentage } from '#lib/responsibility/view-model'
  import { session } from '#lib/stores/session.svelte'
  import type { ApiClientError } from '#lib/api/client'
  import type {
    ResponsibilityControlAction,
    ResponsibilityControlRequest,
    ResponsibilityInboxItemDTO,
    ResponsibilityLane,
    ResponsibilityPolicyUpdateRequest,
    ResponsibilityWorkKind
  } from '#lib/types/responsibility'

  let t = $derived(createTranslator($locale))
  let selectedLane = $state<ResponsibilityLane | 'all'>('all')
  let selectedId = $state('')
  let search = $state('')
  let agentFilter = $state('')
  let lastAutoSelectedSet = $state('')
  let handledRequestedResponsibility = $state('')

  const inboxQuery = useResponsibilityInboxQuery(() => ({ limit: 500 }))
  const detailQuery = useResponsibilityDetailQuery(() => selectedId)
  const selectedSummary = $derived(
    (inboxQuery.data?.items ?? []).find((item) => item.responsibility_id === selectedId) ?? detailQuery.data
  )
  const selectedWorkKind = $derived((selectedSummary?.work_kind ?? 'mission_task') as ResponsibilityWorkKind)
  const policyQuery = useResponsibilityPolicyQuery(
    () => selectedSummary?.accountable_agent ?? '',
    () => selectedWorkKind
  )
  const commands = useResponsibilityCommandMutations()

  const allItems = $derived(inboxQuery.data?.items ?? [])
  const agentOptions = $derived([
    { value: '', label: t('responsibility.allAgents') },
    ...Array.from(new Set(allItems.map((item) => item.accountable_agent))).sort().map((agent) => ({ value: agent, label: agent }))
  ])
  const agentItems = $derived(agentFilter ? allItems.filter((item) => item.accountable_agent === agentFilter) : allItems)
  const visibleItems = $derived(filterResponsibilities(agentItems, selectedLane, search))
  const summary = $derived(inboxQuery.data?.summary ?? {
    total: 0, now: 0, next: 0, waiting: 0, review: 0, risk: 0, paused: 0, overdue: 0, silent_stalls: 0, repeated_failures: 0
  })
  const metrics = $derived(inboxQuery.data?.metrics ?? {
    assignment_to_accept_seconds: 0, autonomous_closure_rate: 0, silent_stall_rate: 0,
    average_blocked_seconds: 0, first_pass_acceptance_rate: 0, evidence_completeness: 0, duplicate_side_effect_rate: 0
  })
  const laneTabs = $derived([
    { value: 'all', label: `${t('responsibility.lane.all')} ${summary.total}` },
    { value: 'now', label: `${t('responsibility.lane.now')} ${summary.now}` },
    { value: 'next', label: `${t('responsibility.lane.next')} ${summary.next}` },
    { value: 'waiting', label: `${t('responsibility.lane.waiting')} ${summary.waiting}` },
    { value: 'review', label: `${t('responsibility.lane.review')} ${summary.review}` },
    { value: 'risk', label: `${t('responsibility.lane.risk')} ${summary.risk}` }
  ])
  const canManage = $derived(hasPermissionScope(session.permissionScope, 'operations.manage'))
  const pending = $derived(commands.control.isPending || commands.resolveBlocker.isPending || commands.updatePolicy.isPending)

  $effect(() => {
    const requested = $page.url.searchParams.get('responsibility')?.trim() ?? ''
    if (!requested || requested === handledRequestedResponsibility) return
    handledRequestedResponsibility = requested
    selectedLane = 'all'
    search = ''
    agentFilter = ''
    selectedId = requested
  })

  $effect(() => {
    const ids = visibleItems.map((item) => item.responsibility_id).join('|')
    if (!ids || ids === lastAutoSelectedSet) return
    lastAutoSelectedSet = ids
    const requested = $page.url.searchParams.get('responsibility')?.trim() ?? ''
    if (requested && selectedId === requested) return
    if (!visibleItems.some((item) => item.responsibility_id === selectedId)) selectedId = visibleItems[0]?.responsibility_id ?? ''
  })

  function selectItem(item: ResponsibilityInboxItemDTO) {
    selectedId = item.responsibility_id
  }

  async function control(action: ResponsibilityControlAction, body: ResponsibilityControlRequest) {
    const detail = detailQuery.data
    if (!detail) throw new Error(t('responsibility.control.failed'))
    await commands.control.mutateAsync({ responsibilityId: detail.responsibility_id, action, version: detail.version, body })
  }

  async function resolveBlocker(blockerId: string, version: number, resolution: string) {
    const detail = detailQuery.data
    if (!detail) throw new Error(t('responsibility.control.failed'))
    await commands.resolveBlocker.mutateAsync({
      responsibilityId: detail.responsibility_id,
      blockerId,
      version,
      body: { resolution }
    })
  }

  async function savePolicy(body: ResponsibilityPolicyUpdateRequest) {
    const item = selectedSummary
    const policy = policyQuery.data ?? detailQuery.data?.policy
    if (!item || !policy) throw new Error(t('responsibility.policy.failed'))
    await commands.updatePolicy.mutateAsync({
      agentId: item.accountable_agent,
      workKind: item.work_kind,
      version: policy.version,
      body
    })
  }

  function formatUpdated(value?: string) {
    if (!value) return '—'
    const date = new Date(value)
    if (Number.isNaN(date.valueOf())) return value
    return new Intl.DateTimeFormat($locale, { dateStyle: 'medium', timeStyle: 'short' }).format(date)
  }
</script>

<svelte:head><title>{t('responsibility.title')} | Orrery</title></svelte:head>

<div class="space-y-5" data-responsibility-page>
  <header class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
    <div>
      <h1 class="page-title">{t('responsibility.title')}</h1>
      <p class="page-description">{t('responsibility.description')}</p>
      {#if inboxQuery.data}<p class="mt-1 text-xs text-muted-foreground">{t('responsibility.updated', { time: formatUpdated(inboxQuery.data.updated_at) })}</p>{/if}
    </div>
    <Button variant="outline" disabled={inboxQuery.isFetching} onclick={() => inboxQuery.refetch()}>
      <RefreshCw class="h-4 w-4 {inboxQuery.isFetching ? 'animate-spin' : ''}" />{t('responsibility.refresh')}
    </Button>
  </header>

  {#if inboxQuery.isLoading}
    <div class="min-h-80 rounded-md border border-border bg-card"><LoadingState title={t('responsibility.loading')} description=" " /></div>
  {:else if inboxQuery.isError}
    {@const error = inboxQuery.error as ApiClientError}
    <ErrorState title={t('responsibility.loadError')} message={error.message} correlationId={error.correlationId} />
  {:else}
    <section class="overflow-hidden rounded-md border border-border bg-card">
      <div class="grid divide-y divide-border sm:grid-cols-2 sm:divide-x sm:divide-y-0 xl:grid-cols-4">
        {@render SummaryItem(ShieldCheck, t('responsibility.summary.total'), summary.total, 'text-status-done')}
        {@render SummaryItem(Clock3, t('responsibility.summary.overdue'), summary.overdue, 'text-status-blocked')}
        {@render SummaryItem(TimerReset, t('responsibility.summary.stalls'), summary.silent_stalls, 'text-destructive')}
        {@render SummaryItem(AlertTriangle, t('responsibility.summary.failures'), summary.repeated_failures, 'text-destructive')}
      </div>
      <div class="grid border-t border-border bg-muted/15 sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-7">
        {@render Metric(t('responsibility.metrics.accept'), durationLabel(metrics.assignment_to_accept_seconds))}
        {@render Metric(t('responsibility.metrics.closure'), percentage(metrics.autonomous_closure_rate))}
        {@render Metric(t('responsibility.metrics.stall'), percentage(metrics.silent_stall_rate))}
        {@render Metric(t('responsibility.metrics.blocked'), durationLabel(metrics.average_blocked_seconds))}
        {@render Metric(t('responsibility.metrics.firstPass'), percentage(metrics.first_pass_acceptance_rate))}
        {@render Metric(t('responsibility.metrics.evidence'), percentage(metrics.evidence_completeness))}
        {@render Metric(t('responsibility.metrics.duplicates'), percentage(metrics.duplicate_side_effect_rate))}
      </div>
    </section>

    <section class="overflow-hidden rounded-md border border-border bg-card">
      <div class="flex flex-col gap-3 border-b border-border px-4 py-3 xl:flex-row xl:items-center xl:justify-between">
        <div class="max-w-full overflow-x-auto"><Tabs bind:value={selectedLane} tabs={laneTabs} /></div>
        <div class="flex flex-col gap-2 sm:flex-row">
          <div class="relative min-w-0 sm:w-72">
            <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input bind:value={search} class="pl-9" placeholder={t('responsibility.search')} ariaLabel={t('responsibility.search')} />
          </div>
          <div class="sm:w-56"><Select bind:value={agentFilter} options={agentOptions} /></div>
        </div>
      </div>

      <div class="grid min-h-[620px] lg:grid-cols-[390px_minmax(0,1fr)] xl:grid-cols-[430px_minmax(0,1fr)]">
        <aside class="max-h-[760px] overflow-y-auto border-b border-border lg:border-b-0 lg:border-r">
          {#if visibleItems.length === 0}
            <div class="p-6"><EmptyState title={t('responsibility.empty')} description={t('responsibility.emptyDescription')} /></div>
          {:else}
            <ResponsibilityInbox items={visibleItems} selectedId={selectedId} onSelect={selectItem} />
          {/if}
        </aside>
        <div class="min-w-0">
          {#if !selectedId}
            <div class="flex min-h-[620px] items-center justify-center p-6"><EmptyState title={t('responsibility.select')} description=" " /></div>
          {:else if detailQuery.isLoading}
            <div class="min-h-[620px]"><LoadingState title={t('responsibility.loading')} description=" " /></div>
          {:else if detailQuery.isError}
            {@const error = detailQuery.error as ApiClientError}
            <div class="p-5"><ErrorState title={t('responsibility.loadError')} message={error.message} correlationId={error.correlationId} /></div>
          {:else if detailQuery.data}
            <ResponsibilityDetailPanel
              detail={detailQuery.data}
              policy={policyQuery.data}
              {canManage}
              {pending}
              onControl={control}
              onResolveBlocker={resolveBlocker}
              onSavePolicy={savePolicy}
            />
          {/if}
        </div>
      </div>
    </section>
  {/if}
</div>

{#snippet SummaryItem(icon: typeof ShieldCheck, label: string, value: number, tone: string)}
  {@const Icon = icon}
  <div class="flex min-h-24 items-center gap-3 px-4 py-3">
    <span class="grid h-9 w-9 shrink-0 place-items-center rounded-md bg-muted {tone}"><Icon class="h-4 w-4" /></span>
    <span><span class="block text-xs text-muted-foreground">{label}</span><span class="mt-1 block text-xl font-semibold text-foreground">{value}</span></span>
  </div>
{/snippet}

{#snippet Metric(label: string, value: string)}
  <div class="min-h-16 border-b border-border px-3 py-3 last:border-b-0 sm:border-r lg:border-b-0">
    <div class="text-[11px] text-muted-foreground">{label}</div><div class="mt-1 text-sm font-semibold text-foreground">{value}</div>
  </div>
{/snippet}
