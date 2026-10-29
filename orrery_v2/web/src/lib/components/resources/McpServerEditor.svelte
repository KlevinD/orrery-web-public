<script lang="ts">
  import { CheckCircle2, Plus, Save, Trash2 } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import Tooltip from '#lib/components/ui/Tooltip.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { TranslationKey } from '#lib/i18n'
  import type {
    DiscoveredMcpServerViewModel,
    McpServerViewModel,
    ResourceMcpServerCommandFormViewModel
  } from '#lib/types/view-models'

  interface Props {
    server?: McpServerViewModel
    busy?: boolean
    discoveryQuery?: string
    discoveryResults?: DiscoveredMcpServerViewModel[]
    discoveryLoading?: boolean
    discoveryError?: string
    ondiscoveryquery?: (query: string) => void
    onsubmit?: (form: ResourceMcpServerCommandFormViewModel) => void
    ondelete?: (server: McpServerViewModel) => void
    onnew?: () => void
  }

  let {
    server = undefined,
    busy = false,
    discoveryQuery = '',
    discoveryResults = [],
    discoveryLoading = false,
    discoveryError = '',
    ondiscoveryquery,
    onsubmit,
    ondelete,
    onnew
  }: Props = $props()

  let toolId = $state('')
  let name = $state('')
  let description = $state('')
  let version = $state('1.0.0')
  let serverName = $state('')
  let transport = $state('stdio')
  let command = $state('')
  let url = $state('')
  let headers = $state('')
  let authHeaderName = $state('Authorization')
  let defaultAK = $state('')
  let args = $state('')
  let exposedTools = $state('')
  let tags = $state('')
  let configJson = $state('')
  let selectedDiscovery = $state<DiscoveredMcpServerViewModel | undefined>()
  let editingServerId = $state<string | undefined>()
  let creatingNew = $state(false)
  let t = $derived(createTranslator($locale))

  $effect(() => {
    const nextServerId = server?.id
    if (nextServerId === editingServerId) return
    editingServerId = nextServerId
    creatingNew = false
    selectedDiscovery = undefined
    if (server) {
      fillFromServer(server)
    } else {
      resetForm()
    }
  })

  const isEditing = $derived(Boolean(server && !creatingNew))
  const hasPastedConfig = $derived(!isEditing && configJson.trim().length > 0)
  const canSubmit = $derived((isEditing || toolId.trim().length === 0) && (hasPastedConfig || (name.trim().length > 0 && serverName.trim().length > 0 && hasTransportTarget())))
  const canRegisterDiscovered = $derived(Boolean(selectedDiscovery))

  function hasTransportTarget() {
    if (transport === 'stdio') return command.trim().length > 0
    return url.trim().length > 0
  }

  function submit() {
    if (!canSubmit) return
    onsubmit?.({
      toolId: toolId.trim(),
      configJson: configJson.trim(),
      name: name.trim(),
      description: description.trim(),
      version: version.trim() || '1.0.0',
      serverName: serverName.trim(),
      transport,
      command: command.trim(),
      url: url.trim(),
      headers,
      authHeaderName: authHeaderName.trim() || 'Authorization',
      defaultAK,
      args,
      exposedTools,
      tags
    })
  }

  function registerDiscovered() {
    if (!selectedDiscovery) return
    submit()
  }

  function updateDiscoveryQuery(value: string) {
    ondiscoveryquery?.(value)
    selectedDiscovery = undefined
  }

  function selectDiscovery(server: DiscoveredMcpServerViewModel) {
    selectedDiscovery = server
    const commandSpec = commandFromPackageRefs(server.packageRefs)
    name = server.name
    description = server.description === 'Not provided' ? '' : server.description
    version = server.version === 'Not provided' ? version : server.version
    serverName = safeMcpServerName(server.name || server.id)
    transport = 'stdio'
    command = commandSpec.command
    args = commandSpec.args.join(', ')
    url = ''
    headers = ''
    configJson = ''
    exposedTools = server.tools.join(', ')
    tags = 'mcp, discovered'
  }

  function startNewServer() {
    creatingNew = true
    selectedDiscovery = undefined
    resetForm()
    onnew?.()
  }

  function fillFromServer(nextServer: McpServerViewModel) {
    toolId = nextServer.id
    name = nextServer.name === 'Not provided' ? '' : nextServer.name
    description = nextServer.description === 'Not provided' ? '' : nextServer.description
    version = nextServer.version === 'Not provided' ? '1.0.0' : nextServer.version
    serverName = nextServer.serverName === 'Not provided' ? '' : nextServer.serverName
    transport = nextServer.transport === 'Not provided' ? 'stdio' : nextServer.transport
    command = nextServer.command === 'Not provided' ? '' : nextServer.command
    url = nextServer.endpoint === 'Not provided' ? '' : nextServer.endpoint
    headers = formatHeaders(nextServer.headers)
    authHeaderName = nextServer.authHeaderName || 'Authorization'
    defaultAK = ''
    args = nextServer.args.join(', ')
    exposedTools = nextServer.exposedTools.join(', ')
    tags = nextServer.tags.join(', ')
    configJson = ''
  }

  function resetForm() {
    toolId = ''
    name = ''
    description = ''
    version = '1.0.0'
    serverName = ''
    transport = 'stdio'
    command = ''
    url = ''
    headers = ''
    authHeaderName = 'Authorization'
    defaultAK = ''
    args = ''
    exposedTools = ''
    tags = ''
    configJson = ''
  }

  function formatHeaders(value: Record<string, string>) {
    return Object.keys(value).length ? JSON.stringify(value, null, 2) : ''
  }

  function commandFromPackageRefs(packageRefs: string[]) {
    const first = packageRefs.find((ref) => ref.trim().length > 0)?.trim() ?? ''
    const [registryType, rawIdentifier] = first.includes(':') ? first.split(/:(.*)/s).filter(Boolean) : ['', first]
    const identifier = packageIdentifier(rawIdentifier)
    switch (registryType.toLowerCase()) {
      case 'npm':
        return { command: 'npx', args: identifier ? ['-y', identifier] : [] }
      case 'docker':
        return { command: 'docker', args: identifier ? ['run', '--rm', identifier] : ['run', '--rm'] }
      default:
        return { command: identifier || first, args: [] }
    }
  }

  function packageIdentifier(value: string) {
    const trimmed = value.trim()
    if (!trimmed) return ''
    const atIndex = trimmed.lastIndexOf('@')
    if (atIndex <= 0) return trimmed
    if (trimmed.startsWith('@')) {
      const scopedVersionIndex = trimmed.indexOf('@', 1)
      return scopedVersionIndex > 0 ? trimmed.slice(0, scopedVersionIndex) : trimmed
    }
    return trimmed.slice(0, atIndex)
  }

  function safeMcpServerName(value: string) {
    const clean = value.replace(/[^a-zA-Z0-9_.-]+/g, '-').replace(/^-+|-+$/g, '')
    return (clean || 'server').toLowerCase()
  }
</script>

<section class="panel p-4">
  <div class="flex flex-wrap items-center justify-between gap-3">
    <h2 class="text-sm font-semibold text-foreground">{t('nav.mcp')}</h2>
    {#if server}
      <Tooltip text={t('resources.action.registerNew')}>
        <Button variant="outline" size="icon" ariaLabel={t('resources.action.registerNew')} disabled={busy} onclick={startNewServer}>
          <Plus class="h-4 w-4" />
        </Button>
      </Tooltip>
    {/if}
  </div>
  {#if !isEditing}
    <div class="mt-4">
      {@render textareaField('resources.mcp.configJson', configJson, (value) => (configJson = value))}
    </div>
  {/if}
  <div class="mt-4 grid gap-3 md:grid-cols-3">
    {#if isEditing}
      {@render readonlyField('filter.resourceId', toolId)}
    {/if}
    {@render textField('resources.editor.name', name, (value) => (name = value))}
    {@render textField('resources.editor.description', description, (value) => (description = value))}
    {@render textField('resources.skills.version', version, (value) => (version = value))}
    {@render textField('resources.mcp.server', serverName, (value) => (serverName = value))}
    {@render selectField('resources.mcp.transport', transport, (value) => (transport = value), [
      { label: 'stdio', value: 'stdio' },
      { label: 'streamable_http', value: 'streamable_http' },
      { label: 'sse', value: 'sse' }
    ])}
    {@render textField('resources.mcp.command', command, (value) => (command = value))}
    {@render textField('resources.mcp.endpoint', url, (value) => (url = value))}
    {@render textField('resources.binding.authHeaderName', authHeaderName, (value) => (authHeaderName = value))}
    {@render textField('resources.mcp.defaultAK', defaultAK, (value) => (defaultAK = value), 'password')}
    {@render textareaField('resources.mcp.headers', headers, (value) => (headers = value), 'small')}
    {@render textField('resources.mcp.args', args, (value) => (args = value))}
    {@render textField('resources.mcp.tools', exposedTools, (value) => (exposedTools = value))}
    {@render textField('resources.editor.category', tags, (value) => (tags = value))}
    <Tooltip text={isEditing ? t('resources.action.update') : t('resources.action.register')}>
      <Button disabled={!canSubmit || busy} size="icon" ariaLabel={isEditing ? t('resources.action.update') : t('resources.action.register')} onclick={submit}>
        <Save class="h-4 w-4" />
      </Button>
    </Tooltip>
    {#if isEditing}
      <Tooltip text={t('resources.action.delete')}>
        <Button disabled={!server || busy} variant="destructive" size="icon" ariaLabel={t('resources.action.delete')} onclick={() => server && ondelete?.(server)}>
          <Trash2 class="h-4 w-4" />
        </Button>
      </Tooltip>
    {/if}
  </div>

  <div class="mt-5 border-t border-border pt-4">
    <div class="grid gap-3 md:grid-cols-[minmax(0,1fr)_auto]">
      {@render textField('resources.mcp.discoverySearch', discoveryQuery, updateDiscoveryQuery)}
      <Tooltip text={t('resources.action.registerDiscovered')}>
        <Button disabled={!canRegisterDiscovered || busy} variant="outline" size="icon" ariaLabel={t('resources.action.registerDiscovered')} onclick={registerDiscovered}>
          <CheckCircle2 class="h-4 w-4" />
        </Button>
      </Tooltip>
    </div>

    {#if discoveryLoading}
      <div class="mt-3 rounded-md border border-border bg-muted px-3 py-2 text-xs text-muted-foreground">
        {t('resources.mcp.discoveryLoading')}
      </div>
    {:else if discoveryError}
      <div class="mt-3 rounded-md border border-status-blocked/30 bg-status-blocked-bg px-3 py-2 text-xs text-status-blocked">
        {discoveryError}
      </div>
    {:else if discoveryQuery.trim().length > 0 && discoveryResults.length === 0}
      <div class="mt-3 rounded-md border border-border bg-muted px-3 py-2 text-xs text-muted-foreground">
        {t('resources.mcp.discoveryEmpty')}
      </div>
    {:else if discoveryResults.length > 0}
      <div class="mt-3 grid gap-2 md:grid-cols-2">
        {#each discoveryResults as result (result.id)}
          <button
            type="button"
            aria-label={t('resources.mcp.selectDiscoveryResult', { name: result.name })}
            class="rounded-md border border-border bg-background p-3 text-left transition hover:border-brand"
            class:border-brand={selectedDiscovery?.id === result.id}
            onclick={() => selectDiscovery(result)}
          >
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="truncate text-sm font-semibold text-foreground">{result.name}</div>
                <div class="mt-1 text-xs text-muted-foreground">{result.description}</div>
              </div>
              <span class="rounded-md border border-border px-2 py-1 text-xs text-muted-foreground">{result.version}</span>
            </div>
            <div class="mt-3 grid gap-2 text-xs text-muted-foreground">
              <div>
                <span class="font-medium text-foreground">{t('resources.mcp.packages')}:</span>
                {result.packageRefs.join(', ') || t('workspace.notProvided')}
              </div>
              <div>
                <span class="font-medium text-foreground">{t('resources.mcp.tools')}:</span>
                {result.tools.join(', ') || t('workspace.notProvided')}
              </div>
              <div>
                <span class="font-medium text-foreground">{t('resources.mcp.secrets')}:</span>
                {result.secretEnvNames.join(', ') || t('workspace.notProvided')}
              </div>
            </div>
          </button>
        {/each}
      </div>
    {/if}
  </div>
</section>

{#snippet textField(label: TranslationKey, value: string, setValue: (value: string) => void, type = 'text')}
  <label class="flex min-h-9 overflow-hidden rounded-md border border-input bg-input-background">
    <span class="grid w-32 shrink-0 place-items-center border-r border-border bg-muted px-3 text-xs font-medium text-muted-foreground">
      {t(label)}
    </span>
    <Input {type} ariaLabel={t(label)} value={value} oninput={(event) => setValue(event.currentTarget.value)} class="h-9 rounded-none border-0 bg-transparent shadow-none focus-visible:ring-0" />
  </label>
{/snippet}

{#snippet textareaField(label: TranslationKey, value: string, setValue: (value: string) => void, size: 'normal' | 'small' = 'normal')}
  <label class={`flex ${size === 'small' ? 'min-h-20' : 'min-h-32'} overflow-hidden rounded-md border border-input bg-input-background`}>
    <span class="grid w-32 shrink-0 place-items-center border-r border-border bg-muted px-3 text-xs font-medium text-muted-foreground">
      {t(label)}
    </span>
    <textarea
      value={value}
      oninput={(event) => setValue(event.currentTarget.value)}
      class={`${size === 'small' ? 'min-h-20' : 'min-h-32'} flex-1 resize-y border-0 bg-transparent px-3 py-2 font-mono text-xs leading-5 shadow-none outline-none focus-visible:ring-0`}
    ></textarea>
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
