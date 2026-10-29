import type { WorkforceChannelBindingsDTO } from '#lib/types/api'
import type { WorkforceChannelAccountViewModel, WorkforceChannelBindingsViewModel } from '#lib/types/view-models'

export function mapWorkforceChannelBindingsView(dto: WorkforceChannelBindingsDTO): WorkforceChannelBindingsViewModel {
  const agentProfileId = dto.agent_profile_id?.trim() ?? ''
  return {
    agentProfileId,
    accounts: (dto.accounts ?? [])
      .map((account): WorkforceChannelAccountViewModel => ({
        key: `${account.channel_key}:${account.account_id}`,
        channelKey: account.channel_key,
        accountId: account.account_id,
        enabled: account.enabled,
        status: account.status,
        statusTone: channelStatusTone(account.status),
        ownerAgentProfileId: account.owner_agent_profile_id?.trim() ?? '',
        ownerDisplayName: account.owner_display_name?.trim() ?? '',
        inboundEnabled: account.inbound_enabled,
        revision: account.revision,
        createdAt: account.created_at ?? '',
        updatedAt: account.updated_at ?? '',
        ownedByTarget: account.owner_agent_profile_id?.trim() === agentProfileId
      }))
      .sort((left, right) => left.accountId.localeCompare(right.accountId))
  }
}

function channelStatusTone(status: string): WorkforceChannelAccountViewModel['statusTone'] {
  switch (status.trim().toLowerCase()) {
    case 'connected':
      return 'success'
    case 'degraded':
      return 'warning'
    case 'failed':
    case 'error':
      return 'danger'
    case 'connecting':
      return 'info'
    default:
      return 'neutral'
  }
}
