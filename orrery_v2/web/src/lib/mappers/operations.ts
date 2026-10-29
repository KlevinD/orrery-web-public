import type { AuditEventPageDTO, AuthIPLockPageDTO, EvaluationPageDTO, PermissionMatrixDTO, PermissionPolicyDTO, PermissionPolicyViewDTO } from '#lib/types/api'
import type {
  AuthIPLockCenterViewModel,
  AuditLogViewModel,
  BadgeTone,
  EvaluationCenterViewModel,
  EvaluationItemViewModel,
  PermissionCenterViewModel,
  PermissionPolicyCenterViewModel,
  PermissionPolicyCommandViewModel
} from '#lib/types/view-models'

export function mapAuditLogView(dto: AuditEventPageDTO): AuditLogViewModel {
  const events = (dto.events ?? []).map((event) => ({
    id: event.audit_id,
    actorId: event.actor_id,
    missionId: event.mission_id || 'platform',
    entity: `${event.entity_type}:${event.entity_id}`,
    action: event.action,
    result: event.result,
    severity: event.severity,
    severityTone: severityTone(event.severity),
    correlationId: event.correlation_id,
    summary: event.summary || event.action,
    metadataPairs: Object.entries(event.metadata ?? {}).map(([key, value]) => ({ key, value })),
    occurredAt: formatDateTime(event.occurred_at)
  }))

  return {
    summary: {
      total: dto.page?.total ?? events.length,
      warning: events.filter((event) => event.severity === 'warning').length,
      error: events.filter((event) => event.severity === 'error').length,
      succeeded: events.filter((event) => event.result === 'succeeded').length
    },
    events,
    page: {
      limit: dto.page?.limit ?? events.length,
      offset: dto.page?.offset ?? 0,
      total: dto.page?.total ?? events.length,
      returned: dto.page?.returned ?? events.length,
      hasMore: dto.page?.has_more ?? false
    }
  }
}

export function mapAuthIPLockView(dto: AuthIPLockPageDTO): AuthIPLockCenterViewModel {
  const locks = (dto.locks ?? []).map((lock) => ({
    ipAddress: lock.ip_address,
    status: lock.status,
    statusTone: authIPLockStatusTone(lock.status),
    failureCount: lock.failure_count ?? 0,
    lastUsername: lock.last_username ?? '-',
    lastReason: lock.last_reason ?? '-',
    lockedAt: formatDateTime(lock.locked_at),
    updatedAt: formatDateTime(lock.updated_at)
  }))

  return {
    summary: {
      active: locks.filter((lock) => lock.status === 'active').length,
      tracking: locks.filter((lock) => lock.status === 'tracking').length,
      totalFailures: locks.reduce((sum, lock) => sum + lock.failureCount, 0)
    },
    locks
  }
}

export function mapEvaluationCenterView(dto: EvaluationPageDTO): EvaluationCenterViewModel {
  const items = (dto.items ?? []).map((item) => {
    const assignedTasks = item.assigned_tasks || 0
    const completedTasks = item.completed_tasks || 0
    const delivery = assignedTasks > 0 ? Math.round((completedTasks / assignedTasks) * 100) : 0
    const metrics = {
      delivery,
      quality: percent(item.quality_score),
      onTime: percent(item.on_time_rate ?? 0),
      collaboration: percent(item.collaboration_score ?? 0),
      efficiency: percent(item.efficiency_score ?? (assignedTasks > 0 ? completedTasks / assignedTasks : 0)),
      stability: percent(item.stability_score ?? Math.max(0, 1 - ((item.rework_count ?? 0) + (item.blocker_count ?? 0)) / Math.max(1, assignedTasks)))
    }
    const fallbackCapabilities = [
      { name: '任务完成', score: metrics.delivery },
      { name: '质量稳定', score: metrics.quality },
      { name: '准时表现', score: metrics.onTime },
      { name: '协作响应', score: metrics.collaboration },
      { name: '运行效率', score: metrics.efficiency },
      { name: '稳定性', score: metrics.stability }
    ]
    const taskHistory = (item.task_history ?? []).map((task) => ({
      goal: fallback(task.goal, item.mission_id, '未记录总体目标'),
      responsibility: fallback(task.responsibility, '未记录负责工作'),
      type: fallback(task.type, '任务执行'),
      result: fallback(task.result, '未记录'),
      quality: task.quality || 0,
      tokenConsumed: task.token_consumed || 0,
      durationSeconds: task.duration_seconds || 0,
      risk: fallback(task.risk, '-'),
      time: formatDateTime(task.time)
    }))
    const tokenConsumed = item.token_consumed ?? taskHistory.reduce((sum, task) => sum + task.tokenConsumed, 0)
    const latestReview = item.latest_review
      ? {
          reviewId: item.latest_review.review_id,
          entityType: item.latest_review.entity_type,
          entityId: item.latest_review.entity_id,
          scopeKind: item.latest_review.scope_kind ?? '',
          scopeId: item.latest_review.scope_id ?? '',
          evaluatorAgentId: item.latest_review.evaluator_agent_id ?? '',
          conversationId: item.latest_review.conversation_id ?? '',
          scheduledRunId: item.latest_review.scheduled_run_id ?? '',
          windowDays: item.latest_review.window_days ?? 0,
          summary: item.latest_review.summary ?? '',
          estimatedHumanDays: item.latest_review.estimated_human_days ?? 0,
          evaluationTokenConsumed: item.latest_review.evaluation_token_consumed ?? 0,
          createdAt: formatDateTime(item.latest_review.created_at)
        }
      : undefined
    const estimatedHumanDays = item.investment?.estimated_human_days ?? latestReview?.estimatedHumanDays ?? 0
    const evaluationTokenConsumed = latestReview?.evaluationTokenConsumed ?? 0

    return {
      id: `${item.entity_type}:${item.entity_id}`,
      entityId: item.entity_id,
      entityType: item.entity_type,
      displayName: item.display_name || item.entity_id,
      missionId: item.mission_id || 'aggregate',
      roleName: fallback(item.role_name, item.position, item.display_name, item.entity_id),
      department: fallback(item.department, '未归属部门'),
      position: fallback(item.position, item.role_name, '未配置岗位'),
      runtimeKind: fallback(item.runtime_kind, '未配置运行方式'),
      assignedTasks,
      completedTasks,
      reworkCount: item.rework_count ?? 0,
      blockerCount: item.blocker_count ?? 0,
      resourceUsageCount: item.resource_usage_count ?? 0,
      discussionReplyCount: item.discussion_reply_count ?? 0,
      averageDurationSeconds: item.average_duration_seconds ?? 0,
      tokenConsumed,
      metrics,
      summary: fallback(item.summary, '结论：当前 Agent 已接入评估中心，等待更多任务、运行和协作数据形成稳定评价。'),
      capabilities: (item.capabilities ?? fallbackCapabilities).map((capability) => ({
        name: capability.name,
        score: clampScore(capability.score)
      })),
      investment: {
        tokenConsumed: item.investment?.token_consumed ?? tokenConsumed,
        estimatedHumanDays,
        evaluationTokenConsumed,
        returnMultiple: item.investment?.return_multiple ?? 0,
        summary: fallback(item.investment?.summary, latestReview?.summary, 'Token 只统计运行时真实用量；创造价值等待综合评价 Agent 估算人天。')
      },
      latestReview,
      taskHistory,
      trend: (item.trend ?? []).map((point) => ({
        date: point.date,
        tasks: point.tasks,
        score: clampScore(point.score),
        tokenConsumed: point.token_consumed ?? 0
      })),
      representativeCases: (item.representative_cases ?? []).map((example) => ({
        title: fallback(example.title, '未命名成果'),
        result: fallback(example.result, '未记录'),
        summary: fallback(example.summary, '未记录成果说明'),
        date: formatDate(example.date)
      })),
      suggestions: item.suggestions ?? [],
      advantages: item.advantages ?? [],
      risks: item.risks ?? [],
      scenarios: item.scenarios ?? [],
      lastActivityAt: formatDateTime(item.last_activity_at)
    } satisfies EvaluationItemViewModel
  })

  const averageQuality =
    items.length > 0 ? Math.round(items.reduce((sum, item) => sum + item.metrics.quality, 0) / items.length) : 0
  const completedTasks = items.reduce((sum, item) => sum + item.completedTasks, 0)
  const radarValues = [
    average(items.map((item) => item.metrics.delivery)),
    average(items.map((item) => item.metrics.quality)),
    average(items.map((item) => item.metrics.onTime)),
    average(items.map((item) => item.metrics.collaboration))
  ]

  return {
    summary: { total: items.length, completedTasks, averageQuality },
    items,
    radarOption: {
      radar: {
        indicator: [
          { name: '任务完成', max: 100 },
          { name: '质量稳定', max: 100 },
          { name: '准时表现', max: 100 },
          { name: '协作响应', max: 100 }
        ],
        axisName: { color: '#94a3b8' },
        splitLine: { lineStyle: { color: '#293241' } }
      },
      series: [{ type: 'radar', data: [{ value: radarValues }] }]
    }
  }
}

export function mapPermissionCenterView(dto: PermissionMatrixDTO): PermissionCenterViewModel {
  const rows = (dto.rows ?? []).map((row) => ({
    agentId: row.agent_id,
    resources: (row.resources ?? []).map((resource) => ({
      toolId: resource.tool_id,
      toolName: resource.tool_name,
      declarations: (resource.declarations ?? []).map((declaration) => ({
        scope: declaration.scope,
        risk: declaration.risk,
        riskTone: riskTone(declaration.risk),
        required: declaration.required,
        effective: declaration.effective,
        effectiveLabel: declaration.effective ? 'Allowed' : 'Blocked',
        source: declaration.source
      }))
    }))
  }))
  const resources = rows.reduce((sum, row) => sum + row.resources.length, 0)
  const declarations = rows.flatMap((row) => row.resources.flatMap((resource) => resource.declarations))

  return {
    summary: {
      agents: rows.length,
      resources,
      declarations: declarations.length,
      effective: declarations.filter((declaration) => declaration.effective).length
    },
    rows,
    chartOption: {
      grid: { left: 90, right: 16, top: 16, bottom: 28 },
      xAxis: { type: 'value', max: 100, splitLine: { lineStyle: { color: '#293241' } } },
      yAxis: { type: 'category', data: rows.map((row) => row.agentId), axisLine: { lineStyle: { color: '#293241' } } },
      series: [
        {
          type: 'bar',
          data: rows.map((row) => {
            const rowDeclarations = row.resources.flatMap((resource) => resource.declarations)
            if (rowDeclarations.length === 0) return 0
            return Math.round((rowDeclarations.filter((declaration) => declaration.effective).length / rowDeclarations.length) * 100)
          }),
          barWidth: 14
        }
      ]
    }
  }
}

export function mapPermissionPolicyCenterView(dto: PermissionPolicyViewDTO): PermissionPolicyCenterViewModel {
  const capabilities = (dto.capabilities ?? [])
    .map((capability) => ({
      id: capability.capability_id,
      domain: capability.domain,
      action: capability.action,
      accessMode: capability.access_mode,
      accessLabelZh: capability.access_mode === 'read' ? '查看' : '编辑',
      accessLabelEn: capability.access_mode === 'read' ? 'Read' : 'Edit',
      scope: capability.scope,
      method: capability.method,
      path: capability.path,
      labelZh: fallback(capability.label_zh, capability.capability_id),
      labelEn: fallback(capability.label_en, capability.description, capability.capability_id),
      displayName: bilingual(fallback(capability.label_zh, capability.capability_id), fallback(capability.label_en, capability.description, capability.capability_id)),
      categoryZh: fallback(capability.category_zh, capability.domain),
      categoryEn: fallback(capability.category_en, capability.domain),
      categoryLabel: bilingual(fallback(capability.category_zh, capability.domain), fallback(capability.category_en, capability.domain)),
      description: fallback(capability.description, capability.label_en, capability.capability_id)
    }))
    .sort((a, b) => `${a.categoryEn}:${a.accessMode}:${a.displayName}`.localeCompare(`${b.categoryEn}:${b.accessMode}:${b.displayName}`))
  const policy = normalizePermissionPolicy(dto.policy ?? {})
  const capabilityById = new Map(capabilities.map((capability) => [capability.id, capability]))
  const derivedEffective = deriveEffectiveGrants(policy, capabilities)

  const roles = policy.roles.map((role) => ({
    id: role.role_id,
    labelZh: fallback(role.label_zh, role.role_id),
    labelEn: fallback(role.label_en, role.role_id),
    displayName: bilingual(fallback(role.label_zh, role.role_id), fallback(role.label_en, role.role_id)),
    builtIn: role.built_in === true
  }))
  const roleById = new Map(roles.map((role) => [role.id, role]))

  const grants = policy.grants.map((grant) => ({
    subjectKind: grant.subject_kind,
    subjectId: grant.subject_id,
    capabilityId: grant.capability_id ?? '',
    scope: grant.scope ?? ''
  }))

  const rolePermissionRows = capabilities.map((capability) => ({
    capability,
    roles: roles.map((role) => ({
      roleId: role.id,
      roleDisplayName: role.displayName,
      granted: policy.grants.some((grant) => grant.subject_kind === 'role' && grant.subject_id === role.id && grantMatchesCapability(grant, capability.id, capability.scope))
    }))
  }))

  const effective = derivedEffective.map((grant) => {
    const capability = capabilityById.get(grant.capability_id)
    const sourceRole = roleById.get(grant.source_id ?? '')
    return {
      principalKind: grant.principal_kind,
      principalId: grant.principal_id,
      principalDisplayName: bilingual(grant.principal_id, grant.principal_id),
      capabilityId: grant.capability_id,
      capabilityDisplayName: capability?.displayName ?? fallback(grant.capability_id, grant.scope),
      scope: grant.scope,
      source: grant.source,
      sourceId: grant.source_id ?? '',
      sourceLabel: grant.source === 'role' ? `角色：${sourceRole?.displayName ?? grant.source_id ?? ''}` : '直接授权'
    }
  })

  return {
    enforcementMode: dto.enforcement_mode ?? 'local_policy',
    policyMutable: dto.policy_mutable ?? true,
    summary: {
      capabilities: capabilities.length,
      readCapabilities: capabilities.filter((capability) => capability.accessMode === 'read').length,
      writeCapabilities: capabilities.filter((capability) => capability.accessMode !== 'read').length,
      roles: roles.length,
      effective: effective.length
    },
    capabilities,
    roles,
    grants,
    rolePermissionRows,
    effective,
    command: policy
  }
}

export function mapPermissionPolicyCommand(view: PermissionPolicyCenterViewModel): PermissionPolicyCommandViewModel {
  return {
    roles: view.command.roles.map((role) => ({ ...role })),
    role_assignments: view.command.role_assignments.map((assignment) => ({ ...assignment })),
    grants: view.command.grants.map((grant) => ({ ...grant }))
  }
}

export function mapPermissionPolicyUpdateCommand(view: PermissionPolicyCenterViewModel): PermissionPolicyCommandViewModel {
  const builtInRoleIDs = new Set(view.roles.filter((role) => role.builtIn).map((role) => role.id))
  return {
    roles: view.command.roles
      .filter((role) => !builtInRoleIDs.has(role.role_id))
      .map(({ built_in: _builtIn, ...role }) => role),
    role_assignments: view.command.role_assignments
      .filter((assignment) => !builtInRoleIDs.has(assignment.role_id))
      .map((assignment) => ({ ...assignment })),
    grants: view.command.grants
      .filter((grant) => grant.subject_kind !== 'role' || !builtInRoleIDs.has(grant.subject_id))
      .map((grant) => ({ ...grant }))
  }
}

function severityTone(severity: string): BadgeTone {
  if (severity === 'error') return 'danger'
  if (severity === 'warning') return 'warning'
  if (severity === 'info') return 'success'
  return 'neutral'
}

function authIPLockStatusTone(status: string): BadgeTone {
  if (status === 'active') return 'danger'
  if (status === 'tracking') return 'warning'
  return 'neutral'
}

function riskTone(risk: string): BadgeTone {
  if (risk === 'high' || risk === 'critical') return 'danger'
  if (risk === 'medium') return 'warning'
  if (risk === 'low') return 'success'
  return 'neutral'
}

function percent(value: number) {
  return Math.round(Math.max(0, Math.min(1, value)) * 100)
}

function clampScore(value: number) {
  return Math.round(Math.max(0, Math.min(100, value)))
}

function average(values: number[]) {
  if (values.length === 0) return 0
  return Math.round(values.reduce((sum, value) => sum + value, 0) / values.length)
}

function normalizePermissionPolicy(policy: PermissionPolicyDTO): PermissionPolicyCommandViewModel {
  return {
    roles: (policy.roles ?? [])
      .map((role) => ({
        role_id: role.role_id,
        label_zh: role.label_zh,
        label_en: role.label_en,
        ...(role.built_in === true ? { built_in: true } : {})
      }))
      .sort((a, b) => a.role_id.localeCompare(b.role_id)),
    role_assignments: (policy.role_assignments ?? [])
      .map((assignment) => ({
        principal_kind: assignment.principal_kind,
        principal_id: assignment.principal_id,
        role_id: assignment.role_id
      }))
      .sort((a, b) => `${a.principal_kind}:${a.principal_id}:${a.role_id}`.localeCompare(`${b.principal_kind}:${b.principal_id}:${b.role_id}`)),
    grants: normalizePolicyGrants(policy.grants ?? [])
  }
}

function grantMatchesCapability(grant: PermissionPolicyCommandViewModel['grants'][number], capabilityId: string, scope: string) {
  return grant.capability_id === capabilityId || (!!grant.scope && grant.scope === scope)
}

function normalizePolicyGrants(grants: NonNullable<PermissionPolicyDTO['grants']>) {
  const expanded = new Map<string, PermissionPolicyCommandViewModel['grants'][number]>()
  for (const grant of grants) {
    putGrant(expanded, {
      subject_kind: grant.subject_kind,
      subject_id: grant.subject_id,
      capability_id: grant.capability_id,
      scope: grant.scope,
      resource_kind: grant.resource_kind,
      resource_id: grant.resource_id
    })
  }
  return Array.from(expanded.values()).sort((a, b) => grantKey(a).localeCompare(grantKey(b)))
}

function putGrant(target: Map<string, PermissionPolicyCommandViewModel['grants'][number]>, grant: PermissionPolicyCommandViewModel['grants'][number]) {
  target.set(grantKey(grant), grant)
}

function grantKey(grant: PermissionPolicyCommandViewModel['grants'][number]) {
  return [
    grant.subject_kind,
    grant.subject_id,
    grant.capability_id ?? '',
    grant.scope ?? '',
    grant.resource_kind ?? '',
    grant.resource_id ?? ''
  ].join(':')
}

function deriveEffectiveGrants(policy: PermissionPolicyCommandViewModel, capabilities: PermissionPolicyCenterViewModel['capabilities']) {
  const capabilityById = new Map(capabilities.map((capability) => [capability.id, capability]))
  const capabilitiesByScope = new Map<string, PermissionPolicyCenterViewModel['capabilities']>()
  for (const capability of capabilities) {
    if (!capabilitiesByScope.has(capability.scope)) capabilitiesByScope.set(capability.scope, [])
    capabilitiesByScope.get(capability.scope)?.push(capability)
  }

  const roleAssignments = new Map<string, Array<{ kind: string; id: string }>>()
  for (const assignment of policy.role_assignments) {
    if (!roleAssignments.has(assignment.role_id)) roleAssignments.set(assignment.role_id, [])
    roleAssignments.get(assignment.role_id)?.push({ kind: assignment.principal_kind, id: assignment.principal_id })
  }

  const effective = new Map<string, NonNullable<PermissionPolicyViewDTO['effective']>[number]>()
  const emit = (principalKind: string, principalId: string, capabilityId: string, scope: string, source: string, sourceId = '') => {
    const key = `${principalKind}:${principalId}:${capabilityId}:${scope}:${source}:${sourceId}`
    effective.set(key, {
      principal_kind: principalKind,
      principal_id: principalId,
      capability_id: capabilityId,
      scope,
      source,
      source_id: sourceId
    })
  }

  const emitGrant = (principalKind: string, principalId: string, grant: PermissionPolicyCommandViewModel['grants'][number], source: string, sourceId = '') => {
    if (grant.capability_id) {
      const capability = capabilityById.get(grant.capability_id)
      emit(principalKind, principalId, grant.capability_id, grant.scope ?? capability?.scope ?? '', source, sourceId)
      return
    }
    for (const capability of capabilitiesByScope.get(grant.scope ?? '') ?? []) {
      emit(principalKind, principalId, capability.id, capability.scope, source, sourceId)
    }
  }

  for (const grant of policy.grants) {
    if (grant.subject_kind === 'role') {
      for (const assignment of roleAssignments.get(grant.subject_id) ?? []) {
        emitGrant(assignment.kind, assignment.id, grant, 'role', grant.subject_id)
      }
    } else {
      emitGrant(grant.subject_kind, grant.subject_id, grant, 'direct')
    }
  }

  return Array.from(effective.values()).sort((a, b) =>
    `${a.principal_kind}:${a.principal_id}:${a.scope}:${a.capability_id}:${a.source}:${a.source_id ?? ''}`.localeCompare(
      `${b.principal_kind}:${b.principal_id}:${b.scope}:${b.capability_id}:${b.source}:${b.source_id ?? ''}`
    )
  )
}

function bilingual(zh: string, en: string) {
  if (!zh) return en
  if (!en || en === zh) return zh
  return `${zh} / ${en}`
}

function fallback(...values: Array<string | undefined>) {
  for (const value of values) {
    if (value && value.trim()) return value.trim()
  }
  return ''
}

function formatDateTime(value: string | undefined) {
  if (!value) return 'Not provided'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return new Intl.DateTimeFormat('en', {
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  }).format(parsed)
}

function formatDate(value: string | undefined) {
  if (!value) return '未记录'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit'
  }).format(parsed)
}
