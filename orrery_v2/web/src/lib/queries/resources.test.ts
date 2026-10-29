import { describe, expect, it, vi } from 'vitest'
import { resourceApi } from '#lib/api/resources'
import {
  DEFAULT_RESOURCE_PAGE_SIZE,
  buildMemoryDigestQuery,
  buildResourceQuery,
  buildMcpDiscoveryQuery,
  combineMcpServerPages,
  combineMemoryProfilePages,
  combineSkillPages,
  combineToolPages,
  createMemoryResourceMutationRegistry,
  createResourceMutationRegistry,
  nextResourcePageOffset,
  resourceCatalogFilters,
  resourceListFilters,
  resourceQueryKeys
} from './resources'

describe('resource queries', () => {
  it('serializes every supported resource filter', () => {
    expect(
      buildResourceQuery({
        search: 'search gateway',
        category: 'knowledge',
        riskLevel: 'high',
        enabled: false,
        resourceId: 'tool-search',
        boundAgentId: 'profile-a',
        requiredToolId: 'tool-core',
        includeUsage: true,
        includeBindings: true
      })
    ).toBe(
      '?search=search+gateway&category=knowledge&risk_level=high&enabled=false&resource_id=tool-search&bound_agent_id=profile-a&required_tool_id=tool-core&include_usage=true&include_bindings=true'
    )
  })

  it('defaults resource lists to the first 20 rows without relationship details', () => {
    expect(DEFAULT_RESOURCE_PAGE_SIZE).toBe(20)
    expect(buildResourceQuery(resourceListFilters())).toBe('?limit=20&offset=0')
    expect(buildResourceQuery(resourceListFilters({ search: 'deploy' }))).toBe('?search=deploy&limit=20&offset=0')
    expect(buildResourceQuery(resourceListFilters({ includeUsage: true, includeBindings: true }))).toBe(
      '?include_usage=true&include_bindings=true&limit=20&offset=0'
    )
  })

  it('builds untruncated resource catalog filters for people assignment pickers', () => {
    expect(buildResourceQuery(resourceCatalogFilters())).toBe('?limit=1000&offset=0')
    expect(buildResourceQuery(resourceCatalogFilters({ search: 'yunji' }))).toBe('?search=yunji&limit=1000&offset=0')
  })

  it('computes the next resource infinite-scroll offset from page metadata', () => {
    expect(nextResourcePageOffset({ page: { offset: 0, returned: 20, has_more: true } })).toBe(20)
    expect(nextResourcePageOffset({ page: { offset: 40, limit: 20, has_more: true } })).toBe(60)
    expect(nextResourcePageOffset({ page: { offset: 40, returned: 5, has_more: false } })).toBeUndefined()
  })

  it('combines resource pages by resource collection', () => {
    const tools = combineToolPages([
      { tools: [{ tool_id: 'tool.one', kind: 'cli_tool', name: 'One', version: '1.0.0', enabled: true, risk_level: 'low', tags: [], secret_refs: [], permission_declarations: [] }], page: { total: 2, limit: 1, offset: 0, returned: 1, has_more: true } },
      { tools: [{ tool_id: 'tool.two', kind: 'cli_tool', name: 'Two', version: '1.0.0', enabled: true, risk_level: 'low', tags: [], secret_refs: [], permission_declarations: [] }], page: { total: 2, limit: 1, offset: 1, returned: 1, has_more: false } }
    ])
    const skills = combineSkillPages([
      { skills: [{ skill_id: 'skill.one', name: 'One', version: '1.0.0', enabled: true, domain_tags: [], task_type_tags: [], prompt_assets: [], required_tools: [], evidence_refs: [] }], page: { total: 1, limit: 1, offset: 0, returned: 1, has_more: false } }
    ])
    const servers = combineMcpServerPages([
      { mcp_servers: [{ tool_id: 'mcp.one', kind: 'mcp_server', name: 'One', version: '1.0.0', enabled: true, risk_level: 'low', tags: [], secret_refs: [], permission_declarations: [], server_name: 'one', transport: 'stdio', exposed_tools: [], health_check_policy: { mode: 'none' } }], page: { total: 1, limit: 1, offset: 0, returned: 1, has_more: false } }
    ])
    const memory = combineMemoryProfilePages([
      { memory_profiles: [{ memory_profile_id: 'memory.one', memory_kind: 'personal', default_scope: { kind: 'agent', id: 'agent-one' }, retrieval_policy: {}, compression_policy: {}, visibility_policy: { mode: 'owner' }, enabled: true, record_count: 0, candidate_count: 0 }], page: { total: 1, limit: 1, offset: 0, returned: 1, has_more: false } }
    ])

    expect(tools.tools?.map((tool) => tool.tool_id)).toEqual(['tool.one', 'tool.two'])
    expect(skills.skills?.map((skill) => skill.skill_id)).toEqual(['skill.one'])
    expect(servers.mcp_servers?.map((server) => server.tool_id)).toEqual(['mcp.one'])
    expect(memory.memory_profiles?.map((profile) => profile.memory_profile_id)).toEqual(['memory.one'])
  })

  it('keeps resource list caches isolated by filter', () => {
    expect(resourceQueryKeys.tools({ search: 'alpha' })).toEqual([
      'resources',
      'tools',
      '?search=alpha'
    ])
    expect(resourceQueryKeys.mcpServers({ includeUsage: true })).toEqual([
      'resources',
      'mcp-servers',
      '?include_usage=true'
    ])
    expect(resourceQueryKeys.mcpServer('mcp.github')).toEqual([
      'resources',
      'mcp-server',
      'mcp.github'
    ])
  })

  it('builds MCP discovery query paths and cache keys', () => {
    expect(buildMcpDiscoveryQuery({ query: 'linear mcp', limit: 10 })).toBe('?query=linear+mcp&limit=10')
    expect(resourceQueryKeys.mcpDiscovery({ query: 'linear mcp', limit: 10 })).toEqual([
      'resources',
      'mcp-discovery',
      '?query=linear+mcp&limit=10'
    ])
  })

  it('builds memory profile query paths and cache keys', () => {
    expect(resourceQueryKeys.memoryProfiles({ search: 'pm', includeUsage: true })).toEqual([
      'resources',
      'memory-profiles',
      '?search=pm&include_usage=true'
    ])
    expect(resourceQueryKeys.memoryProfile('memory-pm-agent')).toEqual([
      'resources',
      'memory-profile',
      'memory-pm-agent'
    ])
    expect(resourceQueryKeys.personalMemoryProfile('pm-agent')).toEqual([
      'resources',
      'memory-profile',
      'personal',
      'pm-agent'
    ])
    expect(buildMemoryDigestQuery({
      agentId: 'pm-agent',
      missionId: '',
      taskId: 'task-1',
      profileRefs: 'memory-pm-agent, memory-org'
    })).toEqual({
      agent_id: 'pm-agent',
      mission_id: undefined,
      task_id: 'task-1',
      profile_refs: [
        { memory_profile_id: 'memory-pm-agent' },
        { memory_profile_id: 'memory-org' }
      ]
    })
  })

  it('uses a stable skill bundle cache key', () => {
    expect(resourceQueryKeys.skillBundles()).toEqual(['resources', 'skill-bundles'])
  })

  it('wraps memory digest reads behind the query and mapper boundary', async () => {
    const registry = createMemoryResourceMutationRegistry()
    vi.spyOn(resourceApi, 'buildMemoryDigest').mockResolvedValue({
      digest_id: 'digest-1',
      agent_id: 'pm-agent',
      scope: { kind: 'agent', id: 'pm-agent' },
      source_memory_refs: ['memory-pm-agent'],
      source_evidence_refs: [],
      summary: 'remembered',
      confidence: 1,
      visibility: { mode: 'owner' },
      created_at: '2026-06-02T02:00:00Z'
    })

    await registry.buildDigest({
      agentId: 'pm-agent',
      missionId: '',
      taskId: '',
      profileRefs: 'memory-pm-agent'
    })

    expect(resourceApi.buildMemoryDigest).toHaveBeenCalledWith({
      agent_id: 'pm-agent',
      mission_id: undefined,
      task_id: undefined,
      profile_refs: [{ memory_profile_id: 'memory-pm-agent' }]
    })
  })

  it('wraps editable resource command endpoints in mutation registry handlers', async () => {
    const invalidateQueries = vi.fn()
    const registry = createResourceMutationRegistry({ invalidateQueries } as any)

    vi.spyOn(resourceApi, 'importSkill').mockResolvedValue({ skill_id: 'skill.imported' })
    vi.spyOn(resourceApi, 'createSkill').mockResolvedValue({ skill: { skill_id: 'skill.new' }, skill_md: { path: 'SKILL.md', raw: '# New' }, roots: [] })
    vi.spyOn(resourceApi, 'exportSkill').mockResolvedValue(new Blob(['skill']))
    vi.spyOn(resourceApi, 'saveSkillMarkdown').mockResolvedValue({ skill: { skill_id: 'skill.review' }, skill_md: { path: 'SKILL.md', raw: '# Review' }, roots: [] })
    vi.spyOn(resourceApi, 'saveSkillFile').mockResolvedValue({ path: 'resources/notes.txt', name: 'notes.txt', size: 5, media_type: 'text/plain', preview_kind: 'text', node: { path: 'resources/notes.txt', name: 'notes.txt', type: 'file', size: 5, media_type: 'text/plain', preview_kind: 'text' } })
    vi.spyOn(resourceApi, 'createSkillFolder').mockResolvedValue({ status: 'accepted' })
    vi.spyOn(resourceApi, 'uploadSkillFile').mockResolvedValue({ path: 'resources/uploaded.txt', name: 'uploaded.txt', size: 5, media_type: 'text/plain', preview_kind: 'text', node: { path: 'resources/uploaded.txt', name: 'uploaded.txt', type: 'file', size: 5, media_type: 'text/plain', preview_kind: 'text' } })
    vi.spyOn(resourceApi, 'downloadSkillFile').mockResolvedValue(new Blob(['asset']))
    vi.spyOn(resourceApi, 'deleteSkillFile').mockResolvedValue({ status: 'accepted' })
    vi.spyOn(resourceApi, 'disableSkill').mockResolvedValue({ status: 'accepted' })
    vi.spyOn(resourceApi, 'deleteSkill').mockResolvedValue({ status: 'accepted' })
    vi.spyOn(resourceApi, 'registerTool').mockResolvedValue({ tool_id: 'tool.deploy' })
    vi.spyOn(resourceApi, 'updateTool').mockResolvedValue({ tool_id: 'tool.deploy' })
    vi.spyOn(resourceApi, 'disableTool').mockResolvedValue({ status: 'accepted' })
    vi.spyOn(resourceApi, 'deleteTool').mockResolvedValue({ status: 'accepted' })
    vi.spyOn(resourceApi, 'callTool').mockResolvedValue({ output: { ok: true } })
    vi.spyOn(resourceApi, 'registerMcpServer').mockResolvedValue({ tool_id: 'mcp.github' })
    vi.spyOn(resourceApi, 'updateMcpServer').mockResolvedValue({ tool_id: 'mcp.github' })
    vi.spyOn(resourceApi, 'disableMcpServer').mockResolvedValue({ status: 'accepted' })
    vi.spyOn(resourceApi, 'deleteMcpServer').mockResolvedValue({ status: 'accepted' })

    await registry.importSkill(new File(['zip'], 'skill.zip'))
    await registry.createSkill({ name: 'New', description: 'New skill', version: '1.0.0', taskTypeTags: 'draft', prompt: 'Do work.' })
    await registry.exportSkill('skill.review')
    await registry.saveSkillMarkdown('skill.review', '# Review')
    await registry.saveSkillFile('skill.review', 'resources/notes.txt', 'notes')
    await registry.createSkillFolder('skill.review', 'resources/drafts')
    await registry.uploadSkillFile('skill.review', 'resources/uploaded.txt', new FormData())
    await registry.downloadSkillFile('skill.review', 'assets/logo.png')
    await registry.deleteSkillFile('skill.review', 'resources/notes.txt')
    await registry.disableSkill('skill.review')
    await registry.deleteSkill('skill.review')
    await registry.registerTool({ toolId: '', name: 'Deploy', description: '', version: '1.0.0', kind: 'cli_tool', riskLevel: 'medium', command: 'deploy', url: '', tags: '', enabled: 'true' })
    await registry.updateTool('tool.deploy', { toolId: '', name: 'Deploy v2', description: '', version: '1.1.0', kind: 'cli_tool', riskLevel: 'high', command: 'deploy-v2', url: '', tags: '', enabled: 'true' })
    await registry.disableTool('tool.deploy')
    await registry.deleteTool('tool.deploy')
    await registry.callTool({ tool_id: 'tool.deploy', arguments: { dryRun: true } })
    await registry.registerMcpServer({ toolId: '', name: 'GitHub MCP', description: '', version: '', serverName: 'github', transport: 'stdio', command: 'github-mcp-server', url: '', headers: '', args: '', exposedTools: '', tags: '' })
    await registry.registerMcpServer({ toolId: '', name: '', description: '', version: '', serverName: '', transport: 'stdio', command: '', url: '', headers: '', args: '', exposedTools: '', tags: '', configJson: '{"mcpServers":{"github":{"command":"github-mcp-server"}}}' })
    await registry.updateMcpServer('mcp.github', { toolId: '', name: 'GitHub MCP', description: '', version: '1.1.0', serverName: 'github', transport: 'stdio', command: 'github-mcp-server', url: '', headers: '', args: '', exposedTools: '', tags: '' })
    await registry.disableMcpServer('mcp.github')
    await registry.deleteMcpServer('mcp.github')

    expect(resourceApi.importSkill).toHaveBeenCalledWith(expect.any(FormData))
    expect(resourceApi.createSkill).toHaveBeenCalledWith({
      name: 'New',
      description: 'New skill',
      version: '1.0.0',
      task_type_tags: ['draft'],
      prompt: 'Do work.'
    })
    expect(resourceApi.saveSkillMarkdown).toHaveBeenCalledWith('skill.review', { raw: '# Review' })
    expect(resourceApi.saveSkillFile).toHaveBeenCalledWith('skill.review', 'resources/notes.txt', { content: 'notes' })
    expect(resourceApi.createSkillFolder).toHaveBeenCalledWith('skill.review', 'resources/drafts')
    expect(resourceApi.uploadSkillFile).toHaveBeenCalledWith('skill.review', 'resources/uploaded.txt', expect.any(FormData))
    expect(resourceApi.downloadSkillFile).toHaveBeenCalledWith('skill.review', 'assets/logo.png')
    expect(resourceApi.deleteSkillFile).toHaveBeenCalledWith('skill.review', 'resources/notes.txt')
    expect(resourceApi.registerTool).toHaveBeenCalledWith(expect.objectContaining({ name: 'Deploy', kind: 'cli_tool' }))
    expect(resourceApi.updateTool).toHaveBeenCalledWith('tool.deploy', expect.objectContaining({ name: 'Deploy v2', command: 'deploy-v2' }))
    expect(resourceApi.disableTool).toHaveBeenCalledWith('tool.deploy')
    expect(resourceApi.deleteTool).toHaveBeenCalledWith('tool.deploy')
    expect(resourceApi.callTool).toHaveBeenCalledWith({ tool_id: 'tool.deploy', arguments: { dryRun: true } })
    expect(resourceApi.registerMcpServer).toHaveBeenCalledWith(expect.objectContaining({ name: 'GitHub MCP', server_name: 'github' }))
    expect(resourceApi.registerMcpServer).toHaveBeenCalledWith(expect.objectContaining({ name: 'github', server_name: 'github', command: 'github-mcp-server' }))
    expect(resourceApi.updateMcpServer).toHaveBeenCalledWith('mcp.github', expect.objectContaining({ name: 'GitHub MCP', headers: {} }))
    expect('importMcpConfig' in resourceApi).toBe(false)
    expect('registerDiscoveredMcpServer' in resourceApi).toBe(false)
    expect(resourceApi.disableMcpServer).toHaveBeenCalledWith('mcp.github')
    expect(resourceApi.deleteSkill).toHaveBeenCalledWith('skill.review')
    expect(resourceApi.deleteMcpServer).toHaveBeenCalledWith('mcp.github')
    expect(invalidateQueries).toHaveBeenCalled()
  })

  it('wraps skill bundle commands and invalidates resource caches', async () => {
    const invalidateQueries = vi.fn()
    const registry = createResourceMutationRegistry({ invalidateQueries } as any)
    const bundle = {
      bundle_id: 'bundle.quality',
      name: 'Quality pack',
      skill_ids: ['skill.review'],
      skills: [{ skill_id: 'skill.review' }],
      created_at: '2026-07-19T08:00:00Z',
      updated_at: '2026-07-19T08:00:00Z'
    }

    vi.spyOn(resourceApi, 'createSkillBundle').mockResolvedValue(bundle)
    vi.spyOn(resourceApi, 'addSkillsToBundle').mockResolvedValue(bundle)
    vi.spyOn(resourceApi, 'importSkillIntoBundle').mockResolvedValue(bundle)
    vi.spyOn(resourceApi, 'removeSkillFromBundle').mockResolvedValue(bundle)
    vi.spyOn(resourceApi, 'deleteSkillBundle').mockResolvedValue({ status: 'accepted' })

    await registry.createSkillBundle({ name: 'Quality pack', description: 'Checks', skillIds: ['skill.review'] })
    await registry.addSkillsToBundle('bundle.quality', ['skill.test'])
    await registry.importSkillIntoBundle('bundle.quality', new File(['zip'], 'skill.zip'))
    await registry.removeSkillFromBundle('bundle.quality', 'skill.review')
    await registry.deleteSkillBundle('bundle.quality', true)

    expect(resourceApi.createSkillBundle).toHaveBeenCalledWith({
      name: 'Quality pack',
      description: 'Checks',
      skill_ids: ['skill.review']
    })
    expect(resourceApi.addSkillsToBundle).toHaveBeenCalledWith('bundle.quality', { skill_ids: ['skill.test'] })
    expect(resourceApi.importSkillIntoBundle).toHaveBeenCalledWith('bundle.quality', expect.any(FormData))
    expect(resourceApi.removeSkillFromBundle).toHaveBeenCalledWith('bundle.quality', 'skill.review')
    expect(resourceApi.deleteSkillBundle).toHaveBeenCalledWith('bundle.quality', true)
    expect(invalidateQueries).toHaveBeenCalledTimes(5)
    expect(invalidateQueries).toHaveBeenLastCalledWith({ queryKey: resourceQueryKeys.all })
  })
})
