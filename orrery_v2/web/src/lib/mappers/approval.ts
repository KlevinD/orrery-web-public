import type { ApprovalActionDTO, ApprovalDecisionRequest, ApprovalPageDTO, ApprovalPayloadDTO } from '#lib/types/api'
import type {
  ApprovalActionViewModel,
  ApprovalCenterViewModel,
  ApprovalDecisionFormViewModel,
  ApprovalItemViewModel,
  ApprovalPayloadViewModel,
  BadgeTone
} from '#lib/types/view-models'

export function mapApprovalCenterView(dto: ApprovalPageDTO): ApprovalCenterViewModel {
  const items = (dto.items ?? [])
    .filter(isActionableApprovalItem)
    .map<ApprovalItemViewModel>((item) => {
      const kind = item.display_kind || displayKind(item.approval_type)
      const payloadPreview = mapPayloadPreview(item.payload_preview)
      const requestedBy = item.requested_by || item.requested_by_actor?.actor_id || 'Not provided'
      const triggeredBy = item.triggered_by || item.triggered_by_actor?.actor_id || ''
      const decidedBy = item.decided_by || item.decided_by_actor?.actor_id || ''
      return {
        id: item.approval_id,
        type: item.approval_type,
        displayKind: kind,
        displayTone: displayTone(kind),
        typeLabel: approvalTypeLabel(item.display_kind || item.approval_type),
        status: item.status,
        statusTone: statusTone(item.status),
        priority: item.priority || 'normal',
        priorityTone: priorityTone(item.priority),
        missionId: item.mission_id || 'Not provided',
        taskId: item.task_id || '',
        sourceLabel: `${item.source_type}:${item.source_id}`,
        discussionId: item.source_type === 'discussion' ? item.source_id : undefined,
        provider: item.provider || 'Not provided',
        toolName: item.tool_name || 'Not provided',
        title: item.title,
        summary: item.summary || 'Not provided',
        details: item.details || '',
        riskSummary: item.risk_summary || '',
        requestedBy,
        requestedByName: actorDisplayName(item.requested_by_actor, requestedBy),
        triggeredBy,
        triggeredByName: actorDisplayName(item.triggered_by_actor, triggeredBy),
        decidedBy,
        decidedByName: actorDisplayName(item.decided_by_actor, decidedBy),
        assignedTo: item.assigned_to || 'Not provided',
        organizationCode: item.organization_code || 'Not provided',
        organizationName: item.organization_name || 'Not provided',
        requestedByActorLabel: actorLabel(item.requested_by_actor),
        targetActorLabel: actorLabel(item.target_actor),
        policyId: item.policy_id || 'Not provided',
        riskLevel: item.risk_level || 'Not provided',
        decisionEffect: item.decision_effect || 'Not provided',
        payloadPreview,
		planDigest: payloadPreview.fields.find((field) => field.key === 'plan_digest')?.value ?? '',
        choices: (item.choices ?? []).map((choice) => ({
          id: choice.choice_id,
          label: choice.label,
          description: choice.description || ''
        })),
        evidenceRefs: item.evidence_refs ?? [],
        createdAt: formatDate(item.created_at),
        createdAtRaw: item.created_at || '',
        expiresAt: formatDate(item.expires_at),
        expiresAtRaw: item.expires_at || '',
        actions: approvalActionsForItem(item, kind).map(mapAction)
      }
    })

  return {
    summary: {
      total: items.length,
      pending: items.filter((item) => item.status === 'pending').length,
      decided: items.filter((item) => item.status !== 'pending').length,
      highPriority: items.filter((item) => item.priority === 'high' || item.priority === 'critical').length
    },
    items
  }
}

export function validateApprovalDecision(action: ApprovalActionViewModel, reason: string) {
  if ((action.reasonRequired || action.id === 'other') && reason.trim().length === 0) {
    return 'approvals.reasonRequired'
  }
}

export function validateApprovalDecisionForm(action: ApprovalActionViewModel, form: Pick<ApprovalDecisionFormViewModel, 'message' | 'modifiedInput' | 'choiceId'>) {
  if ((action.reasonRequired || action.id === 'other') && form.message.trim().length === 0) {
    return 'approvals.reasonRequired'
  }
  if (action.inputKind === 'modified_input' && (form.modifiedInput ?? '').trim().length === 0) {
    return 'approvals.modifiedInputRequired'
  }
  if (action.inputKind === 'choice_or_text' && !form.choiceId && form.message.trim().length === 0) {
    return 'approvals.answerRequired'
  }
}

export function mapApprovalDecisionRequest(form: ApprovalDecisionFormViewModel): {
  approvalId: string
  actionPath?: string
  body: ApprovalDecisionRequest
} {
  const body: ApprovalDecisionRequest = {
    action_id: form.actionId
  }
  const message = form.message.trim()
  const modifiedInput = form.modifiedInput?.trim()
  if (message) body.message = message
  if (modifiedInput) body.modified_input = modifiedInput
  if (form.choiceId) body.choice_id = form.choiceId
  if (form.remember) body.remember = form.remember
  if (form.taskId) body.task_id = form.taskId
  if (form.planDigest) body.plan_digest = form.planDigest
  return {
    approvalId: form.approvalId,
    actionPath: form.actionPath,
    body
  }
}

type ApprovalPageItemDTO = NonNullable<ApprovalPageDTO['items']>[number]

function isActionableApprovalItem(item: ApprovalPageItemDTO) {
  if (item.approval_type === 'inbox_action') return false
  if (item.source_type === 'inbox') return false
  if (item.approval_id?.startsWith('inbox:')) return false
  return true
}

function mapAction(action: ApprovalActionDTO): ApprovalActionViewModel {
  return {
    id: action.action_id,
    label: action.label,
    style: action.style || actionStyle(action.action_id),
    decision: action.decision || actionDecision(action.action_id),
    effect: action.effect || actionEffect(action.action_id),
    inputKind: action.input_kind || actionInputKind(action.action_id, action.reason_required === true),
    defaultMessage: action.default_message || '',
    method: action.method,
    path: action.path,
    reasonRequired: action.reason_required === true,
    confirmationRequired: action.confirmation_required === true,
    allowsModification: action.allows_modification === true,
    rememberAllowed: action.remember_allowed === true
  }
}

function approvalActionsForItem(item: ApprovalPageItemDTO, kind: string) {
  if (!approvalIsPending(item.status)) {
    return []
  }
  const suppliedActions = item.available_actions ?? []
  if (suppliedActions.some((action) => action.path?.startsWith('/api/hil/decisions/'))) {
    return suppliedActions
  }
  if (!providerApprovalKind(item, kind)) {
    return suppliedActions
  }
  const path = approvalActionPath(item)
  if (!path) return item.available_actions ?? []
  return unifiedProviderApprovalActions(path)
}

function approvalIsPending(status: string) {
  const normalized = status.trim().toLowerCase()
  return normalized === '' || normalized === 'pending' || normalized === 'open'
}

function providerApprovalKind(item: ApprovalPageItemDTO, kind: string) {
  if (item.approval_type === 'qa' || item.source_type === 'qa') return false
  switch (displayKind(kind || item.approval_type)) {
    case 'plan_approval':
      return false
    case 'command_execution':
    case 'file_change':
    case 'network_access':
    case 'tool_use':
    case 'user_input':
    case 'permission':
      return true
    default:
      break
  }
  if (['file_edit', 'permission_request', 'tool_access', 'mcp_elicitation'].includes(item.approval_type)) {
    return true
  }
  if (['discussion', 'runtime', 'codex', 'claude', 'openclaw'].includes(item.source_type)) {
    return true
  }
  return Boolean(item.provider || item.tool_name)
}

function approvalActionPath(item: ApprovalPageItemDTO) {
  if (item.source_type === 'discussion' && item.source_id && item.approval_id) {
    return `/api/discussions/${encodeURIComponent(item.source_id)}/approvals/${encodeURIComponent(item.approval_id)}/decision`
  }
  const firstPath = (item.available_actions ?? []).find((action) => action.path)?.path
  if (firstPath) return firstPath
  if (!item.approval_id) return ''
  return `/api/approvals/${encodeURIComponent(item.approval_id)}/decision`
}

function unifiedProviderApprovalActions(path: string): ApprovalActionDTO[] {
  return [
    providerApprovalAction('approve_once', 'Approve once', 'primary', 'approved', 'once', path),
    providerApprovalAction('approve_session', 'Approve for session', 'primary', 'approved', 'session', path, true),
    providerApprovalAction('deny', 'Deny', 'danger', 'denied', 'deny', path),
    providerApprovalAction('other', 'Other', 'secondary', 'approved', 'once', path)
  ]
}

function providerApprovalAction(actionId: string, label: string, style: string, decision: string, effect: string, path: string, rememberAllowed = false): ApprovalActionDTO {
  return {
    action_id: actionId,
    label,
    style,
    decision,
    effect,
    input_kind: 'message',
    method: 'POST',
    path,
    requires_body: true,
    confirmation_required: true,
    reason_required: false,
    remember_allowed: rememberAllowed
  }
}

function mapPayloadPreview(payload?: ApprovalPayloadDTO): ApprovalPayloadViewModel {
  const fields = payload && 'fields' in payload && payload.fields
    ? Object.entries(payload.fields).map(([key, value]) => ({ key, value: String(value) }))
    : []
  return {
    kind: payload && 'kind' in payload && payload.kind ? String(payload.kind) : '',
    language: payload && 'language' in payload && payload.language ? String(payload.language) : '',
    title: payload && 'title' in payload && payload.title ? String(payload.title) : '',
    content: payload && 'content' in payload && payload.content ? String(payload.content) : '',
    fields
  }
}

function displayKind(value: string) {
  if (value === 'mcp_elicitation') return 'user_input'
  if (value === 'tool_access') return 'tool_use'
  if (value === 'permission_request') return 'permission'
  return value || 'permission'
}

function approvalTypeLabel(value: string) {
  switch (displayKind(value)) {
    case 'command_execution':
      return 'Command'
    case 'file_change':
      return 'File change'
    case 'user_input':
      return 'Question'
    case 'tool_use':
      return 'Tool'
    case 'network_access':
      return 'Network'
    case 'plan_approval':
      return 'Plan'
    case 'evidence_review':
      return 'Evidence'
    case 'task_completion':
      return 'Completion'
    case 'qa':
      return 'QA'
    default:
      return value.replaceAll('_', ' ')
  }
}

function displayTone(kind: string): BadgeTone {
  switch (displayKind(kind)) {
    case 'command_execution':
    case 'file_change':
      return 'danger'
    case 'user_input':
    case 'plan_approval':
      return 'warning'
    case 'tool_use':
    case 'network_access':
      return 'warning'
    case 'evidence_review':
    case 'task_completion':
      return 'success'
    default:
      return 'neutral'
  }
}

function actorLabel(actor: { actor_type?: string; actor_id?: string } | undefined) {
  if (!actor?.actor_type || !actor.actor_id) return 'Not provided'
  return `${actor.actor_type}:${actor.actor_id}`
}

function actorDisplayName(actor: { actor_id?: string; display_name?: string } | undefined, fallback: string) {
  return actor?.display_name || actor?.actor_id || fallback
}

function statusTone(status: string): BadgeTone {
  if (status === 'pending') return 'warning'
  if (status === 'approved' || status === 'closed') return 'success'
  if (status === 'denied' || status === 'rejected') return 'danger'
  return 'neutral'
}

function priorityTone(priority = 'normal'): BadgeTone {
  if (priority === 'high' || priority === 'critical') return 'danger'
  if (priority === 'normal') return 'neutral'
  return 'warning'
}

function actionStyle(actionId: string) {
  if (['deny', 'reject', 'dismiss'].includes(actionId)) return 'danger'
  if (['modify', 'needs_context', 'approve_edited', 'other'].includes(actionId)) return 'secondary'
  return 'primary'
}

function actionDecision(actionId: string) {
  if (['deny', 'reject', 'dismiss'].includes(actionId)) return 'denied'
  if (['modify', 'needs_context'].includes(actionId)) return 'needs_input'
  if (actionId === 'answer') return 'answered'
  return 'approved'
}

function actionEffect(actionId: string) {
  if (actionId === 'approve_session') return 'session'
  if (actionId === 'approve_edited') return 'modified'
  if (actionId === 'modify') return 'modify'
  if (actionId === 'needs_context') return 'needs_context'
  if (['deny', 'reject', 'dismiss'].includes(actionId)) return 'deny'
  return 'once'
}

function actionInputKind(actionId: string, reasonRequired: boolean) {
  if (['modify', 'approve_edited'].includes(actionId)) return 'modified_input'
  if (actionId === 'answer') return 'choice_or_text'
  if (['deny', 'reject', 'needs_context', 'dismiss'].includes(actionId) || reasonRequired) return 'reason'
  return 'message'
}

function formatDate(value: string | undefined) {
  if (!value) return 'Not provided'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return new Intl.DateTimeFormat('en', { month: 'short', day: '2-digit', year: 'numeric' }).format(parsed)
}
