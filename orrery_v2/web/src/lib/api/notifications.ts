import { apiFetch, type ApiFetchOptions } from './client'
import type { components } from '#lib/types/api.generated'
export type NotificationChannel = components['schemas']['TaskNotificationChannel']
export type TaskSubscription = components['schemas']['TaskNotificationSubscription']
export type NotificationSource = components['schemas']['TaskNotificationSource']
export type NotificationPush = components['schemas']['TaskNotificationPush']
export type NotificationItem = components['schemas']['TaskNotificationItem']
export type NotificationPage = components['schemas']['TaskNotificationPage']
export type NotificationPerson = components['schemas']['TaskNotificationPerson']
export type NotificationReceipt = components['schemas']['TaskNotificationReceipt']
const base = '/api/notifications'
const segment = encodeURIComponent
const mutate = (method: string, body: unknown, options?: ApiFetchOptions): ApiFetchOptions => ({ ...options, method, body: JSON.stringify(body) })
export const notificationApi = {
 channels: (options?: ApiFetchOptions) => apiFetch<NotificationChannel[]>(base + '/channels', options),
 inbox: (unread = false, offset = 0, options?: ApiFetchOptions) => apiFetch<NotificationPage>(base + '?limit=25&offset=' + offset + '&unread=' + unread, options),
 summary: (options?: ApiFetchOptions) => apiFetch<{ unread: number; total: number }>(base + '/summary', options),
 item: (id: string, options?: ApiFetchOptions) => apiFetch<NotificationItem>(base + '/items/' + segment(id), options),
 read: (id: string, read: boolean, options?: ApiFetchOptions) => apiFetch(base + '/items/' + segment(id), mutate('PATCH', { read }, options)),
 sources: (options?: ApiFetchOptions) => apiFetch<NotificationSource[]>(base + '/sources', options),
 source: (id: string, options?: ApiFetchOptions) => apiFetch<NotificationSource>(base + '/sources/' + segment(id), options),
 events: (source: string, options?: ApiFetchOptions) => apiFetch<NotificationItem['event'][]>(base + '/sources/' + segment(source) + '/events', options),
 candidates: (options?: ApiFetchOptions) => apiFetch<NotificationSource[]>(base + '/sources/candidates', options),
 members: (options?: ApiFetchOptions) => apiFetch<NotificationPerson[]>(base + '/members', options),
 saveSource: (source: NotificationSource, create = false, options?: ApiFetchOptions) => apiFetch<NotificationSource>(base + '/sources' + (create ? '' : '/' + segment(source.id)), mutate(create ? 'POST' : 'PUT', { ...source, subscription: undefined }, options)),
 subscribe: (id: string, sub: TaskSubscription, options?: ApiFetchOptions) => apiFetch<TaskSubscription>(base + '/sources/' + segment(id) + '/subscription', mutate('PUT', sub, options)),
 preview: (id: string, options?: ApiFetchOptions) => apiFetch<components['schemas']['TaskNotificationPreview']>(base + '/sources/' + segment(id) + '/preview', options),
 preferences: (options?: ApiFetchOptions) => apiFetch<components['schemas']['TaskNotificationPreferences']>(base + '/preferences', options),
 savePreferences: (channels: NotificationChannel[], options?: ApiFetchOptions) => apiFetch<components['schemas']['TaskNotificationPreferences']>(base + '/preferences', mutate('PUT', { channels }, options)),
 receipts: (event: string, options?: ApiFetchOptions) => apiFetch<NotificationReceipt[]>(base + '/events/' + segment(event) + '/deliveries', options),
 artifact: (item: string, artifact: string, options?: ApiFetchOptions) => apiFetch<Blob>(base + '/items/' + segment(item) + '/artifacts/' + segment(artifact), { ...options, responseType: 'blob' })
}
