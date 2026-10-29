<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { RefreshCw, Scan, ZoomIn, ZoomOut } from 'lucide-svelte'
  import { forceCollide, forceX, forceY } from 'd3-force'
  import type ForceGraph from 'force-graph'
  import type { LinkObject, NodeObject } from 'force-graph'

  export interface ObsidianGraphNode extends NodeObject {
    id: string
    label: string
    group?: string
    color?: string
    value?: number
    disabled?: boolean
    muted?: boolean
    meta?: Record<string, unknown>
  }

  export interface ObsidianGraphLink extends LinkObject<ObsidianGraphNode> {
    id: string
    source: string | ObsidianGraphNode
    target: string | ObsidianGraphNode
    label?: string
    color?: string
    width?: number
  }

  interface Props {
    nodes: ObsidianGraphNode[]
    links: ObsidianGraphLink[]
    selectedId?: string
    ariaLabel?: string
    height?: number
    controls?: boolean
    compact?: boolean
    onselect?: (id: string) => void
    onlinkselect?: (id: string) => void
  }

  let {
    nodes,
    links,
    selectedId = '',
    ariaLabel = 'Relationship graph',
    height = 420,
    controls = true,
    compact = false,
    onselect,
    onlinkselect
  }: Props = $props()

  let host: HTMLDivElement
  let graphInstance: ForceGraph<ObsidianGraphNode, ObsidianGraphLink> | undefined
  let resizeObserver: ResizeObserver | undefined
  let hoveredId = ''
  let adjacentIds = new Set<string>()
  let destroyed = false
  let fitted = false
  let fitTimers: Array<ReturnType<typeof setTimeout>> = []

  const palette: Record<string, string> = {
    object: '#4f83cc', link: '#5aa897', property: '#d3a24c', mapping: '#8f6db3', action: '#cf6679', event: '#7e8796',
    organization: '#4f83cc', department: '#58a6a6', position: '#d3a24c', person: '#cf6679', marketPerson: '#b47da8',
    memory: '#7e8796', memoryLayer: '#d3a24c', memorySource: '#a3a9b2', evidence: '#4f83cc', tool: '#8f6db3', skill: '#5aa897', mcpServer: '#4f83cc', mcpTool: '#8f6db3',
    knowledgeOwner: '#cf6679', knowledgeBase: '#74a65d', knowledgeDocument: '#9bbf82', todo: '#d98b5f', idea: '#d3a24c', scheduledTask: '#9a7cc1',
    mission: '#cf6679', task: '#d98b5f', approval: '#c45b70', discussion: '#5396bd', profileRef: '#8a94a3', runtimeTool: '#9a7cc1'
  }

  onMount(async () => {
    const { default: ForceGraphConstructor } = await import('force-graph')
    if (destroyed) return
    graphInstance = new ForceGraphConstructor<ObsidianGraphNode, ObsidianGraphLink>(host)
    configureGraph()
    if (typeof ResizeObserver !== 'undefined') {
      resizeObserver = new ResizeObserver(resizeGraph)
      resizeObserver.observe(host)
    }
    resizeGraph()
  })

  onDestroy(() => {
    destroyed = true
    clearFitTimers()
    resizeObserver?.disconnect()
    graphInstance?._destructor()
  })

  $effect(() => {
    nodes
    links
    if (graphInstance) updateGraphData()
  })

  $effect(() => {
    selectedId
    if (graphInstance) redraw()
  })

  function configureGraph() {
    if (!graphInstance) return
    graphInstance
      .nodeId('id')
      .backgroundColor('rgba(255,255,255,0)')
      .minZoom(0.25)
      .maxZoom(8)
      .d3VelocityDecay(0.32)
      .nodeLabel((node) => node.label)
      .linkLabel((link) => link.label ?? '')
      .nodeVal((node) => Math.max(1, node.value ?? 1))
      .nodeCanvasObject(drawNode)
      .nodePointerAreaPaint(drawNodePointerArea)
      .linkColor(linkColor)
      .linkWidth(linkWidth)
      .linkDirectionalArrowLength(0)
      .linkDirectionalArrowColor(linkColor)
      .onNodeHover((node) => setHoveredNode(node?.id ?? ''))
      .onNodeClick((node) => onselect?.(node.id))
      .onLinkClick((link) => onlinkselect?.(link.id))
      .onBackgroundClick(() => setHoveredNode(''))
      .onEngineStop(() => {
        if (!fitted) {
          fitted = true
          clearFitTimers()
          graphInstance?.zoomToFit(450, compact ? 16 : 38)
        }
      })

    const charge = graphInstance.d3Force('charge') as { strength?: (value: number) => unknown } | undefined
    charge?.strength?.(compact ? -24 : -42)
    const linkForce = graphInstance.d3Force('link') as { distance?: (value: number) => unknown } | undefined
    linkForce?.distance?.(compact ? 20 : 30)
    graphInstance.d3Force('x', forceX<ObsidianGraphNode>(0).strength(compact ? 0.045 : 0.028))
    graphInstance.d3Force('y', forceY<ObsidianGraphNode>(0).strength(compact ? 0.045 : 0.028))
    graphInstance.d3Force('collide', forceCollide<ObsidianGraphNode>((node) => nodeRadius(node) + 3).strength(0.7))
    updateGraphData()
  }

  function updateGraphData() {
    if (!graphInstance) return
    fitted = false
    const graphNodes = nodes.map((node) => ({ ...node }))
    const graphLinks = links.map((link) => ({ ...link }))
    graphInstance.graphData({ nodes: graphNodes, links: graphLinks })
    scheduleInitialFit()
    redraw()
  }

  function scheduleInitialFit() {
    clearFitTimers()
    fitTimers = [900, 2600].map((delay) => setTimeout(() => {
      if (!destroyed && !fitted) graphInstance?.zoomToFit(420, compact ? 16 : 38)
    }, delay))
  }

  function clearFitTimers() {
    for (const timer of fitTimers) clearTimeout(timer)
    fitTimers = []
  }

  function resizeGraph() {
    if (!graphInstance) return
    const width = Math.max(1, Math.round(host.clientWidth || host.getBoundingClientRect().width || 1))
    graphInstance.width(width).height(height)
  }

  function nodeRadius(node: ObsidianGraphNode) {
    return (compact ? 1.6 : 2) + Math.min(compact ? 4.8 : 7, Math.sqrt(Math.max(1, node.value ?? 1)) * (compact ? 1.25 : 1.1))
  }

  function nodeColor(node: ObsidianGraphNode, active: boolean) {
    if (node.disabled) return '#aeb4bd'
    if (node.muted && !active) return '#b8bdc5'
    return node.color ?? palette[node.group ?? ''] ?? '#6f7f91'
  }

  function endpointId(endpoint: string | ObsidianGraphNode) {
    return typeof endpoint === 'object' ? endpoint.id : endpoint
  }

  function isDimmedNode(node: ObsidianGraphNode) {
    return Boolean(hoveredId && node.id !== hoveredId && !adjacentIds.has(node.id))
  }

  function isDimmedLink(link: ObsidianGraphLink) {
    return Boolean(hoveredId && endpointId(link.source) !== hoveredId && endpointId(link.target) !== hoveredId)
  }

  function drawNode(node: ObsidianGraphNode, context: CanvasRenderingContext2D, globalScale: number) {
    if (node.x === undefined || node.y === undefined) return
    const radius = nodeRadius(node) / globalScale
    const selected = node.id === selectedId
    const hovered = node.id === hoveredId
    context.save()
    context.globalAlpha = isDimmedNode(node) ? 0.14 : node.disabled || node.muted && !selected && !hovered ? 0.52 : 0.95

    if (selected || hovered) {
      context.beginPath()
      context.arc(node.x, node.y, radius + (selected ? 3 : 2), 0, Math.PI * 2)
      context.strokeStyle = selected ? '#20252d' : '#ffffff'
      context.lineWidth = (selected ? 1.6 : 2.2) / globalScale
      context.stroke()
    }

    context.beginPath()
    context.arc(node.x, node.y, radius, 0, Math.PI * 2)
    context.fillStyle = nodeColor(node, selected || hovered)
    context.fill()

    if (hovered || selected || (!compact && globalScale >= 2.4 && (node.value ?? 1) >= 4)) {
      const fontSize = Math.max(2, 9.5 / globalScale)
      context.font = `500 ${fontSize}px Inter, system-ui, sans-serif`
      context.textAlign = 'left'
      context.textBaseline = 'middle'
      context.lineWidth = 3 / globalScale
      context.strokeStyle = 'rgba(255,255,255,0.94)'
      context.strokeText(node.label, node.x + radius + 3 / globalScale, node.y)
      context.fillStyle = '#303640'
      context.fillText(node.label, node.x + radius + 3 / globalScale, node.y)
    }
    context.restore()
  }

  function drawNodePointerArea(node: ObsidianGraphNode, color: string, context: CanvasRenderingContext2D, globalScale: number) {
    if (node.x === undefined || node.y === undefined) return
    context.fillStyle = color
    context.beginPath()
    context.arc(node.x, node.y, Math.max(5, nodeRadius(node) + 2) / globalScale, 0, Math.PI * 2)
    context.fill()
  }

  function linkColor(link: ObsidianGraphLink) {
    if (isDimmedLink(link)) return 'rgba(160,168,180,0.06)'
    if (hoveredId) return link.color ? withAlpha(link.color, 0.58) : 'rgba(91,105,122,0.55)'
    return link.color ? withAlpha(link.color, 0.24) : 'rgba(126,137,151,0.22)'
  }

  function linkWidth(link: ObsidianGraphLink) {
    if (isDimmedLink(link)) return 0.25
    return hoveredId ? Math.max(0.8, link.width ?? 0.7) : Math.max(0.35, link.width ?? 0.55)
  }

  function setHoveredNode(id: string) {
    hoveredId = id
    adjacentIds = new Set<string>()
    if (id) {
      for (const link of links) {
        const source = endpointId(link.source)
        const target = endpointId(link.target)
        if (source === id) adjacentIds.add(target)
        if (target === id) adjacentIds.add(source)
      }
    }
    redraw()
  }

  function redraw() {
    graphInstance?.nodeCanvasObject(drawNode).linkColor(linkColor).linkWidth(linkWidth)
  }

  function withAlpha(color: string, alpha: number) {
    const hex = color.match(/^#([\da-f]{6})$/i)?.[1]
    if (!hex) return color
    return `rgba(${Number.parseInt(hex.slice(0, 2), 16)},${Number.parseInt(hex.slice(2, 4), 16)},${Number.parseInt(hex.slice(4, 6), 16)},${alpha})`
  }

  function zoomBy(multiplier: number) {
    if (!graphInstance) return
    graphInstance.zoom(Math.min(8, Math.max(0.25, graphInstance.zoom() * multiplier)), 220)
  }

  function fitGraph() {
    graphInstance?.zoomToFit(350, compact ? 16 : 34)
  }

  function reheatGraph() {
    fitted = false
    graphInstance?.d3ReheatSimulation()
  }
</script>

<div class="relative w-full overflow-hidden bg-card" style:height={`${height}px`}>
  <div
    bind:this={host}
    class="h-full w-full cursor-grab active:cursor-grabbing [&_canvas]:block"
    data-force-graph
    role="img"
    aria-label={ariaLabel}
  ></div>
  {#if controls}
    <div class="absolute right-2 top-2 flex items-center gap-0.5 border border-border bg-card/90 p-0.5 shadow-sm backdrop-blur-sm">
      <button type="button" class="grid size-7 place-items-center text-muted-foreground hover:bg-muted hover:text-foreground" aria-label="缩小图谱" title="缩小" onclick={() => zoomBy(1 / 1.35)}><ZoomOut class="size-3.5" /></button>
      <button type="button" class="grid size-7 place-items-center text-muted-foreground hover:bg-muted hover:text-foreground" aria-label="放大图谱" title="放大" onclick={() => zoomBy(1.35)}><ZoomIn class="size-3.5" /></button>
      <button type="button" class="grid size-7 place-items-center text-muted-foreground hover:bg-muted hover:text-foreground" aria-label="图谱适应窗口" title="适应窗口" onclick={fitGraph}><Scan class="size-3.5" /></button>
      {#if !compact}<button type="button" class="grid size-7 place-items-center text-muted-foreground hover:bg-muted hover:text-foreground" aria-label="重新布局图谱" title="重新布局" onclick={reheatGraph}><RefreshCw class="size-3.5" /></button>{/if}
    </div>
  {/if}
</div>
