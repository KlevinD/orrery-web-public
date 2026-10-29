<script lang="ts">
  import Input from '#lib/components/ui/Input.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { buildRuntimeRegistrationConfig, runtimeProviderSchemas, type RuntimeProviderField, type RuntimeProviderKind } from '#lib/runtime/providerConfig'

  interface Props {
    kind: RuntimeProviderKind
    values?: Record<string, string>
    onchange?: (config: ReturnType<typeof buildRuntimeRegistrationConfig>) => void
  }

  let { kind, values = {}, onchange }: Props = $props()
  const schema = $derived(runtimeProviderSchemas.find((item) => item.kind === kind))
  let draft = $state<Record<string, string>>({})
  let t = $derived(createTranslator($locale))

  $effect(() => {
    const next = { ...values }
    if (schema?.fields.some((field) => field.name === 'mode') && !next.mode) next.mode = 'local'
    draft = next
  })

  function updateField(name: string, value: string) {
    draft = { ...draft, [name]: value }
    onchange?.(buildRuntimeRegistrationConfig(kind, draft))
  }

  function isVisible(field: RuntimeProviderField) {
    if (!field.visibleWhen) return true
    return Object.entries(field.visibleWhen).every(([name, values]) => {
      const current = draft[name] || (name === 'mode' && schema?.fields.some((field) => field.name === 'mode') ? 'local' : '')
      return values.includes(current)
    })
  }
</script>

{#if schema}
  <div class="grid gap-3 md:grid-cols-2" data-provider-kind={schema.kind}>
    {#each schema.fields.filter(isVisible) as field}
      <div class="space-y-1">
        <label class="flex min-h-9 overflow-hidden rounded-md border border-input bg-input-background">
          <span class="grid w-32 shrink-0 place-items-center border-r border-border bg-muted px-3 text-xs font-medium text-muted-foreground">
            {t(field.label)}
          </span>
          {#if field.kind === 'select'}
            <select
              aria-label={t(field.label)}
              value={draft[field.name] ?? field.options?.[0]?.value ?? ''}
              onchange={(event) => updateField(field.name, event.currentTarget.value)}
              class="h-9 min-w-0 flex-1 bg-transparent px-3 text-sm outline-none focus-visible:ring-0"
            >
              {#each field.options ?? [] as option}
                <option value={option.value}>{t(option.label)}</option>
              {/each}
            </select>
          {:else}
            <Input
              value={draft[field.name] ?? ''}
              placeholder={field.placeholder ? t(field.placeholder) : ''}
              ariaLabel={t(field.label)}
              oninput={(event) => updateField(field.name, event.currentTarget.value)}
              class="h-9 rounded-none border-0 bg-transparent shadow-none focus-visible:ring-0"
            />
          {/if}
        </label>
        {#if field.help}
          <p class="px-1 text-xs leading-5 text-muted-foreground">{t(field.help)}</p>
        {/if}
      </div>
    {/each}
  </div>
{/if}
