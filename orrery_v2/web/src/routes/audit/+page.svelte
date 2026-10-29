<script lang="ts">
  import AuditLogList from '#lib/components/operations/AuditLogList.svelte'
  import AuthIPLockList from '#lib/components/operations/AuthIPLockList.svelte'
  import KpiCard from '#lib/components/shared/KpiCard.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { ChevronLeft, ChevronRight } from 'lucide-svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { TranslationKey } from '#lib/i18n'
  import { useAuditLogQuery, useAuthIPLocksQuery, useAuthIPUnlockMutation } from '#lib/queries/operations'
  import type { ApiClientError } from '#lib/api/client'

  let missionId = $state('')
  let actorId = $state('')
  let action = $state('')
  let entityType = $state('')
  let entityId = $state('')
  let severity = $state('')
  let activeAuditView = $state<'events' | 'locks'>('events')
  let unlockingIP = $state('')
  let ipLockMessage = $state('')
  let auditOffset = $state(0)
  let previousFilterKey = $state('')
  let t = $derived(createTranslator($locale))

  const auditPageSize = 10

  $effect(() => {
    const filterKey = [missionId, actorId, action, entityType, entityId, severity].join('\u0000')
    if (previousFilterKey && filterKey !== previousFilterKey) auditOffset = 0
    previousFilterKey = filterKey
  })

  const auditQuery = useAuditLogQuery(() => ({
    missionId,
    actorId,
    action,
    entityType,
    entityId,
    severity,
    limit: auditPageSize,
    offset: auditOffset
  }))
  const authIPLocksQuery = useAuthIPLocksQuery(() => ({ status: 'active', limit: 50 }))
  const authIPUnlockMutation = useAuthIPUnlockMutation()
  function unlockIP(ipAddress: string) {
    unlockingIP = ipAddress
    ipLockMessage = ''
    authIPUnlockMutation.mutate(
      { ipAddress, reason: 'administrator removed lock from audit page' },
      {
        onSuccess: () => {
          ipLockMessage = `已解除 ${ipAddress} 的登录锁定`
          unlockingIP = ''
        },
        onError: (error) => {
          ipLockMessage = error instanceof Error ? error.message : '解除锁定失败'
          unlockingIP = ''
        }
      }
    )
  }
</script>

<svelte:head>
  <title>{t('nav.audit')} | Orrery</title>
</svelte:head>

<div class="page-shell max-w-[1040px] space-y-5">
  <!-- Page header -->
  <header class="page-header">
    <div>
      <h1 class="page-title">{t('nav.audit')}</h1>
      <p class="page-description">{t('nav.audit.description')}</p>
    </div>
  </header>

  <nav class="flex items-center gap-5 border-b border-border" aria-label={t('nav.audit')}>
    <button type="button" class="-mb-px border-b-2 pb-2.5 text-[13.5px] transition-colors {activeAuditView === 'events' ? 'border-brand font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}" onclick={() => (activeAuditView = 'events')}>{t('nav.audit')}</button>
    <button type="button" class="-mb-px border-b-2 pb-2.5 text-[13.5px] transition-colors {activeAuditView === 'locks' ? 'border-brand font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}" onclick={() => (activeAuditView = 'locks')}>登录安全</button>
  </nav>

  <section class="space-y-4 {activeAuditView === 'locks' ? '' : 'hidden'}">
    <div class="flex flex-col gap-1.5">
      <h2 class="text-lg font-semibold text-foreground">登录 IP 锁定</h2>
      <p class="max-w-2xl text-sm text-muted-foreground">连续三次密码错误后，登录来源 IP 会被锁定并写入审计记录。</p>
    </div>

    {#if authIPLocksQuery.isLoading}
      <LoadingState title="正在加载登录锁定" description="读取当前活跃的登录 IP 锁定记录。" />
    {:else if authIPLocksQuery.isError}
      {@const lockError = authIPLocksQuery.error as ApiClientError}
      <ErrorState title="登录锁定加载失败" message={lockError.message} correlationId={lockError.correlationId} />
    {:else if authIPLocksQuery.data}
      <section class="grid gap-3 sm:grid-cols-3">
        <KpiCard label="活跃锁定" value={authIPLocksQuery.data.summary.active} compact />
        <KpiCard label="观察中" value={authIPLocksQuery.data.summary.tracking} compact />
        <KpiCard label="失败累计" value={authIPLocksQuery.data.summary.totalFailures} compact />
      </section>
      {#if ipLockMessage}
        <p class="rounded-md border border-border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">{ipLockMessage}</p>
      {/if}
      <AuthIPLockList view={authIPLocksQuery.data} busyIP={unlockingIP} onUnlock={unlockIP} />
    {/if}
  </section>

  <div class="space-y-5 {activeAuditView === 'events' ? '' : 'hidden'}">
  {#if auditQuery.isLoading}
    <LoadingState title={t('state.loadingAudit')} description={t('state.loadingAuditDescription')} />
  {:else if auditQuery.isError}
    {@const error = auditQuery.error as ApiClientError}
    <ErrorState title={t('state.errorAudit')} message={error.message} correlationId={error.correlationId} />
  {:else if auditQuery.data}
    <!-- KPI row -->
    <section class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <KpiCard label={t('kpi.total')} value={auditQuery.data.summary.total} />
      <KpiCard label={t('kpi.warning')} value={auditQuery.data.summary.warning} />
      <KpiCard label={t('kpi.error')} value={auditQuery.data.summary.error} />
      <KpiCard label={t('kpi.succeeded')} value={auditQuery.data.summary.succeeded} />
    </section>

    <!-- Filter row -->
    <section class="flex flex-wrap items-center gap-2.5">
      {@render textFilter('filter.missionId', missionId, (value) => (missionId = value))}
      {@render textFilter('filter.actorId', actorId, (value) => (actorId = value))}
      {@render textFilter('filter.action', action, (value) => (action = value))}
      {@render textFilter('filter.entityType', entityType, (value) => (entityType = value))}
      {@render textFilter('filter.entityId', entityId, (value) => (entityId = value))}
      <label class="flex min-h-9 overflow-hidden rounded-lg border border-input bg-input-background transition-colors focus-within:border-ring focus-within:ring-1 focus-within:ring-ring">
        <span class="grid w-20 shrink-0 place-items-center border-r border-border bg-muted/60 px-2.5 text-[11px] font-medium text-muted-foreground">
          {t('filter.severity')}
        </span>
        <Select
          bind:value={severity}
          options={[
            { label: t('filter.allSeverities'), value: '' },
            { label: t('filter.info'), value: 'info' },
            { label: t('filter.warning'), value: 'warning' },
            { label: t('filter.error'), value: 'error' }
          ]}
          class="h-9 w-32 rounded-none border-0 bg-transparent shadow-none focus-visible:ring-0"
        />
      </label>
    </section>

    <!-- Audit timeline -->
    <AuditLogList events={auditQuery.data.events} />
    {#if auditQuery.data.page.total > auditPageSize || auditQuery.data.page.offset > 0}
      <div class="flex items-center justify-between gap-3 border-t border-border pt-3">
        <span class="text-xs text-muted-foreground">
          {t('operations.audit.pagination.info', {
            page: Math.floor(auditQuery.data.page.offset / auditPageSize) + 1,
            total: auditQuery.data.page.total
          })}
        </span>
        <div class="flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon"
            disabled={auditQuery.data.page.offset <= 0}
            ariaLabel={t('operations.audit.pagination.previous')}
            title={t('operations.audit.pagination.previous')}
            onclick={() => (auditOffset = Math.max(0, auditOffset - auditPageSize))}
          >
            <ChevronLeft class="h-4 w-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            disabled={!auditQuery.data.page.hasMore}
            ariaLabel={t('operations.audit.pagination.next')}
            title={t('operations.audit.pagination.next')}
            onclick={() => (auditOffset += auditPageSize)}
          >
            <ChevronRight class="h-4 w-4" />
          </Button>
        </div>
      </div>
    {/if}
  {/if}
  </div>

  <section class="rounded-xl border border-border bg-card p-5 {activeAuditView === 'events' ? '' : 'hidden'}">
    <h2 class="text-lg font-semibold text-foreground">Ontology Projection 运维</h2>
    <p class="mt-1 text-sm text-muted-foreground">审计重放、确认和历史查询已迁移到带 Scope 的 Projection 工作区。</p>
    <a class="mt-4 inline-flex rounded-md border border-border px-3 py-2 text-sm hover:bg-muted" href="/resources/ontology/operations/projections">打开 Projection 运维</a>
  </section>
</div>

{#snippet textFilter(label: TranslationKey, value: string, setValue: (value: string) => void)}
  <label class="flex min-h-9 overflow-hidden rounded-lg border border-input bg-input-background transition-colors focus-within:border-ring focus-within:ring-1 focus-within:ring-ring">
    <span class="grid w-20 shrink-0 place-items-center border-r border-border bg-muted/60 px-2.5 text-[11px] font-medium text-muted-foreground">
      {t(label)}
    </span>
    <Input value={value} oninput={(event) => setValue(event.currentTarget.value)} class="h-9 w-32 rounded-none border-0 bg-transparent shadow-none focus-visible:ring-0" />
  </label>
{/snippet}
