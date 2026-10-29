import { afterEach, describe, expect, it, vi } from 'vitest'
import { resourceApi } from './resources'
import { operationsApi } from './operations'
describe('resourceApi', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('calls editable MCP resource command endpoints with production API paths', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    ))
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000004')

    await resourceApi.registerMcpServer({ name: 'GitHub MCP', server_name: 'github' })
    await resourceApi.getMcpServer('mcp.github')
    await resourceApi.updateMcpServer('mcp.github', { name: 'GitHub MCP', server_name: 'github' })
    await resourceApi.disableMcpServer('mcp.github')
    await resourceApi.deleteMcpServer('mcp.github')

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/resources/mcp-servers',
      '/api/resources/mcp-servers/mcp.github',
      '/api/resources/mcp-servers/mcp.github',
      '/api/resources/mcp-servers/mcp.github/disable',
      '/api/resources/mcp-servers/mcp.github'
    ])
    expect(fetchMock.mock.calls.map(([, init]) => init?.method)).toEqual(['POST', undefined, 'PATCH', 'POST', 'DELETE'])
    expect('importMcpConfig' in resourceApi).toBe(false)
    expect('registerDiscoveredMcpServer' in resourceApi).toBe(false)
  })

  it('calls editable tool resource command endpoints with production API paths', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    ))
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000006')

    await resourceApi.registerTool({ name: 'Deploy', kind: 'cli_tool', command: 'deploy' })
    await resourceApi.updateTool('tool.deploy', { name: 'Deploy v2', kind: 'cli_tool', command: 'deploy-v2' })
    await resourceApi.disableTool('tool.deploy')
    await resourceApi.deleteTool('tool.deploy')

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/resources/tools',
      '/api/resources/tools/tool.deploy',
      '/api/resources/tools/tool.deploy/disable',
      '/api/resources/tools/tool.deploy'
    ])
    expect(fetchMock.mock.calls.map(([, init]) => init?.method)).toEqual(['POST', 'PATCH', 'POST', 'DELETE'])
  })

  it('calls manual tool execution endpoint without caller-supplied identity fields', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ output: { count: 2 } }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    ))
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000009')

    await resourceApi.callTool({
      tool_id: 'mcp.github.search_issues',
      arguments: { query: 'is:open' }
    })

    expect(fetchMock.mock.calls[0][0]).toBe('/api/resources/tools/call')
    expect(fetchMock.mock.calls[0][1]?.method).toBe('POST')
    expect(fetchMock.mock.calls[0][1]?.body).toBe(JSON.stringify({
      tool_id: 'mcp.github.search_issues',
      arguments: { query: 'is:open' }
    }))
  })

  it('calls package skill resource command endpoints with production API paths', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    ))
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000007')

    const upload = new FormData()
    upload.set('file', new File(['notes'], 'notes.txt'))
    const pkg = new FormData()
    pkg.set('package', new File(['zip'], 'skill.zip'))

    await resourceApi.getSkillDetail('skill.review')
    await resourceApi.saveSkillMarkdown('skill.review', { raw: '# Review' })
    await resourceApi.getSkillFile('skill.review', 'resources/notes.txt')
    await resourceApi.saveSkillFile('skill.review', 'resources/notes.txt', { content: 'notes' })
    await resourceApi.createSkillFolder('skill.review', 'resources/drafts')
    await resourceApi.uploadSkillFile('skill.review', 'resources/notes.txt', upload)
    await resourceApi.downloadSkillFile('skill.review', 'assets/logo.png')
    await resourceApi.deleteSkillFile('skill.review', 'resources/notes.txt')
    await resourceApi.createSkill({
      name: 'Review',
      description: 'Review code',
      version: '1.0.0',
      task_type_tags: ['review'],
      prompt: 'Read carefully.'
    })
    await resourceApi.importSkill(pkg)
    await resourceApi.exportSkill('skill.review')
    await resourceApi.disableSkill('skill.review')
    await resourceApi.deleteSkill('skill.review')

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/resources/skills/skill.review',
      '/api/resources/skills/skill.review/skill-md',
      '/api/resources/skills/skill.review/files?path=resources%2Fnotes.txt',
      '/api/resources/skills/skill.review/files?path=resources%2Fnotes.txt',
      '/api/resources/skills/skill.review/folders',
      '/api/resources/skills/skill.review/files?path=resources%2Fnotes.txt',
      '/api/resources/skills/skill.review/files/download?path=assets%2Flogo.png',
      '/api/resources/skills/skill.review/files?path=resources%2Fnotes.txt',
      '/api/resources/skills',
      '/api/resources/skills/import',
      '/api/resources/skills/skill.review/export',
      '/api/resources/skills/skill.review/disable',
      '/api/resources/skills/skill.review'
    ])
    expect(fetchMock.mock.calls.map(([, init]) => init?.method)).toEqual([
      undefined,
      'PUT',
      undefined,
      'PUT',
      'POST',
      'POST',
      undefined,
      'DELETE',
      'POST',
      'POST',
      undefined,
      'POST',
      'DELETE'
    ])
  })

  it('calls skill bundle endpoints with encoded ids and cascade flags', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ bundles: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    ))
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000010')

    const pkg = new FormData()
    pkg.set('package', new File(['zip'], 'skill.zip'))

    await resourceApi.listSkillBundles()
    await resourceApi.createSkillBundle({ name: 'Quality pack', skill_ids: ['skill.review'] })
    await resourceApi.addSkillsToBundle('quality/bundle', { skill_ids: ['skill.test'] })
    await resourceApi.importSkillIntoBundle('quality/bundle', pkg)
    await resourceApi.removeSkillFromBundle('quality/bundle', 'skill/review')
    await resourceApi.deleteSkillBundle('quality/bundle', true)

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/resources/skill-bundles',
      '/api/resources/skill-bundles',
      '/api/resources/skill-bundles/quality%2Fbundle/skills',
      '/api/resources/skill-bundles/quality%2Fbundle/skills/import',
      '/api/resources/skill-bundles/quality%2Fbundle/skills/skill%2Freview',
      '/api/resources/skill-bundles/quality%2Fbundle?delete_skills=true'
    ])
    expect(fetchMock.mock.calls.map(([, init]) => init?.method)).toEqual([
      undefined,
      'POST',
      'POST',
      'POST',
      'DELETE',
      'DELETE'
    ])
    expect(fetchMock.mock.calls[1][1]?.body).toBe(JSON.stringify({ name: 'Quality pack', skill_ids: ['skill.review'] }))
    expect(fetchMock.mock.calls[2][1]?.body).toBe(JSON.stringify({ skill_ids: ['skill.test'] }))
    expect(fetchMock.mock.calls[3][1]?.body).toBe(pkg)
  })

  it('calls permission policy endpoints with production API paths', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    ))
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000005')

    const policy = {
      roles: [{ role_id: 'ops-admin', label_zh: '运营管理员', label_en: 'Operations Admin' }],
      role_assignments: [],
      grants: [{ subject_kind: 'role', subject_id: 'ops-admin', scope: 'operations.manage' }]
    }

    await operationsApi.getPermissionPolicy()
    await operationsApi.updatePermissionPolicy(policy)

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/permissions/policy',
      '/api/permissions/policy'
    ])
    expect(fetchMock.mock.calls[1][1]?.method).toBe('PUT')
    expect(fetchMock.mock.calls[1][1]?.body).toBe(JSON.stringify(policy))
  })

})
