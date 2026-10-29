import { describe, expect, it } from 'vitest'
import { workCenterNotificationHref } from './work-center'
describe('legacy notification links', () => {
  it.each([
    ['?item=inbox%2F1', 'activity', 'item', 'inbox/1'],
    ['?tab=subscriptions&task=daily', 'settings', 'task', 'daily'],
    ['?tab=preferences', 'settings', 'tab', 'preferences'],
    ['?tab=manage', 'settings', 'tab', 'manage'],
    ['?task=daily', 'settings', 'tab', 'subscriptions']
  ])('preserves the destination for %s', (query, view, key, value) => {
    const target = new URL(workCenterNotificationHref(new URL('https://orrery.example/notifications' + query)), 'https://orrery.example')
    expect(target.pathname).toBe('/work-items')
    expect(target.searchParams.get('view')).toBe(view)
    expect(target.searchParams.get(key)).toBe(value)
  })
})
