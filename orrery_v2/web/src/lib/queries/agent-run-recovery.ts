import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { agentRunRecoveryApi, type AgentRunRecoveryReviewInput } from '#lib/api/agent-run-recovery'
import { session } from '#lib/stores/session.svelte'

export const recoveryQueryKey = (missionId: string, runId: string) =>
  ['agent-run-recovery', session.securityContextVersion, session.actorId, ...session.permissionScope, missionId, runId] as const

export function useAgentRunRecoveryQuery(missionId: () => string, runId: () => string, enabled: () => boolean, active: () => boolean) {
  return createQuery(() => ({
    queryKey: recoveryQueryKey(missionId(), runId()),
    queryFn: ({ signal }) => agentRunRecoveryApi.get(missionId(), runId(), signal),
    enabled: enabled() && Boolean(missionId() && runId()),
    gcTime: 0,
    staleTime: 0,
    retry: false,
    refetchInterval: enabled() && active() ? 5000 : false
  }))
}

export function useAgentRunRecoveryReviewMutation(missionId: () => string, runId: () => string) {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (command: { body: AgentRunRecoveryReviewInput; idempotencyKey: string }) =>
      agentRunRecoveryApi.review(missionId(), runId(), command.body, command.idempotencyKey),
    onSuccess: () => client.invalidateQueries({ queryKey: recoveryQueryKey(missionId(), runId()) })
  }))
}
