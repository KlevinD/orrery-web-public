import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { apiFetch } from '#lib/api/client'
import type {
  KnowledgeProviderCapabilityTestDTO,
  KnowledgeProviderDTO,
  KnowledgeProviderPageDTO,
  KnowledgeProviderRequest,
  KnowledgeProviderSyncResultDTO
} from '#lib/types/api/knowledge-provider'

export const knowledgeProviderQueryKeys = {
  all: ['knowledge-providers'] as const
}

export function useKnowledgeProvidersQuery(enabled: () => boolean = () => true) {
  return createQuery(() => ({
    queryKey: knowledgeProviderQueryKeys.all,
    queryFn: () => apiFetch<KnowledgeProviderPageDTO>('/api/knowledge-providers'),
    enabled: enabled()
  }))
}

export function useKnowledgeProviderCreateMutation() {
  const queryClient = useQueryClient()
  return createMutation(() => ({
    mutationFn: (body: KnowledgeProviderRequest) =>
      apiFetch<KnowledgeProviderDTO>('/api/knowledge-providers', jsonMutation('POST', body)),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: knowledgeProviderQueryKeys.all })
  }))
}

export function useKnowledgeProviderTestMutation() {
  return createMutation(() => ({
    mutationFn: (id: string) =>
      apiFetch<KnowledgeProviderCapabilityTestDTO>(`/api/knowledge-providers/${encodeURIComponent(id)}/test`, { method: 'POST' })
  }))
}

export function useKnowledgeProviderSyncMutation() {
  const queryClient = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ id, ownerId }: { id: string; ownerId: string }) =>
      apiFetch<KnowledgeProviderSyncResultDTO>(
        `/api/knowledge-providers/${encodeURIComponent(id)}/sync`,
        jsonMutation('POST', { owner_id: ownerId })
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: knowledgeProviderQueryKeys.all })
  }))
}

function jsonMutation(method: string, body: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  }
}
