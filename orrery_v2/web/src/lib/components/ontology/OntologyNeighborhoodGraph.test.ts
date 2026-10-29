import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import type { ContextNeighborhoodDTO } from '#lib/api/ontology-context'
import OntologyNeighborhoodGraph from './OntologyNeighborhoodGraph.svelte'

vi.mock('force-graph', () => ({ default: vi.fn(function () {
  const graph = new Proxy({}, { get: () => vi.fn(() => graph) })
  return graph
}) }))

const graph: ContextNeighborhoodDTO = {
  root_id: 'mission-1',
  nodes: [
    { id: 'mission-1', object_type: 'mission', label: 'Mission', degree: 1 },
    { id: 'task-1', object_type: 'task', label: 'First task', degree: 1 },
    { id: 'note-1', object_type: 'note', label: 'Isolated note', degree: 0 }
  ],
  edges: [
    { id: 'edge-1', source_id: 'mission-1', target_id: 'task-1', link_type: 'has_task', link_type_label: '使命包含任务' }
  ],
  visible_nodes: 3,
  visible_edges: 1,
  applied_depth: 1,
  applied_max_nodes: 120,
  applied_max_edges: 240,
  truncated: false
}

describe('OntologyNeighborhoodGraph', () => {
  it('renders a directed bounded graph and an equivalent node list', async () => {
    const onselect = vi.fn()
    const { container } = render(OntologyNeighborhoodGraph, { props: { graph, selectedId: 'mission-1', onselect } })

    expect(screen.getByRole('status').textContent).toContain('3 个对象 · 1 条有向关系')
    expect(container.querySelector('[data-force-graph]')).toBeTruthy()
    expect(container.querySelector('[data-force-graph] svg')).toBeNull()
    expect(screen.getByText('键盘可访问节点与关系列表')).toBeTruthy()
    expect(screen.getByText(/使命包含任务/)).toBeTruthy()
    const isolatedEntries = screen.getAllByRole('button', { name: /Isolated note/ })
    const isolatedListButton = isolatedEntries.find((entry) => entry.tagName === 'BUTTON')
    expect(isolatedListButton).toBeTruthy()

    await fireEvent.click(isolatedListButton as HTMLElement)
    expect(onselect).toHaveBeenCalledWith('note-1')
  })

  it('zooms, enters full screen and exits with Escape', async () => {
    const { container } = render(OntologyNeighborhoodGraph, { props: { graph } })
    expect(screen.getByRole('img', { name: '实际数据关系图' })).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: '全屏查看图谱' }))
    expect(screen.getByRole('region', { name: 'Object neighborhood explorer' }).classList.contains('fixed')).toBe(true)
    expect(screen.getByRole('button', { name: '退出图谱全屏' })).toBeTruthy()

    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(container.querySelector('[aria-label="Object neighborhood explorer"]')?.classList.contains('fixed')).toBe(false)
  })
})
