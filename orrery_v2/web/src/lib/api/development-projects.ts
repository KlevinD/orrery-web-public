import { apiFetch, type ApiFetchOptions } from './client'
import type {
  AddDevelopmentProjectMemberRequest,
  BindDevelopmentRepositoryRequest,
  CreateDevelopmentProjectRequest,
  CreateSCMConnectionRequest,
  DevelopmentProjectDTO,
  DevelopmentArtifactFilters,
  DevelopmentProjectFilters,
  DevelopmentExecutionFilters,
  DevelopmentExecutionPageDTO,
  DevelopmentWorkspacePageDTO,
  DevelopmentProjectMemberMutationDTO,
  DevelopmentProjectMemberPageDTO,
  DevelopmentProjectPageDTO,
  DevelopmentRepositoryBindingPageDTO,
  DevelopmentRepositoryMutationDTO,
  SCMConnectionDTO,
  SCMConnectionFilters,
  SCMConnectionPageDTO,
  SCMConnectionVerificationDTO,
  SCMRepositoryPageDTO,
  TodoExecutionRunPageDTO,
  UpdateDevelopmentProjectMemberRequest,
  UpdateDevelopmentProjectRequest,
  UpdateDevelopmentRepositoryRequest,
  UpdateSCMConnectionRequest
} from '#lib/types/development-project'

export const developmentProjectApi = {
  list: (filters: DevelopmentProjectFilters = {}, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentProjectPageDTO>(`/api/development-projects${buildQuery(filters)}`, options),
  get: (projectId: string, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentProjectDTO>(`/api/development-projects/${encodeURIComponent(projectId)}`, options),
  listExecutions: (projectId: string, filters: DevelopmentExecutionFilters = {}, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentExecutionPageDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/executions${buildQuery(filters)}`, options),
  listWorkspaces: (projectId: string, filters: DevelopmentArtifactFilters = {}, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentWorkspacePageDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/workspaces${buildQuery(filters)}`, options),
  listRuns: (projectId: string, filters: DevelopmentArtifactFilters = {}, options?: ApiFetchOptions) =>
    apiFetch<TodoExecutionRunPageDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/runs${buildQuery(filters)}`, options),
  create: (body: CreateDevelopmentProjectRequest, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentProjectDTO>('/api/development-projects', jsonMutation('POST', body, options)),
  update: (projectId: string, version: number, body: UpdateDevelopmentProjectRequest, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentProjectDTO>(`/api/development-projects/${encodeURIComponent(projectId)}`, versionedMutation('PATCH', version, body, options)),
  archive: (projectId: string, version: number, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentProjectDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/archive`, versionedMutation('POST', version, undefined, options)),
  listMembers: (projectId: string, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentProjectMemberPageDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/members`, options),
  addMember: (projectId: string, projectVersion: number, body: AddDevelopmentProjectMemberRequest, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentProjectMemberMutationDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/members`, versionedMutation('POST', projectVersion, body, options)),
  updateMember: (projectId: string, memberId: string, projectVersion: number, body: UpdateDevelopmentProjectMemberRequest, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentProjectMemberMutationDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/members/${encodeURIComponent(memberId)}`, versionedMutation('PATCH', projectVersion, body, options)),
  removeMember: (projectId: string, memberId: string, projectVersion: number, memberVersion: number, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentProjectDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/members/${encodeURIComponent(memberId)}`, versionedMutation('DELETE', projectVersion, { expected_member_version: memberVersion }, options)),
  listRepositories: (projectId: string, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentRepositoryBindingPageDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/repositories`, options),
  bindRepository: (projectId: string, projectVersion: number, body: BindDevelopmentRepositoryRequest, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentRepositoryMutationDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/repositories`, versionedMutation('POST', projectVersion, body, options)),
  updateRepository: (projectId: string, bindingId: string, projectVersion: number, body: UpdateDevelopmentRepositoryRequest, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentRepositoryMutationDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/repositories/${encodeURIComponent(bindingId)}`, versionedMutation('PATCH', projectVersion, body, options)),
  verifyRepository: (projectId: string, bindingId: string, projectVersion: number, bindingVersion: number, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentRepositoryMutationDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/repositories/${encodeURIComponent(bindingId)}/verify`, versionedMutation('POST', projectVersion, { expected_binding_version: bindingVersion }, options)),
  removeRepository: (projectId: string, bindingId: string, projectVersion: number, bindingVersion: number, options?: ApiFetchOptions) =>
    apiFetch<DevelopmentProjectDTO>(`/api/development-projects/${encodeURIComponent(projectId)}/repositories/${encodeURIComponent(bindingId)}`, versionedMutation('DELETE', projectVersion, { expected_binding_version: bindingVersion }, options))
}

export const scmConnectionApi = {
  list: (filters: SCMConnectionFilters = {}, options?: ApiFetchOptions) =>
    apiFetch<SCMConnectionPageDTO>(`/api/scm-connections${buildQuery(filters)}`, options),
  get: (connectionId: string, options?: ApiFetchOptions) =>
    apiFetch<SCMConnectionDTO>(`/api/scm-connections/${encodeURIComponent(connectionId)}`, options),
  create: (body: CreateSCMConnectionRequest, options?: ApiFetchOptions) =>
    apiFetch<SCMConnectionDTO>('/api/scm-connections', jsonMutation('POST', body, options)),
  update: (connectionId: string, version: number, body: UpdateSCMConnectionRequest, options?: ApiFetchOptions) =>
    apiFetch<SCMConnectionDTO>(`/api/scm-connections/${encodeURIComponent(connectionId)}`, versionedMutation('PATCH', version, body, options)),
  verify: (connectionId: string, version: number, options?: ApiFetchOptions) =>
    apiFetch<SCMConnectionVerificationDTO>(`/api/scm-connections/${encodeURIComponent(connectionId)}/verify`, versionedMutation('POST', version, undefined, options)),
  rotate: (connectionId: string, version: number, secretRef: string, options?: ApiFetchOptions) =>
    apiFetch<SCMConnectionDTO>(`/api/scm-connections/${encodeURIComponent(connectionId)}/rotate`, versionedMutation('POST', version, { secret_ref: secretRef }, options)),
  revoke: (connectionId: string, version: number, options?: ApiFetchOptions) =>
    apiFetch<SCMConnectionDTO>(`/api/scm-connections/${encodeURIComponent(connectionId)}/revoke`, versionedMutation('POST', version, undefined, options)),
  listRepositories: (connectionId: string, options?: ApiFetchOptions) =>
    apiFetch<SCMRepositoryPageDTO>(`/api/scm-connections/${encodeURIComponent(connectionId)}/repositories`, options)
}

function buildQuery(filters: DevelopmentProjectFilters | SCMConnectionFilters | DevelopmentExecutionFilters | DevelopmentArtifactFilters) {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(filters)) {
    if (value === undefined || value === '') continue
    const apiKey = key.replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`)
    params.set(apiKey, String(value))
  }
  const query = params.toString()
  return query ? `?${query}` : ''
}

function jsonMutation(method: string, body: unknown, options: ApiFetchOptions = {}): ApiFetchOptions {
  return { ...options, method, body: JSON.stringify(body) }
}

function versionedMutation(method: string, version: number, body: unknown, options: ApiFetchOptions = {}): ApiFetchOptions {
  const headers = new Headers(options.headers)
  headers.set('If-Match', `"${version}"`)
  return jsonMutation(method, body ?? {}, { ...options, headers })
}
