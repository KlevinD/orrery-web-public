<script lang="ts">
  import { CheckCircle2, ChevronLeft, ChevronRight, RotateCcw } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import { createIdempotencyKey } from '#lib/runtime/ids'
  import { ontologyScopeContext } from '#lib/ontology-workbench/scope-context.svelte'
  import { shouldRotateProjectionCommandKey } from '#lib/ontology-workbench/projection-command'
  import { hasPermissionScope } from '#lib/permissions/access'
  import {
    useOntologyProjectionAcknowledgeMutation,
    useOntologyProjectionAuditQuery,
    useOntologyProjectionReplayHistoryQuery,
    useOntologyProjectionReplayMutation
  } from '#lib/queries/ontology-projections'
  import { session } from '#lib/stores/session.svelte'

  interface Props { tenantId: string; auditId: string }
  let { tenantId, auditId }: Props = $props()
  let acknowledgementReason = $state('')
  let historyCursor = $state('')
  let historyCursorStack = $state<string[]>([])
  let boundAudit = $state('')
  let replayIdempotencyKey = $state('')
  let acknowledgementIdempotencyKey = $state('')
  let acknowledgementKeyReason = $state('')
  const detail = useOntologyProjectionAuditQuery(() => tenantId, () => auditId)
  const history = useOntologyProjectionReplayHistoryQuery(() => tenantId, () => auditId, () => ({ limit: 20, cursor: historyCursor || undefined }))
  const replay = useOntologyProjectionReplayMutation()
  const acknowledge = useOntologyProjectionAcknowledgeMutation()
  const canManage = $derived(Boolean(ontologyScopeContext.activeScope?.capabilities.includes('ontology.manage')) && hasPermissionScope(session.permissionScope, 'operations.manage'))
  const auditIsActionable = $derived(detail.data?.status === 'skipped')
  const mutationPending = $derived(replay.isPending || acknowledge.isPending)

  $effect(() => {
    const key = `${tenantId}\u0000${auditId}`
    if (key !== boundAudit) {
      boundAudit = key
      acknowledgementReason = ''
      historyCursor = ''
      historyCursorStack = []
      replayIdempotencyKey = createIdempotencyKey()
      acknowledgementIdempotencyKey = ''
      acknowledgementKeyReason = ''
      replay.reset()
      acknowledge.reset()
    }
  })

  function nextHistoryPage(cursor: string) {
    historyCursorStack = [...historyCursorStack, historyCursor]
    historyCursor = cursor
  }

  function previousHistoryPage() {
    historyCursor = historyCursorStack.at(-1) ?? ''
    historyCursorStack = historyCursorStack.slice(0, -1)
  }

  function replayAudit() {
    if (shouldRotateProjectionCommandKey(replay.error)) replayIdempotencyKey = ''
    if (!replayIdempotencyKey) replayIdempotencyKey = createIdempotencyKey()
    replay.mutate({ tenantId, auditId, idempotencyKey: replayIdempotencyKey })
  }

  function acknowledgeAudit() {
    const reason = acknowledgementReason.trim()
    if (!reason) return
    if (shouldRotateProjectionCommandKey(acknowledge.error)) acknowledgementIdempotencyKey = ''
    if (!acknowledgementIdempotencyKey || acknowledgementKeyReason !== reason) {
      acknowledgementIdempotencyKey = createIdempotencyKey()
      acknowledgementKeyReason = reason
    }
    acknowledge.mutate({ tenantId, auditId, reason, idempotencyKey: acknowledgementIdempotencyKey })
  }
</script>

{#if detail.isPending}
  <LoadingState />
{:else if detail.error}
  <ErrorState message={detail.error.message} />
{:else if detail.data}
  <div>
    <p class="text-xs text-muted-foreground">审计 ID</p>
    <p class="mt-1 break-all font-mono text-sm">{detail.data.id}</p>
    <dl class="mt-4 grid gap-3 text-sm">
      <div><dt class="text-xs text-muted-foreground">来源事件</dt><dd class="mt-1 break-all">{detail.data.source_event_id ?? '—'}</dd></div>
      <div><dt class="text-xs text-muted-foreground">状态</dt><dd class="mt-1">{detail.data.status ?? '—'}</dd></div>
      <div><dt class="text-xs text-muted-foreground">原因</dt><dd class="mt-1">{detail.data.reason ?? detail.data.reason_code ?? '—'}</dd></div>
    </dl>

    {#if canManage}
      <div class="mt-4 flex flex-wrap gap-2">
        <Button disabled={mutationPending || !auditIsActionable} onclick={replayAudit}><RotateCcw size={16} aria-hidden="true" />重放</Button>
        <Button disabled={mutationPending || !auditIsActionable || acknowledgementReason.trim().length === 0} onclick={acknowledgeAudit}><CheckCircle2 size={16} aria-hidden="true" />确认</Button>
      </div>
      <label class="mt-3 block space-y-1.5"><span class="text-xs font-medium text-muted-foreground">确认原因（必填）</span><Input value={acknowledgementReason} ariaLabel="Acknowledgement reason" oninput={(event) => (acknowledgementReason = event.currentTarget.value)} /></label>
      {#if replay.error}<div class="mt-3"><ErrorState message={replay.error.message} /></div>{/if}
      {#if acknowledge.error}<div class="mt-3"><ErrorState message={acknowledge.error.message} /></div>{/if}
    {/if}

    <div class="mt-5">
      <h3 class="font-medium">重放历史</h3>
      {#if history.isPending}<LoadingState />{:else if history.error}<ErrorState message={history.error.message} />{:else if !history.data || history.data.items.length === 0}<p class="mt-2 text-sm text-muted-foreground">暂无重放记录</p>{:else}
        <ul class="mt-2 divide-y divide-border border-y border-border text-sm">{#each history.data.items as attempt (attempt.id)}<li class="py-3"><div class="flex justify-between gap-2"><span>{attempt.status ?? '—'}</span><span class="text-xs text-muted-foreground">{attempt.attempted_at ?? '—'}</span></div>{#if attempt.error}<p class="mt-1 text-xs text-destructive">{attempt.error}</p>{/if}</li>{/each}</ul>
      {/if}
      <div class="mt-3 flex gap-2">
        {#if historyCursorStack.length > 0}<Button variant="outline" onclick={previousHistoryPage}><ChevronLeft size={16} aria-hidden="true" />上一页</Button>{/if}
        {#if history.data?.page.has_more && history.data.page.next_cursor}<Button onclick={() => nextHistoryPage(history.data?.page.next_cursor ?? '')}>下一页<ChevronRight size={16} aria-hidden="true" /></Button>{/if}
      </div>
    </div>
  </div>
{:else}
  <EmptyState title="审计详情不可用" />
{/if}
