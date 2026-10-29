import { describe, expect, it } from 'vitest'
import { mapPersonalAssistantProfileView } from './profile'

describe('mapPersonalAssistantProfileView', () => {
  it('maps the user execution identity and private avatar', () => {
    const view = mapPersonalAssistantProfileView({
      assistant_enabled: true,
      agent_profile_id: 'personal-assistant-one',
      display_name: '我的助理',
      system_prompt: '只代表当前用户执行。',
      hosting_mode: 'owned_agent',
      owned_agent_id: 'runtime-one',
      workspace_grant_id: 'grant-one',
      fallback_to_managed: false,
      revision: 3,
      avatar_url: '/api/profile/personal-assistant/avatar?v=etag-one',
      avatar_etag: 'etag-one',
      execution_identity: 'user',
      cloud_fallback: false,
      owned_agents: []
    })

    expect(view).toMatchObject({
      agentProfileId: 'personal-assistant-one',
      displayName: '我的助理',
      systemPrompt: '只代表当前用户执行。',
      avatarUrl: '/api/profile/personal-assistant/avatar?v=etag-one',
      avatarETag: 'etag-one',
      executionIdentity: 'user',
      cloudFallback: false,
      fallbackToManaged: false
    })
  })
})
