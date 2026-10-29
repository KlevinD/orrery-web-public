import type { HILWorkflowInstanceViewModel } from '#lib/types/view-models'

export function workflowView(): HILWorkflowInstanceViewModel {
  const activeTask = {
    id: 'task-1', stageId: 'stage-1', stageKey: 'review', stageName: 'Marketing review', status: 'pending', statusTone: 'warning' as const,
    assignee: 'human:ding', assigneeId: 'ding', originalAssignee: 'human:ding', decision: '', reason: '', transferCount: 0,
    transferReason: '', deadlineAt: 'Aug 17, 10:00', deadlineAtRaw: '2026-08-17T10:00:00Z', actionable: true
  }
  return {
    id: 'workflow-1', caseId: 'case-1', decisionRequestId: 'decision-1', status: 'running', statusTone: 'warning', revision: 2, version: 3,
    predecessorInstanceId: 'workflow-0', title: 'Reviewed title', summary: 'Reviewed digest', requester: 'agent:jordan', requesterId: 'jordan',
    toolName: 'mcp_marketing_marketing_wechat_draft_create', resource: 'runtime_approval:approval-1', riskLevel: 'high', currentStageKey: 'review',
    definitionHash: 'sha256:definition', materialHash: 'sha256:material', argsHash: 'sha256:args',
    material: { kind: 'wechat_draft', language: 'markdown', title: 'Reviewed title', content: '# Reviewed article\n', fields: [{ key: 'account_id', value: 'wechat-main' }] },
    isBusinessApproval: false, materialComplete: true, evidenceRefs: [], artifacts: [],
    stages: [
      { id: 'stage-1', key: 'review', name: 'Marketing review', order: 1, type: 'approval', status: 'active', statusTone: 'warning', participantLabels: ['human:ding'], deadlineAt: 'Aug 17, 10:00', deadlineAtRaw: '2026-08-17T10:00:00Z', escalationIndex: 0 },
      { id: 'stage-2', key: 'publish', name: 'Final approval', order: 2, type: 'approval', status: 'scheduled', statusTone: 'neutral', participantLabels: ['human:owner'], deadlineAt: '', deadlineAtRaw: '', escalationIndex: 0 }
    ],
    tasks: [activeTask, { ...activeTask, id: 'task-2', stageId: 'stage-2', stageKey: 'publish', stageName: 'Final approval', status: 'scheduled', statusTone: 'neutral', assignee: 'human:owner', assigneeId: 'owner', originalAssignee: 'human:owner', actionable: false }],
    activeTask,
    notifications: [{ id: 'notification-1', channel: 'feishu', recipient: 'human:ding', eventType: 'task_assigned', status: 'delivered', statusTone: 'success', attemptCount: 1, providerReceipt: 'om_message_1', lastError: '', updatedAt: 'Aug 17, 09:01' }],
    audit: [{ id: 'audit-1', eventType: 'workflow_viewed', actor: 'human:ding', reason: '', createdAt: 'Aug 17, 09:05', metadata: {} }],
    receipts: [{ id: 'receipt-1', effect: 'authorize', argsHash: 'sha256:args', materialHash: 'sha256:material', resolverActor: 'human:ding', resolverReason: 'Reviewed', singleUse: true, createdAt: 'Aug 17, 09:15' }],
    createdAt: 'Aug 17, 09:00', createdAtRaw: '2026-08-17T09:00:00Z', updatedAt: 'Aug 17, 09:10', expiresAt: 'Aug 17, 10:00', expiresAtRaw: '2026-08-17T10:00:00Z'
  }
}
