<script lang="ts">
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  import { Search } from 'lucide-svelte'
  import OntologyLineageInspector from '#lib/components/ontology/OntologyLineageInspector.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'
  import { useOntologyLineageSummaryQuery } from '#lib/queries/ontology-lineage'
  import type { components } from '#lib/types/api.generated'
  import { untrack } from 'svelte'

  type Ratio = components['schemas']['OntologyRatioMetricDTO']
  let factIdDraft = $state($page.url.searchParams.get('fact_id') ?? '')
  let factId = $state($page.url.searchParams.get('fact_id') ?? '')
  let boundTenant = $state('')
  const tenantId = () => ontologyScopeContext.mode === 'scope' ? ontologyScopeContext.activeScope?.tenant_id ?? '' : ''
  const scopeKey = () => `${ontologyScopeContext.mode}:${tenantId()}`
  const summary = useOntologyLineageSummaryQuery(tenantId)
  const metrics = $derived<Array<[string, Ratio, string]>>(summary.data ? [
    ['来源覆盖', summary.data.data.fact_provenance_coverage, '具有 provenance ref 的当前事实'],
    ['证据引用', summary.data.data.fact_evidence_reference_coverage, '至少具有一条 evidence ref 的当前事实'],
    ['可解析证据', summary.data.data.fact_resolvable_evidence_coverage, '至少具有一条可解析 evidence ref 的当前事实']
  ] : [])
  const sampledFactCount = $derived(metrics.reduce((largest, metric) => Math.max(largest, metric[1].denominator ?? 0), 0))
  const hasSampledMetrics = $derived(metrics.some((metric) => metric[1].status === 'sampled') || summary.data?.data.confidence_distribution.status === 'sampled' || summary.data?.data.freshness.status === 'sampled')

  $effect(() => {
    const activeTenant = tenantId()
    const activeScopeKey = scopeKey()
    const url = $page.url
    untrack(() => {
      const restoredFact = ontologyScopeContext.mode === 'scope' && activeTenant === (url.searchParams.get('tenant_id') ?? '').trim() ? (url.searchParams.get('fact_id') ?? '') : ''
      if (boundTenant === activeScopeKey && factId === restoredFact) return
      boundTenant = activeScopeKey
      factId = restoredFact
      factIdDraft = restoredFact
    })
  })
  function inspectFact() { factId = factIdDraft.trim(); if (!factId) return; const params = new URLSearchParams({ tenant_id: tenantId(), fact_id: factId }); void goto(`/resources/ontology/lineage?${params.toString()}`, { invalidateAll: false }) }
  function ratioLabel(metric: Ratio) { return metric.status !== 'unavailable' && metric.denominator !== undefined ? `${metric.numerator ?? 0} / ${metric.denominator}` : '不可用' }
  function ratioPercent(metric: Ratio) { return metric.status !== 'unavailable' && metric.value !== undefined ? `${Math.round(metric.value * 100)}%` : '—' }
</script>

<section class="space-y-4">
  <header><h2 class="text-xl font-semibold">来源与质量</h2><p class="mt-1 text-sm text-muted-foreground">事实来源覆盖、证据可解析性、置信度完整性和数据新鲜度。</p></header>
  {#if hasSampledMetrics}<p class="border-l-2 border-amber-500 pl-3 text-xs text-muted-foreground">基于最近 {sampledFactCount} 条可见事实样本，数值不代表全量精确统计。</p>{/if}
  {#if summary.isPending}<LoadingState />{:else if summary.error}<ErrorState message={summary.error.message} />{:else if !summary.data}<EmptyState title="暂无 Lineage 数据" />{:else}<div class="grid gap-4 md:grid-cols-3">{#each metrics as metric}<article class="border-l-2 border-border pl-4"><p class="text-xs text-muted-foreground">{metric[0]}</p><div class="mt-2 flex items-baseline justify-between gap-3"><p class="text-2xl font-semibold">{ratioPercent(metric[1])}</p><p class="text-sm text-muted-foreground">{ratioLabel(metric[1])}</p></div><p class="mt-2 text-xs text-muted-foreground">{metric[2]}</p></article>{/each}</div>
    <div class="grid gap-4 border-y border-border py-4 lg:grid-cols-2"><section><h3 class="text-sm font-medium">置信度原值分布</h3>{#if summary.data.data.confidence_distribution.status !== 'unavailable'}<dl class="mt-3 grid grid-cols-2 gap-3 text-sm sm:grid-cols-4"><div><dt class="text-xs text-muted-foreground">低</dt><dd class="mt-1 text-lg font-semibold">{summary.data.data.confidence_distribution.low ?? 0}</dd></div><div><dt class="text-xs text-muted-foreground">中</dt><dd class="mt-1 text-lg font-semibold">{summary.data.data.confidence_distribution.medium ?? 0}</dd></div><div><dt class="text-xs text-muted-foreground">高</dt><dd class="mt-1 text-lg font-semibold">{summary.data.data.confidence_distribution.high ?? 0}</dd></div><div><dt class="text-xs text-muted-foreground">未标注</dt><dd class="mt-1 text-lg font-semibold">{summary.data.data.confidence_distribution.unscored ?? 0}</dd></div></dl>{:else}<p class="mt-2 text-sm text-muted-foreground">不可用</p>{/if}</section><section><h3 class="text-sm font-medium">更新时间年龄分布</h3>{#if summary.data.data.freshness.status !== 'unavailable'}<dl class="mt-3 grid grid-cols-3 gap-3 text-sm"><div><dt class="text-xs text-muted-foreground">24 小时内</dt><dd class="mt-1 text-lg font-semibold">{summary.data.data.freshness.last_24h ?? 0}</dd></div><div><dt class="text-xs text-muted-foreground">1–7 天</dt><dd class="mt-1 text-lg font-semibold">{summary.data.data.freshness.days_1_7 ?? 0}</dd></div><div><dt class="text-xs text-muted-foreground">更早</dt><dd class="mt-1 text-lg font-semibold">{summary.data.data.freshness.older ?? 0}</dd></div></dl>{:else}<p class="mt-2 text-sm text-muted-foreground">不可用</p>{/if}</section></div>{/if}
  {#if ontologyScopeContext.mode === 'scope'}
    <form
      class="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end"
      onsubmit={(event) => {
        event.preventDefault()
        inspectFact()
      }}
    >
      <label class="grid gap-1.5">
        <span class="text-xs font-medium text-muted-foreground">事实 ID</span>
        <span class="relative">
          <Search class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <Input value={factIdDraft} class="pl-9" ariaLabel="事实 ID" oninput={(event) => (factIdDraft = event.currentTarget.value)} />
        </span>
      </label>
      <Button type="submit" disabled={!factIdDraft.trim()}>查看来源</Button>
    </form>
    {#if factId}<OntologyLineageInspector tenantId={tenantId()} {factId} />{:else}<EmptyState title="选择事实查看来源链路" />{/if}
  {:else}
    <EmptyState title="选择具体范围后查询事实详情" />
  {/if}
</section>
