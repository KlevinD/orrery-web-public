<script lang="ts">
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { KnowledgeProviderDTO, KnowledgeProviderMode, KnowledgeProviderRequest } from '#lib/types/api/knowledge-provider'
  import { Database, LoaderCircle, Plus, RefreshCw, Wifi } from 'lucide-svelte'

  interface MCPServerOption {
    id: string
    name: string
    enabled: boolean
    exposedTools: string[]
  }

  interface Props {
    providers?: KnowledgeProviderDTO[]
    servers?: MCPServerOption[]
    loading?: boolean
    busy?: boolean
    activeAction?: string
    error?: string
    resultMessage?: string
    oncreate?: (request: KnowledgeProviderRequest) => void
    ontest?: (id: string) => void
    onsync?: (id: string, ownerId: string) => void
  }

  let {
    providers = [],
    servers = [],
    loading = false,
    busy = false,
    activeAction = '',
    error = '',
    resultMessage = '',
    oncreate,
    ontest,
    onsync
  }: Props = $props()

  let t = $derived(createTranslator($locale))
  let selectedServerID = $state('')
  let connectionName = $state('')
  let mode = $state<KnowledgeProviderMode>('read_only')
  let ownerID = $state('minvia')
  let includeText = $state('')
  let excludeText = $state('MinviaBrain')
  let serverOptions = $derived(servers.filter((server) => server.enabled).map((server) => ({ label: server.name, value: server.id })))
  let modeOptions = $derived([
    { label: t('settings.knowledgeMcpReadOnly'), value: 'read_only' },
    { label: t('settings.knowledgeMcpReadWrite'), value: 'read_write' }
  ])
  let selectedServer = $derived(servers.find((server) => server.id === selectedServerID))

  $effect(() => {
    if (!selectedServerID && serverOptions.length > 0) {
      selectedServerID = serverOptions[0].value
    }
  })

  function submit() {
    if (!selectedServerID || !connectionName.trim()) return
    oncreate?.({
      id: `kprovider-${selectedServerID.replace(/[^a-zA-Z0-9_-]+/g, '-')}`,
      name: connectionName.trim(),
      mcp_server_id: selectedServerID,
      mode,
      status: 'active',
      folder_include: splitRules(includeText),
      folder_exclude: splitRules(excludeText)
    })
  }

  function splitRules(value: string): string[] {
    return value.split(/[\n,]/).map((item) => item.trim()).filter(Boolean)
  }
</script>

<section class="border-t border-border pt-6">
  <div class="flex items-center gap-2">
    <Database class="h-4 w-4 text-muted-foreground" />
    <h2 class="text-sm font-semibold text-foreground">{t('settings.knowledgeMcpConnections')}</h2>
  </div>

  <div class="mt-4 grid gap-4 md:grid-cols-2">
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.knowledgeMcpServer')}</span>
      <Select bind:value={selectedServerID} options={serverOptions} disabled={busy || serverOptions.length === 0} />
    </label>
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.knowledgeMcpMode')}</span>
      <Select bind:value={mode} options={modeOptions} disabled={busy} />
    </label>
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.knowledgeMcpName')}</span>
      <Input ariaLabel={t('settings.knowledgeMcpName')} bind:value={connectionName} />
    </label>
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.knowledgeMcpOwner')}</span>
      <Input bind:value={ownerID} />
    </label>
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.knowledgeMcpInclude')}</span>
      <textarea class="min-h-20 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring" bind:value={includeText}></textarea>
    </label>
    <label class="space-y-1.5">
      <span class="text-xs font-medium text-muted-foreground">{t('settings.knowledgeMcpExclude')}</span>
      <textarea class="min-h-20 w-full resize-y rounded-md border border-border bg-input-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring" bind:value={excludeText}></textarea>
    </label>
  </div>

  {#if selectedServer?.exposedTools.length}
    <div class="mt-3 flex flex-wrap gap-1.5">
      {#each selectedServer.exposedTools as tool}
        <code class="rounded border border-border bg-muted/40 px-1.5 py-0.5 text-xs text-muted-foreground">{tool}</code>
      {/each}
    </div>
  {/if}

  <div class="mt-4 flex justify-end">
    <Button disabled={busy || !selectedServerID || !connectionName.trim()} onclick={submit} ariaLabel={t('settings.knowledgeMcpAdd')}>
      <Plus class="h-4 w-4" />
      {t('settings.knowledgeMcpAdd')}
    </Button>
  </div>

  {#if error}
    <p class="mt-3 text-sm text-destructive">{error}</p>
  {:else if resultMessage}
    <p class="mt-3 text-sm text-status-done">{resultMessage}</p>
  {/if}

  <div class="mt-6 divide-y divide-border border-y border-border">
    {#if loading}
      <div class="flex h-16 items-center justify-center"><LoaderCircle class="h-4 w-4 animate-spin text-muted-foreground" /></div>
    {:else if providers.length === 0}
      <p class="py-5 text-sm text-muted-foreground">{t('settings.knowledgeMcpEmpty')}</p>
    {:else}
      {#each providers as provider}
        <div class="flex flex-wrap items-center justify-between gap-3 py-4">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-medium text-foreground">{provider.name}</span>
              <Badge tone={provider.status === 'active' ? 'success' : provider.status === 'error' ? 'danger' : 'neutral'}>{provider.status}</Badge>
              <span class="text-xs text-muted-foreground">{provider.mode}</span>
            </div>
            <p class="mt-1 truncate text-xs text-muted-foreground">{provider.mcp_server_id}</p>
            {#if provider.last_error}<p class="mt-1 text-xs text-destructive">{provider.last_error}</p>{/if}
          </div>
          <div class="flex items-center gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={busy}
              onclick={() => ontest?.(provider.id)}
              ariaLabel={`${t('settings.knowledgeMcpTest')} ${provider.name}`}
              title={t('settings.knowledgeMcpTest')}
            >
              <Wifi class="h-4 w-4" />
              {t('settings.knowledgeMcpTest')}
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={busy}
              onclick={() => onsync?.(provider.id, ownerID.trim())}
              ariaLabel={`${t('settings.knowledgeMcpSync')} ${provider.name}`}
              title={t('settings.knowledgeMcpSync')}
            >
              <RefreshCw class={activeAction === `sync:${provider.id}` ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} />
              {t('settings.knowledgeMcpSync')}
            </Button>
          </div>
        </div>
      {/each}
    {/if}
  </div>
</section>
