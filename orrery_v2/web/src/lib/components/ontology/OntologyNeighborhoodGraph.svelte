<script lang="ts">
  import { Maximize2, Minimize2 } from 'lucide-svelte'
  import type { ContextNeighborhoodDTO } from '#lib/api/ontology-context'
  import ObsidianForceGraph, {
    type ObsidianGraphLink,
    type ObsidianGraphNode
  } from '#lib/components/graph/ObsidianForceGraph.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Tooltip from '#lib/components/ui/Tooltip.svelte'
  import { objectTypeLabel } from '#lib/ontology-workbench/display-labels'

  interface Props { graph: ContextNeighborhoodDTO; selectedId?: string; onselect?: (id: string) => void }
  let { graph, selectedId = '', onselect }: Props = $props()
  let expanded = $state(false)
  const hasVisibleSelection = $derived(graph.nodes.some((node) => node.id === selectedId))
  const forceNodes = $derived<ObsidianGraphNode[]>(graph.nodes.map((node) => ({
    id: node.id,
    label: node.label,
    group: node.object_type,
    color: node.id === graph.root_id ? '#353b45' : undefined,
    value: Math.max(1, node.degree),
    meta: { objectType: node.object_type, degree: node.degree }
  })))
  const forceLinks = $derived<ObsidianGraphLink[]>(graph.edges.map((edge) => ({
    id: edge.id,
    source: edge.source_id,
    target: edge.target_id,
    label: edge.link_type_label || edge.link_type
  })))

  function handleWindowKeydown(event: KeyboardEvent) {
    if (expanded && event.key === 'Escape') expanded = false
  }
</script>

<svelte:window onkeydown={handleWindowKeydown} />

<div
  class="space-y-3 bg-background"
  class:fixed={expanded}
  class:inset-3={expanded}
  class:z-50={expanded}
  class:overflow-hidden={expanded}
  class:border={expanded}
  class:border-border={expanded}
  class:p-4={expanded}
  class:shadow-xl={expanded}
  role="region"
  aria-label="Object neighborhood explorer"
>
  <div class="flex flex-wrap items-center justify-between gap-3">
    <div class="text-xs text-muted-foreground" role="status" aria-live="polite" aria-atomic="true">
      <span>{graph.visible_nodes} 个对象 · {graph.visible_edges} 条有向关系 · 深度 {graph.applied_depth}</span>
      {#if graph.truncated}<span class="ml-3 font-medium text-foreground">邻域已截断</span>{/if}
    </div>
    <Tooltip text={expanded ? '退出全屏' : '全屏查看'}><Button size="icon" variant="outline" ariaLabel={expanded ? '退出图谱全屏' : '全屏查看图谱'} title={expanded ? '退出全屏' : '全屏查看'} onclick={() => (expanded = !expanded)}>{#if expanded}<Minimize2 class="size-4" aria-hidden="true" />{:else}<Maximize2 class="size-4" aria-hidden="true" />{/if}</Button></Tooltip>
  </div>

  <div class="flex flex-wrap gap-x-5 gap-y-2 border-y border-border py-2 text-xs text-muted-foreground" aria-label="Graph legend">
    <span class="inline-flex items-center gap-2"><span class="size-3 rounded-full bg-[#353b45]"></span>根对象</span>
    {#if hasVisibleSelection}<span class="inline-flex items-center gap-2"><span class="size-3 rounded-full border-2 border-foreground bg-white"></span>当前选择</span>{/if}
    <span class="inline-flex items-center gap-2"><span class="h-px w-5 bg-border"></span><span aria-hidden="true">→</span>关系方向</span>
  </div>

  <div class="border-y border-border bg-white">
    <ObsidianForceGraph
      nodes={forceNodes}
      links={forceLinks}
      {selectedId}
      ariaLabel="实际数据关系图"
      height={expanded ? 680 : 500}
      {onselect}
    />
  </div>

  <details>
    <summary class="cursor-pointer text-sm font-medium">键盘可访问节点与关系列表</summary>
    <ul class="mt-2 grid gap-1 sm:grid-cols-2">{#each graph.nodes as node (node.id)}<li><button class="w-full px-2 py-2 text-left text-sm hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-pressed={selectedId === node.id} aria-label={`${objectTypeLabel(node.object_type)}: ${node.label}`} onclick={() => onselect?.(node.id)}><span class="font-medium">{node.label}</span><span class="ml-2 text-xs text-muted-foreground">{objectTypeLabel(node.object_type)}</span></button></li>{/each}</ul>
    <ul class="mt-2 divide-y divide-border border-y border-border text-sm">{#each graph.edges as edge (edge.id)}<li class="py-2"><button class="font-mono text-xs hover:underline" onclick={() => onselect?.(edge.source_id)}>{edge.source_id}</button><span class="mx-2 text-muted-foreground">{edge.link_type_label || edge.link_type} →</span><button class="font-mono text-xs hover:underline" onclick={() => onselect?.(edge.target_id)}>{edge.target_id}</button>{#if edge.link_type_label && edge.link_type_label !== edge.link_type}<span class="ml-2 font-mono text-[11px] text-muted-foreground">{edge.link_type}</span>{/if}</li>{/each}</ul>
  </details>
</div>
