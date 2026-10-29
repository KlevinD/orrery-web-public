import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  buildAuditQuery,
  buildEvaluationQuery,
  buildPermissionQuery,
  buildPermissionPageQuery,
  createPermissionBatchMutationRegistry,
  createPermissionPolicyMutationRegistry,
  operationsQueryKeys,
  subscribeEvaluationStream
} from './operations'
import { approvalQueryKeys, buildApprovalQuery, buildHILDecisionQuery } from './approvals'
import { operationsApi } from '#lib/api/operations'

const eventListeners = new Map<string, EventListenerOrEventListenerObject>()
const close = vi.fn()
const connectEvaluationStream = vi.fn()

vi.mock('#lib/api/websocket', () => ({
  connectEvaluationStream: (query: string) => {
    connectEvaluationStream(query)
    return {
      addEventListener: (event: string, listener: EventListenerOrEventListenerObject) => eventListeners.set(event, listener),
      close
    }
  }
}))

describe('operations queries', () => {
  beforeEach(() => {
    eventListeners.clear()
    close.mockClear()
    connectEvaluationStream.mockClear()
  })

  it('serializes approval, audit, evaluation, and permission filters', () => {
    expect(buildApprovalQuery({ missionId: 'mission-1', status: 'pending' })).toBe('?mission_id=mission-1&status=pending')
    expect(buildAuditQuery({ missionId: 'mission-1', actorId: 'agent-a', action: 'approval.decide', entityType: 'approval', entityId: 'approval-1', result: 'failed', severity: 'warning', limit: 100, offset: 200 })).toBe('?mission_id=mission-1&actor_id=agent-a&action=approval.decide&entity_type=approval&entity_id=approval-1&result=failed&severity=warning&limit=100&offset=200')
    expect(buildEvaluationQuery({ missionId: 'mission-1', entityType: 'agent', entityId: 'agent-a' })).toBe('?mission_id=mission-1&entity_type=agent&entity_id=agent-a')
    expect(buildPermissionQuery({ agentId: 'agent-a', toolId: 'tool-a' })).toBe('?agent_id=agent-a&tool_id=tool-a')
    expect(buildHILDecisionQuery({ mode: 'hil', status: 'pending', limit: 100 })).toBe('?mode=hil&status=pending&limit=100')
    expect(buildHILDecisionQuery({ mode: 'approval', status: '', limit: 100 })).toBe('?mode=approval&limit=100')
    expect(buildHILDecisionQuery({ mode: 'approval', status: 'pending', mine: true, limit: 100 })).toBe('?mode=approval&status=pending&mine=true&limit=100')
    expect(buildHILDecisionQuery({ mode: 'council', status: 'pending', mine: true, limit: 100 })).toBe('?mode=council&status=pending&mine=true&limit=100')
  })

  it('keeps query keys scoped by filter', () => {
    expect(approvalQueryKeys.list({ status: 'pending' })).toEqual(['approvals', '?status=pending'])
    expect(operationsQueryKeys.audit({ severity: 'error' })).toEqual(['operations', 'audit', '?severity=error'])
    expect(operationsQueryKeys.permissionPolicy()).toEqual(['operations', 'permissions', 'policy'])
  })

  it('scopes layered permission queries by role, package, cursor, and lazy view', () => {
    expect(buildPermissionPageQuery({ roleId: 'custom.security', cursor: 'next', limit: 20, accessMode: 'read' })).toBe(
      '?role_id=custom.security&cursor=next&limit=20&access_mode=read'
    )
    expect(operationsQueryKeys.permissionRoles({ limit: 20 })).toEqual(['operations', 'permissions', 'roles', '?limit=20'])
    expect(operationsQueryKeys.permissionPackages({ roleId: 'custom.security', limit: 20 })).toEqual([
      'operations', 'permissions', 'packages', '?role_id=custom.security&limit=20'
    ])
    expect(operationsQueryKeys.permissionPackageCapabilities('permission.read', { roleId: 'custom.security', limit: 20 })).toEqual([
      'operations', 'permissions', 'packages', 'permission.read', 'capabilities', '?role_id=custom.security&limit=20'
    ])
    expect(operationsQueryKeys.permissionEffectiveGrants({ limit: 20 })).toEqual([
      'operations', 'permissions', 'effective-grants', '?limit=20'
    ])
  })

  it('saves all permission deltas through one batch mutation', async () => {
    const invalidateQueries = vi.fn()
    const registry = createPermissionBatchMutationRegistry({ invalidateQueries } as any)
    const batch = {
      expected_revision: 7,
      role_creates: [{ role_id: 'custom.audit', label_zh: '审计员', label_en: 'Auditor' }],
      package_changes: [{ role_id: 'custom.audit', package_id: 'audit.read', effect: 'grant' as const }]
    }
    vi.spyOn(operationsApi, 'applyPermissionPolicyBatch').mockResolvedValue({ revision: 8 })

    await registry.save(batch)

    expect(operationsApi.applyPermissionPolicyBatch).toHaveBeenCalledWith(batch)
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: operationsQueryKeys.permissionRoot })
  })

  it('wraps permission policy saves in a mutation registry handler', async () => {
    const invalidateQueries = vi.fn()
    const registry = createPermissionPolicyMutationRegistry({ invalidateQueries } as any)
    const policy = {
      roles: [{ role_id: 'ops-admin', label_zh: '运营管理员', label_en: 'Operations Admin' }],
      role_assignments: [],
      grants: [{ subject_kind: 'role', subject_id: 'ops-admin', scope: 'operations.manage' }]
    }
    vi.spyOn(operationsApi, 'updatePermissionPolicy').mockResolvedValue({
      policy,
      capabilities: [],
      effective: []
    })

    await registry.save(policy)

    expect(operationsApi.updatePermissionPolicy).toHaveBeenCalledWith(policy)
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: operationsQueryKeys.permissionRoot })
  })

  it('subscribes to evaluation report stream snapshots and updates', () => {
    const onView = vi.fn()
    const onError = vi.fn()

    const unsubscribe = subscribeEvaluationStream({ missionId: 'mission-1', entityType: 'agent' }, onView, onError)

    expect(connectEvaluationStream).toHaveBeenCalledWith('?mission_id=mission-1&entity_type=agent')
    expect(eventListeners.has('snapshot')).toBe(true)
    expect(eventListeners.has('update')).toBe(true)
    expect(eventListeners.has('error')).toBe(true)

    dispatchStreamEvent('snapshot', {
      items: [{ entity_type: 'agent', entity_id: 'agent-a', assigned_tasks: 2, completed_tasks: 1, quality_score: 0.8 }]
    })
    dispatchStreamEvent('update', {
      items: [{ entity_type: 'agent', entity_id: 'agent-a', assigned_tasks: 2, completed_tasks: 2, quality_score: 0.9 }]
    })

    expect(onView).toHaveBeenCalledTimes(2)
    expect(onView.mock.calls[0][0].summary.averageQuality).toBe(80)
    expect(onView.mock.calls[1][0].summary.completedTasks).toBe(2)
    unsubscribe()
    expect(close).toHaveBeenCalledTimes(1)
  })
})

function dispatchStreamEvent(event: string, payload?: unknown) {
  const listener = eventListeners.get(event)
  const streamEvent = new MessageEvent(event, payload === undefined ? undefined : { data: JSON.stringify(payload) })
  if (typeof listener === 'function') {
    listener(streamEvent)
  } else {
    listener?.handleEvent(streamEvent)
  }
}
