import { render, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { writable } from 'svelte/store'
import { goto } from '$app/navigation'
import { page } from '$app/stores'
import ApprovalPage from './+page.svelte'
vi.mock('$app/stores', async () => { const { writable } = await import('svelte/store'); return { page: writable({ url: new URL('https://orrery.test/approvals') }) } })
vi.mock('$app/navigation', () => ({ goto: vi.fn() }))
describe('old approval entry', () => {
 beforeEach(() => vi.mocked(goto).mockClear())
 it.each([
  ['?decision_id=decision-7', 'approval:decision-7', 'inbox'],
  ['?tab=history&approval_id=legacy-3', 'approval:legacy-3', 'history'],
  ['?council=council-8', 'council:council-8', 'inbox']
 ])('preserves the exact target of %s', async (query, action, view) => {
  ;(page as ReturnType<typeof writable<any>>).set({ url: new URL('https://orrery.test/approvals' + query) })
  render(ApprovalPage)
  await waitFor(() => expect(goto).toHaveBeenCalled())
  const target = new URL(String(vi.mocked(goto).mock.calls[0][0]), 'https://orrery.test')
  expect(target.pathname).toBe('/work-items')
  expect(target.searchParams.get('action')).toBe(action)
  expect(target.searchParams.get('view')).toBe(view)
  expect(vi.mocked(goto).mock.calls[0][1]).toEqual({ replaceState: true })
 })
})
