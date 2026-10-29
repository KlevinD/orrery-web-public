import type { PersonalAssistantProfileDTO } from '#lib/types/api'
import type { PersonalAssistantProfileViewModel } from '#lib/types/view-models'

export function mapPersonalAssistantProfileView(dto: PersonalAssistantProfileDTO): PersonalAssistantProfileViewModel {
  return {
    agentProfileId: dto.agent_profile_id?.trim() ?? '',
    displayName: dto.display_name,
    systemPrompt: dto.system_prompt ?? '',
    enabled: dto.assistant_enabled,
    hostingMode: dto.hosting_mode,
    ownedAgentId: dto.owned_agent_id ?? '',
    workspaceGrantId: dto.workspace_grant_id ?? '',
    fallbackToManaged: dto.fallback_to_managed,
    avatarUrl: dto.avatar_url ?? '',
    avatarETag: dto.avatar_etag ?? '',
    executionIdentity: dto.execution_identity || 'user',
    cloudFallback: dto.cloud_fallback,
    revision: dto.revision,
    updatedAt: dto.updated_at ?? '',
    ownedAgents: dto.owned_agents.map((agent) => ({
      id: agent.owned_agent_id,
      deviceId: agent.device_id,
      deviceName: agent.device_name,
      displayName: agent.display_name,
      runtimeKind: agent.runtime_kind,
      capabilities: agent.capabilities,
      workspaceGrants: agent.workspace_grants.map((grant) => ({
        id: grant.grant_id,
        displayName: grant.display_name,
        capabilities: grant.capabilities,
        available: grant.available
      })),
      status: agent.status,
      lastSeenAt: agent.last_seen_at
    }))
  }
}
