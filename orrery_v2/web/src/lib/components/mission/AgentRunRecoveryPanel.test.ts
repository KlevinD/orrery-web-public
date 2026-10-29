import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiClientError } from '#lib/api/client'
import type { AgentRunRecovery } from '#lib/api/agent-run-recovery'
import AgentRunRecoveryPanel from './AgentRunRecoveryPanel.svelte'

const state = vi.hoisted(() => ({
  query: { data: undefined as AgentRunRecovery | undefined, isLoading: false, isError: false, error: undefined as unknown, isFetching: false, refetch: vi.fn() },
  mutation: { isPending: false, mutateAsync: vi.fn() }
}))

vi.mock('#lib/queries/agent-run-recovery', () => ({
  useAgentRunRecoveryQuery: vi.fn(() => state.query),
  useAgentRunRecoveryReviewMutation: vi.fn(() => state.mutation)
}))

function pending(): AgentRunRecovery {
  return { available: true, revision: '9007199254740993', phase: 'tools_pending', model_calls: 12, tool_calls: 5, compactions: 2, summarizing: false, summary_processed_bytes: 0, summary_total_bytes: 0, restart_blocked: true, pending_tools: [{ call_id: 'call-42', tool_id: 'tool.external', state: 'unknown' }], can_review: true, can_restart: false }
}

async function open(onRetry = vi.fn()) {
  render(AgentRunRecoveryPanel, { props: { missionId: 'mission-1', runId: 'run-1', onRetry } })
  await fireEvent.click(screen.getByRole('button', { name: '长任务状态' }))
  return onRetry
}

describe('AgentRunRecoveryPanel', () => {
  beforeEach(() => {
    state.query.data = pending()
    state.query.isLoading = false
    state.query.isError = false
    state.query.error = undefined
    state.query.refetch.mockReset()
    state.mutation.mutateAsync.mockReset().mockResolvedValue({ review_id: 'review-1' })
  })

  it('shows the pending operation and saves evidence without enabling execution', async () => {
    const onRetry = await open()
    expect(screen.getByText('工具操作待核对')).toBeTruthy()
    expect(screen.getByText('tool.external · call-42')).toBeTruthy()
    expect(screen.getByText(/证据每行一条，最多 5 条/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: '重新执行' })).toBeNull()
    const save = screen.getByRole('button', { name: '保存核对记录' }) as HTMLButtonElement
    expect(save.disabled).toBe(true)
    await fireEvent.input(screen.getByLabelText('核对说明'), { target: { value: '已检查外部操作回执' } })
    await fireEvent.input(screen.getByLabelText('证据或回执编号'), { target: { value: ' receipt:42 \n audit:43 ' } })
    await fireEvent.submit(save.closest('form')!)
    await waitFor(() => expect(screen.getByText('核对已登记，任务仍暂停，需要先处置外部操作。')).toBeTruthy())
    const first = state.mutation.mutateAsync.mock.calls[0][0]
    expect(first.body).toEqual({ revision: '9007199254740993', conclusion: 'uncertain', note: '已检查外部操作回执', evidence_refs: ['receipt:42', 'audit:43'] })
    expect(first.idempotencyKey).toBeTruthy()
    await fireEvent.submit(save.closest('form')!)
    expect(state.mutation.mutateAsync.mock.calls[1][0].idempotencyKey).toBe(first.idempotencyKey)
    expect(onRetry).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: '重新执行' })).toBeNull()
  })

  it('refreshes a stale checkpoint after a conflict and does not report success', async () => {
    state.mutation.mutateAsync.mockRejectedValue(new ApiClientError({ status: 409, code: 'conflict', message: 'changed' }))
    await open()
    await fireEvent.input(screen.getByLabelText('核对说明'), { target: { value: 'checked' } })
    await fireEvent.input(screen.getByLabelText('证据或回执编号'), { target: { value: 'receipt:42' } })
    await fireEvent.submit(screen.getByRole('button', { name: '保存核对记录' }).closest('form')!)
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('运行状态已变化'))
    expect(state.query.refetch).toHaveBeenCalledTimes(1)
    expect(screen.queryByText('核对已登记，任务仍暂停，需要先处置外部操作。')).toBeNull()
  })

  it('allows a fresh run only when the server permits it', async () => {
    state.query.data = { ...pending(), phase: 'ready', restart_blocked: false, can_review: false, can_restart: true, pending_tools: [] }
    const onRetry = await open()
    expect(screen.queryByLabelText('核对说明')).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: '重新执行' }))
    expect(onRetry).toHaveBeenCalledTimes(1)
    expect(screen.getByText('将创建新运行，从头处理此任务。')).toBeTruthy()
  })

  it('shows resumable summary progress without displaying memory content', async () => {
    state.query.data = { ...pending(), phase: 'ready', restart_blocked: false, can_review: false, summarizing: true, summary_processed_bytes: 100, summary_total_bytes: 300, pending_tools: [] }
    await open()
    expect(screen.getByLabelText('记忆整理进度').getAttribute('value')).toBe('100')
    expect(screen.getByText('正在整理循环记忆 · 已处理 100 / 300 字节')).toBeTruthy()
  })

  it('fails closed when status is forbidden', async () => {
    state.query.data = undefined
    state.query.isError = true
    state.query.error = new ApiClientError({ status: 403, code: 'forbidden', message: 'denied' })
    await open()
    expect(screen.getByRole('alert').textContent).toContain('没有查看或登记运行核对记录的权限')
    expect(screen.queryByRole('button', { name: '重新执行' })).toBeNull()
    expect(screen.queryByRole('button', { name: '保存核对记录' })).toBeNull()
  })
})
