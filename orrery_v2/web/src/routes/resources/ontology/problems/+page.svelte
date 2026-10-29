<script lang="ts">
  import { goto } from '$app/navigation'
  import { page } from '$app/stores'
  import { Search } from 'lucide-svelte'
  import { untrack } from 'svelte'
  import OntologyProblemInspector from '#lib/components/ontology/OntologyProblemInspector.svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import type { ProblemStatus } from '#lib/api/ontology-problems'
  import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { useOntologyContextObjectsQuery } from '#lib/queries/ontology-context'
  import { useOntologyProblemsQuery } from '#lib/queries/ontology-problems'
  import { session } from '#lib/stores/session.svelte'

  interface QueueItem {
    id: string
    tenantId: string
    title: string
    status: string
    severity?: string
    sourceDomain?: string
    ownerId?: string
    occurrenceCount?: number
    lessonCandidateId?: string
    updatedAt?: string
  }

  let searchDraft = $state($page.url.searchParams.get('search') ?? '')
  let search = $state($page.url.searchParams.get('search') ?? '')
  let status = $state(($page.url.searchParams.get('status') ?? '') as ProblemStatus | '')
  let problemId = $state($page.url.searchParams.get('problem_id') ?? '')
  let sourceTenantId = $state($page.url.searchParams.get('source_tenant_id') ?? '')
  let boundScope = $state('')

  const tenantId = () => ontologyScopeContext.mode === 'scope' ? ontologyScopeContext.activeScope?.tenant_id ?? '' : ''
  const scopeKey = () => `${ontologyScopeContext.mode}:${tenantId()}`
  const canManage = $derived(hasPermissionScope(session.permissionScope, 'operations.manage'))
  const scopedProblems = useOntologyProblemsQuery(
    () => ({ tenantId: tenantId(), status, search: search || undefined, limit: 100 }),
    () => Boolean(tenantId())
  )
  const aggregateProblems = useOntologyContextObjectsQuery(
    () => ({ tenantId: '', objectType: 'problem_case', status: status || undefined, search: search || undefined, limit: 100 }),
    () => !tenantId()
  )
  const queueItems = $derived.by<QueueItem[]>(() => {
    if (tenantId()) {
      return (scopedProblems.data?.items ?? []).map((item) => ({
        id: item.id, tenantId: item.tenant_id, title: item.title, status: item.status,
        severity: item.severity, sourceDomain: item.source_domain, ownerId: item.owner.id,
        occurrenceCount: item.occurrence_count, lessonCandidateId: item.lesson_candidate_id, updatedAt: item.updated_at
      }))
    }
    return (aggregateProblems.data?.items ?? []).map((item) => ({
      id: item.id, tenantId: item.tenant_id ?? '', title: item.label, status: item.status ?? '', updatedAt: item.updated_at
    }))
  })
  const loading = $derived(tenantId() ? scopedProblems.isPending : aggregateProblems.isPending)
  const loadError = $derived(tenantId() ? scopedProblems.error : aggregateProblems.error)
  const unresolvedCount = $derived(queueItems.filter((item) => item.status !== 'resolved').length)
  const routedCount = $derived(queueItems.filter((item) => Boolean(item.ownerId)).length)
  const lessonCount = $derived(queueItems.filter((item) => Boolean(item.lessonCandidateId)).length)

  $effect(() => {
    const activeTenant = tenantId()
    const activeScopeKey = scopeKey()
    const url = $page.url
    untrack(() => syncRouteState(activeScopeKey, activeTenant, url))
  })

  function syncRouteState(activeScopeKey: string, activeTenant: string, url: URL) {
    const restoreRoute = activeTenant === (url.searchParams.get('tenant_id') ?? '').trim()
    const nextProblem = restoreRoute ? (url.searchParams.get('problem_id') ?? '') : ''
    const nextSourceTenant = restoreRoute && nextProblem ? (url.searchParams.get('source_tenant_id') ?? activeTenant) : ''
    const nextSearch = restoreRoute ? (url.searchParams.get('search') ?? '') : ''
    const nextStatus = restoreRoute ? ((url.searchParams.get('status') ?? '') as ProblemStatus | '') : ''
    const unchanged = boundScope === activeScopeKey && problemId === nextProblem && sourceTenantId === nextSourceTenant && search === nextSearch && status === nextStatus
    boundScope = activeScopeKey
    if (unchanged) return
    problemId = nextProblem
    sourceTenantId = nextSourceTenant
    searchDraft = nextSearch
    search = nextSearch
    status = nextStatus
  }

  function updateRoute() {
    const params = new URLSearchParams()
    if (tenantId()) params.set('tenant_id', tenantId())
    if (problemId) { params.set('problem_id', problemId); params.set('source_tenant_id', sourceTenantId) }
    if (search) params.set('search', search)
    if (status) params.set('status', status)
    void goto(`/resources/ontology/problems?${params.toString()}`, { invalidateAll: false })
  }

  function selectProblem(item: QueueItem) {
    problemId = item.id
    sourceTenantId = item.tenantId || tenantId()
    updateRoute()
  }

  function applyFilters() {
    search = searchDraft.trim()
    problemId = ''
    sourceTenantId = ''
    updateRoute()
  }

  function formatDate(value?: string) {
    if (!value) return '—'
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? value : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'short' }).format(parsed)
  }

  function statusLabel(value?: string) {
    return ({ open: '待处理', triaged: '已分流', contained: '已控制', diagnosed: '已诊断', in_progress: '处理中', monitoring: '观察中', resolved: '已解决', escalated: '已升级', quarantined: '已隔离' } as Record<string, string>)[value ?? ''] ?? value ?? '未知'
  }

  function statusTone(value?: string): 'success' | 'warning' | 'danger' | 'info' | 'neutral' {
    if (value === 'resolved') return 'success'
    if (value === 'quarantined') return 'danger'
    if (value === 'escalated') return 'warning'
    if (value === 'in_progress' || value === 'monitoring' || value === 'diagnosed') return 'info'
    return 'neutral'
  }
</script>

<section class="space-y-4">
  <header>
    <h2 class="text-xl font-semibold">问题枢纽</h2>
    <p class="mt-1 text-sm text-muted-foreground">问题、证据、处置结果与组织记忆的统一队列。</p>
  </header>

  <dl class="grid grid-cols-3 gap-4 border-y border-border py-4">
    <div><dt class="text-xs text-muted-foreground">当前结果待处理</dt><dd class="mt-1 text-2xl font-semibold">{unresolvedCount}</dd></div>
    <div><dt class="text-xs text-muted-foreground">已分配负责人</dt><dd class="mt-1 text-2xl font-semibold">{tenantId() ? routedCount : '—'}</dd></div>
    <div><dt class="text-xs text-muted-foreground">已形成经验</dt><dd class="mt-1 text-2xl font-semibold">{tenantId() ? lessonCount : '—'}</dd></div>
  </dl>

  <div class="grid gap-3 sm:grid-cols-[minmax(0,1fr)_180px_auto] sm:items-end">
    <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">搜索问题</span><div class="relative"><Search class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" /><Input value={searchDraft} class="pl-9" ariaLabel="搜索问题" oninput={(event) => (searchDraft = event.currentTarget.value)} /></div></label>
    <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">状态</span><Select value={status} options={[{ label: '全部状态', value: '' }, { label: '待处理', value: 'open' }, { label: '已分流', value: 'triaged' }, { label: '已诊断', value: 'diagnosed' }, { label: '处理中', value: 'in_progress' }, { label: '观察中', value: 'monitoring' }, { label: '已升级', value: 'escalated' }, { label: '已隔离', value: 'quarantined' }, { label: '已解决', value: 'resolved' }]} onchange={(event) => { status = event.currentTarget.value as ProblemStatus | ''; applyFilters() }} /></label>
    <Button onclick={applyFilters}>应用筛选</Button>
  </div>

  <div class="grid gap-4 xl:grid-cols-[minmax(260px,0.72fr)_minmax(0,1.78fr)]">
    <section class="min-w-0 border-b border-border pb-4 xl:border-b-0 xl:border-r xl:pb-0 xl:pr-4" aria-label="问题队列">
      <div class="mb-2 flex items-center justify-between"><h3 class="text-sm font-semibold">问题队列</h3><span class="text-xs text-muted-foreground">{queueItems.length}</span></div>
      {#if loading}
        <LoadingState />
      {:else if loadError}
        <ErrorState message={loadError.message} />
      {:else if !queueItems.length}
        <EmptyState title="当前筛选没有问题" description="新问题进入后会按指纹合并到这里。" />
      {:else}
        <div class="divide-y divide-border border-y border-border">
          {#each queueItems as item (`${item.tenantId}:${item.id}`)}
            <button type="button" class="block w-full px-3 py-3 text-left transition-colors hover:bg-muted" class:bg-muted={problemId === item.id && sourceTenantId === item.tenantId} aria-pressed={problemId === item.id && sourceTenantId === item.tenantId} onclick={() => selectProblem(item)}>
              <span class="block break-words text-sm font-medium">{item.title}</span>
              <span class="mt-2 flex flex-wrap items-center gap-2"><Badge tone={statusTone(item.status)}>{statusLabel(item.status)}</Badge>{#if item.sourceDomain}<span class="text-[11px] text-muted-foreground">{item.sourceDomain}</span>{/if}<span class="ml-auto text-[11px] text-muted-foreground">{formatDate(item.updatedAt)}</span></span>
              {#if item.ownerId || item.occurrenceCount}<span class="mt-2 block truncate text-[11px] text-muted-foreground">{item.ownerId ? `负责人 ${item.ownerId}` : '未分配'}{item.occurrenceCount ? ` · ${item.occurrenceCount} 次` : ''}</span>{/if}
            </button>
          {/each}
        </div>
        {#if tenantId() && scopedProblems.data?.has_more}<p class="mt-2 text-xs text-muted-foreground">结果超过 100 条，请缩小筛选范围。</p>{/if}
      {/if}
    </section>

    <section class="min-w-0" aria-label="问题详情">
      {#if problemId && sourceTenantId}
        <OntologyProblemInspector tenantId={sourceTenantId} {problemId} {canManage} />
      {:else}
        <EmptyState title="选择一个问题" description="查看来源、证据、负责人、验证结果和组织经验。" />
      {/if}
    </section>
  </div>
</section>
