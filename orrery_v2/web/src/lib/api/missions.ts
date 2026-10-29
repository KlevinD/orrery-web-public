import { apiFetch, type ApiFetchOptions } from './client'

export const missionApi = {
  listMissions: (query = '', options?: ApiFetchOptions) => apiFetch(`/api/missions${query}`, options),
  getMissionControl: (missionId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/control`, options),
  getMissionOps: (missionId: string, options?: ApiFetchOptions) => apiFetch(`/api/missions/${missionId}/ops`, options),
  getMissionTeam: (missionId: string, options?: ApiFetchOptions) => apiFetch(`/api/missions/${missionId}/team`, options),
  getMissionTasks: (missionId: string, options?: ApiFetchOptions) => apiFetch(`/api/missions/${missionId}/tasks`, options),
  getMissionProgress: (missionId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/progress`, options),
  getMissionEvidence: (missionId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/evidence`, options),
  getMissionQA: (missionId: string, options?: ApiFetchOptions) => apiFetch(`/api/missions/${missionId}/qa`, options),
  getMissionDiscussion: (missionId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/discussion`, options),
  createMission: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch('/api/missions', jsonMutation('POST', body, options)),
  deleteMission: (missionId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}`, { ...options, method: 'DELETE' }),
  createTask: (missionId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/tasks`, jsonMutation('POST', body, options)),
  startAgentRun: (missionId: string, taskId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/tasks/${taskId}/agent-runs`, jsonMutation('POST', body, options)),
  cancelAgentRun: (missionId: string, runId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/agent-runs/${runId}/cancel`, jsonMutation('POST', body, options)),
  retryAgentRun: (missionId: string, runId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/agent-runs/${runId}/retry`, jsonMutation('POST', {}, options)),
  assignTask: (missionId: string, taskId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/tasks/${taskId}/assign`, jsonMutation('POST', body, options)),
  submitTaskResult: (missionId: string, taskId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/tasks/${taskId}/submit-result`, jsonMutation('POST', body, options)),
  submitQADecision: (missionId: string, qaId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/qa/${qaId}/decision`, jsonMutation('POST', body, options)),
  postDiscussionMessage: (missionId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/discussion/messages`, jsonMutation('POST', body, options)),
  replyDiscussionMessage: (missionId: string, messageId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/missions/${missionId}/discussion/messages/${messageId}/reply`, jsonMutation('POST', body, options))
}

function jsonMutation(method: string, body: unknown, options: ApiFetchOptions = {}): ApiFetchOptions {
  return { ...options, method, body: JSON.stringify(body) }
}
