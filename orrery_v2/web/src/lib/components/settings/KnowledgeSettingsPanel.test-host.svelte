<script lang="ts">
  import type { SettingsPageIndexConnectionTestViewModel, SettingsViewModel } from '#lib/types/view-models'
  import KnowledgeSettingsPanel from './KnowledgeSettingsPanel.svelte'

  interface Props {
    testResult?: SettingsPageIndexConnectionTestViewModel | null
    ontestpageindex?: () => void
  }

  let { testResult = null, ontestpageindex }: Props = $props()
  let draft = $state<SettingsViewModel>({
    platformName: 'Orrery',
    organizationName: 'Acme',
    language: 'zh-CN',
    autoCreateAgentProfile: true,
    autoLinkEvidence: true,
    hilDefaultEnabled: true,
    discussionAgentPairLoopLimit: '8',
    workspaceRoot: '',
    workspaceAllowlist: '',
    memory: {
      enabled: false
    },
    knowledge: {
      enabled: true,
      defaultProvider: 'pageindex',
      pageindex: {
        apiUrl: 'http://127.0.0.1:8091',
        apiKey: 'local-dev',
        apiKeyRef: '',
        defaultFolderId: '',
        timeoutSeconds: '30'
      },
      local: {
        enabled: true
      }
    },
    runtimeProfiles: [],
    llmProfiles: [],
    promptBlocks: [],
    runtimeApprovals: {
      defaultApprovalPolicy: 'on-request', defaultApprovalsReviewer: 'human',
      defaultHILMode: 'manual', reviewer: { llmProfileId: '', timeoutMs: '30000', maxTokens: '360' },
      exec: { approvalPolicy: 'never', security: 'allowlist', allowlist: [] }, tools: { security: 'allowlist', allowlist: [] },
      forcedHuman: { toolIds: [], execExecutables: [] }, conversationDefaults: { personal_assistant: { hilMode: 'auto' } }, agentOverrides: []
    },
    notificationRules: []
  })
</script>

<KnowledgeSettingsPanel bind:draft {testResult} {ontestpageindex} />
