<script lang="ts">
  import { Box, Link2, Radio, Tag, Workflow, Zap } from 'lucide-svelte'
  import type { SchemaDefinitionKind, SchemaGraphDTO } from '#lib/api/ontology-schema'
  import ObsidianForceGraph, {
    type ObsidianGraphLink,
    type ObsidianGraphNode
  } from '#lib/components/graph/ObsidianForceGraph.svelte'
  import { definitionKindLabel, schemaRelationLabel } from '#lib/ontology-workbench/display-labels'

  interface Props {
    graph: SchemaGraphDTO
    tenantId?: string
    selectedId?: string
    selectedKind?: SchemaDefinitionKind
    selectedTenantId?: string
    onselect?: (kind: SchemaDefinitionKind, id: string, tenantId: string) => void
  }

  let { graph, tenantId = '', selectedId = '', selectedKind, selectedTenantId = '', onselect }: Props = $props()
  const nodes = $derived([...graph.nodes].sort((a, b) => a.kind.localeCompare(b.kind) || a.label.localeCompare(b.label) || a.id.localeCompare(b.id)))
  const visibleKinds = $derived([...new Set(nodes.map((node) => node.kind))].sort())
  const hasDisabled = $derived(nodes.some((node) => node.status === 'disabled'))
  const forceNodes = $derived<ObsidianGraphNode[]>(nodes.map((node) => ({
    id: node.id,
    label: node.label,
    group: node.kind,
    value: 1 + graph.edges.filter((edge) => edge.source_id === node.id || edge.target_id === node.id).length,
    disabled: node.status === 'disabled',
    muted: node.matched === false,
    meta: { kind: node.kind, origin: node.origin, status: node.status }
  })))
  const forceLinks = $derived<ObsidianGraphLink[]>(graph.edges.map((edge) => ({
    id: edge.id,
    source: edge.source_id,
    target: edge.target_id,
    label: schemaRelationLabel(edge.relation)
  })))
  const selectedGraphId = $derived(selectedKind && selectedId ? graph.nodes.find((node) =>
    node.kind === selectedKind && definitionId(node) === selectedId && sourceTenantId(node) === selectedTenantId
  )?.id ?? '' : '')

  function selectable(kind: string): kind is SchemaDefinitionKind {
    return kind !== 'event'
  }

  function definitionId(node: SchemaGraphDTO['nodes'][number]) {
    if (node.definition_id) return node.definition_id
    const prefix = `${node.kind}:`
    return node.id.startsWith(prefix) ? node.id.slice(prefix.length) : node.id
  }

  function sourceTenantId(node: SchemaGraphDTO['nodes'][number]) {
    return node.tenant_id ?? tenantId
  }

  function selected(node: SchemaGraphDTO['nodes'][number]) {
    return selectedKind === node.kind && selectedId === definitionId(node) && selectedTenantId === sourceTenantId(node)
  }

  function selectGraphNode(graphNodeId: string) {
    const node = graph.nodes.find((candidate) => candidate.id === graphNodeId)
    if (node && selectable(node.kind)) onselect?.(node.kind, definitionId(node), sourceTenantId(node))
  }
</script>

<div class="space-y-3">
  <div class="text-xs text-muted-foreground" role="status" aria-live="polite" aria-atomic="true">
    <span>{graph.visible_nodes} 个节点 · {graph.visible_edges} 条有向关系</span>
    {#if graph.truncated}<span class="ml-3 font-medium text-foreground">结果已截断，请收窄筛选</span>{/if}
  </div>

  <div class="flex flex-wrap gap-x-5 gap-y-2 border-y border-border py-2 text-xs text-muted-foreground" aria-label="Schema graph legend">
    {#each visibleKinds as kind}
      <span class="inline-flex items-center gap-1.5">
        {#if kind === 'object'}<Box class="size-3.5" aria-hidden="true" />
        {:else if kind === 'link'}<Link2 class="size-3.5" aria-hidden="true" />
        {:else if kind === 'property'}<Tag class="size-3.5" aria-hidden="true" />
        {:else if kind === 'mapping'}<Workflow class="size-3.5" aria-hidden="true" />
        {:else if kind === 'action'}<Zap class="size-3.5" aria-hidden="true" />
        {:else}<Radio class="size-3.5" aria-hidden="true" />{/if}
        {definitionKindLabel(kind)}
      </span>
    {/each}
    {#if hasDisabled}<span class="inline-flex items-center gap-2"><span class="size-2.5 rounded-full bg-[#aeb4bd]"></span>已禁用</span>{/if}
    <span class="inline-flex items-center gap-1"><span aria-hidden="true">→</span>依赖方向</span>
  </div>

  <div class="border-y border-border bg-white">
    <ObsidianForceGraph
      nodes={forceNodes}
      links={forceLinks}
      selectedId={selectedGraphId}
      ariaLabel={graph.view === 'semantic' ? '本体模型关系图' : '本体数据映射图'}
      height={520}
      onselect={selectGraphNode}
    />
  </div>

  <details>
    <summary class="cursor-pointer text-sm font-medium">键盘可访问节点与关系列表</summary>
    <ul class="mt-2 grid gap-1 sm:grid-cols-2">{#each nodes as node (node.id)}<li>{#if selectable(node.kind)}<button class="w-full px-2 py-2 text-left text-sm hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-pressed={selected(node)} aria-label={`${definitionKindLabel(node.kind)}: ${node.label}`} onclick={() => onselect?.(node.kind as SchemaDefinitionKind, definitionId(node), sourceTenantId(node))}><span class="font-medium">{node.label}</span><span class="ml-2 text-xs text-muted-foreground">{definitionKindLabel(node.kind)}</span></button>{:else}<span class="block px-2 py-2 text-sm"><span class="font-medium">{node.label}</span><span class="ml-2 text-xs text-muted-foreground">{definitionKindLabel(node.kind)}</span></span>{/if}</li>{/each}</ul>
    <ul class="mt-2 divide-y divide-border border-y border-border text-sm">{#each graph.edges as edge (edge.id)}<li class="py-2"><span class="font-mono text-xs">{edge.source_id}</span><span class="mx-2 text-muted-foreground">{schemaRelationLabel(edge.relation)} →</span><span class="font-mono text-xs">{edge.target_id}</span></li>{/each}</ul>
  </details>
</div>
