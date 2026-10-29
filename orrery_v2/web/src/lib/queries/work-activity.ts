import { createQuery } from '@tanstack/svelte-query'
import { listAllPersonalNotifications } from '#lib/api/work-activity'
import { schedulerApi } from '#lib/api/scheduler'
import { loadAllPages } from './work-inbox'
import { session } from '#lib/stores/session.svelte'
import { hasPermissionScope } from '#lib/permissions/access'

export function useWorkActivitySources() {
  const notifications = createQuery(() => ({
    queryKey: ['task-notifications', 'activity', session.user.agentProfileId, session.securityContextVersion],
    enabled: !!session.user.agentProfileId,
    queryFn: ({ signal }) => listAllPersonalNotifications({ signal }),
    staleTime: 30_000, refetchInterval: 60_000
  }))
  const results = createQuery(() => ({
    queryKey: ['scheduler', 'activity', session.user.userId, session.securityContextVersion],
    enabled: hasPermissionScope(session.permissionScope, 'scheduler.read'),
    queryFn: ({ signal }) => loadAllPages(async offset => {
      const page = await schedulerApi.listRunInbox(`?limit=100&offset=${offset}`, { signal })
      return { items: page.items, total: page.total }
    }),
    staleTime: 30_000, refetchInterval: 60_000
  }))
  return { notifications, results }
}
