import { describe, expect, it } from 'vitest'
import { mapWorkforceChannelBindingsView } from './workforce-channel-bindings'

describe('mapWorkforceChannelBindingsView', () => {
  it('sorts aliases and marks accounts owned by the selected employee', () => {
    expect(mapWorkforceChannelBindingsView({
      agent_profile_id: 'jordan-belfort',
      accounts: [
        { channel_key: 'feishu', account_id: 'main', enabled: false, status: 'unavailable', owner_agent_profile_id: 'gangbeng', owner_display_name: '贴饼', inbound_enabled: false, revision: 4 },
        { channel_key: 'feishu', account_id: 'jordan-belfort', enabled: true, status: 'connected', owner_agent_profile_id: 'jordan-belfort', owner_display_name: 'Jordan', inbound_enabled: true, revision: 2 }
      ]
    })).toEqual({
      agentProfileId: 'jordan-belfort',
      accounts: [
        expect.objectContaining({ key: 'feishu:jordan-belfort', accountId: 'jordan-belfort', ownedByTarget: true, statusTone: 'success' }),
        expect.objectContaining({ key: 'feishu:main', accountId: 'main', ownedByTarget: false, statusTone: 'neutral' })
      ]
    })
  })
})
