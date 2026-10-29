import { beforeEach, describe, expect, it, vi } from 'vitest'
import { notificationApi, type NotificationItem } from './notifications'
import { schedulerApi } from './scheduler'
import { readActivityNotification, readActivityRun, listAllPersonalNotifications } from './work-activity'
import { session } from '#lib/stores/session.svelte'
const note = (id: string, task = 'task-1', run = 'run-1') => ({ id, read: false, event: { task_id: task, run_id: run } } as NotificationItem)
beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(notificationApi, 'inbox').mockResolvedValue({ items: [note('mine'), note('different-task', 'task-2'), note('different-run', 'task-1', 'run-2')], total: 3, unread: 3, offset: 0, limit: 25 })
  vi.spyOn(notificationApi, 'read').mockResolvedValue({})
  vi.spyOn(schedulerApi, 'listRunInbox').mockResolvedValue({ items: [{ run: { scheduled_task_id: 'task-1', scheduled_run_id: 'run-1' } }], total: 1 } as any)
  vi.spyOn(schedulerApi, 'setRunReadState').mockResolvedValue({ scheduled_run_id: 'run-1', is_read: true })
})
describe('personal result read synchronization', () => {
  it('reads a notification without requesting scheduler access for a notification-only user', async () => {
    await readActivityNotification(note('mine'), false)
    expect(notificationApi.read).toHaveBeenCalledTimes(1)
    expect(notificationApi.read).toHaveBeenCalledWith('mine', true)
    expect(schedulerApi.listRunInbox).not.toHaveBeenCalled()
    expect(schedulerApi.setRunReadState).not.toHaveBeenCalled()
  })
  it('updates both receipts from either entry and only joins exact task/run matches', async () => {
    await readActivityNotification(note('mine'), true)
    expect(schedulerApi.setRunReadState).toHaveBeenCalledWith('run-1', true)
    expect(notificationApi.read).toHaveBeenCalledTimes(1)
    expect(notificationApi.read).toHaveBeenCalledWith('mine', true)
    vi.mocked(notificationApi.read).mockClear()
    await readActivityRun('run-1', false, true)
    expect(notificationApi.read).toHaveBeenCalledTimes(1)
    expect(notificationApi.read).toHaveBeenCalledWith('mine', false)
    expect(schedulerApi.setRunReadState).toHaveBeenLastCalledWith('run-1', false)
  })
  it('retains the personal grant when the scheduler result is outside the authorized scope', async () => {
    vi.mocked(schedulerApi.listRunInbox).mockResolvedValue({ items: [], total: 0 } as any)
    await readActivityNotification(note('mine'), true)
    expect(notificationApi.read).toHaveBeenCalledWith('mine', true)
    expect(schedulerApi.setRunReadState).not.toHaveBeenCalled()
  })
  it('reports partial synchronization failure and lets the same read be retried', async () => {
    vi.mocked(notificationApi.read).mockRejectedValueOnce(new Error('offline'))
    await expect(readActivityRun('run-1', true, true)).rejects.toThrow('offline')
    expect(schedulerApi.setRunReadState).not.toHaveBeenCalled()
    await readActivityRun('run-1', true, true)
    expect(schedulerApi.setRunReadState).toHaveBeenCalledWith('run-1', true)
  })
  it('does not update another login after the reader changes during loading', async () => {
    vi.mocked(notificationApi.inbox).mockImplementationOnce(async () => {
      session.securityContextVersion += 1
      return { items: [note('mine')], total: 1, unread: 1, offset: 0, limit: 25 }
    })
    await expect(readActivityNotification(note('mine'), false)).rejects.toThrow('Session changed')
    expect(notificationApi.read).not.toHaveBeenCalled()
  })
  it('loads all pages before changing read state and rejects incomplete pagination', async () => {
    vi.mocked(notificationApi.inbox).mockResolvedValueOnce({ items: [note('one')], total: 2, unread: 2, offset: 0, limit: 25 }).mockResolvedValueOnce({ items: [note('two')], total: 2, unread: 2, offset: 1, limit: 25 })
    expect(await listAllPersonalNotifications()).toHaveLength(2)
    expect(notificationApi.inbox).toHaveBeenNthCalledWith(2, false, 1, undefined)
    vi.mocked(notificationApi.inbox).mockResolvedValue({ items: [], total: 1, unread: 1, offset: 0, limit: 25 })
    await expect(listAllPersonalNotifications()).rejects.toThrow('incomplete')
  })
})
