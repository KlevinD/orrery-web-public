<script lang="ts">
  import { Check, ShieldAlert, UserRoundCheck, X } from 'lucide-svelte'
  import ApprovalMaterial from '#lib/components/approvals/ApprovalMaterial.svelte'
  import ApprovalReviewShell from '#lib/components/approvals/ApprovalReviewShell.svelte'
  import { approvalActionLabel } from '#lib/components/approvals/approvalActionLabels'
  import {
    approvalActorDisplayName,
    approvalRiskDisplayLabel,
    approvalStatusDisplayLabel,
    approvalTitleLabel,
    approvalTypeDisplayLabel
  } from '#lib/components/approvals/approvalDisplayLabels'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { ApprovalActionViewModel, ApprovalItemViewModel } from '#lib/types/view-models'

  interface Props {
    item: ApprovalItemViewModel
    onaction?: (item: ApprovalItemViewModel, action: ApprovalActionViewModel) => void
    class?: string
  }

  let { item, onaction, class: className = '' }: Props = $props()
  let t = $derived(createTranslator($locale))
  const reviewKind = $derived(
    item.type === 'hil' || item.displayKind === 'hil_decision'
      ? 'hil'
      : item.type === 'approval' || item.displayKind === 'approval_decision'
        ? 'fixed'
        : 'runtime'
  )
  const hasTrigger = $derived(Boolean(item.triggeredBy))
  const fixedFlowNodes = $derived([
    {
      key: 'requester',
      role: t(hasTrigger ? 'approvals.flow.businessInitiator' : 'approvals.flow.requester'),
      profileId: item.requestedBy,
      name: approvalRequester(item),
      human: hasTrigger || actorIsHuman(item.requestedBy),
      status: '',
      ring: 'ring-emerald-500/15'
    },
    ...(hasTrigger ? [{
      key: 'trigger',
      role: t('approvals.flow.triggeredBy'),
      profileId: item.triggeredBy || '',
      name: actorName(item.triggeredByName || item.triggeredBy || ''),
      human: false,
      status: '',
      ring: 'ring-brand/20'
    }] : []),
    {
      key: 'approver',
      role: t('approvals.flow.approver'),
      profileId: approvalAssigneeId(item),
      name: approvalAssignee(item),
      human: true,
      status: statusLabel(item.status),
      ring: approverRingClass(item.status)
    }
  ])
  const hilFlowNodes = $derived([
    {
      key: 'requester',
      role: text('发起请求', 'Request submitted'),
      profileId: item.requestedBy,
      name: approvalRequester(item),
      human: actorIsHuman(item.requestedBy),
      status: createdAtLabel(item),
      ring: 'ring-brand/15'
    },
    {
      key: 'reviewer',
      role: text('人工处理', 'Human review'),
      profileId: approvalAssigneeId(item),
      name: approvalAssignee(item),
      human: true,
      status: statusLabel(item.status),
      ring: approverRingClass(item.status)
    }
  ])

  function text(zh: string, en: string) {
    return $locale === 'zh-CN' ? zh : en
  }

  function displayField(value: string) {
    return value === 'Not provided' || value === '' ? t('workspace.notProvided') : value
  }

  function actorName(value: string) {
    return displayField(approvalActorDisplayName(value, $locale))
  }

  function actorIsHuman(value: string) {
    const kind = value.split(':', 1)[0]?.trim().toLowerCase()
    return kind === 'human' || kind === 'user'
  }

  function isHumanReadableDecision(value: ApprovalItemViewModel) {
    return value.displayKind === 'hil_decision' || value.displayKind === 'approval_decision'
  }

  function approvalRequester(value: ApprovalItemViewModel) {
    if (value.requestedByName) return actorName(value.requestedByName)
    if (isHumanReadableDecision(value)) return text('审批发起人', 'Approval requester')
    return actorName(value.requestedBy)
  }

  function approvalAssigneeId(value: ApprovalItemViewModel) {
    const actorId = value.decidedBy || value.assignedTo
    return actorId === 'Not provided' ? '' : actorId
  }

  function approvalAssignee(value: ApprovalItemViewModel) {
    if (value.decidedBy) {
      if (value.decidedByName) return actorName(value.decidedByName)
      if (isHumanReadableDecision(value)) {
        return value.assignedToName ? actorName(value.assignedToName) : text('人工审批人', 'Human reviewer')
      }
      return actorName(value.decidedBy)
    }
    if (!value.assignedTo || value.assignedTo === 'Not provided') return t('approvals.flow.unassigned')
    if (value.assignedToName) return actorName(value.assignedToName)
    return isHumanReadableDecision(value) ? text('人工审批人', 'Human reviewer') : actorName(value.assignedTo)
  }

  function localizedTitle(value: string) {
    return approvalTitleLabel(value, $locale)
  }

  function localizedType(value: string) {
    return approvalTypeDisplayLabel(value, $locale)
  }

  function statusLabel(status: string) {
    return approvalStatusDisplayLabel(status, $locale)
  }

  function isPending(status: string) {
    return ['', 'pending', 'open'].includes(status.trim().toLowerCase())
  }

  function isApproved(status: string) {
    return status.trim().toLowerCase() === 'approved'
  }

  function approverRingClass(status: string) {
    if (isPending(status)) return 'ring-brand/20'
    if (isApproved(status)) return 'ring-emerald-500/20'
    if (status.trim().toLowerCase() === 'unavailable') return 'ring-muted-foreground/15'
    return 'ring-rose-500/20'
  }

  function createdAtLabel(value: ApprovalItemViewModel) {
    if (!value.createdAtRaw) return displayField(value.createdAt)
    const date = new Date(value.createdAtRaw)
    if (Number.isNaN(date.getTime())) return displayField(value.createdAt)
    const now = new Date()
    const sameDay = date.getFullYear() === now.getFullYear()
      && date.getMonth() === now.getMonth()
      && date.getDate() === now.getDate()
    const time = new Intl.DateTimeFormat($locale, { hour: '2-digit', minute: '2-digit', hour12: false }).format(date)
    if (sameDay) return text(`今天 ${time}`, `Today ${time}`)
    return new Intl.DateTimeFormat($locale, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }).format(date)
  }

  function riskLabel(value: string) {
    return approvalRiskDisplayLabel(value, $locale)
  }

  function interventionReason() {
    const risk = item.riskLevel.trim().toLowerCase()
    if (risk === 'high' || risk === 'critical') {
      return text('该操作风险较高，自动执行已暂停，需要人工确认后才能继续。', 'This action has elevated risk. Automation is paused until a person confirms it.')
    }
    return text('自动执行需要人工判断，确认后才会继续。', 'Automation needs human judgment before it can continue.')
  }

  function visibleDetails() {
    return item.sourceLabel.startsWith('hil:') ? '' : item.details
  }

  function visibleRiskSummary() {
    if (!item.riskSummary) return ''
    if (item.sourceLabel.startsWith('hil:')) {
      return text('该操作符合审批规则，需要人工确认后才能继续。', 'This action matches an approval rule and needs human confirmation before it can continue.')
    }
    return item.riskSummary
  }

  function actionVariant(action: ApprovalActionViewModel): 'default' | 'outline' {
    return action.style === 'primary' ? 'default' : 'outline'
  }
</script>

<ApprovalReviewShell actionsVisible={item.actions.length > 0} class={className}>
  {#snippet header()}
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0 flex-1">
        <div class="mb-1.5 flex flex-wrap items-center gap-2">
          <Badge tone={item.displayTone} class="text-[10px]">{localizedType(item.typeLabel)}</Badge>
          <span class="text-[10.5px] text-muted-foreground">{createdAtLabel(item)}</span>
        </div>
        <h2 class="break-words text-[16px] font-semibold leading-6 text-foreground">{localizedTitle(item.title)}</h2>
        <p class="mt-1 text-[11px] text-muted-foreground">{approvalRequester(item)}</p>
      </div>
      <Badge tone={item.statusTone} class="shrink-0 text-[10.5px]">{statusLabel(item.status)}</Badge>
    </div>
  {/snippet}

  {#snippet content()}
    <ApprovalMaterial
      material={item.payloadPreview}
      summary={item.summary}
      evidenceRefs={item.evidenceRefs}
      readableOnly
    />
    {#if !isHumanReadableDecision(item) && visibleDetails() && visibleDetails() !== item.summary}
      <p data-approval-details class="mt-3 whitespace-pre-wrap break-words text-[11.5px] leading-5 text-muted-foreground">
        {visibleDetails()}
      </p>
    {/if}
    {#if !isHumanReadableDecision(item) && visibleRiskSummary()}
      <p data-approval-review-provenance class="mt-3 whitespace-pre-wrap break-words border-l-2 border-amber-500/50 bg-amber-500/5 px-2.5 py-2 text-[11.5px] leading-5 text-amber-800 dark:text-amber-300">
        {visibleRiskSummary()}
      </p>
    {/if}
  {/snippet}

  {#snippet context()}
    {#if reviewKind === 'hil'}
      <section data-hil-context>
        <div class="flex items-center gap-2">
          <ShieldAlert class="h-4 w-4 text-amber-600" />
          <h3 class="text-[12.5px] font-medium text-foreground">{text('人工介入', 'Human review')}</h3>
        </div>
        <p data-hil-intervention-reason class="mt-3 border-l-2 border-amber-400 pl-3 text-[12px] leading-5 text-foreground/80">{interventionReason()}</p>

        <ol data-hil-intervention-flow class="mt-5">
          {#each hilFlowNodes as node, index (node.key)}
            <li class="grid grid-cols-[32px_minmax(0,1fr)] gap-3">
              <div class="relative flex justify-center">
                {#if index < hilFlowNodes.length - 1}<span class="absolute bottom-0 top-8 w-px bg-border" aria-hidden="true"></span>{/if}
                <WorkforceAvatar
                  profileId={node.profileId}
                  name={node.name}
                  human={node.human}
                  class="h-8 w-8 rounded-full ring-2 {node.ring}"
                  textClass="text-[9px]"
                />
              </div>
              <div class="pb-5">
                <div class="flex items-start justify-between gap-2">
                  <p class="text-[10.5px] text-muted-foreground">{node.role}</p>
                  <span class="shrink-0 text-[10px] text-muted-foreground">{node.status}</span>
                </div>
                <h4 class="mt-0.5 break-words text-[12.5px] font-medium text-foreground">{node.name}</h4>
              </div>
            </li>
          {/each}
        </ol>

        <dl class="divide-y divide-border/70 border-y border-border/70 text-[11.5px]">
          <div class="flex items-start justify-between gap-3 py-2.5">
            <dt class="text-muted-foreground">{text('触发类型', 'Trigger')}</dt>
            <dd class="text-right text-foreground">{localizedType(item.typeLabel)}</dd>
          </div>
          <div class="flex items-start justify-between gap-3 py-2.5">
            <dt class="text-muted-foreground">{text('风险', 'Risk')}</dt>
            <dd class="text-right text-foreground">{riskLabel(item.riskLevel)}</dd>
          </div>
        </dl>

      </section>
    {:else}
      <section data-approval-flow>
        <div class="flex items-center gap-2">
          <UserRoundCheck class="h-4 w-4 text-muted-foreground" />
          <h3 class="text-[12.5px] font-medium text-foreground">{reviewKind === 'fixed' ? text('固定审批流', 'Fixed approval flow') : text('执行确认', 'Execution review')}</h3>
        </div>
        <ol class="mt-4">
          {#each fixedFlowNodes as node, index (node.key)}
            <li class="grid grid-cols-[32px_minmax(0,1fr)] gap-3">
              <div class="relative flex justify-center">
                {#if index < fixedFlowNodes.length - 1}<span class="absolute bottom-0 top-8 w-px bg-border" aria-hidden="true"></span>{/if}
                <WorkforceAvatar
                  profileId={node.profileId}
                  name={node.name}
                  human={node.human}
                  class="h-8 w-8 rounded-full ring-2 {node.ring}"
                  textClass="text-[9px]"
                />
              </div>
              <div class="pb-5">
                <div class="flex items-start justify-between gap-2">
                  <p class="text-[10.5px] text-muted-foreground">{node.role}</p>
                  {#if node.status}<span class="shrink-0 text-[10px] text-muted-foreground">{node.status}</span>{/if}
                </div>
                <h4 class="mt-0.5 break-words text-[12.5px] font-medium text-foreground">{node.name}</h4>
                {#if node.key === 'requester'}<p class="mt-0.5 text-[10px] text-muted-foreground/75">{createdAtLabel(item)}</p>{/if}
              </div>
            </li>
          {/each}
        </ol>
      </section>
    {/if}
  {/snippet}

  {#snippet actions()}
    <div class="flex items-center justify-between gap-3">
      <div>
        <h3 class="text-[12.5px] font-medium text-foreground">{text('审批操作', 'Review actions')}</h3>
        <p class="mt-0.5 text-[10px] text-muted-foreground">{approvalAssignee(item)}</p>
      </div>
      <span class="text-[10px] text-muted-foreground">{statusLabel(item.status)}</span>
    </div>
    <div class="mt-3 grid grid-cols-2 gap-2">
      {#each item.actions as action (action.id)}
        <Button
          size="default"
          variant={actionVariant(action)}
          class="h-9 w-full rounded-md px-2 !text-[12px] {action.style === 'danger' ? '!border-rose-200 !bg-transparent !text-rose-600 hover:!bg-rose-50' : ''}"
          onclick={() => onaction?.(item, action)}
        >
          {#if action.style === 'danger'}
            <X class="h-3.5 w-3.5" />
          {:else if action.style !== 'secondary'}
            <Check class="h-3.5 w-3.5" />
          {/if}
          {approvalActionLabel(action, t)}
        </Button>
      {/each}
    </div>
  {/snippet}
</ApprovalReviewShell>
