import { describe, expect, it } from 'vitest'
import {
  mapMemoryDigestCommand,
  mapMemoryDigestView,
  mapMemoryProfileListView,
  mapMcpConfigCommands,
  mapMcpServerCommand,
  mapMcpDiscoveryView,
  mapMcpServerPatchCommand,
  mapResourceBindingCommand,
  mapMcpServerListView,
  mapSkillBundleListView,
  mapSkillListView,
  mapToolGatewayView,
  mapToolPatchCommand
} from './resource'
import type { DiscoverMCPResponseDTO, ResourceAdminViewDTO, SkillBundleListDTO } from '#lib/types/api'
import type { MemoryResourceAdminViewDTO } from '#lib/types/resource-memory'

const resourceView: ResourceAdminViewDTO = {
  tools: [
    {
      tool_id: 'tool-search',
      kind: 'http',
      name: 'Search Gateway',
      description: 'Finds indexed mission material.',
      version: '1.2.0',
      command: 'search-cli',
      enabled: false,
      risk_level: 'high',
      tags: ['knowledge', 'search'],
      secret_refs: ['search-token'],
      permission_declarations: [{ scope: 'network:egress', risk: 'high', required: true }],
      created_at: '2026-05-20T01:00:00Z',
      updated_at: '2026-05-22T02:00:00Z'
    }
  ],
  skills: [
    {
      skill_id: 'skill-brief',
      name: 'Brief Composer',
      version: '0.4.0',
      description: 'Drafts concise decision briefs.',
      domain_tags: ['operations'],
      task_type_tags: ['writing'],
      risk_level: 'medium',
      prompt_assets: [{ path: 'brief.md', kind: 'markdown' }],
      required_tools: [{ tool_id: 'tool-search', alias: 'search' }],
      evidence_refs: [{ id: 'sample-brief' }],
      enabled: true,
      source_ref: 'git://skills/brief',
      created_at: '2026-05-18T01:00:00Z',
      updated_at: '2026-05-23T02:00:00Z'
    }
  ],
  mcp_servers: [
    {
      tool_id: 'mcp-docs',
      kind: 'mcp',
      name: 'Docs MCP',
      description: 'Project documentation tools.',
      version: '2.0.0',
      enabled: true,
      risk_level: 'medium',
      tags: ['docs'],
      secret_refs: [],
      permission_declarations: [{ scope: 'filesystem:read', risk: 'medium', required: true }],
      created_at: '2026-05-10T01:00:00Z',
      updated_at: '2026-05-24T02:00:00Z',
      server_name: 'docs',
      transport: 'stdio',
      command: 'node',
      headers: {
        Authorization: '********',
        'X-Tenant': 'docs'
      },
      args: ['server.mjs'],
      exposed_tools: ['read_doc', 'search_doc'],
      exposed_tool_details: [
        {
          name: 'read_doc',
          description: 'Read a document by path.',
          input_schema: { type: 'object' }
        }
      ],
      health_check_policy: { mode: 'startup', timeout: '2s' }
    }
  ],
  usage: [
    {
      resource_id: 'tool-search',
      resource_type: 'tool',
      used_by_agent_ids: ['agent-a', 'agent-b'],
      required_by_skill_ids: ['skill-brief'],
      usage_count: 17
    },
    {
      resource_id: 'mcp-docs',
      resource_type: 'mcp_server',
      used_by_agent_ids: ['agent-a'],
      required_by_skill_ids: [],
      usage_count: 4
    }
  ],
  bindings: [
    {
      resource_id: 'tool-search',
      resource_type: 'tool',
      binding_type: 'agent_tool_ref',
      bound_to_type: 'agent',
      bound_to_id: 'agent-a',
      bound_to_display_name: 'Research Agent',
      alias: 'search',
      permission_scope: 'mission',
      mcp_auth_required: true,
      auth_header_name: 'Authorization',
      default_ak_configured: false,
      agent_ak_configured: true,
      agent_ak_masked: 'Bear****oken'
    }
  ],
  page: { total: 1, limit: 20, offset: 0 }
}

describe('resource mappers', () => {
  it('maps disabled tools with usage and bindings', () => {
    const view = mapToolGatewayView(resourceView)

    expect(view.summary.total).toBe(1)
    expect(view.summary.enabled).toBe(0)
    expect(view.summary.disabled).toBe(1)
    expect(view.summary.highRisk).toBe(1)
    expect(view.tools[0]).toMatchObject({
      id: 'tool-search',
      name: 'Search Gateway',
      category: 'knowledge',
      statusLabel: 'Disabled',
      statusTone: 'danger',
      command: 'search-cli',
      url: '',
      usageCount: 17,
      boundAgentCount: 1
    })
    expect(view.tools[0].boundAgents).toEqual(['Research Agent'])
    expect(view.tools[0].bindings[0]).toMatchObject({
      mcpAuthRequired: true,
      authHeaderName: 'Authorization',
      defaultAKConfigured: false,
      agentAKConfigured: true,
      agentAKMasked: 'Bear****oken'
    })
    expect(view.tools[0].permissions).toEqual(['network:egress'])
  })

  it('maps gateway-backed MCP tool metadata as read-only runtime tools', () => {
    const view = mapToolGatewayView({
      tools: [
        {
          tool_id: 'mcp.github.search_issues',
          kind: 'mcp_tool',
          name: 'GitHub MCP: search_issues',
          description: 'Search issues in GitHub.',
          version: 'runtime',
          enabled: true,
          risk_level: 'medium',
          tags: ['mcp', 'external'],
          source: 'external_mcp',
          server_tool_id: 'mcp.github',
          downstream_tool_name: 'search_issues',
          input_schema: {
            type: 'object',
            required: ['query'],
            properties: {
              query: { type: 'string', description: 'Search query' },
              limit: { type: 'integer', default: 10 }
            }
          },
          manageable: false,
          permission_declarations: [{ scope: 'github.read', risk: 'medium', required: true }]
        }
      ],
      page: { total: 1, limit: 20, offset: 0 }
    } satisfies ResourceAdminViewDTO)

    expect(view.tools[0]).toMatchObject({
      id: 'mcp.github.search_issues',
      kind: 'mcp_tool',
      source: 'external_mcp',
      serverToolId: 'mcp.github',
      downstreamToolName: 'search_issues',
      manageable: false,
      permissions: ['github.read']
    })
    expect(view.tools[0].inputSchema?.properties).toMatchObject({
      query: { type: 'string', description: 'Search query' }
    })
  })

  it('uses translatable sentinels for missing resource descriptions', () => {
    const view = mapToolGatewayView({
      tools: [
        {
          tool_id: 'tool-empty',
          enabled: true,
          created_at: '2026-06-01T01:00:00Z'
        }
      ],
      skills: [
        {
          skill_id: 'skill-empty',
          enabled: true,
          created_at: '2026-06-01T01:00:00Z'
        }
      ],
      mcp_servers: [],
      usage: [],
      bindings: [],
      page: { total: 1, limit: 20, offset: 0 }
    } satisfies ResourceAdminViewDTO)
    const skills = mapSkillListView({
      tools: [],
      skills: [
        {
          skill_id: 'skill-empty',
          enabled: true,
          created_at: '2026-06-01T01:00:00Z'
        }
      ],
      mcp_servers: [],
      usage: [],
      bindings: [],
      page: { total: 1, limit: 20, offset: 0 }
    } satisfies ResourceAdminViewDTO)

    expect(view.tools[0].description).toBe('Not provided')
    expect(skills.skills[0].description).toBe('Not provided')
  })

  it('uses translatable sentinels for missing resource metadata fields', () => {
    const emptyResourceView = {
      tools: [
        {
          tool_id: 'tool-empty',
          enabled: true,
          created_at: ''
        }
      ],
      skills: [
        {
          skill_id: 'skill-empty',
          enabled: true,
          created_at: ''
        }
      ],
      mcp_servers: [
        {
          tool_id: 'mcp-empty',
          enabled: true,
          created_at: ''
        }
      ],
      usage: [],
      bindings: [
        {
          resource_id: 'tool-empty',
          resource_type: 'tool',
          binding_type: 'agent_tool_ref',
          bound_to_type: 'agent',
          bound_to_id: '',
          bound_to_display_name: '',
          alias: '',
          permission_scope: ''
        }
      ],
      page: { total: 3, limit: 20, offset: 0 }
    } satisfies ResourceAdminViewDTO

    const tools = mapToolGatewayView(emptyResourceView)
    const skills = mapSkillListView(emptyResourceView)
    const mcp = mapMcpServerListView(emptyResourceView)

    expect(tools.tools[0]).toMatchObject({
      category: 'Not provided',
      version: 'Not provided',
      updatedAt: 'Not provided'
    })
    expect(tools.tools[0].bindings[0]).toMatchObject({
      agentId: 'Not provided',
      alias: 'Not provided',
      scope: 'Not provided'
    })
    expect(skills.skills[0]).toMatchObject({
      category: 'Not provided',
      version: 'Not provided',
      sourceRef: 'Not provided',
      updatedAt: 'Not provided'
    })
    expect(mcp.servers[0]).toMatchObject({
      transport: 'Not provided',
      command: 'Not provided',
      endpoint: 'Not provided',
      headers: {},
      healthPolicy: 'Not provided',
      updatedAt: 'Not provided'
    })
  })

  it('maps skills with required tool and category metadata', () => {
    const view = mapSkillListView(resourceView)

    expect(view.summary.total).toBe(1)
    expect(view.summary.enabled).toBe(1)
    expect(view.skills[0]).toMatchObject({
      id: 'skill-brief',
      category: 'operations',
      taskTypes: ['writing'],
      requiredTools: ['tool-search'],
      promptAssets: ['brief.md'],
      evidenceRefs: ['sample-brief'],
      riskLevel: 'medium',
      riskTone: 'warning',
      statusLabel: 'Enabled'
    })
  })

  it('maps skill bundles and their hydrated member skills', () => {
    const view = mapSkillBundleListView({
      bundles: [{
        bundle_id: 'bundle.quality',
        name: 'Quality pack',
        description: 'Review and test workflow',
        skill_ids: ['skill-brief'],
        skills: resourceView.skills ?? [],
        created_by: 'operator-1',
        created_at: '2026-07-19T08:00:00Z',
        updated_at: '2026-07-19T09:00:00Z'
      }]
    } satisfies SkillBundleListDTO)

    expect(view.bundles[0]).toMatchObject({
      id: 'bundle.quality',
      name: 'Quality pack',
      description: 'Review and test workflow',
      skillIds: ['skill-brief'],
      memberCount: 1,
      createdBy: 'operator-1',
      updatedAt: 'Jul 19, 2026'
    })
    expect(view.bundles[0].skills[0]).toMatchObject({
      id: 'skill-brief',
      name: 'Brief Composer',
      category: 'operations'
    })
  })

  it('summarizes high-risk skills from package risk metadata', () => {
    const view = mapSkillListView({
      skills: [
        {
          skill_id: 'skill-exec',
          name: 'Executable skill',
          version: '1.0.0',
          enabled: true,
          risk_level: 'high',
          task_type_tags: []
        },
        {
          skill_id: 'skill-text',
          name: 'Text skill',
          version: '1.0.0',
          enabled: true,
          risk_level: 'low',
          task_type_tags: []
        }
      ],
      page: { total: 2, limit: 20, offset: 0 }
    } satisfies ResourceAdminViewDTO)

    expect(view.summary.highRisk).toBe(1)
    expect(view.skills[0]).toMatchObject({ riskLevel: 'high', riskTone: 'danger' })
  })

  it('maps mcp servers with transport and exposed tools', () => {
    const view = mapMcpServerListView(resourceView)

    expect(view.summary.total).toBe(1)
    expect(view.summary.enabled).toBe(1)
    expect(view.servers[0]).toMatchObject({
      id: 'mcp-docs',
      serverName: 'docs',
      transport: 'stdio',
      headers: {
        Authorization: '********',
        'X-Tenant': 'docs'
      },
      exposedTools: ['read_doc', 'search_doc'],
      exposedToolDetails: [
        {
          name: 'read_doc',
          description: 'Read a document by path.',
          inputSchema: { type: 'object' }
        }
      ],
      healthPolicy: 'startup (2s)'
    })
  })

  it('maps MCP auth requirement and saved probe metadata', () => {
    const view = mapMcpServerListView({
      mcp_servers: [
        {
          tool_id: 'mcp-secure',
          kind: 'mcp_server',
          name: 'Secure MCP',
          enabled: true,
          risk_level: 'medium',
          server_name: 'secure',
          transport: 'streamable_http',
          url: 'http://127.0.0.1:5000/api/mcp',
          auth_required: true,
          auth_mode: 'per_agent',
          auth_header_name: 'Authorization',
          default_ak_configured: false,
          availability_status: 'requires_auth',
          last_probe: {
            status: 'requires_auth',
            failure_reason: 'Unauthorized: Invalid or missing API Key',
            http_status: 401,
            latency_ms: 163,
            checked_at: '2026-06-12T08:00:00Z'
          }
        }
      ],
      page: { total: 1, limit: 20, offset: 0 }
    } satisfies ResourceAdminViewDTO)

    expect(view.servers[0]).toMatchObject({
      id: 'mcp-secure',
      enabled: true,
      statusLabel: 'Requires auth',
      statusTone: 'warning',
      authRequired: true,
      authMode: 'per_agent',
      authHeaderName: 'Authorization',
      defaultAKConfigured: false,
      availabilityStatus: 'requires_auth',
      lastProbe: {
        status: 'requires_auth',
        failureReason: 'Unauthorized: Invalid or missing API Key',
        httpStatus: 401,
        latencyMs: 163
      }
    })
  })

  it('maps memory profiles with readable scope, policy, and usage counts', () => {
    const view = mapMemoryProfileListView({
      memory_profiles: [
        {
          memory_profile_id: 'memory-pm-agent',
          owner_agent_id: 'pm-agent',
          organization_id: 'org-core',
          department_id: 'dept-product',
          memory_kind: 'personal',
          default_scope: { kind: 'agent', id: 'pm-agent' },
          retrieval_policy: { max_records: 12, tags: ['pm', 'coordination'] },
          compression_policy: { max_summary_bytes: 4096 },
          visibility_policy: { mode: 'owner', scope_kind: 'agent', scope_id: 'pm-agent' },
          enabled: true,
          record_count: 7,
          candidate_count: 2
        }
      ],
      bindings: [
        {
          resource_id: 'memory-pm-agent',
          resource_type: 'memory_profile',
          bound_to_id: 'pm-agent',
          bound_to_display_name: 'PM Agent',
          permission_scope: 'personal'
        }
      ],
      page: { total: 1, limit: 20, offset: 0 }
    } satisfies MemoryResourceAdminViewDTO)

    expect(view.summary).toMatchObject({
      total: 1,
      enabled: 1,
      disabled: 0,
      bound: 1,
      usageCount: 7
    })
    expect(view.profiles[0]).toMatchObject({
      id: 'memory-pm-agent',
      ownerAgentId: 'pm-agent',
      memoryKind: 'personal',
      defaultScopeLabel: 'agent:pm-agent',
      visibilityLabel: 'owner (agent:pm-agent)',
      retrievalTags: ['pm', 'coordination'],
      maxRecords: 12,
      maxSummaryBytes: 4096,
      recordCount: 7,
      candidateCount: 2,
      boundAgents: ['PM Agent']
    })
  })

  it('maps memory digest form values into backend commands', () => {
    expect(
      mapMemoryDigestCommand({
        agentId: ' pm-agent ',
        missionId: ' mission-1 ',
        taskId: '',
        profileRefs: ' memory-pm-agent, memory-org ,, '
      })
    ).toEqual({
      agent_id: 'pm-agent',
      mission_id: 'mission-1',
      task_id: undefined,
      profile_refs: [
        { memory_profile_id: 'memory-pm-agent' },
        { memory_profile_id: 'memory-org' }
      ]
    })
  })

  it('maps memory digest responses for report-like rendering', () => {
    const digest = mapMemoryDigestView({
      digest_id: 'digest-1',
      agent_id: 'pm-agent',
      mission_id: 'mission-1',
      scope: { kind: 'mission', id: 'mission-1' },
      source_memory_refs: ['memory-pm-agent'],
      source_evidence_refs: ['evidence-1'],
      summary: 'PM remembers the delivery preference.',
      facts: ['Stakeholder wants weekly updates.'],
      preferences: ['Use concise reports.'],
      warnings: ['Timeline is tight.'],
      decisions: ['Start with Lead staffing.'],
      confidence: 0.86,
      visibility: { mode: 'mission', scope_kind: 'mission', scope_id: 'mission-1' },
      created_at: '2026-06-02T02:00:00Z'
    })

    expect(digest).toMatchObject({
      id: 'digest-1',
      agentId: 'pm-agent',
      scopeLabel: 'mission:mission-1',
      confidenceLabel: '86%',
      sourceMemoryRefs: ['memory-pm-agent'],
      facts: ['Stakeholder wants weekly updates.']
    })
  })

  it('maps MCP discovery results without exposing internal ids as input labels', () => {
    const view = mapMcpDiscoveryView({
      servers: [
        {
          discovered_id: 'linear',
          name: 'Linear MCP',
          description: 'Issue tracking tools.',
          version: '1.0.0',
          package_refs: ['npm:@linear/mcp@1.0.0'],
          tools: ['issue.search', 'issue.create'],
          env_secret_refs: [{ name: 'mcp.linear.api_key', env: 'LINEAR_API_KEY' }],
          homepage: 'https://linear.app'
        }
      ]
    } satisfies DiscoverMCPResponseDTO)

    expect(view.servers[0]).toMatchObject({
      id: 'linear',
      name: 'Linear MCP',
      packageRefs: ['npm:@linear/mcp@1.0.0'],
      tools: ['issue.search', 'issue.create'],
      secretEnvNames: ['LINEAR_API_KEY'],
      homepage: 'https://linear.app'
    })
  })

  it('maps resource binding form values into backend commands', () => {
    expect(
      mapResourceBindingCommand({
        resourceId: 'tool-search',
        resourceType: 'tool',
        agentId: ' profile-a ',
        alias: ' ',
        scope: 'mission'
      })
    ).toEqual({
      agent_id: 'profile-a',
      resource_id: 'tool-search',
      resource_type: 'tool',
      alias: undefined,
      permission_scope: 'mission',
      agent_ak: undefined,
      auth_header_name: undefined
    })
  })

  it('maps per-agent MCP AK fields into backend bind requests', () => {
    expect(
      mapResourceBindingCommand({
        resourceId: 'mcp-private',
        resourceType: 'mcp_server',
        agentId: 'profile-a',
        alias: 'private',
        scope: 'mission',
        agentAK: ' Bearer agent-token ',
        authHeaderName: ' Authorization '
      })
    ).toEqual({
      agent_id: 'profile-a',
      resource_id: 'mcp-private',
      resource_type: 'mcp_server',
      alias: 'private',
      permission_scope: 'mission',
      agent_ak: 'Bearer agent-token',
      auth_header_name: 'Authorization'
    })
  })

  it('maps skill resource bindings into backend bind requests', () => {
    expect(
      mapResourceBindingCommand({
        resourceId: 'skill-brief',
        resourceType: 'skill',
        agentId: 'profile-a',
        alias: 'brief',
        scope: 'workspace'
      })
    ).toEqual({
      agent_id: 'profile-a',
      resource_id: 'skill-brief',
      resource_type: 'skill',
      alias: 'brief',
      permission_scope: 'workspace',
      agent_ak: undefined,
      auth_header_name: undefined
    })
  })

  it('maps MCP command forms and pasted JSON into regular register requests', () => {
    expect(
      mapMcpServerCommand({
        toolId: '',
        name: ' GitHub MCP ',
        description: ' Repo tools ',
        version: '',
        serverName: ' github ',
        transport: 'stdio',
        command: ' github-mcp-server ',
        url: '',
        headers: '{ "Authorization": "Bearer sk_test", "X-Tenant": "demo" }',
        args: '--stdio, --verbose',
        exposedTools: 'repo.read, issue.create',
        tags: 'engineering'
      })
    ).toEqual({
      name: 'GitHub MCP',
      description: 'Repo tools',
      version: '1.0.0',
      server_name: 'github',
      transport: 'stdio',
      command: 'github-mcp-server',
      url: undefined,
      headers: {
        Authorization: 'Bearer sk_test',
        'X-Tenant': 'demo'
      },
      args: ['--stdio', '--verbose'],
      exposed_tools: ['repo.read', 'issue.create'],
      tags: ['engineering']
    })

    expect(
      mapMcpConfigCommands(`{
        "mcpServers": {
          "yunji-zhaopin": {
            "url": "http://127.0.0.1:5000/api/mcp",
            "headers": {
              "Authorization": "Bearer sk_valid"
            },
            "tools": ["resume.search"]
          }
        }
      }`)
    ).toEqual([
      {
        name: 'yunji-zhaopin',
        description: undefined,
        version: '1.0.0',
        server_name: 'yunji-zhaopin',
        transport: 'streamable_http',
        command: undefined,
        url: 'http://127.0.0.1:5000/api/mcp',
        headers: {
          Authorization: 'Bearer sk_valid'
        },
        auth_header_name: 'Authorization',
        args: undefined,
        env_secret_refs: undefined,
        exposed_tools: ['resume.search'],
        tags: ['mcp', 'json-import']
      }
    ])
  })

  it('keeps explicit empty fields in patch commands so updates can clear optional resource metadata', () => {
    expect(
      mapToolPatchCommand({
        toolId: 'tool.deploy',
        name: 'Deploy',
        description: '',
        version: '1.0.0',
        kind: 'cli_tool',
        riskLevel: 'medium',
        command: 'deploy',
        url: '',
        tags: '',
        enabled: 'true'
      })
    ).toEqual({
      name: 'Deploy',
      description: '',
      version: '1.0.0',
      kind: 'cli_tool',
      risk_level: 'medium',
      enabled: true,
      command: 'deploy',
      url: undefined,
      tags: []
    })

    expect(
      mapMcpServerPatchCommand({
        toolId: 'mcp.github',
        name: 'GitHub MCP',
        description: '',
        version: '1.0.0',
        serverName: 'github',
        transport: 'streamable_http',
        command: '',
        url: 'https://mcp.example.test',
        headers: '',
        authHeaderName: '',
        args: '',
        exposedTools: '',
        tags: ''
      })
    ).toEqual({
      name: 'GitHub MCP',
      description: '',
      version: '1.0.0',
      server_name: 'github',
      transport: 'streamable_http',
      command: '',
      url: 'https://mcp.example.test',
      headers: {},
      auth_header_name: '',
      args: [],
      exposed_tools: [],
      tags: []
    })
  })

  it('maps default MCP AK into the selected auth header while preserving other headers', () => {
    const command = mapMcpServerCommand({
      toolId: '',
      configJson: '',
      name: 'Private MCP',
      description: '',
      version: '',
      serverName: 'private',
      transport: 'streamable_http',
      command: '',
      url: 'https://mcp.example.test/api',
      headers: '{ "X-Trace": "trace-1" }',
      authHeaderName: 'X-API-Key',
      defaultAK: ' sk_default ',
      args: '',
      exposedTools: '',
      tags: ''
    })

    expect(command.headers).toEqual({
      'X-Trace': 'trace-1',
      'X-API-Key': 'sk_default'
    })
  })

  it('rejects invalid MCP headers JSON instead of silently dropping credentials', () => {
    expect(() =>
      mapMcpServerCommand({
        toolId: '',
        name: 'GitHub MCP',
        description: '',
        version: '',
        serverName: 'github',
        transport: 'streamable_http',
        command: '',
        url: 'https://mcp.example.test',
        headers: '{ "Authorization": ',
        args: '',
        exposedTools: '',
        tags: ''
      })
    ).toThrow('MCP headers must be a JSON object with string values')
  })
})
