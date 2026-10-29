import type { DiscussionViewModel } from '#lib/types/discussions'
import type { MissionControlViewModel } from '#lib/types/view-models'

interface DiscussionWorkspaceContextState {
  assistantMode: boolean
  discussionId: string
  discussionTitle: string
  discussionStatus: string
  members: Array<{ id: string; name: string; role: string; kind?: string }>
  linkedMissions: DiscussionViewModel['linkedMissions']
  activeMissionId: string
  missionId: string
  missionTitle: string
  missionHref: string
  mission?: MissionControlViewModel
  missionLoading: boolean
  missionUnavailable: boolean
  missionError: string
  onMissionSelect?: (missionId: string) => void
  onMissionCreate?: () => void
}

export const discussionWorkspaceContext = $state<DiscussionWorkspaceContextState>({
  assistantMode: false,
  discussionId: '',
  discussionTitle: '',
  discussionStatus: '',
  members: [],
  linkedMissions: [],
  activeMissionId: '',
  missionId: '',
  missionTitle: '',
  missionHref: '',
  mission: undefined,
  missionLoading: false,
  missionUnavailable: false,
  missionError: '',
  onMissionSelect: undefined,
  onMissionCreate: undefined
})

export function updateDiscussionWorkspaceContext(
  discussion: DiscussionViewModel | undefined,
  mission: MissionControlViewModel | undefined,
  missionLoading: boolean,
  missionError: string,
  assistantMode = false,
  activeMissionId = '',
  onMissionSelect?: (missionId: string) => void,
  onMissionCreate?: () => void,
  missionUnavailable = false
) {
  const linkedMissions = discussion?.linkedMissions ?? []
  const linkedMission = linkedMissions.find((item) => item.id === activeMissionId) ?? linkedMissions[0]
  const selectedMission = mission?.mission.id === linkedMission?.id ? mission : undefined
  discussionWorkspaceContext.assistantMode = assistantMode
  discussionWorkspaceContext.discussionId = discussion?.id ?? ''
  discussionWorkspaceContext.discussionTitle = discussion?.title ?? ''
  discussionWorkspaceContext.discussionStatus = discussion?.status ?? ''
  discussionWorkspaceContext.members = (discussion?.members ?? []).map((member) => ({
    id: member.id,
    name: member.name,
    role: member.role,
    kind: member.kind
  }))
  discussionWorkspaceContext.linkedMissions = linkedMissions
  discussionWorkspaceContext.activeMissionId = linkedMission?.id ?? ''
  discussionWorkspaceContext.missionId = linkedMission?.id ?? ''
  discussionWorkspaceContext.missionTitle = selectedMission?.mission.title ?? linkedMission?.title ?? ''
  discussionWorkspaceContext.missionHref = linkedMission?.href ?? (linkedMission?.id ? `/missions/${encodeURIComponent(linkedMission.id)}` : '')
  discussionWorkspaceContext.mission = selectedMission
  discussionWorkspaceContext.missionLoading = missionLoading
  discussionWorkspaceContext.missionUnavailable = missionUnavailable
  discussionWorkspaceContext.missionError = missionError
  discussionWorkspaceContext.onMissionSelect = onMissionSelect
  discussionWorkspaceContext.onMissionCreate = onMissionCreate
}

export function clearDiscussionWorkspaceContext(discussionId = '') {
  if (discussionId && discussionWorkspaceContext.discussionId !== discussionId) return
  updateDiscussionWorkspaceContext(undefined, undefined, false, '', false)
}
