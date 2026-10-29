<script lang="ts">
  import { ArrowLeft } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Tooltip from '#lib/components/ui/Tooltip.svelte'
  import McpServerDetailPanel from '#lib/components/resources/McpServerDetailPanel.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { useMcpServerQuery } from '#lib/queries/resources'
  import type { ApiClientError } from '#lib/api/client'

  interface Props {
    data: { toolId: string }
  }

  let { data }: Props = $props()
  const toolId = $derived(data.toolId)
  const mcpQuery = useMcpServerQuery(() => toolId)
  let t = $derived(createTranslator($locale))
</script>

<svelte:head>
  <title>{toolId} | {t('nav.mcp')} | Orrery</title>
</svelte:head>

<div class="page-shell max-w-[1040px] space-y-5">
  <header class="flex flex-wrap items-center justify-between gap-3">
    <div>
      <h1 class="page-title">{t('resources.mcp.detail')}</h1>
      <p class="mt-1 text-sm text-muted-foreground">{toolId}</p>
    </div>
    <Tooltip text={t('resources.mcp.backToList')}>
      <Button href="/resources/mcp" variant="outline" size="icon" ariaLabel={t('resources.mcp.backToList')}>
        <ArrowLeft class="h-4 w-4" />
      </Button>
    </Tooltip>
  </header>

  {#if mcpQuery.isLoading}
    <LoadingState title={t('state.loadingMcpServers')} description={t('state.loadingMcpServersDescription')} />
  {:else if mcpQuery.isError}
    {@const error = mcpQuery.error as ApiClientError}
    <ErrorState title={t('state.errorMcpServers')} message={error.message} correlationId={error.correlationId} />
  {:else if mcpQuery.data}
    <McpServerDetailPanel server={mcpQuery.data} />
  {/if}
</div>
