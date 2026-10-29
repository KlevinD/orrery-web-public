<script lang="ts">
  import { BookOpen, CheckCircle2, Circle, ExternalLink, Route } from 'lucide-svelte'
  import { untrack } from 'svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { useOntologyContextObjectQuery } from '#lib/queries/ontology-context'
  import { useOntologyProblemQuery, useOntologyProblemRouteMutation } from '#lib/queries/ontology-problems'
  import { createIdempotencyKey } from '#lib/runtime/ids'

  interface Props {
    tenantId: string
    problemId: string
    canManage: boolean
  }

  let { tenantId, problemId, canManage }: Props = $props()
  let ownerKind = $state('team')
  let ownerId = $state('')
  let message = $state('')
  let messageIsError = $state(false)
  let boundProblem = $state('')

  const detail = useOntologyProblemQuery(() => tenantId, () => problemId)
  const routeProblem = useOntologyProblemRouteMutation()
  const candidate = useOntologyContextObjectQuery(
    () => tenantId,
    () => detail.data?.lesson_candidate_id ?? '',
    () => Boolean(detail.data?.lesson_candidate_id)
  )
  const candidateReviewOutcome = $derived(propertyText(candidate.data?.properties, 'review_outcome'))
  const acceptedMemoryId = $derived(propertyText(candidate.data?.properties, 'accepted_memory_id'))

  $effect(() => {
    const problem = detail.data
    if (!problem) return
    const binding = `${problem.tenant_id}:${problem.id}:${problem.version}`
    if (binding === boundProblem) return
    boundProblem = binding
    untrack(() => {
      ownerKind = problem.owner.kind || 'team'
      ownerId = problem.owner.id || ''
      message = ''
      messageIsError = false
    })
  })

  function assignOwner() {
    if (!ownerKind.trim() || !ownerId.trim()) return
    message = ''
    routeProblem.mutate(
      { tenantId, problemId, owner: { kind: ownerKind, id: ownerId }, idempotencyKey: createIdempotencyKey() },
      {
        onSuccess: () => {
          message = '负责人已更新'
          messageIsError = false
        },
        onError: (error) => {
          message = error instanceof Error ? error.message : '转派失败'
          messageIsError = true
        }
      }
    )
  }

  function propertyText(properties: Record<string, unknown> | undefined, key: string) {
    const value = properties?.[key]
    return typeof value === 'string' ? value : ''
  }

  function formatDate(value?: string) {
    if (!value) return '—'
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? value : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(parsed)
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

{#if detail.isPending}
  <LoadingState title="载入问题详情" description="正在汇总来源、证据和处置结果。" />
{:else if detail.error}
  <ErrorState message={detail.error.message} />
{:else if !detail.data}
  <EmptyState title="问题不可用" />
{:else}
  {@const problem = detail.data}
  <div class="space-y-5">
    <header class="border-b border-border pb-4">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <div class="mb-2 flex flex-wrap items-center gap-2">
            <Badge tone={statusTone(problem.status)}>{statusLabel(problem.status)}</Badge>
            <Badge tone="neutral">{problem.source_domain}</Badge>
            <Badge tone={problem.severity === 'critical' ? 'danger' : problem.severity === 'error' ? 'warning' : 'neutral'}>{problem.severity}</Badge>
          </div>
          <h3 class="break-words text-lg font-semibold">{problem.title}</h3>
          <p class="mt-2 break-words text-sm leading-6 text-muted-foreground">{problem.redacted_summary || '暂无摘要'}</p>
        </div>
        <div class="text-right text-xs text-muted-foreground">
          <div>出现 {problem.occurrence_count} 次</div>
          <div class="mt-1">更新于 {formatDate(problem.updated_at)}</div>
        </div>
      </div>
    </header>

    <ol class="grid grid-cols-5 border-y border-border py-3" aria-label="问题知识沉淀进度">
      {#each [
        { label: '问题', done: true },
        { label: '证据', done: problem.evidence_ids.length > 0 },
        { label: '结果', done: Boolean(problem.latest_outcome_id) },
        { label: '经验', done: Boolean(problem.lesson_candidate_id) },
        { label: '记忆', done: candidateReviewOutcome === 'accepted' || Boolean(acceptedMemoryId) }
      ] as stage}
        <li class="flex min-w-0 flex-col items-center gap-1 text-center text-[11px] {stage.done ? 'text-foreground' : 'text-muted-foreground'}">
          {#if stage.done}<CheckCircle2 class="size-4 text-status-done" aria-hidden="true" />{:else}<Circle class="size-4" aria-hidden="true" />{/if}
          <span>{stage.label}</span>
        </li>
      {/each}
    </ol>

    <section class="border-b border-border pb-5" aria-labelledby="problem-owner-title">
      <div class="mb-3 flex items-center justify-between gap-3">
        <h4 id="problem-owner-title" class="text-sm font-semibold">责任路由</h4>
        {#if problem.owner.id}<span class="text-xs text-muted-foreground">{problem.owner.kind} · {problem.owner.id}</span>{:else}<span class="text-xs text-muted-foreground">未分配</span>{/if}
      </div>
      {#if canManage}
        <div class="grid items-end gap-2 sm:grid-cols-[140px_minmax(0,1fr)_auto]">
          <Select value={ownerKind} options={[{ label: '团队', value: 'team' }, { label: '部门', value: 'department' }, { label: '人员', value: 'human' }, { label: '智能体', value: 'agent' }, { label: '服务', value: 'service' }]} onchange={(event) => (ownerKind = event.currentTarget.value)} />
          <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">负责人 ID</span><Input value={ownerId} ariaLabel="负责人 ID" oninput={(event) => (ownerId = event.currentTarget.value)} /></label>
          <Button disabled={!ownerId.trim() || routeProblem.isPending} onclick={assignOwner}><Route class="size-4" aria-hidden="true" />转派</Button>
        </div>
        {#if message}<p class="mt-2 text-xs {messageIsError ? 'text-destructive' : 'text-status-done'}">{message}</p>{/if}
      {/if}
    </section>

    <div class="grid gap-5 lg:grid-cols-2">
      <section class="min-w-0" aria-labelledby="problem-source-title">
        <h4 id="problem-source-title" class="text-sm font-semibold">来源与证据</h4>
        <dl class="mt-3 space-y-3 text-sm">
          <div><dt class="text-xs text-muted-foreground">首要来源</dt><dd class="mt-1 break-all font-mono text-xs">{problem.primary_source.kind}:{problem.primary_source.id}</dd></div>
          <div><dt class="text-xs text-muted-foreground">问题指纹</dt><dd class="mt-1 break-all font-mono text-xs">{problem.fingerprint}</dd></div>
          <div><dt class="text-xs text-muted-foreground">证据引用</dt><dd class="mt-1 flex flex-wrap gap-1.5">{#each problem.evidence_ids as id}<Badge tone="neutral" class="max-w-full break-all font-mono">{id}</Badge>{/each}{#if !problem.evidence_ids.length}<span class="text-muted-foreground">暂无</span>{/if}</dd></div>
        </dl>
        {#if problem.primary_source.kind === 'failure_incident'}
          <Button class="mt-4" size="sm" variant="outline" href={`/resources/ontology/reliability?tenant_id=${encodeURIComponent(tenantId)}&incident_id=${encodeURIComponent(problem.primary_source.id)}&source_tenant_id=${encodeURIComponent(tenantId)}`}><ExternalLink class="size-4" aria-hidden="true" />可靠性处置</Button>
        {/if}
      </section>

      <section class="min-w-0 border-t border-border pt-5 lg:border-l lg:border-t-0 lg:pl-5 lg:pt-0" aria-labelledby="problem-learning-title">
        <h4 id="problem-learning-title" class="text-sm font-semibold">结果与组织记忆</h4>
        <dl class="mt-3 space-y-3 text-sm">
          <div><dt class="text-xs text-muted-foreground">最近结果</dt><dd class="mt-1 leading-6">{problem.latest_outcome_summary || '尚未记录验证结果'}</dd></div>
          <div><dt class="text-xs text-muted-foreground">解决方案</dt><dd class="mt-1 break-all font-mono text-xs">{problem.latest_resolution.id ? `${problem.latest_resolution.kind}:${problem.latest_resolution.id}` : '—'}</dd></div>
          <div><dt class="text-xs text-muted-foreground">经验候选</dt><dd class="mt-1 break-all font-mono text-xs">{problem.lesson_candidate_id || '问题解决并验证后生成'}</dd></div>
          <div><dt class="text-xs text-muted-foreground">Memory 审核</dt><dd class="mt-1">{acceptedMemoryId ? `已接纳 · ${acceptedMemoryId}` : candidateReviewOutcome === 'accepted' ? '已接纳' : problem.lesson_candidate_id ? '待审核' : '尚未提交'}</dd></div>
        </dl>
        {#if problem.lesson_candidate_id}
          <Button class="mt-4" size="sm" variant="outline" href="/resources/memory"><BookOpen class="size-4" aria-hidden="true" />Memory 审核</Button>
        {/if}
      </section>
    </div>
  </div>
{/if}
