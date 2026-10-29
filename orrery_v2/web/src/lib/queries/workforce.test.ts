import { describe, expect, it, vi } from 'vitest'
import { operationsApi } from '#lib/api/operations'
import * as workforceQueries from './workforce'
import {
  DEFAULT_WORKFORCE_PAGE_SIZE,
  buildOrgGraphQuery,
  buildWorkforceQuery,
  combineWorkforcePages,
  nextWorkforcePageOffset,
  workforceReleaseRequest,
  workforceListFilters,
  workforceQueryKeys
} from './workforce'

describe('workforce queries', () => {
  it('serializes workforce filters', () => {
    expect(
      buildWorkforceQuery({
        organizationId: 'org-1',
        departmentId: 'dept-ops',
        enabledOnly: true,
        limit: 25,
        offset: 50
      })
    ).toBe('?organization_id=org-1&department_id=dept-ops&enabled_only=true&limit=25&offset=50')
  })

  it('defaults workforce lists to the first 20 rows', () => {
    expect(DEFAULT_WORKFORCE_PAGE_SIZE).toBe(20)
    expect(buildWorkforceQuery(workforceListFilters())).toBe('?limit=20&offset=0')
    expect(buildWorkforceQuery(workforceListFilters({ departmentId: 'dept-ops' }))).toBe(
      '?department_id=dept-ops&limit=20&offset=0'
    )
    expect(buildWorkforceQuery(workforceListFilters({ limit: 50, offset: 100 }))).toBe('?limit=50&offset=100')
  })

  it('builds a minimal first-page query for talent market counts', () => {
    expect(workforceQueries.marketCountFilters({ enabledOnly: true })).toEqual({
      enabledOnly: true,
      limit: 1,
      offset: 0
    })
    expect(buildWorkforceQuery(workforceQueries.marketCountFilters({ enabledOnly: true }))).toBe(
      '?enabled_only=true&limit=1&offset=0'
    )
    expect(workforceQueryKeys.marketCount(workforceQueries.marketCountFilters())).toEqual([
      'workforce',
      'talent-market',
      'count',
      '?limit=1&offset=0'
    ])
  })

  it('computes the next infinite-scroll offset from API page metadata', () => {
    expect(nextWorkforcePageOffset({ page: { offset: 20, returned: 20, has_more: true } })).toBe(40)
    expect(nextWorkforcePageOffset({ page: { offset: 20, limit: 20, has_more: true } })).toBe(40)
    expect(nextWorkforcePageOffset({ page: { offset: 20, returned: 20, has_more: false } })).toBeUndefined()
  })

  it('combines workforce pages without losing total page metadata', () => {
    const page = combineWorkforcePages([
      {
        members: [
          { agent_id: 'agent-1', agent_profile_id: 'agent-1', real_name: 'Agent One', agent_kind: 'ai', employment_type: 'ai_agent', enabled: true },
          { agent_id: 'agent-2', agent_profile_id: 'agent-2', real_name: 'Agent Two', agent_kind: 'ai', employment_type: 'ai_agent', enabled: true }
        ],
        page: { total: 3, limit: 2, offset: 0, returned: 2, has_more: true }
      },
      {
        members: [{ agent_id: 'agent-3', agent_profile_id: 'agent-3', real_name: 'Agent Three', agent_kind: 'ai', employment_type: 'ai_agent', enabled: true }],
        page: { total: 3, limit: 2, offset: 2, returned: 1, has_more: false }
      }
    ])

    expect(page.members?.map((member) => member.agent_profile_id)).toEqual(['agent-1', 'agent-2', 'agent-3'])
    expect(page.page).toEqual({ total: 3, limit: 2, offset: 0, returned: 3, has_more: false })
  })

  it('keeps workforce caches scoped by filter', () => {
    expect(workforceQueryKeys.members({ departmentId: 'dept-ops' })).toEqual([
      'workforce',
      'members',
      '?department_id=dept-ops'
    ])
    expect(workforceQueryKeys.orgGraph({ organizationId: 'org-1' })).toEqual([
      'workforce',
      'org-graph',
      '?organization_id=org-1'
    ])
    expect(buildOrgGraphQuery({ organizationId: 'org-1', includeMembers: true })).toBe(
      '?organization_id=org-1&include_members=true'
    )
    expect(workforceQueryKeys.orgGraph({ organizationId: 'org-1', includeMembers: true })).toEqual([
      'workforce',
      'org-graph',
      '?organization_id=org-1&include_members=true'
    ])
    expect(workforceQueryKeys.market({ enabledOnly: true })).toEqual([
      'workforce',
      'talent-market',
      '?enabled_only=true'
    ])
    expect(workforceQueryKeys.agentResponsibilityProfile('agent-1', 0)).toEqual([
      'workforce',
      'responsibility-profiles',
      'agent',
      'agent-1',
      0
    ])
    expect(workforceQueryKeys.organizationResponsibilityProfile('org-1', 20)).toEqual([
      'workforce',
      'responsibility-profiles',
      'organization',
      'org-1',
      20
    ])
  })

  it('serializes role assignment principal filters', () => {
    expect(workforceQueries.buildRoleAssignmentQuery({
      actorType: 'human',
      actorId: 'profile-1',
      roleId: 'platform.admin',
      effectiveOnly: true
    })).toBe('?actor_type=human&actor_id=profile-1&role_id=platform.admin&effective_only=true')
  })

  it('builds workforce release requests with optional reasons', () => {
    expect(workforceReleaseRequest('profile-1')).toEqual({
      agentProfileId: 'profile-1',
      body: {}
    })
    expect(workforceReleaseRequest({ agentProfileId: 'profile-1', reason: ' removed from org ' })).toEqual({
      agentProfileId: 'profile-1',
      body: { reason: 'removed from org' }
    })
  })

  it('wraps organization catalog mutations in real backend command handlers', async () => {
    const invalidateQueries = vi.fn()
    expect(typeof workforceQueries.createOrganizationMutationRegistry).toBe('function')
    const registry = workforceQueries.createOrganizationMutationRegistry({ invalidateQueries } as any)

    expect(typeof operationsApi.deleteOrganization).toBe('function')
    vi.spyOn(operationsApi, 'createOrganizationPosition').mockResolvedValue({ position_id: 'position-ai' })
    vi.spyOn(operationsApi, 'deleteOrganization').mockResolvedValue({ status: 'deleted' })
    vi.spyOn(operationsApi, 'deleteOrganizationUnit').mockResolvedValue({ status: 'deleted' })
    vi.spyOn(operationsApi, 'deleteOrganizationPosition').mockResolvedValue({ status: 'deleted' })
    vi.spyOn(operationsApi, 'updateOrganizationOwner').mockResolvedValue({ id: 'dept-ai', owner_profile_id: 'human-owner' })

    await registry.createOrganizationPosition({
      organizationId: 'org-1',
      departmentId: 'dept-ai',
      name: '算法工程师'
    })
    await registry.deleteOrganization('org-1')
    await registry.deleteOrganizationUnit('dept-ai')
    await registry.deleteOrganizationPosition('position-ai')
    await registry.updateOrganizationOwner({ organizationNodeId: 'dept-ai', ownerProfileId: ' human-owner ' })

    expect(operationsApi.createOrganizationPosition).toHaveBeenCalledWith({
      organization_id: 'org-1',
      department_id: 'dept-ai',
      name: '算法工程师'
    }, expect.objectContaining({
      idempotencyKey: expect.any(String)
    }))
    expect(operationsApi.deleteOrganization).toHaveBeenCalledWith('org-1', expect.objectContaining({
      idempotencyKey: expect.any(String)
    }))
    expect(operationsApi.deleteOrganizationUnit).toHaveBeenCalledWith('dept-ai', expect.objectContaining({
      idempotencyKey: expect.any(String)
    }))
    expect(operationsApi.deleteOrganizationPosition).toHaveBeenCalledWith('position-ai', expect.objectContaining({
      idempotencyKey: expect.any(String)
    }))
    expect(operationsApi.updateOrganizationOwner).toHaveBeenCalledWith('dept-ai', {
      owner_profile_id: 'human-owner'
    }, expect.objectContaining({
      idempotencyKey: expect.any(String)
    }))
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: workforceQueryKeys.all })
  })
})
