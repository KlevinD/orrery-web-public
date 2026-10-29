import { existsSync, readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('settings migration readiness', () => {
  it('keeps settings page behind a view model mapper instead of API DTOs', () => {
    const source = readFileSync('src/routes/settings/+page.svelte', 'utf-8')
    const workspace = readFileSync('src/lib/components/settings/SettingsWorkspace.svelte', 'utf-8')

    expect(source).not.toContain('#lib/types/api')
    expect(source).not.toContain('SettingsDTO')
    expect(source).toContain('SettingsWorkspace')
    expect(workspace).not.toContain('#lib/types/api')
    expect(workspace).not.toContain('SettingsDTO')
    expect(workspace).toContain('SettingsViewModel')
  })

  it('keeps runtime profile data helpers out of the settings workspace component', () => {
    const workspace = readFileSync('src/lib/components/settings/SettingsWorkspace.svelte', 'utf-8')

    expect(existsSync('src/lib/components/settings/runtimeProfileHelpers.ts')).toBe(true)
    expect(workspace).toContain("from './runtimeProfileHelpers'")
    expect(workspace).not.toContain('function cloneRuntimeConfig')
    expect(workspace).not.toContain('function runtimeConfigToFormValues')
    expect(workspace).not.toContain('function stringRuntimeConfigValue')
    expect(workspace).not.toContain('function runtimeProfileIDFromDiscovery')
  })

  it('drives settings notification rules from the settings API view model without placeholder integrations', () => {
    const source = readFileSync('src/routes/settings/+page.svelte', 'utf-8')
    const workspace = readFileSync('src/lib/components/settings/SettingsWorkspace.svelte', 'utf-8')
    const notificationsPanel = readFileSync('src/lib/components/settings/NotificationRulesPanel.svelte', 'utf-8')
    const mapper = readFileSync('src/lib/mappers/settings.ts', 'utf-8')
    const api = readFileSync('src/lib/api/settings.ts', 'utf-8')
    const viewModels = readFileSync('src/lib/types/view-models.ts', 'utf-8')
    const apiTypes = readFileSync('src/lib/types/api.ts', 'utf-8')

    expect(workspace).not.toContain('settings.actionUnavailable')
    expect(workspace).not.toContain('const integrations =')
    expect(workspace).not.toContain('const notificationRules =')
    expect(notificationsPanel).toContain('draft.notificationRules')
    for (const [file, text] of [
      ['settings route', source],
      ['settings workspace', workspace],
      ['settings notification panel', notificationsPanel],
      ['settings mapper', mapper],
      ['settings api', api],
      ['view models', viewModels],
      ['api types', apiTypes]
    ] as const) {
      expect(text, file).not.toContain('integrations')
      expect(text, file).not.toContain('Integration')
      expect(text, file).not.toContain('/api/settings/integrations/')
    }
  })

  it('keeps workforce management in the people directory instead of settings', () => {
    const settings = readFileSync('src/routes/settings/+page.svelte', 'utf-8')
    const people = readFileSync('src/routes/people/+page.svelte', 'utf-8')

    expect(settings).not.toContain("'users'")
    expect(settings).not.toContain('settings.users')
    expect(settings).not.toContain("activeTab === 'users'")
    expect(settings).not.toContain('useWorkforceMembersInfiniteQuery')
    expect(settings).not.toContain('workforceQuery')
    expect(people).toContain('useWorkforceMembersInfiniteQuery')
  })

  it('keeps gateway settings as source-aware forms instead of flattening YAML keys', () => {
    const workspace = readFileSync('src/lib/components/settings/SettingsWorkspace.svelte', 'utf-8')
    const config = readFileSync('src/lib/components/settings/settingsConfig.ts', 'utf-8')
    const gatewayPanel = readFileSync('src/lib/components/settings/GatewayChannelPanel.svelte', 'utf-8')

    expect(workspace).toContain('availableGatewayChannels')
    expect(workspace).toContain('visibleGatewayChannels')
    expect(config).toContain('gatewayChannelFieldGroups')
    expect(workspace).toContain('submitGatewayChannel')
    expect(gatewayPanel).toContain('onextrafieldchange')
    expect(workspace).not.toContain('JSON Config')
    expect(gatewayPanel).not.toContain('JSON Config')
    expect(workspace).not.toContain('No JSON config')
    expect(workspace).not.toContain('{#each gatewayChannelKeys as [channelKey, _backendKey, label]}')
  })

  it('wires runtime registration into system settings with active discovery while preserving LLM panels', () => {
    const source = readFileSync('src/lib/components/settings/SettingsWorkspace.svelte', 'utf-8')
    const config = readFileSync('src/lib/components/settings/settingsConfig.ts', 'utf-8')
    const runtimePanel = readFileSync('src/lib/components/settings/RuntimeProfilePanel.svelte', 'utf-8')
    const runtimeList = readFileSync('src/lib/components/settings/RuntimeSettingsPanel.svelte', 'utf-8')
    const runtimeDiscovery = readFileSync('src/lib/components/settings/RuntimeDiscoveryDialog.svelte', 'utf-8')
    const llmPanel = readFileSync('src/lib/components/settings/LLMProfilePanel.svelte', 'utf-8')
    const llmList = readFileSync('src/lib/components/settings/LLMSettingsPanel.svelte', 'utf-8')
    const mapper = readFileSync('src/lib/mappers/settings.ts', 'utf-8')
    const apiTypes = readFileSync('src/lib/types/api.ts', 'utf-8')
    const settingsApiTypes = readFileSync('src/lib/types/api/settings.ts', 'utf-8')
    const viewModels = readFileSync('src/lib/types/view-models.ts', 'utf-8')
    const settingsViewModels = readFileSync('src/lib/types/view-models/settings.ts', 'utf-8')
    const settingsDTO = settingsApiTypes.slice(
      settingsApiTypes.indexOf('export interface SettingsDTO'),
      settingsApiTypes.indexOf('export interface SettingsLLMProfileDTO')
    )
    const settingsViewModel = settingsViewModels.slice(
      settingsViewModels.indexOf('export interface SettingsViewModel'),
      settingsViewModels.indexOf('export interface SettingsRuntimeProfileViewModel')
    )
    const llmSection = llmList + llmPanel

    expect(config).toContain("{ id: 'llm'")
    expect(source).toContain("activeTab === 'llm'")
    expect(config).toContain('llmProviderOptions')
    expect(source).toContain('openLLMCreatePanel')
    expect(source).toContain('submitLLMProfile')
    expect(source).toContain('saveLLMProfiles')
    expect(source).toContain('llmPanelOpen')
    expect(source).toContain('llmPanelMode')
    expect(source).toContain('selectedLLMProfileId')
    expect(llmList).toContain('draft.llmProfiles')
    expect(source).toContain('llmProfileMissingBaseUrl')
    expect(source).toContain('llmProfileMissingCredential')
    expect(source).toContain('setLLMProfileGlobalDefault')
    expect(source).toContain('normalizeLLMProfileDefaults')
    expect(llmList).toContain("t('settings.globalDefault')")
    expect(source).toContain('cleanLLMText(llmProfileForm.baseUrl).length > 0')
    expect(source).toContain('cleanLLMText(llmProfileForm.apiKeyRef).length > 0')
    expect(llmPanel).not.toContain('bind:value={profile.apiKey}')
    expect(llmPanel).toContain("t('settings.baseUrlRequired')")
    expect(llmPanel).toContain("t('settings.credentialRequired')")
    expect(source).not.toContain('selectedLLMProvider')
    expect(source).not.toContain('bind:value={selectedLLMProvider}')
    expect(llmSection).not.toContain('<Button disabled={updateSettings.isPending} onclick={save}>{t(\'settings.save\')}</Button>')
    expect(runtimePanel).toContain("import RuntimeProviderConfigForm from '#lib/components/runtime/RuntimeProviderConfigForm.svelte'")
    expect(source).toContain('runtimeProviderSchemas')
    expect(config).toContain("{ id: 'runtime'")
    expect(source).toContain("activeTab === 'runtime'")
    expect(runtimePanel).toContain('<RuntimeProviderConfigForm')
    expect(source).toContain('useRuntimeDiscoveryQuery')
    expect(source).toContain('runtimeDiscoveryDialogOpen')
    expect(runtimeList).toContain("t('settings.autoDiscover')")
    expect(runtimeDiscovery).toContain("t('settings.registerSelected'")
    expect(source).toContain('applySelectedDiscoveredRuntimes')
    expect(source).toContain('selectedRuntimeDiscoveryKeys')
    expect(runtimeList).toContain('draft.runtimeProfiles')
    expect(source).toContain('openRuntimeCreatePanel')
    expect(source).toContain('submitRuntimeProfile')
    expect(source).toContain('saveRuntimeProfiles')
    expect(mapper).toContain('llm_profiles:')
    expect(mapper).toContain('runtime_profiles:')
    expect(mapper).toContain('mapRuntimeProfilesView')
    expect(mapper).not.toContain('dto.runtime_kind')
    expect(mapper).toContain('runtime_config_ref')
    expect(mapper).toContain('profile.runtimeConfigRef')
    expect(apiTypes).toContain("export type * from './api/settings'")
    expect(settingsDTO).toContain('llm_profiles?:')
    expect(settingsApiTypes).toContain('global_default?: boolean')
    expect(settingsDTO).toContain('runtime_profiles?:')
    expect(settingsDTO).not.toContain('runtime_kind?:')
    expect(viewModels).toContain("export type * from './view-models/settings'")
    expect(settingsViewModel).toContain('llmProfiles: SettingsLLMProfileViewModel[]')
    expect(settingsViewModels).toContain('globalDefault: boolean')
    expect(settingsViewModel).toContain('runtimeProfiles: SettingsRuntimeProfileViewModel[]')
  })

  it('wires prompt block settings as draggable cards without tool configuration', () => {
    const workspace = readFileSync('src/lib/components/settings/SettingsWorkspace.svelte', 'utf-8')
    const viewHelpers = readFileSync('src/lib/components/settings/settingsViewHelpers.ts', 'utf-8')
    const config = readFileSync('src/lib/components/settings/settingsConfig.ts', 'utf-8')
    const panel = readFileSync('src/lib/components/settings/PromptBlocksPanel.svelte', 'utf-8')
    const mapper = readFileSync('src/lib/mappers/settings.ts', 'utf-8')
    const apiTypes = readFileSync('src/lib/types/api/settings.ts', 'utf-8')
    const viewModels = readFileSync('src/lib/types/view-models/settings.ts', 'utf-8')

    expect(config).toContain("{ id: 'prompt'")
    expect(workspace).toContain('PromptBlocksPanel')
    expect(workspace).toContain("activeTab === 'prompt'")
    expect(workspace).toContain("import { copySettingsView } from './settingsViewHelpers'")
    expect(viewHelpers).toContain('promptBlocks: view.promptBlocks.map')
    expect(workspace).toContain('page-shell flex h-full min-h-0 max-w-[920px] flex-col gap-5')
    expect(workspace).toContain('role="tablist"')
    expect(workspace).toContain("activeTab === 'prompt' ? 'flex-1' : ''")
    expect(panel).toContain('draggable="true"')
    expect(panel).toContain('addCustomBlock')
    expect(panel).toContain('removeBlock')
    expect(panel).toContain('Dialog')
    expect(panel).toContain('editingIndex')
    expect(panel).toContain('openEditor')
    expect(panel).toContain('availablePromptVariables')
    expect(panel).toContain('discussion_goal')
    expect(panel).toContain('discussion_members')
    expect(panel).not.toContain('agent_core_prompt')
    expect(panel).not.toContain('promptVariable.corePrompt')
    expect(panel).toContain('isLockedDynamicBlock')
    expect(panel).toContain("t('settings.promptVariables')")
    expect(panel).toContain('<aside')
    expect(panel).toContain('<details')
    expect(panel.indexOf("t('settings.promptPreview')")).toBeLessThan(panel.indexOf("t('settings.promptVariables')"))
    expect(panel).toContain('bind:value={editingBlock.title}')
    expect(panel).toContain('bind:value={editingBlock.code}')
    expect(panel).toContain('bind:value={editingBlock.role}')
    expect(panel).toContain('bind:value={editingBlock.content}')
    expect(panel).not.toContain('bind:value={block.content}')
    expect(panel).toContain('bind:checked={editingBlock.variableOnly}')
    expect(panel).toContain('checked={block.enabled}')
    expect(panel).toContain('setBlockEnabled')
    expect(panel).toContain('resolvePreviewVariables')
    expect(panel).toContain('finalPreview()')
    expect(panel).not.toContain('block.dynamic')
    expect(panel).toContain('editingBlock.editable')
    expect(panel).toContain('block.removable')
    expect(panel).toContain('<<')
    expect(panel).not.toContain("block.dynamic ? t('settings.promptDynamic.desc')")
    expect(panel).not.toContain("editingBlock.dynamic")
    expect(panel).not.toContain('max-h-52')
    expect(panel).not.toContain('max-h-[560px] overflow-auto')
    expect(panel).toContain('flex h-full min-h-0 flex-col')
    expect(panel).toContain('subtle-scrollbar min-h-0 space-y-2 overflow-y-auto')
    expect(panel).toContain('pre class="subtle-scrollbar mt-3 min-h-0 flex-1 overflow-auto')
    expect(panel).toContain('grid min-h-0 flex-1 gap-2 overflow-y-auto')
    expect(panel).not.toContain("---")
    expect(mapper).toContain('prompt_blocks:')
    expect(apiTypes).toContain('SettingsPromptBlockDTO')
    expect(viewModels).toContain('SettingsPromptBlockViewModel')
    expect(apiTypes).toContain('code?: string')
    expect(viewModels).toContain('code: string')
    expect(apiTypes).toContain('removable?: boolean')
    expect(viewModels).toContain('removable: boolean')
    expect(apiTypes).toContain('variable_only?: boolean')
    expect(viewModels).toContain('variableOnly: boolean')
    expect(panel).not.toContain('toolBindings')
    expect(panel).not.toContain('mcp')
    expect(panel).not.toContain('tool_schema')
  })

  it('does not show raw gateway YAML import controls in settings', () => {
    const source = readFileSync('src/routes/settings/+page.svelte', 'utf-8')

    expect(source).not.toContain('Advanced YAML Import')
    expect(source).not.toContain('Save YAML')
    expect(source).not.toContain('saveGatewayRaw')
    expect(source).not.toContain('useGatewaySettingsRawUpdateMutation')
    expect(source).not.toContain('bind:value={gatewayDraft.rawYaml}')
    expect(source).not.toContain('gatewayDraft.rawYaml')
    expect(source).not.toContain('raw_yaml')
    expect(source).not.toContain('YAML')
  })

  it('keeps message gateway channels out of removed settings integrations', () => {
    const settingsHandlerPath = '../internal/transport/http/settings_handler.go'
    const source = readFileSync('src/routes/settings/+page.svelte', 'utf-8')

    if (existsSync(settingsHandlerPath)) {
      const settingsHandler = readFileSync(settingsHandlerPath, 'utf-8')
      expect(settingsHandler).not.toContain('defaultIntegration("wecom"')
      expect(settingsHandler).not.toContain('defaultIntegration("feishu"')
      expect(settingsHandler).not.toContain('defaultIntegration("dingtalk"')
    }
    expect(source).not.toContain('gatewayOwnedIntegrationIds')
    expect(source).not.toContain('integrations')
  })

  it('keeps workforce page form commands behind the query and mapper boundary', () => {
    for (const file of [
      'src/routes/people/+page.svelte',
      'src/lib/components/operations/WorkforceMemberEditor.svelte'
    ]) {
      const source = readFileSync(file, 'utf-8')

      expect(source, file).not.toContain('#lib/types/api')
      expect(source, file).not.toContain('UpsertWorkforceMemberCommand')
      expect(source, file).toContain('WorkforceMemberFormViewModel')
    }
  })

  it('wires runtime profile references into workforce member editing', () => {
    const source = readFileSync('src/lib/components/operations/WorkforceMemberEditor.svelte', 'utf-8')
    const typeSource = readFileSync('src/lib/types/view-models/settings.ts', 'utf-8')

    expect(existsSync('src/lib/components/runtime/RuntimeProviderConfigForm.svelte')).toBe(true)
    expect(source).not.toContain("import RuntimeProviderConfigForm from '#lib/components/runtime/RuntimeProviderConfigForm.svelte'")
    expect(source).toContain('runtimeOptions')
    expect(source).toContain('runtimeConfigRef')
    expect(source).toContain("selectField('people.runtime', runtimeConfigRef")
    expect(source).toContain('runtimeConfig')
    expect(typeSource).toContain('runtimeConfig: Record<string, string | string[]>')
  })

  it('keeps workforce command and view mappers in dedicated modules', () => {
    const aggregator = readFileSync('src/lib/mappers/workforce.ts', 'utf-8')

    expect(existsSync('src/lib/mappers/workforceCommands.ts')).toBe(true)
    expect(existsSync('src/lib/mappers/workforceViews.ts')).toBe(true)
    expect(aggregator).toContain("from './workforceCommands'")
    expect(aggregator).toContain("from './workforceViews'")
    expect(aggregator).not.toContain('function mapWorkforceMemberCommand')
    expect(aggregator).not.toContain('function mapWorkforceView')
    expect(aggregator).not.toContain('function mapOrgGraphView')
  })
})
