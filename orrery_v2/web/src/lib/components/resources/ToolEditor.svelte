<script lang="ts">
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { TranslationKey } from '#lib/i18n'
  import type { ResourceToolCommandFormViewModel, ToolViewModel } from '#lib/types/view-models'

  interface Props {
    tool?: ToolViewModel
    busy?: boolean
    onsubmit?: (form: ResourceToolCommandFormViewModel) => void
    ondisable?: (tool: ToolViewModel) => void
    ondelete?: (tool: ToolViewModel) => void
  }

  let { tool = undefined, busy = false, onsubmit, ondisable, ondelete }: Props = $props()

  let toolId = $state('')
  let name = $state('')
  let description = $state('')
  let version = $state('1.0.0')
  let kind = $state('internal_service_tool')
  let riskLevel = $state('medium')
  let command = $state('')
  let url = $state('')
  let tags = $state('')
  let enabled = $state('true')
  let t = $derived(createTranslator($locale))
  const editable = $derived(!tool || tool.manageable)

  $effect(() => {
    toolId = tool?.id ?? ''
    name = tool?.name === 'Not provided' ? '' : (tool?.name ?? '')
    description = tool?.description === 'Not provided' ? '' : (tool?.description ?? '')
    version = tool?.version === 'Not provided' ? '1.0.0' : (tool?.version ?? '1.0.0')
    kind = tool?.kind === 'Not provided' ? 'internal_service_tool' : (tool?.kind ?? 'internal_service_tool')
    riskLevel = tool?.riskLevel === 'Not provided' ? 'medium' : (tool?.riskLevel ?? 'medium')
    command = tool?.command ?? ''
    url = tool?.url ?? ''
    tags = tool?.tags.join(', ') ?? ''
    enabled = tool ? String(tool.enabled) : 'true'
  })

  const canSubmit = $derived(editable && name.trim().length > 0 && hasKindTarget())

  function hasKindTarget() {
    if (kind === 'cli_tool') return command.trim().length > 0
    if (kind === 'api_tool') return url.trim().length > 0
    return true
  }

  function submit() {
    if (!canSubmit) return
    onsubmit?.({
      toolId: toolId.trim(),
      name: name.trim(),
      description: description.trim(),
      version: version.trim() || '1.0.0',
      kind,
      riskLevel,
      command: command.trim(),
      url: url.trim(),
      tags,
      enabled
    })
  }
</script>

<section class="panel p-4">
  <h2 class="text-sm font-semibold text-foreground">{t('resources.tools.editor')}</h2>
  <div class="mt-4 grid gap-3 md:grid-cols-3">
    {#if tool}
      {@render readonlyField('filter.resourceId', toolId)}
      {#if !editable}
        {@render readonlyField('resources.tools.kind', kind)}
        {@render readonlyField('resources.mcp.server', tool.serverToolId)}
        {@render readonlyField('resources.mcp.tools', tool.downstreamToolName)}
      {/if}
    {/if}
    {#if editable}
      {@render textField('resources.editor.name', name, (value) => (name = value))}
      {@render textField('resources.editor.description', description, (value) => (description = value))}
      {@render textField('resources.skills.version', version, (value) => (version = value))}
      {@render selectField('resources.tools.kind', kind, (value) => (kind = value), [
        // TODO(resource-tool-approval): expose approval tool kinds after the command DTO carries approval policy fields.
        { label: 'internal_service_tool', value: 'internal_service_tool' },
        { label: 'cli_tool', value: 'cli_tool' },
        { label: 'api_tool', value: 'api_tool' },
        { label: 'browser_tool', value: 'browser_tool' },
        { label: 'rpa_tool', value: 'rpa_tool' }
      ])}
      {@render selectField('resources.editor.risk', riskLevel, (value) => (riskLevel = value), [
        { label: t('risk.low'), value: 'low' },
        { label: t('risk.medium'), value: 'medium' },
        { label: t('risk.high'), value: 'high' },
        { label: t('risk.critical'), value: 'critical' }
      ])}
      {@render selectField('resources.editor.status', enabled, (value) => (enabled = value), [
        { label: t('kpi.enabled'), value: 'true' },
        { label: t('kpi.disabled'), value: 'false' }
      ])}
      {@render textField('resources.mcp.command', command, (value) => (command = value))}
      {@render textField('resources.mcp.endpoint', url, (value) => (url = value))}
      {@render textField('resources.editor.category', tags, (value) => (tags = value))}
      <Button disabled={!canSubmit || busy} onclick={submit}>{tool ? t('resources.action.update') : t('resources.action.register')}</Button>
      {#if tool}
        <Button disabled={!tool.enabled || busy} variant="destructive" onclick={() => ondisable?.(tool)}>{t('resources.action.disable')}</Button>
        <Button disabled={busy} variant="destructive" onclick={() => ondelete?.(tool)}>{t('resources.action.delete')}</Button>
      {/if}
    {/if}
  </div>
</section>

{#snippet textField(label: TranslationKey, value: string, setValue: (value: string) => void)}
  <label class="flex min-h-9 overflow-hidden rounded-md border border-input bg-input-background">
    <span class="grid w-32 shrink-0 place-items-center border-r border-border bg-muted px-3 text-xs font-medium text-muted-foreground">
      {t(label)}
    </span>
    <Input value={value} oninput={(event) => setValue(event.currentTarget.value)} class="h-9 rounded-none border-0 bg-transparent shadow-none focus-visible:ring-0" />
  </label>
{/snippet}

{#snippet readonlyField(label: TranslationKey, value: string)}
  <div class="flex min-h-9 overflow-hidden rounded-md border border-input bg-muted">
    <span class="grid w-32 shrink-0 place-items-center border-r border-border bg-muted px-3 text-xs font-medium text-muted-foreground">
      {t(label)}
    </span>
    <span class="flex min-w-0 flex-1 items-center break-all px-3 py-1 text-sm text-foreground">{value}</span>
  </div>
{/snippet}

{#snippet selectField(label: TranslationKey, value: string, setValue: (value: string) => void, options: Array<{ label: string; value: string }>)}
  <label class="flex min-h-9 overflow-hidden rounded-md border border-input bg-input-background">
    <span class="grid w-32 shrink-0 place-items-center border-r border-border bg-muted px-3 text-xs font-medium text-muted-foreground">
      {t(label)}
    </span>
    <Select value={value} {options} onchange={(event) => setValue(event.currentTarget.value)} class="h-9 rounded-none border-0 bg-transparent shadow-none focus-visible:ring-0" />
  </label>
{/snippet}
