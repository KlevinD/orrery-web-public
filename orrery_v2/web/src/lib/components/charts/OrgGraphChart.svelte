<script lang="ts">
  import type { EChartsCoreOption } from 'echarts/core'
  import ObsidianForceGraph, {
    type ObsidianGraphLink,
    type ObsidianGraphNode
  } from '#lib/components/graph/ObsidianForceGraph.svelte'

  interface Props {
    option?: EChartsCoreOption
    height?: number
    onselect?: (id: string) => void
  }

  const defaultOption = {
    series: [{
      type: 'graph',
      data: [{ id: 'ops', name: 'Ops', category: 'organization', value: 4 }, { id: 'agents', name: 'Agents', category: 'person' }, { id: 'review', name: 'Review', category: 'task' }],
      links: [{ source: 'ops', target: 'agents' }, { source: 'ops', target: 'review' }]
    }]
  } as EChartsCoreOption

  let { option = defaultOption, height = 420, onselect }: Props = $props()
  const series = $derived(graphSeries(option))
  const nodes = $derived<ObsidianGraphNode[]>(series.data.map((node, index) => ({
    id: String(node.id ?? node.name ?? index),
    label: String(node.name ?? node.id ?? index),
    group: String(node.category ?? 'organization'),
    value: typeof node.value === 'number' ? node.value : 1,
    color: typeof node.itemStyle?.color === 'string' ? node.itemStyle.color : undefined,
    meta: node
  })))
  const links = $derived<ObsidianGraphLink[]>(series.links.map((link, index) => ({
    id: String(link.id ?? `${endpoint(link.source)}-${endpoint(link.target)}-${index}`),
    source: endpoint(link.source),
    target: endpoint(link.target),
    label: String(link.name ?? link.label?.formatter ?? ''),
    color: typeof link.lineStyle?.color === 'string' ? link.lineStyle.color : undefined,
    width: typeof link.lineStyle?.width === 'number' ? link.lineStyle.width : undefined
  })))

  type GraphDatum = Record<string, any>

  function graphSeries(value: EChartsCoreOption) {
    const raw = value as { series?: GraphDatum | GraphDatum[] }
    const candidates = Array.isArray(raw.series) ? raw.series : raw.series ? [raw.series] : []
    const graph = candidates.find((item) => item.type === 'graph') ?? candidates[0] ?? {}
    return {
      data: Array.isArray(graph.data) ? graph.data as GraphDatum[] : [],
      links: Array.isArray(graph.links) ? graph.links as GraphDatum[] : Array.isArray(graph.edges) ? graph.edges as GraphDatum[] : []
    }
  }

  function endpoint(value: unknown) {
    if (typeof value === 'object' && value !== null) {
      const record = value as GraphDatum
      return String(record.id ?? record.name ?? '')
    }
    return String(value ?? '')
  }
</script>

<div class="overflow-hidden border-y border-border bg-white">
  <ObsidianForceGraph {nodes} {links} {height} ariaLabel="Organization relationship graph" {onselect} />
</div>
