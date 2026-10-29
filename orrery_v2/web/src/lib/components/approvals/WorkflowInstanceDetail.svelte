<script lang="ts">
  import { AlertTriangle, Check, Clock, RefreshCw, Send, UserRoundCog, X } from 'lucide-svelte'
  import ApprovalMaterial from '#lib/components/approvals/ApprovalMaterial.svelte'
  import ApprovalReviewShell from '#lib/components/approvals/ApprovalReviewShell.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import { locale } from '#lib/i18n'
  import type { HILWorkflowDecision, HILWorkflowInstanceViewModel } from '#lib/types/view-models'
  import { approvalActorDisplayName, approvalTitleLabel, workflowRevisionLabel } from './approvalDisplayLabels'
  import { workflowStageLabel, workflowStatusLabel } from './workflowLabels'

  interface Props {
    item?: HILWorkflowInstanceViewModel
    busy?: boolean
    canManage?: boolean
    ondecision?: (input: { taskId: string; decision: HILWorkflowDecision; reason: string; expectedVersion: number }) => void
    ontransfer?: (input: { taskId: string; newAssigneeId: string; reason: string; expectedVersion: number; administrator: boolean }) => void
    onviewed?: (input: { instanceId: string; revision: number }) => void
    class?: string
  }

  type Command = HILWorkflowDecision | 'transfer' | 'reassign' | ''

  let { item, busy = false, canManage = false, ondecision, ontransfer, onviewed, class: className = '' }: Props = $props()
  let command = $state<Command>('')
  let reason = $state('')
  let newAssigneeId = $state('')
  let viewedInstanceId = $state('')
  const needsReason = $derived(command === 'reject' || command === 'request_changes' || command === 'transfer' || command === 'reassign')
  const needsAssignee = $derived(command === 'transfer' || command === 'reassign')
  const submitDisabled = $derived(busy || (needsReason && !reason.trim()) || (needsAssignee && !newAssigneeId.trim()))

  $effect(() => {
    if (!item) return
    if (item.id === viewedInstanceId) return
    viewedInstanceId = item.id
    onviewed?.({ instanceId: item.id, revision: item.revision })
  })

  function text(zh: string, en: string) {
    return $locale === 'zh-CN' ? zh : en
  }

  function actorName(value: string) {
    return approvalActorDisplayName(value, $locale)
  }

  function participantNames(values: string[]) {
    const names = values.map(actorName).filter(Boolean)
    if (names.length === 0) return text('待分配', 'Unassigned')
    return names.join($locale === 'zh-CN' ? '、' : ', ')
  }

  function actorId(value: string) {
    return actorName(value).trim()
  }

  function actorIsHuman(value: string) {
    const kind = value.slice(0, value.indexOf(':')).toLowerCase()
    return kind === 'human' || kind === 'user'
  }

  function stageParticipant(values: string[]) {
    return values[0] ?? ''
  }

  function stageAvatarRing(status: string) {
    const normalized = status.trim().toLowerCase()
    if (['approved', 'completed', 'done'].includes(normalized)) return 'ring-emerald-500/20'
    if (['rejected', 'failed', 'cancelled', 'canceled'].includes(normalized)) return 'ring-rose-500/20'
    if (['active', 'pending', 'running'].includes(normalized)) return 'ring-brand/25'
    return 'ring-border'
  }

  function openCommand(next: Command) {
    command = next
    reason = ''
    newAssigneeId = ''
  }

  function closeCommand() {
    command = ''
    reason = ''
    newAssigneeId = ''
  }

  function submitCommand() {
    const task = item?.activeTask
    if (!item || !task || submitDisabled || !command) return
    if (command === 'transfer' || command === 'reassign') {
      ontransfer?.({
        taskId: task.id,
        newAssigneeId: newAssigneeId.trim(),
        reason: reason.trim(),
        expectedVersion: item.version,
        administrator: command === 'reassign'
      })
    } else {
      ondecision?.({ taskId: task.id, decision: command, reason: reason.trim(), expectedVersion: item.version })
    }
    closeCommand()
  }

  function commandTitle() {
    switch (command) {
      case 'approve': return text('确认批准', 'Confirm approval')
      case 'reject': return text('确认拒绝', 'Confirm rejection')
      case 'request_changes': return text('确认要求修改', 'Confirm changes request')
      case 'transfer': return text('转交任务', 'Transfer task')
      case 'reassign': return text('重新指派', 'Reassign task')
      default: return ''
    }
  }
</script>

{#if item}
  <div data-workflow-instance-detail class="h-full {className}">
    <ApprovalReviewShell
      kind="workflow"
      actionsVisible={Boolean(item.activeTask && (!item.isBusinessApproval || item.materialComplete))}
      class="h-full"
    >
      {#snippet header()}
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0 flex-1">
            <h2 class="break-words text-[16px] font-semibold leading-6 text-foreground">{approvalTitleLabel(item.title, $locale)}</h2>
            <p class="mt-1 text-[11px] text-muted-foreground">{actorName(item.requester)} &middot; {item.createdAt}</p>
          </div>
          <div class="flex items-center gap-2">
            <span class="text-[10.5px] text-muted-foreground">{workflowRevisionLabel(item.revision, $locale)}</span>
            <Badge tone={item.statusTone} class="text-[10.5px]">{workflowStatusLabel(item.status, $locale)}</Badge>
          </div>
        </div>
      {/snippet}

      {#snippet content()}
        <ApprovalMaterial
          material={item.material}
          summary={item.summary}
          businessMaterial={item.businessMaterial}
          evidenceRefs={item.evidenceRefs}
          artifacts={item.artifacts}
          readableOnly
        />

        {#if item.isBusinessApproval && !item.materialComplete}
          <div class="mt-5 flex items-start gap-2 border-y border-amber-200 bg-amber-50 px-3 py-2.5 text-[12px] text-amber-900" role="status">
            <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" />
            <p>{text('材料不完整，请发起人重新提交', 'Material is incomplete. Ask the requester to resubmit.')}</p>
          </div>
        {/if}
      {/snippet}

      {#snippet context()}
        <section data-workflow-flow>
          <h3 class="text-[12.5px] font-medium text-foreground">{text('审批流', 'Approval flow')}</h3>
          <ol class="mt-4">
            {#each item.stages as stage, index (stage.id)}
              {@const participant = stageParticipant(stage.participantLabels)}
              {@const participantLabel = participantNames(stage.participantLabels)}
              <li data-workflow-stage class="grid grid-cols-[32px_minmax(0,1fr)] gap-3">
                <div class="relative flex justify-center">
                  {#if index < item.stages.length - 1}<span data-workflow-stage-connector class="absolute bottom-0 top-8 w-px bg-border" aria-hidden="true"></span>{/if}
                  <WorkforceAvatar
                    profileId={actorId(participant)}
                    name={participantLabel}
                    human={actorIsHuman(participant)}
                    class="h-8 w-8 rounded-full ring-2 {stageAvatarRing(stage.status)}"
                    textClass="text-[9px]"
                  />
                </div>
                <div class="pb-5">
                  <div class="flex items-start justify-between gap-2">
                    <p class="min-w-0 break-words text-[10.5px] text-muted-foreground">{workflowStageLabel(stage.name, $locale)}</p>
                    <Badge tone={stage.statusTone} class="shrink-0 text-[9.5px]">{workflowStatusLabel(stage.status, $locale)}</Badge>
                  </div>
                  <h4 class="mt-0.5 break-words text-[12.5px] font-medium text-foreground">{participantLabel}</h4>
                  <p class="mt-0.5 text-[10px] text-muted-foreground/75">{stage.type === 'council' ? text('合议', 'Council') : text('审批', 'Approval')}</p>
                  {#if stage.deadlineAt}<p class="mt-1 flex items-center gap-1 text-[10px] text-muted-foreground"><Clock class="h-3 w-3" />{stage.deadlineAt}</p>{/if}
                </div>
              </li>
            {/each}
          </ol>
        </section>
      {/snippet}

      {#snippet actions()}
        <div class="flex items-center justify-between gap-3">
          <div>
            <h3 class="text-[12.5px] font-medium text-foreground">{text('审批操作', 'Approval actions')}</h3>
            <p class="mt-0.5 text-[10.5px] text-muted-foreground">{item.activeTask ? actorName(item.activeTask.assignee) : ''}</p>
          </div>
          <span class="text-[10px] text-muted-foreground">{workflowStatusLabel(item.activeTask?.status ?? item.status, $locale)}</span>
        </div>
        <div class="mt-3 grid grid-cols-2 gap-2">
          <Button class="h-9 rounded-md px-2 !text-[11.5px]" disabled={busy} onclick={() => openCommand('approve')}><Check class="h-3.5 w-3.5" />{text('批准', 'Approve')}</Button>
          <Button variant="outline" class="h-9 rounded-md px-2 !border-rose-200 !text-[11.5px] !text-rose-700" disabled={busy} onclick={() => openCommand('reject')}><X class="h-3.5 w-3.5" />{text('拒绝', 'Reject')}</Button>
          <Button variant="outline" class="h-9 rounded-md px-2 !text-[11.5px]" disabled={busy} onclick={() => openCommand('request_changes')}><RefreshCw class="h-3.5 w-3.5" />{text('要求修改', 'Request changes')}</Button>
          <Button variant="outline" class="h-9 rounded-md px-2 !text-[11.5px]" disabled={busy} onclick={() => openCommand('transfer')}><Send class="h-3.5 w-3.5" />{text('转交', 'Transfer')}</Button>
          {#if canManage}
            <Button variant="outline" class="col-span-2 h-9 rounded-md px-2 !text-[11.5px]" disabled={busy} onclick={() => openCommand('reassign')}><UserRoundCog class="h-3.5 w-3.5" />{text('重新指派', 'Reassign')}</Button>
          {/if}
        </div>
      {/snippet}
    </ApprovalReviewShell>
  </div>
{:else}
  <div class="flex min-h-64 items-center justify-center rounded-md border border-border text-[12px] text-muted-foreground">{text('选择审批流查看详情', 'Select a workflow')}</div>
{/if}

<Dialog open={Boolean(command)} title={commandTitle()} onClose={closeCommand}>
  <div class="space-y-3">
    {#if needsAssignee}
      <label class="block space-y-1 text-xs text-muted-foreground">
        <span>{text('新审批人', 'New assignee')}</span>
        <input bind:value={newAssigneeId} aria-label={text('新审批人', 'New assignee')} placeholder={text('请输入人员账号', 'Enter an assignee ID')} class="h-9 w-full rounded-md border border-input bg-background px-2 text-sm text-foreground" />
      </label>
    {/if}
    {#if needsReason || command === 'approve'}
      <label class="block space-y-1 text-xs text-muted-foreground">
        <span>{text('理由', 'Reason')}</span>
        <textarea bind:value={reason} aria-label={text('理由', 'Reason')} rows="3" class="w-full resize-y rounded-md border border-input bg-background px-2.5 py-2 text-sm text-foreground"></textarea>
      </label>
    {/if}
    <div class="flex justify-end gap-2">
      <Button variant="outline" onclick={closeCommand}>{text('取消', 'Cancel')}</Button>
      <Button disabled={submitDisabled} onclick={submitCommand}>{commandTitle()}</Button>
    </div>
  </div>
</Dialog>
