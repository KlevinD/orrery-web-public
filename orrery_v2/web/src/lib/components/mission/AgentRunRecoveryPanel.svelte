<script lang="ts">
  import { ChevronDown, ChevronRight, RefreshCw, ShieldAlert } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import { useAgentRunRecoveryQuery, useAgentRunRecoveryReviewMutation } from '#lib/queries/agent-run-recovery'
  import { createIdempotencyKey } from '#lib/runtime/ids'
  import { ApiClientError } from '#lib/api/client'
  import { session } from '#lib/stores/session.svelte'
  import type { AgentRunRecoveryReviewInput } from '#lib/api/agent-run-recovery'

  interface Props {
    missionId: string
    runId: string
    active?: boolean
    retryPending?: boolean
    retryError?: unknown
    onRetry: () => void
  }
  let { missionId, runId, active = false, retryPending = false, retryError, onRetry }: Props = $props()
  let expanded = $state(false)
  let conclusion = $state<AgentRunRecoveryReviewInput['conclusion']>('uncertain')
  let note = $state('')
  let evidence = $state('')
  let saved = $state(false)
  let submitError = $state('')
  let previousBody = ''
  let idempotencyKey = ''
  let formScope = ''
  const statusQuery = useAgentRunRecoveryQuery(() => missionId, () => runId, () => expanded, () => active)
  const reviewMutation = useAgentRunRecoveryReviewMutation(() => missionId, () => runId)
  const status = $derived(statusQuery.data)
  const evidenceRefs = $derived(evidence.split('\n').map((value) => value.trim()).filter(Boolean))
  const formValid = $derived(Boolean(note.trim()) && new TextEncoder().encode(note).length <= 2000 &&
    evidenceRefs.length > 0 && evidenceRefs.length <= 5 && evidenceRefs.every((value) => new TextEncoder().encode(value).length <= 512))
  const phaseLabels: Record<string, string> = { not_started: '尚无持久记录', ready: '已保存检查点', tools_pending: '工具操作待核对', completed: '已完成' }
  const conclusionLabels = { confirmed_executed: '已确认执行', confirmed_not_executed: '已确认未执行', uncertain: '仍无法确认' }
  const toolStateLabels: Record<string, string> = { started: '已发起，结果待确认', completed: '已有完成记录', failed: '执行失败', unknown: '结果未知', not_dispatched: '未派发', not_recorded: '无执行记录' }

  $effect(() => {
    const identity = JSON.stringify([missionId, runId, session.securityContextVersion])
    if (identity !== formScope) {
      formScope = identity
      conclusion = 'uncertain'
      note = ''
      evidence = ''
      saved = false
      submitError = ''
      previousBody = ''
      idempotencyKey = ''
    }
  })

  function errorText(error: unknown) {
    if (error instanceof ApiClientError) {
      if (error.status === 403) return '你没有查看或登记运行核对记录的权限。'
      if (error.status === 404) return '此运行不可见或不支持长任务状态。'
      if (error.status === 409) return '运行状态已变化，请刷新后重新核对。'
    }
    return '操作未完成，请刷新状态后重试。'
  }

  function formatTime(value?: string) {
    if (!value || value.startsWith('0001-')) return '未设置'
    const time = new Date(value)
    return Number.isNaN(time.getTime()) ? '未设置' : time.toLocaleString('zh-CN', { hour12: false })
  }

  async function saveReview(event: SubmitEvent) {
    event.preventDefault()
    if (!status?.can_review || !formValid || reviewMutation.isPending) return
    const body = { revision: status.revision, conclusion, note: note.trim(), evidence_refs: evidenceRefs }
    const fingerprint = JSON.stringify([missionId, runId, body])
    const identity = [missionId, runId, session.securityContextVersion].join(':')
    if (fingerprint !== previousBody) {
      previousBody = fingerprint
      idempotencyKey = createIdempotencyKey()
    }
    saved = false
    submitError = ''
    try {
      await reviewMutation.mutateAsync({ body, idempotencyKey })
      if (identity !== [missionId, runId, session.securityContextVersion].join(':')) return
      saved = true
    } catch (error) {
      if (identity !== [missionId, runId, session.securityContextVersion].join(':')) return
      submitError = errorText(error)
      if (error instanceof ApiClientError && error.status === 409) void statusQuery.refetch()
    }
  }
</script>

<div class="mt-3 border-t border-border pt-3">
  <button class="flex items-center gap-2 text-xs font-medium text-foreground" aria-expanded={expanded} onclick={() => expanded = !expanded}>
    {#if expanded}<ChevronDown class="h-3.5 w-3.5" />{:else}<ChevronRight class="h-3.5 w-3.5" />{/if}
    长任务状态
  </button>
  {#if expanded}
    <div class="mt-3 space-y-4" aria-label={`长任务状态 ${runId}`}>
      {#if statusQuery.isLoading}
        <p class="text-xs text-muted-foreground" role="status">正在读取运行状态…</p>
      {:else if statusQuery.isError}
        <div role="alert" class="flex items-center justify-between gap-3 text-xs text-destructive">
          <span>{errorText(statusQuery.error)}</span>
          <Button size="sm" variant="outline" onclick={() => statusQuery.refetch()}>刷新状态</Button>
        </div>
      {:else if status}
        <div class="flex flex-wrap items-center justify-between gap-2">
          <Badge tone={status.restart_blocked ? 'warning' : 'neutral'}>{phaseLabels[status.phase] ?? status.phase}</Badge>
          <Button size="sm" variant="ghost" disabled={statusQuery.isFetching} onclick={() => statusQuery.refetch()} ariaLabel="刷新长任务状态">
            <RefreshCw class="h-3 w-3" />刷新
          </Button>
        </div>
        {#if status.available}
          <dl class="grid grid-cols-2 gap-3 text-xs sm:grid-cols-4">
            <div><dt class="text-muted-foreground">模型调用</dt><dd class="mt-1 font-medium">{status.model_calls} 次</dd></div>
            <div><dt class="text-muted-foreground">工具调用</dt><dd class="mt-1 font-medium">{status.tool_calls} 次</dd></div>
            <div><dt class="text-muted-foreground">记忆压缩</dt><dd class="mt-1 font-medium">{status.compactions} 次</dd></div>
            <div><dt class="text-muted-foreground">任务截止时间</dt><dd class="mt-1">{formatTime(status.deadline)}</dd></div>
          </dl>
          {#if status.summarizing}
            <div class="text-xs" role="status">
              <p class="mb-2">正在整理循环记忆 · 已处理 {status.summary_processed_bytes.toLocaleString()} / {status.summary_total_bytes.toLocaleString()} 字节</p>
              <progress class="h-1.5 w-full accent-brand" value={status.summary_processed_bytes} max={Math.max(1, status.summary_total_bytes)} aria-label="记忆整理进度"></progress>
            </div>
          {/if}
        {:else}
          <p class="text-xs text-muted-foreground">此运行尚无 AgentCore 持久记录。</p>
        {/if}
        {#if status.restart_blocked}
          <div class="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-xs">
            <p class="flex items-center gap-2 font-medium"><ShieldAlert class="h-4 w-4" />已有工具操作结果未确认，重新执行已暂停。</p>
            <p class="mt-2 leading-relaxed text-muted-foreground">请先核对外部系统的操作回执。登记结论后仍保留暂停状态，避免重复执行。</p>
            {#if status.pending_tools.length > 0}
              <ul class="mt-3 space-y-2">
                {#each status.pending_tools as tool (tool.call_id)}
                  <li class="flex flex-wrap justify-between gap-1"><span class="break-all">{tool.tool_id} · {tool.call_id}</span><span>{toolStateLabels[tool.state] ?? tool.state}</span></li>
                {/each}
              </ul>
            {/if}
          </div>
        {/if}
        {#if status.latest_review}
          <div class="rounded-lg bg-muted/50 p-3 text-xs" aria-label="最近核对记录">
            <p class="font-medium">{conclusionLabels[status.latest_review.conclusion]} · {status.latest_review.actor_id}</p>
            <p class="mt-1 text-muted-foreground">{formatTime(status.latest_review.recorded_at)}</p>
            <p class="mt-2 whitespace-pre-wrap break-words">{status.latest_review.note}</p>
            <ul class="mt-2 space-y-1 text-muted-foreground">{#each status.latest_review.evidence_refs as ref}<li class="break-all">{ref}</li>{/each}</ul>
          </div>
        {/if}
        {#if status.can_review}
          <form class="space-y-3" onsubmit={saveReview}>
            <label class="block text-xs font-medium">核对结论
              <select class="mt-1 block w-full rounded-md border border-border bg-card p-2" bind:value={conclusion}>
                <option value="uncertain">仍无法确认</option><option value="confirmed_executed">已确认执行</option><option value="confirmed_not_executed">已确认未执行</option>
              </select>
            </label>
            <label class="block text-xs font-medium">核对说明
              <textarea class="mt-1 block min-h-20 w-full rounded-md border border-border bg-card p-2" bind:value={note} maxlength={2000} required></textarea>
            </label>
            <label class="block text-xs font-medium">证据或回执编号
              <textarea class="mt-1 block min-h-16 w-full rounded-md border border-border bg-card p-2" bind:value={evidence} required></textarea>
            </label>
            <p class="text-[11px] text-muted-foreground">请在说明中记录核对过程和发现，最多 2000 字节。证据每行一条，最多 5 条，每条最多 512 字节。请使用回执编号或引用，避免填写密钥。</p>
            <Button type="submit" size="sm" variant="outline" disabled={!formValid || reviewMutation.isPending}>{reviewMutation.isPending ? '正在保存…' : '保存核对记录'}</Button>
          </form>
        {/if}
        {#if saved}<p role="status" class="text-xs text-muted-foreground">核对已登记，任务仍暂停，需要先处置外部操作。</p>{/if}
        {#if submitError}<p role="alert" class="text-xs text-destructive">{submitError}</p>{/if}
        {#if retryError}<p role="alert" class="text-xs text-destructive">{errorText(retryError)}</p>{/if}
        {#if status.can_restart && !status.restart_blocked}
          <div class="flex flex-wrap items-center gap-3">
            <Button size="sm" variant="outline" disabled={retryPending} onclick={onRetry}>重新执行</Button>
            <p class="text-xs text-muted-foreground">将创建新运行，从头处理此任务。</p>
          </div>
        {/if}
      {/if}
    </div>
  {/if}
</div>
