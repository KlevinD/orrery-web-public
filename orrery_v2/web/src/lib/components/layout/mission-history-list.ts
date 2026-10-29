import { workActionHref } from '#lib/components/workhub/work-actions'
import { hilDecisionMissionId } from '#lib/mappers/hil'
import { withinHistoryWindow, type HistoryWindow } from '#lib/queries/history-window'
import type { MissionListItemViewModel } from '#lib/types/view-models/mission'
import type { ApprovalItemViewModel, HILDecisionItemViewModel } from '#lib/types/view-models/operations'

export type MissionDecisionKind = 'hil' | 'approval' | 'council'
export type MissionHistoryState = 'attention' | 'running' | 'idle'
export type MissionHistorySectionKey = 'needsYou' | 'running' | 'recent'

export interface MissionDecisionListItem {
  id: string
  kind: MissionDecisionKind
  title: string
  href: string
  createdAtRaw: string
}

export interface MissionHistoryItem {
  missionId: string
  title: string
  status: string
  state: MissionHistoryState
  lifecycleStatusVisible: boolean
  missionHref: string
  conversationHref: string
  primaryHref: string
  decisionHref: string
  latestDecisionTitle: string
  latestAt: string
  updatedAt: string
  updatedAtRaw?: string
  activeTaskCount: number
  activeRunCount: number
  deliveryPercent: number
  total: number
  hilCount: number
  approvalCount: number
  councilCount: number
  decisions: MissionDecisionListItem[]
}

export interface MissionHistorySection {
  key: MissionHistorySectionKey
  missions: MissionHistoryItem[]
}

export function buildMissionHistoryList(
  missions: readonly MissionListItemViewModel[],
  approvals: readonly ApprovalItemViewModel[],
  councils: readonly HILDecisionItemViewModel[],
  viewerIdentityIds: readonly string[]
): MissionHistoryItem[] {
  const missionById = new Map(missions.map((mission) => [mission.id, mission]))
  const missionOrder = new Map(missions.map((mission, index) => [mission.id, index]))
  const viewerIds = new Set(viewerIdentityIds.map(normalize).filter(Boolean))
  const decisionsByMission = new Map<string, MissionDecisionListItem[]>()

  for (const approval of approvals) {
    const missionId = validMissionId(approval.missionId)
    if (!missionId) continue
    const fixedHIL = approval.sourceLabel.startsWith('hil:')
    if (!fixedHIL && !viewerIds.has(normalize(approval.assignedTo))) continue
    const kind: MissionDecisionKind = approval.sourceLabel === 'hil:hil' ? 'hil' : 'approval'
    appendDecision(decisionsByMission, missionId, {
      id: approval.id,
      kind,
      title: approval.title,
      href: workActionHref(`approval:${approval.hilDecisionId || approval.id}`),
      createdAtRaw: approval.createdAtRaw
    })
  }

  for (const council of councils) {
    const missionId = validMissionId(hilDecisionMissionId(council))
    if (!missionId) continue
    appendDecision(decisionsByMission, missionId, {
      id: council.id,
      kind: 'council',
      title: council.purpose || council.action,
      href: workActionHref(`council:${council.councilId}`),
      createdAtRaw: council.createdAtRaw
    })
  }

  const missionIds = [...missionById.keys()]
  for (const missionId of decisionsByMission.keys()) {
    if (!missionById.has(missionId)) missionIds.push(missionId)
  }

  return missionIds
    .map((missionId) => {
      const mission = missionById.get(missionId)
      const decisions = (decisionsByMission.get(missionId) ?? [])
        .toSorted((left, right) => timestamp(right.createdAtRaw) - timestamp(left.createdAtRaw))
      const missionHref = `/missions/${encodeURIComponent(missionId)}`
      const conversationHref = mission?.conversationId
        ? `/discussions/${encodeURIComponent(mission.conversationId)}`
        : ''
      const decisionHref = decisions[0]?.href ?? ''
      const state: MissionHistoryState = decisions.length > 0
        ? 'attention'
        : missionIsRunning(mission)
          ? 'running'
          : 'idle'
      return {
        missionId,
        title: mission?.title || missionId,
        status: mission?.status || '',
        state,
        lifecycleStatusVisible: state === 'idle' && missionLifecycleStatusIsTerminal(mission?.status),
        missionHref,
        conversationHref,
        primaryHref: mission ? missionHref : decisionHref || missionHref,
        decisionHref,
        latestDecisionTitle: decisions[0]?.title ?? '',
        latestAt: decisions[0]?.createdAtRaw ?? '',
        updatedAt: mission?.updatedAt ?? '',
        updatedAtRaw: mission?.updatedAtRaw,
        activeTaskCount: mission?.activeTaskCount ?? 0,
        activeRunCount: mission?.activeRunCount ?? 0,
        deliveryPercent: mission?.deliveryPercent ?? 0,
        total: decisions.length,
        hilCount: decisions.filter((item) => item.kind === 'hil').length,
        approvalCount: decisions.filter((item) => item.kind === 'approval').length,
        councilCount: decisions.filter((item) => item.kind === 'council').length,
        decisions
      }
    })
    .sort((left, right) => {
      const stateDifference = stateRank(left.state) - stateRank(right.state)
      if (stateDifference !== 0) return stateDifference
      if (left.state === 'attention') {
        const timeDifference = timestamp(right.latestAt) - timestamp(left.latestAt)
        if (timeDifference !== 0) return timeDifference
      }
      return (missionOrder.get(left.missionId) ?? Number.MAX_SAFE_INTEGER)
        - (missionOrder.get(right.missionId) ?? Number.MAX_SAFE_INTEGER)
    })
}

export function filterCompletedMissionHistory(items: readonly MissionHistoryItem[], range: HistoryWindow, now: number): MissionHistoryItem[] {
  return items.filter(item => item.state !== 'idle' || item.activeTaskCount > 0 || item.activeRunCount > 0
    || !['completed', 'done', 'approved', 'succeeded', 'cancelled', 'canceled'].includes(item.status.trim().toLowerCase())
    || withinHistoryWindow(item.updatedAtRaw, range, now))
}

export function buildMissionHistorySections(items: readonly MissionHistoryItem[]): MissionHistorySection[] {
  const sections: MissionHistorySection[] = [
    { key: 'needsYou', missions: items.filter((item) => item.state === 'attention') },
    { key: 'running', missions: items.filter((item) => item.state === 'running') },
    { key: 'recent', missions: items.filter((item) => item.state === 'idle') }
  ]
  return sections.filter((section) => section.missions.length > 0)
}

function missionIsRunning(mission: MissionListItemViewModel | undefined) {
  return (mission?.activeRunCount ?? 0) > 0
}

function missionLifecycleStatusIsTerminal(status: string | undefined) {
  return ['blocked', 'completed', 'done', 'approved', 'succeeded', 'cancelled', 'canceled', 'failed']
    .includes((status ?? '').trim().toLowerCase())
}

function appendDecision(
  decisionsByMission: Map<string, MissionDecisionListItem[]>,
  missionId: string,
  decision: MissionDecisionListItem
) {
  decisionsByMission.set(missionId, [...(decisionsByMission.get(missionId) ?? []), decision])
}

function validMissionId(value: string) {
  const normalized = value.trim()
  return normalized && normalized !== 'Not provided' ? normalized : ''
}

function stateRank(state: MissionHistoryState) {
  if (state === 'attention') return 0
  if (state === 'running') return 1
  return 2
}

function normalize(value: string) {
  return value.trim().toLowerCase()
}

function timestamp(value: string) {
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? parsed : 0
}
