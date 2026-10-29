import type {
  AssignTaskRequest,
  CreateMissionRequest,
  CreateTaskRequest,
  DiscussionMessageRequest,
  DiscussionReplyRequest,
  DiscussionDTO,
  EvidenceDTO,
  MissionControlViewDTO,
  MissionDTO,
  MissionListDTO,
  MissionOpsViewDTO,
  MissionSummaryDTO,
  ProgressGraphDTO,
  QADTO,
  QADecisionRequest,
  TaskBoardDTO,
  TeamDTO,
  SubmitTaskResultRequest
} from '#lib/types/api'
import type {
  BadgeTone,
  DiscussionMessageFormViewModel,
  DiscussionReplyFormViewModel,
  AgentLaneViewModel,
  ActionCardViewModel,
  ApprovalGateViewModel,
  CommandLinkViewModel,
  EvidenceGateViewModel,
  MissionControlViewModel,
  MissionCreateFormViewModel,
  MissionListItemViewModel,
  MissionListViewModel,
  MissionOpsViewModel,
  OpsFocusViewModel,
  ResourceRefViewModel,
  RuntimeSessionViewModel,
  MissionQaDecisionFormViewModel,
  MissionTaskAssignmentFormViewModel,
  MissionTaskFormViewModel,
  MissionTaskResultFormViewModel
} from '#lib/types/view-models'

export function mapMissionListView(dto: MissionListDTO): MissionListViewModel {
  const summariesById = new Map((dto.summaries ?? []).map((summary) => [summary.mission_id, summary]))
  const missions = (dto.missions ?? []).map((mission) => mapMissionItem(mission, summariesById.get(mission.mission_id)))

  return {
    summary: {
      total: dto.page?.total ?? missions.length,
      active: missions.filter((mission) => mission.status === 'active').length,
      pendingApprovals: missions.reduce((sum, mission) => sum + mission.pendingApprovalCount, 0),
      blockers: missions.reduce((sum, mission) => sum + mission.blockerCount, 0)
    },
    missions
  }
}

export function mapMissionControlView(dto: MissionControlViewDTO): MissionControlViewModel {
  return mapMissionControlViewFromSubviews(dto, {})
}

export function mapMissionOpsView(dto: MissionOpsViewDTO): MissionOpsViewModel {
  const runtimeSessions = (dto.runtime_sessions ?? []).map(mapRuntimeSession)
  const actionCards = (dto.action_cards ?? []).map(mapActionCard)
  const evidenceGates = (dto.evidence_gates ?? []).map(mapEvidenceGate)
  const approvalGates = (dto.approval_gates ?? []).map(mapApprovalGate)

  return {
    missionId: dto.mission_id,
    title: dto.title,
    status: dto.status,
    statusTone: statusTone(dto.status),
    phase: dto.phase || 'Not provided',
    currentFocus: (dto.current_focus ?? []).map(mapOpsFocus),
    agentLanes: (dto.agent_lanes ?? []).map(mapAgentLane),
    runtimeSessions,
    actionCards,
    evidenceGates,
    approvalGates,
    autonomyPolicy: {
      mode: dto.autonomy_policy?.mode || 'manual',
      autoStartAllowed: dto.autonomy_policy?.auto_start_allowed ?? false,
      maxRiskWithoutApproval: dto.autonomy_policy?.max_risk_without_approval || 'low',
      requiredApprovals: dto.autonomy_policy?.required_approvals ?? []
    },
    summary: {
      agentCount: dto.agent_lanes?.length ?? 0,
      runningSessions: runtimeSessions.filter((session) => session.status === 'queued' || session.status === 'running').length,
      blockedSessions: runtimeSessions.filter((session) => session.canRetry || session.isStale || session.laneHealth === 'stale').length,
      openActions: actionCards.filter((card) => card.status === 'open').length,
      missingEvidence: evidenceGates.filter((gate) => gate.status === 'missing').length,
      pendingApprovals: approvalGates.filter((gate) => gate.status === 'pending').length
    },
    updatedAt: formatDate(dto.updated_at),
    version: dto.version
  }
}

export function mapMissionControlViewFromSubviews(
  dto: MissionControlViewDTO,
  subviews: {
    team?: TeamDTO
    taskBoard?: TaskBoardDTO
    evidence?: EvidenceDTO
    qa?: QADTO
    discussion?: DiscussionDTO
    progressGraph?: ProgressGraphDTO
  }
): MissionControlViewModel {
  const team = subviews.team ?? dto.team
  const taskBoard = subviews.taskBoard ?? dto.task_board
  const evidence = subviews.evidence ?? dto.evidence
  const qa = subviews.qa ?? dto.qa
  const discussion = subviews.discussion ?? dto.discussion
  const progressGraph = subviews.progressGraph ?? dto.progress_graph
  const mission = mapMissionItem(dto.mission, {
    mission_id: dto.mission.mission_id,
    title: dto.mission.title,
    status: dto.mission.status,
    phase: dto.mission.phase,
    risk_level: dto.mission.risk_level,
    team_size: dto.summary.team_size,
    active_task_count: dto.summary.active_task_count,
    pending_approval_count: dto.summary.pending_approval_count,
    blocker_count: dto.summary.blocker_count,
    delivery_rate: dto.summary.delivery_rate,
    updated_at: dto.summary.updated_at
  })
  const teamNameById = new Map((team.members ?? []).map((member) => [member.agent_id, displayNameForTeamMember(member)]))
  const qaByTaskId = new Map<string, { ref: string; message: string }>()
  for (const check of qa.checks ?? []) {
    qaByTaskId.set(check.task_id, { ref: check.qa_ref, message: check.message })
  }
  for (const decision of [...(qa.approvals ?? []), ...(qa.rejections ?? [])]) {
    if (!qaByTaskId.has(decision.task_id)) qaByTaskId.set(decision.task_id, { ref: decision.qa_ref, message: decision.message })
  }
  for (const rework of qa.rework_requests ?? []) {
    if (!qaByTaskId.has(rework.task_id)) qaByTaskId.set(rework.task_id, { ref: rework.qa_ref, message: rework.message })
  }

  return {
    mission,
    summary: {
      activeTasks: dto.summary.active_task_count,
      pendingApprovals: dto.summary.pending_approval_count,
      risks: dto.summary.risk_count,
      blockers: dto.summary.blocker_count,
      teamSize: dto.summary.team_size,
      deliveryPercent: percent(dto.summary.delivery_rate),
      version: dto.version
    },
    team: (team.members ?? []).map((member) => ({
      id: member.agent_id,
      profileId: member.agent_profile_id || member.agent_id,
      name: displayNameForTeamMember(member),
      role: member.mission_role || member.role_name || 'member',
      type: member.agent_kind || 'Not provided',
      department: member.department || 'Not provided',
      position: member.position || 'Not provided',
      status: member.status || 'Not provided'
    })),
    events: (discussion.messages ?? []).map((message) => ({
      id: message.message_id,
      groupId: message.discussion_group_id,
      kind: message.kind || 'message',
      authorId: message.author_id,
      authorName: message.author_display_name || message.author_id,
      authorType: message.author_type || 'Not provided',
      body: message.body,
      mentions: message.mentions ?? [],
      replyToMessageId: message.reply_to_message_id || '',
      replyToAuthor: '',
      replyPreview: message.reply_preview || '',
      toolName: message.metadata?.tool_name || '',
      oldStatus: message.metadata?.old_status || '',
      newStatus: message.metadata?.new_status || '',
      postedAt: formatDate(message.posted_at)
    })),
    progressFocus: mapProgressFocus(progressGraph),
    tasks: (taskBoard.tasks ?? []).map((task) => {
      const qa = qaByTaskId.get(task.task_id)
      return {
      id: task.task_id,
      title: task.title || task.goal,
      description: task.worker_personal_goal_summary || task.goal || 'Not provided',
      goal: task.goal,
      boundary: task.worker_personal_goal_summary || 'Not provided',
      assigneeId: task.assignee || '',
      assigneeName: task.assignee_display_name || teamNameById.get(task.assignee || '') || task.assignee || 'Not provided',
      assigneeType: task.assignee_type || 'Not provided',
      status: task.status,
      statusTone: statusTone(task.status),
      priority: task.priority || 'Not provided',
      dueAt: formatDate(task.due_at),
      acceptanceCriteria: (task.acceptance_criteria ?? []).map((item) => item.description),
      deliverables: (task.expected_deliverables ?? []).map((item) => item.title),
      evidenceRequirements: (task.required_evidence ?? []).map((item) => item.description),
      latestResultRef: task.latest_result_ref || '',
      qaRef: qa?.ref || '',
      qaMessage: qa?.message || '',
      qaStatus: task.qa_status || 'Not provided',
      blockerRefs: task.blocker_refs ?? [],
      blockedReason: task.blocked_reason || '',
      updatedAt: formatDate(task.updated_at)
      }
    }),
    evidenceCount: evidence.evidence_items?.length ?? 0,
    qaCount: qa.checks?.length ?? 0,
    blockers: (dto.blockers ?? []).map((blocker) => ({ id: blocker.blocker_id, taskId: blocker.task_id, summary: blocker.summary })),
    discussionGroupId: discussion.discussion_group_id,
    discussionConversationId: discussion.conversation_id || '',
    discussionRestricted: discussion.restricted ?? false
  }
}

function mapProgressFocus(progressGraph: ProgressGraphDTO) {
  const nodesById = new Map((progressGraph.nodes ?? []).map((node) => [node.id, node]))
  return (progressGraph.current_focus ?? [])
    .map((id) => nodesById.get(id))
    .filter((node): node is NonNullable<typeof node> => Boolean(node))
    .map((node) => ({
      id: node.id,
      label: node.label,
      status: node.status,
      details: node.details || '',
      occurredAt: formatDate(node.occurred_at)
    }))
}

function mapOpsFocus(focus: NonNullable<MissionOpsViewDTO['current_focus']>[number]): OpsFocusViewModel {
  return {
    id: focus.focus_id,
    kind: focus.kind,
    priority: focus.priority || 'normal',
    tone: priorityTone(focus.priority),
    title: focus.title || 'Untitled focus',
    summary: focus.summary || '',
    missionId: focus.mission_id,
    taskId: focus.task_id || '',
    agentId: focus.agent_id || '',
    sourceType: focus.source_type || '',
    sourceId: focus.source_id || '',
    createdAt: formatDate(focus.created_at)
  }
}

function mapAgentLane(lane: NonNullable<MissionOpsViewDTO['agent_lanes']>[number]): AgentLaneViewModel {
  return {
    agentId: lane.agent_id,
    profileId: lane.agent_profile_id || lane.agent_id,
    instanceId: lane.agent_instance_id || '',
    agentKind: lane.agent_kind || 'agent',
    displayName: firstDisplayValue(lane.display_name, lane.agent_id) || lane.agent_id,
    missionRole: lane.mission_role || 'member',
    status: lane.status || 'available',
    statusTone: runtimeStatusTone(lane.status),
    activeTaskIds: lane.active_task_ids ?? [],
    waitingTaskIds: lane.waiting_task_ids ?? [],
    inboxOpenCount: lane.inbox_open_count ?? 0,
    runtimeSessions: (lane.runtime_sessions ?? []).map(mapRuntimeSession),
    resourceBindings: (lane.resource_bindings ?? []).map(mapResourceRef),
    nextActions: (lane.next_actions ?? []).map(mapCommandLink)
  }
}

function mapRuntimeSession(session: NonNullable<MissionOpsViewDTO['runtime_sessions']>[number]): RuntimeSessionViewModel {
  const status = session.status || 'unknown'
  return {
    runId: session.run_id,
    agentId: session.agent_id,
    taskId: session.task_id || '',
    runtimeKind: session.runtime_kind || '',
    queueId: session.queue_id || '',
    status,
    statusTone: runtimeStatusTone(status),
    lastEvent: session.last_event || '',
    errorSummary: session.error_summary || '',
    lastHeartbeatAt: formatDate(session.last_heartbeat_at),
    leaseExpiresAt: formatDate(session.lease_expires_at),
    isAlive: session.is_alive ?? false,
    isStale: session.is_stale ?? false,
    laneHealth: session.lane_health || '',
    startedAt: formatDate(session.started_at),
    updatedAt: formatDate(session.updated_at),
    canCancel: isCancelableRuntimeStatus(status),
    canRetry: isRetryableRuntimeStatus(status)
  }
}

function mapResourceRef(resource: NonNullable<NonNullable<MissionOpsViewDTO['agent_lanes']>[number]['resource_bindings']>[number]): ResourceRefViewModel {
  return {
    resourceType: resource.resource_type,
    resourceId: resource.resource_id,
    label: resource.alias || resource.resource_id,
    source: resource.source || ''
  }
}

function mapActionCard(card: NonNullable<MissionOpsViewDTO['action_cards']>[number]): ActionCardViewModel {
  return {
    actionId: card.action_id,
    kind: card.kind,
    priority: card.priority || 'normal',
    tone: priorityTone(card.priority),
    title: card.title || 'Action',
    summary: card.summary || '',
    missionId: card.mission_id,
    taskId: card.task_id || '',
    agentId: card.agent_id || '',
    sourceType: card.source_type || '',
    sourceId: card.source_id || '',
    status: card.status || 'open',
    commands: (card.commands ?? []).map(mapCommandLink),
    createdAt: formatDate(card.created_at)
  }
}

function mapCommandLink(command: NonNullable<NonNullable<MissionOpsViewDTO['action_cards']>[number]['commands']>[number]): CommandLinkViewModel {
  return {
    actionId: command.action_id,
    label: command.label,
    method: command.method,
    path: command.path,
    requiredPermission: command.required_permission || ''
  }
}

function mapEvidenceGate(gate: NonNullable<MissionOpsViewDTO['evidence_gates']>[number]): EvidenceGateViewModel {
  return {
    taskId: gate.task_id,
    status: gate.status,
    tone: evidenceGateTone(gate.status),
    requiredCount: gate.required_count,
    submittedCount: gate.submitted_count,
    missingRequirementIds: gate.missing_requirement_ids ?? [],
    submittedEvidenceIds: gate.submitted_evidence_ids ?? [],
    lastSubmittedAt: formatDate(gate.last_submitted_at)
  }
}

function mapApprovalGate(gate: NonNullable<MissionOpsViewDTO['approval_gates']>[number]): ApprovalGateViewModel {
  return {
    approvalId: gate.approval_id,
    approvalType: gate.approval_type,
    status: gate.status,
    tone: approvalGateTone(gate.status),
    priority: gate.priority || 'normal',
    missionId: gate.mission_id,
    taskId: gate.task_id || '',
    sourceType: gate.source_type || '',
    sourceId: gate.source_id || '',
    title: gate.title || 'Approval',
    summary: gate.summary || '',
    requestedBy: gate.requested_by || '',
    assignedTo: gate.assigned_to || '',
    evidenceRefs: gate.evidence_refs ?? [],
    commands: (gate.commands ?? []).map(mapCommandLink),
    createdAt: formatDate(gate.created_at),
    decidedAt: formatDate(gate.decided_at)
  }
}

export function mapMissionCreateRequest(form: MissionCreateFormViewModel): CreateMissionRequest {
  return {
    title: form.title.trim(),
    final_goal: form.finalGoal.trim(),
    business_direction: form.businessDirection.trim(),
    source_personal_assistant_conversation_id: form.sourcePersonalAssistantConversationId?.trim() || undefined
  }
}

export function mapMissionTaskCreateRequest(form: MissionTaskFormViewModel): CreateTaskRequest {
  const deliverables = form.expectedDeliverables
    .split(',')
    .map((title) => title.trim())
    .filter(Boolean)
    .map((title) => ({ title }))

  return {
    goal: form.goal.trim(),
    worker_personal_goal: form.goal.trim(),
    assignee_id: form.assigneeId.trim() || undefined,
    scope: form.scope.trim() || undefined,
    priority: form.priority || undefined,
    due_at: form.dueAt.trim() || undefined,
    expected_deliverables: deliverables.length > 0 ? deliverables : undefined
  }
}

export function mapMissionTaskAssignmentRequest(form: MissionTaskAssignmentFormViewModel): {
  taskId: string
  body: AssignTaskRequest
} {
  return {
    taskId: form.taskId,
    body: {
      assignee_id: form.assigneeId.trim(),
      reason: form.reason.trim() || undefined
    }
  }
}

export function mapMissionTaskResultRequest(form: MissionTaskResultFormViewModel): {
  taskId: string
  body: SubmitTaskResultRequest
} {
  return {
    taskId: form.taskId,
    body: {
      agent_result_id: `${form.taskId}-manual-result`,
      agent_id: form.agentId.trim(),
      run_id: form.runId.trim() || 'manual',
      summary: form.summary.trim(),
      created_at: new Date().toISOString()
    }
  }
}

export function mapMissionQaDecisionRequest(form: MissionQaDecisionFormViewModel): {
  qaId: string
  body: QADecisionRequest
} {
  return {
    qaId: form.qaId,
    body: {
      task_id: form.taskId,
      decision: form.decision,
      message: form.message.trim() || undefined
    }
  }
}

export function mapMissionDiscussionMessageRequest(form: DiscussionMessageFormViewModel): DiscussionMessageRequest {
  const content = form.content.trim()
  return {
    discussion_group_id: form.discussionGroupId,
    content,
    summary: content.slice(0, 120),
    message_kind: 'request',
    visibility: 'mission'
  }
}

export function mapMissionDiscussionReplyRequest(form: DiscussionReplyFormViewModel & { inboxItemId?: string }): {
  messageId: string
  body: DiscussionReplyRequest
} {
  const content = form.content.trim()
  const inboxItemId = form.inboxItemId?.trim()
  return {
    messageId: form.messageId,
    body: {
      discussion_group_id: form.discussionGroupId,
      content,
      summary: content.slice(0, 120),
      inbox_item_id: inboxItemId || undefined
    }
  }
}

function mapMissionItem(mission: MissionDTO, summary?: MissionSummaryDTO): MissionListItemViewModel {
  const riskLevel = summary?.risk_level || mission.risk_level || 'Not provided'

  return {
    id: mission.mission_id,
    title: summary?.title || mission.title,
    description: mission.final_goal || 'Not provided',
    status: summary?.status || mission.status,
    statusTone: statusTone(summary?.status || mission.status),
    phase: normalizePhase(summary?.phase || mission.phase || 'Not provided'),
    riskLevel,
    riskTone: riskTone(riskLevel),
    teamSize: summary?.team_size ?? 0,
    activeTaskCount: summary?.active_task_count ?? 0,
    activeRunCount: summary?.active_run_count ?? 0,
    pendingApprovalCount: summary?.pending_approval_count ?? 0,
    blockerCount: summary?.blocker_count ?? 0,
    deliveryPercent: percent(summary?.delivery_rate ?? 0),
    deadline: formatDate(mission.deadline),
    goals: [mission.final_goal].filter((goal): goal is string => Boolean(goal)),
    createdBy: 'backend',
    updatedAt: formatDate(summary?.updated_at || mission.created_at),
    updatedAtRaw: summary?.updated_at || mission.created_at,
    originKind: mission.origin_kind || '',
    originId: mission.origin_id || '',
    originTodoId: mission.origin_todo_id || '',
    executionAttempt: mission.execution_attempt ?? 0,
    discussionMode: mission.discussion_mode || '',
    conversationId: mission.conversation_id || '',
    originDevelopmentProjectId: mission.origin_development_project_id || ''
  }
}

function percent(value: number) {
  return Math.round(Math.max(0, Math.min(1, value)) * 100)
}

function riskTone(risk: string): BadgeTone {
  if (risk === 'high' || risk === 'critical') return 'danger'
  if (risk === 'medium') return 'warning'
  if (risk === 'low') return 'success'
  return 'neutral'
}

function statusTone(status: string): BadgeTone {
  if (status === 'active' || status === 'completed' || status === 'approved') return 'success'
  if (status === 'paused') return 'warning'
  if (status === 'blocked' || status === 'failed') return 'danger'
  if (status === 'pending' || status === 'todo' || status === 'created' || status === 'assigned' || status === 'started') return 'warning'
  return 'neutral'
}

function runtimeStatusTone(status: string | undefined): BadgeTone {
  switch ((status ?? '').toLowerCase()) {
    case 'running':
    case 'queued':
    case 'working':
      return 'info'
    case 'completed':
    case 'available':
      return 'success'
    case 'failed':
    case 'timed_out':
    case 'timeout':
    case 'cancelled':
    case 'blocked':
    case 'stale':
      return 'danger'
    case 'invited':
    case 'pending':
      return 'warning'
    default:
      return 'neutral'
  }
}

function priorityTone(priority: string | undefined): BadgeTone {
  switch ((priority ?? '').toLowerCase()) {
    case 'critical':
    case 'high':
      return 'warning'
    case 'low':
      return 'neutral'
    default:
      return 'info'
  }
}

function evidenceGateTone(status: string): BadgeTone {
  if (status === 'complete' || status === 'submitted') return 'success'
  if (status === 'missing') return 'danger'
  return 'neutral'
}

function approvalGateTone(status: string): BadgeTone {
  if (status === 'approved' || status === 'done') return 'success'
  if (status === 'rejected' || status === 'blocked') return 'danger'
  if (status === 'pending') return 'warning'
  return 'neutral'
}

function isCancelableRuntimeStatus(status: string) {
  return status === 'queued' || status === 'running'
}

function isRetryableRuntimeStatus(status: string) {
  return status === 'failed' || status === 'timed_out' || status === 'cancelled' || status === 'blocked'
}

function normalizePhase(phase: string) {
  if (phase === 'execution') return 'execute'
  return phase
}

function displayNameForTeamMember(member: { agent_id: string; display_name?: string; real_name?: string; role_name?: string; mission_role?: string }) {
  return firstDisplayValue(member.display_name, member.real_name) || bootstrappedMemberDisplayName(member) || firstDisplayValue(member.role_name, member.agent_id) || 'Not provided'
}

function firstDisplayValue(...values: Array<string | undefined>) {
  for (const value of values) {
    const trimmed = value?.trim() ?? ''
    if (trimmed && !isInternalAgentIdentifier(trimmed)) return trimmed
  }
  return ''
}

function isInternalAgentIdentifier(value: string) {
  return value.includes(':') || /^(agent|human|[\w-]*agent)(?:-[\w]+)*$/i.test(value)
}

function bootstrappedMemberDisplayName(member: { agent_id: string; role_name?: string; mission_role?: string }) {
  const role = [member.role_name, member.mission_role].map((value) => normalizeRoleName(value ?? ''))
  if (role.includes('realhumancontributor')) return '真人用户'
  return ''
}

function normalizeRoleName(value: string) {
  return value.trim().replace(/[\s_-]+/g, '').toLowerCase()
}

function formatDate(value: string | undefined) {
  if (!value) return 'Not provided'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return new Intl.DateTimeFormat('en', { month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(parsed)
}
