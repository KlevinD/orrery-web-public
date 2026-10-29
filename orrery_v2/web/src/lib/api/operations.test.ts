import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({
  apiFetchMock: vi.fn()
}))

vi.mock('./client', () => ({
  apiFetch: apiFetchMock
}))

import { operationsApi } from './operations'

describe('operations API client', () => {
  beforeEach(() => {
    apiFetchMock.mockReset()
  })

  it('requests responsibility profiles with an explicit bounded history limit', async () => {
    await operationsApi.getAgentResponsibilityProfile('agent/1', 0, { signal: new AbortController().signal })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/responsibility-profiles/agents/agent%2F1?history_limit=0',
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )

    await operationsApi.getOrganizationResponsibilityProfile('org/1', 200)
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/responsibility-profiles/organizations/org%2F1?history_limit=100',
      undefined
    )
  })

  it('calls layered permission endpoints without the legacy full-policy read', async () => {
    await operationsApi.listPermissionRoles('?limit=20')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/permissions/roles?limit=20', undefined)

    await operationsApi.listPermissionPackages('?role_id=custom.security&limit=20')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/permissions/packages?role_id=custom.security&limit=20', undefined)

    await operationsApi.listPermissionPackageCapabilities('permission.read', '?role_id=custom.security&limit=20')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/permissions/packages/permission.read/capabilities?role_id=custom.security&limit=20', undefined)

    await operationsApi.listEffectivePermissionGrants('?limit=20')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/permissions/effective-grants?limit=20', undefined)

    const batch = {
      expected_revision: 4,
      package_changes: [{ role_id: 'custom.security', package_id: 'permission.read', effect: 'grant' as const }]
    }
    await operationsApi.applyPermissionPolicyBatch(batch)
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/permissions/policy/batch',
      expect.objectContaining({ method: 'PUT', body: JSON.stringify(batch) })
    )
  })

  it('calls talent market workforce placement endpoints', async () => {
    await operationsApi.listTalentMarketMembers('?enabled_only=true')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/talent-market/members?enabled_only=true', undefined)

    const file = new File(['zip'], 'agents.zip', { type: 'application/zip' })
    await operationsApi.previewTalentMarketImport(file, { idempotencyKey: 'preview-1' })
    const previewCall = apiFetchMock.mock.calls.at(-1)
    expect(previewCall?.[0]).toBe('/api/talent-market/import/preview')
    expect(previewCall?.[1]).toEqual(expect.objectContaining({ method: 'POST', idempotencyKey: 'preview-1' }))
    expect(previewCall?.[1].body).toBeInstanceOf(FormData)

    await operationsApi.commitTalentMarketImport(
      {
        candidates: [{ real_name: '导入专家', conflict_resolution: 'rename', resolved_real_name: '导入专家二号' }],
        skills: [{ skill_id: 'skill.imported', name: '导入技能', version: '1.0.0', enabled: true }]
      },
      { idempotencyKey: 'commit-1' }
    )
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/talent-market/import/commit',
      expect.objectContaining({
        method: 'POST',
        idempotencyKey: 'commit-1',
        body: JSON.stringify({
          candidates: [{ real_name: '导入专家', conflict_resolution: 'rename', resolved_real_name: '导入专家二号' }],
          skills: [{ skill_id: 'skill.imported', name: '导入技能', version: '1.0.0', enabled: true }]
        })
      })
    )

    await operationsApi.exportTalentMarket({ agent_profile_ids: ['profile-market'] })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/talent-market/export',
      expect.objectContaining({
        method: 'POST',
        responseType: 'blob',
        body: JSON.stringify({ agent_profile_ids: ['profile-market'] })
      })
    )

    await operationsApi.deleteTalentMarketMember('profile-market', { idempotencyKey: 'delete-market-1' })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/talent-market/members/profile-market',
      expect.objectContaining({
        method: 'DELETE',
        idempotencyKey: 'delete-market-1'
      })
    )

    await operationsApi.deleteTalentMarketMembers(
      { agent_profile_ids: ['profile-market', 'profile-other'], reason: 'cleanup selected' },
      { idempotencyKey: 'delete-market-batch-1' }
    )
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/talent-market/members/delete-batch',
      expect.objectContaining({
        method: 'POST',
        idempotencyKey: 'delete-market-batch-1',
        body: JSON.stringify({ agent_profile_ids: ['profile-market', 'profile-other'], reason: 'cleanup selected' })
      })
    )

    await operationsApi.releaseWorkforceMemberToMarket('profile-internal', { reason: 'team changed' }, { idempotencyKey: 'release-1' })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/workforce/members/profile-internal/release-to-market',
      expect.objectContaining({
        method: 'POST',
        idempotencyKey: 'release-1',
        body: JSON.stringify({ reason: 'team changed' })
      })
    )

    await operationsApi.hireTalentFromMarket(
      'profile-market',
      {
        agent_profile_id: 'profile-market',
        organization_id: 'org-1',
        department_id: 'dept-ops',
        position_id: 'pos-operator'
      },
      { idempotencyKey: 'hire-1' }
    )
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/talent-market/members/profile-market/hire',
      expect.objectContaining({
        method: 'POST',
        idempotencyKey: 'hire-1',
        body: JSON.stringify({
          agent_profile_id: 'profile-market',
          organization_id: 'org-1',
          department_id: 'dept-ops',
          position_id: 'pos-operator'
        })
      })
    )
  })

  it('calls organization catalog command endpoints', async () => {
    await operationsApi.createOrganization(
      { name: 'Platform' },
      { idempotencyKey: 'create-org-1' }
    )
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/org',
      expect.objectContaining({
        method: 'POST',
        idempotencyKey: 'create-org-1',
        body: JSON.stringify({ name: 'Platform' })
      })
    )

    await operationsApi.createOrganizationUnit(
      { organization_id: 'org-1', name: 'Research' },
      { idempotencyKey: 'create-unit-1' }
    )
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/org/units',
      expect.objectContaining({
        method: 'POST',
        idempotencyKey: 'create-unit-1',
        body: JSON.stringify({ organization_id: 'org-1', name: 'Research' })
      })
    )

    await operationsApi.createOrganizationPosition(
      { organization_id: 'org-1', department_id: 'dept-ai', name: '算法工程师' },
      { idempotencyKey: 'create-position-1' }
    )
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/org/positions',
      expect.objectContaining({
        method: 'POST',
        idempotencyKey: 'create-position-1',
        body: JSON.stringify({ organization_id: 'org-1', department_id: 'dept-ai', name: '算法工程师' })
      })
    )

    await operationsApi.deleteOrganization('org-1', { idempotencyKey: 'delete-org-1' })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/org/org-1',
      expect.objectContaining({
        method: 'DELETE',
        idempotencyKey: 'delete-org-1'
      })
    )

    await operationsApi.deleteOrganizationUnit('dept-ai', { idempotencyKey: 'delete-unit-1' })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/org/units/dept-ai',
      expect.objectContaining({
        method: 'DELETE',
        idempotencyKey: 'delete-unit-1'
      })
    )

    await operationsApi.deleteOrganizationPosition('position-ai', { idempotencyKey: 'delete-position-1' })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/org/positions/position-ai',
      expect.objectContaining({
        method: 'DELETE',
        idempotencyKey: 'delete-position-1'
      })
    )

    await operationsApi.deleteOrgRoleAssignment('assign:owner', { idempotencyKey: 'delete-role-1' })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/org/role-assignments/assign%3Aowner',
      expect.objectContaining({
        method: 'DELETE',
        idempotencyKey: 'delete-role-1'
      })
    )
  })

  it('normalizes nullable talent market import preview collections', async () => {
    apiFetchMock.mockResolvedValueOnce({
      items: [
        {
          candidate_index: 0,
          status: 'create',
          candidate: { real_name: 'Imported Agent' },
          conflicts: null
        }
      ],
      conflicts: null,
      skills: [
        {
          skill_id: 'skill.imported',
          name: '导入技能',
          version: '1.0.0',
          enabled: true,
          prompt_assets: [{ path: 'SKILL.md', kind: 'markdown', content: '完整 skill 内容' }]
        }
      ]
    })

    const file = new File(['zip'], 'agents.zip', { type: 'application/zip' })
    const preview = await operationsApi.previewTalentMarketImport(file)

    expect(preview.conflicts).toEqual([])
    expect(preview.items[0].conflicts).toEqual([])
    expect(preview.skills).toEqual([
      {
        skill_id: 'skill.imported',
        name: '导入技能',
        version: '1.0.0',
        enabled: true,
        prompt_assets: [{ path: 'SKILL.md', kind: 'markdown', content: '完整 skill 内容' }]
      }
    ])
  })

})
