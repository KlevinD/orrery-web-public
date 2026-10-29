import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import type { SchemaGraphDTO } from '#lib/api/ontology-schema'
import OntologySchemaGraph from './OntologySchemaGraph.svelte'

vi.mock('force-graph', () => ({ default: vi.fn(function () {
  const graph = new Proxy({}, { get: () => vi.fn(() => graph) })
  return graph
}) }))

const graph: SchemaGraphDTO = {
  view: 'semantic',
  nodes: [
    { id: 'object:mission', kind: 'object', label: 'Mission', status: 'active', origin: 'system', matched: true },
    { id: 'link:owns', kind: 'link', label: '负责', status: 'disabled', origin: 'tenant', matched: false },
    { id: 'tenant-1:object:isolated', tenant_id: 'tenant-1', definition_id: 'isolated', kind: 'object', label: 'Isolated', status: 'active', origin: 'tenant', matched: false }
  ],
  edges: [
    { id: 'edge-1', source_id: 'object:mission', target_id: 'link:owns', relation: 'source' }
  ],
  visible_nodes: 3,
  visible_edges: 1,
  applied_max_nodes: 180,
  truncated: false
}

describe('OntologySchemaGraph', () => {
  it('renders directed definitions, status semantics and isolated keyboard entries', async () => {
    const onselect = vi.fn()
    const { container } = render(OntologySchemaGraph, { props: { graph, onselect } })

    await waitFor(() => expect(screen.getByRole('img', { name: '本体模型关系图' })).toBeTruthy())
    expect(screen.getByRole('status').textContent).toContain('3 个节点 · 1 条有向关系')
    expect(container.querySelector('[data-force-graph]')).toBeTruthy()
    expect(container.querySelector('[data-force-graph] svg')).toBeNull()
    expect(screen.getByText('已禁用')).toBeTruthy()
    expect(screen.getByText('起点类型 →')).toBeTruthy()
    expect(screen.getByRole('button', { name: /Isolated/ })).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: /Isolated/ }))
    expect(onselect).toHaveBeenCalledWith('object', 'isolated', 'tenant-1')
  })

  it('uses shared force graph navigation controls', async () => {
    render(OntologySchemaGraph, { props: { graph } })
    expect(await screen.findByRole('button', { name: '放大图谱' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '缩小图谱' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '图谱适应窗口' })).toBeTruthy()
  })
})
