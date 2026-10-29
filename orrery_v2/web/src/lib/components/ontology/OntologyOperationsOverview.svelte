<script lang="ts">
  import { ArrowRight, CheckCircle2 } from 'lucide-svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'
  import { useOntologyContextObjectsQuery } from '#lib/queries/ontology-context'
  import { useOntologyProjectionSummaryQuery } from '#lib/queries/ontology-projections'
  import { useOntologySchemaGraphQuery } from '#lib/queries/ontology-schema'

  interface Props {
    detailsHref?: string
  }

  let { detailsHref = '' }: Props = $props()

  const tenantId = () => ontologyScopeContext.mode === 'scope' ? ontologyScopeContext.activeScope?.tenant_id ?? '' : ''
  const projectionSummary = useOntologyProjectionSummaryQuery(tenantId)
  const projectionGraph = useOntologySchemaGraphQuery(tenantId, () => ({ view: 'projection', maxNodes: 40 }))
  const recentObjects = useOntologyContextObjectsQuery(() => ({ tenantId: tenantId(), limit: 8 }))
  const outputs = $derived(recentObjects.data?.items ?? [])
  const projectionLinks = $derived(projectionGraph.data?.edges.slice(0, 8) ?? [])
  const latestOutput = $derived(outputs[0]?.updated_at)

  function formatDate(value?: string) {
    if (!value) return '—'
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? value : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(parsed)
  }

  function nodeLabel(id: string) {
    return projectionGraph.data?.nodes.find((node) => node.id === id)?.label ?? id
  }

  function unresolvedAudits() {
    const metric = projectionSummary.data?.data.unresolved_audits
    return metric?.status === 'exact' ? metric.value ?? 0 : '—'
  }
</script>

<section class="space-y-4" aria-labelledby="projection-overview-title">
  <div class="flex flex-wrap items-center justify-between gap-3">
    <div>
      <h3 id="projection-overview-title" class="text-sm font-semibold">投影运行概览</h3>
      <p class="mt-1 text-xs text-muted-foreground">当前投影定义、异常状态和 Context Graph 最近产出。</p>
    </div>
    {#if detailsHref}
      <a class="inline-flex h-9 items-center gap-2 border border-border px-3 text-sm font-medium hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" href={detailsHref}>
        审计与处置
        <ArrowRight class="size-4" aria-hidden="true" />
      </a>
    {/if}
  </div>

  <div class="grid gap-4 border-y border-border py-4 sm:grid-cols-2 xl:grid-cols-4">
    <div>
      <p class="text-xs text-muted-foreground">投影定义</p>
      <p class="mt-1 text-2xl font-semibold">{projectionGraph.data ? `${projectionGraph.data.visible_nodes}${projectionGraph.data.truncated ? '+' : ''}` : '—'}</p>
    </div>
    <div>
      <p class="text-xs text-muted-foreground">可见投影关系</p>
      <p class="mt-1 text-2xl font-semibold">{projectionGraph.data?.visible_edges ?? '—'}</p>
    </div>
    <div>
      <p class="text-xs text-muted-foreground">未处理异常</p>
      <div class="mt-1 flex items-center gap-2">
        {#if unresolvedAudits() === 0}<CheckCircle2 class="size-5 text-emerald-600" aria-hidden="true" />{/if}
        <p class="text-2xl font-semibold">{unresolvedAudits()}</p>
      </div>
    </div>
    <div>
      <p class="text-xs text-muted-foreground">最近产出</p>
      <p class="mt-2 text-sm font-medium">{formatDate(latestOutput)}</p>
    </div>
  </div>

  {#if projectionSummary.isPending || projectionGraph.isPending || recentObjects.isPending}
    <LoadingState />
  {/if}
  {#if projectionSummary.error}<ErrorState message={projectionSummary.error.message} />{/if}
  {#if projectionGraph.error}<ErrorState message={projectionGraph.error.message} />{/if}
  {#if recentObjects.error}<ErrorState message={recentObjects.error.message} />{/if}

  <div class="grid gap-6 xl:grid-cols-2">
    <section class="min-w-0">
      <h4 class="text-sm font-medium">最近投影产出</h4>
      {#if outputs.length === 0 && !recentObjects.isPending}
        <p class="mt-3 text-sm text-muted-foreground">当前范围尚无 Context Graph 对象。</p>
      {:else if outputs.length > 0}
        <div class="mt-3 overflow-x-auto border-y border-border">
          <table class="w-full min-w-[560px] table-fixed text-left text-sm">
            <thead class="text-[11px] font-medium uppercase text-muted-foreground">
              <tr><th class="w-[42%] px-3 py-2">对象</th><th class="w-[24%] px-3 py-2">类型</th><th class="w-[34%] px-3 py-2">更新时间</th></tr>
            </thead>
            <tbody>
              {#each outputs as item (`${item.tenant_id ?? tenantId()}:${item.id}`)}
                <tr class="border-t border-border">
                  <td class="px-3 py-3"><span class="block truncate font-medium" title={item.label || item.id}>{item.label || item.id}</span><span class="block truncate font-mono text-[10px] text-muted-foreground">{item.tenant_id ?? tenantId()}</span></td>
                  <td class="truncate px-3 py-3" title={item.object_type}>{item.object_type}</td>
                  <td class="truncate px-3 py-3 text-xs text-muted-foreground">{formatDate(item.updated_at)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>

    <section class="min-w-0">
      <h4 class="text-sm font-medium">投影链路定义</h4>
      {#if projectionLinks.length === 0 && !projectionGraph.isPending}
        <p class="mt-3 text-sm text-muted-foreground">当前范围尚无可见投影链路。</p>
      {:else if projectionLinks.length > 0}
        <div class="mt-3 divide-y divide-border border-y border-border">
          {#each projectionLinks as link (link.id)}
            <div class="grid gap-1 py-3 text-sm sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] sm:items-center sm:gap-3">
              <span class="truncate font-medium" title={nodeLabel(link.source_id)}>{nodeLabel(link.source_id)}</span>
              <span class="text-xs text-muted-foreground">{link.relation}</span>
              <span class="truncate sm:text-right" title={nodeLabel(link.target_id)}>{nodeLabel(link.target_id)}</span>
            </div>
          {/each}
        </div>
      {/if}
    </section>
  </div>
</section>
