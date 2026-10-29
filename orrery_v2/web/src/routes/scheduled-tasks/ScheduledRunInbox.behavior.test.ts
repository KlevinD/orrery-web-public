import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'

const { setRunReadStateMock, refetchMock } = vi.hoisted(() => ({
  setRunReadStateMock: vi.fn(),
  refetchMock: vi.fn()
}))

const inboxItems = [
  {
    task_title: 'Daily AI briefing',
    is_read: true,
    read_at: '2026-09-03T01:00:00Z',
    attention_required: false,
    run: {
      scheduled_run_id: 'run-1',
      scheduled_task_id: 'task-1',
      trigger_type: 'due',
      status: 'completed',
      target_mode: 'agent_task',
      attempt: 1,
      completed_at: '2026-09-03T01:00:00Z',
      created_at: '2026-09-03T00:59:00Z',
      result: { summary: '# Today\n\nThree important model releases.' },
      mission_id: 'mission-1'
    }
  },
  {
    task_title: 'PageIndex refresh',
    is_read: false,
    attention_required: true,
    run: {
      scheduled_run_id: 'run-2',
      scheduled_task_id: 'task-2',
      trigger_type: 'retry',
      status: 'failed',
      target_mode: 'discussion_workflow',
      attempt: 2,
      failure_reason: 'Knowledge source could not be fetched.',
      completed_at: '2026-09-03T02:00:00Z',
      created_at: '2026-09-03T01:59:00Z'
    }
  }
]

vi.mock('#lib/queries/scheduler', () => ({
  useScheduledRunInboxQuery: () => ({
    data: { items: inboxItems, total: 2, unread_total: 1, attention_total: 1, limit: 30, offset: 0 },
    isLoading: false,
    isFetching: false,
    isError: false,
    error: null,
    refetch: refetchMock
  }),
  useScheduledTaskQuery: () => ({ data: { tenant_id: 'tenant-a' } }),
  useSchedulerCommandMutations: () => ({
    setRunReadState: { mutate: setRunReadStateMock, isPending: false }
  }),
  useResponsibilityEvaluationItemsQuery: () => ({
    data: undefined,
    isLoading: false,
    isFetching: false,
    isError: false,
    refetch: vi.fn()
  })
}))

import ScheduledRunInbox from './ScheduledRunInbox.svelte'

describe('ScheduledRunInbox behavior', () => {
  beforeEach(() => {
    setRunReadStateMock.mockReset()
    refetchMock.mockReset()
    setLocale('zh-CN')
  })

  it('shows a readable report and lets the viewer change personal read state', async () => {
    const { container } = render(ScheduledRunInbox)

    expect(screen.getByRole('heading', { name: 'Today' })).toBeTruthy()
    expect(screen.getByText('Three important model releases.')).toBeTruthy()
    expect(screen.getByRole('tab', { name: '未读 1' })).toBeTruthy()
    expect(screen.getByRole('tab', { name: '待处理 1' })).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: '标记为未读' }))
    expect(setRunReadStateMock).toHaveBeenCalledWith(
      { scheduledRunID: 'run-1', read: false },
      expect.objectContaining({ onError: expect.any(Function) })
    )

    await fireEvent.click(container.querySelector('[data-scheduled-run-inbox-item="run-2"]') as HTMLElement)
    await waitFor(() => expect(setRunReadStateMock).toHaveBeenCalledWith(
      { scheduledRunID: 'run-2', read: true },
      expect.objectContaining({ onError: expect.any(Function) })
    ))
    expect(screen.getAllByText('Knowledge source could not be fetched.')).toHaveLength(2)
  })

  it('marks an exact deep-linked unread report without cascading to other rows', async () => {
    render(ScheduledRunInbox, { props: { initialRunID: 'run-2' } })

    await waitFor(() => expect(setRunReadStateMock).toHaveBeenCalledTimes(1))
    expect(setRunReadStateMock).toHaveBeenCalledWith(
      { scheduledRunID: 'run-2', read: true },
      expect.any(Object)
    )
  })
})
