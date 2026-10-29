import { fireEvent, render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import type {
  ResponsibilityEvaluationBatchResultDTO,
  ResponsibilityEvaluationItemPageDTO,
  ResponsibilityEvaluationItemStatus
} from '#lib/types/scheduler'

const queryMocks = vi.hoisted(() => ({
  useResponsibilityEvaluationItemsQuery: vi.fn(),
  refetch: vi.fn()
}))

vi.mock('#lib/queries/scheduler', () => ({
  useResponsibilityEvaluationItemsQuery: queryMocks.useResponsibilityEvaluationItemsQuery
}))

import ResponsibilityEvaluationBatch from './ResponsibilityEvaluationBatch.svelte'

type FilterReader = () => {
  status?: ResponsibilityEvaluationItemStatus | ''
  cursor?: string
  limit?: number
}

describe('ResponsibilityEvaluationBatch', () => {
  let readFilters: FilterReader
  let readEnabled: () => boolean

  beforeEach(() => {
    setLocale('en-US')
    queryMocks.refetch.mockReset()
    queryMocks.useResponsibilityEvaluationItemsQuery.mockReset()
    queryMocks.useResponsibilityEvaluationItemsQuery.mockImplementation((...args: unknown[]) => {
      readFilters = args[2] as FilterReader
      readEnabled = args[3] as () => boolean
      return {
        data: itemPage,
        error: null,
        isError: false,
        isFetching: false,
        isLoading: false,
        refetch: queryMocks.refetch
      }
    })
  })

  it('shows the batch summary immediately and enables item loading only after expansion', async () => {
    render(ResponsibilityEvaluationBatch, {
      props: {
        scheduledTaskID: 'schedule-1',
        scheduledRunID: 'run-1',
        batch
      }
    })

    expect(screen.getByText('People responsibility batch evaluation')).toBeTruthy()
    expect(screen.getByText('Discovered portfolios')).toBeTruthy()
    expect(screen.getByText(/tenant-people-1/)).toBeTruthy()
    expect(readEnabled()).toBe(false)

    const summary = screen.getByText('Portfolio results').closest('summary')
    expect(summary).toBeTruthy()
    const details = summary?.closest('details') as HTMLDetailsElement
    details.open = true
    await fireEvent(details, new Event('toggle'))
    await tick()

    expect(readEnabled()).toBe(true)
    expect(screen.getByText('agent-profile-1')).toBeTruthy()
  })

  it('uses cursor pagination and resets the cursor when the status changes', async () => {
    render(ResponsibilityEvaluationBatch, {
      props: {
        scheduledTaskID: 'schedule-1',
        scheduledRunID: 'run-1',
        batch
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
    await tick()
    expect(readFilters()).toEqual({ status: '', cursor: 'cursor-page-2', limit: 20 })

    await fireEvent.change(screen.getByRole('combobox', { name: 'Portfolio result status' }), {
      target: { value: 'failed' }
    })
    await tick()
    expect(readFilters()).toEqual({ status: 'failed', cursor: '', limit: 20 })
  })
})

const batch: ResponsibilityEvaluationBatchResultDTO = {
  scope_id: 'tenant-people-1',
  discovered_count: 4,
  evaluated_count: 3,
  no_action_count: 1,
  linked_existing_mission_count: 1,
  materialized_count: 1,
  skipped_count: 0,
  failed_count: 1,
  candidate_count: 5,
  actionable_count: 2,
  waiting_count: 1,
  review_count: 1,
  escalated_count: 0,
  paused_count: 0,
  invalid_count: 0
}

const itemPage: ResponsibilityEvaluationItemPageDTO = {
  items: [{
    item_id: 'item-1',
    scheduled_task_id: 'schedule-1',
    scheduled_run_id: 'run-1',
    portfolio_id: 'portfolio-1',
    portfolio_binding_id: 'binding-1',
    portfolio_version_id: 'version-1',
    agent_profile_id: 'agent-profile-1',
    status: 'completed',
    attempt: 1,
    created_at: '2026-08-31T01:00:00Z',
    updated_at: '2026-08-31T01:01:00Z'
  }],
  next_cursor: 'cursor-page-2',
  has_more: true,
  limit: 20
}
