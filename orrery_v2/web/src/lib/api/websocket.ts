import { session } from '#lib/stores/session.svelte'
import { waitForApiBaseUrl } from '#lib/runtime/api-base'

export function connectMissionStream(missionId: string): EventSource {
  const baseUrl = waitForApiBaseUrl()
  return new EventSource(`${baseUrl}/api/missions/${missionId}/stream${viewerQuery()}`, { withCredentials: true })
}

export function connectEvaluationStream(query = ''): EventSource {
  const baseUrl = waitForApiBaseUrl()
  return new EventSource(`${baseUrl}/api/evaluations/stream${mergeQueries(query, viewerQuery())}`, { withCredentials: true })
}

function viewerQuery() {
  const params = new URLSearchParams()
  if (session.authenticated) {
    params.set('viewer_id', session.actorId)
    params.set('viewer_kind', session.viewerKind)
    params.set('permission_scope', session.permissionScope.join(','))
  }
  const query = params.toString()
  return query ? `?${query}` : ''
}

function mergeQueries(left: string, right: string) {
  const leftQuery = left.replace(/^\?/, '')
  const rightQuery = right.replace(/^\?/, '')
  const query = [leftQuery, rightQuery].filter(Boolean).join('&')
  return query ? `?${query}` : ''
}
