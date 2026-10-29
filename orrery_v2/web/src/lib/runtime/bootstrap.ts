import { resolveApiBaseUrl } from './api-base'
import { isTauriDesktop } from './environment'

export interface AppBootstrapResult {
  mode: 'desktop' | 'web'
  apiBaseUrl: string
}

export async function bootstrapApp(): Promise<AppBootstrapResult> {
  const desktop = isTauriDesktop()
  const apiBaseUrl = await resolveApiBaseUrl({
    desktop,
    envBaseUrl: import.meta.env.VITE_API_BASE_URL
  })
  return {
    mode: desktop ? 'desktop' : 'web',
    apiBaseUrl
  }
}
