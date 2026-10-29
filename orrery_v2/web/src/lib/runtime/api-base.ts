export interface DesktopBackendAdapter {
  startLocalBackend: () => Promise<number>
}

export interface ApiBaseOptions {
  desktop: boolean
  envBaseUrl?: string
}

let apiBaseUrl = ''
let desktopBackendAdapter: DesktopBackendAdapter | undefined

export async function resolveApiBaseUrl(options: ApiBaseOptions): Promise<string> {
  const envBaseUrl = normalizeApiBaseUrl(options.envBaseUrl)
  if (!options.desktop) {
    apiBaseUrl = envBaseUrl
    return apiBaseUrl
  }

  const adapter = desktopBackendAdapter ?? (await loadDesktopBackendAdapter())
  const port = await adapter.startLocalBackend()
  apiBaseUrl = `http://127.0.0.1:${port}`
  return apiBaseUrl
}

export function waitForApiBaseUrl() {
  return apiBaseUrl
}

export function setApiBaseUrl(value: string) {
  apiBaseUrl = normalizeApiBaseUrl(value)
}

function normalizeApiBaseUrl(value: string | undefined) {
  return (value ?? '').trim().replace(/\/+$/, '')
}

async function loadDesktopBackendAdapter(): Promise<DesktopBackendAdapter> {
  const module = await import('./desktop-backend')
  return module.desktopBackendAdapter
}

export function setDesktopBackendAdapterForTests(adapter: DesktopBackendAdapter | undefined) {
  desktopBackendAdapter = adapter
}

export function clearApiBaseUrlForTests() {
  apiBaseUrl = ''
}
