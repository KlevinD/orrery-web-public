import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearApiBaseUrlForTests } from '#lib/runtime/api-base'
import { developmentProjectApi, scmConnectionApi } from './development-projects'

describe('development resource APIs', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    clearApiBaseUrlForTests()
  })

  it('binds optimistic concurrency to If-Match for project and child mutations', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () =>
      new Response(JSON.stringify({ development_project_id: 'project/one', version: 8 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    )
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000041')

    await developmentProjectApi.update('project/one', 7, { name: 'Updated' })
    const [updateURL, updateInit] = fetchMock.mock.calls[0]
    expect(updateURL).toBe('/api/development-projects/project%2Fone')
    expect(updateInit?.method).toBe('PATCH')
    expect(new Headers(updateInit?.headers).get('If-Match')).toBe('"7"')

    await developmentProjectApi.removeMember('project/one', 'member/one', 8, 3)
    const [memberURL, memberInit] = fetchMock.mock.calls[1]
    expect(memberURL).toBe('/api/development-projects/project%2Fone/members/member%2Fone')
    expect(new Headers(memberInit?.headers).get('If-Match')).toBe('"8"')
    expect(memberInit?.body).toBe(JSON.stringify({ expected_member_version: 3 }))
  })

  it('sends secret references only in connection mutation bodies', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({
        connection_id: 'connection-1', scope: 'personal', provider: 'generic_git', status: 'active',
        created_at: '2026-07-25T00:00:00Z', updated_at: '2026-07-25T00:00:00Z', version: 1,
        capabilities: { can_view: true, can_use: true, can_manage: true, can_verify: true, can_rotate: true, can_revoke: true }
      }), { status: 201, headers: { 'Content-Type': 'application/json' } })
    )
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000042')

    const dto = await scmConnectionApi.create({
      scope: 'personal', provider: 'generic_git', base_url: 'https://git.example/repo', secret_ref: 'secret://scm/token'
    })
    expect(fetchMock.mock.calls[0][1]?.body).toContain('secret://scm/token')
    expect(JSON.stringify(dto)).not.toContain('secret_ref')
    expect(JSON.stringify(dto)).not.toContain('secret://')
  })

  it('lists managed workspaces with encoded project and filters', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ items: [], page: { limit: 20, offset: 0, total: 0, returned: 0, has_more: false } }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await developmentProjectApi.listWorkspaces('project/one', { todoId: 'todo/one', status: 'ready', limit: 20 })

    expect(fetchMock.mock.calls[0][0]).toBe('/api/development-projects/project%2Fone/workspaces?todo_id=todo%2Fone&status=ready&limit=20')
  })
})
