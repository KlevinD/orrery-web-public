import { mapHILApprovalMaterial } from './hil'
import type { HILWorkflowInstanceDetailDTO } from '#lib/types/api'
import type {
  BadgeTone,
  ApprovalArtifactViewModel,
  BusinessApprovalMaterialViewModel,
  HILWorkflowAuditViewModel,
  HILWorkflowInstanceViewModel,
  HILWorkflowNotificationViewModel,
  HILWorkflowReceiptViewModel,
  HILWorkflowStageViewModel,
  HILWorkflowTaskViewModel
} from '#lib/types/view-models'

export function mapHILWorkflowInstance(
  dto: HILWorkflowInstanceDetailDTO,
  viewerActorIds: string[] = []
): HILWorkflowInstanceViewModel {
  const viewerIds = new Set(viewerActorIds.map((value) => value.trim()).filter(Boolean))
  const payload = dto.decision.payload_preview ?? {}
  const isBusinessApproval = payloadString(payload.approval_request_kind) === 'business'
    || dto.case.action_type_id === 'business_approval'
    || dto.case.tool_name === 'request_business_approval'
    || dto.decision.action === 'business.approval.request'
  const businessMaterial = mapBusinessApprovalMaterial(payload.business_approval_material)
  const materialComplete = !isBusinessApproval || businessApprovalMaterialComplete(businessMaterial)
  const stages = dto.stages
    .map(mapWorkflowStage)
    .sort((left, right) => left.order - right.order)
  const stageById = new Map(stages.map((stage) => [stage.id, stage]))
  const tasks = dto.tasks.map((task): HILWorkflowTaskViewModel => {
    const stage = stageById.get(task.stage_instance_id)
    const assignee = actorKey(task.current_assignee)
    return {
      id: task.id,
      stageId: task.stage_instance_id,
      stageKey: stage?.key ?? '',
      stageName: stage?.name ?? '',
      status: task.status,
      statusTone: workflowStatusTone(task.status),
      assignee,
      assigneeId: task.current_assignee.id,
      originalAssignee: actorKey(task.original_assignee),
      decision: task.decision ?? '',
      reason: task.reason ?? '',
      transferCount: task.transfer_count,
      transferReason: task.transfer_reason ?? '',
      deadlineAt: formatDate(task.deadline_at),
      deadlineAtRaw: task.deadline_at ?? '',
      actionable: materialComplete && dto.instance.status === 'running' && task.status === 'pending' && stage?.status === 'active'
        && (viewerIds.has(task.current_assignee.id) || viewerIds.has(assignee))
    }
  })
  const material = mapHILApprovalMaterial(payload)
  const title = businessMaterial?.title || (material.title === 'Decision context'
    ? (dto.case.tool_name || dto.decision.action)
    : material.title)
  return {
    id: dto.instance.id,
    caseId: dto.case.id,
    decisionRequestId: dto.instance.decision_request_id,
    status: dto.instance.status,
    statusTone: workflowStatusTone(dto.instance.status),
    revision: dto.instance.revision,
    version: dto.instance.version,
    predecessorInstanceId: dto.instance.predecessor_instance_id ?? '',
    title,
    summary: businessMaterial?.decisionRequest
      || payloadString(payload.approval_summary)
      || payloadString(payload.summary)
      || material.content
      || dto.decision.purpose
      || dto.decision.route.reasons[0]
      || title,
    requester: actorKey(dto.case.requester),
    requesterId: dto.case.requester.id,
    toolName: dto.case.tool_name ?? '',
    resource: actorKey(dto.case.resource),
    riskLevel: dto.decision.risk_level,
    currentStageKey: dto.instance.current_stage_key ?? '',
    definitionHash: dto.instance.definition_hash,
    materialHash: dto.instance.material_hash,
    argsHash: dto.instance.args_hash,
    material,
    isBusinessApproval,
    materialComplete,
    businessMaterial,
    evidenceRefs: uniqueStrings([...(dto.decision.context.evidence_refs ?? []), ...payloadStringArray(payload.evidence_refs)]),
    artifacts: mapBusinessApprovalArtifacts(payload.artifacts),
    stages,
    tasks,
    activeTask: tasks.find((task) => task.actionable),
    notifications: dto.notifications.map(mapWorkflowNotification),
    audit: dto.audit.map(mapWorkflowAudit),
    receipts: (dto.receipts ?? []).map(mapWorkflowReceipt),
    createdAt: formatDate(dto.instance.created_at),
    createdAtRaw: dto.instance.created_at,
    updatedAt: formatDate(dto.instance.updated_at),
    expiresAt: formatDate(dto.instance.expires_at),
    expiresAtRaw: dto.instance.expires_at ?? ''
  }
}

function mapWorkflowStage(stage: HILWorkflowInstanceDetailDTO['stages'][number]): HILWorkflowStageViewModel {
  return {
    id: stage.id,
    key: stage.definition.key,
    name: stage.definition.name,
    order: stage.definition.order,
    type: stage.definition.type,
    status: stage.status,
    statusTone: workflowStatusTone(stage.status),
    participantLabels: stage.resolved_participants.map(actorKey),
    deadlineAt: formatDate(stage.deadline_at),
    deadlineAtRaw: stage.deadline_at ?? '',
    escalationIndex: stage.escalation_index
  }
}

function mapWorkflowNotification(notification: HILWorkflowInstanceDetailDTO['notifications'][number]): HILWorkflowNotificationViewModel {
  return {
    id: notification.id,
    channel: notification.channel,
    recipient: actorKey(notification.recipient),
    eventType: notification.event_type,
    status: notification.status,
    statusTone: workflowStatusTone(notification.status),
    attemptCount: notification.attempt_count,
    providerReceipt: notification.provider_receipt ?? '',
    lastError: notification.last_error ?? '',
    updatedAt: formatDate(notification.updated_at)
  }
}

function mapWorkflowAudit(event: HILWorkflowInstanceDetailDTO['audit'][number]): HILWorkflowAuditViewModel {
  return {
    id: event.id,
    eventType: event.event_type,
    actor: actorKey(event.actor),
    reason: event.reason ?? '',
    createdAt: formatDate(event.created_at),
    metadata: event.metadata ?? {}
  }
}

function mapWorkflowReceipt(receipt: HILWorkflowInstanceDetailDTO['receipts'][number]): HILWorkflowReceiptViewModel {
  const resolverActor = receipt.resolver_actor_id
    ? `${receipt.resolver_actor_kind || 'human'}:${receipt.resolver_actor_id}`
    : ''
  return {
    id: receipt.id,
    effect: receipt.effect,
    argsHash: receipt.args_hash,
    materialHash: receipt.material_hash ?? '',
    resolverActor,
    resolverReason: receipt.resolver_reason ?? '',
    singleUse: receipt.single_use,
    createdAt: formatDate(receipt.created_at)
  }
}

function actorKey(actor: { kind: string; id: string }) {
  return `${actor.kind}:${actor.id}`
}

function workflowStatusTone(status: string): BadgeTone {
  if (['approved', 'passed', 'delivered'].includes(status)) return 'success'
  if (['rejected', 'failed', 'expired', 'needs_attention'].includes(status)) return 'danger'
  if (['running', 'active', 'pending', 'processing', 'changes_requested'].includes(status)) return 'warning'
  return 'neutral'
}

function payloadString(value: unknown) {
  return typeof value === 'string' ? value.trim() : ''
}

function mapBusinessApprovalMaterial(value: unknown): BusinessApprovalMaterialViewModel | undefined {
  if (!isRecord(value)) return undefined
  return {
    language: payloadString(value.language) || 'zh-CN',
    title: payloadString(value.title),
    decisionRequest: payloadString(value.decision_request),
    businessContext: payloadString(value.business_context),
    proposedAction: payloadString(value.proposed_action),
    expectedResult: payloadString(value.expected_result),
    impactScope: payloadString(value.impact_scope),
    risks: Array.isArray(value.risks) ? value.risks.flatMap((risk) => {
      if (!isRecord(risk)) return []
      return [{ description: payloadString(risk.description), mitigation: payloadString(risk.mitigation) }]
    }) : []
  }
}

function businessApprovalMaterialComplete(material?: BusinessApprovalMaterialViewModel) {
  if (!material) return false
  return [material.title, material.decisionRequest, material.businessContext, material.proposedAction, material.expectedResult, material.impactScope].every(Boolean)
    && material.risks.length > 0
    && material.risks.every((risk) => Boolean(risk.description && risk.mitigation))
}

function mapBusinessApprovalArtifacts(value: unknown): ApprovalArtifactViewModel[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((artifact) => {
    if (!isRecord(artifact)) return []
    const label = payloadString(artifact.label)
    const url = payloadString(artifact.url)
    if (!label || !url) return []
    return [{ label, url, kind: payloadString(artifact.kind) }]
  })
}

function payloadStringArray(value: unknown) {
  if (!Array.isArray(value)) return []
  return value.map(payloadString).filter(Boolean)
}

function uniqueStrings(values: string[]) {
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))]
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function formatDate(value?: string) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(undefined, {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false
  }).format(date)
}
