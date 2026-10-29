import { apiFetch, type ApiFetchOptions } from './client'
import type { FeishuRegistrationDTO, GatewayAccountsDTO } from '#lib/types/api'

export const gatewaySettingsApi = {
  getSettings: (options?: ApiFetchOptions) => apiFetch('/api/settings/gateway', options),
  getAccounts: (options?: ApiFetchOptions) => apiFetch<GatewayAccountsDTO>('/api/settings/gateway/accounts', options),
  updateSettings: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch('/api/settings/gateway', { ...options, method: 'PUT', body: JSON.stringify(body) }),
  updateRawSettings: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch('/api/settings/gateway/raw', { ...options, method: 'PUT', body: JSON.stringify(body) }),
  startFeishuRegistration: (accountId: string, options?: ApiFetchOptions) =>
    apiFetch<FeishuRegistrationDTO>('/api/settings/gateway/feishu/registrations', {
      ...options,
      method: 'POST',
      body: JSON.stringify({ account_id: accountId })
    }),
  getFeishuRegistration: (registrationId: string, options?: ApiFetchOptions) =>
    apiFetch<FeishuRegistrationDTO>(`/api/settings/gateway/feishu/registrations/${encodeURIComponent(registrationId)}`, options),
  cancelFeishuRegistration: (registrationId: string, options?: ApiFetchOptions) =>
    apiFetch<FeishuRegistrationDTO>(`/api/settings/gateway/feishu/registrations/${encodeURIComponent(registrationId)}`, {
      ...options,
      method: 'DELETE'
    })
}
