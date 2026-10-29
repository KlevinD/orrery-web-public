export function workCenterNotificationHref(url: URL) {
  const params = new URLSearchParams(url.search)
  const tab = params.get('tab')
  params.set('view', params.has('item') ? 'activity' : tab === 'subscriptions' || tab === 'preferences' || tab === 'manage' || params.has('task') ? 'settings' : 'activity')
  if (params.get('view') === 'settings' && !tab) params.set('tab', 'subscriptions')
  return `/work-items?${params}${url.hash}`
}
