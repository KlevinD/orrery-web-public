<script lang="ts">
  import { ArrowUpRight, Network } from 'lucide-svelte'
  import ObsidianForceGraph, { type ObsidianGraphLink } from './ObsidianForceGraph.svelte'
  import type { ResourceGraphNode } from '#lib/graph/resourceRelationships'

  interface LegendItem {
    label: string
    color: string
  }

  interface Props {
    title: string
    description: string
    nodes: ResourceGraphNode[]
    links: ObsidianGraphLink[]
    legend: LegendItem[]
    ariaLabel: string
    emptyLabel: string
    openLabel: string
    selectHint: string
    nodeCountLabel: string
    relationCountLabel: string
    embedded?: boolean
    loading?: boolean
    loadingLabel?: string
    onopen?: (actionId: string) => void
  }

  let {
    title,
    description,
    nodes,
    links,
    legend,
    ariaLabel,
    emptyLabel,
    openLabel,
    selectHint,
    nodeCountLabel,
    relationCountLabel,
    embedded = false,
    loading = false,
    loadingLabel = '',
    onopen
  }: Props = $props()

  let selectedId = $state('')
  const selectedNode = $derived(nodes.find((node) => node.id === selectedId))

  $effect(() => {
    nodes
    if (selectedId && !nodes.some((node) => node.id === selectedId)) selectedId = ''
  })
</script>

<section class={embedded ? 'overflow-hidden bg-card' : 'overflow-hidden rounded-lg border border-border bg-card'} data-resource-relationship-graph>
  <header class="flex flex-col gap-3 border-b border-border px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
    <div class="min-w-0">
      <div class="flex items-center gap-2">
        <Network class="h-4 w-4 text-brand" />
        <h2 class="text-[13.5px] font-semibold text-foreground">{title}</h2>
      </div>
      <p class="mt-1 text-xs text-muted-foreground">{description}</p>
    </div>
    <div class="flex shrink-0 items-center gap-3 text-[11px] text-muted-foreground">
      <span>{nodeCountLabel}</span>
      <span>{relationCountLabel}</span>
    </div>
  </header>

  <div class="flex flex-wrap gap-x-4 gap-y-2 border-b border-border px-4 py-2.5" aria-label="Graph legend">
    {#each legend as item}
      <span class="inline-flex items-center gap-1.5 text-[11px] text-muted-foreground">
        <span class="h-2 w-2 rounded-full" style:background-color={item.color}></span>
        {item.label}
      </span>
    {/each}
  </div>

  {#if loading}
    <div class="grid min-h-[420px] place-items-center px-5 text-center text-sm text-muted-foreground">{loadingLabel}</div>
  {:else if nodes.length === 0}
    <div class="grid min-h-[420px] place-items-center px-5 text-center">
      <div>
        <Network class="mx-auto h-7 w-7 text-muted-foreground/40" />
        <p class="mt-3 text-sm text-muted-foreground">{emptyLabel}</p>
      </div>
    </div>
  {:else}
    <div class="grid lg:grid-cols-[minmax(0,1fr)_250px]">
      <div class="min-w-0 border-b border-border lg:border-b-0 lg:border-r">
        <ObsidianForceGraph
          {nodes}
          {links}
          {selectedId}
          height={440}
          {ariaLabel}
          onselect={(id) => (selectedId = id)}
        />
      </div>
      <aside class="min-h-[190px] bg-muted/10 p-4 lg:min-h-0" aria-live="polite">
        {#if selectedNode}
          <p class="text-[10.5px] font-medium uppercase text-brand">{selectedNode.meta.kindLabel}</p>
          <h3 class="mt-1 break-words text-sm font-semibold text-foreground">{selectedNode.label}</h3>
          {#if selectedNode.meta.description}
            <p class="mt-2 line-clamp-5 text-xs leading-5 text-muted-foreground">{selectedNode.meta.description}</p>
          {/if}
          {#if selectedNode.meta.details?.length}
            <dl class="mt-4 divide-y divide-border border-y border-border">
              {#each selectedNode.meta.details as detail}
                <div class="grid grid-cols-[72px_minmax(0,1fr)] gap-2 py-2 text-[11px]">
                  <dt class="text-muted-foreground">{detail.label}</dt>
                  <dd class="min-w-0 break-words text-right font-medium text-foreground">{detail.value}</dd>
                </div>
              {/each}
            </dl>
          {/if}
          {#if selectedNode.meta.href}
            <a class="mt-4 inline-flex h-8 items-center gap-1.5 rounded-md bg-brand px-3 text-[12px] font-medium text-brand-foreground hover:bg-brand-hover" href={selectedNode.meta.href}>
              {openLabel}
              <ArrowUpRight class="h-3.5 w-3.5" />
            </a>
          {:else if selectedNode.meta.actionId && onopen}
            <button class="mt-4 inline-flex h-8 items-center gap-1.5 rounded-md bg-brand px-3 text-[12px] font-medium text-brand-foreground hover:bg-brand-hover" type="button" onclick={() => onopen?.(selectedNode.meta.actionId as string)}>
              {openLabel}
              <ArrowUpRight class="h-3.5 w-3.5" />
            </button>
          {/if}
        {:else}
          <div class="grid h-full min-h-[150px] place-items-center text-center">
            <div>
              <Network class="mx-auto h-6 w-6 text-muted-foreground/35" />
              <p class="mt-2 text-xs leading-5 text-muted-foreground">{selectHint}</p>
            </div>
          </div>
        {/if}
      </aside>
    </div>
  {/if}
</section>
