<script lang="ts">
  import Badge from '#lib/components/ui/Badge.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import type { McpExposedToolViewModel, McpServerViewModel } from '#lib/types/view-models'

  interface Props {
    server: McpServerViewModel
  }

  let { server }: Props = $props()
  let t = $derived(createTranslator($locale))
  const tools = $derived(toolDetailsFor(server))

  function toolDetailsFor(current: McpServerViewModel): McpExposedToolViewModel[] {
    const byName = new Map(current.exposedToolDetails.map((tool) => [tool.name, tool]))
    const names = current.exposedTools.length > 0 ? current.exposedTools : current.exposedToolDetails.map((tool) => tool.name)
    return names.map((name) => byName.get(name) ?? {
      name,
      description: '',
      inputSchema: undefined,
      outputSchema: undefined
    })
  }

  function displayField(value: string) {
    return value === 'Not provided' || value === '' ? t('workspace.notProvided') : value
  }

  function schemaText(schema: Record<string, unknown> | undefined) {
    if (!schema || Object.keys(schema).length === 0) return t('workspace.notProvided')
    return JSON.stringify(schema, null, 2)
  }
</script>

<section class="space-y-5">
  <div class="rounded-xl border border-border bg-card p-5">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <h1 class="truncate text-[22px] font-semibold leading-[33px] text-foreground">{server.name}</h1>
        <p class="mt-1 text-[13px] text-muted-foreground">{displayField(server.description)}</p>
      </div>
      <div class="flex flex-wrap gap-1.5">
        <Badge tone={server.statusTone}>{server.statusLabel}</Badge>
        <Badge tone={server.riskTone}>{server.riskLevel}</Badge>
      </div>
    </div>

    <div class="mt-5 grid gap-3 text-sm md:grid-cols-2 xl:grid-cols-4">
      {@render detailField('filter.resourceId', server.id)}
      {@render detailField('resources.mcp.server', server.serverName)}
      {@render detailField('resources.mcp.transport', server.transport)}
      {@render detailField('resources.mcp.endpoint', server.endpoint)}
      {@render detailField('resources.mcp.command', server.command)}
      {@render detailField('resources.mcp.args', server.args.join(', '))}
      {@render detailField('resources.skills.version', server.version)}
      {@render detailField('resources.mcp.healthPolicy', server.healthPolicy)}
    </div>
  </div>

  <div class="rounded-xl border border-border bg-card p-5">
    <div class="flex items-center justify-between gap-3">
      <h2 class="text-base font-semibold text-foreground">{t('resources.mcp.tools')}</h2>
      <Badge>{tools.length}</Badge>
    </div>

    {#if tools.length === 0}
      <div class="mt-4">
        <EmptyState title={t('resources.mcp.toolsEmpty')} description={t('resources.mcp.toolsEmpty.description')} />
      </div>
    {:else}
      <div class="mt-4 space-y-3">
        {#each tools as tool (tool.name)}
          <article class="rounded-md border border-border bg-background p-4">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0">
                <h3 class="font-mono text-sm font-semibold text-foreground">{tool.name}</h3>
                <p class="mt-1 text-sm text-muted-foreground">{displayField(tool.description)}</p>
              </div>
            </div>
            <div class="mt-4 grid gap-3 lg:grid-cols-2">
              {@render schemaBlock('resources.mcp.inputSchema', tool.inputSchema)}
              {@render schemaBlock('resources.mcp.outputSchema', tool.outputSchema)}
            </div>
          </article>
        {/each}
      </div>
    {/if}
  </div>
</section>

{#snippet detailField(label: TranslationKey, value: string)}
  <div class="min-w-0 rounded-md border border-border bg-background px-3 py-2">
    <div class="text-xs text-muted-foreground">{t(label)}</div>
    <div class="mt-1 truncate font-medium text-foreground">{displayField(value)}</div>
  </div>
{/snippet}

{#snippet schemaBlock(label: TranslationKey, schema: Record<string, unknown> | undefined)}
  <div class="min-w-0">
    <div class="mb-1.5 text-xs font-medium text-muted-foreground">{t(label)}</div>
    <pre class="max-h-80 overflow-auto rounded-md border border-border bg-muted p-3 text-xs leading-relaxed text-foreground">{schemaText(schema)}</pre>
  </div>
{/snippet}
