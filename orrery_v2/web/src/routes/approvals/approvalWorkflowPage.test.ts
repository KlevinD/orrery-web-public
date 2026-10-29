import { render, waitFor } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { goto } from '$app/navigation'
import WorkflowRoute from './workflows/+page.svelte'
vi.mock('$app/stores', async () => { const { readable } = await import('svelte/store'); return { page: readable({ url: new URL('https://orrery.test/approvals/workflows?template_id=template-1') }) } })
vi.mock('$app/navigation', () => ({ goto: vi.fn() }))
describe('old workflow settings entry', () => {
 it('moves settings into the work center and preserves the selected template', async () => {
  render(WorkflowRoute)
  await waitFor(() => expect(goto).toHaveBeenCalledWith('/work-items?template_id=template-1&view=workflows', { replaceState: true }))
 })
})
