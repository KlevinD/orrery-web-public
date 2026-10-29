<script lang="ts">
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { ToolCallResultDTO } from '#lib/types/api'
  import type { ToolViewModel } from '#lib/types/view-models'
  import {
    buildToolArguments,
    formFromToolSchema,
    setRawToolArguments,
    setToolFieldValue,
    type ToolCallFormState,
    type ToolSchemaField
  } from './toolCallForm'

  interface Props {
    open?: boolean
    tool?: ToolViewModel
    busy?: boolean
    result?: ToolCallResultDTO
    error?: string
    onclose?: () => void
    onsubmit?: (argumentsValue: Record<string, unknown>) => void
  }

  let {
    open = false,
    tool = undefined,
    busy = false,
    result = undefined,
    error = '',
    onclose,
    onsubmit
  }: Props = $props()

  let form = $state<ToolCallFormState>(formFromToolSchema(undefined))
  let currentToolId = $state('')
  let localError = $state('')
  let t = $derived(createTranslator($locale))

  $effect(() => {
    const nextToolId = tool?.id ?? ''
    if (currentToolId === nextToolId) return
    currentToolId = nextToolId
    form = formFromToolSchema(tool?.inputSchema)
    localError = ''
  })

  const hasResult = $derived(result !== undefined)

  function setField(name: string, value: string) {
    form = setToolFieldValue(form, name, value)
  }

  function setRawArguments(value: string) {
    form = setRawToolArguments(form, value)
  }

  function submit() {
    localError = ''
    try {
      onsubmit?.(buildToolArguments(form))
    } catch (err) {
      localError = err instanceof Error ? err.message : String(err)
    }
  }

  function resultText() {
    return JSON.stringify(result?.output ?? result ?? {}, null, 2)
  }
</script>

<Dialog open={open} title={tool ? `${t('resources.tools.call')}: ${tool.name}` : t('resources.tools.call')} size="wide" onClose={onclose}>
  <div class="grid gap-4">
    {#if tool}
      <div class="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
        <div class="font-mono text-foreground">{tool.id}</div>
        {#if tool.description}
          <div class="mt-1 leading-5">{tool.description}</div>
        {/if}
      </div>

      <div class="grid gap-3">
        {#if form.usesRawArguments}
          <label class="grid gap-1.5">
            <span class="flex items-center gap-1 text-xs font-medium text-muted-foreground">{t('resources.tools.argumentsJson')}</span>
            <textarea
              aria-label={t('resources.tools.argumentsJson')}
              value={form.rawArguments}
              oninput={(event) => setRawArguments(event.currentTarget.value)}
              class="min-h-32 rounded-md border border-border bg-input-background px-3 py-2 font-mono text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            ></textarea>
          </label>
        {:else}
          {#each form.fields as field (field.name)}
            {@render toolField(field)}
          {/each}
        {/if}
      </div>

      {#if localError || error}
        <div class="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {localError || error}
        </div>
      {/if}

      {#if hasResult}
        <pre class="max-h-80 overflow-auto rounded-md border border-border bg-muted/50 p-3 text-xs leading-5 text-foreground">{resultText()}</pre>
      {/if}

      <div class="flex justify-end gap-2">
        <Button variant="outline" disabled={busy} onclick={onclose}>{t('resources.action.cancel')}</Button>
        <Button disabled={busy} onclick={submit}>{busy ? t('resources.tools.calling') : t('resources.tools.call')}</Button>
      </div>
    {/if}
  </div>
</Dialog>

{#snippet toolField(field: ToolSchemaField)}
  <label class="grid gap-1.5">
    <span class="flex items-center gap-1 text-xs font-medium text-muted-foreground">
      {field.label}{field.required ? ' *' : ''}
    </span>
    {#if field.enumValues.length > 0}
      <Select
        value={form.values[field.name] ?? ''}
        options={[
          ...(field.required ? [] : [{ label: '', value: '' }]),
          ...field.enumValues.map((option) => ({ label: option.label, value: option.value }))
        ]}
        onchange={(event) => setField(field.name, event.currentTarget.value)}
      />
    {:else if field.type === 'boolean'}
      <Select
        value={form.values[field.name] ?? ''}
        options={[
          ...(field.required ? [] : [{ label: '', value: '' }]),
          { label: 'true', value: 'true' },
          { label: 'false', value: 'false' }
        ]}
        onchange={(event) => setField(field.name, event.currentTarget.value)}
      />
    {:else if field.type === 'array' || field.type === 'object'}
      <textarea
        aria-label={field.label}
        value={form.values[field.name] ?? ''}
        oninput={(event) => setField(field.name, event.currentTarget.value)}
        class="min-h-24 rounded-md border border-border bg-input-background px-3 py-2 font-mono text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      ></textarea>
    {:else}
      <Input
        ariaLabel={field.label}
        type={field.type === 'number' || field.type === 'integer' ? 'number' : 'text'}
        value={form.values[field.name] ?? ''}
        oninput={(event) => setField(field.name, event.currentTarget.value)}
      />
    {/if}
    {#if field.description}
      <span class="text-xs leading-5 text-muted-foreground">{field.description}</span>
    {/if}
  </label>
{/snippet}
