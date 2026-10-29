import { createQuery } from '@tanstack/svelte-query'
import { profileApi } from '#lib/api/profile'
import { mapPersonalAssistantProfileView } from '#lib/mappers/profile'
import type { PersonalAssistantProfileDTO } from '#lib/types/api'

export const profileQueryKeys = {
  personalAssistant: ['profile', 'personal-assistant'] as const
}

export function usePersonalAssistantQuery(enabled: () => boolean = () => true) {
  return createQuery(() => ({
    queryKey: profileQueryKeys.personalAssistant,
    queryFn: async () => mapPersonalAssistantProfileView(
      await profileApi.getPersonalAssistant() as PersonalAssistantProfileDTO
    ),
    enabled: enabled()
  }))
}
