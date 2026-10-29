import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import type { SettingsViewModel } from '#lib/types/view-models'
import RuntimeApprovalSettingsPanel from './RuntimeApprovalSettingsPanel.svelte'

describe('RuntimeApprovalSettingsPanel', () => {
  it('edits reviewer, exec, tool, forced-human, and agent settings through the existing save action', async () => {
    setLocale('en-US')
    const draft = settingsDraft()
    const onsave = vi.fn()
    render(RuntimeApprovalSettingsPanel, { props: { draft, busy: false, onsave } })

    expect(screen.getByRole('heading', { name: 'Execution and approvals' })).toBeTruthy()
    expect(screen.getByText(/cannot be disabled/i)).toBeTruthy()

    await fireEvent.change(screen.getByLabelText('Reviewer model'), { target: { value: 'llm-reviewer' } })
    await fireEvent.change(screen.getByLabelText('Command approval'), { target: { value: 'on-request' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Add Exec rule' }))
    await fireEvent.input(screen.getByLabelText('Executable 1'), { target: { value: 'git' } })
    await fireEvent.input(screen.getByLabelText('Subcommands 1'), { target: { value: 'status, diff' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Add Tool rule' }))
    await fireEvent.input(screen.getByLabelText('Tool ID 1'), { target: { value: 'discussion_talent_list' } })
    await fireEvent.click(screen.getByLabelText('Read effect 1'))
    await fireEvent.input(screen.getByLabelText('Additional forced-human Tool IDs'), { target: { value: 'publish_contract\ncharge_card' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Add Agent override' }))
    await fireEvent.input(screen.getByLabelText('Logical Agent ID 1'), { target: { value: 'assistant:human-1' } })

	const approvalMode = screen.getByLabelText('Approval mode')
	await fireEvent.change(approvalMode, { target: { value: 'human' } })
	expect(draft.runtimeApprovals.agentOverrides[0]).toMatchObject({ approvalPolicy: 'on-request', approvalsReviewer: 'human' })
	await fireEvent.change(approvalMode, { target: { value: 'ai' } })
	expect(draft.runtimeApprovals.agentOverrides[0]).toMatchObject({ approvalPolicy: 'on-request', approvalsReviewer: 'ai' })
	await fireEvent.change(approvalMode, { target: { value: 'always_allow' } })
	expect(draft.runtimeApprovals.agentOverrides[0]).toMatchObject({ approvalPolicy: 'never' })
	expect(draft.runtimeApprovals.agentOverrides[0].approvalsReviewer).toBeUndefined()
	expect(screen.getByText(/skips permission review/i)).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Save Settings' }))

    expect(draft.runtimeApprovals.reviewer.llmProfileId).toBe('llm-reviewer')
    expect(draft.runtimeApprovals.exec.approvalPolicy).toBe('on-request')
    expect(draft.runtimeApprovals.exec.allowlist[0]).toMatchObject({ executable: 'git', subcommands: ['status', 'diff'] })
    expect(draft.runtimeApprovals.tools.allowlist[0]).toMatchObject({ toolId: 'discussion_talent_list', effects: ['read'] })
    expect(draft.runtimeApprovals.forcedHuman.toolIds).toEqual(['publish_contract', 'charge_card'])
    expect(draft.runtimeApprovals.agentOverrides[0].logicalAgentId).toBe('assistant:human-1')
    expect(onsave).toHaveBeenCalledTimes(1)
  })
})

function settingsDraft(): SettingsViewModel {
  return {
    platformName: 'Orrery', organizationName: 'Acme', language: 'en-US',
    autoCreateAgentProfile: true, autoLinkEvidence: true, hilDefaultEnabled: true,
    discussionAgentPairLoopLimit: '8', workspaceRoot: '/workspace', workspaceAllowlist: '/workspace',
    memory: { enabled: false },
    knowledge: { enabled: true, defaultProvider: 'local', pageindex: { apiUrl: 'https://api.pageindex.ai', apiKey: '', apiKeyRef: '', defaultFolderId: '', timeoutSeconds: '30' }, local: { enabled: true } },
    runtimeProfiles: [],
    llmProfiles: [{ profileId: 'llm-reviewer', displayName: 'Approval reviewer', provider: 'openai', baseUrl: 'https://api.openai.com/v1', apiKey: '', apiKeyRef: 'secret://llm/reviewer', model: 'gpt-5', temperature: '0', contextLength: '16000', enabled: true, globalDefault: false }],
    promptBlocks: [], notificationRules: [],
    runtimeApprovals: {
      defaultHILMode: 'manual', defaultApprovalPolicy: 'on-request', defaultApprovalsReviewer: 'human', reviewer: { llmProfileId: '', timeoutMs: '30000', maxTokens: '360' },
      exec: { approvalPolicy: 'never', security: 'allowlist', allowlist: [] }, tools: { security: 'allowlist', allowlist: [] },
      forcedHuman: { toolIds: [], execExecutables: [] }, conversationDefaults: { personal_assistant: { hilMode: 'auto' } }, agentOverrides: []
    }
  }
}
