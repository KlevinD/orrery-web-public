import { apiFetch, type ApiFetchOptions } from './client'
import type {
  GatewayWeixinLoginStartResponseDTO,
  GatewayWeixinLoginStatusResponseDTO,
  PersonalAssistantAvatarDTO,
  PersonalAssistantNotificationPreferencesDTO,
  PersonalAssistantProfileDTO,
  ProfileGatewayBindingCodeDTO,
  ProfileGatewayBindingsDTO,
  ProfileIdentityBindingDTO,
  ProfileWeixinDTO,
  ProfileWeixinLoginStartRequest,
  UpdatePersonalAssistantNotificationsRequest,
  UpdatePersonalAssistantProfileRequest
} from '#lib/types/api'

export const profileApi = {
	getPersonalAssistant: (options?: ApiFetchOptions) =>
		apiFetch<PersonalAssistantProfileDTO>('/api/profile/personal-assistant', options),
	createPersonalAssistant: (options?: ApiFetchOptions) =>
		apiFetch<PersonalAssistantProfileDTO>('/api/profile/personal-assistant', {
			...options,
			method: 'POST'
		}),
	updatePersonalAssistant: (body: UpdatePersonalAssistantProfileRequest, options?: ApiFetchOptions) =>
		apiFetch<PersonalAssistantProfileDTO>('/api/profile/personal-assistant', {
			...options,
			method: 'PUT',
			body: JSON.stringify(body)
		}),
	getPersonalAssistantNotifications: (options?: ApiFetchOptions) =>
		apiFetch<PersonalAssistantNotificationPreferencesDTO>('/api/profile/personal-assistant/notifications', options),
	updatePersonalAssistantNotifications: (body: UpdatePersonalAssistantNotificationsRequest, options?: ApiFetchOptions) =>
		apiFetch<PersonalAssistantNotificationPreferencesDTO>('/api/profile/personal-assistant/notifications', {
			...options,
			method: 'PUT',
			body: JSON.stringify(body)
		}),
	uploadPersonalAssistantAvatar: (file: File, options?: ApiFetchOptions) => {
		const body = new FormData()
		body.set('file', file)
		return apiFetch<PersonalAssistantAvatarDTO>('/api/profile/personal-assistant/avatar', {
			...options,
			method: 'PUT',
			body
		})
	},
	deletePersonalAssistantAvatar: (options?: ApiFetchOptions) =>
		apiFetch<void>('/api/profile/personal-assistant/avatar', {
			...options,
			method: 'DELETE'
		}),
	getFeishuIdentity: (options?: ApiFetchOptions) =>
		apiFetch<ProfileIdentityBindingDTO>('/api/profile/identity/feishu', options),
	unbindFeishuIdentity: (options?: ApiFetchOptions) =>
		apiFetch<ProfileIdentityBindingDTO>('/api/profile/identity/feishu', {
			...options,
			method: 'DELETE'
		}),
  getWeixin: (options?: ApiFetchOptions) => apiFetch<ProfileWeixinDTO>('/api/profile/weixin', options),
  setWeixinTarget: (targetKind: 'agent' | 'personal_assistant', agentId?: string, options?: ApiFetchOptions) =>
    apiFetch<ProfileWeixinDTO>('/api/profile/weixin/default-agent', {
      ...options,
      method: 'PUT',
      body: JSON.stringify({ target_kind: targetKind, agent_id: agentId })
    }),
  setWeixinDefaultAgent: (agentId: string, options?: ApiFetchOptions) =>
    apiFetch<ProfileWeixinDTO>('/api/profile/weixin/default-agent', {
      ...options,
      method: 'PUT',
      body: JSON.stringify({ agent_id: agentId })
    }),
  unbindWeixin: (options?: ApiFetchOptions) =>
    apiFetch<ProfileWeixinDTO>('/api/profile/weixin', {
      ...options,
      method: 'DELETE'
    }),
  startWeixinLogin: (body: ProfileWeixinLoginStartRequest = {}, options?: ApiFetchOptions) =>
    apiFetch<GatewayWeixinLoginStartResponseDTO>('/api/profile/weixin/login/start', {
      ...options,
      method: 'POST',
      body: JSON.stringify(body)
    }),
  getWeixinLoginStatus: (sessionKey: string, options?: ApiFetchOptions) =>
    apiFetch<GatewayWeixinLoginStatusResponseDTO>(`/api/profile/weixin/login/${encodeURIComponent(sessionKey)}`, options),
  submitWeixinVerifyCode: (sessionKey: string, verifyCode: string, options?: ApiFetchOptions) =>
    apiFetch<GatewayWeixinLoginStatusResponseDTO>(`/api/profile/weixin/login/${encodeURIComponent(sessionKey)}/verify-code`, {
      ...options,
      method: 'POST',
      body: JSON.stringify({ verify_code: verifyCode })
    }),
  getGatewayBindings: (options?: ApiFetchOptions) => apiFetch<ProfileGatewayBindingsDTO>('/api/profile/gateway-bindings', options),
  createGatewayBindingCode: (channel: 'feishu' | 'dingtalk', accountId: string, agentId: string, options?: ApiFetchOptions) =>
    apiFetch<ProfileGatewayBindingCodeDTO>(`/api/profile/gateway-bindings/${encodeURIComponent(channel)}/code`, {
      ...options,
      method: 'POST',
      body: JSON.stringify({ account_id: accountId, agent_id: agentId })
    }),
  deleteGatewayBinding: (bindingId: string, options?: ApiFetchOptions) =>
    apiFetch<void>(`/api/profile/gateway-bindings/${encodeURIComponent(bindingId)}`, {
      ...options,
      method: 'DELETE'
    })
}
