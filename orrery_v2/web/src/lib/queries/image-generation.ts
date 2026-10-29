import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { imageGenerationApi } from '#lib/api/image-generation'

const imageSettingsKey = ['settings', 'image-generation'] as const

export function useImageGenerationSettingsQuery() {
  return createQuery(() => ({ queryKey: imageSettingsKey, queryFn: imageGenerationApi.getSettings, refetchOnWindowFocus: false }))
}

export function useImageGenerationSettingsMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: imageGenerationApi.saveSettings,
    onSuccess: (data) => client.setQueryData(imageSettingsKey, data),
  }))
}

export function useImageGenerationConnectionTestMutation() {
  return createMutation(() => ({ mutationFn: imageGenerationApi.testConnection }))
}
