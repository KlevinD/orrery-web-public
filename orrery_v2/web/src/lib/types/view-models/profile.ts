export type PersonalAssistantHostingModeViewModel = 'orrery_managed' | 'owned_agent'

export interface PersonalAssistantWorkspaceGrantViewModel {
  id: string
  displayName: string
  capabilities: string[]
  available: boolean
}

export interface PersonalAssistantOwnedAgentViewModel {
  id: string
  deviceId: string
  deviceName: string
  displayName: string
  runtimeKind: string
  capabilities: string[]
  workspaceGrants: PersonalAssistantWorkspaceGrantViewModel[]
  status: string
  lastSeenAt: string
}

export interface PersonalAssistantProfileViewModel {
  agentProfileId: string
  displayName: string
  systemPrompt: string
  enabled: boolean
  hostingMode: PersonalAssistantHostingModeViewModel
  ownedAgentId: string
  workspaceGrantId: string
  fallbackToManaged: boolean
  avatarUrl: string
  avatarETag: string
  executionIdentity: string
  cloudFallback: boolean
  revision: number
  updatedAt: string
  ownedAgents: PersonalAssistantOwnedAgentViewModel[]
}
