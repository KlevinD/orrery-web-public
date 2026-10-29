<script lang="ts">
  import { Check, FileClock, RefreshCw, ShieldCheck, X } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import OntologyCursorControls from './OntologyCursorControls.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Tabs from '#lib/components/ui/Tabs.svelte'
  import type { GovernanceApplyResultDTO, GovernancePromotionPlanDTO } from '#lib/api/ontology-governance'
  import { createGovernanceIdempotencyKey, governanceWorkflowKey } from '#lib/ontology-workbench/governance-workflow'
  import { setOntologyScopeSwitchGuard } from '#lib/ontology-workbench/scope-context.svelte'
  import {
    useOntologyGovernanceAnalysisQuery,
    useOntologyGovernanceApplyPlanMutation,
    useOntologyGovernanceAuditEventsQuery,
    useOntologyGovernanceCreatePlanMutation,
    useOntologyGovernanceDecisionMutation,
    useOntologyGovernanceProposalQuery,
    useOntologyGovernanceProjectionRefreshRetryMutation,
    useOntologyGovernanceReceiptsQuery
  } from '#lib/queries/ontology-governance'

  interface Props {
    tenantId: string
    proposalId: string
    canManage: boolean
  }

  let { tenantId, proposalId, canManage }: Props = $props()
  let activeTab = $state('analysis')
  let decisionReason = $state('')
  let plan = $state<GovernancePromotionPlanDTO | undefined>()
  let applied = $state<GovernanceApplyResultDTO | undefined>()
  let confirmation = $state(false)
  let applyConfirmed = $state(false)
  let idempotencyKey = $state('')
  let message = $state('')
  let boundKey = $state('')
  let receiptCursor = $state('')
  let receiptHistory = $state<string[]>([])
  let auditCursor = $state('')
  let auditHistory = $state<string[]>([])

  const detail = useOntologyGovernanceProposalQuery(() => tenantId, () => proposalId)
  const analysis = useOntologyGovernanceAnalysisQuery(() => tenantId, () => proposalId, () => activeTab === 'analysis')
  const receipts = useOntologyGovernanceReceiptsQuery(() => tenantId, () => proposalId, () => ({ limit: 25, cursor: receiptCursor || undefined }), () => activeTab === 'receipts')
  const audits = useOntologyGovernanceAuditEventsQuery(() => tenantId, () => proposalId, () => ({ limit: 25, cursor: auditCursor || undefined }), () => activeTab === 'audit')
  const decide = useOntologyGovernanceDecisionMutation()
  const createPlan = useOntologyGovernanceCreatePlanMutation()
  const applyPlan = useOntologyGovernanceApplyPlanMutation()
  const retryRefresh = useOntologyGovernanceProjectionRefreshRetryMutation()
  const currentKey = $derived(governanceWorkflowKey({ tenantId, proposalId, revision: detail.data?.revision ?? 0 }))
  const isDirty = $derived(Boolean(decisionReason.trim() || plan || confirmation))

  $effect(() => {
    if (boundKey !== currentKey) {
      boundKey = currentKey
      resetCommandState()
      receiptCursor = ''
      receiptHistory = []
      auditCursor = ''
      auditHistory = []
    }
  })

  $effect(() => setOntologyScopeSwitchGuard(async () => {
    if (!isDirty) return true
    const proceed = globalThis.confirm?.('当前治理操作尚未完成。切换 Scope 将放弃这些内容，是否继续？') ?? false
    if (proceed) resetCommandState()
    return proceed
  }))

  function resetCommandState() {
    decisionReason = ''
    plan = undefined
    applied = undefined
    confirmation = false
    applyConfirmed = false
    idempotencyKey = ''
    message = ''
  }

  async function submitDecision(decision: 'accepted' | 'rejected') {
    if (!detail.data?.revision || !decisionReason.trim()) return
    message = ''
    try {
      await decide.mutateAsync({ tenantId, proposalId, expectedRevision: detail.data.revision, decision, reason: decisionReason.trim() })
      decisionReason = ''
      message = decision === 'accepted' ? '提案已接受。' : '提案已拒绝。'
    } catch (error) {
      message = error instanceof Error ? error.message : '决策失败'
    }
  }

  async function preparePlan() {
    if (!detail.data?.revision) return
    resetCommandState()
    try {
      plan = await createPlan.mutateAsync({ tenantId, proposalId, expectedRevision: detail.data.revision })
      idempotencyKey = createGovernanceIdempotencyKey()
    } catch (error) {
      message = error instanceof Error ? error.message : '无法创建应用计划'
    }
  }

  async function applyPreparedPlan() {
    if (!plan || !applyConfirmed || !idempotencyKey) return
    try {
      applied = await applyPlan.mutateAsync({ tenantId, planId: plan.plan_id, idempotencyKey })
      plan = undefined
      confirmation = false
      applyConfirmed = false
      message = applied.projection_refresh_status === 'pending'
        ? '变更已应用，运行时投影正在后台刷新。'
        : applied.projection_refresh_status === 'abandoned'
          ? '变更已应用，但运行时投影刷新失败，需要重新入队。'
        : applied.applied ? '变更已应用。' : '计划已完成，无需变更。'
      activeTab = 'receipts'
    } catch (error) {
      message = error instanceof Error ? error.message : '应用失败'
    }
  }

  async function retryProjectionRefresh(receiptId: string) {
    message = ''
    try {
      const result = await retryRefresh.mutateAsync({ tenantId, receiptId, idempotencyKey: createGovernanceIdempotencyKey() })
      message = result.projection_refresh_status === 'succeeded'
        ? '运行时投影已刷新。'
        : '运行时投影已重新入队。'
    } catch (error) {
      message = error instanceof Error ? error.message : '运行时投影重新入队失败'
    }
  }

  function nextReceipt(cursor: string) { receiptHistory = [...receiptHistory, receiptCursor]; receiptCursor = cursor }
  function previousReceipt() { receiptCursor = receiptHistory.at(-1) ?? ''; receiptHistory = receiptHistory.slice(0, -1) }
  function nextAudit(cursor: string) { auditHistory = [...auditHistory, auditCursor]; auditCursor = cursor }
  function previousAudit() { auditCursor = auditHistory.at(-1) ?? ''; auditHistory = auditHistory.slice(0, -1) }
</script>

{#if detail.isPending}
  <LoadingState />
{:else if detail.error}
  <ErrorState message={detail.error.message} />
{:else if !detail.data}
  <EmptyState title="提案不可用" />
{:else}
  <div class="space-y-5">
    <header class="border-b border-border pb-4">
      <p class="text-xs text-muted-foreground">提案 {detail.data.id}</p>
      <h3 class="mt-1 text-base font-semibold">{detail.data.target_kind ?? '—'}:{detail.data.target_id ?? '—'}</h3>
      <dl class="mt-3 grid gap-3 text-sm sm:grid-cols-3">
        <div><dt class="text-xs text-muted-foreground">字段</dt><dd class="mt-1">{detail.data.metadata_key ?? '—'}</dd></div>
        <div><dt class="text-xs text-muted-foreground">修订</dt><dd class="mt-1">{detail.data.revision ?? '—'}</dd></div>
        <div><dt class="text-xs text-muted-foreground">状态</dt><dd class="mt-1">{detail.data.status ?? '—'}</dd></div>
      </dl>
      <div class="mt-4 border-l-2 border-primary pl-3">
        <p class="text-xs text-muted-foreground">候选值</p>
        <p class="mt-1 break-words text-sm">{detail.data.proposed_value ?? '—'}</p>
      </div>
    </header>

    <Tabs bind:value={activeTab} tabs={[{ label: '分析', value: 'analysis' }, { label: '回执', value: 'receipts' }, { label: '审计', value: 'audit' }]} class="max-w-full overflow-x-auto" />

    {#if activeTab === 'analysis'}
      {#if analysis.isPending}<LoadingState />{:else if analysis.error}<ErrorState message={analysis.error.message} />{:else if analysis.data}
        <section aria-label="Proposal analysis">
          <div class="flex gap-5 text-sm"><span>阻断 <strong>{analysis.data.blocking_count}</strong></span><span>警告 <strong>{analysis.data.warning_count}</strong></span></div>
          {#if analysis.data.issues.length > 0}
            <ul class="mt-3 divide-y divide-border border-y border-border text-sm">{#each analysis.data.issues as issue (issue.code)}<li class="py-3"><span class="font-medium">{issue.severity}</span><span class="ml-2 text-muted-foreground">{issue.message}</span></li>{/each}</ul>
          {:else}<p class="mt-3 text-sm text-muted-foreground">确定性分析未发现问题。</p>{/if}
        </section>
      {/if}
    {:else if activeTab === 'receipts'}
      {#if receipts.isPending}<LoadingState />{:else if receipts.error}<ErrorState message={receipts.error.message} />{:else if !receipts.data?.items.length}<EmptyState title="暂无应用回执" />{:else}
        <ul class="divide-y divide-border border-y border-border text-sm">{#each receipts.data.items as receipt (receipt.receipt_id)}<li class="grid gap-2 py-3 sm:grid-cols-[minmax(0,1fr)_auto]"><span><FileClock class="mr-2 inline size-4" aria-hidden="true" />{receipt.status ?? '—'} · {receipt.change_count ?? 0} 项变更{#if receipt.projection_refresh_status}<span class="ml-2 text-xs text-muted-foreground">投影 {receipt.projection_refresh_status}</span>{/if}</span><div class="flex items-center gap-2"><span class="text-xs text-muted-foreground">{receipt.applied_at ?? receipt.updated_at ?? '—'}</span>{#if canManage && receipt.projection_refresh_status === 'abandoned'}<Button size="sm" variant="outline" disabled={retryRefresh.isPending} onclick={() => void retryProjectionRefresh(receipt.receipt_id)}><RefreshCw class="size-3.5" aria-hidden="true" />重新入队</Button>{/if}</div></li>{/each}</ul><OntologyCursorControls hasPrevious={receiptHistory.length > 0} hasMore={receipts.data.page.has_more} nextCursor={receipts.data.page.next_cursor} onprevious={previousReceipt} onnext={nextReceipt} />
      {/if}
    {:else}
      {#if audits.isPending}<LoadingState />{:else if audits.error}<ErrorState message={audits.error.message} />{:else if !audits.data?.items.length}<EmptyState title="暂无治理审计" />{:else}
        <ul class="divide-y divide-border border-y border-border text-sm">{#each audits.data.items as event (event.audit_id)}<li class="py-3"><div class="flex items-center justify-between gap-3"><span class="font-medium">{event.action ?? '—'} · {event.result ?? '—'}</span><span class="text-xs text-muted-foreground">{event.occurred_at ?? '—'}</span></div><p class="mt-1 text-muted-foreground">{event.summary ?? event.audit_id}</p></li>{/each}</ul><OntologyCursorControls hasPrevious={auditHistory.length > 0} hasMore={audits.data.page.has_more} nextCursor={audits.data.page.next_cursor} onprevious={previousAudit} onnext={nextAudit} />
      {/if}
    {/if}

    {#if canManage}
      <section class="border-t border-border pt-4" aria-label="Governance commands">
        {#if detail.data.status === 'proposed'}
          <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">审核理由</span><Input value={decisionReason} ariaLabel="Decision reason" oninput={(event) => (decisionReason = event.currentTarget.value)} /></label>
          <div class="mt-3 flex flex-wrap gap-2"><Button disabled={!decisionReason.trim() || decide.isPending} onclick={() => void submitDecision('accepted')}><Check class="size-4" aria-hidden="true" />接受</Button><Button variant="outline" disabled={!decisionReason.trim() || decide.isPending} onclick={() => void submitDecision('rejected')}><X class="size-4" aria-hidden="true" />拒绝</Button></div>
        {:else if detail.data.status === 'accepted' && !plan}
          <Button disabled={createPlan.isPending} onclick={() => void preparePlan()}><ShieldCheck class="size-4" aria-hidden="true" />生成安全应用计划</Button>
        {/if}

        {#if plan}
          <div class="mt-4 border-y border-border py-4">
            <div class="flex flex-wrap items-center justify-between gap-2"><div><h4 class="font-medium">待应用变更</h4><p class="mt-1 text-xs text-muted-foreground">有效期至 {plan.expires_at}</p></div><span class="text-sm">{plan.changes.length} 项</span></div>
            <ul class="mt-3 divide-y divide-border text-sm">{#each plan.changes as change, index (`${change.target_kind}:${change.target_id}:${change.field}:${index}`)}<li class="grid gap-1 py-2 sm:grid-cols-[90px_minmax(0,1fr)]"><span>{change.operation}</span><span class="break-all text-muted-foreground">{change.target_kind}:{change.target_id} · {change.field}</span></li>{/each}</ul>
            {#if !confirmation}<Button class="mt-3" onclick={() => (confirmation = true)}>核对并继续</Button>{:else}
              <label class="mt-3 flex items-start gap-2 text-sm"><input class="mt-0.5 size-4" type="checkbox" bind:checked={applyConfirmed} /><span>我已核对目标、修订和变更内容。</span></label>
              <div class="mt-3 flex gap-2"><Button disabled={!applyConfirmed || applyPlan.isPending} onclick={() => void applyPreparedPlan()}>确认应用</Button><Button variant="ghost" onclick={resetCommandState}>取消</Button></div>
            {/if}
          </div>
        {/if}
      </section>
    {/if}
    {#if message}<p class="text-sm" class:text-destructive={Boolean(decide.error || createPlan.error || applyPlan.error || retryRefresh.error)} aria-live="polite">{message}</p>{/if}
  </div>
{/if}
