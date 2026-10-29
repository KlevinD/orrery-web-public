import { apiFetch, type ApiFetchOptions } from './client'
import type {
  ResolveResponsibilityBlockerRequest,
  ResponsibilityControlAction,
  ResponsibilityControlRequest,
  ResponsibilityDetailDTO,
  ResponsibilityExternalSignalRequest,
  ResponsibilityExternalSignalResultDTO,
  ResponsibilityFilters,
  ResponsibilityInboxPageDTO,
  ResponsibilityPolicyDTO,
  ResponsibilityPolicyUpdateRequest,
  ResponsibilityWorkKind
} from '#lib/types/responsibility'

export const responsibilityApi = {
  list: (filters: ResponsibilityFilters = {}, options?: ApiFetchOptions) =>
    apiFetch<ResponsibilityInboxPageDTO>(`/api/responsibilities${buildResponsibilityQuery(filters)}`, options),
  get: (responsibilityId: string, options?: ApiFetchOptions) =>
    apiFetch<ResponsibilityDetailDTO>(`/api/responsibilities/${encodeURIComponent(responsibilityId)}`, options),
  control: (
    responsibilityId: string,
    action: ResponsibilityControlAction,
    version: number,
    body: ResponsibilityControlRequest,
    options?: ApiFetchOptions
  ) => apiFetch<ResponsibilityDetailDTO>(
    `/api/responsibilities/${encodeURIComponent(responsibilityId)}/${action}`,
    versionedMutation('POST', version, body, options)
  ),
  getPolicy: (agentId: string, workKind: ResponsibilityWorkKind = 'mission_task', options?: ApiFetchOptions) =>
    apiFetch<ResponsibilityPolicyDTO>(
      `/api/responsibility-policies/${encodeURIComponent(agentId)}?work_kind=${encodeURIComponent(workKind)}`,
      options
    ),
  updatePolicy: (
    agentId: string,
    workKind: ResponsibilityWorkKind,
    version: number,
    body: ResponsibilityPolicyUpdateRequest,
    options?: ApiFetchOptions
  ) => apiFetch<ResponsibilityPolicyDTO>(
    `/api/responsibility-policies/${encodeURIComponent(agentId)}?work_kind=${encodeURIComponent(workKind)}`,
    versionedMutation('PUT', version, body, options)
  ),
  resolveBlocker: (
    blockerId: string,
    version: number,
    body: ResolveResponsibilityBlockerRequest,
    options?: ApiFetchOptions
  ) => apiFetch<ResponsibilityDetailDTO>(
    `/api/responsibility-blockers/${encodeURIComponent(blockerId)}/resolve`,
    versionedMutation('POST', version, body, options)
  ),
  observeSignal: (body: ResponsibilityExternalSignalRequest, options?: ApiFetchOptions) =>
    apiFetch<ResponsibilityExternalSignalResultDTO>('/api/responsibility-signals', jsonMutation('POST', body, options))
}

export function buildResponsibilityQuery(filters: ResponsibilityFilters = {}) {
  const params = new URLSearchParams()
  if (filters.agentId?.trim()) params.set('agent_id', filters.agentId.trim())
  if (filters.states?.length) params.set('state', filters.states.join(','))
  if (filters.limit !== undefined) params.set('limit', String(filters.limit))
  const query = params.toString()
  return query ? `?${query}` : ''
}

function jsonMutation(method: string, body: unknown, options: ApiFetchOptions = {}): ApiFetchOptions {
  return { ...options, method, body: JSON.stringify(body) }
}

function versionedMutation(
  method: string,
  version: number,
  body: unknown,
  options: ApiFetchOptions = {}
): ApiFetchOptions {
  const headers = new Headers(options.headers)
  headers.set('If-Match', `"${version}"`)
  return jsonMutation(method, body ?? {}, { ...options, headers })
}
