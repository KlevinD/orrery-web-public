<script lang="ts">
  import {
    Server,
    Plus,
    Activity,
    RefreshCw,
    Ban,
    Trash2,
    Pencil,
    Link2,
    X,
    Wrench,
    MoreHorizontal
  } from 'lucide-svelte'
  import ResourceBindingPanel from '#lib/components/resources/ResourceBindingPanel.svelte'
  import McpServerDeleteConfirmDialog from '#lib/components/resources/McpServerDeleteConfirmDialog.svelte'
  import McpServerEditor from '#lib/components/resources/McpServerEditor.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import Tooltip from '#lib/components/ui/Tooltip.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { TranslationKey } from '#lib/i18n'
  import {
    combineMcpServerPages,
    useMcpCommandMutations,
    useMcpDiscoveryQuery,
    useMcpHealthMutation,
    useMcpServersInfiniteQuery,
    useMcpSyncMutation,
    useResourceBindMutation,
    useResourceUnbindMutation
  } from '#lib/queries/resources'
  import { mapMcpServerListView } from '#lib/mappers/resource'
  import type { ApiClientError } from '#lib/api/client'
  import type { MCPServerHealthDTO, MCPServerSyncDTO } from '#lib/types/api'
  import type {
    McpExposedToolViewModel,
    McpServerViewModel,
    ResourceBindingFormViewModel,
    ResourceMcpServerCommandFormViewModel
  } from '#lib/types/view-models'

  type McpCardStatusTone = 'success' | 'warning' | 'danger' | 'neutral'
  type McpCardStatus = {
    title: string
    details: string[]
    tone: McpCardStatusTone
  }
  type McpToolBadge = {
    name: string
    title: string
  }

  let search = $state('')
  let category = $state('')
  let riskLevel = $state('')
  let enabled = $state('')
  let resourceId = $state('')
  let discoverySearch = $state('')
  let selectedServer = $state<McpServerViewModel | undefined>()
  let pendingDeleteServer = $state<McpServerViewModel | undefined>()
  let showMcpEditorDialog = $state(false)
  let showMcpBindingDialog = $state(false)
  let busyId = $state('')
  let actionMessage = $state('')
  let cardStatusById = $state<Record<string, McpCardStatus>>({})
  let syncedToolsById = $state<Record<string, string[]>>({})
  let syncedToolDetailsById = $state<Record<string, McpExposedToolViewModel[]>>({})
  let actionsMenuOpen = $state(false)
  let loadMoreMarker = $state<HTMLElement | undefined>()
  let t = $derived(createTranslator($locale))

  const mcpQuery = useMcpServersInfiniteQuery(() => ({
    search,
    category,
    riskLevel,
    enabled: enabled === '' ? undefined : enabled,
    resourceId,
    includeBindings: true
  }))
  const discoveryQuery = useMcpDiscoveryQuery(() => ({
    query: discoverySearch,
    limit: 10
  }))
  const bindMutation = useResourceBindMutation()
  const unbindMutation = useResourceUnbindMutation()
  const healthMutation = useMcpHealthMutation()
  const syncMutation = useMcpSyncMutation()
  const mcpCommands = useMcpCommandMutations()
  const mcpData = $derived(mapMcpServerListView(combineMcpServerPages(mcpQuery.data?.pages ?? [])))
  const activeServer = $derived(selectedServer ?? mcpData.servers[0])
  const selectedId = $derived(activeServer?.id ?? '')
  const bindingBusy = $derived(bindMutation.isPending || unbindMutation.isPending)
  const commandBusy = $derived(mcpCommands.register.isPending || mcpCommands.update.isPending || mcpCommands.disable.isPending || mcpCommands.delete.isPending)

  function openRegisterMcpDialog() {
    actionMessage = ''
    selectedServer = undefined
    showMcpEditorDialog = true
  }

  function openEditMcpDialog(server?: McpServerViewModel) {
    actionMessage = ''
    if (server) selectedServer = server
    if (!selectedServer) return
    showMcpEditorDialog = true
  }

  function openMcpBindingDialog(server?: McpServerViewModel) {
    if (server) selectedServer = server
    if (!selectedServer) return
    showMcpBindingDialog = true
  }

  function bind(form: ResourceBindingFormViewModel) {
    actionMessage = ''
    bindMutation.mutate(form, {
      onSuccess: () => {
        actionMessage = t('resources.binding.bindSucceeded')
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('resources.binding.bindFailed')
      }
    })
  }

  function unbind(form: ResourceBindingFormViewModel) {
    actionMessage = ''
    unbindMutation.mutate(form, {
      onSuccess: () => {
        actionMessage = t('resources.binding.unbindSucceeded')
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('resources.binding.unbindFailed')
      }
    })
  }

  function health(server: McpServerViewModel) {
    busyId = server.id
    actionMessage = ''
    clearCardStatus(server.id)
    healthMutation.mutate(server.id, {
      onSuccess: (result) => {
        setCardStatus(server.id, formatHealthStatus(server.name, result))
        busyId = ''
      },
      onError: (error) => {
        setCardStatus(server.id, errorCardStatus(error, t('resources.mcp.healthFailed')))
        busyId = ''
      }
    })
  }

  function sync(server: McpServerViewModel) {
    busyId = server.id
    actionMessage = ''
    clearCardStatus(server.id)
    syncMutation.mutate(server.id, {
      onSuccess: (result) => {
        const syncedToolDetails = mapSyncedToolDetails(result.exposed_tool_details)
        syncedToolsById = {
          ...syncedToolsById,
          [server.id]: result.exposed_tools?.length ? result.exposed_tools : syncedToolDetails.map((tool) => tool.name)
        }
        syncedToolDetailsById = {
          ...syncedToolDetailsById,
          [server.id]: syncedToolDetails
        }
        setCardStatus(server.id, formatSyncStatus(server.name, result))
        busyId = ''
      },
      onError: (error) => {
        setCardStatus(server.id, errorCardStatus(error, t('resources.mcp.syncFailed')))
        busyId = ''
      }
    })
  }

  function disableServer(server: McpServerViewModel) {
    busyId = server.id
    actionMessage = ''
    mcpCommands.disable.mutate(server.id, {
      onSuccess: () => {
        actionMessage = t('resources.message.mcpDisabled')
        busyId = ''
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('resources.message.mcpDisableFailed')
        busyId = ''
      }
    })
  }

  function requestDeleteServer(server: McpServerViewModel) {
    selectedServer = server
    if (serverHasBindings(server)) {
      pendingDeleteServer = server
      return
    }
    deleteServer(server)
  }

  function confirmDeleteServer(server: McpServerViewModel) {
    pendingDeleteServer = undefined
    deleteServer(server)
  }

  function cancelDeleteServer() {
    pendingDeleteServer = undefined
  }

  function serverHasBindings(server: McpServerViewModel) {
    return server.boundAgentCount > 0 || server.bindings.length > 0
  }

  function deleteServer(server: McpServerViewModel) {
    busyId = server.id
    selectedServer = server
    actionMessage = ''
    mcpCommands.delete.mutate(server.id, {
      onSuccess: () => {
        actionMessage = t('resources.message.mcpDeleted')
        busyId = ''
        selectedServer = undefined
        pendingDeleteServer = undefined
        showMcpEditorDialog = false
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('resources.message.mcpDeleteFailed')
        busyId = ''
      }
    })
  }

  function saveServer(form: ResourceMcpServerCommandFormViewModel) {
    actionMessage = ''
    if (selectedServer && selectedServer.id === form.toolId) {
      mcpCommands.update.mutate(
        { toolId: selectedServer.id, form },
        {
          onSuccess: (result) => {
            actionMessage = mcpSaveSuccessMessage(result, 'resources.message.mcpUpdated')
          },
          onError: (error) => {
            actionMessage = error instanceof Error ? error.message : t('resources.message.mcpUpdateFailed')
          }
        }
      )
      return
    }
    mcpCommands.register.mutate(form, {
      onSuccess: (result) => {
        actionMessage = mcpSaveSuccessMessage(result, 'resources.message.mcpRegistered')
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('resources.message.mcpRegisterFailed')
      }
    })
  }

  function setCardStatus(serverId: string, status: McpCardStatus) {
    cardStatusById = { ...cardStatusById, [serverId]: status }
  }

  function clearCardStatus(serverId: string) {
    const next = { ...cardStatusById }
    delete next[serverId]
    cardStatusById = next
  }

  function formatHealthStatus(name: string, result: MCPServerHealthDTO): McpCardStatus {
    return {
      title: resourceMcpResultTitle(name, result.status, 'health'),
      details: resultDetails([
        mcpStatusLabel(result.status),
        result.message,
        result.failure_reason,
        result.latency_ms === undefined ? '' : `${t('resources.mcp.latency')}: ${result.latency_ms} ms`
      ]),
      tone: statusTone(result.status)
    }
  }

  function formatSyncStatus(name: string, result: MCPServerSyncDTO): McpCardStatus {
    return {
      title: resourceMcpResultTitle(name, result.status, 'sync'),
      details: resultDetails([
      mcpStatusLabel(result.status),
      result.failure_reason,
      result.exposed_tool_count === undefined ? '' : `${t('resources.mcp.exposedToolCount')}: ${result.exposed_tool_count}`,
      result.latency_ms === undefined ? '' : `${t('resources.mcp.latency')}: ${result.latency_ms} ms`
      ]),
      tone: statusTone(result.status)
    }
  }

  function errorCardStatus(error: unknown, fallbackMessage: string): McpCardStatus {
    return {
      title: fallbackMessage,
      details: [error instanceof Error ? error.message : fallbackMessage],
      tone: 'danger'
    }
  }

  function resourceMcpResultTitle(name: string, status: string | undefined, kind: 'health' | 'sync') {
    if (status === 'not_connected') {
      return t('resources.mcp.notConnected', { name })
    }
    return t(kind === 'health' ? 'resources.mcp.healthSucceeded' : 'resources.mcp.syncSucceeded', { name })
  }

  function resultDetails(details: Array<string | undefined>) {
    return details.map((detail) => detail?.trim()).filter(Boolean) as string[]
  }

  function statusTone(status: string | undefined): McpCardStatusTone {
    if (status === 'available' || status === 'synced') return 'success'
    if (status === 'not_connected' || status === 'disabled') return 'warning'
    if (status === 'unavailable' || status === 'failed') return 'danger'
    return 'neutral'
  }

  function mapSyncedToolDetails(details: MCPServerSyncDTO['exposed_tool_details']): McpExposedToolViewModel[] {
    return (details ?? [])
      .filter((tool) => Boolean(tool.name))
      .map((tool) => ({
        name: tool.name,
        description: tool.description ?? '',
        inputSchema: tool.input_schema,
        outputSchema: tool.output_schema
      }))
  }

  function toolBadgesFor(server: McpServerViewModel): McpToolBadge[] {
    const details = syncedToolDetailsById[server.id] ?? server.exposedToolDetails
    const detailsByName = new Map(details.map((tool) => [tool.name, tool]))
    const names = toolsFor(server)
    const visibleNames = names.length > 0 ? names : details.map((tool) => tool.name)
    return visibleNames.map((name) => {
      const detail = detailsByName.get(name)
      return { name, title: localizedToolDescription(detail?.description, name) }
    })
  }

  function toolsFor(server: McpServerViewModel) {
    return syncedToolsById[server.id] ?? server.exposedTools
  }

  function cardStatusClasses(tone: McpCardStatusTone) {
    if (tone === 'success') return 'border-status-done/30 bg-status-done/10 text-status-done'
    if (tone === 'warning') return 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300'
    if (tone === 'danger') return 'border-destructive/30 bg-destructive/10 text-destructive'
    return 'border-border bg-muted/60 text-muted-foreground'
  }

  function mcpSaveSuccessMessage(result: unknown, fallbackKey: 'resources.message.mcpRegistered' | 'resources.message.mcpUpdated') {
    if (mcpResultUnavailable(result)) return t('resources.message.mcpUnavailableSaved')
    return mcpResultNeedsAuthChoice(result) ? t('resources.message.mcpAuthRequired') : t(fallbackKey)
  }

  function mcpResultUnavailable(result: unknown) {
    return mcpResultServers(result).some((fields) => fields.availability_status === 'unavailable')
  }

  function mcpResultNeedsAuthChoice(result: unknown) {
    return mcpResultServers(result).some((fields) => fields.auth_required === true && fields.default_ak_configured !== true)
  }

  function mcpResultServers(result: unknown) {
    if (!result || typeof result !== 'object') return []
    const fields = result as { servers?: unknown[] }
    if (Array.isArray(fields.servers)) return fields.servers.filter(isRecord) as Array<Record<string, unknown>>
    return [result as Record<string, unknown>]
  }

  function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === 'object' && value !== null
  }

  function displayField(value: string) {
    return value === 'Not provided' || value === '' ? t('workspace.notProvided') : value
  }

  function mcpStatusLabel(status: string | undefined, enabled = true) {
    switch ((status ?? '').toLowerCase()) {
      case 'requires_auth':
        return t('resources.mcp.status.requiresAuth')
      case 'unavailable':
      case 'failed':
        return t('resources.mcp.status.unavailable')
      case 'available':
      case 'synced':
        return t('resources.mcp.status.available')
      case 'not_connected':
        return t('resources.mcp.status.notConnected')
      case 'disabled':
        return t('resources.mcp.status.disabled')
      default:
        return t(enabled ? 'resources.mcp.status.enabled' : 'resources.mcp.status.disabled')
    }
  }

  function riskLabel(value: string) {
    if (value === 'high') return t('risk.high')
    if (value === 'medium') return t('risk.medium')
    if (value === 'low') return t('risk.low')
    return displayField(value)
  }

  function statusDotClass(server: McpServerViewModel) {
    if (!server.enabled) return 'bg-muted-foreground/40'
    if (server.availabilityStatus === 'unavailable') return 'bg-destructive'
    if (server.availabilityStatus === 'requires_auth') return 'bg-amber-500'
    if (server.availabilityStatus === 'available') return 'bg-status-done'
    return 'bg-brand'
  }

  function statusTextClass(server: McpServerViewModel) {
    if (!server.enabled) return 'text-muted-foreground'
    if (server.availabilityStatus === 'unavailable') return 'text-destructive'
    if (server.availabilityStatus === 'requires_auth') return 'text-amber-600'
    if (server.availabilityStatus === 'available') return 'text-emerald-600'
    return 'text-brand'
  }

  function lastSyncLabel(server: McpServerViewModel) {
    return displayField(server.lastProbe?.checkedAt || server.updatedAt)
  }

  function transportLabel(value: string) {
    if (value === 'stdio') return t('resources.mcp.transport.stdio')
    if (value === 'sse') return t('resources.mcp.transport.sse')
    if (value === 'streamable_http') return t('resources.mcp.transport.streamableHttp')
    return displayField(value)
  }

  function healthPolicyLabel(value: string) {
    const text = displayField(value)
    const match = /^([a-z_]+)(?:\s*\(([^)]+)\))?$/i.exec(text)
    if (!match) return text
    const key = ({
      startup: 'resources.mcp.healthPolicy.startup',
      periodic: 'resources.mcp.healthPolicy.periodic',
      on_demand: 'resources.mcp.healthPolicy.onDemand',
      disabled: 'resources.mcp.healthPolicy.disabled'
    } as const satisfies Record<string, TranslationKey>)[match[1].toLowerCase()]
    if (!key) return text
    const timeout = match[2]?.replace(/^(\d+)s$/, $locale === 'zh-CN' ? '$1 秒' : '$1s')
    return timeout ? `${t(key)}（${timeout}）` : t(key)
  }

  function serverDescription(server: McpServerViewModel) {
    const description = displayField(server.description)
    if ($locale !== 'zh-CN' || /[\u3400-\u9fff]/.test(description) || description === t('workspace.notProvided')) {
      return description
    }
    return t('resources.mcp.descriptionFallback', { count: toolsFor(server).length })
  }

  function localizedToolDescription(description: string | undefined, fallback: string) {
    const text = description?.trim() || fallback
    if ($locale === 'zh-CN' && !/[\u3400-\u9fff]/.test(text)) return fallback
    return text
  }

  function loadMore() {
    if (mcpQuery.hasNextPage && !mcpQuery.isFetchingNextPage) {
      mcpQuery.fetchNextPage()
    }
  }

  $effect(() => {
    if (!loadMoreMarker || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) loadMore()
    }, { rootMargin: '240px' })
    observer.observe(loadMoreMarker)
    return () => observer.disconnect()
  })
</script>

<svelte:head>
  <title>{t('nav.mcp')} | Orrery</title>
</svelte:head>

<div class="page-shell max-w-[1040px]">
  <header class="page-header mb-4">
    <div>
      <h1 class="page-title">{t('nav.mcp')}</h1>
      <p class="page-description">{t('nav.mcp.description')}</p>
    </div>
    <Button onclick={openRegisterMcpDialog} ariaLabel={t('resources.mcp.registerServer')} class="h-9 shrink-0 rounded-lg !px-3 !text-[13px]">
      <Plus class="h-4 w-4" />
      {t('resources.mcp.registerServer')}
    </Button>
  </header>

  {#if mcpQuery.isLoading}
    <LoadingState title={t('state.loadingMcpServers')} description={t('state.loadingMcpServersDescription')} />
  {:else if mcpQuery.isError}
    {@const error = mcpQuery.error as ApiClientError}
    <ErrorState title={t('state.errorMcpServers')} message={error.message} correlationId={error.correlationId} />
  {:else if mcpQuery.data}
    {#if actionMessage}
      <div class="mb-3 rounded-lg border border-brand/25 bg-brand/10 px-4 py-2.5 text-sm text-brand">
        {actionMessage}
      </div>
    {/if}

    {#if mcpData.servers.length === 0}
      <EmptyState title={t('resources.mcp.empty')} description={t('resources.mcp.empty.description')} />
    {:else}
      <section class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_300px]">
        <div class="min-h-[450px] overflow-hidden rounded-xl border border-border bg-card">
          <div class="grid grid-cols-[minmax(0,1fr)_76px] gap-3 border-b border-border/70 px-4 py-2 text-[11px] font-medium text-muted-foreground sm:grid-cols-[minmax(0,1fr)_90px_60px_60px]">
            <span>{t('resources.mcp.server')}</span>
            <span>{t('resources.editor.status')}</span>
            <span class="hidden text-right sm:block">{t('resources.mcp.tools')}</span>
            <span class="hidden text-right sm:block">{t('resources.filter.bound')}</span>
          </div>
          <div class="divide-y divide-border/60">
            {#each mcpData.servers as server (server.id)}
              {@const visibleTools = toolsFor(server)}
              <button
                type="button"
                class="grid w-full grid-cols-[minmax(0,1fr)_76px] items-center gap-3 px-4 py-3 text-left transition-colors sm:grid-cols-[minmax(0,1fr)_90px_60px_60px] {selectedId === server.id ? 'selection-wash' : 'hover:bg-muted/35'}"
                onclick={() => {
                  selectedServer = server
                  actionsMenuOpen = false
                }}
              >
                <span class="flex min-w-0 items-center gap-2.5">
                  <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground">
                    <Server class="h-4 w-4" />
                  </span>
                  <span class="min-w-0">
                    <span class="block truncate text-[13px] text-foreground">{server.name}</span>
                    <span class="block truncate font-mono text-[11px] text-muted-foreground">{displayField(server.endpoint)}</span>
                  </span>
                </span>
                <span class="flex items-center gap-1.5 text-[12.5px] {statusTextClass(server)}">
                  <span class="h-2 w-2 shrink-0 rounded-full {statusDotClass(server)}"></span>
                  <span class="truncate">{mcpStatusLabel(server.availabilityStatus, server.enabled)}</span>
                </span>
                <span class="hidden text-right font-mono text-xs text-muted-foreground sm:block">{visibleTools.length}</span>
                <span class="hidden text-right font-mono text-xs text-muted-foreground sm:block">{server.boundAgentCount}</span>
              </button>
            {/each}
          </div>
        </div>

        {#if activeServer}
          {@const activeTools = toolsFor(activeServer)}
          {@const activeCardStatus = cardStatusById[activeServer.id]}
          <aside class="space-y-3 self-start">
            <div class="rounded-xl border border-border bg-card p-4">
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0">
                  <h2 class="truncate text-[15px] font-medium text-foreground">{activeServer.name}</h2>
                  <p class="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">{displayField(activeServer.endpoint)}</p>
                </div>
                <div class="relative">
                  <Button variant="ghost" size="icon" class="!h-7 !w-7" ariaLabel={t('filter.action')} onclick={() => (actionsMenuOpen = !actionsMenuOpen)}>
                    <MoreHorizontal class="h-4 w-4" />
                  </Button>
                  {#if actionsMenuOpen}
                    <div class="absolute right-0 top-8 z-20 w-40 overflow-hidden rounded-lg border border-border bg-card py-1 shadow-lg">
                      <a href="/resources/mcp/{encodeURIComponent(activeServer.id)}" class="flex items-center gap-2 px-3 py-2 text-xs text-foreground hover:bg-muted">
                        <Wrench class="h-3.5 w-3.5" />{t('resources.mcp.detail')}
                      </a>
                      <button type="button" class="flex w-full items-center gap-2 px-3 py-2 text-xs text-foreground hover:bg-muted" onclick={() => { actionsMenuOpen = false; openEditMcpDialog(activeServer) }}>
                        <Pencil class="h-3.5 w-3.5" />{t('resources.mcp.editSelected')}
                      </button>
                      <button type="button" class="flex w-full items-center gap-2 px-3 py-2 text-xs text-foreground hover:bg-muted" onclick={() => { actionsMenuOpen = false; openMcpBindingDialog(activeServer) }}>
                        <Link2 class="h-3.5 w-3.5" />{t('resources.mcp.bindSelected')}
                      </button>
                      <button type="button" class="flex w-full items-center gap-2 px-3 py-2 text-xs text-foreground hover:bg-muted" disabled={busyId === activeServer.id} onclick={() => { actionsMenuOpen = false; health(activeServer) }}>
                        <Activity class="h-3.5 w-3.5" />{t('resources.mcp.health')}
                      </button>
                      <button type="button" class="flex w-full items-center gap-2 px-3 py-2 text-xs text-foreground hover:bg-muted disabled:opacity-50" disabled={!activeServer.enabled || busyId === activeServer.id} onclick={() => { actionsMenuOpen = false; disableServer(activeServer) }}>
                        <Ban class="h-3.5 w-3.5" />{t('resources.action.disable')}
                      </button>
                      <button type="button" class="flex w-full items-center gap-2 px-3 py-2 text-xs text-destructive hover:bg-destructive/10 disabled:opacity-50" disabled={busyId === activeServer.id} onclick={() => { actionsMenuOpen = false; requestDeleteServer(activeServer) }}>
                        <Trash2 class="h-3.5 w-3.5" />{t('resources.action.delete')}
                      </button>
                    </div>
                  {/if}
                </div>
              </div>
              <div class="mt-2 flex items-center gap-1.5 text-[12.5px] {statusTextClass(activeServer)}">
                <span class="h-2 w-2 rounded-full {statusDotClass(activeServer)}"></span>
                {mcpStatusLabel(activeServer.availabilityStatus, activeServer.enabled)}
                <span class="text-muted-foreground">&middot; {t('resources.mcp.lastSync')} {lastSyncLabel(activeServer)}</span>
              </div>
              <Button variant="outline" class="mt-3 h-9 w-full rounded-lg !text-[13px]" disabled={busyId === activeServer.id} onclick={() => sync(activeServer)}>
                <RefreshCw class="h-3.5 w-3.5" />{t('resources.mcp.syncAndHealth')}
              </Button>
              {#if activeCardStatus}
                <div class="mt-3 rounded-md border px-3 py-2 text-xs {cardStatusClasses(activeCardStatus.tone)}">
                  <div class="font-medium">{activeCardStatus.title}</div>
                  {#if activeCardStatus.details.length > 0}
                    <div class="mt-1 text-[11px] opacity-85">{activeCardStatus.details.join(' / ')}</div>
                  {/if}
                </div>
              {/if}
            </div>

            <div class="overflow-hidden rounded-xl border border-border bg-card">
              <div class="flex items-center gap-2 border-b border-border/70 px-4 py-3 text-[13px] font-medium text-foreground">
                <Wrench class="h-4 w-4 text-muted-foreground" />{t('resources.mcp.exposedTools')} {activeTools.length}
              </div>
              {#if activeTools.length === 0}
                <p class="px-4 py-6 text-center text-xs text-muted-foreground">{t('resources.mcp.toolsEmpty')}</p>
              {:else}
                <div class="divide-y divide-border/60">
                  {#each activeTools as toolName}
                    <div class="flex items-center justify-between gap-3 px-4 py-2.5 text-[12.5px]">
                      <span class="min-w-0 truncate font-mono text-foreground" title={toolName}>{toolName}</span>
                      <span class="shrink-0 text-muted-foreground">{t('resources.mcp.toolReady')}</span>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          </aside>
        {/if}
      </section>

      <div bind:this={loadMoreMarker} class="h-px"></div>
      {#if mcpQuery.isFetchingNextPage}
        <div class="py-4 text-center text-xs text-muted-foreground">{t('state.loadingMcpServers')}</div>
      {/if}
    {/if}

    <!-- MCP editor dialog -->
    <Dialog open={showMcpEditorDialog} title={selectedServer ? selectedServer.name : t('resources.mcp.registerOrDiscover')} size="wide">
      {#if actionMessage}
        <div class="mb-4 rounded-lg border border-brand/25 bg-brand/10 px-4 py-2.5 text-sm text-brand">
          {actionMessage}
        </div>
      {/if}
      <McpServerEditor
        server={selectedServer}
        busy={commandBusy}
        discoveryQuery={discoverySearch}
        discoveryResults={discoveryQuery.data?.servers ?? []}
        discoveryLoading={discoveryQuery.isFetching}
        discoveryError={discoveryQuery.isError && discoveryQuery.error instanceof Error ? discoveryQuery.error.message : ''}
        ondiscoveryquery={(value) => (discoverySearch = value)}
        onsubmit={saveServer}
        ondelete={requestDeleteServer}
        onnew={() => (selectedServer = undefined)}
      />
      <div class="mt-4 flex justify-end">
        <Tooltip text={t('organization.cancel')}>
          <Button variant="outline" size="icon" ariaLabel={t('organization.cancel')} onclick={() => (showMcpEditorDialog = false)}>
            <X class="h-4 w-4" />
          </Button>
        </Tooltip>
      </div>
    </Dialog>

    <!-- Binding dialog -->
    <Dialog open={showMcpBindingDialog} title={selectedServer ? selectedServer.name : t('resources.binding.title')} size="wide">
      <ResourceBindingPanel
        resource={selectedServer}
        relatedResources={mcpData.servers}
        resourceType="mcp_server"
        busy={bindingBusy}
        message={actionMessage}
        onbind={bind}
        onunbind={unbind}
      />
      <div class="mt-4 flex justify-end">
        <Tooltip text={t('organization.cancel')}>
          <Button variant="outline" size="icon" ariaLabel={t('organization.cancel')} onclick={() => (showMcpBindingDialog = false)}>
            <X class="h-4 w-4" />
          </Button>
        </Tooltip>
      </div>
    </Dialog>

    <McpServerDeleteConfirmDialog
      open={pendingDeleteServer !== undefined}
      server={pendingDeleteServer}
      busy={commandBusy}
      onconfirm={confirmDeleteServer}
      oncancel={cancelDeleteServer}
    />
  {/if}
</div>
