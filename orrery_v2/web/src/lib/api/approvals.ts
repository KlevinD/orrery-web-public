import { apiFetch, type ApiFetchOptions } from './client'
import type {
  DecideHILWorkflowTaskRequest,
  HILDecisionMutationRequest,
  PublishHILWorkflowTemplateRequest,
  ResubmitHILWorkflowRequest,
  SaveHILWorkflowTemplateRequest,
  TransferHILWorkflowTaskRequest
} from '#lib/types/api'

export const approvalApi = {
  listApprovals: (query = '', options?: ApiFetchOptions) => apiFetch(`/api/approvals${query}`, options),
  listHILDecisions: (query = '', options?: ApiFetchOptions) => apiFetch(`/api/hil/decisions${query}`, options),
  getHILCouncil: (councilId: string, options?: ApiFetchOptions) => apiFetch(`/api/hil/councils/${encodeURIComponent(councilId)}`, options),
  submitHILContribution: (councilId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/councils/${encodeURIComponent(councilId)}/contributions`, {
      ...options,
      method: 'POST',
      body: JSON.stringify(body)
    }),
  castHILVote: (councilId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/councils/${encodeURIComponent(councilId)}/votes`, {
      ...options,
      method: 'POST',
      body: JSON.stringify(body)
    }),
  decideHILDecision: (decisionId: string, body: HILDecisionMutationRequest, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/decisions/${encodeURIComponent(decisionId)}/decision`, {
      ...options,
      method: 'POST',
      body: JSON.stringify(body)
    }),
  listHILWorkflowInstances: (query = '', options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/workflow-instances${query}`, options),
  getHILWorkflowInstance: (instanceId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/workflow-instances/${encodeURIComponent(instanceId)}`, options),
  recordHILWorkflowViewed: (instanceId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/workflow-instances/${encodeURIComponent(instanceId)}/viewed`, { ...options, method: 'POST', body: '{}' }),
  decideHILWorkflowTask: (taskId: string, body: DecideHILWorkflowTaskRequest, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/workflow-tasks/${encodeURIComponent(taskId)}/decision`, { ...options, method: 'POST', body: JSON.stringify(body) }),
  transferHILWorkflowTask: (taskId: string, body: TransferHILWorkflowTaskRequest, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/workflow-tasks/${encodeURIComponent(taskId)}/transfer`, { ...options, method: 'POST', body: JSON.stringify(body) }),
  reassignHILWorkflowTask: (taskId: string, body: TransferHILWorkflowTaskRequest, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/workflow-tasks/${encodeURIComponent(taskId)}/reassign`, { ...options, method: 'POST', body: JSON.stringify(body) }),
  resubmitHILWorkflow: (instanceId: string, body: ResubmitHILWorkflowRequest, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/workflow-instances/${encodeURIComponent(instanceId)}/resubmit`, { ...options, method: 'POST', body: JSON.stringify(body) }),
  listHILWorkflowTemplates: (query = '', options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/workflow-templates${query}`, options),
  createHILWorkflowTemplate: (body: SaveHILWorkflowTemplateRequest, options?: ApiFetchOptions) =>
    apiFetch('/api/hil/workflow-templates', { ...options, method: 'POST', body: JSON.stringify(body) }),
  getHILWorkflowTemplate: (templateId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/workflow-templates/${encodeURIComponent(templateId)}`, options),
  updateHILWorkflowTemplateDraft: (templateId: string, body: SaveHILWorkflowTemplateRequest, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/workflow-templates/${encodeURIComponent(templateId)}/draft`, { ...options, method: 'PATCH', body: JSON.stringify(body) }),
  publishHILWorkflowTemplate: (templateId: string, body: PublishHILWorkflowTemplateRequest, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/workflow-templates/${encodeURIComponent(templateId)}/publish`, { ...options, method: 'POST', body: JSON.stringify(body) }),
  disableHILWorkflowTemplate: (templateId: string, body: { reason: string }, options?: ApiFetchOptions) =>
    apiFetch(`/api/hil/workflow-templates/${encodeURIComponent(templateId)}/disable`, { ...options, method: 'POST', body: JSON.stringify(body) }),
  decideApproval: (approvalId: string, body: unknown, options?: ApiFetchOptions, actionPath?: string) =>
    apiFetch(actionPath || `/api/approvals/${approvalId}/decision`, {
      ...options,
      method: 'POST',
      body: JSON.stringify(body)
    })
}
