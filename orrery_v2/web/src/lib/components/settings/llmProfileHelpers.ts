import type { SettingsLLMProfileViewModel } from '#lib/types/view-models'
import { llmProviderDefaults } from './settingsConfig'

export type LLMProviderLabeler = (provider: string) => string

export function createLLMProfile(
  provider: string,
  profileId: string,
  providerLabel: LLMProviderLabeler
): SettingsLLMProfileViewModel {
  const defaults = llmProviderDefaults[provider] ?? {}
  return {
    profileId,
    displayName: providerLabel(provider),
    provider,
    baseUrl: defaults.baseUrl ?? '',
    apiKey: '',
    apiKeyRef: '',
    model: defaults.model ?? '',
    temperature: defaults.temperature ?? '0.2',
    contextLength: defaults.contextLength ?? '16384',
    enabled: true,
    globalDefault: false
  }
}

export function updateLLMProvider(
  profile: SettingsLLMProfileViewModel,
  provider: string,
  providerLabel: LLMProviderLabeler
) {
  const previousLabel = providerLabel(profile.provider)
  const previousDefaults = llmProviderDefaults[profile.provider] ?? {}
  const defaults = llmProviderDefaults[provider] ?? {}
  profile.provider = provider
  if (!profile.displayName || profile.displayName === previousLabel) {
    profile.displayName = providerLabel(provider)
  }
  if (!profile.baseUrl || profile.baseUrl === previousDefaults.baseUrl) profile.baseUrl = defaults.baseUrl ?? ''
  if (!profile.model || profile.model === previousDefaults.model) profile.model = defaults.model ?? ''
  if (!profile.temperature || profile.temperature === previousDefaults.temperature) profile.temperature = defaults.temperature ?? '0.2'
  if (!profile.contextLength || profile.contextLength === previousDefaults.contextLength) profile.contextLength = defaults.contextLength ?? '16384'
}

export function normalizeLLMProfile(
  profile: SettingsLLMProfileViewModel,
  nextProfileId: (provider: string) => string,
  providerLabel: LLMProviderLabeler
): SettingsLLMProfileViewModel {
  const provider = cleanLLMText(profile.provider) || 'openai'
  return {
    ...profile,
    profileId: cleanLLMText(profile.profileId) || nextProfileId(provider),
    displayName: cleanLLMText(profile.displayName) || providerLabel(provider),
    provider,
    baseUrl: cleanLLMText(profile.baseUrl),
    apiKey: '',
    apiKeyRef: cleanLLMText(profile.apiKeyRef),
    model: cleanLLMText(profile.model),
    temperature: cleanLLMText(profile.temperature),
    contextLength: cleanLLMText(profile.contextLength),
    enabled: profile.globalDefault ? true : profile.enabled,
    globalDefault: profile.globalDefault
  }
}

export function normalizeLLMProfileDefaults(profiles: SettingsLLMProfileViewModel[]) {
  const defaultIndex = profiles.findLastIndex((profile) => profile.enabled && profile.globalDefault)
  return profiles.map((profile, index) => ({
    ...profile,
    globalDefault: index === defaultIndex
  }))
}

export function cleanLLMText(value: string | number | undefined | null): string {
  return String(value ?? '').trim()
}
