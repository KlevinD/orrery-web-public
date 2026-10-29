<script lang="ts">
  import { LoaderCircle } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import ApprovalMaterial from '#lib/components/approvals/ApprovalMaterial.svelte'
  import { approvalActionLabel } from '#lib/components/approvals/approvalActionLabels'
  import {
    approvalReferenceLabel,
    approvalRiskDisplayLabel,
    approvalTitleLabel,
    approvalTypeDisplayLabel
  } from '#lib/components/approvals/approvalDisplayLabels'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import { validateApprovalDecisionForm } from '#lib/mappers/approval'
  import type { ApprovalActionViewModel, ApprovalItemViewModel } from '#lib/types/view-models'

  interface Props {
    item?: ApprovalItemViewModel
    action?: ApprovalActionViewModel
    open?: boolean
    busy?: boolean
    onclose?: () => void
    onsubmit?: (form: { message: string; modifiedInput?: string; choiceId?: string; remember?: boolean }) => void
  }

  let { item, action, open = false, busy = false, onclose, onsubmit }: Props = $props()
  let message = $state('')
  let modifiedInput = $state('')
  let choiceId = $state('')
  let remember = $state(false)
  let t = $derived(createTranslator($locale))
  const validation = $derived(action ? validateApprovalDecisionForm(action, { message, modifiedInput, choiceId }) : undefined)
  const showChoices = $derived(action?.inputKind === 'choice_or_text' && (item?.choices.length ?? 0) > 0)
  const showModifiedInput = $derived(action?.inputKind === 'modified_input' || action?.allowsModification)
  const humanReadableDecision = $derived(Boolean(item && isHumanReadableDecision(item)))

  function isHumanReadableDecision(value: ApprovalItemViewModel) {
    return value.displayKind === 'hil_decision' || value.displayKind === 'approval_decision'
  }

  function displayField(value: string) {
    return !value || value === 'Not provided' ? t('workspace.notProvided') : value
  }

  function visibleRiskSummary(value: ApprovalItemViewModel) {
    if (!value.riskSummary) return ''
    if (value.sourceLabel.startsWith('hil:')) {
      return $locale === 'zh-CN'
        ? '该操作符合审批规则，需要人工确认后才能继续。'
        : 'This action matches an approval rule and needs human confirmation before it can continue.'
    }
    return value.riskSummary
  }

  $effect(() => {
    if (open && item && action) {
      message = action.defaultMessage
      modifiedInput = item.payloadPreview.content
      choiceId = ''
      remember = action.rememberAllowed
    }
  })
</script>

<Dialog
  {open}
  title={action ? approvalActionLabel(action, t) : t('approvals.decision')}
  size="wide"
  onClose={() => { if (!busy) onclose?.() }}
>
  {#if item && action}
    <div class="space-y-4">
      <div>
        <div class="flex flex-wrap items-center gap-2">
          <div class="text-sm font-medium text-foreground">{approvalTitleLabel(item.title, $locale)}</div>
          <span class="rounded border border-border px-2 py-0.5 text-[10px] text-muted-foreground">{approvalTypeDisplayLabel(item.typeLabel, $locale)}</span>
          {#if item.riskLevel !== 'Not provided'}
            <span class="rounded border border-amber-500/35 bg-amber-500/10 px-2 py-0.5 text-[10px] text-amber-700">{approvalRiskDisplayLabel(item.riskLevel, $locale)}</span>
          {/if}
        </div>
        {#if !humanReadableDecision}
          <div class="mt-1 text-xs text-muted-foreground">{approvalReferenceLabel(item.id, $locale)}</div>
        {/if}
      </div>
      {#if !humanReadableDecision}
        <div class="rounded-md border border-border bg-muted/25 p-3">
          <p class="whitespace-pre-wrap break-words text-sm leading-6 text-foreground">{displayField(item.summary)}</p>
          {#if visibleRiskSummary(item)}
            <p class="mt-2 rounded border border-amber-500/30 bg-amber-500/10 px-2 py-1 text-xs text-amber-800">{visibleRiskSummary(item)}</p>
          {/if}
        </div>
      {/if}
      <ApprovalMaterial material={item.payloadPreview} summary={humanReadableDecision ? item.summary : ''} readableOnly />
      {#if showChoices}
        <section class="space-y-2">
          <div class="text-xs font-medium text-muted-foreground">{t('approvals.chooseResponse')}</div>
          <div class="grid gap-2">
            {#each item.choices as choice}
              <button
                type="button"
                class={`rounded-md border px-3 py-2 text-left text-sm ${choiceId === choice.id ? 'border-brand bg-brand/10 text-foreground' : 'border-border bg-background text-foreground hover:bg-muted/40'}`}
                onclick={() => choiceId = choice.id}
              >
                <span class="font-medium">{choice.label}</span>
                {#if choice.description}
                  <span class="mt-1 block text-xs text-muted-foreground">{choice.description}</span>
                {/if}
              </button>
            {/each}
          </div>
        </section>
      {/if}
      {#if showModifiedInput}
        <label class="block space-y-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('approvals.modifiedInput')}</span>
          <textarea
            bind:value={modifiedInput}
            class="min-h-32 w-full rounded-md border border-input bg-input-background px-3 py-2 font-mono text-xs text-foreground shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          ></textarea>
        </label>
      {/if}
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">
          {action.reasonRequired ? t('approvals.reasonRequired') : action.inputKind === 'choice_or_text' ? t('approvals.answer') : t('approvals.message')}
        </span>
        <Input bind:value={message} />
      </label>
      {#if action.rememberAllowed}
        <label class="flex items-center gap-2 text-xs text-muted-foreground">
          <input type="checkbox" bind:checked={remember} class="h-4 w-4 accent-brand" />
          <span>{t('approvals.rememberDecision')}</span>
        </label>
      {/if}
      {#if validation}
        <div class="text-xs text-status-failed">{t(validation as TranslationKey)}</div>
      {/if}
      <div class="flex justify-end gap-2">
        <Button disabled={busy} onclick={() => { if (!busy) onclose?.() }}>{t('approvals.cancel')}</Button>
        <Button
          disabled={busy || !!validation}
          ariaLabel={busy ? t('approvals.submitting') : approvalActionLabel(action, t)}
          onclick={() => onsubmit?.({ message, modifiedInput, choiceId, remember })}
        >
          {#if busy}
            <LoaderCircle class="h-4 w-4 animate-spin" aria-hidden="true" />
            {t('approvals.submitting')}
          {:else}
            {approvalActionLabel(action, t)}
          {/if}
        </Button>
      </div>
    </div>
  {/if}
</Dialog>
