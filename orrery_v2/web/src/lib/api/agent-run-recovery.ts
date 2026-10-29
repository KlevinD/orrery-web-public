import { apiFetch } from './client'
import type { components } from '#lib/types/api.generated'

export type AgentRunRecovery = components['schemas']['AgentRunRecoveryDTO']
export type AgentRunRecoveryReviewInput = components['schemas']['AgentRunRecoveryReviewInput']
export type AgentRunRecoveryReview = components['schemas']['AgentRunRecoveryReviewDTO']

function recoveryPath(missionId: string, runId: string) {
  return `/api/missions/${encodeURIComponent(missionId)}/agent-runs/${encodeURIComponent(runId)}/recovery`
}

export const agentRunRecoveryApi = {
  get: (missionId: string, runId: string, signal?: AbortSignal) =>
    apiFetch<AgentRunRecovery>(recoveryPath(missionId, runId), { signal, cache: 'no-store' }),
  review: (missionId: string, runId: string, body: AgentRunRecoveryReviewInput, idempotencyKey: string) =>
    apiFetch<AgentRunRecoveryReview>(`${recoveryPath(missionId, runId)}/reviews`, {
      method: 'POST', body: JSON.stringify(body), idempotencyKey
    })
}
