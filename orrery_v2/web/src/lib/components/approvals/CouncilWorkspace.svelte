<script lang="ts">
  import { Check, Clock, ShieldAlert, Users, X } from 'lucide-svelte'
  import ApprovalMaterial from '#lib/components/approvals/ApprovalMaterial.svelte'
  import ApprovalReviewShell from '#lib/components/approvals/ApprovalReviewShell.svelte'
  import ApprovalWorkspaceLayout from '#lib/components/approvals/ApprovalWorkspaceLayout.svelte'
  import {
    approvalActorDisplayName,
    approvalOperationLabel,
    approvalRiskDisplayLabel,
    approvalStatusDisplayLabel,
    councilPositionDisplayLabel,
    councilVoteDisplayLabel,
    participantRoleDisplayLabel
  } from '#lib/components/approvals/approvalDisplayLabels'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { mapHILApprovalMaterial } from '#lib/mappers/hil'
  import type { HILCouncilViewModel, HILDecisionItemViewModel } from '#lib/types/view-models'

  interface Props {
    items: HILDecisionItemViewModel[]
    detailOnly?: boolean
    selectedId?: string
    council?: HILCouncilViewModel
    loading?: boolean
    busy?: boolean
    viewerActorIds?: string[]
    onselect?: (councilId: string) => void
    oncontribute?: (input: { position: string; reasoning: string; evidence_refs?: string[] }) => void
    onvote?: (input: { choice: 'approve' | 'reject' | 'abstain'; reason?: string }) => void
    class?: string
  }

  let {
    items,
    detailOnly = false,
    selectedId = '',
    council,
    loading = false,
    busy = false,
    viewerActorIds = [],
    onselect,
    oncontribute,
    onvote,
    class: className = ''
  }: Props = $props()
  let position = $state('approve')
  let reasoning = $state('')
  let voteReason = $state('')
  let t = $derived(createTranslator($locale))
  const activeItem = $derived(items.find((item) => item.councilId === selectedId) ?? items[0])
  const activeMaterial = $derived(mapHILApprovalMaterial(activeItem?.payloadPreview ?? {}))
  const viewerActors = $derived(new Set(viewerActorIds.map((value) => value.trim()).filter(Boolean)))
  const currentParticipant = $derived(council?.participants.find((participant) =>
    viewerActors.has(participant.actorId) || viewerActors.has(participant.actorKey)
  ))
  const voteDisabled = $derived(busy || !currentParticipant?.contributed || Boolean(currentParticipant.vote))

  function text(zh: string, en: string) {
    return $locale === 'zh-CN' ? zh : en
  }

  function actorName(value: string) {
    return approvalActorDisplayName(value, $locale)
  }

  function submitContribution() {
    const value = reasoning.trim()
    if (!value) return
    oncontribute?.({ position, reasoning: value })
  }

  function submitVote(choice: 'approve' | 'reject' | 'abstain') {
    onvote?.({ choice, reason: voteReason.trim() || undefined })
  }

  function percent(value: number) {
    return `${Math.round(Math.max(0, Math.min(1, value)) * 100)}%`
  }
</script>

<ApprovalWorkspaceLayout kind="council" {detailOnly} class={className}>
  {#snippet queue()}
    {#if items.length === 0}
      <div class="flex h-full min-h-64 flex-col items-center justify-center gap-2 px-6 py-16 text-center text-muted-foreground">
        <Users class="h-6 w-6 text-status-done" />
        <p class="text-[13px] font-medium text-foreground">{t('approvals.council.empty')}</p>
        <p class="max-w-sm text-xs leading-5">{t('approvals.council.empty.description')}</p>
      </div>
    {:else}
      <div class="subtle-scrollbar h-full divide-y divide-border/60 overflow-y-auto">
        {#each items as item (item.id)}
          <button
            type="button"
            aria-pressed={activeItem?.id === item.id}
            class="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors {activeItem?.id === item.id ? 'selection-wash' : 'hover:bg-muted/35'}"
            onclick={() => onselect?.(item.councilId)}
          >
            <span class="grid h-9 w-9 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground">
              <Users class="h-4 w-4" />
            </span>
            <span class="min-w-0 flex-1">
              <span class="block truncate text-[13.5px] text-foreground">{approvalOperationLabel(item.action, $locale)}</span>
              <span class="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11.5px] text-muted-foreground">
                <span class="truncate">{actorName(item.requester)}</span>
                <span aria-hidden="true">&middot;</span>
                <span class="shrink-0">{item.createdAt}</span>
              </span>
            </span>
            <Badge tone={item.riskTone} class="shrink-0 text-[10.5px]">{approvalRiskDisplayLabel(item.riskLevel, $locale)}</Badge>
          </button>
        {/each}
      </div>
    {/if}
  {/snippet}

  {#snippet detail()}
    {#if !activeItem}
      <div class="flex h-full min-h-64 items-center justify-center rounded-md border border-border bg-card px-6 text-center text-[13px] text-muted-foreground">
        {t('approvals.selectItem')}
      </div>
    {:else if loading || !council}
      <div class="flex h-full min-h-64 items-center justify-center rounded-md border border-border bg-card text-[13px] text-muted-foreground">{t('state.loadingApprovals')}</div>
    {:else}
      <ApprovalReviewShell kind="council" actionsVisible={council.status === 'open'} class="h-full">
        {#snippet header()}
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0 flex-1">
              <div class="mb-1.5 flex items-center gap-2 text-[10.5px] text-muted-foreground">
                <Clock class="h-3.5 w-3.5" />
                <span>{activeItem.createdAt}</span>
                <span aria-hidden="true">&middot;</span>
                <span>{actorName(activeItem.requester)}</span>
              </div>
              <h2 class="break-words text-[16px] font-semibold leading-6 text-foreground">{approvalOperationLabel(activeItem.action, $locale)}</h2>
            </div>
            <Badge tone={council.statusTone} class="shrink-0 text-[10.5px]">{approvalStatusDisplayLabel(council.status, $locale)}</Badge>
          </div>
        {/snippet}

        {#snippet content()}
          {#if activeItem.purpose}
            <p data-council-purpose class="mb-5 border-l-2 border-brand/50 pl-4 text-[13.5px] leading-6 text-foreground/85">{activeItem.purpose}</p>
          {/if}

          <ApprovalMaterial material={activeMaterial} readableOnly />

          {#if council.contributions.length > 0}
            <section class="mt-7 border-t border-border/70 pt-5">
              <h3 class="text-[12.5px] font-medium text-foreground">{t('approvals.council.opinions')}</h3>
              <div class="mt-3 space-y-4">
                {#each council.contributions as contribution (contribution.actorKey)}
                  <div class="border-l-2 border-border pl-3">
                    <div class="flex items-center justify-between gap-2 text-[11px]">
                      <span class="font-medium text-foreground">{contribution.actorName}</span>
                      <span class="text-muted-foreground">{councilPositionDisplayLabel(contribution.position, $locale)}</span>
                    </div>
                    <p class="mt-1 text-[12.5px] leading-5 text-foreground/80">{contribution.reasoning}</p>
                  </div>
                {/each}
              </div>
            </section>
          {/if}

          {#if council.status === 'open' && !currentParticipant?.contributed}
            <section data-council-contribution class="mt-7 border-t border-border/70 pt-5">
              <h3 class="text-[12.5px] font-medium text-foreground">{t('approvals.council.yourOpinion')}</h3>
              <div class="mt-3 grid gap-3 sm:grid-cols-[8rem_minmax(0,1fr)]">
                <select bind:value={position} aria-label={t('approvals.council.yourOpinion')} class="h-9 rounded-md border border-input bg-background px-2 text-[12px]">
                  <option value="approve">{t('approvals.council.approve')}</option>
                  <option value="reject">{t('approvals.council.reject')}</option>
                  <option value="abstain">{t('approvals.council.abstain')}</option>
                </select>
                <textarea bind:value={reasoning} rows="3" class="w-full resize-y rounded-md border border-input bg-background px-2.5 py-2 text-[12px] leading-5" placeholder={t('approvals.council.reasoningPlaceholder')}></textarea>
              </div>
              <Button class="mt-3 h-9 rounded-md px-3 !text-[12px]" disabled={busy || !reasoning.trim()} onclick={submitContribution}>{t('approvals.council.submitOpinion')}</Button>
            </section>
          {/if}
        {/snippet}

        {#snippet context()}
          <section data-council-quorum>
            <div class="flex items-center justify-between gap-3">
              <h3 class="text-[12.5px] font-medium text-foreground">{text('合议进度', 'Council progress')}</h3>
              <span class="text-[11px] text-muted-foreground">{council.approvals + council.rejections}/{council.quorum}</span>
            </div>
            <div class="mt-3 h-1.5 overflow-hidden rounded-full bg-muted">
              <div class="h-full bg-brand" style:width={percent((council.approvals + council.rejections) / council.quorum)}></div>
            </div>
            <div class="mt-3 grid grid-cols-3 gap-1.5 text-center text-[10.5px]">
              <span class="rounded bg-emerald-500/8 px-1.5 py-1 text-emerald-700">{t('approvals.council.approve')} {council.approvals}</span>
              <span class="rounded bg-rose-500/8 px-1.5 py-1 text-rose-700">{t('approvals.council.reject')} {council.rejections}</span>
              <span class="rounded bg-muted px-1.5 py-1 text-muted-foreground">{t('approvals.council.abstain')} {council.abstentions}</span>
            </div>
          </section>

          <section data-council-participants class="mt-5 border-t border-border/70 pt-4">
            <h3 class="text-[12.5px] font-medium text-foreground">{t('approvals.council.participants')}</h3>
            <div class="mt-3 space-y-3">
              {#each council.participants as participant (participant.actorKey)}
                <div class="flex items-center gap-2.5">
                  <WorkforceAvatar
                    profileId={participant.actorId}
                    name={participant.displayName}
                    human={participant.actorKind !== 'agent'}
                    class="h-8 w-8 rounded-full"
                    textClass="text-[9px]"
                  />
                  <span class="min-w-0 flex-1">
                    <span class="block truncate text-[12px] font-medium text-foreground">{participant.displayName}</span>
                    <span class="block truncate text-[10.5px] text-muted-foreground">{participantRoleDisplayLabel(participant.role, $locale)}</span>
                  </span>
                  <span class="shrink-0 text-[10px] text-muted-foreground">{participant.vote ? councilVoteDisplayLabel(participant.vote, $locale) : participant.contributed ? t('approvals.council.contributed') : t('approvals.council.waiting')}</span>
                </div>
              {/each}
            </div>
          </section>

          <section class="mt-5 border-t border-border/70 pt-4">
            <dl class="space-y-2.5 text-[11.5px]">
              <div class="flex items-start justify-between gap-3">
                <dt class="text-muted-foreground">{text('风险', 'Risk')}</dt>
                <dd class="text-right text-foreground">{approvalRiskDisplayLabel(activeItem.riskLevel, $locale)}</dd>
              </div>
              <div class="flex items-start justify-between gap-3">
                <dt class="text-muted-foreground">{text('材料状态', 'Material')}</dt>
                <dd class="text-right text-foreground">{activeItem.contextComplete ? t('approvals.council.complete') : t('approvals.council.partial')}</dd>
              </div>
            </dl>
          </section>

          {#if council.effect === 'requires_approval'}
            <div class="mt-5 flex items-start gap-2 border-y border-amber-200 bg-amber-50 px-3 py-2.5 text-[11.5px] leading-5 text-amber-900">
              <ShieldAlert class="mt-0.5 h-3.5 w-3.5 shrink-0" />{t('approvals.council.requiresApproval')}
            </div>
          {/if}
        {/snippet}

        {#snippet actions()}
          <h3 class="text-[12.5px] font-medium text-foreground">{t('approvals.council.finalVote')}</h3>
          <input bind:value={voteReason} class="mt-2 h-9 w-full rounded-md border border-input bg-background px-2 text-[12px]" placeholder={t('approvals.council.voteReasonPlaceholder')} />
          <div class="mt-2 grid grid-cols-3 gap-1.5">
            <Button class="h-9 rounded-md px-1.5 !text-[11px]" disabled={voteDisabled} onclick={() => submitVote('approve')}><Check class="h-3.5 w-3.5" />{t('approvals.council.approve')}</Button>
            <Button variant="outline" class="h-9 rounded-md px-1.5 !border-rose-200 !text-[11px] !text-rose-700" disabled={voteDisabled} onclick={() => submitVote('reject')}><X class="h-3.5 w-3.5" />{t('approvals.council.reject')}</Button>
            <Button variant="outline" class="h-9 rounded-md px-1.5 !text-[11px]" disabled={voteDisabled} onclick={() => submitVote('abstain')}>{t('approvals.council.abstain')}</Button>
          </div>
        {/snippet}
      </ApprovalReviewShell>
    {/if}
  {/snippet}
</ApprovalWorkspaceLayout>
