<script lang="ts">
  import Badge from '#lib/components/ui/Badge.svelte'
  import OntologySummaryError from '#lib/components/ontology/OntologySummaryError.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'
  import { useOntologySchemaSummaryQuery } from '#lib/queries/ontology-schema'
  import { useOntologyContextCompanyOverviewQuery, useOntologyContextOverviewQuery } from '#lib/queries/ontology-context'
  import { useOntologyLineageSummaryQuery } from '#lib/queries/ontology-lineage'
  import { useOntologyGovernanceSummaryQuery } from '#lib/queries/ontology-governance'
  import { useOntologyProjectionSummaryQuery } from '#lib/queries/ontology-projections'

  const tenantId = () => ontologyScopeContext.mode === 'scope' ? ontologyScopeContext.activeScope?.tenant_id ?? '' : ''
  const schema = useOntologySchemaSummaryQuery(tenantId)
  const companyContext = useOntologyContextCompanyOverviewQuery(() => ontologyScopeContext.mode === 'all')
  const scopedContext = useOntologyContextOverviewQuery(tenantId, () => ontologyScopeContext.mode === 'scope')
  const context = $derived(ontologyScopeContext.mode === 'all' ? companyContext : scopedContext)
  const lineage = useOntologyLineageSummaryQuery(tenantId)
  const governance = useOntologyGovernanceSummaryQuery(tenantId)
  const projections = useOntologyProjectionSummaryQuery(tenantId)

  function metric(metric: { status: string; value?: number } | undefined) {
    return metric?.status === 'exact' && metric.value !== undefined ? String(metric.value) : '—'
  }
  function ratio(metric: { status: string; numerator?: number; denominator?: number; value?: number } | undefined) {
    if (!metric || metric.status !== 'exact' || metric.numerator === undefined || metric.denominator === undefined || metric.denominator === 0) return '—'
    return `${metric.numerator}/${metric.denominator}`
  }
  function scopedPath(path: string) {
    return tenantId() ? `${path}?tenant_id=${encodeURIComponent(tenantId())}` : path
  }
  const scopeLabel = $derived(ontologyScopeContext.mode === 'all' ? '全部可见范围' : ontologyScopeContext.activeScope?.kind === 'system' ? '全局本体' : ontologyScopeContext.activeScope?.kind ?? '范围')
</script>

<div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
  <article class="rounded-xl border border-border bg-card p-5"><div class="flex items-center justify-between"><a class="font-semibold hover:underline" href={scopedPath('/resources/ontology/schema')}>本体定义</a><Badge tone="info">{scopeLabel}</Badge></div>{#if schema.isPending}<LoadingState />{:else if schema.error}<OntologySummaryError message={schema.error.message} onretry={() => void schema.refetch()} />{:else}<dl class="mt-4 grid gap-3 sm:grid-cols-2">{#each [['对象类型', metric(schema.data?.data.object_types)], ['关系类型', metric(schema.data?.data.link_types)], ['属性类型', metric(schema.data?.data.property_types)], ['映射规则', metric(schema.data?.data.mapping_rules)], ['动作类型', metric(schema.data?.data.action_types)]] as item}<div><dt class="text-xs text-muted-foreground">{item[0]}</dt><dd class="mt-1 text-2xl font-semibold">{item[1]}</dd></div>{/each}</dl><p class="mt-4 text-xs text-muted-foreground">统计于 {schema.data?.meta.as_of ?? '—'}</p>{/if}</article>
  <article class="rounded-xl border border-border bg-card p-5"><div class="flex items-center justify-between"><a class="font-semibold hover:underline" href={scopedPath('/resources/ontology/context')}>实际数据</a><Badge tone="info">{scopeLabel}</Badge></div>{#if context.isPending}<LoadingState />{:else if context.error}<OntologySummaryError message={context.error.message} onretry={() => void context.refetch()} />{:else}<dl class="mt-4 grid gap-3 sm:grid-cols-2">{#each [['对象', metric(context.data?.data.objects)], ['关系', metric(context.data?.data.relations)], ['事实', metric(context.data?.data.facts)], ['证据', metric(context.data?.data.evidence)]] as item}<div><dt class="text-xs text-muted-foreground">{item[0]}</dt><dd class="mt-1 text-2xl font-semibold">{item[1]}</dd></div>{/each}</dl><p class="mt-4 text-xs text-muted-foreground">统计于 {context.data?.meta.as_of ?? '—'}</p>{/if}</article>
  <article class="rounded-xl border border-border bg-card p-5"><div class="flex items-center justify-between"><a class="font-semibold hover:underline" href={scopedPath('/resources/ontology/lineage')}>来源与质量</a><Badge tone="info">{scopeLabel}</Badge></div>{#if lineage.isPending}<LoadingState />{:else if lineage.error}<OntologySummaryError message={lineage.error.message} onretry={() => void lineage.refetch()} />{:else}<dl class="mt-4 grid gap-3 sm:grid-cols-2">{#each [['事实来源', ratio(lineage.data?.data.fact_provenance_coverage)], ['证据引用', ratio(lineage.data?.data.fact_evidence_reference_coverage)], ['可解析证据', ratio(lineage.data?.data.fact_resolvable_evidence_coverage)]] as item}<div><dt class="text-xs text-muted-foreground">{item[0]}</dt><dd class="mt-1 text-2xl font-semibold">{item[1]}</dd></div>{/each}</dl><p class="mt-4 text-xs text-muted-foreground">统计于 {lineage.data?.meta.as_of ?? '—'}</p>{/if}</article>
  <article class="rounded-xl border border-border bg-card p-5"><div class="flex items-center justify-between"><a class="font-semibold hover:underline" href={scopedPath('/resources/ontology/governance')}>治理</a><Badge tone="info">{scopeLabel}</Badge></div>{#if governance.isPending}<LoadingState />{:else if governance.error}<OntologySummaryError message={governance.error.message} onretry={() => void governance.refetch()} />{:else}<dl class="mt-4 grid gap-3 sm:grid-cols-2"><div><dt class="text-xs text-muted-foreground">可处理提案</dt><dd class="mt-1 text-2xl font-semibold">{metric(governance.data?.data.actionable_proposals)}</dd></div><div><dt class="text-xs text-muted-foreground">最近回执</dt><dd class="mt-1 text-sm font-semibold">{governance.data?.data.latest_receipt.receipt?.applied_at ?? '—'}</dd></div></dl><p class="mt-4 text-xs text-muted-foreground">统计于 {governance.data?.meta.as_of ?? '—'}</p>{/if}</article>
  <article class="rounded-xl border border-border bg-card p-5"><div class="flex items-center justify-between"><a class="font-semibold hover:underline" href={scopedPath('/resources/ontology/operations/projections')}>投影运维</a><Badge tone="info">{scopeLabel}</Badge></div>{#if projections.isPending}<LoadingState />{:else if projections.error}<OntologySummaryError message={projections.error.message} onretry={() => void projections.refetch()} />{:else}<dl class="mt-4 grid gap-3 sm:grid-cols-2"><div><dt class="text-xs text-muted-foreground">未解决审计</dt><dd class="mt-1 text-2xl font-semibold">{metric(projections.data?.data.unresolved_audits)}</dd></div><div><dt class="text-xs text-muted-foreground">最近活动</dt><dd class="mt-1 text-sm font-semibold">{projections.data?.data.latest_activity.observed_at ?? '—'}</dd></div></dl><p class="mt-4 text-xs text-muted-foreground">统计于 {projections.data?.meta.as_of ?? '—'}</p>{/if}</article>
</div>
