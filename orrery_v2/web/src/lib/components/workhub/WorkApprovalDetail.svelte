<script lang="ts">
  import { createQuery } from '@tanstack/svelte-query'
  import ApprovalList from '#lib/components/approvals/ApprovalList.svelte'
  import ApprovalDecisionDialog from '#lib/components/approvals/ApprovalDecisionDialog.svelte'
  import CouncilWorkspace from '#lib/components/approvals/CouncilWorkspace.svelte'
  import { approvalApi } from '#lib/api/approvals'
  import { apiFetch } from '#lib/api/client'
  import { mapHILDecisionPage, mapHILDecisionToApprovalItem } from '#lib/mappers/hil'
  import { mapApprovalCenterView } from '#lib/mappers/approval'
  import { loadAllPages } from '#lib/queries/work-inbox'
  import { useWorkActionWorkflows } from '#lib/queries/work-actions'
  import { useHILCouncilQuery, useHILContributionMutation, useHILVoteMutation, useHILDecisionMutation, useApprovalDecisionMutation, useHILWorkflowTaskDecisionMutation, useHILWorkflowTaskTransferMutation, useHILWorkflowViewedMutation, useHILWorkflowDetailQuery } from '#lib/queries/approvals'
  import { currentSessionUserIdentityIds, session } from '#lib/stores/session.svelte'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { locale } from '#lib/i18n'
  import type { ApprovalItemViewModel, ApprovalActionViewModel, ApprovalDecisionFormViewModel, HILWorkflowDecision } from '#lib/types/view-models'
  import type { ApprovalPageDTO, HILDecisionPageDTO } from '#lib/types/api'
  import type { WorkActionItem } from './work-actions'

  let { item, workflowId = '', onprocessed }: { item: WorkActionItem; workflowId?: string; onprocessed?: () => void } = $props()
  const L = (zh: string, en: string) => $locale.startsWith('zh') ? zh : en
  const actors = $derived(currentSessionUserIdentityIds())
  const identity = $derived(`${session.user.userId}:${session.viewerKind}:${session.securityContextVersion}`)
  const canDecide = $derived(hasPermissionScope(session.permissionScope, 'approvals.decide'))
  const canManage = $derived(hasPermissionScope(session.permissionScope, 'approvals.manage'))
  const isHIL = $derived(Boolean(item.approval?.sourceLabel.startsWith('hil:')))
  const workflows = useWorkActionWorkflows(() => identity, () => actors, () => Boolean(item.approval), () => 'all', () => item.approval?.id ?? '')
  const exactWorkflow = useHILWorkflowDetailQuery(() => workflowId, () => actors, () => Boolean(workflowId))
  const workflow = $derived(exactWorkflow.data?.decisionRequestId === item.approval?.id ? exactWorkflow.data : workflows.data?.find(value => value.decisionRequestId === item.approval?.id))
  let refreshDetail = $state(false)
  const fresh = createQuery(() => ({
    queryKey: ['approvals', 'work-action-detail', identity, item.id], enabled: refreshDetail && Boolean(item.approval), staleTime: 0,
    queryFn: async ({ signal }): Promise<ApprovalItemViewModel | undefined> => {
      if (!item.approval) return undefined
      if (isHIL) {
        const decision = await apiFetch<HILDecisionPageDTO['items'][number]>(`/api/hil/decisions/${encodeURIComponent(item.approval.id)}`, { signal })
        return mapHILDecisionToApprovalItem(mapHILDecisionPage({ items: [decision], total: 1 })[0], item.approval.sourceLabel === 'hil:hil' ? 'hil' : 'approval')
      }
      const rows = await loadAllPages(async offset => {
        const page = await approvalApi.listApprovals(`?limit=100&offset=${offset}`, { signal }) as ApprovalPageDTO
        return { items: page.items ?? [], total: page.page?.total ?? 0 }
      })
      return mapApprovalCenterView({ items: rows, page: { total: rows.length, returned: rows.length } }).items.find(value => value.id === item.approval?.id)
    }
  }))
  const approval = $derived(fresh.data ?? item.approval)
  const visibleWorkflow = $derived(workflow && !canDecide ? { ...workflow, activeTask: undefined, tasks: workflow.tasks.map(task => ({ ...task, actionable: false })) } : workflow)
  const councilID = $derived(item.council?.councilId ?? '')
  const council = useHILCouncilQuery(() => councilID, () => Boolean(councilID))
  const hilMutation = useHILDecisionMutation(), legacyMutation = useApprovalDecisionMutation()
  const workflowMutation = useHILWorkflowTaskDecisionMutation(), transferMutation = useHILWorkflowTaskTransferMutation()
  const viewedMutation = useHILWorkflowViewedMutation(), contributionMutation = useHILContributionMutation(), voteMutation = useHILVoteMutation()
  let action = $state<ApprovalActionViewModel>()
  let actionItem = $state<ApprovalItemViewModel>()
  let message = $state(''), error = $state(''), submitted = $state(false)
  const busy = $derived(hilMutation.isPending || legacyMutation.isPending || workflowMutation.isPending || transferMutation.isPending)
  const workflowError = $derived(workflow ? '' : workflows.error instanceof Error ? workflows.error.message : exactWorkflow.error instanceof Error ? exactWorkflow.error.message : '')
  const actionsReady = $derived(canDecide && !submitted && !workflows.isPending && !workflows.isError && (!workflowId || !exactWorkflow.isPending && !exactWorkflow.isError))
  function fail(cause: unknown) { error = cause instanceof Error ? cause.message : L('提交失败，请重试。', 'Could not submit. Please retry.') }
  function processed() {
    message = L('处理结果已保存', 'Your response has been saved'); error = ''; action = undefined; actionItem = undefined
    submitted = true; refreshDetail = true; void fresh.refetch(); onprocessed?.()
  }
  function open(value: ApprovalItemViewModel, next: ApprovalActionViewModel) {
    if (!actionsReady || busy) return
    actionItem = value; action = next; error = ''
  }
  function decide(form: { message: string; modifiedInput?: string; choiceId?: string; remember?: boolean }) {
    if (!canDecide || !actionItem || !action || busy || submitted) return
    const value: ApprovalDecisionFormViewModel = { approvalId: actionItem.id, actionId: action.id, taskId: actionItem.taskId,
      actionPath: action.path && action.path !== `/api/approvals/${actionItem.id}/decision` ? action.path : undefined,
      message: form.message, modifiedInput: form.modifiedInput, choiceId: form.choiceId, remember: form.remember, planDigest: actionItem.planDigest }
    const mutation = actionItem.sourceLabel.startsWith('hil:') || action.path.startsWith('/api/hil/decisions/') ? hilMutation : legacyMutation
    mutation.mutate(value, { onSuccess: processed, onError: fail })
  }
  function decideWorkflow(input: { taskId: string; decision: HILWorkflowDecision; reason: string; expectedVersion: number }) {
    if (!canDecide || !workflow || busy) return
    workflowMutation.mutate({ instanceId: workflow.id, ...input }, { onSuccess: processed, onError: fail })
  }
  function transfer(input: { taskId: string; newAssigneeId: string; reason: string; expectedVersion: number; administrator: boolean }) {
    if (!workflow || busy) return
    transferMutation.mutate({ instanceId: workflow.id, ...input }, { onSuccess: processed, onError: fail })
  }
</script>

<section class="min-w-0 space-y-3" data-work-approval-detail>
  {#if message}<p role="status" class="rounded-md border border-emerald-500/20 bg-emerald-500/5 p-3 text-sm">{message}</p>{/if}
  {#if error}<p role="alert" class="rounded-md border border-destructive/20 p-3 text-sm text-destructive">{error}</p>{/if}
  {#if fresh.isError}<p role="alert" class="text-sm text-amber-700">{L('处理结果已提交，详情刷新失败，请重试。', 'Submitted, but details could not be refreshed.')} <button class="underline" onclick={() => fresh.refetch()}>{L('重试', 'Retry')}</button></p>{/if}
  {#if approval}
    {#if workflowError}<p role="alert" class="text-sm text-amber-700">{L('审批流程暂时无法加载，请刷新后处理。', 'The approval workflow could not be loaded. Refresh before responding.')}</p>{/if}
    <ApprovalList detailOnly items={[actionsReady ? approval : { ...approval, actions: [] }]} selectedId={approval.id} autoSelectFirst={false}
      workflowByDecisionId={visibleWorkflow ? { [approval.id]: visibleWorkflow } : {}} workflowLoaded={!workflows.isPending && (!workflowId || !exactWorkflow.isPending)}
      {workflowError} workflowBusy={busy} {canManage} onaction={open} onworkflowdecision={decideWorkflow} onworkflowtransfer={transfer}
      onworkflowviewed={input => { if (canDecide) viewedMutation.mutate(input) }} />
  {:else if item.council}
    {#if council.isError}<p role="alert" class="text-sm text-destructive">{L('会审详情加载失败', 'Could not load council details')}</p>{/if}
    <CouncilWorkspace detailOnly items={[item.council]} selectedId={councilID} council={council.data} loading={council.isPending}
      busy={!canDecide || contributionMutation.isPending || voteMutation.isPending} viewerActorIds={actors}
      oncontribute={body => contributionMutation.mutate({ councilId: councilID, body }, { onSuccess: () => { message = L('意见已提交', 'Contribution submitted'); onprocessed?.() }, onError: fail })}
      onvote={body => voteMutation.mutate({ councilId: councilID, body }, { onSuccess: () => { message = L('投票已提交', 'Vote submitted'); onprocessed?.() }, onError: fail })} />
  {/if}
  <ApprovalDecisionDialog open={Boolean(action && actionItem)} item={actionItem} {action} {busy} onclose={() => { if (!busy) { action = undefined; actionItem = undefined } }} onsubmit={decide} />
</section>
