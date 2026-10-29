import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { tick } from 'svelte'
import { beforeEach, expect, it, vi } from 'vitest'
import { setLocale, createTranslator } from '#lib/i18n'
import { listKnowledgeBases, loadKnowledgeGraphDocuments } from '#lib/queries/knowledge'
import KnowledgePage from './+page.svelte'

vi.mock('#lib/queries/knowledge', () => ({
  listKnowledgeBases: vi.fn(),
  loadKnowledgeGraphDocuments: vi.fn(),
  deleteKnowledgeBase: vi.fn(),
  searchKnowledge: vi.fn()
}))
vi.mock('#lib/components/graph/ResourceRelationshipGraph.svelte', () => ({ default: () => {} }))

beforeEach(() => {
  vi.resetAllMocks()
  setLocale('en-US')
  vi.mocked(listKnowledgeBases).mockResolvedValue({ items: [{ id: 'base-1', name: 'Knowledge', owner_type: 'human', owner_id: 'user-1' }] } as never)
})

it('does not automatically retry failed graph requests; refresh permits a new attempt', async () => {
  vi.mocked(loadKnowledgeGraphDocuments).mockRejectedValueOnce(new Error('graph unavailable')).mockResolvedValue({})
  render(KnowledgePage)
  const t = createTranslator('en-US')
  await fireEvent.click(await screen.findByRole('tab', { name: t('resources.graph.graphView') }))
  await waitFor(() => expect(screen.getByText('graph unavailable')).toBeTruthy())
  await tick()
  await tick()
  expect(loadKnowledgeGraphDocuments).toHaveBeenCalledTimes(1)
  await fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() => expect(loadKnowledgeGraphDocuments).toHaveBeenCalledTimes(2))
  await waitFor(() => expect(screen.queryByText('graph unavailable')).toBeNull())
})
