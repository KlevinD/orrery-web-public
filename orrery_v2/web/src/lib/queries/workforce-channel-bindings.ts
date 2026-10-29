import { createMutation, createQuery, useQueryClient, type QueryClient } from '@tanstack/svelte-query'
import { workforceChannelBindingsApi } from '#lib/api/workforce-channel-bindings'
import { mapWorkforceChannelBindingsView } from '#lib/mappers/workforce-channel-bindings'
import type { WorkforceChannelAccountViewModel } from '#lib/types/view-models'

export const workforceChannelBindingsQueryKeys = {
  all: ['workforce-channel-bindings'] as const,
  detail: (agentProfileId: string) => ['workforce-channel-bindings', agentProfileId] as const
}

export function workforceChannelBindingsQueryOptions(agentProfileId: string) {
  const normalized = agentProfileId.trim()
  return {
    queryKey: workforceChannelBindingsQueryKeys.detail(normalized),
    queryFn: async () => mapWorkforceChannelBindingsView(await workforceChannelBindingsApi.list(normalized)),
    enabled: Boolean(normalized)
  }
}

export function useWorkforceChannelBindingsQuery(agentProfileId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => ({
    ...workforceChannelBindingsQueryOptions(agentProfileId()),
    enabled: enabled() && Boolean(agentProfileId().trim())
  }))
}

export interface PutWorkforceChannelBindingInput {
  targetAgentProfileId: string
  account: WorkforceChannelAccountViewModel
  inboundEnabled: boolean
}

export interface DeleteWorkforceChannelBindingInput {
  targetAgentProfileId: string
  account: WorkforceChannelAccountViewModel
}

export async function invalidateWorkforceChannelBindingOwners(client: QueryClient, targetAgentProfileId: string, previousOwnerAgentProfileId = '') {
  const owners = new Set([targetAgentProfileId.trim(), previousOwnerAgentProfileId.trim()])
  await Promise.all(Array.from(owners).filter(Boolean).map((owner) =>
    client.invalidateQueries({ queryKey: workforceChannelBindingsQueryKeys.detail(owner) })
  ))
}

export function usePutWorkforceChannelBindingMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ targetAgentProfileId, account, inboundEnabled }: PutWorkforceChannelBindingInput) =>
      workforceChannelBindingsApi.put(targetAgentProfileId, account.accountId, {
        inbound_enabled: inboundEnabled,
        expected_revision: account.revision,
        expected_owner_agent_profile_id: account.ownerAgentProfileId
      }),
    onSuccess: async (_result, variables) => {
      await invalidateWorkforceChannelBindingOwners(client, variables.targetAgentProfileId, variables.account.ownerAgentProfileId)
    }
  }))
}

export function useDeleteWorkforceChannelBindingMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ targetAgentProfileId, account }: DeleteWorkforceChannelBindingInput) =>
      workforceChannelBindingsApi.delete(targetAgentProfileId, account.accountId, {
        expected_revision: account.revision,
        expected_owner_agent_profile_id: account.ownerAgentProfileId
      }),
    onSuccess: async (_result, variables) => {
      await invalidateWorkforceChannelBindingOwners(client, variables.targetAgentProfileId, variables.account.ownerAgentProfileId)
    }
  }))
}
