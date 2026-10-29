<script lang="ts">
  import { GripVertical, Pencil, Plus, Trash2 } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { SettingsPromptBlockViewModel, SettingsViewModel } from '#lib/types/view-models'

  interface Props {
    draft: SettingsViewModel
    busy?: boolean
    onsave?: () => void
  }

  let { draft = $bindable(), busy = false, onsave }: Props = $props()
  let t = $derived(createTranslator($locale))
  let draggingIndex = $state<number | null>(null)
  let editingIndex = $state<number | null>(null)
  let editingBlock = $derived(editingIndex === null ? undefined : draft.promptBlocks[editingIndex])

  const roleOptions = [
    { label: 'system', value: 'system' },
    { label: 'user', value: 'user' },
    { label: 'assistant', value: 'assistant' }
  ]

  let availablePromptVariables = $derived([
    { code: 'agent_id', label: 'Agent ID', description: t('settings.promptVariable.agentId') },
    { code: 'agent_real_name', label: t('settings.promptVariable.realName'), description: t('settings.promptVariable.realName.desc') },
    { code: 'agent_display_name', label: t('settings.promptVariable.displayName'), description: t('settings.promptVariable.displayName.desc') },
    { code: 'agent_role_name', label: t('settings.promptVariable.roleName'), description: t('settings.promptVariable.roleName.desc') },
    { code: 'agent_kind', label: t('settings.promptVariable.kind'), description: t('settings.promptVariable.kind.desc') },
    { code: 'agent_backstory', label: t('settings.promptVariable.backstory'), description: t('settings.promptVariable.backstory.desc') },
    {
      code: 'agent_responsibility_boundary',
      label: t('settings.promptVariable.responsibility'),
      description: t('settings.promptVariable.responsibility.desc')
    },
    { code: 'agent_behavior_boundary', label: t('settings.promptVariable.behavior'), description: t('settings.promptVariable.behavior.desc') },
    { code: 'discussion_goal', label: t('settings.promptVariable.discussionGoal'), description: t('settings.promptVariable.discussionGoal.desc') },
    { code: 'discussion_members', label: t('settings.promptVariable.discussionMembers'), description: t('settings.promptVariable.discussionMembers.desc') },
    { code: 'workspace_info', label: t('settings.promptVariable.workspaceInfo'), description: t('settings.promptVariable.workspaceInfo.desc') },
    { code: 'knowledge_context', label: t('settings.promptVariable.knowledgeContext'), description: t('settings.promptVariable.knowledgeContext.desc') },
    { code: 'runtime_context', label: t('settings.promptVariable.runtimeContext'), description: t('settings.promptVariable.runtimeContext.desc') },
    { code: 'conversation_history', label: t('settings.promptVariable.history'), description: t('settings.promptVariable.history.desc') },
    { code: 'current_user_input', label: t('settings.promptVariable.currentInput'), description: t('settings.promptVariable.currentInput.desc') }
  ])

  function moveBlock(from: number, to: number) {
    if (from === to || from < 0 || to < 0 || from >= draft.promptBlocks.length || to >= draft.promptBlocks.length) return
    const next = [...draft.promptBlocks]
    const [item] = next.splice(from, 1)
    next.splice(to, 0, item)
    draft.promptBlocks = next
    if (editingIndex === from) {
      editingIndex = to
    }
  }

  function onDragStart(index: number) {
    draggingIndex = index
  }

  function onDrop(index: number) {
    if (draggingIndex === null) return
    moveBlock(draggingIndex, index)
    draggingIndex = null
  }

  function setBlockEnabled(index: number, enabled: boolean) {
    draft.promptBlocks = draft.promptBlocks.map((block, currentIndex) =>
      currentIndex === index ? { ...block, enabled } : block
    )
  }

  function openEditor(index: number) {
    editingIndex = index
  }

  function closeEditor() {
    editingIndex = null
  }

  function addCustomBlock() {
    const id = `custom-${Date.now().toString(36)}`
    draft.promptBlocks = [
      ...draft.promptBlocks,
      {
        blockId: id,
        code: id,
        title: t('settings.promptCustomTitle'),
        description: '',
        role: 'system',
        content: '',
        placeholder: `<<${id}>>`,
        dynamic: false,
        editable: true,
        removable: true,
        variableOnly: false,
        enabled: true
      }
    ]
    editingIndex = draft.promptBlocks.length - 1
  }

  function removeBlock(index: number) {
    const block = draft.promptBlocks[index]
    if (!block?.removable) return
    draft.promptBlocks = draft.promptBlocks.filter((_, itemIndex) => itemIndex !== index)
    if (editingIndex === index) {
      editingIndex = null
    } else if (editingIndex !== null && editingIndex > index) {
      editingIndex -= 1
    }
  }

  function referenceFor(block: SettingsPromptBlockViewModel) {
    return `<<${block.code || block.blockId}>>`
  }

  function isLockedDynamicBlock(block: SettingsPromptBlockViewModel) {
    return block.blockId === 'conversation_history'
  }

  function variableValues() {
    const values = new Map<string, string>()
    for (const variable of availablePromptVariables) {
      values.set(variable.code, `<<${variable.code}>>`)
    }
    for (const block of draft.promptBlocks) {
      if (!block.enabled) continue
      values.set(block.code || block.blockId, isLockedDynamicBlock(block) ? referenceFor(block) : block.content)
    }
    return values
  }

  function resolvePreviewVariables(content: string, values = variableValues()) {
    let resolved = content
    for (let i = 0; i < 6; i += 1) {
      let changed = false
      const next = resolved.replace(/<<\s*([A-Za-z][A-Za-z0-9_.-]*)\s*>>|\{\{\s*([A-Za-z][A-Za-z0-9_.-]*)\s*\}\}/g, (token, angleCode, braceCode) => {
        const value = values.get(angleCode || braceCode)
        if (value === undefined) return token
        changed = true
        return value
      })
      resolved = next
      if (!changed) break
    }
    return resolved
  }

  function previewContent(block: SettingsPromptBlockViewModel) {
    if (!block.enabled || block.variableOnly) return ''
    if (isLockedDynamicBlock(block)) return referenceFor(block)
    return resolvePreviewVariables(block.content)
  }

  function finalPreview() {
    return draft.promptBlocks
      .filter((block) => block.enabled && !block.variableOnly)
      .map((block) => previewContent(block))
      .filter((content) => content.trim().length > 0)
      .join('\n\n')
  }
</script>

<section class="flex h-full min-h-0 flex-col rounded-xl border border-border bg-card p-4">
  <div class="flex shrink-0 flex-col items-start justify-between gap-4 sm:flex-row">
    <div>
      <h2 class="text-sm font-semibold text-foreground">{t('settings.promptBlocks')}</h2>
      <p class="mt-1 text-xs text-muted-foreground">{t('settings.promptBlocks.desc')}</p>
    </div>
    <div class="flex w-full shrink-0 flex-wrap items-center gap-2 sm:w-auto">
      <Button type="button" variant="outline" disabled={busy} onclick={addCustomBlock}>
        <Plus class="h-4 w-4" />
        {t('settings.promptAddBlock')}
      </Button>
      <Button disabled={busy} onclick={onsave}>{t('settings.save')}</Button>
    </div>
  </div>

  <div class="mt-4 grid min-h-0 flex-1 gap-4 xl:grid-cols-[minmax(0,1.05fr)_minmax(360px,0.95fr)]">
    <div class="subtle-scrollbar min-h-0 space-y-2 overflow-y-auto pr-1">
      {#each draft.promptBlocks as block, index (block.blockId)}
        <article
          class="rounded-lg border border-border bg-background p-2 transition-colors {draggingIndex === index ? 'border-brand bg-brand/5' : ''} {!block.enabled ? 'opacity-60' : ''}"
          draggable="true"
          ondragstart={() => onDragStart(index)}
          ondragend={() => (draggingIndex = null)}
          ondragover={(event) => event.preventDefault()}
          ondrop={() => onDrop(index)}
        >
          <div class="flex items-start gap-2">
            <span
              class="inline-flex h-8 w-8 shrink-0 cursor-grab items-center justify-center rounded-md text-muted-foreground"
              title={t('settings.promptDragTip')}
              aria-hidden="true"
            >
              <GripVertical class="h-4 w-4" />
            </span>

            <button
              type="button"
              class="min-w-0 flex-1 rounded-md px-2 py-1 text-left hover:bg-muted/60"
              onclick={() => openEditor(index)}
              disabled={busy}
              title={t('settings.promptEditBlock')}
            >
              <div class="flex min-w-0 flex-wrap items-center gap-2">
                <span class="min-w-0 truncate text-sm font-semibold text-foreground">{block.title}</span>
                <code class="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{referenceFor(block)}</code>
                <span class="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{block.role}</span>
                <span class="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
                  {isLockedDynamicBlock(block) ? t('settings.promptDynamic') : t('settings.promptStatic')}
                </span>
                {#if block.variableOnly}
                  <span class="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{t('settings.promptVariableOnly')}</span>
                {/if}
              </div>
              {#if block.description}
                <p class="mt-1 truncate text-xs text-muted-foreground">{block.description}</p>
              {/if}
              <p class="mt-1 text-[11px] text-muted-foreground">
                {isLockedDynamicBlock(block) ? t('settings.promptDynamic.desc') : t('settings.promptContentCollapsed')}
              </p>
            </button>

            <label
              class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
              title={t('settings.enabled')}
            >
              <input
                class="h-4 w-4 rounded border-input"
                type="checkbox"
                checked={block.enabled}
                disabled={busy}
                onchange={(event) => setBlockEnabled(index, event.currentTarget.checked)}
              />
              <span class="sr-only">{t('settings.enabled')}</span>
            </label>

            <button
              type="button"
              class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
              onclick={() => openEditor(index)}
              disabled={busy}
              title={t('settings.promptEditBlock')}
              aria-label={t('settings.promptEditBlock')}
            >
              <Pencil class="h-4 w-4" />
            </button>

            {#if block.removable}
              <button
                type="button"
                class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive disabled:opacity-50"
                onclick={() => removeBlock(index)}
                disabled={busy}
                title={t('settings.promptRemoveBlock')}
                aria-label={t('settings.promptRemoveBlock')}
              >
                <Trash2 class="h-4 w-4" />
              </button>
            {/if}
          </div>
        </article>
      {/each}
    </div>

    <aside class="flex min-h-0 flex-col rounded-xl border border-border bg-background p-3">
      <div class="shrink-0">
        <h3 class="text-sm font-semibold text-foreground">{t('settings.promptPreview')}</h3>
        <p class="mt-1 text-xs text-muted-foreground">{t('settings.promptPreview.desc')}</p>
      </div>
      <pre class="subtle-scrollbar mt-3 min-h-0 flex-1 overflow-auto rounded-md bg-muted p-3 text-xs leading-5 text-foreground whitespace-pre-wrap">{finalPreview()}</pre>

      <div class="mt-4 flex min-h-0 flex-1 flex-col border-t border-border pt-3">
        <div class="flex shrink-0 flex-wrap items-start justify-between gap-3">
          <div>
            <h3 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{t('settings.promptVariables')}</h3>
            <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('settings.promptVariables.desc')}</p>
          </div>
          <code class="rounded-md border border-border bg-muted px-2 py-1 text-[11px] text-foreground">&lt;&lt;code&gt;&gt;</code>
        </div>
        <div class="subtle-scrollbar mt-3 grid min-h-0 flex-1 gap-2 overflow-y-auto pr-1">
          {#each availablePromptVariables as variable}
            <div class="rounded-md border border-border bg-muted/30 px-2.5 py-2">
              <div class="flex min-w-0 items-center gap-2">
                <code class="shrink-0 rounded bg-background px-1.5 py-0.5 text-[11px] text-foreground">&lt;&lt;{variable.code}&gt;&gt;</code>
                <span class="truncate text-xs font-medium text-foreground">{variable.label}</span>
              </div>
              <p class="mt-1 text-[11px] leading-4 text-muted-foreground">{variable.description}</p>
            </div>
          {/each}
        </div>
      </div>
    </aside>
  </div>
</section>

<Dialog open={editingIndex !== null} title={editingBlock?.title ?? t('settings.promptEditBlock')} size="wide" onClose={closeEditor}>
  {#if editingBlock}
    <div class="space-y-4">
      <div class="grid gap-3 md:grid-cols-2">
        <label class="grid gap-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('settings.promptTitle')}</span>
          <Input bind:value={editingBlock.title} disabled={busy} />
        </label>
        <label class="grid gap-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('settings.promptCode')}</span>
          <Input bind:value={editingBlock.code} disabled={busy} />
        </label>
        <label class="grid gap-1.5 md:col-span-2">
          <span class="text-xs font-medium text-muted-foreground">{t('settings.promptDescription')}</span>
          <Input bind:value={editingBlock.description} disabled={busy} />
        </label>
        <label class="grid gap-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('settings.promptRole')}</span>
          <Select bind:value={editingBlock.role} options={roleOptions} disabled={busy} />
        </label>
        <div class="flex flex-wrap items-end gap-4">
          <label class="flex items-center gap-2 text-xs text-muted-foreground">
            <input class="h-4 w-4 rounded border-input" type="checkbox" bind:checked={editingBlock.enabled} disabled={busy} />
            {t('settings.enabled')}
          </label>
          <label class="flex items-center gap-2 text-xs text-muted-foreground" title={t('settings.promptVariableOnly.desc')}>
            <input class="h-4 w-4 rounded border-input" type="checkbox" bind:checked={editingBlock.variableOnly} disabled={busy} />
            {t('settings.promptVariableOnly')}
          </label>
        </div>
      </div>

      {#if isLockedDynamicBlock(editingBlock)}
        <div class="rounded-md border border-dashed border-border bg-muted/40 p-3 text-xs text-muted-foreground">
          <div class="font-medium text-foreground">{referenceFor(editingBlock)}</div>
          <p class="mt-1">{t('settings.promptDynamic.desc')}</p>
        </div>
      {:else}
        <label class="block space-y-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('settings.promptContent')}</span>
          <textarea
            class="min-h-72 w-full resize-y rounded-md border border-input bg-input-background px-3 py-2 text-sm leading-6 shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
            bind:value={editingBlock.content}
            disabled={!editingBlock.editable || busy}
          ></textarea>
        </label>
      {/if}

      <details class="rounded-lg border border-border bg-muted/30 p-3">
        <summary class="cursor-pointer text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          {t('settings.promptVariables')}
        </summary>
        <p class="mt-2 text-xs leading-5 text-muted-foreground">{t('settings.promptVariables.desc')}</p>
        <div class="mt-3 grid gap-2 sm:grid-cols-2">
          {#each availablePromptVariables as variable}
            <div class="rounded-md border border-border bg-background px-2.5 py-2">
              <div class="flex min-w-0 items-center gap-2">
                <code class="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[11px] text-foreground">&lt;&lt;{variable.code}&gt;&gt;</code>
                <span class="truncate text-xs font-medium text-foreground">{variable.label}</span>
              </div>
              <p class="mt-1 text-[11px] leading-4 text-muted-foreground">{variable.description}</p>
            </div>
          {/each}
        </div>
      </details>
    </div>
  {/if}
</Dialog>
