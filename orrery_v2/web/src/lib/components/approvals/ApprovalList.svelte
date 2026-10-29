<script lang="ts">
  import { ChevronRight, FileDiff, FileText, HelpCircle, Network, ShieldAlert, SquareTerminal, Wrench } from 'lucide-svelte'
  import ApprovalReviewDetail from '#lib/components/approvals/ApprovalReviewDetail.svelte'
  import ApprovalWorkspaceLayout from '#lib/components/approvals/ApprovalWorkspaceLayout.svelte'
  import WorkflowInstanceDetail from '#lib/components/approvals/WorkflowInstanceDetail.svelte'
  import {
    approvalActorDisplayName,
    approvalStatusDisplayLabel,
    approvalTitleLabel,
    approvalTypeDisplayLabel
  } from '#lib/components/approvals/approvalDisplayLabels'
  import Badge from '#lib/components/ui/Badge.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type {
    ApprovalActionViewModel,
    ApprovalItemViewModel,
    HILWorkflowDecision,
    HILWorkflowInstanceViewModel
  } from '#lib/types/view-models'

  interface Props {
    items: ApprovalItemViewModel[]
    detailOnly?: boolean
    selectedId?: string
    autoSelectFirst?: boolean
    workflowByDecisionId?: Record<string, HILWorkflowInstanceViewModel>
    workflowLoaded?: boolean
    workflowError?: string
    workflowBusy?: boolean
    canManage?: boolean
    onaction?: (item: ApprovalItemViewModel, action: ApprovalActionViewModel) => void
    onworkflowdecision?: (input: { taskId: string; decision: HILWorkflowDecision; reason: string; expectedVersion: number }) => void
    onworkflowtransfer?: (input: { taskId: string; newAssigneeId: string; reason: string; expectedVersion: number; administrator: boolean }) => void
    onworkflowviewed?: (input: { instanceId: string; revision: number }) => void
    class?: string
  }

  let {
    items,
    detailOnly = false,
    selectedId = $bindable(''),
    autoSelectFirst = true,
    workflowByDecisionId = {},
    workflowLoaded = true,
    workflowError = '',
    workflowBusy = false,
    canManage = false,
    onaction,
    onworkflowdecision,
    onworkflowtransfer,
    onworkflowviewed,
    class: className = ''
  }: Props = $props()
  let t = $derived(createTranslator($locale))
  const selectedItem = $derived(items.find((item) => item.id === selectedId))
  const selectedWorkflow = $derived(workflowByDecisionId[selectedId])
  const activeItem = $derived(selectedItem ?? (selectedWorkflow || !autoSelectFirst ? undefined : items[0]))
  const activeWorkflow = $derived(selectedWorkflow ?? (activeItem ? workflowByDecisionId[activeItem.id] : undefined))

  $effect(() => {
    const selectionExists = items.some((item) => item.id === selectedId) || Boolean(workflowByDecisionId[selectedId])
    const nextId = selectionExists ? selectedId : (autoSelectFirst ? (items[0]?.id ?? '') : '')
    if (selectedId !== nextId) selectedId = nextId
  })

  function displayField(value: string) {
    return value === 'Not provided' || value === '' ? t('workspace.notProvided') : value
  }

  function isPending(status: string) {
    return ['', 'pending', 'open'].includes(status.trim().toLowerCase())
  }

  function statusLabel(status: string) {
    return approvalStatusDisplayLabel(status, $locale)
  }

  function approvalRequester(item: ApprovalItemViewModel) {
    if (item.requestedByName) return localizedActor(item.requestedByName)
    if (isHumanReadableDecision(item)) return $locale === 'zh-CN' ? '审批发起人' : 'Approval requester'
    return localizedActor(item.requestedBy)
  }

  function isHumanReadableDecision(item: ApprovalItemViewModel) {
    return item.displayKind === 'hil_decision' || item.displayKind === 'approval_decision'
  }

  function isBusinessApproval(item: ApprovalItemViewModel) {
    return item.payloadPreview.kind === 'business_approval'
      || item.toolName === 'request_business_approval'
      || item.payloadPreview.fields.some((field) => field.key === 'approval_request_kind' && field.value === 'business')
  }

  function localizedTitle(title: string) {
    return approvalTitleLabel(title, $locale)
  }

  function localizedType(typeLabel: string) {
    return approvalTypeDisplayLabel(typeLabel, $locale)
  }

  function localizedActor(actor: string) {
    return displayField(approvalActorDisplayName(actor, $locale))
  }

  function createdAtLabel(item: ApprovalItemViewModel) {
    if (!item.createdAtRaw) return displayField(item.createdAt)
    const value = new Date(item.createdAtRaw)
    if (Number.isNaN(value.getTime())) return displayField(item.createdAt)
    const now = new Date()
    const sameDay = value.getFullYear() === now.getFullYear()
      && value.getMonth() === now.getMonth()
      && value.getDate() === now.getDate()
    const time = new Intl.DateTimeFormat($locale, { hour: '2-digit', minute: '2-digit', hour12: false }).format(value)
    if (sameDay) return $locale === 'zh-CN' ? `今天 ${time}` : `Today ${time}`
    return new Intl.DateTimeFormat($locale, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }).format(value)
  }
</script>

<ApprovalWorkspaceLayout {detailOnly} class={className}>
  {#snippet queue()}
    {#if items.length === 0}
      <div class="flex h-full min-h-64 flex-col items-center justify-center gap-2 px-6 py-16 text-center text-muted-foreground">
        <ShieldAlert class="h-6 w-6 text-status-done" />
        <p class="text-[13px] font-medium text-foreground">{t('approvals.empty')}</p>
        <p class="max-w-sm text-xs leading-5">{t('approvals.empty.description')}</p>
      </div>
    {:else}
      <div class="subtle-scrollbar h-full divide-y divide-border/60 overflow-y-auto">
        {#each items as item (item.id)}
          <button
            type="button"
            aria-pressed={activeItem?.id === item.id}
            class="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors {activeItem?.id === item.id ? 'selection-wash' : 'hover:bg-muted/35'}"
            onclick={() => (selectedId = item.id)}
          >
            <span class="grid h-9 w-9 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground">
              {#if item.displayKind === 'command_execution'}
                <SquareTerminal class="h-4 w-4" />
              {:else if item.displayKind === 'file_change'}
                <FileDiff class="h-4 w-4" />
              {:else if item.displayKind === 'user_input'}
                <HelpCircle class="h-4 w-4" />
              {:else if item.displayKind === 'network_access'}
                <Network class="h-4 w-4" />
              {:else if item.displayKind === 'tool_use'}
                <Wrench class="h-4 w-4" />
              {:else if item.displayKind === 'plan_approval'}
                <FileText class="h-4 w-4" />
              {:else}
                <ShieldAlert class="h-4 w-4" />
              {/if}
            </span>
            <span class="min-w-0 flex-1">
              <span class="block truncate text-[13.5px] text-foreground">{localizedTitle(item.title)}</span>
              <span class="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11.5px] text-muted-foreground">
                <span class="shrink-0">{localizedType(item.typeLabel)}</span>
                <span aria-hidden="true">&middot;</span>
                <span class="truncate">{approvalRequester(item)}</span>
                <span aria-hidden="true">&middot;</span>
                <span class="shrink-0">{createdAtLabel(item)}</span>
              </span>
            </span>
            {#if isPending(item.status)}
              <ChevronRight class="h-4 w-4 shrink-0 text-muted-foreground/45" />
            {:else}
              <Badge tone={item.statusTone} class="shrink-0 text-[10.5px]">{statusLabel(item.status)}</Badge>
            {/if}
          </button>
        {/each}
      </div>
    {/if}
  {/snippet}

  {#snippet detail()}
    {#if activeWorkflow && !workflowError}
      <WorkflowInstanceDetail
        item={activeWorkflow}
        busy={workflowBusy}
        {canManage}
        class="h-full"
        ondecision={onworkflowdecision}
        ontransfer={onworkflowtransfer}
        onviewed={onworkflowviewed}
      />
    {:else if activeItem && isBusinessApproval(activeItem)}
      <div class="flex h-full min-h-64 items-center justify-center rounded-md border border-border bg-card p-6 text-center">
        {#if !workflowLoaded}
          <p role="status" class="text-[13px] text-muted-foreground">正在加载审批流...</p>
        {:else}
          <div role="alert" class="max-w-xs border-y border-amber-200 bg-amber-50 px-4 py-3 text-left text-[12px] text-amber-900">
            <p class="font-medium">审批流不可用，暂时不能处理该审批</p>
            {#if workflowError}<p class="mt-1 text-[11px] text-amber-800/80">{workflowError}</p>{/if}
          </div>
        {/if}
      </div>
    {:else if activeItem}
      <ApprovalReviewDetail item={activeItem} {onaction} class="h-full" />
    {:else}
      <div class="flex h-full min-h-64 items-center justify-center rounded-md border border-border bg-card px-6 text-center text-[13px] text-muted-foreground">
        {t('approvals.selectItem')}
      </div>
    {/if}
  {/snippet}
</ApprovalWorkspaceLayout>
