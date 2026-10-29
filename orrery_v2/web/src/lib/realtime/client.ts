import { Centrifuge } from 'centrifuge'
import { waitForApiBaseUrl } from '#lib/runtime/api-base'
import { session } from '#lib/stores/session.svelte'

export interface RealtimeEnvelope {
  event_id: string
  type: string
  channel: string
  aggregate_type: string
  aggregate_id: string
  version: number
  occurred_at: string
  payload: Record<string, unknown>
}

export interface RealtimeConnectOptions {
  baseUrl?: string
  accessToken?: string
  viewerId?: string
  viewerKind?: string
  permissionScope?: string[]
}

export function createRealtimeClient(options: RealtimeConnectOptions = {}) {
  return new Centrifuge(buildRealtimeConnectUrl({
    baseUrl: options.baseUrl ?? waitForApiBaseUrl(),
    viewerId: options.viewerId ?? session.actorId,
    viewerKind: options.viewerKind ?? session.viewerKind,
    permissionScope: options.permissionScope ?? session.permissionScope
  }))
}

export function buildRealtimeConnectUrl(options: RealtimeConnectOptions = {}) {
  const url = normalizeRealtimeBaseUrl(options.baseUrl ?? '')
  const params = new URLSearchParams()
  if (options.viewerId) params.set('viewer_id', options.viewerId)
  if (options.viewerKind) params.set('viewer_kind', options.viewerKind)
  if (options.permissionScope?.length) params.set('permission_scope', options.permissionScope.join(','))
  const query = params.toString()
  return query ? `${url}?${query}` : url
}

export function normalizeRealtimeBaseUrl(baseUrl: string) {
  const trimmed = baseUrl.trim().replace(/\/+$/, '')
  if (!trimmed) return defaultRealtimeConnectUrl()
  const suffix = trimmed.endsWith('/api') ? '/realtime/connect' : '/api/realtime/connect'
  if (trimmed.startsWith('https://')) return `${trimmed.replace(/^https:\/\//, 'wss://')}${suffix}`
  if (trimmed.startsWith('http://')) return `${trimmed.replace(/^http:\/\//, 'ws://')}${suffix}`
  return `${trimmed}${suffix}`
}

function defaultRealtimeConnectUrl() {
  const current = globalThis.location
  if (!current?.host) return '/api/realtime/connect'
  const protocol = current.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${current.host}/api/realtime/connect`
}
