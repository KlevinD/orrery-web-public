import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import WorkItemsCenter from './WorkItemsCenter.svelte'
import type { WorkCenterItem } from './work-center'

const items: WorkCenterItem[] = [
  {
    id: 'idea-1', title: 'Explore renewal signals', summary: 'Review customer feedback', kind: 'idea',
    phase: 'explore', status: 'submitted', priority: 'medium', owner: 'Ding',
    updatedAt: '2026-08-13T08:00:00Z', href: '/ideas/idea-1', progress: 12
  },
  {
    id: 'todo-1', title: 'Plan the renewal flow', summary: 'Define the acceptance criteria', kind: 'todo',
    phase: 'plan', status: 'planned', priority: 'high', owner: 'Assistant',
    updatedAt: '2026-08-14T08:00:00Z', href: '/todos/todo-1', progress: 35
  },
  {
    id: 'mission-1', title: 'Ship renewal workflow', summary: 'Agents are executing the plan', kind: 'mission',
    phase: 'mission', status: 'active', priority: 'high', owner: 'Ops Agent',
    updatedAt: '2026-08-15T08:00:00Z', href: '/missions/mission-1', progress: 62, missionId: 'mission-1'
  }
]

describe('WorkItemsCenter', () => {
  beforeEach(() => setLocale('en-US'))

  it('renders a unified real-data surface without a conversation composer', () => {
    const { container } = render(WorkItemsCenter, { props: { items } })

    expect(screen.getByRole('heading', { name: 'Work center' })).toBeTruthy()
    expect(container.querySelectorAll('[data-testid="work-items-list"] a')).toHaveLength(3)
    expect(container.querySelector('textarea')).toBeNull()
    expect(container.querySelector('a[href="/ideas/idea-1"]')).toBeTruthy()
    expect(container.querySelector('a[href="/todos/todo-1"]')).toBeTruthy()
    expect(container.querySelector('a[href="/missions/mission-1"]')).toBeTruthy()
  })

  it('filters from the vertical stage rail and search field', async () => {
    const { container } = render(WorkItemsCenter, { props: { items } })

    await fireEvent.click(screen.getByRole('button', { name: /Explore/ }))
    expect(container.querySelector('a[href="/ideas/idea-1"]')).toBeTruthy()
    expect(container.querySelector('a[href="/missions/mission-1"]')).toBeNull()

    await fireEvent.click(screen.getByRole('button', { name: /Explore/ }))
    await fireEvent.input(screen.getByRole('searchbox'), { target: { value: 'renewal flow' } })
    expect(container.querySelector('a[href="/todos/todo-1"]')).toBeTruthy()
    expect(container.querySelector('a[href="/ideas/idea-1"]')).toBeNull()
  })

  it('uses the selected category for rows and stage counts without duplicate navigation', async () => {
    const { container, rerender } = render(WorkItemsCenter, {
      props: { items, kindFilter: 'todo', embedded: true }
    })
    expect(container.querySelectorAll('[data-testid="work-items-list"] a')).toHaveLength(1)
    expect(container.querySelector('a[href="/todos/todo-1"]')).toBeTruthy()
    expect(screen.getByRole('button', { name: /Explore/ }).textContent).toContain('0')
    expect(screen.queryByRole('heading', { name: 'Work center' })).toBeNull()
    expect(screen.queryByRole('group', { name: 'Item type' })).toBeNull()

    await rerender({ items, kindFilter: 'idea', embedded: true })
    expect(container.querySelector('a[href="/ideas/idea-1"]')).toBeTruthy()
    expect(container.querySelector('a[href="/todos/todo-1"]')).toBeNull()
    expect(screen.getByRole('button', { name: /Explore/ }).textContent).toContain('1')
  })

  it('does not discard server matches and resets the stage on a new search', async () => {
    const { container } = render(WorkItemsCenter, { props: { items, serverSearch: true } })
    await fireEvent.click(screen.getByRole('button', { name: /Explore/ }))
    expect(container.querySelectorAll('[data-testid="work-items-list"] a')).toHaveLength(1)
    await fireEvent.input(screen.getByRole('searchbox'), { target: { value: 'renewal criteria' } })
    expect(container.querySelectorAll('[data-testid="work-items-list"] a')).toHaveLength(3)
    expect(screen.getByRole('button', { name: /Explore/ }).getAttribute('aria-pressed')).toBe('false')
  })

  it('keeps pagination available for an empty local stage and disables duplicate loads', async () => {
    const onLoadMore = vi.fn()
    const { rerender } = render(WorkItemsCenter, { props: { items, hasMore: true, onLoadMore } })
    await fireEvent.click(screen.getByRole('button', { name: /Done/ }))
    expect(screen.getByText('No matches in loaded items')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
    expect(onLoadMore).toHaveBeenCalledOnce()
    await rerender({ items, hasMore: true, onLoadMore, loadingMore: true })
    expect((screen.getByRole('button', { name: 'Load more' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('hides stale rows while searching and keeps successful rows on a partial error', async () => {
    const { container, rerender } = render(WorkItemsCenter, { props: { items, loading: true } })
    expect(container.querySelector('[data-testid="work-items-list"]')).toBeNull()
    await rerender({ items, loading: false, errorMessage: 'Request failed' })
    expect(container.querySelectorAll('[data-testid="work-items-list"] a')).toHaveLength(3)
    expect(screen.getByText(/Some data is temporarily unavailable/)).toBeTruthy()
  })
})
