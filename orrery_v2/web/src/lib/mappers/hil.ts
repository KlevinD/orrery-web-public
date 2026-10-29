import type { HILCouncilSessionDTO, HILDecisionMutationRequest, HILDecisionPageDTO } from '#lib/types/api'
import type {
  BadgeTone,
  ApprovalDecisionFormViewModel,
  HILCouncilViewModel,
  HILDecisionItemViewModel,
  ApprovalItemViewModel,
  ApprovalPayloadViewModel
} from '#lib/types/view-models'

export function mapHILDecisionPage(dto: HILDecisionPageDTO): HILDecisionItemViewModel[] {
  return dto.items.map((decision) => ({
    id: decision.id,
    councilId: decision.council_id ?? '',
    routeMode: decision.route.mode,
    councilStage: Boolean(decision.council_id),
    action: decision.action,
    resource: actorKey(decision.resource),
    purpose: decision.purpose ?? '',
    requester: actorKey(decision.requester),
    riskLevel: decision.risk_level,
    riskTone: riskTone(decision.risk_level),
    status: decision.status,
    statusTone: decisionStatusTone(decision.status),
    effect: decision.route.council_effect ?? '',
    reasons: decision.route.reasons,
    evidenceRefs: decision.context.evidence_refs ?? [],
    contextBundleId: decision.context.bundle_id,
    contextComplete: decision.context.complete,
    contextConfidence: decision.context.confidence ?? 0,
    payloadPreview: decision.payload_preview ?? {},
    createdAt: formatHILDate(decision.created_at),
    createdAtRaw: decision.created_at,
    expiresAt: decision.expires_at ? formatHILDate(decision.expires_at) : '',
    expiresAtRaw: decision.expires_at ?? ''
  }))
}

export function collapseRepeatedPendingRuntimeApprovals(items: HILDecisionItemViewModel[]) {
  const seen = new Set<string>()
  return items.filter((item) => {
    const key = repeatedResponsibilityApprovalKey(item)
    if (!key) return true
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

function repeatedResponsibilityApprovalKey(item: HILDecisionItemViewModel) {
  if (item.status !== 'pending' || item.action !== 'runtime.approval') return ''
  const payload = item.payloadPreview ?? {}
  const runId = payloadString(payload.approval_run_id).trim()
  const conversationId = payloadString(payload.approval_conversation_id).trim()
  const materialHash = firstPayloadString(payload.approval_material_hash, payload.material_hash)
  const materialFields = isRecord(payload.fields) ? payload.fields : {}
  const accountId = firstPayloadString(payload.approval_material_account_id, materialFields.account_id)
  const intentId = firstPayloadString(payload.approval_material_intent_id, materialFields.intent_id)
  if (!runId.startsWith('responsibility-run-') || !conversationId || !materialHash) return ''
  return JSON.stringify([
    item.routeMode,
    conversationId,
    payloadString(payload.approval_assigned_to).trim(),
    payloadString(payload.approval_tool_name).trim(),
    accountId,
    intentId,
    materialHash
  ])
}

function firstPayloadString(...values: unknown[]) {
  for (const value of values) {
    const text = payloadString(value).trim()
    if (text) return text
  }
  return ''
}

/** Adapt a new HIL decision to the shared approval queue/detail components. */
export function mapHILDecisionToApprovalItem(
  item: HILDecisionItemViewModel,
  mode: 'hil' | 'approval'
): ApprovalItemViewModel {
  const pending = item.status === 'pending'
  const approved = item.status === 'resolved'
  const displayStatus = pending ? 'pending' : approved ? 'approved' : 'rejected'
  const actionPath = `/api/hil/decisions/${encodeURIComponent(item.id)}/decision`
  const resource = item.resource || 'Not provided'
  const payloadPreview = item.payloadPreview ?? {}
  const approvalAssignee = payloadString(payloadPreview.approval_assigned_to)
  const requesterName = firstPayloadString(
    payloadPreview.approval_requester_name,
    payloadPreview.requested_by_name,
    payloadPreview.requester_name
  )
  const approvalAssigneeName = firstPayloadString(
    payloadPreview.approval_reviewer_name,
    payloadPreview.approval_assigned_to_name,
    payloadPreview.assigned_to_name
  )
  const material = mapHILApprovalMaterial(payloadPreview)
  const title = approvalTitle(payloadPreview, material, mode, item.purpose)
  const summary = firstNaturalApprovalText(
    payloadString(payloadPreview.summary),
    material.content,
    item.purpose
  )
  const reviewable = Boolean(summary || material.content.trim())
  return {
    id: item.id,
    type: mode,
    displayKind: mode === 'hil' ? 'hil_decision' : 'approval_decision',
    displayTone: item.riskTone,
    typeLabel: mode === 'hil' ? 'HIL' : 'Approval',
    status: displayStatus,
    statusTone: approved ? 'success' : displayStatus === 'rejected' ? 'danger' : 'warning',
    priority: item.riskLevel || 'normal',
    priorityTone: item.riskTone,
    missionId: hilDecisionMissionId(item) || 'Not provided',
    taskId: '',
    sourceLabel: `hil:${mode}`,
    provider: 'Orrery HIL',
    toolName: payloadString(payloadPreview.approval_tool_name) || item.action,
    title,
    summary,
    details: '',
    riskSummary: '',
    requestedBy: item.requester,
    requestedByName: requesterName || undefined,
    assignedTo: approvalAssignee || 'Not provided',
    assignedToName: approvalAssigneeName || undefined,
    organizationCode: 'Not provided',
    organizationName: 'Not provided',
    requestedByActorLabel: item.requester,
    targetActorLabel: resource,
    policyId: 'hil',
    riskLevel: item.riskLevel || 'Not provided',
    decisionEffect: item.effect || 'requires_approval',
    payloadPreview: material,
    choices: [],
    evidenceRefs: item.evidenceRefs,
    createdAt: item.createdAt,
    createdAtRaw: item.createdAtRaw,
    expiresAt: item.expiresAt ?? '',
    expiresAtRaw: item.expiresAtRaw ?? '',
    actions: pending && reviewable ? [
      {
        id: 'approve', label: 'Approve', style: 'primary', decision: 'approved', effect: 'once', inputKind: 'message',
        defaultMessage: '', method: 'POST', path: actionPath, reasonRequired: false, confirmationRequired: true,
        allowsModification: false, rememberAllowed: false
      },
      {
        id: 'reject', label: 'Reject', style: 'danger', decision: 'denied', effect: 'deny', inputKind: 'reason',
        defaultMessage: '', method: 'POST', path: actionPath, reasonRequired: true, confirmationRequired: true,
        allowsModification: false, rememberAllowed: false
      }
    ] : []
  }
}

export function hilDecisionMissionId(item: HILDecisionItemViewModel) {
  const payload = item.payloadPreview ?? {}
  const fields = isRecord(payload.fields) ? payload.fields : {}
  const mission = isRecord(payload.mission) ? payload.mission : {}
  const candidate = firstPayloadString(
    payload.mission_id,
    payload.missionId,
    payload.approval_mission_id,
    payload.approvalMissionId,
    fields.mission_id,
    fields.missionId,
    mission.mission_id,
    mission.id
  )
  if (candidate && candidate !== 'Not provided') return candidate
  return item.resource.startsWith('mission:') ? item.resource.slice('mission:'.length).trim() : ''
}

const priorityPayloadFieldKeys = ['summary', 'steps', 'source_refs', 'approval_assigned_to']
const materialStructuralKeys = new Set(['content', 'fields', 'artifacts', 'material_kind', 'title', 'language'])
const readableApprovalKeys = [
  'approval_summary', 'approval_description', 'request_context', 'description', 'message', 'prompt', 'question',
  'final_goal', 'root_goal', 'goal', 'task_goal', 'business_direction', 'plan_summary', 'query', 'summary'
]
const readableApprovalFallbackKeys = ['payload_preview', 'command', 'path', 'url']

export function mapHILApprovalMaterial(payload: Record<string, unknown>): ApprovalPayloadViewModel {
  const explicitContent = typeof payload.content === 'string' ? payload.content : ''
  const content = (genericRuntimeApprovalText(explicitContent.trim()) ? '' : explicitContent) || mapReadableApprovalContent(payload)
  const kind = payloadString(payload.material_kind) || 'hil_decision'
  const title = payloadString(payload.title) || 'Decision context'
  const explicitLanguage = payloadString(payload.language)
  return {
    kind,
    language: explicitLanguage || (content ? (kind === 'hil_decision' ? 'text' : 'markdown') : 'json'),
    title,
    content,
    fields: [
      ...mapPayloadFields(payload),
      ...mapNestedMaterialFields(payload.fields),
      ...mapMaterialArtifacts(payload.artifacts)
    ]
  }
}

function mapReadableApprovalContent(payload: Record<string, unknown>) {
  const steps = mapReadableApprovalSteps(payload.steps)
  if (steps) return steps

  const nestedFields = isRecord(payload.fields) ? payload.fields : {}
  for (const key of readableApprovalKeys) {
    const value = firstReadableApprovalValue(payload[key], nestedFields[key])
    if (value) return value
  }
  for (const key of readableApprovalFallbackKeys) {
    const value = firstReadableApprovalValue(payload[key], nestedFields[key])
    if (value) return value
  }
  return ''
}

function mapReadableApprovalSteps(value: unknown) {
  if (!Array.isArray(value)) return ''
  const steps = value.flatMap((entry) => {
    if (typeof entry === 'string' && entry.trim()) return [entry.trim()]
    if (!isRecord(entry)) return []
    for (const key of ['title', 'summary', 'description', 'goal', 'name', 'action']) {
      const text = payloadString(entry[key]).trim()
      if (text) return [text]
    }
    return []
  })
  return steps.map((step, index) => `${index + 1}. ${step}`).join('\n')
}

function firstReadableApprovalValue(...values: unknown[]) {
  for (const value of values) {
    if (typeof value !== 'string') continue
    const text = value.trim()
    if (text && !genericRuntimeApprovalText(text)) return text
  }
  return ''
}

function approvalTitle(
  payload: Record<string, unknown>,
  material: ApprovalPayloadViewModel,
  mode: 'hil' | 'approval',
  purpose: string
) {
  const explicit = payloadString(payload.title).trim()
  if (explicit && explicit !== 'Decision context') return explicit
  if (material.title && material.title !== 'Decision context') return material.title
  const runtimePurpose = purpose.trim()
  if (/^runtime approval required for\b/i.test(runtimePurpose)) return runtimePurpose
  return mode === 'hil' ? 'Human review request' : 'Fixed approval request'
}

function firstNaturalApprovalText(...values: Array<string | undefined>) {
  for (const value of values) {
    const text = value?.trim() ?? ''
    if (!text || genericRuntimeApprovalText(text)) continue
    return text
  }
  return ''
}

function genericRuntimeApprovalText(value: string) {
  return /^runtime approval required for\b/i.test(value)
    || /^(?:runtime\.approval|hil decision|approval decision|human review required)$/i.test(value)
}

function mapPayloadFields(payload: Record<string, unknown>) {
  const keys = Object.keys(payload).filter((key) => !materialStructuralKeys.has(key))
  const ordered = [
    ...priorityPayloadFieldKeys.filter((key) => keys.includes(key)),
    ...keys.filter((key) => !priorityPayloadFieldKeys.includes(key)).sort()
  ]
  return ordered.map((key) => ({ key, value: payloadString(payload[key]) }))
}

function mapNestedMaterialFields(value: unknown) {
  if (!isRecord(value)) return []
  return Object.keys(value).sort().map((key) => ({ key, value: payloadString(value[key]) }))
}

function mapMaterialArtifacts(value: unknown) {
  if (!Array.isArray(value)) return []
  return value.flatMap((entry, index) => {
    if (!isRecord(entry)) return []
    const role = payloadString(entry.role) || String(index + 1)
    const details = [
      payloadString(entry.path),
      payloadString(entry.media_type),
      typeof entry.size_bytes === 'number' ? `${entry.size_bytes} bytes` : '',
      payloadString(entry.sha256)
    ].filter(Boolean)
    return details.length > 0 ? [{ key: `artifact.${role}`, value: details.join(' | ') }] : []
  })
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function payloadString(value: unknown) {
  if (value === undefined || value === null) return ''
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') return String(value)
  try {
    return JSON.stringify(value) ?? String(value)
  } catch {
    return String(value)
  }
}

export function mapHILDecisionRequest(form: ApprovalDecisionFormViewModel): {
  decisionId: string
  body: HILDecisionMutationRequest
} {
  const rejected = ['reject', 'deny', 'denied'].includes(form.actionId.trim().toLowerCase())
  const reason = form.message.trim()
  return {
    decisionId: form.approvalId,
    body: {
      decision: rejected ? 'rejected' : 'approved',
      ...(reason ? { reason } : {}),
      action_id: form.actionId
    }
  }
}

export function mapHILCouncil(dto: HILCouncilSessionDTO): HILCouncilViewModel {
  const contributions = dto.contributions ?? []
  const votes = dto.votes ?? []
  const nameByActor = new Map(dto.participants.map((participant) => [
    actorKey(participant.actor),
    participant.display_name || participant.actor.id
  ]))
  const voteByActor = new Map(votes.map((vote) => [actorKey(vote.actor), vote.choice]))
  const contributed = new Set(contributions.map((contribution) => actorKey(contribution.actor)))
  return {
    id: dto.id,
    requestId: dto.request_id,
    status: dto.status,
    statusTone: councilStatusTone(dto.status),
    riskLevel: dto.risk_level,
    effect: dto.effect ?? '',
    quorum: dto.quorum,
    approvalThreshold: dto.approval_threshold,
    approvals: votes.filter((vote) => vote.choice === 'approve').length,
    rejections: votes.filter((vote) => vote.choice === 'reject').length,
    abstentions: votes.filter((vote) => vote.choice === 'abstain').length,
    participants: dto.participants.map((participant) => {
      const key = actorKey(participant.actor)
      return {
        actorKey: key,
        actorId: participant.actor.id,
        actorKind: participant.actor.kind,
        displayName: participant.display_name || participant.actor.id,
        role: participant.role ?? '',
        required: participant.required,
        contributed: contributed.has(key),
        vote: voteByActor.get(key) ?? ''
      }
    }),
    contributions: contributions.map((contribution) => ({
      actorKey: actorKey(contribution.actor),
      actorName: nameByActor.get(actorKey(contribution.actor)) ?? contribution.actor.id,
      position: contribution.position,
      reasoning: contribution.reasoning,
      evidenceRefs: contribution.evidence_refs ?? [],
      updatedAt: formatHILDate(contribution.updated_at)
    })),
    dissent: (dto.dissent ?? []).map(actorKey),
    expiresAt: formatHILDate(dto.expires_at),
    resolvedAt: dto.resolved_at ? formatHILDate(dto.resolved_at) : ''
  }
}

function actorKey(actor: { kind: string; id: string }) {
  return `${actor.kind}:${actor.id}`
}

function riskTone(risk: string): BadgeTone {
  if (risk === 'high' || risk === 'critical') return 'danger'
  if (risk === 'medium') return 'warning'
  return 'neutral'
}

function decisionStatusTone(status: string): BadgeTone {
  if (status === 'pending') return 'warning'
  if (status === 'denied') return 'danger'
  return 'success'
}

function councilStatusTone(status: string): BadgeTone {
  if (status === 'open') return 'warning'
  if (status === 'passed') return 'success'
  if (status === 'rejected' || status === 'expired') return 'danger'
  return 'neutral'
}

function formatHILDate(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(undefined, {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false
  }).format(date)
}
