import { describe, expect, it } from 'vitest'
import { ApiClientError } from '#lib/api/client'
import { approvalRefreshIntervalMs, buildHILWorkflowQuery, buildHILWorkflowTemplateQuery, shouldRefreshHILWorkflowAfterError } from './approvals'

describe('HIL workflow queries', () => {
  it('builds explicit workflow view, status, and pagination filters', () => {
    expect(buildHILWorkflowQuery({ view: 'my_tasks', status: 'running', limit: 50, offset: 10 }))
      .toBe('?view=my_tasks&status=running&limit=50&offset=10')
  })

  it('builds the supported workflow template status filter', () => {
    expect(buildHILWorkflowTemplateQuery({ status: 'disabled' })).toBe('?status=disabled')
    expect(buildHILWorkflowTemplateQuery({ status: '' })).toBe('')
  })

  it('refreshes workflow state after optimistic concurrency conflicts', () => {
    expect(shouldRefreshHILWorkflowAfterError(new ApiClientError({ status: 409, code: 'conflict', message: 'stale version' }))).toBe(true)
    expect(shouldRefreshHILWorkflowAfterError(new ApiClientError({ status: 400, code: 'invalid_request', message: 'reason required' }))).toBe(false)
  })

  it('refreshes actionable approval data frequently enough for the inbox', () => {
    expect(approvalRefreshIntervalMs).toBe(15_000)
  })
})
