import { notificationApi, type NotificationItem } from './notifications'
import { schedulerApi } from './scheduler'
import type { ApiFetchOptions } from './client'
import { session } from '#lib/stores/session.svelte'

function readerGuard() {
  const version = session.securityContextVersion
  return () => { if (session.securityContextVersion !== version) throw new Error('Session changed. Reopen the result to update read state.') }
}

export async function listAllPersonalNotifications(options?: ApiFetchOptions) {
  const items = new Map<string, NotificationItem>()
  let offset = 0
  for (let page = 0; page < 4000; page++) {
    const result = await notificationApi.inbox(false, offset, options)
    const previousSize = items.size
    for (const item of result.items) items.set(item.id, item)
    offset += result.items.length
    if (offset >= result.total) {
      if (items.size !== result.total) throw new Error('Notification list changed while loading. Refresh to reload.')
      return [...items.values()]
    }
    if (previousSize === items.size) throw new Error('Notification pagination is incomplete.')
  }
  throw new Error('Notification pagination exceeds the supported range.')
}

function forbidden(error: unknown) { return (error as { status?: number })?.status === 403 }

async function readableNotifications() {
  try { return await listAllPersonalNotifications() }
  catch (error) {
    // Accounts without a personal inbox can still read authorized scheduler results.
    if (forbidden(error)) return []
    throw error
  }
}

async function writeNotificationReads(items: NotificationItem[], read: boolean, checkReader: () => void) {
  for (let offset = 0; offset < items.length; offset += 3) {
    checkReader()
    const results = await Promise.allSettled(items.slice(offset, offset + 3).map(item => notificationApi.read(item.id, read)))
    const failure = results.find(result => result.status === 'rejected')
    if (failure?.status === 'rejected') throw failure.reason
  }
}

// Only calls the existing, authenticated per-user endpoints. Reading never mutates a task or approval.
export async function readActivityNotification(item: NotificationItem, canReadScheduler: boolean, read = true) {
  const checkReader = readerGuard()
  const notifications = (await readableNotifications()).filter(candidate =>
    candidate.id === item.id || (item.event.run_id && candidate.event.run_id === item.event.run_id && candidate.event.task_id === item.event.task_id))
  if (!notifications.some(candidate => candidate.id === item.id)) notifications.push(item)
  if (canReadScheduler && item.event.run_id && item.event.task_id) {
    try {
      checkReader()
      const params = new URLSearchParams({ run_id: item.event.run_id, task_id: item.event.task_id, limit: '1' })
      const page = await schedulerApi.listRunInbox(`?${params}`)
      const match = page.items.find(result => result.run.scheduled_run_id === item.event.run_id && result.run.scheduled_task_id === item.event.task_id)
      checkReader()
      if (match) await schedulerApi.setRunReadState(match.run.scheduled_run_id, read)
    } catch (error) {
      if (!forbidden(error)) throw error
    }
  }
  await writeNotificationReads(notifications, read, checkReader)
}

export async function readActivityRun(runID: string, read: boolean, hasPersonalInbox: boolean) {
  const checkReader = readerGuard()
  // Read the exact authorized run before joining its personal notifications.
  if (hasPersonalInbox) {
    const page = await schedulerApi.listRunInbox(`?run_id=${encodeURIComponent(runID)}&limit=1`)
    const run = page.items.find(item => item.run.scheduled_run_id === runID)?.run
    if (run) {
      const notifications = (await readableNotifications()).filter(item => item.event.run_id === runID && item.event.task_id === run.scheduled_task_id)
      await writeNotificationReads(notifications, read, checkReader)
    }
  }
  checkReader()
  return schedulerApi.setRunReadState(runID, read)
}
