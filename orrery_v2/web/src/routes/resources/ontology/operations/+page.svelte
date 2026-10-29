<script lang="ts">
  import OntologyOperationsOverview from '#lib/components/ontology/OntologyOperationsOverview.svelte'
  import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'

  function scopedHref(path: string) {
    const tenantId = ontologyScopeContext.mode === 'scope' ? ontologyScopeContext.activeScope?.tenant_id : undefined
    return tenantId ? `${path}?tenant_id=${encodeURIComponent(tenantId)}` : path
  }
</script>

<section class="space-y-4">
  <header><h2 class="text-xl font-semibold">高级运维</h2><p class="mt-1 text-sm text-muted-foreground">监控本体投影链路、运行产出和异常审计。</p></header>
  <OntologyOperationsOverview detailsHref={scopedHref('/resources/ontology/operations/projections')} />
</section>
