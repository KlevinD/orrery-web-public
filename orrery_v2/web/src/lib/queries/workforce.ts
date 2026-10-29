import { createInfiniteQuery, createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { operationsApi } from '#lib/api/operations'
import {
  mapOrganizationCreateCommand,
  mapOrganizationPositionCreateCommand,
  mapOrganizationUnitCreateCommand,
  mapOrgGraphView,
  mapTalentMarketHireCommand,
  mapWorkforceMemberCommand,
  mapWorkforceView
} from '#lib/mappers/workforce'
import { operationsQueryKeys } from '#lib/queries/operations'
import { createIdempotencyKey } from '#lib/runtime/ids'
import type {
  DeleteTalentMarketMembersCommand,
  DeleteTalentMarketMembersResponseDTO,
  OrgGraphDTO,
  PutRoleAssignmentsCommand,
  RoleAssignmentPageDTO,
  TalentMarketExportRequest,
  TalentMarketImportCommitRequest,
  TalentMarketImportCommitResponseDTO,
  TalentMarketImportPreviewResponseDTO,
  UpsertWorkforceMemberCommand,
  WorkforcePageDTO
} from '#lib/types/api'
import type { WorkforceAvatarDTO } from '#lib/types/api'
import { refreshWorkforceAvatar } from '#lib/avatar/workforceAvatar.svelte'
import type {
  OrganizationCreateFormViewModel,
  OrganizationPositionCreateFormViewModel,
  OrganizationUnitCreateFormViewModel,
  TalentMarketHireFormViewModel,
  WorkforceMemberFormViewModel
} from '#lib/types/view-models'

export interface WorkforceFilters {
  organizationId?: string
  departmentId?: string
  enabledOnly?: boolean | string
  includeDetails?: boolean | string
  limit?: number
  offset?: number
}

export const DEFAULT_WORKFORCE_PAGE_SIZE = 20

export interface OrgGraphFilters {
  organizationId?: string
  includeMembers?: boolean | string
}

export interface RoleAssignmentFilters {
  actorType?: string
  actorId?: string
  roleId?: string
  organizationCode?: string
  effectiveOnly?: boolean | string
}

export const workforceQueryKeys = {
  all: ['workforce'] as const,
  membersRoot: ['workforce', 'members'] as const,
  marketRoot: ['workforce', 'talent-market'] as const,
  orgRoot: ['workforce', 'org-graph'] as const,
  rolesRoot: ['workforce', 'org-roles'] as const,
  roleAssignmentsRoot: ['workforce', 'org-role-assignments'] as const,
  responsibilityProfilesRoot: ['workforce', 'responsibility-profiles'] as const,
  members: (filters: WorkforceFilters = {}) => ['workforce', 'members', buildWorkforceQuery(filters)] as const,
  market: (filters: WorkforceFilters = {}) => ['workforce', 'talent-market', buildWorkforceQuery(filters)] as const,
  marketCount: (filters: WorkforceFilters = {}) => ['workforce', 'talent-market', 'count', buildWorkforceQuery(filters)] as const,
  orgGraph: (filters: OrgGraphFilters = {}) => ['workforce', 'org-graph', buildOrgGraphQuery(filters)] as const,
  agentResponsibilityProfile: (agentProfileId: string, historyLimit = 20) => ['workforce', 'responsibility-profiles', 'agent', agentProfileId, historyLimit] as const,
  organizationResponsibilityProfile: (organizationScopeId: string, historyLimit = 20) => ['workforce', 'responsibility-profiles', 'organization', organizationScopeId, historyLimit] as const,
  roleAssignments: (filters: RoleAssignmentFilters = {}) => ['workforce', 'org-role-assignments', buildRoleAssignmentQuery(filters)] as const
}

const idempotency = createIdempotencyKey

export function useWorkforceMembersQuery(filters: () => WorkforceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = workforceListFilters(filters())
    return {
      queryKey: workforceQueryKeys.members(current),
      queryFn: async ({ signal }) =>
        mapWorkforceView((await operationsApi.listWorkforceMembers(buildWorkforceQuery(current), { signal })) as WorkforcePageDTO),
      enabled: enabled()
    }
  })
}

export function useTalentMarketMembersQuery(filters: () => WorkforceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = workforceListFilters(filters())
    return {
      queryKey: workforceQueryKeys.market(current),
      queryFn: async ({ signal }) =>
        mapWorkforceView((await operationsApi.listTalentMarketMembers(buildWorkforceQuery(current), { signal })) as WorkforcePageDTO),
      enabled: enabled()
    }
  })
}

export function useTalentMarketCountQuery(filters: () => WorkforceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = marketCountFilters(filters())
    return {
      queryKey: workforceQueryKeys.marketCount(current),
      queryFn: async ({ signal }) =>
        (await operationsApi.listTalentMarketMembers(buildWorkforceQuery(current), { signal })) as WorkforcePageDTO,
      enabled: enabled()
    }
  })
}

export function useOrgGraphQuery(filters: () => OrgGraphFilters = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: workforceQueryKeys.orgGraph(current),
      queryFn: async ({ signal }) => mapOrgGraphView((await operationsApi.getOrgGraph(buildOrgGraphQuery(current), { signal })) as OrgGraphDTO),
      enabled: enabled()
    }
  })
}

export function useAgentResponsibilityProfileQuery(
  agentProfileId: () => string,
  enabled: () => boolean = () => true,
  historyLimit: () => number = () => 20
) {
  return createQuery(() => {
    const profileId = agentProfileId().trim()
    const limit = normalizeResponsibilityProfileHistoryLimit(historyLimit())
    return {
      queryKey: workforceQueryKeys.agentResponsibilityProfile(profileId, limit),
      queryFn: ({ signal }) => operationsApi.getAgentResponsibilityProfile(profileId, limit, { signal }),
      enabled: enabled() && profileId.length > 0,
      staleTime: 60_000
    }
  })
}

export function useOrganizationResponsibilityProfileQuery(
  organizationScopeId: () => string,
  enabled: () => boolean = () => true,
  historyLimit: () => number = () => 20
) {
  return createQuery(() => {
    const scopeId = organizationScopeId().trim()
    const limit = normalizeResponsibilityProfileHistoryLimit(historyLimit())
    return {
      queryKey: workforceQueryKeys.organizationResponsibilityProfile(scopeId, limit),
      queryFn: ({ signal }) => operationsApi.getOrganizationResponsibilityProfile(scopeId, limit, { signal }),
      enabled: enabled() && scopeId.length > 0,
      staleTime: 60_000
    }
  })
}

function normalizeResponsibilityProfileHistoryLimit(historyLimit: number) {
  return Number.isFinite(historyLimit) ? Math.min(100, Math.max(0, Math.trunc(historyLimit))) : 20
}

export function useWorkforceMembersInfiniteQuery(filters: () => WorkforceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createInfiniteQuery(() => {
    const current = workforceListFilters(filters(), { offset: 0 })
    return {
      queryKey: [...workforceQueryKeys.members(current), 'infinite'] as const,
      initialPageParam: 0,
      queryFn: async ({ pageParam, signal }) =>
        (await operationsApi.listWorkforceMembers(buildWorkforceQuery(workforceListFilters(filters(), { offset: Number(pageParam) || 0 })), { signal })) as WorkforcePageDTO,
      getNextPageParam: nextWorkforcePageOffset,
      enabled: enabled()
    }
  })
}

export function useTalentMarketMembersInfiniteQuery(filters: () => WorkforceFilters = () => ({}), enabled: () => boolean = () => true) {
  return createInfiniteQuery(() => {
    const current = workforceListFilters(filters(), { offset: 0 })
    return {
      queryKey: [...workforceQueryKeys.market(current), 'infinite'] as const,
      initialPageParam: 0,
      queryFn: async ({ pageParam, signal }) =>
        (await operationsApi.listTalentMarketMembers(buildWorkforceQuery(workforceListFilters(filters(), { offset: Number(pageParam) || 0 })), { signal })) as WorkforcePageDTO,
      getNextPageParam: nextWorkforcePageOffset,
      enabled: enabled()
    }
  })
}

export function useOrganizationCreateMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: OrganizationCreateFormViewModel) =>
      operationsApi.createOrganization(mapOrganizationCreateCommand(form), { idempotencyKey: idempotency() }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: workforceQueryKeys.orgRoot })
      client.invalidateQueries({ queryKey: workforceQueryKeys.membersRoot })
    }
  }))
}

export function useOrganizationUnitCreateMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: OrganizationUnitCreateFormViewModel) =>
      operationsApi.createOrganizationUnit(mapOrganizationUnitCreateCommand(form), { idempotencyKey: idempotency() }),
    onSuccess: () => client.invalidateQueries({ queryKey: workforceQueryKeys.orgRoot })
  }))
}

export function useOrganizationPositionCreateMutation() {
  const client = useQueryClient()
  const registry = createOrganizationMutationRegistry(client)

  return createMutation(() => ({
    mutationFn: registry.createOrganizationPosition
  }))
}

export interface OrganizationOwnerUpdateInput {
	organizationNodeId: string
	ownerProfileId: string
}

type QueryInvalidator = {
	invalidateQueries: (filters: { queryKey: readonly unknown[] }) => unknown
}

export function createOrganizationMutationRegistry(client: QueryInvalidator) {
	return {
		createOrganizationPosition: async (form: OrganizationPositionCreateFormViewModel) => {
			const result = await operationsApi.createOrganizationPosition(mapOrganizationPositionCreateCommand(form), { idempotencyKey: idempotency() })
			invalidateOrganizationMutations(client)
			return result
		},
		deleteOrganization: async (organizationId: string) => {
			const result = await operationsApi.deleteOrganization(organizationId, { idempotencyKey: idempotency() })
			invalidateOrganizationMutations(client)
			return result
		},
		deleteOrganizationUnit: async (departmentId: string) => {
			const result = await operationsApi.deleteOrganizationUnit(departmentId, { idempotencyKey: idempotency() })
			invalidateOrganizationMutations(client)
			return result
		},
		deleteOrganizationPosition: async (positionId: string) => {
			const result = await operationsApi.deleteOrganizationPosition(positionId, { idempotencyKey: idempotency() })
			invalidateOrganizationMutations(client)
			return result
		},
		updateOrganizationOwner: async (input: OrganizationOwnerUpdateInput) => {
			const result = await operationsApi.updateOrganizationOwner(input.organizationNodeId, {
				owner_profile_id: input.ownerProfileId.trim()
			}, { idempotencyKey: idempotency() })
			invalidateOrganizationMutations(client)
			return result
		}
	}
}

function invalidateOrganizationMutations(client: QueryInvalidator) {
	client.invalidateQueries({ queryKey: workforceQueryKeys.all })
	client.invalidateQueries({ queryKey: operationsQueryKeys.all })
}

export function useOrganizationDeleteMutation() {
	const client = useQueryClient()
	const registry = createOrganizationMutationRegistry(client)

	return createMutation(() => ({
		mutationFn: registry.deleteOrganization
	}))
}

export function useOrganizationUnitDeleteMutation() {
	const client = useQueryClient()
	const registry = createOrganizationMutationRegistry(client)

	return createMutation(() => ({
		mutationFn: registry.deleteOrganizationUnit
	}))
}

export function useOrganizationPositionDeleteMutation() {
	const client = useQueryClient()
	const registry = createOrganizationMutationRegistry(client)

	return createMutation(() => ({
		mutationFn: registry.deleteOrganizationPosition
	}))
}

export function useOrganizationOwnerUpdateMutation() {
	const client = useQueryClient()
	const registry = createOrganizationMutationRegistry(client)

	return createMutation(() => ({
		mutationFn: registry.updateOrganizationOwner
	}))
}

export function useWorkforceUpsertMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: WorkforceMemberFormViewModel) => {
      const command = mapWorkforceMemberCommand(form)
      return form.agentProfileId
        ? operationsApi.upsertWorkforceMember(form.agentProfileId, command, { idempotencyKey: idempotency() })
        : operationsApi.createWorkforceMember(command, { idempotencyKey: idempotency() })
    },
    onSuccess: () => {
      client.invalidateQueries({ queryKey: workforceQueryKeys.membersRoot })
      client.invalidateQueries({ queryKey: workforceQueryKeys.orgRoot })
    }
  }))
}

export function useWorkforceAvatarUploadMutation() {
  return createMutation(() => ({
    mutationFn: ({ agentProfileId, file }: { agentProfileId: string; file: File }) =>
      operationsApi.uploadWorkforceAvatar(agentProfileId, file) as Promise<WorkforceAvatarDTO>,
    onSuccess: (_, input) => refreshWorkforceAvatar(input.agentProfileId)
  }))
}

export function useWorkforceAvatarDefaultMutation() {
  return createMutation(() => ({
    mutationFn: ({ agentProfileId, defaultId }: { agentProfileId: string; defaultId: string }) =>
      operationsApi.setWorkforceDefaultAvatar(agentProfileId, defaultId) as Promise<WorkforceAvatarDTO>,
    onSuccess: (_, input) => refreshWorkforceAvatar(input.agentProfileId)
  }))
}

export type WorkforceReleaseMutationInput = string | {
  agentProfileId: string
  reason?: string
}

export function workforceReleaseRequest(input: WorkforceReleaseMutationInput): { agentProfileId: string; body: Record<string, string> } {
  const agentProfileId = typeof input === 'string' ? input : input.agentProfileId
  const reason = typeof input === 'string' ? '' : (input.reason ?? '').trim()
  return {
    agentProfileId,
    body: reason ? { reason } : {}
  }
}

export function useWorkforceReleaseMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (input: WorkforceReleaseMutationInput) => {
      const request = workforceReleaseRequest(input)
      return operationsApi.releaseWorkforceMemberToMarket(request.agentProfileId, request.body, { idempotencyKey: idempotency() })
    },
    onSuccess: () => {
      client.invalidateQueries({ queryKey: workforceQueryKeys.all })
      client.invalidateQueries({ queryKey: operationsQueryKeys.all })
    }
  }))
}

export function useTalentMarketHireMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: TalentMarketHireFormViewModel) => {
      const command = mapTalentMarketHireCommand(form)
      return operationsApi.hireTalentFromMarket(command.agent_profile_id, command, { idempotencyKey: idempotency() })
    },
    onSuccess: () => {
      client.invalidateQueries({ queryKey: workforceQueryKeys.all })
      client.invalidateQueries({ queryKey: operationsQueryKeys.all })
    }
  }))
}

export function useOrgRolesQuery(enabled: () => boolean = () => true) {
  return createQuery(() => ({
    queryKey: workforceQueryKeys.rolesRoot,
    queryFn: async () => operationsApi.listOrgRoles(),
    enabled: enabled()
  }))
}

export function useOrgRoleAssignmentsQuery(
  filters: () => RoleAssignmentFilters = () => ({}),
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: workforceQueryKeys.roleAssignments(current),
      queryFn: async () => operationsApi.listOrgRoleAssignments(buildRoleAssignmentQuery(current)) as Promise<RoleAssignmentPageDTO>,
      enabled: enabled()
    }
  })
}

export function useOrgRoleAssignmentsMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (body: PutRoleAssignmentsCommand) =>
      operationsApi.putOrgRoleAssignments(body, { idempotencyKey: idempotency() }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: workforceQueryKeys.roleAssignmentsRoot })
      client.invalidateQueries({ queryKey: workforceQueryKeys.orgRoot })
      client.invalidateQueries({ queryKey: operationsQueryKeys.permissionRoot })
    }
  }))
}

export function useOrgRoleAssignmentDeleteMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (assignmentId: string) =>
      operationsApi.deleteOrgRoleAssignment(assignmentId, { idempotencyKey: idempotency() }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: workforceQueryKeys.roleAssignmentsRoot })
      client.invalidateQueries({ queryKey: workforceQueryKeys.orgRoot })
      client.invalidateQueries({ queryKey: operationsQueryKeys.all })
    }
  }))
}

export function useTalentMarketDeleteMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (agentProfileId: string) =>
      operationsApi.deleteTalentMarketMember(agentProfileId, {
        idempotencyKey: idempotency()
      }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: workforceQueryKeys.marketRoot })
      client.invalidateQueries({ queryKey: operationsQueryKeys.all })
    }
  }))
}

export function useTalentMarketBatchDeleteMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (body: DeleteTalentMarketMembersCommand) =>
      operationsApi.deleteTalentMarketMembers(body, {
        idempotencyKey: idempotency()
      }) as Promise<DeleteTalentMarketMembersResponseDTO>,
    onSuccess: () => {
      client.invalidateQueries({ queryKey: workforceQueryKeys.marketRoot })
      client.invalidateQueries({ queryKey: operationsQueryKeys.all })
    }
  }))
}

export function useTalentMarketImportPreviewMutation() {
  return createMutation(() => ({
    mutationFn: (file: File) =>
      operationsApi.previewTalentMarketImport(file, {
        idempotencyKey: idempotency()
      }) as Promise<TalentMarketImportPreviewResponseDTO>
  }))
}

export function useTalentMarketImportCommitMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (body: TalentMarketImportCommitRequest) =>
      operationsApi.commitTalentMarketImport(body, {
        idempotencyKey: idempotency()
      }) as Promise<TalentMarketImportCommitResponseDTO>,
    onSuccess: () => {
      client.invalidateQueries({ queryKey: workforceQueryKeys.marketRoot })
      client.invalidateQueries({ queryKey: operationsQueryKeys.all })
    }
  }))
}

export function useTalentMarketExportMutation() {
  return createMutation(() => ({
    mutationFn: (body: TalentMarketExportRequest) => operationsApi.exportTalentMarket(body) as Promise<Blob>
  }))
}

export function useWorkforceEnableMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (agentProfileId: string) => operationsApi.enableWorkforceMember(agentProfileId),
    onSuccess: () => client.invalidateQueries({ queryKey: workforceQueryKeys.all })
  }))
}

export function useWorkforceDisableMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (agentProfileId: string) => operationsApi.disableWorkforceMember(agentProfileId),
    onSuccess: () => client.invalidateQueries({ queryKey: workforceQueryKeys.all })
  }))
}

export function buildWorkforceQuery(filters: WorkforceFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'organization_id', filters.organizationId)
  appendParam(params, 'department_id', filters.departmentId)
  appendParam(params, 'enabled_only', filters.enabledOnly)
  appendParam(params, 'include_details', filters.includeDetails)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  return queryString(params)
}

export function workforceListFilters(filters: WorkforceFilters = {}, page: Partial<Pick<WorkforceFilters, 'limit' | 'offset'>> = {}) {
  return {
    ...filters,
    limit: page.limit ?? filters.limit ?? DEFAULT_WORKFORCE_PAGE_SIZE,
    offset: page.offset ?? filters.offset ?? 0
  }
}

export function marketCountFilters(filters: WorkforceFilters = {}) {
  return workforceListFilters(filters, { limit: 1, offset: 0 })
}

export function nextWorkforcePageOffset(lastPage: Pick<WorkforcePageDTO, 'page'>) {
  const page = lastPage.page
  if (!page?.has_more) return undefined
  const offset = page.offset ?? 0
  const returned = page.returned ?? page.limit ?? DEFAULT_WORKFORCE_PAGE_SIZE
  return offset + returned
}

export function combineWorkforcePages(pages: WorkforcePageDTO[] = []): WorkforcePageDTO {
  const members = pages.flatMap((page) => page.members ?? [])
  const first = pages[0]?.page
  const last = pages[pages.length - 1]?.page
  return {
    members,
    page: {
      total: last?.total ?? first?.total ?? members.length,
      limit: first?.limit ?? DEFAULT_WORKFORCE_PAGE_SIZE,
      offset: first?.offset ?? 0,
      returned: members.length,
      has_more: last?.has_more ?? false
    }
  }
}

export function buildOrgGraphQuery(filters: OrgGraphFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'organization_id', filters.organizationId)
  appendParam(params, 'include_members', filters.includeMembers)
  return queryString(params)
}

export function buildRoleAssignmentQuery(filters: RoleAssignmentFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'actor_type', filters.actorType)
  appendParam(params, 'actor_id', filters.actorId)
  appendParam(params, 'role_id', filters.roleId)
  appendParam(params, 'organization_code', filters.organizationCode)
  appendParam(params, 'effective_only', filters.effectiveOnly)
  return queryString(params)
}

function appendParam(params: URLSearchParams, key: string, value: string | number | boolean | undefined) {
  if (value === undefined || value === '') return
  params.set(key, String(value))
}

function queryString(params: URLSearchParams) {
  const query = params.toString()
  return query ? `?${query}` : ''
}
