import type { RuntimeDiscoveryCandidateViewModel } from '#lib/types/view-models'
import type { SettingsRuntimeProfileViewModel } from '#lib/types/view-models'

export type RuntimeConfigValue = string | string[]
export type RuntimeConfig = Record<string, RuntimeConfigValue>

export function cloneRuntimeConfig(config: RuntimeConfig): RuntimeConfig {
  return Object.fromEntries(
    Object.entries(config).map(([key, value]) => [key, Array.isArray(value) ? [...value] : value])
  )
}

export function runtimeConfigToFormValues(config: RuntimeConfig): Record<string, string> {
  return Object.fromEntries(
    Object.entries(config).map(([key, value]) => [key, Array.isArray(value) ? value.join(' ') : value])
  )
}

export function stringRuntimeConfigValue(value: RuntimeConfigValue | undefined): string {
  return Array.isArray(value) ? value.join(' ') : value ?? ''
}

export function runtimeProfileIDFromDiscovery(candidate: RuntimeDiscoveryCandidateViewModel): string {
  const base = (candidate.configRef || `runtime-${candidate.kind}-local`)
    .replace(/[^a-zA-Z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return base || `runtime-${candidate.kind}-local`
}

export function runtimeProfileCanSubmit(profile: SettingsRuntimeProfileViewModel | undefined): boolean {
  if (!profile) return false
  const kind = cleanRuntimeText(profile.runtimeKind)
  const mode = cleanRuntimeText(stringRuntimeConfigValue(profile.runtimeConfig.mode)) || 'local'
  if (!kind || !cleanRuntimeText(profile.displayName)) return false
  if (kind === 'claude' && mode === 'agent_sdk') {
    return cleanRuntimeText(stringRuntimeConfigValue(profile.runtimeConfig.approval_timeout_ms)).length > 0
  }
  return cleanRuntimeText(stringRuntimeConfigValue(profile.runtimeConfig.executable)).length > 0
}

function cleanRuntimeText(value: string | undefined | null): string {
  return String(value ?? '').trim()
}
