<script lang="ts">
  import { page } from '$app/stores'
  import { X } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import { onMount, untrack } from 'svelte'
  import { setForbiddenHandler } from '#lib/api/client'
  import { activateOntologyScope, clearOntologyScope, restoreOntologyScope, ontologyScopeContext, revalidateOntologyScopeAfterForbidden } from '#lib/ontology-workbench/scope-context.svelte'
  import { session } from '#lib/stores/session.svelte'
  import { useOntologyScopeCatalogQuery } from '#lib/queries/ontology-scopes'

  interface Props { children: import('svelte').Snippet }
  let { children }: Props = $props()
  let sessionEpoch = $state(session.securityContextVersion)
  const catalogQuery = useOntologyScopeCatalogQuery(
    () => ({ kind: 'organization', limit: 100 })
  )
  const organizationScopes = $derived(catalogQuery.data?.items ?? [])
  const scopeOptions = $derived([
    { label: '公司全景', value: '' },
    ...organizationScopes.map((scope) => ({
      label: scope.label,
      value: scope.tenant_id
    }))
  ])
  const activeScope = $derived(ontologyScopeContext.activeScope)
  const companyScopeValue = $derived(ontologyScopeContext.mode === 'scope' && activeScope?.kind === 'organization' ? activeScope.tenant_id : '')
  const activeBusinessContext = $derived(ontologyScopeContext.mode === 'scope' && activeScope?.kind !== 'organization' ? activeScope : undefined)
  const activeRangeLabel = $derived(
    ontologyScopeContext.mode === 'all' || !activeScope
      ? '公司全景'
      : activeScope.kind === 'organization'
        ? activeScope.label
        : `公司全景 · ${scopeKindLabel(activeScope.kind)}`
  )

  onMount(() => {
    setForbiddenHandler((error, path) => revalidateOntologyScopeAfterForbidden(path, error))
    return () => {
      setForbiddenHandler(undefined)
    }
  })

  $effect(() => {
    const currentEpoch = session.securityContextVersion
    if (currentEpoch !== sessionEpoch) {
      sessionEpoch = currentEpoch
      clearOntologyScope()
    }
  })

  $effect(() => {
    const tenant = $page.url.searchParams.get('tenant_id') ?? ''
    const pathname = $page.url.pathname
    untrack(() => void restoreOntologyScope(tenant, pathname))
  })

  const nav = [
    ['/resources/ontology', '总览'], ['/resources/ontology/schema', '本体定义'],
    ['/resources/ontology/context', '实际数据'], ['/resources/ontology/problems', '问题枢纽'], ['/resources/ontology/executions', '智能体执行'], ['/resources/ontology/reliability', '可靠性'], ['/resources/ontology/lineage', '来源与质量'],
    ['/resources/ontology/governance', '治理'], ['/resources/ontology/operations', '运维']
  ] as const

  function scopedHref(path: string) {
    const tenant = ontologyScopeContext.mode === 'scope' ? ontologyScopeContext.activeScope?.tenant_id : undefined
    return tenant ? `${path}?tenant_id=${encodeURIComponent(tenant)}` : path
  }

  function navIsActive(path: string) {
    return $page.url.pathname === path || (path === '/resources/ontology/operations' && $page.url.pathname.startsWith(`${path}/`))
  }

  function switchScope(tenantId: string) {
    void activateOntologyScope(tenantId, $page.url.pathname)
  }

  function scopeKindLabel(kind: string) {
    switch (kind) {
      case 'mission': return 'Mission 上下文'
      case 'project': return '项目上下文'
      case 'resource': return '资源上下文'
      case 'system': return '平台上下文'
      default: return '业务上下文'
    }
  }
</script>

<section class="page-shell max-w-[1040px] space-y-5">
  <header class="border-b border-border pb-5">
    <div class="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between">
    <div>
      <p class="text-xs font-semibold uppercase text-muted-foreground">本体工作台</p>
      <h1 class="page-title mt-1">公司本体</h1>
      <p class="page-description">公司对象、关系、事实、来源与治理结果。</p>
    </div>
    <div class="w-full max-w-md space-y-2">
      <label class="grid gap-1.5">
        <span class="text-[11px] text-muted-foreground">公司范围</span>
        <Select value={companyScopeValue} options={scopeOptions} disabled={catalogQuery.isPending || ontologyScopeContext.resolving} onchange={(event) => switchScope(event.currentTarget.value)} />
      </label>
      {#if activeBusinessContext}
        <div class="flex min-h-10 items-center gap-3 border-l-2 border-brand pl-3">
          <div class="min-w-0 flex-1">
            <p class="text-[11px] text-muted-foreground">业务上下文</p>
            <p class="truncate text-sm font-medium">{activeBusinessContext.label} · {scopeKindLabel(activeBusinessContext.kind)}</p>
          </div>
          <button class="inline-flex size-8 shrink-0 items-center justify-center rounded border border-border text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring" type="button" aria-label="返回公司全景" title="返回公司全景" onclick={() => switchScope('')}>
            <X class="size-4" aria-hidden="true" />
          </button>
        </div>
      {/if}
      {#if catalogQuery.error}<ErrorState message={catalogQuery.error.message} />{/if}
      {#if ontologyScopeContext.error}<ErrorState message={ontologyScopeContext.error} />{/if}
    </div>
    </div>
  </header>

  <nav class="flex flex-wrap items-center gap-1 border-b border-border" aria-label="Ontology sections">
    {#each nav as item}
      <a class="-mb-px border-b-2 px-3 py-2 text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring {navIsActive(item[0]) ? 'border-brand font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}" aria-current={navIsActive(item[0]) ? 'page' : undefined} href={scopedHref(item[0])}>{item[1]}</a>
    {/each}
    <span class="ml-auto pb-2"><Badge tone="info">{activeRangeLabel}</Badge></span>
  </nav>

  {@render children()}
</section>
