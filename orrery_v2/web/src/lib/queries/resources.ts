import { createInfiniteQuery, createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { resourceApi } from '#lib/api/resources'
import { skillCreatePayload } from '#lib/skillMarkdown'
import {
  mapMemoryDigestCommand,
  mapMemoryDigestView,
  mapMemoryProfileListView,
  mapMcpConfigCommands,
  mapMcpDiscoveryView,
  mapMcpServerCommand,
  mapMcpServerListView,
  mapMcpServerPatchCommand,
  mapResourceBindingCommand,
  mapToolCommand,
  mapToolPatchCommand,
  mapSkillBundleListView,
  mapSkillListView,
  mapToolGatewayView
} from '#lib/mappers/resource'
import type { DiscoverMCPResponseDTO, ResourceAdminViewDTO, SkillBundleListDTO, ToolCallRequest } from '#lib/types/api'
import type {
  ResourceBindingFormViewModel,
  ResourceMcpServerCommandFormViewModel,
  ResourceSkillBundleCommandFormViewModel,
  ResourceSkillCommandFormViewModel,
  ResourceToolCommandFormViewModel
} from '#lib/types/view-models'
import type {
  MemoryDigestFormViewModel,
  MemoryDigestViewModel,
  MemoryResourceAdminViewDTO
} from '#lib/types/resource-memory'

export interface ResourceFilters {
  search?: string
  category?: string
  riskLevel?: string
  enabled?: boolean | string
  resourceId?: string
  boundAgentId?: string
  requiredToolId?: string
  includeUsage?: boolean
  includeBindings?: boolean
  limit?: number
  offset?: number
}

export const DEFAULT_RESOURCE_PAGE_SIZE = 20
export const RESOURCE_CATALOG_PAGE_SIZE = 1000

export interface McpDiscoveryFilters {
  query?: string
  limit?: number
}

export const resourceQueryKeys = {
  all: ['resources'] as const,
  tools: (filters: ResourceFilters = {}) => ['resources', 'tools', buildResourceQuery(filters)] as const,
  skills: (filters: ResourceFilters = {}) => ['resources', 'skills', buildResourceQuery(filters)] as const,
  skillDetail: (skillId: string) => ['resources', 'skills', 'detail', skillId] as const,
  skillBundles: () => ['resources', 'skill-bundles'] as const,
  mcpServers: (filters: ResourceFilters = {}) => ['resources', 'mcp-servers', buildResourceQuery(filters)] as const,
  mcpServer: (toolId: string) => ['resources', 'mcp-server', toolId] as const,
  mcpDiscovery: (filters: McpDiscoveryFilters = {}) => ['resources', 'mcp-discovery', buildMcpDiscoveryQuery(filters)] as const,
  memoryProfiles: (filters: ResourceFilters = {}) => ['resources', 'memory-profiles', buildResourceQuery(filters)] as const,
  memoryProfile: (profileId: string) => ['resources', 'memory-profile', profileId] as const,
  personalMemoryProfile: (agentId: string) => ['resources', 'memory-profile', 'personal', agentId] as const,
}

export function useResourceToolsQuery(filters: () => ResourceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = resourceListFilters(filters())
    const query = buildResourceQuery(current)
    return {
      queryKey: resourceQueryKeys.tools(current),
      queryFn: async () => mapToolGatewayView((await resourceApi.listTools(query)) as ResourceAdminViewDTO),
      enabled: enabled()
    }
  })
}

export function useResourceSkillsQuery(filters: () => ResourceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = resourceListFilters(filters())
    const query = buildResourceQuery(current)
    return {
      queryKey: resourceQueryKeys.skills(current),
      queryFn: async () => mapSkillListView((await resourceApi.listSkills(query)) as ResourceAdminViewDTO),
      enabled: enabled()
    }
  })
}

export function useSkillDetailQuery(skillId: () => string) {
  return createQuery(() => {
    const id = skillId().trim()
    return {
      queryKey: resourceQueryKeys.skillDetail(id),
      queryFn: () => resourceApi.getSkillDetail(id),
      enabled: id.length > 0
    }
  })
}

export function useSkillBundlesQuery(enabled: () => boolean = () => true) {
  return createQuery(() => ({
    queryKey: resourceQueryKeys.skillBundles(),
    queryFn: async () => mapSkillBundleListView((await resourceApi.listSkillBundles()) as SkillBundleListDTO),
    enabled: enabled()
  }))
}

export function useMcpServersQuery(filters: () => ResourceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = resourceListFilters(filters())
    const query = buildResourceQuery(current)
    return {
      queryKey: resourceQueryKeys.mcpServers(current),
      queryFn: async () => mapMcpServerListView((await resourceApi.listMcpServers(query)) as ResourceAdminViewDTO),
      enabled: enabled()
    }
  })
}

export function useResourceToolsInfiniteQuery(filters: () => ResourceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createInfiniteQuery(() => {
    const current = resourceListFilters(filters(), { offset: 0 })
    return {
      queryKey: [...resourceQueryKeys.tools(current), 'infinite'] as const,
      initialPageParam: 0,
      queryFn: async ({ pageParam, signal }) =>
        (await resourceApi.listTools(buildResourceQuery(resourceListFilters(filters(), { offset: Number(pageParam) || 0 })), { signal })) as ResourceAdminViewDTO,
      getNextPageParam: nextResourcePageOffset,
      enabled: enabled()
    }
  })
}

export function useResourceSkillsInfiniteQuery(filters: () => ResourceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createInfiniteQuery(() => {
    const current = resourceListFilters(filters(), { offset: 0 })
    return {
      queryKey: [...resourceQueryKeys.skills(current), 'infinite'] as const,
      initialPageParam: 0,
      queryFn: async ({ pageParam, signal }) =>
        (await resourceApi.listSkills(buildResourceQuery(resourceListFilters(filters(), { offset: Number(pageParam) || 0 })), { signal })) as ResourceAdminViewDTO,
      getNextPageParam: nextResourcePageOffset,
      enabled: enabled()
    }
  })
}

export function useMcpServersInfiniteQuery(filters: () => ResourceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createInfiniteQuery(() => {
    const current = resourceListFilters(filters(), { offset: 0 })
    return {
      queryKey: [...resourceQueryKeys.mcpServers(current), 'infinite'] as const,
      initialPageParam: 0,
      queryFn: async ({ pageParam, signal }) =>
        (await resourceApi.listMcpServers(buildResourceQuery(resourceListFilters(filters(), { offset: Number(pageParam) || 0 })), { signal })) as ResourceAdminViewDTO,
      getNextPageParam: nextResourcePageOffset,
      enabled: enabled()
    }
  })
}

export function useMcpServerQuery(toolId: () => string) {
  return createQuery(() => {
    const id = toolId().trim()
    return {
      queryKey: resourceQueryKeys.mcpServer(id),
      queryFn: async () => mapMcpServerListView({ mcp_servers: [await resourceApi.getMcpServer(id)] }).servers[0],
      enabled: id.length > 0
    }
  })
}

export function useMcpDiscoveryQuery(filters: () => McpDiscoveryFilters = () => ({})) {
  return createQuery(() => {
    const current = filters()
    const query = buildMcpDiscoveryQuery(current)
    return {
      queryKey: resourceQueryKeys.mcpDiscovery(current),
      queryFn: async () => mapMcpDiscoveryView((await resourceApi.discoverMcpServers(query)) as DiscoverMCPResponseDTO),
      enabled: (current.query ?? '').trim().length > 0
    }
  })
}

export function useMemoryProfilesQuery(filters: () => ResourceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = resourceListFilters(filters())
    const query = buildResourceQuery(current)
    return {
      queryKey: resourceQueryKeys.memoryProfiles(current),
      queryFn: async () => mapMemoryProfileListView(await resourceApi.listMemoryProfiles(query)),
      enabled: enabled()
    }
  })
}

export function useMemoryProfilesInfiniteQuery(filters: () => ResourceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createInfiniteQuery(() => {
    const current = resourceListFilters(filters(), { offset: 0 })
    return {
      queryKey: [...resourceQueryKeys.memoryProfiles(current), 'infinite'] as const,
      initialPageParam: 0,
      queryFn: async ({ pageParam }) =>
        (await resourceApi.listMemoryProfiles(buildResourceQuery(resourceListFilters(filters(), { offset: Number(pageParam) || 0 })))) as MemoryResourceAdminViewDTO,
      getNextPageParam: nextResourcePageOffset,
      enabled: enabled()
    }
  })
}

export function useMemoryProfileQuery(profileId: () => string) {
  return createQuery(() => {
    const id = profileId().trim()
    return {
      queryKey: resourceQueryKeys.memoryProfile(id),
      queryFn: async () => mapMemoryProfileListView({ memory_profiles: [await resourceApi.getMemoryProfile(id)] }).profiles[0],
      enabled: id.length > 0
    }
  })
}

export function useMemoryProfileUpdateMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ profileId, enabled, maxRecords, maxSummaryBytes, retrievalTags }: {
      profileId: string
      enabled: boolean
      maxRecords: number
      maxSummaryBytes: number
      retrievalTags: string[]
    }) => resourceApi.updateMemoryProfile(profileId, {
      enabled,
      max_records: maxRecords,
      max_summary_bytes: maxSummaryBytes,
      retrieval_tags: retrievalTags
    }),
    onSuccess: () => client.invalidateQueries({ queryKey: resourceQueryKeys.all })
  }))
}

export function usePersonalMemoryProfileQuery(agentId: () => string) {
  return createQuery(() => {
    const id = agentId().trim()
    return {
      queryKey: resourceQueryKeys.personalMemoryProfile(id),
      queryFn: async () => mapMemoryProfileListView({ memory_profiles: [await resourceApi.getPersonalMemoryProfileByAgent(id)] }).profiles[0],
      enabled: id.length > 0
    }
  })
}

export function useMcpHealthMutation() {
  return createMutation(() => ({
    mutationFn: (toolId: string) => resourceApi.getMcpServerHealth(toolId)
  }))
}

export function createResourceMutationRegistry(client: { invalidateQueries: (options: { queryKey: readonly string[] }) => unknown }) {
  const invalidate = () => client.invalidateQueries({ queryKey: resourceQueryKeys.all })
  return {
    registerTool: async (form: ResourceToolCommandFormViewModel) => {
      const result = await resourceApi.registerTool(mapToolCommand(form))
      invalidate()
      return result
    },
    updateTool: async (toolId: string, form: ResourceToolCommandFormViewModel) => {
      const result = await resourceApi.updateTool(toolId, mapToolPatchCommand(form))
      invalidate()
      return result
    },
    disableTool: async (toolId: string) => {
      const result = await resourceApi.disableTool(toolId)
      invalidate()
      return result
    },
    deleteTool: async (toolId: string) => {
      const result = await resourceApi.deleteTool(toolId)
      invalidate()
      return result
    },
    callTool: (body: ToolCallRequest) => resourceApi.callTool(body),
    importSkill: async (file: File) => {
      const body = new FormData()
      body.set('package', file)
      const result = await resourceApi.importSkill(body)
      invalidate()
      return result
    },
    createSkill: async (form: ResourceSkillCommandFormViewModel) => {
      const result = await resourceApi.createSkill(skillCreatePayload(form))
      invalidate()
      return result
    },
    exportSkill: (skillId: string) => resourceApi.exportSkill(skillId),
    saveSkillMarkdown: async (skillId: string, raw: string) => {
      const result = await resourceApi.saveSkillMarkdown(skillId, { raw })
      invalidate()
      return result
    },
    saveSkillFile: async (skillId: string, path: string, content: string) => {
      const result = await resourceApi.saveSkillFile(skillId, path, { content })
      invalidate()
      return result
    },
    createSkillFolder: async (skillId: string, path: string) => {
      const result = await resourceApi.createSkillFolder(skillId, path)
      invalidate()
      return result
    },
    uploadSkillFile: async (skillId: string, path: string, body: FormData) => {
      const result = await resourceApi.uploadSkillFile(skillId, path, body)
      invalidate()
      return result
    },
    downloadSkillFile: (skillId: string, path: string) => resourceApi.downloadSkillFile(skillId, path),
    deleteSkillFile: async (skillId: string, path: string) => {
      const result = await resourceApi.deleteSkillFile(skillId, path)
      invalidate()
      return result
    },
    disableSkill: async (skillId: string) => {
      const result = await resourceApi.disableSkill(skillId)
      invalidate()
      return result
    },
    deleteSkill: async (skillId: string) => {
      const result = await resourceApi.deleteSkill(skillId)
      invalidate()
      return result
    },
    createSkillBundle: async (form: ResourceSkillBundleCommandFormViewModel) => {
      const result = await resourceApi.createSkillBundle({
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        skill_ids: form.skillIds
      })
      invalidate()
      return result
    },
    addSkillsToBundle: async (bundleId: string, skillIds: string[]) => {
      const result = await resourceApi.addSkillsToBundle(bundleId, { skill_ids: skillIds })
      invalidate()
      return result
    },
    importSkillIntoBundle: async (bundleId: string, file: File) => {
      const body = new FormData()
      body.set('package', file)
      const result = await resourceApi.importSkillIntoBundle(bundleId, body)
      invalidate()
      return result
    },
    removeSkillFromBundle: async (bundleId: string, skillId: string) => {
      const result = await resourceApi.removeSkillFromBundle(bundleId, skillId)
      invalidate()
      return result
    },
    deleteSkillBundle: async (bundleId: string, deleteSkills: boolean) => {
      const result = await resourceApi.deleteSkillBundle(bundleId, deleteSkills)
      invalidate()
      return result
    },
    registerMcpServer: async (form: ResourceMcpServerCommandFormViewModel) => {
      const configJson = form.configJson?.trim()
      if (configJson) {
        const servers = []
        for (const command of mapMcpConfigCommands(configJson)) {
          servers.push(await resourceApi.registerMcpServer(command))
        }
        invalidate()
        return servers.length === 1 ? servers[0] : { imported_count: servers.length, servers }
      }
      const result = await resourceApi.registerMcpServer(mapMcpServerCommand(form))
      invalidate()
      return result
    },
    updateMcpServer: async (toolId: string, form: ResourceMcpServerCommandFormViewModel) => {
      const result = await resourceApi.updateMcpServer(toolId, mapMcpServerPatchCommand(form))
      invalidate()
      return result
    },
    disableMcpServer: async (toolId: string) => {
      const result = await resourceApi.disableMcpServer(toolId)
      invalidate()
      return result
    },
    deleteMcpServer: async (toolId: string) => {
      const result = await resourceApi.deleteMcpServer(toolId)
      invalidate()
      return result
    }
  }
}

export function buildMemoryDigestQuery(form: MemoryDigestFormViewModel) {
  return mapMemoryDigestCommand(form)
}

export function createMemoryResourceMutationRegistry() {
  return {
    buildDigest: async (form: MemoryDigestFormViewModel): Promise<MemoryDigestViewModel> => {
      const result = await resourceApi.buildMemoryDigest(buildMemoryDigestQuery(form))
      return mapMemoryDigestView(result)
    }
  }
}

export function useSkillCommandMutations() {
  const client = useQueryClient()
  const registry = createResourceMutationRegistry(client)
  return {
    import: createMutation(() => ({ mutationFn: registry.importSkill })),
    create: createMutation(() => ({ mutationFn: registry.createSkill })),
    export: createMutation(() => ({ mutationFn: registry.exportSkill })),
    saveMarkdown: createMutation(() => ({
      mutationFn: ({ skillId, raw }: { skillId: string; raw: string }) => registry.saveSkillMarkdown(skillId, raw)
    })),
    getFile: createMutation(() => ({
      mutationFn: ({ skillId, path }: { skillId: string; path: string }) => resourceApi.getSkillFile(skillId, path)
    })),
    saveFile: createMutation(() => ({
      mutationFn: ({ skillId, path, content }: { skillId: string; path: string; content: string }) => registry.saveSkillFile(skillId, path, content)
    })),
    createFolder: createMutation(() => ({
      mutationFn: ({ skillId, path }: { skillId: string; path: string }) => registry.createSkillFolder(skillId, path)
    })),
    uploadFile: createMutation(() => ({
      mutationFn: ({ skillId, path, body }: { skillId: string; path: string; body: FormData }) => registry.uploadSkillFile(skillId, path, body)
    })),
    downloadFile: createMutation(() => ({
      mutationFn: ({ skillId, path }: { skillId: string; path: string }) => registry.downloadSkillFile(skillId, path)
    })),
    deleteFile: createMutation(() => ({
      mutationFn: ({ skillId, path }: { skillId: string; path: string }) => registry.deleteSkillFile(skillId, path)
    })),
    disable: createMutation(() => ({ mutationFn: registry.disableSkill })),
    delete: createMutation(() => ({ mutationFn: registry.deleteSkill }))
  }
}

export function useSkillBundleCommandMutations() {
  const client = useQueryClient()
  const registry = createResourceMutationRegistry(client)
  return {
    create: createMutation(() => ({ mutationFn: registry.createSkillBundle })),
    addSkills: createMutation(() => ({
      mutationFn: ({ bundleId, skillIds }: { bundleId: string; skillIds: string[] }) =>
        registry.addSkillsToBundle(bundleId, skillIds)
    })),
    importSkill: createMutation(() => ({
      mutationFn: ({ bundleId, file }: { bundleId: string; file: File }) =>
        registry.importSkillIntoBundle(bundleId, file)
    })),
    removeSkill: createMutation(() => ({
      mutationFn: ({ bundleId, skillId }: { bundleId: string; skillId: string }) =>
        registry.removeSkillFromBundle(bundleId, skillId)
    })),
    delete: createMutation(() => ({
      mutationFn: ({ bundleId, deleteSkills }: { bundleId: string; deleteSkills: boolean }) =>
        registry.deleteSkillBundle(bundleId, deleteSkills)
    }))
  }
}

export function useToolCommandMutations() {
  const client = useQueryClient()
  const registry = createResourceMutationRegistry(client)
  return {
    register: createMutation(() => ({ mutationFn: registry.registerTool })),
    update: createMutation(() => ({
      mutationFn: ({ toolId, form }: { toolId: string; form: ResourceToolCommandFormViewModel }) => registry.updateTool(toolId, form)
    })),
    disable: createMutation(() => ({ mutationFn: registry.disableTool })),
    delete: createMutation(() => ({ mutationFn: registry.deleteTool }))
  }
}

export function useToolCallMutation() {
  return createMutation(() => ({
    mutationFn: (body: ToolCallRequest) => resourceApi.callTool(body)
  }))
}

export function useMcpCommandMutations() {
  const client = useQueryClient()
  const registry = createResourceMutationRegistry(client)
  return {
    register: createMutation(() => ({ mutationFn: registry.registerMcpServer })),
    update: createMutation(() => ({
      mutationFn: ({ toolId, form }: { toolId: string; form: ResourceMcpServerCommandFormViewModel }) => registry.updateMcpServer(toolId, form)
    })),
    disable: createMutation(() => ({ mutationFn: registry.disableMcpServer })),
    delete: createMutation(() => ({ mutationFn: registry.deleteMcpServer }))
  }
}

export function useMemoryDigestMutation() {
  const registry = createMemoryResourceMutationRegistry()
  return createMutation(() => ({
    mutationFn: registry.buildDigest
  }))
}

export function useMcpSyncMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (toolId: string) => resourceApi.syncMcpServer(toolId),
    onSuccess: () => client.invalidateQueries({ queryKey: resourceQueryKeys.all })
  }))
}

export function useResourceBindMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: ResourceBindingFormViewModel) => resourceApi.bindResource(mapResourceBindingCommand(form)),
    onSuccess: () => client.invalidateQueries({ queryKey: resourceQueryKeys.all })
  }))
}

export function useResourceUnbindMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: ResourceBindingFormViewModel) => resourceApi.unbindResource(mapResourceBindingCommand(form)),
    onSuccess: () => client.invalidateQueries({ queryKey: resourceQueryKeys.all })
  }))
}

export function buildResourceQuery(filters: ResourceFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'search', filters.search)
  appendParam(params, 'category', filters.category)
  appendParam(params, 'risk_level', filters.riskLevel)
  appendParam(params, 'enabled', filters.enabled)
  appendParam(params, 'resource_id', filters.resourceId)
  appendParam(params, 'bound_agent_id', filters.boundAgentId)
  appendParam(params, 'required_tool_id', filters.requiredToolId)
  appendParam(params, 'include_usage', filters.includeUsage)
  appendParam(params, 'include_bindings', filters.includeBindings)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  const query = params.toString()
  return query ? `?${query}` : ''
}

export function resourceListFilters(filters: ResourceFilters = {}, page: Partial<Pick<ResourceFilters, 'limit' | 'offset'>> = {}) {
  return {
    ...filters,
    limit: page.limit ?? filters.limit ?? DEFAULT_RESOURCE_PAGE_SIZE,
    offset: page.offset ?? filters.offset ?? 0
  }
}

export function resourceCatalogFilters(filters: ResourceFilters = {}) {
  return resourceListFilters(filters, { limit: RESOURCE_CATALOG_PAGE_SIZE, offset: 0 })
}

export function nextResourcePageOffset(lastPage: { page?: { offset?: number; limit?: number; returned?: number; has_more?: boolean } }) {
  const page = lastPage.page
  if (!page?.has_more) return undefined
  const offset = page.offset ?? 0
  const returned = page.returned ?? page.limit ?? DEFAULT_RESOURCE_PAGE_SIZE
  return offset + returned
}

export function combineToolPages(pages: ResourceAdminViewDTO[] = []): ResourceAdminViewDTO {
  return combineResourcePages(pages, {
    tools: pages.flatMap((page) => page.tools ?? [])
  })
}

export function combineSkillPages(pages: ResourceAdminViewDTO[] = []): ResourceAdminViewDTO {
  return combineResourcePages(pages, {
    skills: pages.flatMap((page) => page.skills ?? [])
  })
}

export function combineMcpServerPages(pages: ResourceAdminViewDTO[] = []): ResourceAdminViewDTO {
  return combineResourcePages(pages, {
    mcp_servers: pages.flatMap((page) => page.mcp_servers ?? [])
  })
}

export function combineMemoryProfilePages(pages: MemoryResourceAdminViewDTO[] = []): MemoryResourceAdminViewDTO {
  return combineResourcePages(pages, {
    memory_profiles: pages.flatMap((page) => page.memory_profiles ?? [])
  })
}

function combineResourcePages<T extends { page?: { total?: number; limit?: number; offset?: number; returned?: number; has_more?: boolean }; usage?: unknown[]; bindings?: unknown[] }>(
  pages: T[],
  collections: Partial<T>
): T {
  const itemCount = Object.values(collections).reduce((sum, value) => sum + (Array.isArray(value) ? value.length : 0), 0)
  const first = pages[0]?.page
  const last = pages[pages.length - 1]?.page
  return {
    ...collections,
    usage: pages.flatMap((page) => page.usage ?? []),
    bindings: pages.flatMap((page) => page.bindings ?? []),
    page: {
      total: last?.total ?? first?.total ?? itemCount,
      limit: first?.limit ?? DEFAULT_RESOURCE_PAGE_SIZE,
      offset: first?.offset ?? 0,
      returned: itemCount,
      has_more: last?.has_more ?? false
    }
  } as T
}

export function buildMcpDiscoveryQuery(filters: McpDiscoveryFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'query', filters.query)
  appendParam(params, 'limit', filters.limit)
  const query = params.toString()
  return query ? `?${query}` : ''
}

function appendParam(params: URLSearchParams, key: string, value: string | boolean | number | undefined) {
  if (value === undefined || value === '') return
  params.set(key, String(value))
}
