import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ObsidianForceGraph from './ObsidianForceGraph.svelte'

const graphApi = {
  graphData: vi.fn(), width: vi.fn(), height: vi.fn(), backgroundColor: vi.fn(),
  nodeId: vi.fn(), nodeVal: vi.fn(), nodeLabel: vi.fn(), nodeCanvasObject: vi.fn(),
  nodePointerAreaPaint: vi.fn(), linkLabel: vi.fn(), linkColor: vi.fn(), linkWidth: vi.fn(),
  linkDirectionalArrowLength: vi.fn(), linkDirectionalArrowColor: vi.fn(),
  onNodeClick: vi.fn(), onNodeHover: vi.fn(), onLinkClick: vi.fn(), onBackgroundClick: vi.fn(),
  onEngineStop: vi.fn(), d3Force: vi.fn(), d3VelocityDecay: vi.fn(),
  minZoom: vi.fn(), maxZoom: vi.fn(), zoom: vi.fn(), zoomToFit: vi.fn(),
  d3ReheatSimulation: vi.fn(), _destructor: vi.fn()
}

for (const [name, fn] of Object.entries(graphApi)) {
  if (name === 'zoom') fn.mockImplementation((value?: number) => value === undefined ? 1 : graphApi)
  else if (name === 'd3Force') fn.mockImplementation(() => undefined)
  else fn.mockReturnValue(graphApi)
}

vi.mock('force-graph', () => ({ default: vi.fn(function () { return graphApi }) }))

describe('ObsidianForceGraph', () => {
  beforeEach(() => vi.clearAllMocks())

  it('mounts a canvas force graph with mapped relationship data', async () => {
    render(ObsidianForceGraph, {
      props: {
        ariaLabel: 'Test relationship graph',
        nodes: [
          { id: 'a', label: 'Alpha', group: 'object', value: 4 },
          { id: 'b', label: 'Beta', group: 'person', value: 1 }
        ],
        links: [{ id: 'a-b', source: 'a', target: 'b', label: 'owns' }]
      }
    })

    expect(screen.getByRole('img', { name: 'Test relationship graph' }).hasAttribute('data-force-graph')).toBe(true)
    await waitFor(() => expect(graphApi.graphData).toHaveBeenCalled())
    expect(graphApi.graphData).toHaveBeenCalledWith(expect.objectContaining({
      nodes: expect.arrayContaining([expect.objectContaining({ id: 'a', group: 'object' })]),
      links: [expect.objectContaining({ id: 'a-b', source: 'a', target: 'b' })]
    }))
  })

  it('forwards node selection and exposes graph navigation controls', async () => {
    const onselect = vi.fn()
    render(ObsidianForceGraph, {
      props: { nodes: [{ id: 'a', label: 'Alpha' }], links: [], onselect }
    })

    await waitFor(() => expect(graphApi.onNodeClick).toHaveBeenCalled())
    const callback = graphApi.onNodeClick.mock.calls.at(-1)?.[0]
    callback?.({ id: 'a', label: 'Alpha' }, new MouseEvent('click'))
    expect(onselect).toHaveBeenCalledWith('a')

    await fireEvent.click(screen.getByRole('button', { name: '放大图谱' }))
    await fireEvent.click(screen.getByRole('button', { name: '图谱适应窗口' }))
    expect(graphApi.zoom).toHaveBeenCalledWith(1.35, 220)
    expect(graphApi.zoomToFit).toHaveBeenCalledWith(350, 34)
  })

  it('renders indirect nodes muted while preserving degree-based size differences', async () => {
    render(ObsidianForceGraph, {
      props: {
        nodes: [
          { id: 'direct', label: 'Direct', group: 'object', value: 1 },
          { id: 'indirect', label: 'Indirect', group: 'link', value: 9, muted: true }
        ],
        links: []
      }
    })

    await waitFor(() => expect(graphApi.nodeCanvasObject).toHaveBeenCalled())
    const draw = graphApi.nodeCanvasObject.mock.calls.find(([value]) => typeof value === 'function')?.[0]
    const arcs: number[] = []
    const fills: string[] = []
    const context = {
      save: vi.fn(), restore: vi.fn(), beginPath: vi.fn(), arc: vi.fn((_: number, __: number, radius: number) => arcs.push(radius)),
      fill: vi.fn(() => fills.push(context.fillStyle)), stroke: vi.fn(), strokeText: vi.fn(), fillText: vi.fn(),
      globalAlpha: 1, fillStyle: '', strokeStyle: '', lineWidth: 1, font: '', textAlign: '', textBaseline: ''
    }
    draw?.({ id: 'direct', label: 'Direct', group: 'object', value: 1, x: 0, y: 0 }, context, 1)
    draw?.({ id: 'indirect', label: 'Indirect', group: 'link', value: 9, muted: true, x: 0, y: 0 }, context, 1)

    expect(fills).toEqual(['#4f83cc', '#b8bdc5'])
    expect(arcs[1]).toBeGreaterThan(arcs[0])
  })
})
