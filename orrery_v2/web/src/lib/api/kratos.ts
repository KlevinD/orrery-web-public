import { apiFetch } from './client'
import type { components } from '#lib/types/api.generated'

export type KratosLoginFlowMessageDTO = components['schemas']['BrowserLoginFlowMessage']
export type KratosLoginFlowNodeDTO = components['schemas']['BrowserLoginFlowNode']
export type KratosLoginFlowDTO = components['schemas']['BrowserLoginFlow']

export const kratosApi = {
  loginFlow(provider: string, id: string, options: { signal?: AbortSignal } = {}) {
    return apiFetch<KratosLoginFlowDTO>(
      `/api/auth/identity/${encodeURIComponent(provider)}/login-flow?id=${encodeURIComponent(id)}`,
      { signal: options.signal }
    )
  }
}
