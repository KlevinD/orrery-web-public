import { apiFetch, type ApiFetchOptions } from './client'
import type {
  DeleteWorkforceChannelBindingRequest,
  PutWorkforceChannelBindingRequest,
  WorkforceChannelAccountDTO,
  WorkforceChannelBindingsDTO
} from '#lib/types/api'

function memberBindingsPath(agentProfileId: string) {
  return `/api/workforce/members/${encodeURIComponent(agentProfileId)}/channel-bindings`
}

function accountBindingPath(agentProfileId: string, accountId: string) {
  return `${memberBindingsPath(agentProfileId)}/feishu/${encodeURIComponent(accountId)}`
}

export const workforceChannelBindingsApi = {
  list: (agentProfileId: string, options?: ApiFetchOptions) =>
    apiFetch<WorkforceChannelBindingsDTO>(memberBindingsPath(agentProfileId), options),
  put: (agentProfileId: string, accountId: string, body: PutWorkforceChannelBindingRequest, options?: ApiFetchOptions) =>
    apiFetch<WorkforceChannelAccountDTO>(accountBindingPath(agentProfileId, accountId), {
      ...options,
      method: 'PUT',
      body: JSON.stringify(body)
    }),
  delete: (agentProfileId: string, accountId: string, body: DeleteWorkforceChannelBindingRequest, options?: ApiFetchOptions) =>
    apiFetch<void>(accountBindingPath(agentProfileId, accountId), {
      ...options,
      method: 'DELETE',
      body: JSON.stringify(body)
    })
}
