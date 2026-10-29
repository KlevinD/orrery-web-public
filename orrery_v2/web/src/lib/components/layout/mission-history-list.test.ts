import { describe, expect, it } from 'vitest'
import type { ApprovalItemViewModel, HILDecisionItemViewModel, MissionListItemViewModel } from '#lib/types/view-models'
import { buildMissionHistoryList, buildMissionHistorySections } from './mission-history-list'

describe('Mission history list', () => {
  it('keeps visible Missions in the list and prioritizes work that needs the viewer', () => {
    const items = buildMissionHistoryList(
      [
        mission('mission-running', 'Security scan', 'active', 2, 1, 'conversation-running'),
        mission('mission-attention', 'Release review', 'active', 1, 0, 'conversation/attention'),
        mission('mission-complete', 'Completed migration', 'completed', 0, 0)
      ],
      [
        approval('approval-mine', 'mission-attention', 'operator', 'Review evidence'),
        approval('approval-other', 'mission-running', 'someone-else', 'Private approval')
      ],
      [council('council-decision', 'mission-attention')],
      ['operator', 'operator-1']
    )

    expect(items.map((item) => [item.missionId, item.state])).toEqual([
      ['mission-attention', 'attention'],
      ['mission-running', 'running'],
      ['mission-complete', 'idle']
    ])
    expect(items[0]).toMatchObject({
      total: 2,
      approvalCount: 1,
      councilCount: 1,
      conversationHref: '/discussions/conversation%2Fattention',
      primaryHref: '/missions/mission-attention'
    })
    expect(items[1].primaryHref).toBe('/missions/mission-running')
    expect(items[1].lifecycleStatusVisible).toBe(false)
    expect(items[2].lifecycleStatusVisible).toBe(true)
    expect(buildMissionHistorySections(items).map((section) => section.key)).toEqual(['needsYou', 'running', 'recent'])
  })

  it('does not spin for an active Mission when all runtime sessions are terminal', () => {
    const items = buildMissionHistoryList(
      [mission('mission-stopped', 'Security audit', 'active', 3, 0)],
      [],
      [],
      []
    )

    expect(items[0]).toMatchObject({
      missionId: 'mission-stopped',
      status: 'active',
      activeTaskCount: 3,
      activeRunCount: 0,
      state: 'idle',
      lifecycleStatusVisible: false
    })
  })

  it('keeps fixed HIL decisions even when the Mission is absent from the catalog page', () => {
    const fixed = approval('fixed-hil', 'mission-protected', 'operator', 'Confirm protected action')
    fixed.sourceLabel = 'hil:hil'

    const items = buildMissionHistoryList([], [fixed], [], [])

    expect(items).toHaveLength(1)
    expect(items[0]).toMatchObject({
      missionId: 'mission-protected',
      title: 'mission-protected',
      state: 'attention',
      hilCount: 1,
      primaryHref: '/work-items?view=inbox&action=approval%3Afixed-hil'
    })
  })
})

function mission(id: string, title: string, status: string, activeTaskCount: number, activeRunCount: number, conversationId = '') {
  return { id, title, status, activeTaskCount, activeRunCount, conversationId, updatedAt: 'Sep 01, 18:00' } as MissionListItemViewModel
}

function approval(id: string, missionId: string, assignedTo: string, title: string) {
  return {
    id,
    missionId,
    assignedTo,
    title,
    sourceLabel: 'task:task-1',
    createdAtRaw: '2026-09-01T10:00:00Z'
  } as ApprovalItemViewModel
}

function council(id: string, missionId: string) {
  return {
    id,
    councilId: 'council-1',
    resource: `mission:${missionId}`,
    purpose: 'Approve security findings',
    action: 'mission.security.review',
    createdAtRaw: '2026-09-01T11:00:00Z',
    payloadPreview: {}
  } as HILDecisionItemViewModel
}
