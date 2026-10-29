import type {
  DiscoverMCPResponseDTO,
  MCPExposedToolDTO,
  PatchMCPServerRequest,
  PatchToolDefinitionRequest,
  RegisterMCPServerRequest,
  ResourceAdminViewDTO,
  ResourceBindingCommand,
  ResourceBindingDTO,
  ResourceUsageDTO,
  SkillBundleListDTO,
  SkillDTO,
  ToolDefinitionRequest
} from '#lib/types/api'
import type {
  BadgeTone,
  McpDiscoveryViewModel,
  McpServerListViewModel,
  ResourceMcpServerCommandFormViewModel,
  ResourceBindingFormViewModel,
  ResourceBindingViewModel,
  ResourceToolCommandFormViewModel,
  ResourceSummaryViewModel,
  SkillBundleListViewModel,
  SkillListViewModel,
  SkillViewModel,
  ToolGatewayViewModel,
  ToolViewModel
} from '#lib/types/view-models'
import type {
  BuildMemoryDigestRequest,
  MemoryDigestDTO,
  MemoryDigestFormViewModel,
  MemoryDigestViewModel,
  MemoryProfileDTO,
  MemoryProfileListViewModel,
  MemoryProfileViewModel,
  MemoryResourceAdminViewDTO
} from '#lib/types/resource-memory'

export function mapToolGatewayView(dto: ResourceAdminViewDTO): ToolGatewayViewModel {
  const tools = (dto.tools ?? []).map((tool) =>
    mapTool(tool, usageFor(dto.usage, tool.tool_id), bindingsFor(dto.bindings, tool.tool_id))
  )

  return {
    summary: summarizeResources(tools),
    tools
  }
}

export function mapSkillListView(dto: ResourceAdminViewDTO): SkillListViewModel {
  const skills = (dto.skills ?? []).map((skill) =>
    mapSkillViewModel(skill, usageFor(dto.usage, skill.skill_id), bindingsFor(dto.bindings, skill.skill_id))
  )

  return {
    summary: summarizeResources(skills),
    skills
  }
}

export function mapSkillBundleListView(dto: SkillBundleListDTO): SkillBundleListViewModel {
  return {
    bundles: (dto.bundles ?? []).map((bundle) => ({
      id: bundle.bundle_id,
      name: fallback(bundle.name, bundle.bundle_id),
      description: fallback(bundle.description, 'Not provided'),
      skillIds: bundle.skill_ids ?? [],
      skills: (bundle.skills ?? []).map((skill) => mapSkillViewModel(skill)),
      memberCount: bundle.skill_ids?.length ?? bundle.skills?.length ?? 0,
      createdBy: fallback(bundle.created_by, 'Not provided'),
      createdAt: formatDate(bundle.created_at),
      updatedAt: formatDate(bundle.updated_at ?? bundle.created_at)
    }))
  }
}

function mapSkillViewModel(skill: SkillDTO, usage?: ResourceUsageDTO, bindings: ResourceBindingDTO[] = []): SkillViewModel {
  const enabled = skill.enabled !== false
  const riskLevel = fallback(skill.risk_level, 'low').toLowerCase()

  return {
    id: skill.skill_id,
    name: fallback(skill.name, skill.skill_id),
    description: fallback(skill.description, 'Not provided'),
    category: firstValue(skill.domain_tags, 'Not provided'),
    version: fallback(skill.version, 'Not provided'),
    enabled,
    riskLevel,
    riskTone: riskTone(riskLevel),
    statusLabel: enabled ? 'Enabled' : 'Disabled',
    statusTone: enabled ? 'success' : 'danger',
    taskTypes: skill.task_type_tags ?? [],
    requiredTools: toolRefIds(skill.required_tools),
    promptAssets: promptAssetPaths(skill.prompt_assets),
    evidenceRefs: evidenceRefIds(skill.evidence_refs),
    sourceRef: fallback(skill.source_ref, 'Not provided'),
    updatedAt: formatDate(skill.updated_at ?? skill.created_at),
    skillMdPresent: skill.skill_md_present === true,
    resourceCounts: {
      assets: skill.resource_counts?.assets ?? 0,
      resources: skill.resource_counts?.resources ?? 0,
      references: skill.resource_counts?.references ?? 0,
      scripts: skill.resource_counts?.scripts ?? 0,
      other: skill.resource_counts?.other ?? 0,
      total: skill.resource_counts?.total ?? 0
    },
    usageCount: usage?.usage_count ?? 0,
    boundAgentCount: bindings.length,
    boundAgents: bindings.map((binding) => fallback(binding.bound_to_display_name, binding.bound_to_id))
  }
}

export function mapMcpServerListView(dto: ResourceAdminViewDTO): McpServerListViewModel {
  const servers = (dto.mcp_servers ?? []).map((server) => {
    const exposedToolDetails = mapMcpExposedToolDetails(server.exposed_tool_details)
    const tool = mapTool(server, usageFor(dto.usage, server.tool_id), bindingsFor(dto.bindings, server.tool_id))
    const availability = server.availability_status ?? ''
    const status = mcpStatus(availability, tool.enabled)
    return {
      ...tool,
      statusLabel: status.label,
      statusTone: status.tone,
      serverName: fallback(server.server_name, server.name ?? server.tool_id),
      transport: fallback(server.transport, 'Not provided'),
      command: fallback(server.command, 'Not provided'),
      endpoint: fallback(server.url, server.command),
      headers: server.headers ?? {},
      authRequired: server.auth_required === true,
      authMode: server.auth_mode ?? '',
      authHeaderName: server.auth_header_name ?? '',
      defaultAKConfigured: server.default_ak_configured === true,
      availabilityStatus: availability,
      lastProbe: mapMcpProbe(server.last_probe),
      args: server.args ?? [],
      exposedTools: server.exposed_tools?.length ? server.exposed_tools : exposedToolDetails.map((tool) => tool.name),
      exposedToolDetails,
      healthPolicy: formatHealthPolicy(server.health_check_policy)
    }
  })

  return {
    summary: summarizeResources(servers),
    servers
  }
}

function mapMcpExposedToolDetails(details: MCPExposedToolDTO[] | undefined) {
  return (details ?? [])
    .filter((tool) => Boolean(tool.name))
    .map((tool) => ({
      name: tool.name,
      description: tool.description ?? '',
      inputSchema: tool.input_schema,
      outputSchema: tool.output_schema
    }))
}

export function mapMcpDiscoveryView(dto: DiscoverMCPResponseDTO): McpDiscoveryViewModel {
  return {
    servers: (dto.servers ?? []).map((server) => ({
      id: server.discovered_id,
      name: fallback(server.name, server.discovered_id),
      description: fallback(server.description, 'Not provided'),
      version: fallback(server.version, 'Not provided'),
      packageRefs: server.package_refs ?? [],
      tools: server.tools ?? [],
      secretEnvNames: (server.env_secret_refs ?? []).map((ref) => fallback(ref.env, ref.name)),
      homepage: server.homepage ?? ''
    }))
  }
}

export function mapMemoryProfileListView(dto: MemoryResourceAdminViewDTO): MemoryProfileListViewModel {
  const profiles = (dto.memory_profiles ?? []).map((profile) => mapMemoryProfile(profile, bindingsFor(dto.bindings, profile.memory_profile_id)))

  return {
    summary: summarizeResources(profiles.map((profile) => ({
      enabled: profile.enabled,
      usageCount: profile.recordCount,
      boundAgentCount: profile.boundAgents.length
    }))),
    profiles
  }
}

export function mapMemoryDigestCommand(form: MemoryDigestFormViewModel): BuildMemoryDigestRequest {
  return {
    agent_id: form.agentId.trim(),
    mission_id: optionalText(form.missionId),
    task_id: optionalText(form.taskId),
    profile_refs: csv(form.profileRefs).map((profileId) => ({ memory_profile_id: profileId }))
  }
}

export function mapMemoryDigestView(dto: MemoryDigestDTO): MemoryDigestViewModel {
  return {
    id: dto.digest_id,
    agentId: dto.agent_id,
    missionId: dto.mission_id ?? '',
    taskId: dto.task_id ?? '',
    scopeLabel: formatScope(dto.scope),
    sourceMemoryRefs: dto.source_memory_refs ?? [],
    sourceEvidenceRefs: dto.source_evidence_refs ?? [],
    summary: fallback(dto.summary, 'Not provided'),
    facts: dto.facts ?? [],
    preferences: dto.preferences ?? [],
    warnings: dto.warnings ?? [],
    decisions: dto.decisions ?? [],
    confidenceLabel: `${Math.round((dto.confidence ?? 0) * 100)}%`,
    visibilityLabel: formatVisibility(dto.visibility),
    createdAt: formatDate(dto.created_at),
    expiresAt: formatDate(dto.expires_at)
  }
}

export function mapResourceBindingCommand(form: ResourceBindingFormViewModel): ResourceBindingCommand {
  return {
    agent_id: form.agentId.trim(),
    resource_id: form.resourceId.trim(),
    resource_type: form.resourceType.trim(),
    alias: form.alias.trim() || undefined,
    permission_scope: form.scope.trim() || undefined,
    agent_ak: optionalText(form.agentAK ?? ''),
    auth_header_name: optionalText(form.authHeaderName ?? '')
  }
}

export function mapMcpServerCommand(form: ResourceMcpServerCommandFormViewModel): RegisterMCPServerRequest {
  const toolId = optionalText(form.toolId)
  return {
    ...(toolId ? { tool_id: toolId } : {}),
    name: form.name.trim(),
    description: optionalText(form.description),
    version: form.version.trim() || '1.0.0',
    server_name: form.serverName.trim(),
    transport: form.transport,
    command: optionalText(form.command),
    url: optionalText(form.url),
    headers: withDefaultAKHeader(parseHeaderJSON(form.headers), form.authHeaderName, form.defaultAK),
    auth_header_name: optionalText(form.authHeaderName ?? ''),
    args: csv(form.args),
    exposed_tools: csv(form.exposedTools),
    tags: csv(form.tags)
  }
}

export function mapMcpServerPatchCommand(form: ResourceMcpServerCommandFormViewModel): PatchMCPServerRequest {
  const transport = form.transport.trim()
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    version: form.version.trim() || '1.0.0',
    server_name: form.serverName.trim(),
    transport,
    command: transport === 'stdio' ? form.command.trim() : '',
    url: transport === 'stdio' ? '' : form.url.trim(),
    headers: withDefaultAKHeader(parseHeaderJSON(form.headers), form.authHeaderName, form.defaultAK) ?? {},
    auth_header_name: (form.authHeaderName ?? '').trim(),
    args: csv(form.args),
    exposed_tools: csv(form.exposedTools),
    tags: csv(form.tags)
  }
}

export function mapMcpConfigCommands(configJson: string): RegisterMCPServerRequest[] {
  const raw = configJson.trim()
  if (!raw) {
    throw new Error('MCP config JSON is required')
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    throw new Error('MCP config JSON is invalid')
  }
  if (!isRecord(parsed) || Array.isArray(parsed)) {
    throw new Error('MCP config JSON must be an object')
  }

  const servers = mcpConfigServers(parsed)
  if (servers.length === 0) {
    throw new Error('MCP config JSON must define at least one MCP server')
  }
  return servers.map(([serverName, server]) => mapMcpConfigServerCommand(serverName, server))
}

function parseHeaderJSON(value: string | undefined) {
  if (value === undefined) return undefined
  const raw = value.trim()
  if (!raw) return {}
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    throw new Error('MCP headers must be a JSON object with string values')
  }
  if (!isRecord(parsed) || Array.isArray(parsed)) {
    throw new Error('MCP headers must be a JSON object with string values')
  }
  const headers: Record<string, string> = {}
  for (const [name, headerValue] of Object.entries(parsed)) {
    if (!name.trim()) continue
    if (typeof headerValue !== 'string') {
      throw new Error('MCP headers must be a JSON object with string values')
    }
    const trimmed = headerValue.trim()
    if (!trimmed) continue
    headers[name.trim()] = trimmed
  }
  return Object.keys(headers).length ? headers : undefined
}

function mcpConfigServers(config: Record<string, unknown>): Array<[string, Record<string, unknown>]> {
  const servers = new Map<string, Record<string, unknown>>()
  collectMcpConfigServers(servers, config.mcpServers)
  collectMcpConfigServers(servers, config.servers)
  if (servers.size === 0 && isSingleMcpConfigServer(config)) {
    const name = firstConfigText(configStringField(config, 'name'), inferMcpConfigServerName(config), 'server')
    servers.set(name, config)
  }
  return Array.from(servers.entries()).sort(([left], [right]) => left.localeCompare(right))
}

function collectMcpConfigServers(servers: Map<string, Record<string, unknown>>, value: unknown) {
  if (!isRecord(value) || Array.isArray(value)) return
  for (const [name, server] of Object.entries(value)) {
    if (!servers.has(name) && isRecord(server) && !Array.isArray(server)) {
      servers.set(name, server)
    }
  }
}

function mapMcpConfigServerCommand(serverKey: string, server: Record<string, unknown>): RegisterMCPServerRequest {
  const serverName = firstConfigText(
    serverKey,
    configStringField(server, 'server_name'),
    configStringField(server, 'serverName'),
    configStringField(server, 'name'),
    inferMcpConfigServerName(server)
  )
  if (!serverName) {
    throw new Error('MCP config server name is required')
  }
  const transport = inferMcpConfigTransport(server)
  const command = configStringField(server, 'command')
  const url = firstConfigText(configStringField(server, 'url'), configStringField(server, 'endpoint'))
  if (transport === 'stdio' && !command) {
    throw new Error(`stdio MCP server "${serverName}" requires command`)
  }
  if (transport !== 'stdio' && !url) {
    throw new Error(`${transport} MCP server "${serverName}" requires url`)
  }

  const headers = mcpConfigHeaders(server.headers)
  return {
    name: firstConfigText(configStringField(server, 'name'), serverName),
    description: configStringField(server, 'description') || undefined,
    version: firstConfigText(configStringField(server, 'version'), '1.0.0'),
    server_name: serverName,
    transport,
    command: command || undefined,
    url: url || undefined,
    headers,
    auth_header_name: authHeaderNameFromHeaders(headers),
    args: mcpConfigStringArray(server.args),
    env_secret_refs: mcpConfigEnvSecrets(server.env, serverName),
    exposed_tools: mcpConfigStringArray(server.tools),
    tags: uniqueStrings(['mcp', 'json-import', ...(mcpConfigStringArray(server.tags) ?? [])])
  }
}

function inferMcpConfigTransport(server: Record<string, unknown>) {
  const raw = firstConfigText(configStringField(server, 'transport'), configStringField(server, 'type')).toLowerCase().replaceAll('-', '_')
  switch (raw) {
    case '':
      return firstConfigText(configStringField(server, 'url'), configStringField(server, 'endpoint')) ? 'streamable_http' : 'stdio'
    case 'stdio':
      return 'stdio'
    case 'sse':
      return 'sse'
    case 'http':
    case 'stream_http':
    case 'streamablehttp':
    case 'streamable_http':
      return 'streamable_http'
    default:
      throw new Error(`unsupported MCP transport "${raw}"`)
  }
}

function mcpConfigHeaders(value: unknown) {
  if (!isRecord(value) || Array.isArray(value)) return undefined
  const headers: Record<string, string> = {}
  for (const [name, headerValue] of Object.entries(value)) {
    if (typeof headerValue !== 'string') {
      throw new Error('MCP config headers must be string values')
    }
    const cleanName = name.trim()
    const cleanValue = headerValue.trim()
    if (cleanName && cleanValue) {
      headers[cleanName] = cleanValue
    }
  }
  return Object.keys(headers).length ? headers : undefined
}

function mcpConfigEnvSecrets(value: unknown, serverName: string) {
  if (!isRecord(value) || Array.isArray(value)) return undefined
  const refs = Object.keys(value)
    .map((envName) => envName.trim())
    .filter(Boolean)
    .sort()
    .map((envName) => ({
      name: `mcp.${safeMcpId(serverName)}.${envName.toLowerCase()}`,
      env: envName
    }))
  return refs.length ? refs : undefined
}

function mcpConfigStringArray(value: unknown) {
  if (Array.isArray(value)) {
    return value.filter((item): item is string => typeof item === 'string').map((item) => item.trim()).filter(Boolean)
  }
  if (typeof value === 'string') {
    return csv(value)
  }
  return undefined
}

function authHeaderNameFromHeaders(headers: Record<string, string> | undefined) {
  if (!headers) return undefined
  return Object.keys(headers).find((name) => name.toLowerCase() === 'authorization') ?? Object.keys(headers).find((name) => /(^|[-_])(api[-_]?key|token|secret)$/i.test(name))
}

function isSingleMcpConfigServer(server: Record<string, unknown>) {
  return Boolean(firstConfigText(configStringField(server, 'command'), configStringField(server, 'url'), configStringField(server, 'endpoint')))
}

function inferMcpConfigServerName(server: Record<string, unknown>) {
  const command = configStringField(server, 'command')
  if (!command) return 'server'
  return command.split(/\s+/)[0]?.replace(/\.exe$/i, '') ?? 'server'
}

function configStringField(value: Record<string, unknown>, key: string) {
  const field = value[key]
  return typeof field === 'string' ? field.trim() : ''
}

function firstConfigText(...values: string[]) {
  return values.find((value) => value.trim().length > 0)?.trim() ?? ''
}

function uniqueStrings(values: Array<string | undefined>) {
  const seen = new Set<string>()
  const out: string[] = []
  for (const value of values) {
    const clean = value?.trim()
    if (!clean || seen.has(clean)) continue
    seen.add(clean)
    out.push(clean)
  }
  return out
}

function safeMcpId(value: string) {
  const clean = value.replace(/[^a-zA-Z0-9_.-]+/g, '-').replace(/^-+|-+$/g, '')
  return (clean || 'server').toLowerCase()
}

function withDefaultAKHeader(headers: Record<string, string> | undefined, authHeaderName: string | undefined, defaultAK: string | undefined) {
  const credential = optionalText(defaultAK ?? '')
  if (!credential) return headers
  const headerName = optionalText(authHeaderName ?? '') ?? 'Authorization'
  return {
    ...(headers ?? {}),
    [headerName]: credential
  }
}

export function mapToolCommand(form: ResourceToolCommandFormViewModel): ToolDefinitionRequest {
  const toolId = optionalText(form.toolId)
  const kind = optionalText(form.kind) ?? 'internal_service_tool'
  const enabled = optionalText(form.enabled)
  return {
    ...(toolId ? { tool_id: toolId } : {}),
    name: form.name.trim(),
    description: optionalText(form.description),
    version: form.version.trim() || '1.0.0',
    kind,
    risk_level: optionalText(form.riskLevel) ?? 'medium',
    enabled: enabled === undefined ? true : enabled === 'true',
    command: kind === 'cli_tool' ? optionalText(form.command) : undefined,
    url: kind === 'api_tool' ? optionalText(form.url) : undefined,
    tags: csv(form.tags)
  }
}

export function mapToolPatchCommand(form: ResourceToolCommandFormViewModel): PatchToolDefinitionRequest {
  const kind = optionalText(form.kind) ?? 'internal_service_tool'
  const enabled = optionalText(form.enabled)
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    version: form.version.trim() || '1.0.0',
    kind,
    risk_level: optionalText(form.riskLevel) ?? 'medium',
    enabled: enabled === undefined ? true : enabled === 'true',
    command: kind === 'cli_tool' ? form.command.trim() : undefined,
    url: kind === 'api_tool' ? form.url.trim() : undefined,
    tags: csv(form.tags)
  }
}

function mcpStatus(availability: string, enabled: boolean): { label: string; tone: BadgeTone } {
  switch (availability) {
    case 'requires_auth':
      return { label: 'Requires auth', tone: 'warning' }
    case 'unavailable':
      return { label: 'Unavailable', tone: 'danger' }
    case 'available':
      return { label: 'Available', tone: 'success' }
    default:
      return enabled ? { label: 'Enabled', tone: 'success' } : { label: 'Disabled', tone: 'danger' }
  }
}

function mapMcpProbe(probe: {
  status?: string
  message?: string
  failure_reason?: string
  http_status?: number
  latency_ms?: number
  checked_at?: string
} | undefined) {
  if (!probe) return undefined
  return {
    status: probe.status ?? '',
    message: probe.message ?? '',
    failureReason: probe.failure_reason ?? '',
    httpStatus: probe.http_status ?? 0,
    latencyMs: probe.latency_ms ?? 0,
    checkedAt: formatDate(probe.checked_at)
  }
}

function mapTool(
  tool: {
    tool_id: string
    name?: string
    description?: string
    kind?: string
    version?: string
    input_schema?: Record<string, unknown>
    output_schema?: Record<string, unknown>
    enabled?: boolean
    risk_level?: string
    tags?: string[]
    permission_declarations?: unknown[]
    secret_refs?: unknown[]
    source?: string
    server_tool_id?: string
    downstream_tool_name?: string
    manageable?: boolean
    command?: string
    url?: string
    updated_at?: string
    created_at?: string
  },
  usage?: ResourceUsageDTO,
  bindings: ResourceBindingDTO[] = []
): ToolViewModel {
  const enabled = tool.enabled !== false
  const riskLevel = fallback(tool.risk_level, 'medium').toLowerCase()
  const bindingViews = bindings.map(mapBinding)

  return {
    id: tool.tool_id,
    name: fallback(tool.name, tool.tool_id),
    description: fallback(tool.description, 'Not provided'),
    kind: fallback(tool.kind, 'tool'),
    category: firstValue(tool.tags, 'Not provided'),
    version: fallback(tool.version, 'Not provided'),
    inputSchema: tool.input_schema,
    outputSchema: tool.output_schema,
    enabled,
    riskLevel,
    riskTone: riskTone(riskLevel),
    statusLabel: enabled ? 'Enabled' : 'Disabled',
    statusTone: enabled ? 'success' : 'danger',
    source: fallback(tool.source, 'Not provided'),
    serverToolId: tool.server_tool_id ?? '',
    downstreamToolName: tool.downstream_tool_name ?? '',
    manageable: tool.manageable !== false,
    command: tool.command ?? '',
    url: tool.url ?? '',
    tags: tool.tags ?? [],
    permissions: permissionScopes(tool.permission_declarations),
    secretCount: tool.secret_refs?.length ?? 0,
    usageCount: usage?.usage_count ?? 0,
    boundAgentCount: bindingViews.length,
    boundAgents: bindingViews.map((binding) => binding.label),
    bindings: bindingViews,
    updatedAt: formatDate(tool.updated_at ?? tool.created_at)
  }
}

function permissionScopes(values: unknown[] | undefined) {
  return (values ?? [])
    .map((value) => {
      if (typeof value === 'string') return value
      if (isRecord(value)) return stringField(value, 'scope')
      return ''
    })
    .filter(Boolean)
}

function mapBinding(binding: ResourceBindingDTO): ResourceBindingViewModel {
  return {
    agentId: fallback(binding.bound_to_id, 'Not provided'),
    label: fallback(binding.bound_to_display_name, binding.bound_to_id),
    alias: fallback(binding.alias, 'Not provided'),
    scope: fallback(binding.permission_scope, binding.binding_policy),
    mcpAuthRequired: binding.mcp_auth_required === true,
    defaultAKConfigured: binding.default_ak_configured === true,
    agentAKConfigured: binding.agent_ak_configured === true,
    agentAKMasked: binding.agent_ak_masked ?? '',
    authHeaderName: binding.auth_header_name ?? ''
  }
}

function mapMemoryProfile(profile: MemoryProfileDTO, bindings: ResourceBindingDTO[]): MemoryProfileViewModel {
  const enabled = profile.enabled !== false
  const bindingViews = bindings.map(mapBinding)

  return {
    id: profile.memory_profile_id,
    ownerAgentId: fallback(profile.owner_agent_id, 'Not provided'),
    organizationId: fallback(profile.organization_id, 'Not provided'),
    departmentId: fallback(profile.department_id, 'Not provided'),
    projectId: fallback(profile.project_id, 'Not provided'),
    memoryKind: fallback(profile.memory_kind, 'Not provided'),
    enabled,
    statusLabel: enabled ? 'Enabled' : 'Disabled',
    statusTone: enabled ? 'success' : 'danger',
    defaultScopeKind: profile.default_scope?.kind ?? '',
    defaultScopeId: profile.default_scope?.id ?? '',
    defaultScopeLabel: formatScope(profile.default_scope),
    visibilityLabel: formatVisibility(profile.visibility_policy),
    retrievalTags: profile.retrieval_policy?.tags ?? [],
    maxRecords: profile.retrieval_policy?.max_records ?? 0,
    maxSummaryBytes: profile.compression_policy?.max_summary_bytes ?? 0,
    recordCount: profile.record_count ?? 0,
    candidateCount: profile.candidate_count ?? 0,
    boundAgents: bindingViews.map((binding) => binding.label)
  }
}

function summarizeResources(
  resources: Array<{ enabled: boolean; riskLevel?: string; usageCount?: number; boundAgentCount?: number }>
): ResourceSummaryViewModel {
  return resources.reduce<ResourceSummaryViewModel>(
    (summary, resource) => {
      summary.total += 1
      summary.enabled += resource.enabled ? 1 : 0
      summary.disabled += resource.enabled ? 0 : 1
      summary.highRisk += resource.riskLevel === 'high' ? 1 : 0
      summary.bound += resource.boundAgentCount && resource.boundAgentCount > 0 ? 1 : 0
      summary.usageCount += resource.usageCount ?? 0
      return summary
    },
    { total: 0, enabled: 0, disabled: 0, highRisk: 0, bound: 0, usageCount: 0 }
  )
}

function usageFor(usage: ResourceUsageDTO[] | undefined, resourceId: string) {
  return usage?.find((entry) => entry.resource_id === resourceId)
}

function bindingsFor(bindings: ResourceBindingDTO[] | undefined, resourceId: string) {
  return bindings?.filter((entry) => entry.resource_id === resourceId) ?? []
}

function riskTone(riskLevel: string): BadgeTone {
  if (riskLevel === 'high') return 'danger'
  if (riskLevel === 'medium') return 'warning'
  if (riskLevel === 'low') return 'success'
  return 'neutral'
}

function firstValue(values: string[] | undefined, fallbackValue: string) {
  return values?.find(Boolean) ?? fallbackValue
}

function csv(value: string) {
  return value.split(',').map((item) => item.trim()).filter(Boolean)
}

function toolRefIds(values: unknown[] | undefined) {
  return (values ?? []).map((value) => {
    if (typeof value === 'string') return value
    if (isRecord(value)) return stringField(value, 'tool_id')
    return ''
  }).filter(Boolean)
}

function promptAssetPaths(values: unknown[] | undefined) {
  return (values ?? []).map((value) => {
    if (typeof value === 'string') return value
    if (isRecord(value)) return stringField(value, 'path')
    return ''
  }).filter(Boolean)
}

function evidenceRefIds(values: unknown[] | undefined) {
  return (values ?? []).map((value) => {
    if (typeof value === 'string') return value
    if (isRecord(value)) return stringField(value, 'id')
    return ''
  }).filter(Boolean)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function stringField(value: Record<string, unknown>, key: string) {
  const field = value[key]
  return typeof field === 'string' ? field : ''
}

function optionalText(value: string) {
  const trimmed = value.trim()
  return trimmed.length > 0 ? trimmed : undefined
}

function formatScope(value: { kind?: string; id?: string } | undefined) {
  if (!value) return 'Not provided'
  const kind = fallback(value.kind, 'Not provided')
  const id = fallback(value.id, 'Not provided')
  if (kind === 'Not provided' && id === 'Not provided') return 'Not provided'
  return `${kind}:${id}`
}

function formatVisibility(value: { mode?: string; scope_kind?: string; scope_id?: string } | undefined) {
  if (!value) return 'Not provided'
  const mode = fallback(value.mode, 'Not provided')
  if (!value.scope_kind && !value.scope_id) return mode
  return `${mode} (${formatScope({ kind: value.scope_kind, id: value.scope_id })})`
}

function formatHealthPolicy(value: string | { mode?: string; timeout?: string } | undefined) {
  if (!value) return 'Not provided'
  if (typeof value === 'string') return fallback(value, 'Not provided')
  const mode = fallback(value.mode, 'Not provided')
  if (!value.timeout || value.timeout.trim().length === 0) return mode
  return `${mode} (${value.timeout})`
}

function fallback(value: string | undefined, fallbackValue = 'Not provided') {
  return value && value.trim().length > 0 ? value : fallbackValue
}

function formatDate(value: string | undefined) {
  if (!value) return 'Not provided'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  if (parsed.getUTCFullYear() <= 1) return 'Not provided'
  return new Intl.DateTimeFormat('en', { month: 'short', day: '2-digit', year: 'numeric' }).format(parsed)
}
