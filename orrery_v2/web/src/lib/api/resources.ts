import { apiFetch, type ApiFetchOptions } from './client'
import type {
  AddSkillsToBundleRequest,
  CreateSkillBundleRequest,
  DiscoverMCPResponseDTO,
  CreateSkillRequest,
  MCPServerDTO,
  MCPServerHealthDTO,
  MCPServerSyncDTO,
  SaveSkillFileRequest,
  SaveSkillMarkdownRequest,
  RegisterMCPServerRequest,
  PatchMCPServerRequest,
  PatchToolDefinitionRequest,
  ToolCallRequest,
  ToolCallResultDTO,
  ToolDefinitionRequest,
  SkillDetailDTO,
  SkillBundleDTO,
  SkillBundleListDTO,
  SkillDTO,
  SkillFileDTO
} from '#lib/types/api'
import type { BuildMemoryDigestRequest, MemoryDigestDTO, MemoryProfileDTO, MemoryResourceAdminViewDTO, UpdateMemoryProfileRequest } from '#lib/types/resource-memory'

export const resourceApi = {
  listTools: (query = '', options?: ApiFetchOptions) => apiFetch(`/api/resources/tools${query}`, options),
  registerTool: (body: ToolDefinitionRequest, options?: ApiFetchOptions) =>
    apiFetch('/api/resources/tools', jsonMutation('POST', body, options)),
  updateTool: (toolId: string, body: PatchToolDefinitionRequest, options?: ApiFetchOptions) =>
    apiFetch(`/api/resources/tools/${toolId}`, jsonMutation('PATCH', body, options)),
  disableTool: (toolId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/resources/tools/${toolId}/disable`, { ...options, method: 'POST' }),
  deleteTool: (toolId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/resources/tools/${toolId}`, { ...options, method: 'DELETE' }),
  callTool: (body: ToolCallRequest, options?: ApiFetchOptions) =>
    apiFetch<ToolCallResultDTO>('/api/resources/tools/call', jsonMutation('POST', {
      tool_id: body.tool_id,
      arguments: body.arguments ?? {}
    }, options)),
  listSkills: (query = '', options?: ApiFetchOptions) => apiFetch(`/api/resources/skills${query}`, options),
  createSkill: (body: CreateSkillRequest, options?: ApiFetchOptions) =>
    apiFetch<SkillDetailDTO>('/api/resources/skills', jsonMutation('POST', body, options)),
  getSkillDetail: (skillId: string, options?: ApiFetchOptions) =>
    apiFetch<SkillDetailDTO>(`/api/resources/skills/${encodeURIComponent(skillId)}`, options),
  saveSkillMarkdown: (skillId: string, body: SaveSkillMarkdownRequest, options?: ApiFetchOptions) =>
    apiFetch<SkillDetailDTO>(
      `/api/resources/skills/${encodeURIComponent(skillId)}/skill-md`,
      jsonMutation('PUT', body, options)
    ),
  getSkillFile: (skillId: string, path: string, options?: ApiFetchOptions) =>
    apiFetch<SkillFileDTO>(`/api/resources/skills/${encodeURIComponent(skillId)}/files${resourceFileQuery(path)}`, options),
  saveSkillFile: (skillId: string, path: string, body: SaveSkillFileRequest, options?: ApiFetchOptions) =>
    apiFetch<SkillFileDTO>(
      `/api/resources/skills/${encodeURIComponent(skillId)}/files${resourceFileQuery(path)}`,
      jsonMutation('PUT', body, options)
    ),
  uploadSkillFile: (skillId: string, path: string, body: FormData, options?: ApiFetchOptions) =>
    apiFetch<SkillFileDTO>(
      `/api/resources/skills/${encodeURIComponent(skillId)}/files${resourceFileQuery(path)}`,
      { ...options, method: 'POST', body }
    ),
  downloadSkillFile: (skillId: string, path: string, options?: ApiFetchOptions) =>
    apiFetch<Blob>(
      `/api/resources/skills/${encodeURIComponent(skillId)}/files/download${resourceFileQuery(path)}`,
      { ...options, responseType: 'blob' }
    ),
  deleteSkillFile: (skillId: string, path: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/resources/skills/${encodeURIComponent(skillId)}/files${resourceFileQuery(path)}`, { ...options, method: 'DELETE' }),
  createSkillFolder: (skillId: string, path: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/resources/skills/${encodeURIComponent(skillId)}/folders`, jsonMutation('POST', { path }, options)),
  importSkill: (body: FormData, options?: ApiFetchOptions) =>
    apiFetch<SkillDTO>('/api/resources/skills/import', { ...options, method: 'POST', body }),
  exportSkill: (skillId: string, options?: ApiFetchOptions) =>
    apiFetch<Blob>(`/api/resources/skills/${skillId}/export`, { ...options, responseType: 'blob' }),
  disableSkill: (skillId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/resources/skills/${skillId}/disable`, { ...options, method: 'POST' }),
  deleteSkill: (skillId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/resources/skills/${skillId}`, { ...options, method: 'DELETE' }),
  listSkillBundles: (options?: ApiFetchOptions) =>
    apiFetch<SkillBundleListDTO>('/api/resources/skill-bundles', options),
  createSkillBundle: (body: CreateSkillBundleRequest, options?: ApiFetchOptions) =>
    apiFetch<SkillBundleDTO>('/api/resources/skill-bundles', jsonMutation('POST', body, options)),
  addSkillsToBundle: (bundleId: string, body: AddSkillsToBundleRequest, options?: ApiFetchOptions) =>
    apiFetch<SkillBundleDTO>(
      `/api/resources/skill-bundles/${encodeURIComponent(bundleId)}/skills`,
      jsonMutation('POST', body, options)
    ),
  importSkillIntoBundle: (bundleId: string, body: FormData, options?: ApiFetchOptions) =>
    apiFetch<SkillBundleDTO>(
      `/api/resources/skill-bundles/${encodeURIComponent(bundleId)}/skills/import`,
      { ...options, method: 'POST', body }
    ),
  removeSkillFromBundle: (bundleId: string, skillId: string, options?: ApiFetchOptions) =>
    apiFetch<SkillBundleDTO>(
      `/api/resources/skill-bundles/${encodeURIComponent(bundleId)}/skills/${encodeURIComponent(skillId)}`,
      { ...options, method: 'DELETE' }
    ),
  deleteSkillBundle: (bundleId: string, deleteSkills = false, options?: ApiFetchOptions) => {
    const query = new URLSearchParams({ delete_skills: String(deleteSkills) })
    return apiFetch(
      `/api/resources/skill-bundles/${encodeURIComponent(bundleId)}?${query.toString()}`,
      { ...options, method: 'DELETE' }
    )
  },
  listMcpServers: (query = '', options?: ApiFetchOptions) => apiFetch(`/api/resources/mcp-servers${query}`, options),
  getMcpServer: (toolId: string, options?: ApiFetchOptions) =>
    apiFetch<MCPServerDTO>(`/api/resources/mcp-servers/${encodeURIComponent(toolId)}`, options),
  registerMcpServer: (body: RegisterMCPServerRequest, options?: ApiFetchOptions) =>
    apiFetch('/api/resources/mcp-servers', jsonMutation('POST', body, options)),
  updateMcpServer: (toolId: string, body: PatchMCPServerRequest, options?: ApiFetchOptions) =>
    apiFetch(`/api/resources/mcp-servers/${toolId}`, jsonMutation('PATCH', body, options)),
  disableMcpServer: (toolId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/resources/mcp-servers/${toolId}/disable`, { ...options, method: 'POST' }),
  deleteMcpServer: (toolId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/resources/mcp-servers/${toolId}`, { ...options, method: 'DELETE' }),
  syncMcpServer: (toolId: string, options?: ApiFetchOptions) =>
    apiFetch<MCPServerSyncDTO>(`/api/resources/mcp-servers/${toolId}/sync`, { ...options, method: 'POST' }),
  getMcpServerHealth: (toolId: string, options?: ApiFetchOptions) =>
    apiFetch<MCPServerHealthDTO>(`/api/resources/mcp-servers/${toolId}/health`, options),
  discoverMcpServers: (query = '', options?: ApiFetchOptions) =>
    apiFetch<DiscoverMCPResponseDTO>(`/api/resources/mcp-servers/discover${query}`, options),
  listMemoryProfiles: (query = '', options?: ApiFetchOptions) =>
    apiFetch<MemoryResourceAdminViewDTO>(`/api/resources/memory-profiles${query}`, options),
  getMemoryProfile: (profileId: string, options?: ApiFetchOptions) =>
    apiFetch<MemoryProfileDTO>(`/api/resources/memory-profiles/${encodeURIComponent(profileId)}`, options),
  updateMemoryProfile: (profileId: string, body: UpdateMemoryProfileRequest, options?: ApiFetchOptions) =>
    apiFetch<MemoryProfileDTO>(`/api/resources/memory-profiles/${encodeURIComponent(profileId)}`, jsonMutation('PATCH', body, options)),
  getPersonalMemoryProfileByAgent: (agentId: string, options?: ApiFetchOptions) => {
    const params = new URLSearchParams({ agent_id: agentId })
    return apiFetch<MemoryProfileDTO>(`/api/resources/memory-profiles/personal?${params.toString()}`, options)
  },
  buildMemoryDigest: (body: BuildMemoryDigestRequest, options?: ApiFetchOptions) =>
    apiFetch<MemoryDigestDTO>('/api/resources/memory-profiles/digest', jsonMutation('POST', body, options)),
  bindResource: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch('/api/resources/bindings', jsonMutation('POST', body, options)),
  unbindResource: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch('/api/resources/bindings', jsonMutation('DELETE', body, options))
}

function jsonMutation(method: string, body: unknown, options: ApiFetchOptions = {}): ApiFetchOptions {
  return { ...options, method, body: JSON.stringify(body) }
}

function resourceFileQuery(path: string) {
  return `?${new URLSearchParams({ path }).toString()}`
}
