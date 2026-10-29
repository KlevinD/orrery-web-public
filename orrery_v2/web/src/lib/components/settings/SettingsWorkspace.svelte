<script lang="ts">
  import GatewayChannelPanel from './GatewayChannelPanel.svelte'
  import AppearanceSettingsPanel from './AppearanceSettingsPanel.svelte'
  import GatewaySettingsPanel from './GatewaySettingsPanel.svelte'
  import GeneralSettingsPanel from './GeneralSettingsPanel.svelte'
  import ImageGenerationSettingsSection from './ImageGenerationSettingsSection.svelte'
  import KnowledgeSettingsPanel from './KnowledgeSettingsPanel.svelte'
  import KnowledgeProviderSettingsSection from './KnowledgeProviderSettingsSection.svelte'
  import LLMProfilePanel from './LLMProfilePanel.svelte'
  import LLMSettingsPanel from './LLMSettingsPanel.svelte'
  import NotificationRulesPanel from './NotificationRulesPanel.svelte'
  import PromptBlocksPanel from './PromptBlocksPanel.svelte'
  import RuntimeDiscoveryDialog from './RuntimeDiscoveryDialog.svelte'
  import RuntimeApprovalSettingsPanel from './RuntimeApprovalSettingsPanel.svelte'
  import RuntimeProfilePanel from './RuntimeProfilePanel.svelte'
  import RuntimeSettingsPanel from './RuntimeSettingsPanel.svelte'
  import {
    cleanLLMText,
    createLLMProfile,
    normalizeLLMProfile,
    normalizeLLMProfileDefaults,
    updateLLMProvider
  } from './llmProfileHelpers'
  import {
    availableGatewayChannels as buildAvailableGatewayChannels,
    copyFeishuAccounts,
    copyGatewaySettingsView,
    gatewayChannelFieldGroup,
    gatewayChannelHasConfig,
    gatewayExtraFieldLabel,
    gatewayPrimaryFieldValue,
    visibleGatewayChannels as buildVisibleGatewayChannels
  } from './gatewaySettingsHelpers'
  import {
    cloneRuntimeConfig,
    runtimeProfileCanSubmit,
    runtimeConfigToFormValues,
    runtimeProfileIDFromDiscovery,
    stringRuntimeConfigValue
  } from './runtimeProfileHelpers'
  import { copySettingsView } from './settingsViewHelpers'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { CircleCheck, FolderGit2 } from 'lucide-svelte'
  import { createTranslator, locale, setLocale } from '#lib/i18n'
  import { hasPermissionScope } from '#lib/permissions/access'
  import { useGatewayAccountsQuery, useGatewaySettingsQuery, useGatewaySettingsUpdateMutation } from '#lib/queries/gateway-settings'
  import { usePageIndexConnectionTestMutation, useRuntimeDiscoveryQuery, useSettingsQuery, useSettingsUpdateMutation } from '#lib/queries/settings'
  import { applyRuntimeDiscoveryCandidate, runtimeProviderSchemas, type RuntimeProviderKind } from '#lib/runtime/providerConfig'
  import { session } from '#lib/stores/session.svelte'
  import type { ApiClientError } from '#lib/api/client'
  import {
    llmProviderOptions,
    runtimeExecutableDefaults,
    runtimeProviderOptionsBase,
    tabs,
    type GatewayChannelKey,
    type GatewayFieldGroup,
    type LLMPanelMode,
    type RuntimePanelMode,
    type SettingsTab
  } from './settingsConfig'
  import type { GatewayChannelSettingsViewModel, GatewayExtraFieldViewModel, GatewayFeishuAccountViewModel, GatewaySettingsViewModel,
    RuntimeDiscoveryCandidateViewModel, SettingsLLMProfileViewModel, SettingsPageIndexConnectionTestViewModel,
    SettingsRuntimeProfileViewModel, SettingsViewModel } from '#lib/types/view-models'
  let activeTab = $state<SettingsTab>('general')
  let actionMessage = $state('')
  let draft = $state<SettingsViewModel | undefined>()
  let gatewayDraft = $state<GatewaySettingsViewModel | undefined>()
  let gatewayExtraFieldId = 0
  let selectedGatewayChannelKey = $state('')
  let gatewayPanelOpen = $state(false)
  let gatewayPanelMode = $state<'create' | 'edit'>('create')
  let gatewayEditingChannelKey = $state('')
  let gatewayPanelEnabled = $state(false)
  let gatewayPanelFieldValues = $state<Record<string, string>>({})
  let gatewayPanelExtraFields = $state<GatewayExtraFieldViewModel[]>([])
  let gatewayPanelFeishuAccounts = $state<Record<string, GatewayFeishuAccountViewModel>>({})
  let llmProfileCounter = 0
  let llmPanelOpen = $state(false)
  let llmPanelMode = $state<LLMPanelMode>('create')
  let selectedLLMProfileId = $state('')
  let llmProfileForm = $state<SettingsLLMProfileViewModel | undefined>()
  let runtimeProfileCounter = 0
  let runtimePanelOpen = $state(false)
  let runtimePanelMode = $state<RuntimePanelMode>('create')
  let selectedRuntimeProfileId = $state('')
  let runtimeProfileForm = $state<SettingsRuntimeProfileViewModel | undefined>()
  let pageIndexTestResult = $state<SettingsPageIndexConnectionTestViewModel | null>(null)
  let pageIndexTestError = $state('')
  let runtimeDiscoveryDialogOpen = $state(false)
  let selectedRuntimeDiscoveryKeys = $state<Record<string, boolean>>({})

  const settingsQuery = useSettingsQuery()
  const gatewaySettingsQuery = useGatewaySettingsQuery(() => activeTab === 'gateway')
  const gatewayAccountsQuery = useGatewayAccountsQuery(() => activeTab === 'gateway')
  const runtimeDiscoveryQuery = useRuntimeDiscoveryQuery()
  const updateSettings = useSettingsUpdateMutation()
  const updateGatewaySettings = useGatewaySettingsUpdateMutation()
  const testPageIndexConnection = usePageIndexConnectionTestMutation()
  let t = $derived(createTranslator($locale))
  let canManageSettings = $derived(hasPermissionScope(session.permissionScope, 'settings.manage'))
  let canReadConnections = $derived(hasPermissionScope(session.permissionScope, 'connection.read'))
  let runtimeProviderOptions = $derived([{ label: t('settings.selectRuntime'), value: '' }, ...runtimeProviderOptionsBase])
  let selectedRuntimeProvider = $derived(runtimeProviderSchemas.find((provider) => provider.kind === runtimeProfileForm?.runtimeKind))
  let runtimeFormValues = $derived(runtimeProfileForm ? runtimeConfigToFormValues({ ...runtimeProfileForm.runtimeConfig, config_ref: runtimeProfileForm.runtimeConfigRef }) : {})
  let workspaceRootLabel = $derived(draft?.workspaceRoot?.trim() || t('settings.noConfigRef'))
  $effect(() => {
    if (settingsQuery.data && !draft) {
      draft = copySettingsView(settingsQuery.data)
    }
  })
  $effect(() => {
    if (gatewaySettingsQuery.data && !gatewayDraft) {
      gatewayDraft = copyGatewaySettingsView(gatewaySettingsQuery.data)
    }
  })
  function save() {
    if (!canManageSettings || !draft) return
    actionMessage = ''
    updateSettings.mutate(draft, {
      onSuccess: () => {
        actionMessage = t('settings.saved')
        setLocale(draft?.language ?? 'zh-CN')
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('settings.saveFailed')
      }
    })
  }

  function testPageIndex() {
    if (!canManageSettings || !draft) return
    pageIndexTestResult = null
    pageIndexTestError = ''
    testPageIndexConnection.mutate(draft.knowledge.pageindex, {
      onSuccess: (result) => {
        pageIndexTestResult = result
      },
      onError: (error) => {
        pageIndexTestError = error instanceof Error ? error.message : t('settings.pageindexTestFailed')
      }
    })
  }

  function runtimeProviderLabel(kind: string) {
    return runtimeProviderSchemas.find((provider) => provider.kind === kind)?.label ?? kind
  }

  function nextRuntimeProfileId(kind: string): string {
    runtimeProfileCounter += 1
    return `runtime-${kind || 'custom'}-${Date.now()}-${runtimeProfileCounter}`
  }

  function runtimeProfileSummary(profile: SettingsRuntimeProfileViewModel): string {
    const executable = stringRuntimeConfigValue(profile.runtimeConfig.executable)
    return [runtimeProviderLabel(profile.runtimeKind), executable || t('settings.noExecutable'), profile.runtimeConfigRef || t('settings.noConfigRef')].join(' · ')
  }

  function createRuntimeProfile(kind: string): SettingsRuntimeProfileViewModel {
    const executable = runtimeExecutableDefaults[kind as RuntimeProviderKind]
    return {
      profileId: nextRuntimeProfileId(kind),
      displayName: runtimeProviderLabel(kind),
      runtimeKind: kind,
      runtimeConfigRef: kind ? `runtime-${kind}-local` : '',
      runtimeConfig: executable ? { executable } : {},
      enabled: true
    }
  }

  function openRuntimeCreatePanel() {
    if (!canManageSettings) return
    runtimePanelMode = 'create'
    selectedRuntimeProfileId = ''
    runtimeProfileForm = createRuntimeProfile('codex')
    runtimePanelOpen = true
    actionMessage = ''
  }

  function openRuntimeEditPanel(profile: SettingsRuntimeProfileViewModel) {
    if (!canManageSettings) return
    runtimePanelMode = 'edit'
    selectedRuntimeProfileId = profile.profileId
    runtimeProfileForm = {
      ...profile,
      runtimeConfig: cloneRuntimeConfig(profile.runtimeConfig)
    }
    runtimePanelOpen = true
    actionMessage = ''
  }

  function closeRuntimePanel() {
    runtimePanelOpen = false
    selectedRuntimeProfileId = ''
    runtimeProfileForm = undefined
  }

  function updateRuntimeProvider(kind: string) {
    if (!canManageSettings || !runtimeProfileForm) return
    const previousLabel = runtimeProviderLabel(runtimeProfileForm.runtimeKind)
    runtimeProfileForm.runtimeKind = kind
    runtimeProfileForm.runtimeConfigRef = kind ? `runtime-${kind}-local` : ''
    if (!runtimeProfileForm.displayName || runtimeProfileForm.displayName === previousLabel) {
      runtimeProfileForm.displayName = runtimeProviderLabel(kind)
    }
    const executable = runtimeExecutableDefaults[kind as RuntimeProviderKind]
    runtimeProfileForm.runtimeConfig = executable ? { executable } : {}
  }

  function updateRuntimeConfig(config: {
    runtimeKind: RuntimeProviderKind
    runtimeConfigRef: string
    runtimeConfig: Record<string, string | string[]>
  }) {
    if (!canManageSettings || !runtimeProfileForm) return
    runtimeProfileForm.runtimeKind = config.runtimeKind
    runtimeProfileForm.runtimeConfigRef = config.runtimeConfigRef
    runtimeProfileForm.runtimeConfig = cloneRuntimeConfig(config.runtimeConfig)
  }

  function normalizeRuntimeProfile(profile: SettingsRuntimeProfileViewModel): SettingsRuntimeProfileViewModel {
    const kind = cleanRuntimeText(profile.runtimeKind) || 'codex'
    return {
      ...profile,
      profileId: cleanRuntimeText(profile.profileId) || nextRuntimeProfileId(kind),
      displayName: cleanRuntimeText(profile.displayName) || runtimeProviderLabel(kind),
      runtimeKind: kind,
      runtimeConfigRef: cleanRuntimeText(profile.runtimeConfigRef),
      runtimeConfig: cloneRuntimeConfig(profile.runtimeConfig),
      enabled: profile.enabled
    }
  }

  function cleanRuntimeText(value: string | undefined | null): string {
    return String(value ?? '').trim()
  }

  function canSubmitRuntimeProfile() {
    return runtimeProfileCanSubmit(runtimeProfileForm)
  }

  function saveRuntimeProfiles(profiles: SettingsRuntimeProfileViewModel[], closePanelOnSuccess = false) {
    if (!canManageSettings || !draft) return
    const previous = copySettingsView(draft)
    const next = copySettingsView({ ...draft, runtimeProfiles: profiles.map(normalizeRuntimeProfile) })
    draft = next
    actionMessage = ''
    updateSettings.mutate(next, {
      onSuccess: () => {
        actionMessage = t('settings.saved')
        if (closePanelOnSuccess) closeRuntimePanel()
      },
      onError: (error) => {
        draft = previous
        actionMessage = error instanceof Error ? error.message : t('settings.saveFailed')
      }
    })
  }

  function submitRuntimeProfile() {
    if (!canManageSettings || !draft || !runtimeProfileForm) return
    const form = normalizeRuntimeProfile(runtimeProfileForm)
    const profiles =
      runtimePanelMode === 'edit'
        ? draft.runtimeProfiles.map((profile) => (profile.profileId === selectedRuntimeProfileId ? form : profile))
        : [...draft.runtimeProfiles, form]
    saveRuntimeProfiles(profiles, true)
  }

  function removeRuntimeProfile(profileId: string) {
    if (!canManageSettings || !draft) return
    saveRuntimeProfiles(draft.runtimeProfiles.filter((profile) => profile.profileId !== profileId), selectedRuntimeProfileId === profileId)
  }

  function setRuntimeProfileEnabled(profileId: string, enabled: boolean) {
    if (!canManageSettings || !draft) return
    saveRuntimeProfiles(draft.runtimeProfiles.map((profile) => (profile.profileId === profileId ? { ...profile, enabled } : profile)))
  }

  async function openRuntimeDiscovery() {
    if (!canManageSettings) return
    actionMessage = ''
    selectedRuntimeDiscoveryKeys = {}
    runtimeDiscoveryDialogOpen = true
    await runtimeDiscoveryQuery.refetch()
  }

  function runtimeDiscoveryKey(candidate: RuntimeDiscoveryCandidateViewModel): string {
    return [candidate.kind, candidate.configRef, candidate.executable].join('|')
  }

  function runtimeProfileFromDiscovery(candidate: RuntimeDiscoveryCandidateViewModel): SettingsRuntimeProfileViewModel {
    const registration = applyRuntimeDiscoveryCandidate(candidate)
    return normalizeRuntimeProfile({
      profileId: runtimeProfileIDFromDiscovery(candidate),
      displayName: candidate.label,
      runtimeKind: registration.runtimeKind,
      runtimeConfigRef: registration.runtimeConfigRef,
      runtimeConfig: cloneRuntimeConfig(registration.runtimeConfig),
      enabled: candidate.status !== 'error'
    })
  }

  function selectedRuntimeDiscoveryCount() {
    return Object.values(selectedRuntimeDiscoveryKeys).filter(Boolean).length
  }

  function toggleAllRuntimeDiscoveryCandidates(checked: boolean) {
    if (!canManageSettings) return
    const next: Record<string, boolean> = {}
    for (const candidate of runtimeDiscoveryQuery.data ?? []) {
      next[runtimeDiscoveryKey(candidate)] = checked
    }
    selectedRuntimeDiscoveryKeys = next
  }

  function applySelectedDiscoveredRuntimes() {
    if (!canManageSettings || !draft) return
    const selected = (runtimeDiscoveryQuery.data ?? []).filter((candidate) => selectedRuntimeDiscoveryKeys[runtimeDiscoveryKey(candidate)])
    if (selected.length === 0) return
    const byId = new Map(draft.runtimeProfiles.map((profile) => [profile.profileId, profile]))
    for (const candidate of selected) {
      const profile = runtimeProfileFromDiscovery(candidate)
      byId.set(profile.profileId, profile)
    }
    saveRuntimeProfiles(Array.from(byId.values()))
    runtimeDiscoveryDialogOpen = false
  }

  function llmProviderLabel(provider: string) {
    return llmProviderOptions.find((option) => option.value === provider)?.label ?? provider
  }

  function nextLLMProfileId(provider: string): string {
    llmProfileCounter += 1
    return `llm-${provider || 'custom'}-${Date.now()}-${llmProfileCounter}`
  }

  function openLLMCreatePanel() {
    if (!canManageSettings) return
    llmPanelMode = 'create'
    selectedLLMProfileId = ''
    llmProfileForm = createLLMProfile('openai', nextLLMProfileId('openai'), llmProviderLabel)
    llmPanelOpen = true
    actionMessage = ''
  }

  function openLLMEditPanel(profile: SettingsLLMProfileViewModel) {
    if (!canManageSettings) return
    llmPanelMode = 'edit'
    selectedLLMProfileId = profile.profileId
    llmProfileForm = { ...profile }
    llmPanelOpen = true
    actionMessage = ''
  }

  function closeLLMPanel() {
    llmPanelOpen = false
    selectedLLMProfileId = ''
    llmProfileForm = undefined
  }

  function saveLLMProfiles(profiles: SettingsLLMProfileViewModel[], closePanelOnSuccess = false) {
    if (!canManageSettings || !draft) return
    const previous = copySettingsView(draft)
    const next = copySettingsView({ ...draft, llmProfiles: normalizeLLMProfileDefaults(profiles) })
    draft = next
    actionMessage = ''
    updateSettings.mutate(next, {
      onSuccess: () => {
        actionMessage = t('settings.saved')
        if (closePanelOnSuccess) closeLLMPanel()
      },
      onError: (error) => {
        draft = previous
        actionMessage = error instanceof Error ? error.message : t('settings.saveFailed')
      }
    })
  }

  function submitLLMProfile() {
    if (!canManageSettings || !draft || !llmProfileForm) return
    const form = normalizeLLMProfile(llmProfileForm, nextLLMProfileId, llmProviderLabel)
    const profiles =
      llmPanelMode === 'edit'
        ? draft.llmProfiles.map((profile) => (profile.profileId === selectedLLMProfileId ? form : profile))
        : [...draft.llmProfiles, form]
    saveLLMProfiles(profiles, true)
  }

  function canSubmitLLMProfile() {
    return Boolean(
      llmProfileForm &&
        cleanLLMText(llmProfileForm.provider).length > 0 &&
        cleanLLMText(llmProfileForm.displayName).length > 0 &&
        cleanLLMText(llmProfileForm.model).length > 0 &&
        cleanLLMText(llmProfileForm.baseUrl).length > 0 &&
        cleanLLMText(llmProfileForm.apiKeyRef).length > 0
    )
  }

  function llmProfileMissingBaseUrl() {
    return Boolean(llmProfileForm && cleanLLMText(llmProfileForm.baseUrl).length === 0)
  }

  function llmProfileMissingCredential() {
    return Boolean(
      llmProfileForm && cleanLLMText(llmProfileForm.apiKeyRef).length === 0
    )
  }

  function removeLLMProfile(profileId: string) {
    if (!canManageSettings || !draft) return
    saveLLMProfiles(draft.llmProfiles.filter((profile) => profile.profileId !== profileId), selectedLLMProfileId === profileId)
  }

  function setLLMProfileEnabled(profileId: string, enabled: boolean) {
    if (!canManageSettings || !draft) return
    saveLLMProfiles(draft.llmProfiles.map((profile) => (profile.profileId === profileId ? { ...profile, enabled, globalDefault: enabled ? profile.globalDefault : false } : profile)))
  }

  function setLLMProfileGlobalDefault(profileId: string, globalDefault: boolean) {
    if (!canManageSettings || !draft) return
    saveLLMProfiles(
      draft.llmProfiles.map((profile) => ({
        ...profile,
        enabled: profile.profileId === profileId && globalDefault ? true : profile.enabled,
        globalDefault: profile.profileId === profileId ? globalDefault : false
      }))
    )
  }

  function updateLLMFormProvider(provider: string) {
    if (!canManageSettings || !llmProfileForm) return
    updateLLMProvider(llmProfileForm, provider, llmProviderLabel)
  }

  function nextGatewayExtraFieldId(channelKey: string): string {
    gatewayExtraFieldId += 1
    return `${channelKey}-extra-${Date.now()}-${gatewayExtraFieldId}`
  }

  function visibleGatewayChannels(): Array<{ key: GatewayChannelKey; label: string; channel: GatewayChannelSettingsViewModel }> {
    return buildVisibleGatewayChannels(gatewayDraft)
  }

  function availableGatewayChannels() {
    return buildAvailableGatewayChannels(gatewayDraft)
  }

  function openGatewayCreatePanel() {
    if (!canManageSettings) return
    gatewayPanelMode = 'create'
    gatewayEditingChannelKey = ''
    selectedGatewayChannelKey = ''
    gatewayPanelEnabled = true
    gatewayPanelFieldValues = {}
    gatewayPanelExtraFields = []
    gatewayPanelFeishuAccounts = {}
    gatewayPanelOpen = true
  }

  function defaultGatewayExtraFields(channelKey: string): GatewayExtraFieldViewModel[] {
    return gatewayChannelFieldGroup(channelKey).extraFields.map((opt) => ({
      id: nextGatewayExtraFieldId(channelKey),
      key: opt.key,
      value: opt.defaultValue ?? ''
    }))
  }

  function setSelectedGatewayChannel(channelKey: string) {
    if (!canManageSettings) return
    selectedGatewayChannelKey = channelKey
    gatewayPanelFieldValues = {}
    gatewayPanelExtraFields = channelKey ? defaultGatewayExtraFields(channelKey) : []
    gatewayPanelFeishuAccounts = channelKey === 'feishu' ? copyFeishuAccounts(gatewayDraft?.feishuAccounts) : {}
  }

  function openGatewayEditPanel(channelKey: string) {
    if (!canManageSettings || !gatewayDraft) return
    const channel = gatewayDraft.channels[channelKey]
    if (!channel) return
    const group = gatewayChannelFieldGroup(channelKey)
    gatewayPanelMode = 'edit'
    gatewayEditingChannelKey = channelKey
    gatewayPanelEnabled = channel.enabled
    gatewayPanelFieldValues = {
      token: channel.token,
      apiKey: channel.apiKey,
      webhookUrl: channel.webhookUrl
    }
    gatewayPanelExtraFields = group.extraFields.map((opt) => {
      const existing = channel.extraFields.find((f) => f.key === opt.key)
      return existing ? { ...existing } : { id: nextGatewayExtraFieldId(channelKey), key: opt.key, value: opt.defaultValue ?? '' }
    })
    gatewayPanelFeishuAccounts = channelKey === 'feishu' ? copyFeishuAccounts(gatewayDraft.feishuAccounts) : {}
    gatewayPanelOpen = true
  }

  function closeGatewayPanel() {
    gatewayPanelOpen = false
  }

  function submitGatewayChannel() {
    if (!canManageSettings || !gatewayDraft) return
    const previous = copyGatewaySettingsView(gatewayDraft)
    const key = gatewayPanelMode === 'edit' ? gatewayEditingChannelKey : selectedGatewayChannelKey
    const channel = gatewayDraft.channels[key]
    if (!channel) return
    channel.enabled = gatewayPanelEnabled
    channel.token = gatewayPanelFieldValues.token ?? ''
    channel.apiKey = gatewayPanelFieldValues.apiKey ?? ''
    channel.webhookUrl = gatewayPanelFieldValues.webhookUrl ?? ''
    channel.extraFields = gatewayPanelExtraFields.map((f) => ({ ...f }))
    if (key === 'feishu') gatewayDraft.feishuAccounts = copyFeishuAccounts(gatewayPanelFeishuAccounts)
    saveGatewayDraft(true, previous)
  }

  function gatewayPanelChannelKey(): string {
    return gatewayPanelMode === 'edit' ? gatewayEditingChannelKey : selectedGatewayChannelKey
  }

  function gatewayPanelFieldGroup(): GatewayFieldGroup {
    return gatewayChannelFieldGroup(gatewayPanelChannelKey())
  }

  function removeGatewayChannel(channelKey: string) {
    if (!canManageSettings) return
    const current = gatewayDraft
    const channel = current?.channels[channelKey]
    if (!channel) return
    const previous = copyGatewaySettingsView(current)
    channel.enabled = false
    channel.token = ''
    channel.apiKey = ''
    channel.webhookUrl = ''
    channel.extraFields = []
    if (channelKey === 'feishu') current.feishuAccounts = {}
    saveGatewayDraft(true, previous)
  }

  function saveGatewayDraft(closePanelOnSuccess: boolean, previous: GatewaySettingsViewModel) {
    if (!canManageSettings || !gatewayDraft) return
    const next = copyGatewaySettingsView(gatewayDraft)
    actionMessage = ''
    updateGatewaySettings.mutate(next, {
      onSuccess: () => {
        actionMessage = t('settings.saved')
        gatewayDraft = next
        if (closePanelOnSuccess) closeGatewayPanel()
      },
      onError: (error) => {
        gatewayDraft = previous
        actionMessage = error instanceof Error ? error.message : t('settings.saveFailed')
      }
    })
  }

  function setGatewayPanelEnabled(enabled: boolean) {
    if (!canManageSettings) return
    gatewayPanelEnabled = enabled
  }

  function setGatewayPanelFieldValue(fieldId: string, value: string) {
    if (!canManageSettings) return
    gatewayPanelFieldValues = { ...gatewayPanelFieldValues, [fieldId]: value }
  }

  function setGatewayPanelExtraFieldValue(channelKey: string, fieldKey: string, value: string) {
    if (!canManageSettings) return
    const idx = gatewayPanelExtraFields.findIndex((field) => field.key === fieldKey)
    if (idx >= 0) {
      gatewayPanelExtraFields = gatewayPanelExtraFields.map((field, index) => (index === idx ? { ...field, value } : field))
      return
    }
    gatewayPanelExtraFields = [...gatewayPanelExtraFields, { id: nextGatewayExtraFieldId(channelKey), key: fieldKey, value }]
  }

  function setGatewayPanelFeishuAccounts(accounts: Record<string, GatewayFeishuAccountViewModel>) {
    if (!canManageSettings) return
    gatewayPanelFeishuAccounts = copyFeishuAccounts(accounts)
  }

  async function refreshGatewayAfterRegistration() {
    if (!canManageSettings) return
    closeGatewayPanel()
    const [result] = await Promise.all([gatewaySettingsQuery.refetch(), gatewayAccountsQuery.refetch()])
    if (result.data) {
      gatewayDraft = copyGatewaySettingsView(result.data)
    }
  }

  function setRuntimeDiscoverySelection(candidateKey: string, selected: boolean) {
    if (!canManageSettings) return
    selectedRuntimeDiscoveryKeys = { ...selectedRuntimeDiscoveryKeys, [candidateKey]: selected }
  }

</script>

<svelte:head>
  <title>{t('nav.settings')} | Orrery</title>
</svelte:head>

<div class="page-shell flex h-full min-h-0 max-w-[920px] flex-col gap-5">
  <header class="page-header shrink-0">
    <div>
      <h1 class="page-title">{t('nav.settings')}</h1>
      <p class="page-description">{t('nav.settings.description')}</p>
    </div>
    {#if canReadConnections}
      <Button href="/settings/scm-connections" variant="outline" class="shrink-0">
        <FolderGit2 class="h-4 w-4" />
        {t('development.scmTitle')}
      </Button>
    {/if}
  </header>

  {#if settingsQuery.isLoading}
    <LoadingState title={t('nav.settings')} description={t('nav.settings.description')} />
  {:else if settingsQuery.isError}
    {@const error = settingsQuery.error as ApiClientError}
    <ErrorState title={t('nav.settings')} message={error.message} correlationId={error.correlationId} />
  {:else if draft}
    <div class="subtle-scrollbar -mx-1 flex shrink-0 items-center gap-1 overflow-x-auto border-b border-border px-1" role="tablist" aria-label={t('nav.settings')}>
      {#each tabs as tab}
        {@const Icon = tab.icon}
        {@const tabLabel = t(tab.label)}
        <button
          class="-mb-px inline-flex h-10 shrink-0 items-center gap-2 whitespace-nowrap border-b-2 px-3 text-[13px] font-medium transition-colors {activeTab === tab.id
            ? 'border-brand text-foreground'
            : 'border-transparent text-muted-foreground hover:text-foreground'}"
          type="button"
          role="tab"
          aria-selected={activeTab === tab.id}
          onclick={() => (activeTab = tab.id)}
        >
          <Icon class="h-4 w-4" />
          {tabLabel}
        </button>
      {/each}
    </div>

    <fieldset class="contents" disabled={!canManageSettings && activeTab !== 'appearance'}>
      <div class="min-h-0 {activeTab === 'prompt' ? 'flex-1' : ''}">
        {#if activeTab === 'general'}
          <GeneralSettingsPanel bind:draft busy={updateSettings.isPending} onsave={save} />
      {:else if activeTab === 'prompt'}
        <PromptBlocksPanel bind:draft busy={updateSettings.isPending} onsave={save} />
      {:else if activeTab === 'runtime'}
        <RuntimeSettingsPanel
          {draft}
          busy={updateSettings.isPending}
          discoveryBusy={runtimeDiscoveryQuery.isFetching}
          {workspaceRootLabel}
          {runtimeProviderLabel}
          {runtimeProfileSummary}
          ondiscover={openRuntimeDiscovery}
          oncreate={openRuntimeCreatePanel}
          onedit={openRuntimeEditPanel}
          onremove={removeRuntimeProfile}
          onenabledchange={setRuntimeProfileEnabled}
        />
      {:else if activeTab === 'approval'}
        <RuntimeApprovalSettingsPanel bind:draft busy={updateSettings.isPending} onsave={save} />
      {:else if activeTab === 'llm'}
        <LLMSettingsPanel
          {draft}
          busy={updateSettings.isPending}
          {workspaceRootLabel}
          {llmProviderLabel}
          oncreate={openLLMCreatePanel}
          onedit={openLLMEditPanel}
          onremove={removeLLMProfile}
          onenabledchange={setLLMProfileEnabled}
          onsetdefault={setLLMProfileGlobalDefault}
        />
      {:else if activeTab === 'image'}
        <ImageGenerationSettingsSection canManage={canManageSettings} />
      {:else if activeTab === 'knowledge'}
        <KnowledgeSettingsPanel
          bind:draft
          busy={updateSettings.isPending}
          testing={testPageIndexConnection.isPending}
          testResult={pageIndexTestResult}
          testError={pageIndexTestError}
          onsave={save}
          ontestpageindex={testPageIndex}
        />
        <KnowledgeProviderSettingsSection />
      {:else if activeTab === 'gateway'}
        <GatewaySettingsPanel
          draft={gatewayDraft}
          loading={gatewaySettingsQuery.isLoading}
          error={gatewaySettingsQuery.isError ? (gatewaySettingsQuery.error as ApiClientError) : null}
          visibleChannels={visibleGatewayChannels()}
          accountStatuses={gatewayAccountsQuery.data ?? []}
          fieldGroup={gatewayChannelFieldGroup}
          primaryFieldValue={gatewayPrimaryFieldValue}
          extraFieldLabel={gatewayExtraFieldLabel}
          oncreate={openGatewayCreatePanel}
          onedit={openGatewayEditPanel}
        />
      {:else if activeTab === 'notifications'}
        <NotificationRulesPanel bind:draft busy={updateSettings.isPending} onsave={save} />
      {:else if activeTab === 'appearance'}
        <AppearanceSettingsPanel />
        {/if}
      </div>
    </fieldset>

    {#if canManageSettings}
      <RuntimeDiscoveryDialog
      open={runtimeDiscoveryDialogOpen}
      candidates={runtimeDiscoveryQuery.data ?? []}
      fetching={runtimeDiscoveryQuery.isFetching}
      error={runtimeDiscoveryQuery.isError ? (runtimeDiscoveryQuery.error as ApiClientError) : null}
      busy={updateSettings.isPending}
      bind:selectedKeys={selectedRuntimeDiscoveryKeys}
      discoveryKey={runtimeDiscoveryKey}
      selectedCount={selectedRuntimeDiscoveryCount}
      onclose={() => (runtimeDiscoveryDialogOpen = false)}
      ontoggleall={toggleAllRuntimeDiscoveryCandidates}
      onselectedchange={setRuntimeDiscoverySelection}
      onapply={applySelectedDiscoveredRuntimes}
      />

      <RuntimeProfilePanel
      open={runtimePanelOpen}
      mode={runtimePanelMode}
      bind:profile={runtimeProfileForm}
      selectedProfileId={selectedRuntimeProfileId}
      provider={selectedRuntimeProvider}
      providerOptions={runtimeProviderOptions}
      formValues={runtimeFormValues}
      busy={updateSettings.isPending}
      canSubmit={canSubmitRuntimeProfile()}
      onclose={closeRuntimePanel}
      onproviderchange={updateRuntimeProvider}
      onconfigchange={updateRuntimeConfig}
      onremove={removeRuntimeProfile}
      onsubmit={submitRuntimeProfile}
      />

      <LLMProfilePanel
      open={llmPanelOpen}
      mode={llmPanelMode}
      bind:profile={llmProfileForm}
      selectedProfileId={selectedLLMProfileId}
      providerOptions={llmProviderOptions}
      busy={updateSettings.isPending}
      canSubmit={canSubmitLLMProfile()}
      missingBaseUrl={llmProfileMissingBaseUrl()}
      missingCredential={llmProfileMissingCredential()}
      onclose={closeLLMPanel}
      onproviderchange={updateLLMFormProvider}
      onremove={removeLLMProfile}
      onsubmit={submitLLMProfile}
      />

      <GatewayChannelPanel
      open={gatewayPanelOpen}
      mode={gatewayPanelMode}
      bind:selectedChannelKey={selectedGatewayChannelKey}
      editingChannelKey={gatewayEditingChannelKey}
      availableChannels={availableGatewayChannels()}
      group={gatewayPanelFieldGroup()}
      channelKey={gatewayPanelChannelKey()}
      enabled={gatewayPanelEnabled}
      fieldValues={gatewayPanelFieldValues}
      extraFields={gatewayPanelExtraFields}
      feishuAccounts={gatewayPanelFeishuAccounts}
      busy={updateGatewaySettings.isPending}
      onclose={closeGatewayPanel}
      onselectedchannelchange={setSelectedGatewayChannel}
      onenabledchange={setGatewayPanelEnabled}
      onfieldchange={setGatewayPanelFieldValue}
      onextrafieldchange={setGatewayPanelExtraFieldValue}
      onfeishuaccountschange={setGatewayPanelFeishuAccounts}
      onremove={removeGatewayChannel}
      onsubmit={submitGatewayChannel}
      onregistrationconnected={refreshGatewayAfterRegistration}
      />
    {/if}

    {#if actionMessage}
      <div class="flex items-center gap-2 rounded-lg border border-brand/25 bg-brand/5 px-4 py-2.5 text-sm text-brand animate-fade-in" role="status" aria-live="polite">
        <CircleCheck class="h-4 w-4" />
        {actionMessage}
      </div>
    {/if}

  {/if}
</div>
