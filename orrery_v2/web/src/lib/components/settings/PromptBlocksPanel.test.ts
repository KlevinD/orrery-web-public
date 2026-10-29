import { render, screen } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import { setLocale } from '#lib/i18n'
import type { SettingsViewModel } from '#lib/types/view-models'
import PromptBlocksPanel from './PromptBlocksPanel.svelte'

describe('PromptBlocksPanel', () => {
  it('lists knowledge context as an available runtime variable', () => {
    setLocale('en-US')
    render(PromptBlocksPanel, { props: { draft: settingsDraft(), busy: false } })

    expect(screen.getAllByText('<<knowledge_context>>').length).toBeGreaterThan(0)
    expect(screen.getByText('Knowledge context')).toBeTruthy()
    expect(screen.getAllByText(/retrieved knowledge snippets/i).length).toBeGreaterThan(0)
  })
})

function settingsDraft(): SettingsViewModel {
  return {
    platformName: 'Orrery',
    organizationName: 'Acme',
    language: 'en-US',
    autoCreateAgentProfile: true,
    autoLinkEvidence: true,
    hilDefaultEnabled: true,
    discussionAgentPairLoopLimit: '3',
    workspaceRoot: '',
    workspaceAllowlist: '',
    memory: {
      enabled: false
    },
    knowledge: {
      enabled: true,
      defaultProvider: 'pageindex',
      pageindex: {
        apiUrl: 'https://api.pageindex.ai',
        apiKey: '',
        apiKeyRef: 'secret://knowledge/pageindex',
        defaultFolderId: '',
        timeoutSeconds: '30'
      },
      local: { enabled: true }
    },
    runtimeProfiles: [],
    llmProfiles: [],
    promptBlocks: [
      {
        blockId: 'runtime_input_template',
        code: 'runtime_input_template',
        title: 'Runtime input',
        description: '',
        role: 'user',
        content: '<<knowledge_context>>\n<<current_user_input>>',
        placeholder: '<<runtime_input_template>>',
        dynamic: false,
        editable: true,
        removable: false,
        variableOnly: false,
        enabled: true
      }
    ],
    runtimeApprovals: {
      defaultApprovalPolicy: 'on-request', defaultApprovalsReviewer: 'human',
      defaultHILMode: 'manual', reviewer: { llmProfileId: '', timeoutMs: '30000', maxTokens: '360' },
      exec: { approvalPolicy: 'never', security: 'allowlist', allowlist: [] }, tools: { security: 'allowlist', allowlist: [] },
      forcedHuman: { toolIds: [], execExecutables: [] }, conversationDefaults: { personal_assistant: { hilMode: 'auto' } }, agentOverrides: []
    },
    notificationRules: []
  }
}
