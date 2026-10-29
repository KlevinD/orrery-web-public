import { describe, expect, it } from 'vitest'
import { withinHistoryWindow } from './history-window'
import { buildWorkInbox, filterProcessedWorkHistory } from './work-inbox'
import { buildMissionHistoryList, filterCompletedMissionHistory } from '#lib/components/layout/mission-history-list'
import type { MissionListItemViewModel } from '#lib/types/view-models/mission'
import type { TodoItemDTO } from '#lib/types/todo'

const now = new Date(2026, 8, 5, 14).getTime()
const at = (day: number, hour = 0) => new Date(2026, 8, day, hour).toISOString()

describe('processed history windows', () => {
  it('uses inclusive local calendar boundaries, not rolling hours', () => {
    expect(withinHistoryWindow(at(5), 'today', now)).toBe(true)
    expect(withinHistoryWindow(at(4, 23), 'today', now)).toBe(false)
    expect(withinHistoryWindow(at(-1), '7d', now)).toBe(true)
    expect(withinHistoryWindow(at(-2, 23), '7d', now)).toBe(false)
    expect(withinHistoryWindow(at(-24), '30d', now)).toBe(true)
    expect(withinHistoryWindow(at(-25, 23), '30d', now)).toBe(false)
    expect(withinHistoryWindow(at(5), 'today', new Date(2026, 8, 6).getTime())).toBe(false)
  })
  it('preserves unknown dates and restores all history', () => {
    for (const value of [undefined, '', 'invalid']) expect(withinHistoryWindow(value, 'today', now)).toBe(true)
    expect(withinHistoryWindow(at(-100), 'all', now)).toBe(true)
  })
  it('hides only processed work without mutating source items or counts', () => {
    const todos = ['done', 'canceled', 'inbox'].map(status => ({ todo_id: status, title: status, status, created_at: at(1) } as TodoItemDTO))
    const model = buildWorkInbox(todos, undefined, [])
    const completed = model.done[0]
    const items = [...model.done, ...model.pending, { ...completed, id: 'unread', unread: true }, { ...completed, id: 'attention', attention: true }, { ...completed, id: 'today', date: at(5) }]
    expect(filterProcessedWorkHistory(items, 'today', now).map(item => item.id)).toEqual(['todo:inbox', 'unread', 'attention', 'today'])
    expect(filterProcessedWorkHistory(items, 'all', now)).toEqual(items)
    expect(model.done).toHaveLength(2)
    expect(model.pending).toHaveLength(1)
  })
  it('keeps unresolved Missions, active work and unknown dates visible', () => {
    const missions = ['completed', 'cancelled', 'blocked', 'failed', 'active'].map(status => ({ id: status, title: status, status, updatedAt: 'localized date', updatedAtRaw: at(1) } as MissionListItemViewModel))
    const items = buildMissionHistoryList(missions, [], [], [])
    const completed = items[0]
    items.push({ ...completed, missionId: 'today', updatedAtRaw: at(5) }, { ...completed, missionId: 'unknown', updatedAtRaw: undefined }, { ...completed, missionId: 'tasks', activeTaskCount: 1 }, { ...completed, missionId: 'runs', activeRunCount: 1 }, { ...completed, missionId: 'attention', state: 'attention' })
    expect(filterCompletedMissionHistory(items, 'today', now).map(item => item.missionId)).toEqual(['blocked', 'failed', 'active', 'today', 'unknown', 'tasks', 'runs', 'attention'])
    expect(filterCompletedMissionHistory(items, 'all', now)).toEqual(items)
  })
})
