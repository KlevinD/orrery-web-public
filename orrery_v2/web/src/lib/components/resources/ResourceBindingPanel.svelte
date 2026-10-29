<script lang="ts">
  import { KeyRound, Link2, ShieldCheck, Unlink } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import Tooltip from '#lib/components/ui/Tooltip.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { TranslationKey } from '#lib/i18n'
  import type { ResourceBindingFormViewModel, ResourceBindingViewModel } from '#lib/types/view-models'

  interface BindableResource {
    id: string
    name: string
    authRequired?: boolean
    authMode?: string
    authHeaderName?: string
    defaultAKConfigured?: boolean
    bindings?: ResourceBindingViewModel[]
  }

  interface AgentAKSummary extends ResourceBindingViewModel {
    serverName: string
  }

  interface Props {
    resource?: BindableResource
    relatedResources?: BindableResource[]
    resourceType?: string
    busy?: boolean
    message?: string
    onbind?: (form: ResourceBindingFormViewModel) => void
    onunbind?: (form: ResourceBindingFormViewModel) => void
  }

  let {
    resource,
    relatedResources = [],
    resourceType = 'tool',
    busy = false,
    message = '',
    onbind,
    onunbind
  }: Props = $props()

  let agentId = $state('')
  let alias = $state('')
  let scope = $state('mission')
  let agentAK = $state('')
  let authHeaderName = $state('Authorization')
  let loadedResourceId = $state<string | undefined>()
  let t = $derived(createTranslator($locale))

  $effect(() => {
    if (loadedResourceId === resource?.id) return
    loadedResourceId = resource?.id
    agentAK = ''
    authHeaderName = resource?.authHeaderName?.trim() || 'Authorization'
  })

  const isMcpServerBinding = $derived(resourceType === 'mcp_server')
  const mcpAuthRequired = $derived(isMcpServerBinding && resource?.authRequired === true)
  const defaultAKConfigured = $derived(mcpAuthRequired && resource?.defaultAKConfigured === true)
  const requiresAgentAK = $derived(mcpAuthRequired && !defaultAKConfigured)
  const effectiveAuthHeaderName = $derived(authHeaderName.trim() || resource?.authHeaderName?.trim() || 'Authorization')
  const agentAKBindings = $derived(agentAKSummaries(relatedResources.length > 0 ? relatedResources : resource ? [resource] : [], agentId))

  const form = $derived<ResourceBindingFormViewModel | undefined>(
    resource && agentId.trim() && (!requiresAgentAK || agentAK.trim())
      ? {
          resourceId: resource.id,
          resourceType,
          agentId,
          alias,
          scope,
          agentAK: requiresAgentAK ? agentAK : undefined,
          authHeaderName: requiresAgentAK ? effectiveAuthHeaderName : undefined
        }
      : undefined
  )

  function agentAKSummaries(resources: BindableResource[], agentId: string): AgentAKSummary[] {
    const targetAgentId = agentId.trim()
    return resources.flatMap((resource) =>
      (resource.bindings ?? [])
        .filter((binding) => binding.agentAKConfigured && (!targetAgentId || binding.agentId === targetAgentId))
        .map((binding) => ({
          ...binding,
          serverName: resource.name
        }))
    )
  }
</script>

<section class="panel p-4">
  <div class="flex items-center justify-between gap-3">
    <div>
      <h2 class="text-sm font-semibold text-foreground">{t('resources.binding.title')}</h2>
      <p class="mt-1 text-xs text-muted-foreground">
        {resource ? resource.name : t('resources.binding.selectResource')}
      </p>
    </div>
  </div>

  {#if mcpAuthRequired}
    <div class="mt-4 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-foreground">
      <div class="flex items-start gap-2">
        {#if defaultAKConfigured}
          <ShieldCheck class="mt-0.5 h-4 w-4 shrink-0 text-status-done" />
          <div>
            <div class="font-semibold">{t('resources.binding.defaultAKConfigured')}</div>
            <p class="mt-1 text-muted-foreground">{t('resources.binding.defaultAKConfiguredDescription')}</p>
          </div>
        {:else}
          <KeyRound class="mt-0.5 h-4 w-4 shrink-0 text-amber-700 dark:text-amber-300" />
          <div>
            <div class="font-semibold">{t('resources.binding.agentAKRequired')}</div>
            <p class="mt-1 text-muted-foreground">{t('resources.binding.agentAKRequiredDescription')}</p>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <div class="mt-4 grid gap-3 md:grid-cols-[1fr_1fr_11rem_auto_auto]">
    {@render textField('resources.binding.agentProfileId', agentId, (value) => (agentId = value))}
    {@render textField('resources.binding.alias', alias, (value) => (alias = value))}
    {@render selectField('resources.editor.scope', scope, (value) => (scope = value), [
      { label: t('resources.binding.scopeMission'), value: 'mission' },
      { label: t('resources.binding.scopeWorkspace'), value: 'workspace' },
      { label: t('resources.binding.scopeGlobal'), value: 'global' }
    ])}
    <Tooltip text={t('resources.binding.bind')}>
      <Button disabled={!form || busy} size="icon" ariaLabel={t('resources.binding.bind')} onclick={() => form && onbind?.(form)}>
        <Link2 class="h-4 w-4" />
      </Button>
    </Tooltip>
    <Tooltip text={t('resources.binding.unbind')}>
      <Button disabled={!form || busy} size="icon" ariaLabel={t('resources.binding.unbind')} onclick={() => form && onunbind?.(form)}>
        <Unlink class="h-4 w-4" />
      </Button>
    </Tooltip>
  </div>

  {#if requiresAgentAK}
    <div class="mt-3 grid gap-3 md:grid-cols-2">
      {@render textField('resources.binding.authHeaderName', authHeaderName, (value) => (authHeaderName = value))}
      {@render textField('resources.binding.agentAK', agentAK, (value) => (agentAK = value), 'password')}
    </div>
  {/if}

  {#if agentAKBindings.length > 0}
    <div class="mt-3 rounded-md border border-border bg-muted/40 px-3 py-2">
      <div class="text-xs font-semibold text-foreground">{t('resources.binding.agentAKList')}</div>
      <div class="mt-2 grid gap-2">
        {#each agentAKBindings as binding}
          <div class="flex flex-wrap items-center gap-2 rounded-md bg-background px-2 py-1.5 text-xs">
            <span class="font-medium text-foreground">{binding.label}</span>
            <span class="text-muted-foreground">{t('resources.binding.agentAKUsage', { server: binding.serverName, scope: binding.scope })}</span>
            <span class="rounded-md border border-border px-1.5 py-0.5 text-muted-foreground">{binding.authHeaderName || effectiveAuthHeaderName}</span>
            <span class="font-mono text-foreground">{binding.agentAKMasked}</span>
          </div>
        {/each}
      </div>
    </div>
  {/if}

  {#if message}
    <div class="mt-3 rounded-md border border-brand/25 bg-brand/10 px-3 py-2 text-xs text-foreground">
      {message}
    </div>
  {/if}
</section>

{#snippet textField(label: TranslationKey, value: string, setValue: (value: string) => void, type = 'text')}
  <label class="flex min-h-9 overflow-hidden rounded-md border border-input bg-input-background">
    <span class="grid w-32 shrink-0 place-items-center border-r border-border bg-muted px-3 text-xs font-medium text-muted-foreground">
      {t(label)}
    </span>
    <Input {type} ariaLabel={t(label)} value={value} oninput={(event) => setValue(event.currentTarget.value)} class="h-9 rounded-none border-0 bg-transparent shadow-none focus-visible:ring-0" />
  </label>
{/snippet}

{#snippet selectField(label: TranslationKey, value: string, setValue: (value: string) => void, options: Array<{ label: string; value: string }>)}
  <label class="flex min-h-9 overflow-hidden rounded-md border border-input bg-input-background">
    <span class="grid w-24 shrink-0 place-items-center border-r border-border bg-muted px-3 text-xs font-medium text-muted-foreground">
      {t(label)}
    </span>
    <Select value={value} {options} onchange={(event) => setValue(event.currentTarget.value)} class="h-9 rounded-none border-0 bg-transparent shadow-none focus-visible:ring-0" />
  </label>
{/snippet}
