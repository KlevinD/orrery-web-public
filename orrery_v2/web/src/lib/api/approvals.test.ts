import { afterEach, describe, expect, it, vi } from 'vitest'
import { approvalApi } from './approvals'
import type { SaveHILWorkflowTemplateRequest } from '#lib/types/api'

describe('approvalApi HIL decisions', () => {
  afterEach(() => vi.restoreAllMocks())

  it('posts an authenticated HIL decision to the decision endpoint', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' }
    }))

    await approvalApi.decideHILDecision('decision/1', {
      decision: 'rejected',
      reason: 'Needs more evidence',
      action_id: 'reject'
    }, { idempotencyKey: 'hil-decision-1' })

    expect(fetchMock).toHaveBeenCalledWith('/api/hil/decisions/decision%2F1/decision', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ decision: 'rejected', reason: 'Needs more evidence', action_id: 'reject' }),
      headers: expect.any(Headers)
    }))
    const headers = fetchMock.mock.calls[0][1]?.headers as Headers
    expect(headers.get('Idempotency-Key')).toBe('hil-decision-1')
  })

  it('calls workflow list, detail, viewed, decision, transfer, reassign, and resubmit endpoints', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' }
    }))

    await approvalApi.listHILWorkflowInstances('?view=my_tasks&status=running')
    await approvalApi.getHILWorkflowInstance('workflow/1')
    await approvalApi.recordHILWorkflowViewed('workflow/1', { idempotencyKey: 'view-workflow-1' })
    await approvalApi.decideHILWorkflowTask('task/1', { decision: 'reject', reason: 'Unsupported claim', expected_instance_version: 3 }, { idempotencyKey: 'decide-task-1' })
    await approvalApi.transferHILWorkflowTask('task/1', { new_assignee: { kind: 'human', id: 'human-2' }, reason: 'Channel owner', expected_instance_version: 3 }, { idempotencyKey: 'transfer-task-1' })
    await approvalApi.reassignHILWorkflowTask('task/1', { new_assignee: { kind: 'human', id: 'human-3' }, reason: 'Unavailable', expected_instance_version: 3 }, { idempotencyKey: 'reassign-task-1' })
    await approvalApi.resubmitHILWorkflow('workflow/1', {
      args_hash: 'sha256:new-args', material_hash: 'sha256:new-material', payload_preview: { title: 'Revised' },
      expires_at: '2026-08-18T09:00:00Z', expected_instance_version: 3
    }, { idempotencyKey: 'resubmit-workflow-1' })

    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      '/api/hil/workflow-instances?view=my_tasks&status=running',
      '/api/hil/workflow-instances/workflow%2F1',
      '/api/hil/workflow-instances/workflow%2F1/viewed',
      '/api/hil/workflow-tasks/task%2F1/decision',
      '/api/hil/workflow-tasks/task%2F1/transfer',
      '/api/hil/workflow-tasks/task%2F1/reassign',
      '/api/hil/workflow-instances/workflow%2F1/resubmit'
    ])
    expect(fetchMock.mock.calls.slice(2).every(([, options]) => options?.method === 'POST')).toBe(true)
    expect((fetchMock.mock.calls[3][1]?.headers as Headers).get('Idempotency-Key')).toBe('decide-task-1')
  })

  it('calls workflow template draft, publish, and disable endpoints', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' }
    }))
    const draft: SaveHILWorkflowTemplateRequest = {
      name: 'Marketing approval',
      definition: {
        risk_levels: ['high'],
        stages: [{ key: 'review', name: 'Review', order: 1, type: 'approval' as const, approval: { strategy: 'single' as const, assignees: [{ kind: 'explicit_human' as const, id: 'ding' }], deadline_minutes: 60 } }]
      }
    }

    await approvalApi.listHILWorkflowTemplates('?status=active')
    await approvalApi.createHILWorkflowTemplate(draft, { idempotencyKey: 'create-template-1' })
    await approvalApi.getHILWorkflowTemplate('template/1')
    await approvalApi.updateHILWorkflowTemplateDraft('template/1', draft, { idempotencyKey: 'update-template-1' })
    await approvalApi.publishHILWorkflowTemplate('template/1', { bindings: [] }, { idempotencyKey: 'publish-template-1' })
    await approvalApi.disableHILWorkflowTemplate('template/1', { reason: 'Retired' }, { idempotencyKey: 'disable-template-1' })

    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      '/api/hil/workflow-templates?status=active',
      '/api/hil/workflow-templates',
      '/api/hil/workflow-templates/template%2F1',
      '/api/hil/workflow-templates/template%2F1/draft',
      '/api/hil/workflow-templates/template%2F1/publish',
      '/api/hil/workflow-templates/template%2F1/disable'
    ])
    expect(fetchMock.mock.calls[1][1]?.method).toBe('POST')
    expect(fetchMock.mock.calls[3][1]?.method).toBe('PATCH')
    expect((fetchMock.mock.calls[4][1]?.headers as Headers).get('Idempotency-Key')).toBe('publish-template-1')
  })
})
