import { createQuery } from '@tanstack/svelte-query'
import { notificationApi } from '#lib/api/notifications'
import { session } from '#lib/stores/session.svelte'

export const notificationKeys = { all: ['task-notifications'] as const, summary: ['task-notifications', 'summary'] as const }

export function notificationViewerKey() {
  return JSON.stringify([session.securityContextVersion, session.actorId, session.viewerKind,
    session.user.agentProfileId, session.permissionScope])
}

export function useTaskNotificationSummary() {
  return createQuery(() => ({
    queryKey: [...notificationKeys.all, notificationViewerKey(), 'summary'],
    enabled: session.authenticated && session.viewerKind === 'user' && !!session.user.agentProfileId,
    queryFn: ({ signal }) => notificationApi.summary({ signal }),
    staleTime: 20_000, refetchInterval: 30_000, retry: false
  }))
}
