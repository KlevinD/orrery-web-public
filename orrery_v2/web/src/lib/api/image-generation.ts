import { apiFetch } from './client'
import type { ImageConnectionTestDTO, ImageGenerationSettingsDTO } from '#lib/types/api/image-generation'

export const imageGenerationApi = {
  getSettings: () => apiFetch<ImageGenerationSettingsDTO>('/api/settings/image-generation'),
  saveSettings: (body: ImageGenerationSettingsDTO) => apiFetch<ImageGenerationSettingsDTO>('/api/settings/image-generation', {
    method: 'PUT', body: JSON.stringify(body),
  }),
  testConnection: () => apiFetch<ImageConnectionTestDTO>('/api/settings/image-generation/test', { method: 'POST', body: '{}' }),
}
