<script lang="ts">
  import { untrack } from 'svelte'
  import { AlertTriangle, Check, FileSearch, FlaskConical, Play, Plus, RefreshCw, ShieldCheck, Wrench, X } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Tabs from '#lib/components/ui/Tabs.svelte'
  import type {
    ReliabilityDecision,
    ReliabilityDiagnosisDecision,
    ReliabilityExecutionMode,
    ReliabilityRepairRuleDTO,
    ReliabilityRootCauseCategory,
    ReliabilityRuleStrategy
  } from '#lib/api/ontology-reliability'
  import {
    useOntologyReliabilityActivationDecisionMutation,
    useOntologyReliabilityActivationReviewMutation,
    useOntologyReliabilityAuthorizationMutation,
    useOntologyReliabilityDecisionMutation,
    useOntologyReliabilityDiagnosisDecisionMutation,
    useOntologyReliabilityDiagnosisMutation,
    useOntologyReliabilityIncidentQuery,
    useOntologyReliabilityInvokeMutation,
    useOntologyReliabilityRuleActivationMutation,
    useOntologyReliabilityRuleProposalMutation
  } from '#lib/queries/ontology-reliability'
  import { createIdempotencyKey } from '#lib/runtime/ids'

  interface Props {
    tenantId: string
    incidentId: string
    ruleId: string
    canManage: boolean
    onrulechange?: (ruleId: string) => void
  }

  let { tenantId, incidentId, ruleId, canManage, onrulechange = () => undefined }: Props = $props()
  let executionMode = $state<ReliabilityExecutionMode>('dry_run')
  let diagnosisCategory = $state<ReliabilityRootCauseCategory>('transient')
  let diagnosisSummary = $state('')
  let diagnosisConfidence = $state(0.7)
  let selectedEvidenceIds = $state<string[]>([])
  let diagnosisReason = $state('')
  let ruleStrategy = $state<ReliabilityRuleStrategy>('retry')
  let ruleReversible = $state(true)
  let ruleIdempotent = $state(true)
  let activationReason = $state('')
  let decisionReason = $state('')
  let message = $state('')
  let messageIsError = $state(false)
  let boundMaterial = $state('')

  const detail = useOntologyReliabilityIncidentQuery(() => tenantId, () => incidentId)
  const createDiagnosis = useOntologyReliabilityDiagnosisMutation()
  const decideDiagnosisMutation = useOntologyReliabilityDiagnosisDecisionMutation()
  const proposeRuleMutation = useOntologyReliabilityRuleProposalMutation()
  const requestActivationReview = useOntologyReliabilityActivationReviewMutation()
  const decideActivationReview = useOntologyReliabilityActivationDecisionMutation()
  const activateRuleMutation = useOntologyReliabilityRuleActivationMutation()
  const requestAuthorization = useOntologyReliabilityAuthorizationMutation()
  const decideAuthorization = useOntologyReliabilityDecisionMutation()
  const invokeAuthorization = useOntologyReliabilityInvokeMutation()
  const selectedRule = $derived(detail.data?.rules.find((rule) => rule.id === ruleId) ?? detail.data?.rules[0])
  const activationReview = $derived(
    detail.data?.activation_reviews.find((item) => item.rule_id === selectedRule?.id && item.current)
      ?? detail.data?.activation_reviews.find((item) => item.rule_id === selectedRule?.id && item.activated)
      ?? detail.data?.activation_reviews.find((item) => item.rule_id === selectedRule?.id)
  )
  const authorization = $derived(detail.data?.authorizations.find((item) => item.rule_id === selectedRule?.id && item.execution_mode === executionMode && item.current))
  const currentPreflight = $derived(detail.data?.preflights.find((item) => item.rule_id === selectedRule?.id && item.current && item.passed))
  const latestExecution = $derived(detail.data?.executions.find((item) => item.rule_id === selectedRule?.id))
  const commandPending = $derived(createDiagnosis.isPending || decideDiagnosisMutation.isPending || proposeRuleMutation.isPending || requestActivationReview.isPending || decideActivationReview.isPending || activateRuleMutation.isPending || requestAuthorization.isPending || decideAuthorization.isPending || invokeAuthorization.isPending)
  const actionReady = $derived(executionMode === 'dry_run' ? Boolean(selectedRule?.dry_run_ready) : Boolean(selectedRule?.apply_ready))
  const materialKey = $derived(`${tenantId}:${incidentId}:${selectedRule?.id ?? ''}:${selectedRule?.version ?? 0}`)

  $effect(() => {
    const firstRule = detail.data?.rules[0]?.id ?? ''
    if (firstRule && !ruleId) untrack(() => onrulechange(firstRule))
  })

  $effect(() => {
    if (boundMaterial === materialKey) return
    boundMaterial = materialKey
    activationReason = ''
    decisionReason = ''
    message = ''
    messageIsError = false
  })

  async function requestRuleActivation() {
    if (!selectedRule?.activation_ready) return
    clearMessage()
    try {
      await requestActivationReview.mutateAsync({
        tenantId,
        ruleId: selectedRule.id,
        purpose: '激活已通过当前预检且具备诊断证据的修复规则',
        idempotencyKey: createIdempotencyKey()
      })
      message = '规则激活评审已创建，尚未改变规则状态。'
    } catch (error) {
      showError(error, '无法创建规则激活评审')
    }
  }

  async function decideRuleActivation(decision: ReliabilityDecision) {
    if (!selectedRule || !activationReview || !activationReason.trim()) return
    clearMessage()
    try {
      await decideActivationReview.mutateAsync({
        tenantId,
        ruleId: selectedRule.id,
        reviewId: activationReview.id,
        decision,
        reason: activationReason,
        idempotencyKey: createIdempotencyKey()
      })
      activationReason = ''
      message = decision === 'authorize' ? '激活评审已批准，规则仍需显式激活。' : '激活评审已拒绝。'
    } catch (error) {
      showError(error, '无法提交激活评审决定')
    }
  }

  async function activateSelectedRule() {
    if (!selectedRule || !activationReview || activationReview.status !== 'resolved' || !activationReview.current) return
    clearMessage()
    try {
      await activateRuleMutation.mutateAsync({
        tenantId,
        ruleId: selectedRule.id,
        reviewId: activationReview.id,
        idempotencyKey: createIdempotencyKey()
      })
      message = '规则已激活；执行修复前需要针对新规则版本重新预检并单独审批。'
    } catch (error) {
      showError(error, '无法激活规则')
    }
  }

  async function requestApproval() {
    if (!selectedRule || !actionReady) return
    clearMessage()
    try {
      await requestAuthorization.mutateAsync({
        tenantId,
        incidentId,
        ruleId: selectedRule.id,
        executionMode,
        purpose: executionMode === 'dry_run' ? '验证当前事件的精确修复目标' : '修复当前事件的精确失败运行',
        idempotencyKey: createIdempotencyKey()
      })
      message = executionMode === 'dry_run' ? '预检审批已创建。' : '修复审批已创建。'
    } catch (error) {
      showError(error, '无法创建审批')
    }
  }

  function toggleEvidence(evidenceId: string, checked: boolean) {
    selectedEvidenceIds = checked
      ? Array.from(new Set([...selectedEvidenceIds, evidenceId]))
      : selectedEvidenceIds.filter((id) => id !== evidenceId)
  }

  async function proposeDiagnosis() {
    if (!diagnosisSummary.trim() || selectedEvidenceIds.length === 0) return
    clearMessage()
    try {
      await createDiagnosis.mutateAsync({
        tenantId,
        incidentId,
        rootCauseCategory: diagnosisCategory,
        redactedSummary: diagnosisSummary,
        confidence: diagnosisConfidence,
        evidenceIds: selectedEvidenceIds,
        idempotencyKey: createIdempotencyKey()
      })
      diagnosisSummary = ''
      selectedEvidenceIds = []
      message = '诊断提议已记录，等待人工确认。'
    } catch (error) {
      showError(error, '无法提交诊断提议')
    }
  }

  async function decideDiagnosis(diagnosisId: string, decision: ReliabilityDiagnosisDecision) {
    if (!diagnosisReason.trim()) return
    clearMessage()
    try {
      await decideDiagnosisMutation.mutateAsync({
        tenantId,
        incidentId,
        diagnosisId,
        decision,
        reason: diagnosisReason,
        idempotencyKey: createIdempotencyKey()
      })
      diagnosisReason = ''
      message = decision === 'confirm' ? '诊断已由人工确认，可据此生成候选规则。' : '诊断已拒绝并保留审计记录。'
    } catch (error) {
      showError(error, '无法提交诊断决定')
    }
  }

  async function proposeRule(diagnosisId: string) {
    clearMessage()
    try {
      const proposed = await proposeRuleMutation.mutateAsync({
        tenantId,
        incidentId,
        diagnosisId,
        strategy: ruleStrategy,
        reversible: ruleReversible,
        idempotent: ruleIdempotent,
        idempotencyKey: createIdempotencyKey()
      })
      onrulechange(proposed.id)
      message = '候选规则已生成，仍需预检、审批和受控执行。'
    } catch (error) {
      showError(error, '无法生成候选规则')
    }
  }

  async function decide(decision: ReliabilityDecision) {
    if (!selectedRule || !authorization || !decisionReason.trim()) return
    clearMessage()
    try {
      await decideAuthorization.mutateAsync({
        tenantId,
        incidentId,
        ruleId: selectedRule.id,
        authorizationId: authorization.id,
        decision,
        reason: decisionReason.trim(),
        idempotencyKey: createIdempotencyKey()
      })
      decisionReason = ''
      message = decision === 'authorize' ? '审批已通过，授权仍绑定当前事件版本。' : '审批已拒绝。'
    } catch (error) {
      showError(error, '无法提交审批决定')
    }
  }

  async function invoke() {
    if (!selectedRule || !authorization || authorization.status !== 'resolved') return
    clearMessage()
    try {
      const run = await invokeAuthorization.mutateAsync({
        tenantId,
        incidentId,
        ruleId: selectedRule.id,
        authorizationId: authorization.id,
        idempotencyKey: createIdempotencyKey()
      })
      message = `${executionMode === 'dry_run' ? '预检' : '修复'}已进入动作运行 ${run.id}。`
    } catch (error) {
      showError(error, '执行失败')
    }
  }

  function selectRule(id: string) {
    executionMode = 'dry_run'
    onrulechange(id)
  }

  function clearMessage() {
    message = ''
    messageIsError = false
  }

  function showError(error: unknown, fallback: string) {
    message = error instanceof Error ? error.message : fallback
    messageIsError = true
  }

  function formatDate(value?: string) {
    if (!value) return '—'
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? value : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'short' }).format(parsed)
  }

  function confidence(value: number) { return `${Math.round(value * 100)}%` }
  function modeLabel(mode: ReliabilityExecutionMode) { return mode === 'dry_run' ? '预检' : '修复' }
  function incidentStatusLabel(value: string) { return ({ open: '待处理', contained: '已控制', diagnosed: '已诊断', repairing: '修复中', monitoring: '观察中', resolved: '已解决', escalated: '已升级', quarantined: '已隔离' } as Record<string, string>)[value] ?? value }
  function severityLabel(value: string) { return ({ warning: '警告', error: '错误', critical: '严重' } as Record<string, string>)[value] ?? value }
  function strategyLabel(value: string) { return ({ retry: '重试', restart: '重启', fallback: '回退', reconfigure: '重配置', patch: '补丁', permission_change: '权限变更', data_mutation: '数据变更' } as Record<string, string>)[value] ?? value }
  function categoryLabel(value: string) { return ({ transient: '瞬时故障', configuration: '配置问题', code: '代码缺陷', permission: '权限问题', data: '数据问题', semantic: '语义偏差', unknown: '尚不确定' } as Record<string, string>)[value] ?? value }
  function diagnosisStatusLabel(value: string) { return ({ proposed: '待确认', confirmed: '已确认', rejected: '已拒绝' } as Record<string, string>)[value] ?? value }
  function ruleStatusLabel(value: string) { return ({ proposed: '待审核', active: '已启用', quarantined: '已隔离', retired: '已停用' } as Record<string, string>)[value] ?? value }
  function authorizationStatusLabel(value: string) { return ({ pending: '待审批', resolved: '已批准', denied: '已拒绝' } as Record<string, string>)[value] ?? value }
  function executionStatusLabel(value: string) { return ({ dispatching: '派发中', monitoring: '观察中', succeeded: '已验证', failed: '失败' } as Record<string, string>)[value] ?? value }
  function ruleTone(rule: ReliabilityRepairRuleDTO): 'success' | 'warning' | 'danger' | 'neutral' {
    if (rule.status === 'active') return 'success'
    if (rule.status === 'quarantined') return 'danger'
    if (rule.status === 'proposed') return 'warning'
    return 'neutral'
  }
  function blockerLabel(code: string) {
    return ({
      rule_binding_mismatch: '规则与事件指纹不匹配', rule_not_active: '规则尚未激活', strategy_not_executable: '策略尚无受控执行器',
      rule_not_reversible: '规则不可回滚', rule_not_idempotent: '规则不具备幂等性', incident_not_retryable: '事件不可重试',
      rule_expired: '规则已经过期', incident_state_ineligible: '事件状态不允许修复', exact_target_required: '缺少精确失败运行目标',
      current_preflight_required: '需要当前版本的通过预检', execution_active: '已有修复正在执行',
      rule_not_proposed: '只有候选规则可以申请激活', diagnosis_required: '规则缺少绑定诊断', diagnosis_not_current: '诊断不再对应当前事件版本',
      diagnosis_evidence_required: '诊断缺少证据'
    } as Record<string, string>)[code] ?? code
  }
</script>

{#if detail.isPending}
  <LoadingState />
{:else if detail.error}
  <ErrorState message={detail.error.message} />
{:else if !detail.data}
  <EmptyState title="事件不可用" />
{:else}
  {@const incident = detail.data.incident}
  <div class="space-y-4">
    <header class="border-b border-border pb-4">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0"><p class="text-xs text-muted-foreground">{incident.component} · {incident.error_code}</p><h3 class="mt-1 break-words text-base font-semibold">{incident.redacted_summary}</h3></div>
        <div class="flex shrink-0 gap-2"><Badge tone={incident.severity === 'critical' ? 'danger' : incident.severity === 'error' ? 'warning' : 'neutral'}>{severityLabel(incident.severity)}</Badge><Badge tone={incident.status === 'resolved' ? 'success' : incident.status === 'quarantined' ? 'danger' : 'info'}>{incidentStatusLabel(incident.status)}</Badge></div>
      </div>
      <dl class="mt-3 grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
        <div><dt class="text-[11px] text-muted-foreground">事件版本</dt><dd class="mt-1 font-mono text-xs">v{incident.version}</dd></div>
        <div><dt class="text-[11px] text-muted-foreground">发生次数</dt><dd class="mt-1">{incident.occurrence_count}</dd></div>
        <div><dt class="text-[11px] text-muted-foreground">最后出现</dt><dd class="mt-1 text-xs">{formatDate(incident.last_seen_at)}</dd></div>
        <div><dt class="text-[11px] text-muted-foreground">精确目标</dt><dd class="mt-1 truncate font-mono text-xs" title={detail.data.target?.run_id ?? ''}>{detail.data.target?.run_id ?? '未绑定'}</dd></div>
      </dl>
    </header>

    <section class="border-b border-border pb-4" aria-label="自动修复进度">
      <h4 class="mb-2 text-sm font-semibold">自动修复进度</h4>
      {#if detail.data.automatic_repair}
        {@const repair = detail.data.automatic_repair}
        <p class="text-sm">{({ pending: '等待修复', running: '正在排查与修复', retry_wait: '等待重试', needs_attention: '需要继续处理', technical_recovered: '技术恢复，业务待验收', resolved: '修复验证通过' } as Record<string, string>)[repair.status] ?? repair.status}</p>
        {#if !repair.current}<p class="mt-1 text-xs text-amber-600">这是历史维修记录，不代表当前故障状态。请核对上方状态及最新核验证据。</p>{/if}
        <p class="mt-2 whitespace-pre-wrap break-words text-xs text-muted-foreground">{repair.summary}</p>
        <p class="mt-2 text-xs text-muted-foreground">更新于 {formatDate(repair.updated_at)}</p>
        {#if repair.repair_run_id}<p class="mt-1 break-all font-mono text-xs">修复运行：{repair.repair_run_id}</p>{/if}
        {#if repair.evidence?.length}
          <details class="mt-2 text-xs"><summary class="cursor-pointer">查看验证证据</summary><ul class="mt-2 space-y-1">{#each repair.evidence as evidence}<li class="break-all">{evidence}</li>{/each}</ul></details>
        {/if}
      {:else}
        <p class="text-xs text-muted-foreground">暂无自动修复记录。</p>
      {/if}
    </section>

    <section class="border-b border-border pb-4" aria-label="最近错误明细">
      <h4 class="mb-2 text-sm font-semibold">最近错误明细</h4>
      {#if detail.data.diagnostics_warning}
        <p class="text-sm text-muted-foreground">{detail.data.diagnostics_warning === 'manage_access_required' ? '查看错误明细需要当前范围的管理权限。' : '最近诊断暂时无法读取，请稍后刷新。'}</p>
      {:else if !detail.data.diagnostics?.length}
        <p class="text-xs text-muted-foreground">暂无已采集的明细。</p>
      {:else}
        <div class="space-y-2">
          {#each detail.data.diagnostics as diagnostic (diagnostic.id)}
            <details class="min-w-0 rounded border border-border px-3 py-2">
              <summary class="cursor-pointer break-words text-xs">{formatDate(diagnostic.occurred_at)} · {diagnostic.source === 'web_client' ? '客户端' : '服务端'} · {diagnostic.correlation_id || diagnostic.id}</summary>
              <dl class="mt-3 space-y-2 text-xs">
                {#each Object.entries(diagnostic.details) as [key, value] (key)}
                  <div><dt class="text-muted-foreground">{({ cause: '错误原因', stack: '调用堆栈', path: '请求路径', request_id: '请求编号', run_id: '运行编号', task_id: '任务编号', operation: '操作', runtime_kind: '运行环境' } as Record<string, string>)[key] ?? key}</dt><dd><pre class="mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded bg-muted/40 p-2 font-mono">{value}</pre></dd></div>
                {/each}
              </dl>
            </details>
          {/each}
        </div>
      {/if}
    </section>

    <section class="border-b border-border pb-4" aria-label="诊断与证据">
      <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
        <h4 class="flex items-center gap-2 text-sm font-semibold"><FileSearch class="size-4" aria-hidden="true" />诊断与证据</h4>
        <span class="text-xs text-muted-foreground">{detail.data.available_evidence.length} 条可见证据 · {detail.data.diagnoses.length} 个诊断</span>
      </div>
      <div class="grid gap-5 lg:grid-cols-2 lg:divide-x lg:divide-border">
        <div class="min-w-0 lg:pr-5">
          <p class="mb-2 text-xs font-medium text-muted-foreground">事件证据</p>
          {#if detail.data.available_evidence.length}
            <div class="divide-y divide-border border-y border-border">
              {#each detail.data.available_evidence as evidence (evidence.id)}
                <label class="flex min-h-12 cursor-pointer items-center gap-3 px-1 py-2.5">
                  <input
                    type="checkbox"
                    class="size-4 shrink-0 accent-primary"
                    checked={selectedEvidenceIds.includes(evidence.id)}
                    disabled={!canManage || commandPending}
                    onchange={(event) => toggleEvidence(evidence.id, event.currentTarget.checked)}
                  />
                  <span class="min-w-0 flex-1"><span class="block break-words text-sm">{evidence.label || '故障观察'}</span><span class="mt-0.5 block truncate font-mono text-[10px] text-muted-foreground" title={evidence.id}>{evidence.id}</span></span>
                  {#if evidence.status}<Badge tone={evidence.status === 'observed' ? 'info' : 'neutral'}>{evidence.status === 'observed' ? '已观察' : evidence.status}</Badge>{/if}
                </label>
              {/each}
            </div>
          {:else}
            <EmptyState title="暂无可引用的事件证据" />
          {/if}

          {#if canManage && detail.data.available_evidence.length}
            <div class="mt-4 grid gap-3">
              <div class="grid gap-3 sm:grid-cols-[minmax(150px,0.7fr)_minmax(0,1.3fr)]">
                <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">根因类别</span><select class="h-9 w-full border border-input bg-background px-2.5 text-sm outline-none focus:border-ring focus:ring-1 focus:ring-ring" bind:value={diagnosisCategory} disabled={commandPending} aria-label="诊断根因类别"><option value="transient">瞬时故障</option><option value="configuration">配置问题</option><option value="code">代码缺陷</option><option value="permission">权限问题</option><option value="data">数据问题</option><option value="semantic">语义偏差</option><option value="unknown">尚不确定</option></select></label>
                <label class="space-y-1.5"><span class="flex items-center justify-between text-xs font-medium text-muted-foreground"><span>置信度</span><span>{confidence(diagnosisConfidence)}</span></span><input type="range" min="0" max="1" step="0.05" bind:value={diagnosisConfidence} disabled={commandPending} class="h-9 w-full accent-primary" aria-label="诊断置信度" /></label>
              </div>
              <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">诊断摘要</span><textarea class="min-h-20 w-full resize-y border border-input bg-background px-3 py-2 text-sm outline-none focus:border-ring focus:ring-1 focus:ring-ring" bind:value={diagnosisSummary} maxlength="2048" disabled={commandPending} aria-label="诊断摘要"></textarea></label>
              <div><Button disabled={!diagnosisSummary.trim() || selectedEvidenceIds.length === 0 || commandPending} onclick={() => void proposeDiagnosis()}>{#if createDiagnosis.isPending}<RefreshCw class="size-4 animate-spin" aria-hidden="true" />{:else}<Plus class="size-4" aria-hidden="true" />{/if}提交诊断提议</Button></div>
            </div>
          {/if}
        </div>

        <div class="min-w-0 lg:pl-5">
          <p class="mb-2 text-xs font-medium text-muted-foreground">诊断记录</p>
          {#if !detail.data.diagnoses.length}
            <EmptyState title="尚无诊断记录" />
          {:else}
            <div class="divide-y divide-border border-y border-border">
              {#each detail.data.diagnoses as diagnosis (diagnosis.id)}
                {@const hasLearnedRule = detail.data.rules.some((rule) => rule.diagnosis_case_id === diagnosis.id)}
                <article class="py-3">
                  <div class="flex flex-wrap items-start justify-between gap-2"><div class="min-w-0"><p class="text-sm font-medium">{categoryLabel(diagnosis.root_cause_category)}</p><p class="mt-1 break-words text-xs text-muted-foreground">{diagnosis.redacted_summary}</p></div><Badge tone={diagnosis.status === 'confirmed' ? 'success' : diagnosis.status === 'rejected' ? 'danger' : 'warning'}>{diagnosisStatusLabel(diagnosis.status)}</Badge></div>
                  <p class="mt-2 text-[11px] text-muted-foreground">置信 {confidence(diagnosis.confidence)} · 事件 v{diagnosis.incident_version} · {diagnosis.evidence_ids.length} 条证据 · 提议者 {diagnosis.proposed_by.kind}</p>
                  {#if diagnosis.review_reason}<p class="mt-2 border-l-2 border-border pl-2 text-xs text-muted-foreground">{diagnosis.review_reason}</p>{/if}

                  {#if canManage && diagnosis.status === 'proposed'}
                    <div class="mt-3"><Input value={diagnosisReason} ariaLabel="诊断审核意见" oninput={(event) => (diagnosisReason = event.currentTarget.value)} /></div>
                    <div class="mt-2 flex flex-wrap gap-2"><Button disabled={!diagnosisReason.trim() || commandPending} onclick={() => void decideDiagnosis(diagnosis.id, 'confirm')}><Check class="size-4" aria-hidden="true" />确认诊断</Button><Button variant="outline" disabled={!diagnosisReason.trim() || commandPending} onclick={() => void decideDiagnosis(diagnosis.id, 'reject')}><X class="size-4" aria-hidden="true" />拒绝</Button></div>
                  {:else if canManage && diagnosis.status === 'confirmed' && !hasLearnedRule}
                    <div class="mt-3 grid gap-3 sm:grid-cols-[minmax(140px,0.8fr)_minmax(0,1.2fr)]">
                      <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">修复策略</span><select class="h-9 w-full border border-input bg-background px-2.5 text-sm outline-none focus:border-ring focus:ring-1 focus:ring-ring" bind:value={ruleStrategy} disabled={commandPending} aria-label="候选修复策略"><option value="retry">重试</option><option value="restart">重启</option><option value="fallback">回退</option><option value="reconfigure">重配置</option><option value="patch">补丁</option><option value="permission_change">权限变更</option><option value="data_mutation">数据变更</option></select></label>
                      <div class="flex flex-wrap items-end gap-x-4 gap-y-2 pb-1 text-xs"><label class="flex items-center gap-2"><input type="checkbox" class="size-4 accent-primary" bind:checked={ruleReversible} disabled={commandPending} />可回滚</label><label class="flex items-center gap-2"><input type="checkbox" class="size-4 accent-primary" bind:checked={ruleIdempotent} disabled={commandPending} />幂等</label></div>
                    </div>
                    <div class="mt-3"><Button disabled={commandPending} onclick={() => void proposeRule(diagnosis.id)}>{#if proposeRuleMutation.isPending}<RefreshCw class="size-4 animate-spin" aria-hidden="true" />{:else}<Wrench class="size-4" aria-hidden="true" />{/if}生成候选规则</Button></div>
                  {:else if diagnosis.status === 'confirmed' && hasLearnedRule}
                    <p class="mt-2 flex items-center gap-1.5 text-xs text-status-done"><Check class="size-3.5" aria-hidden="true" />已形成候选规则</p>
                  {/if}
                </article>
              {/each}
            </div>
          {/if}
        </div>
      </div>
    </section>

    <div class="grid gap-4 xl:grid-cols-[minmax(210px,0.72fr)_minmax(0,1.28fr)]">
      <section class="min-w-0 border-b border-border pb-4 xl:border-b-0 xl:border-r xl:pb-0 xl:pr-4" aria-label="修复规则">
        <div class="mb-2 flex items-center justify-between"><h4 class="text-sm font-semibold">修复规则</h4><span class="text-xs text-muted-foreground">{detail.data.rules.length}</span></div>
        {#if !detail.data.rules.length}<EmptyState title="尚未学习到修复规则" />{:else}
          <div class="divide-y divide-border border-y border-border">
            {#each detail.data.rules as rule (rule.id)}
              <button type="button" class="block w-full px-3 py-3 text-left transition-colors hover:bg-muted" class:bg-muted={selectedRule?.id === rule.id} aria-pressed={selectedRule?.id === rule.id} onclick={() => selectRule(rule.id)}>
                <span class="flex items-center justify-between gap-2"><span class="truncate text-sm font-medium">{strategyLabel(rule.strategy)}</span><Badge tone={ruleTone(rule)}>{ruleStatusLabel(rule.status)}</Badge></span>
                <span class="mt-2 grid grid-cols-3 gap-2 text-[11px] text-muted-foreground"><span>置信 {confidence(rule.confidence)}</span><span>成功 {rule.success_count}</span><span>失败 {rule.failure_count}</span></span>
                <span class="mt-2 block truncate font-mono text-[10px] text-muted-foreground">v{rule.version} · {rule.diagnosis_case_id ? '诊断支撑' : '历史规则'} · {rule.action_type_id}</span>
              </button>
            {/each}
          </div>
        {/if}
      </section>

      <section class="min-w-0 space-y-4" aria-label="修复控制流">
        {#if selectedRule}
          <ol class="grid grid-cols-4 gap-px overflow-hidden rounded border border-border bg-border text-center text-[11px] sm:grid-cols-8" aria-label="可靠性闭环阶段">
            {#each [
              ['捕获', true], ['诊断', detail.data.diagnoses.some((item) => item.status === 'confirmed')], ['规则', true], ['预检', Boolean(currentPreflight)], ['激活', selectedRule.status === 'active'], ['审批', Boolean(authorization?.status === 'resolved')],
              ['执行', Boolean(latestExecution)], ['验证', latestExecution?.status === 'succeeded']
            ] as step}
              <li class="min-h-14 bg-background px-2 py-2"><span class="mx-auto mb-1 flex size-5 items-center justify-center rounded-full {step[1] ? 'bg-status-done-bg text-status-done' : 'bg-muted text-muted-foreground'}">{#if step[1]}<Check class="size-3" aria-hidden="true" />{:else}<span class="size-1.5 rounded-full bg-current"></span>{/if}</span>{step[0]}</li>
            {/each}
          </ol>

          <section class="border-y border-border py-3" aria-label="规则激活评审">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div>
                <h5 class="flex items-center gap-2 text-sm font-semibold"><ShieldCheck class="size-4" aria-hidden="true" />规则激活评审</h5>
                <p class="mt-1 text-xs text-muted-foreground">激活仅改变规则生命周期，不授权任何一次修复执行。</p>
              </div>
              <Badge tone={selectedRule.status === 'active' ? 'success' : activationReview?.status === 'denied' ? 'danger' : activationReview?.status === 'pending' ? 'warning' : 'neutral'}>
                {selectedRule.status === 'active' ? '已激活' : activationReview ? authorizationStatusLabel(activationReview.status) : '未申请'}
              </Badge>
            </div>

            {#if selectedRule.status === 'active'}
              <dl class="mt-3 grid gap-2 text-xs sm:grid-cols-2">
                <div><dt class="text-muted-foreground">激活审批</dt><dd class="mt-1 truncate font-mono" title={selectedRule.activation_decision_id ?? ''}>{selectedRule.activation_decision_id ?? '—'}</dd></div>
                <div><dt class="text-muted-foreground">激活时间</dt><dd class="mt-1">{formatDate(selectedRule.activated_at)}</dd></div>
              </dl>
            {:else}
              {#if selectedRule.activation_blockers.length}
                <ul class="mt-3 space-y-1 text-xs text-muted-foreground" aria-label="激活阻断原因">
                  {#each selectedRule.activation_blockers as blocker}<li>{blockerLabel(blocker)}</li>{/each}
                </ul>
              {/if}
              {#if activationReview}
                <p class="mt-3 text-xs text-muted-foreground">规则 v{activationReview.rule_version} · {activationReview.current ? '材料仍有效' : '材料已变化'} · {formatDate(activationReview.created_at)}</p>
              {/if}
              {#if canManage && activationReview?.status === 'pending' && activationReview.current}
                <label class="mt-3 block space-y-1.5"><span class="text-xs font-medium text-muted-foreground">激活评审意见</span><Input value={activationReason} ariaLabel="规则激活评审意见" oninput={(event) => (activationReason = event.currentTarget.value)} /></label>
                <div class="mt-3 flex flex-wrap gap-2">
                  <Button disabled={!activationReason.trim() || commandPending} onclick={() => void decideRuleActivation('authorize')}><Check class="size-4" aria-hidden="true" />批准激活</Button>
                  <Button variant="outline" disabled={!activationReason.trim() || commandPending} onclick={() => void decideRuleActivation('deny')}><X class="size-4" aria-hidden="true" />拒绝</Button>
                </div>
              {:else if canManage && activationReview?.status === 'resolved' && activationReview.current}
                <div class="mt-3"><Button disabled={commandPending} onclick={() => void activateSelectedRule()}>{#if activateRuleMutation.isPending}<RefreshCw class="size-4 animate-spin" aria-hidden="true" />{:else}<ShieldCheck class="size-4" aria-hidden="true" />{/if}激活规则</Button></div>
              {:else if canManage && selectedRule.activation_ready}
                <div class="mt-3"><Button disabled={commandPending} onclick={() => void requestRuleActivation()}>{#if requestActivationReview.isPending}<RefreshCw class="size-4 animate-spin" aria-hidden="true" />{:else}<ShieldCheck class="size-4" aria-hidden="true" />{/if}申请激活评审</Button></div>
              {/if}
            {/if}
          </section>

          <div class="flex flex-wrap items-center justify-between gap-3">
            <Tabs bind:value={executionMode} tabs={[{ label: '预检', value: 'dry_run' }, { label: '修复', value: 'apply' }]} ariaLabel="执行模式" />
            <div class="flex items-center gap-2 text-xs text-muted-foreground"><ShieldCheck class="size-4" aria-hidden="true" />{selectedRule.reversible ? '可回滚' : '不可回滚'} · {selectedRule.idempotent ? '幂等' : '非幂等'}</div>
          </div>

          {#if executionMode === 'apply' && selectedRule.apply_blockers.length}
            <div class="border-l-2 border-status-blocked pl-3" role="status"><p class="flex items-center gap-2 text-sm font-medium"><AlertTriangle class="size-4 text-status-blocked" aria-hidden="true" />修复被阻断</p><ul class="mt-2 space-y-1 text-xs text-muted-foreground">{#each selectedRule.apply_blockers as blocker}<li>{blockerLabel(blocker)}</li>{/each}</ul></div>
          {/if}

          <dl class="divide-y divide-border border-y border-border text-sm">
            <div class="grid gap-1 py-2 sm:grid-cols-[112px_minmax(0,1fr)]"><dt class="text-muted-foreground">当前预检</dt><dd>{currentPreflight ? `${currentPreflight.passed ? '通过' : '未通过'} · ${formatDate(currentPreflight.checked_at)}` : '尚无当前证据'}</dd></div>
            <div class="grid gap-1 py-2 sm:grid-cols-[112px_minmax(0,1fr)]"><dt class="text-muted-foreground">审批状态</dt><dd>{authorization ? `${modeLabel(authorization.execution_mode)} · ${authorizationStatusLabel(authorization.status)}${authorization.current ? '' : ' · 已失效'}` : '尚未申请'}</dd></div>
            <div class="grid gap-1 py-2 sm:grid-cols-[112px_minmax(0,1fr)]"><dt class="text-muted-foreground">最近执行</dt><dd>{latestExecution ? `${executionStatusLabel(latestExecution.status)} · ${formatDate(latestExecution.updated_at)}` : '尚未执行'}</dd></div>
            <div class="grid gap-1 py-2 sm:grid-cols-[112px_minmax(0,1fr)]"><dt class="text-muted-foreground">规则依据</dt><dd class="truncate font-mono text-[11px]" title={selectedRule.diagnosis_case_id ?? ''}>{selectedRule.diagnosis_case_id ?? '历史导入规则'}</dd></div>
            <div class="grid gap-1 py-2 sm:grid-cols-[112px_minmax(0,1fr)]"><dt class="text-muted-foreground">参数绑定</dt><dd class="truncate font-mono text-[11px]" title={selectedRule.args_hash}>{selectedRule.args_hash}</dd></div>
          </dl>

          {#if canManage}
            <div class="border-t border-border pt-4">
              {#if authorization?.status === 'pending'}
                <label class="space-y-1.5"><span class="text-xs font-medium text-muted-foreground">审批意见</span><Input value={decisionReason} ariaLabel="可靠性审批意见" oninput={(event) => (decisionReason = event.currentTarget.value)} /></label>
                <div class="mt-3 flex flex-wrap gap-2"><Button disabled={!decisionReason.trim() || commandPending} onclick={() => void decide('authorize')}><Check class="size-4" aria-hidden="true" />批准{modeLabel(executionMode)}</Button><Button variant="outline" disabled={!decisionReason.trim() || commandPending} onclick={() => void decide('deny')}><X class="size-4" aria-hidden="true" />拒绝</Button></div>
              {:else if authorization?.status === 'resolved'}
                <Button disabled={!actionReady || commandPending} onclick={() => void invoke()}>{#if commandPending}<RefreshCw class="size-4 animate-spin" aria-hidden="true" />{:else if executionMode === 'dry_run'}<FlaskConical class="size-4" aria-hidden="true" />{:else}<Play class="size-4" aria-hidden="true" />{/if}执行{modeLabel(executionMode)}</Button>
              {:else}
                <Button disabled={!actionReady || commandPending} onclick={() => void requestApproval()}><ShieldCheck class="size-4" aria-hidden="true" />申请{modeLabel(executionMode)}审批</Button>
              {/if}
            </div>
          {/if}

          {#if detail.data.authorizations.length > 0}
            <section class="border-t border-border pt-4" aria-label="最近审批"><h5 class="text-xs font-semibold text-muted-foreground">最近审批</h5><ul class="mt-2 divide-y divide-border text-xs">{#each detail.data.authorizations.slice(0, 4) as item (item.id)}<li class="flex flex-wrap items-center justify-between gap-2 py-2"><span>{modeLabel(item.execution_mode)} · {authorizationStatusLabel(item.status)}{item.current ? '' : ' · 已失效'}</span><span class="text-muted-foreground">{formatDate(item.created_at)}</span></li>{/each}</ul></section>
          {/if}
          {#if message}<p class="text-sm" class:text-destructive={messageIsError} aria-live="polite">{message}</p>{/if}
        {:else}
          <EmptyState title="选择修复规则" />
        {/if}
      </section>
    </div>
  </div>
{/if}
