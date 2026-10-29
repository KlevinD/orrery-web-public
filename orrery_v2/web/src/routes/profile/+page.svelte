<script lang="ts">
  import { page } from '$app/stores'
  import { onDestroy, onMount } from 'svelte'
  import { Building2, Check, CheckCircle2, Link, Link2Off, LogIn, Moon, Palette, RefreshCcw, Sun, UserRound } from 'lucide-svelte'
  import QRCode from 'qrcode'
  import { profileApi } from '#lib/api/profile'
  import PersonalAssistantPanel from '#lib/components/profile/PersonalAssistantPanel.svelte'
  import PersonalWeixinPanel from '#lib/components/profile/PersonalWeixinPanel.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import WorkforceAvatarPicker from '#lib/components/operations/WorkforceAvatarPicker.svelte'
  import { createTranslator, locale, setLocale, type Locale, type TranslationKey } from '#lib/i18n'
  import { useWorkforceAvatarDefaultMutation, useWorkforceAvatarUploadMutation } from '#lib/queries/workforce'
  import { session } from '#lib/stores/session.svelte'
  import { setThemeBrand, setThemeMode, theme, type ThemeBrand } from '#lib/stores/theme.svelte'
  import type { GatewayWeixinLoginStatusResponseDTO, PersonalAssistantHostingMode, PersonalAssistantNotificationPreferencesDTO, PersonalAssistantProfileDTO, ProfileGatewayBindingAccountDTO, ProfileGatewayBindingDTO, ProfileIdentityBindingDTO, ProfileWeixinDTO } from '#lib/types/api'

  interface Props {
    surface?: 'profile' | 'personal-assistant'
  }

  let { surface }: Props = $props()

  type GatewayBindingChannel = 'feishu' | 'dingtalk'

  interface GatewayBindingRow {
    channel: GatewayBindingChannel
    accountId: string
    agentId: string
    agentDisplayName: string
    enabled: boolean
    status: string
  }

  let profile = $state<ProfileWeixinDTO | undefined>()
  let loading = $state(true)
  let saving = $state(false)
  let error = $state('')
  let message = $state('')
  let selectedAgentId = $state('')
  let weixinTargetKind = $state<'agent' | 'personal_assistant'>('personal_assistant')
  let loginStatus = $state('idle')
  let loginMessage = $state('')
  let loginQRCodeUrl = $state('')
  let loginQRCodeDataUrl = $state('')
  let loginSessionKey = $state('')
  let verifyCode = $state('')
  let loginBusy = $state(false)
  let gatewayBindings = $state<ProfileGatewayBindingDTO[]>([])
  let gatewayBindingAccounts = $state<ProfileGatewayBindingAccountDTO[]>([])
  let bindingCommands = $state<Record<string, string>>({})
  let bindingBusy = $state('')
  let feishuIdentity = $state<ProfileIdentityBindingDTO | undefined>()
  let feishuIdentityBusy = $state(false)
  let personalAssistant = $state<PersonalAssistantProfileDTO | undefined>()
  let assistantHostingMode = $state<PersonalAssistantHostingMode>('orrery_managed')
  let selectedOwnedAgentId = $state('')
  let selectedWorkspaceGrantId = $state('')
  let assistantSaving = $state(false)
  let assistantAvatarSaving = $state(false)
  let personalAssistantNotifications = $state<PersonalAssistantNotificationPreferencesDTO | undefined>()
  let notificationSaving = $state(false)
  let loginTimer: ReturnType<typeof setTimeout> | undefined
  let t = $derived(createTranslator($locale))
  const activeSurface = $derived(
    surface ?? ($page.url.searchParams.get('section') === 'personal-assistant' ? 'personal-assistant' : 'profile')
  )
  const personalAssistantSurface = $derived(activeSurface === 'personal-assistant')
  const personalAssistantWeixinConnected = $derived(Boolean(profile?.weixin_bound && profile.target_kind === 'personal_assistant'))
  const avatarUploadMutation = useWorkforceAvatarUploadMutation()
  const avatarDefaultMutation = useWorkforceAvatarDefaultMutation()
  const currentAvatarProfileId = $derived((session.user.agentProfileId || session.actorId || '').trim())
  const avatarUploading = $derived(avatarUploadMutation.isPending && avatarUploadMutation.variables?.agentProfileId === currentAvatarProfileId)
  const avatarChanging = $derived(avatarUploading || (avatarDefaultMutation.isPending && avatarDefaultMutation.variables?.agentProfileId === currentAvatarProfileId))

  const brandOptions: Array<{ value: ThemeBrand; color: string; label: TranslationKey }> = [
    { value: 'default', color: '#5b5bd6', label: 'topbar.brand.default' },
    { value: 'ocean', color: '#12b8b0', label: 'topbar.brand.ocean' },
    { value: 'forest', color: '#16a34a', label: 'topbar.brand.forest' },
    { value: 'purple', color: '#7c3aed', label: 'topbar.brand.purple' }
  ]
  const gatewayBindingRows = $derived.by(() => {
    const rows: GatewayBindingRow[] = gatewayBindingAccounts.map((account) => ({
      channel: 'feishu',
      accountId: account.account_id,
      agentId: account.agent_id,
      agentDisplayName: account.agent_display_name || account.agent_id,
      enabled: account.enabled,
      status: account.status
    }))
    for (const binding of gatewayBindings) {
      if (binding.channel_key !== 'feishu' || rows.some((row) => row.channel === 'feishu' && row.accountId === binding.account_id)) continue
      rows.push({
        channel: 'feishu',
        accountId: binding.account_id,
        agentId: binding.default_agent_id || binding.active_agent_id || '',
        agentDisplayName: binding.default_agent_id || binding.active_agent_id || '',
        enabled: false,
        status: 'unavailable'
      })
    }
    const dingtalkBinding = gatewayBindings.find((binding) => binding.channel_key === 'dingtalk')
    const dingtalkAgent = (profile?.agents ?? []).find((agent) => agent.agent_id === selectedAgentId)
    rows.sort((left, right) => left.accountId.localeCompare(right.accountId))
    rows.push({
      channel: 'dingtalk',
      accountId: dingtalkBinding?.account_id || '',
      agentId: selectedAgentId.trim(),
      agentDisplayName: dingtalkAgent?.display_name || selectedAgentId.trim(),
      enabled: true,
      status: 'connected'
    })
    return rows
  })
  const languageOptions = $derived([
    { label: t('topbar.language.zh'), value: 'zh-CN' },
    { label: t('topbar.language.en'), value: 'en-US' }
  ])
  const selectedWorkspaceGrantAvailable = $derived(Boolean(
    personalAssistant?.owned_agents
      .find((agent) => agent.owned_agent_id === selectedOwnedAgentId)
      ?.workspace_grants.some((grant) => grant.grant_id === selectedWorkspaceGrantId && grant.available)
  ))

  onMount(() => {
    if (!personalAssistantSurface && new URL(window.location.href).searchParams.get('feishu_sso') === 'bound') {
      message = t('profile.feishuSso.boundMessage')
    }
    void loadProfile()
  })

  onDestroy(() => clearLoginTimer())

  $effect(() => {
    const url = loginQRCodeUrl
    let cancelled = false
    if (!url) {
      loginQRCodeDataUrl = ''
      return
    }
    QRCode.toDataURL(url, { width: 220, margin: 1, errorCorrectionLevel: 'M' })
      .then((value) => {
        if (!cancelled) loginQRCodeDataUrl = value
      })
      .catch(() => {
        if (!cancelled) loginQRCodeDataUrl = ''
      })
    return () => {
      cancelled = true
    }
  })

  async function loadProfile() {
    loading = true
    error = ''
    const failures: string[] = []

    if (personalAssistantSurface) {
      const [weixinResult, personalAssistantResult, notificationsResult] = await Promise.allSettled([
        profileApi.getWeixin(),
        profileApi.getPersonalAssistant(),
        profileApi.getPersonalAssistantNotifications()
      ])
      if (weixinResult.status === 'fulfilled') {
        applyProfile(weixinResult.value)
      } else {
        failures.push(loadErrorMessage(weixinResult.reason))
      }
      if (personalAssistantResult.status === 'fulfilled') {
        applyPersonalAssistant(personalAssistantResult.value)
      } else {
        failures.push(loadErrorMessage(personalAssistantResult.reason))
      }
      if (notificationsResult.status === 'fulfilled') {
        personalAssistantNotifications = notificationsResult.value
      } else {
        failures.push(loadErrorMessage(notificationsResult.reason))
      }
      error = failures.join(' ')
      loading = false
      return
    }

    const [weixinResult, bindingsResult, feishuIdentityResult, personalAssistantResult, notificationsResult] = await Promise.allSettled([
      profileApi.getWeixin(),
      profileApi.getGatewayBindings(),
      profileApi.getFeishuIdentity(),
      profileApi.getPersonalAssistant(),
      profileApi.getPersonalAssistantNotifications()
    ])
    if (weixinResult.status === 'fulfilled') {
      applyProfile(weixinResult.value)
    } else {
      failures.push(loadErrorMessage(weixinResult.reason))
    }
    if (bindingsResult.status === 'fulfilled') {
      gatewayBindings = bindingsResult.value.bindings ?? []
      gatewayBindingAccounts = bindingsResult.value.accounts ?? []
    } else {
      failures.push(loadErrorMessage(bindingsResult.reason))
    }
    if (feishuIdentityResult.status === 'fulfilled') {
      feishuIdentity = feishuIdentityResult.value
    } else {
      failures.push(loadErrorMessage(feishuIdentityResult.reason))
    }
    if (personalAssistantResult.status === 'fulfilled') {
      applyPersonalAssistant(personalAssistantResult.value)
    } else {
      failures.push(loadErrorMessage(personalAssistantResult.reason))
    }
    if (notificationsResult.status === 'fulfilled') {
      personalAssistantNotifications = notificationsResult.value
    } else {
      failures.push(loadErrorMessage(notificationsResult.reason))
    }
    error = failures.join(' ')
    loading = false
  }

  async function unbindFeishuIdentity() {
    if (!confirm(t('profile.feishuSso.unbindConfirm'))) return
    feishuIdentityBusy = true
    error = ''
    message = ''
    try {
      feishuIdentity = await profileApi.unbindFeishuIdentity()
      message = t('profile.feishuSso.unboundMessage')
    } catch (err) {
      error = err instanceof Error ? err.message : t('profile.feishuSso.unbindFailed')
    } finally {
      feishuIdentityBusy = false
    }
  }

  function loadErrorMessage(reason: unknown) {
    return reason instanceof Error ? reason.message : t('profile.loadFailed')
  }

  function gatewayBinding(row: GatewayBindingRow) {
    return gatewayBindings.find((binding) => binding.channel_key === row.channel && (row.channel === 'dingtalk' || binding.account_id === row.accountId))
  }

  function gatewayBindingKey(row: GatewayBindingRow) {
    return `${row.channel}:${row.accountId || 'default'}`
  }

  function gatewayBindingStatus(row: GatewayBindingRow) {
    if (!row.enabled) return t('profile.gatewayBinding.status.disabled')
    switch (row.status) {
      case 'connected': return t('profile.gatewayBinding.status.connected')
      case 'degraded': return t('profile.gatewayBinding.status.degraded')
      default: return t('profile.gatewayBinding.status.unavailable')
    }
  }

  function gatewayBindingCanCreate(row: GatewayBindingRow) {
    if (row.channel === 'dingtalk') return Boolean(selectedAgentId.trim())
    return row.enabled && row.status === 'connected' && Boolean(row.accountId && row.agentId)
  }

  async function createGatewayBindingCode(row: GatewayBindingRow) {
    const agentId = row.channel === 'dingtalk' ? selectedAgentId.trim() : row.agentId.trim()
    if (!agentId) {
      message = t('profile.weixin.chooseBot')
      return
    }
    const key = gatewayBindingKey(row)
    bindingBusy = key
    error = ''
    message = ''
    try {
      const result = await profileApi.createGatewayBindingCode(row.channel, row.accountId, agentId)
      bindingCommands = { ...bindingCommands, [key]: result.command }
      message = t('profile.gatewayBinding.codeReady')
    } catch (err) {
      error = err instanceof Error ? err.message : t('profile.gatewayBinding.codeFailed')
    } finally {
      bindingBusy = ''
    }
  }

  async function unbindGateway(row: GatewayBindingRow) {
    const binding = gatewayBinding(row)
    if (!binding || !confirm(t('profile.gatewayBinding.unbindConfirm'))) return
    const key = gatewayBindingKey(row)
    bindingBusy = key
    error = ''
    message = ''
    try {
      await profileApi.deleteGatewayBinding(binding.id)
      gatewayBindings = gatewayBindings.filter((candidate) => candidate.id !== binding.id)
      bindingCommands = { ...bindingCommands, [key]: '' }
      await loadProfile()
      message = t('profile.gatewayBinding.unbound')
    } catch (err) {
      error = err instanceof Error ? err.message : t('profile.gatewayBinding.unbindFailed')
    } finally {
      bindingBusy = ''
    }
  }

  function applyProfile(next: ProfileWeixinDTO) {
    profile = next
    selectedAgentId = next.default_agent_id || next.current_agent_id || next.agents[0]?.agent_id || ''
    weixinTargetKind = personalAssistantSurface ? 'personal_assistant' : (next.target_kind || 'personal_assistant')
  }

  function applyPersonalAssistant(next: PersonalAssistantProfileDTO) {
    personalAssistant = next
    assistantHostingMode = next.hosting_mode
    selectedOwnedAgentId = next.owned_agent_id ?? ''
    selectedWorkspaceGrantId = next.workspace_grant_id ?? ''
  }

  async function createPersonalAssistant() {
    if (!personalAssistant || personalAssistant.agent_profile_id?.trim()) return
    assistantSaving = true
    error = ''
    message = ''
    try {
      applyPersonalAssistant(await profileApi.createPersonalAssistant())
      message = t('profile.personalAssistant.created')
    } catch (err) {
      error = err instanceof Error ? err.message : t('profile.personalAssistant.createFailed')
    } finally {
      assistantSaving = false
    }
  }

  async function savePersonalAssistant() {
    if (!personalAssistant?.agent_profile_id?.trim()) return
    if (assistantHostingMode === 'owned_agent' && !selectedOwnedAgentId) {
      error = t('profile.personalAssistant.chooseAgent')
      return
    }
    if (assistantHostingMode === 'owned_agent' && !selectedWorkspaceGrantAvailable) {
      error = t('profile.personalAssistant.chooseGrant')
      return
    }
    assistantSaving = true
    error = ''
    message = ''
    try {
      applyPersonalAssistant(await profileApi.updatePersonalAssistant({
        assistant_enabled: true,
        assigned_agent_id: personalAssistant.assigned_agent_id,
        hosting_mode: assistantHostingMode,
        owned_agent_id: assistantHostingMode === 'owned_agent' ? selectedOwnedAgentId : undefined,
        workspace_grant_id: assistantHostingMode === 'owned_agent' ? selectedWorkspaceGrantId : undefined,
        fallback_to_managed: false,
        revision: personalAssistant.revision
      }))
      message = t('profile.personalAssistant.saved')
    } catch (err) {
      error = err instanceof Error ? err.message : t('profile.personalAssistant.saveFailed')
    } finally {
      assistantSaving = false
    }
  }

  async function changeApprovalNotifications(enabled: boolean) {
    if (!personalAssistantNotifications || notificationSaving) return
    notificationSaving = true
    error = ''
    message = ''
    try {
      personalAssistantNotifications = await profileApi.updatePersonalAssistantNotifications({
        approval_notifications_enabled: enabled,
        revision: personalAssistantNotifications.revision
      })
      message = t('profile.personalAssistant.approvalNotificationsSaved')
    } catch (err) {
      error = err instanceof Error ? err.message : t('profile.personalAssistant.approvalNotificationsSaveFailed')
    } finally {
      notificationSaving = false
    }
  }

  async function changePersonalAssistantAvatar(event: Event) {
    const input = event.currentTarget as HTMLInputElement
    const file = input.files?.[0]
    input.value = ''
    if (!file || !personalAssistant) return
    error = ''
    message = ''
    if (file.type && !['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) {
      error = t('people.avatarInvalidType')
      return
    }
    if (file.size > 5 * 1024 * 1024) {
      error = t('people.avatarTooLarge')
      return
    }
    assistantAvatarSaving = true
    try {
      const avatar = await profileApi.uploadPersonalAssistantAvatar(file)
      personalAssistant = { ...personalAssistant, avatar_url: avatar.avatar_url, avatar_etag: avatar.etag }
      message = t('profile.personalAssistant.avatarUploaded')
    } catch (err) {
      error = err instanceof Error ? err.message : t('profile.personalAssistant.avatarUploadFailed')
    } finally {
      assistantAvatarSaving = false
    }
  }

  async function removePersonalAssistantAvatar() {
    if (!personalAssistant?.avatar_url || assistantAvatarSaving) return
    assistantAvatarSaving = true
    error = ''
    message = ''
    try {
      await profileApi.deletePersonalAssistantAvatar()
      personalAssistant = { ...personalAssistant, avatar_url: undefined, avatar_etag: undefined }
      message = t('profile.personalAssistant.avatarRemoved')
    } catch (err) {
      error = err instanceof Error ? err.message : t('profile.personalAssistant.avatarRemoveFailed')
    } finally {
      assistantAvatarSaving = false
    }
  }

  function uploadAvatar(file: File) {
    if (!file) return
    if (!currentAvatarProfileId) {
      error = t('profile.notBound')
      return
    }
    error = ''
    message = ''
    if (file.type && !['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) {
      error = t('people.avatarInvalidType')
      return
    }
    if (file.size > 5 * 1024 * 1024) {
      error = t('people.avatarTooLarge')
      return
    }
    avatarUploadMutation.mutate({ agentProfileId: currentAvatarProfileId, file }, {
      onSuccess: () => {
        message = t('people.avatarUploaded')
      },
      onError: (reason) => {
        error = reason instanceof Error ? reason.message : t('people.avatarUploadFailed')
      }
    })
  }

  function selectDefaultAvatar(defaultId: string) {
    if (!currentAvatarProfileId) {
      error = t('profile.notBound')
      return
    }
    error = ''
    message = ''
    avatarDefaultMutation.mutate({ agentProfileId: currentAvatarProfileId, defaultId }, {
      onSuccess: () => {
        message = t('people.avatarDefaultUpdated')
      },
      onError: (reason) => {
        error = reason instanceof Error ? reason.message : t('people.avatarDefaultUpdateFailed')
      }
    })
  }

  async function saveWeixinTarget() {
    const agentId = selectedAgentId.trim()
    if (weixinTargetKind === 'agent' && !agentId) {
      message = t('profile.weixin.chooseBot')
      return
    }
    if (weixinTargetKind === 'personal_assistant' && !personalAssistant?.agent_profile_id?.trim()) {
      message = t('profile.weixin.chooseAssistantAgent')
      return
    }
    saving = true
    message = ''
    error = ''
    try {
      const next = weixinTargetKind === 'agent'
        ? await profileApi.setWeixinDefaultAgent(agentId)
        : await profileApi.setWeixinTarget('personal_assistant')
      applyProfile(next)
      message = t('profile.weixin.targetSaved')
    } catch (err) {
      error = err instanceof Error ? err.message : t('profile.weixin.defaultBotSaveFailed')
    } finally {
      saving = false
    }
  }

  async function startWeixinBinding() {
    const agentId = selectedAgentId.trim()
    if (weixinTargetKind === 'agent' && !agentId) {
      message = t('profile.weixin.chooseBotBeforeScan')
      return
    }
    if (weixinTargetKind === 'personal_assistant' && !personalAssistant?.agent_profile_id?.trim()) {
      message = t('profile.weixin.chooseAssistantAgent')
      return
    }
    resetLoginState()
    loginBusy = true
    loginStatus = 'starting'
    loginMessage = t('people.weixinBinding.loginStarting')
    error = ''
    message = ''
    try {
      const result = await profileApi.startWeixinLogin({
        target_kind: weixinTargetKind,
        agent_id: weixinTargetKind === 'agent' ? agentId : undefined
      })
      loginSessionKey = result.session_key
      loginQRCodeUrl = result.qrcode_url
      loginStatus = result.status || 'waiting'
      loginMessage = result.message || t('profile.weixin.scanPrompt')
      scheduleLoginPoll(800)
    } catch (err) {
      loginStatus = 'error'
      loginMessage = err instanceof Error ? err.message : t('people.weixinBinding.loginFailed')
    } finally {
      loginBusy = false
    }
  }

  async function unbindWeixin() {
    if (!confirm(t('profile.weixin.unbindConfirm'))) return
    saving = true
    message = ''
    error = ''
    try {
      applyProfile(await profileApi.unbindWeixin())
      resetLoginState()
      message = t('profile.weixin.unbound')
    } catch (err) {
      error = err instanceof Error ? err.message : t('profile.weixin.unbindFailed')
    } finally {
      saving = false
    }
  }

  function clearLoginTimer() {
    if (!loginTimer) return
    clearTimeout(loginTimer)
    loginTimer = undefined
  }

  function resetLoginState() {
    clearLoginTimer()
    loginStatus = 'idle'
    loginMessage = ''
    loginQRCodeUrl = ''
    loginQRCodeDataUrl = ''
    loginSessionKey = ''
    verifyCode = ''
    loginBusy = false
  }

  function shouldPollLogin() {
    return ['waiting', 'wait', 'scaned', 'starting'].includes(loginStatus)
  }

  function scheduleLoginPoll(delayMs: number) {
    clearLoginTimer()
    if (!loginSessionKey || !shouldPollLogin()) return
    loginTimer = setTimeout(() => {
      void pollWeixinLogin()
    }, delayMs)
  }

  async function pollWeixinLogin() {
    if (!loginSessionKey || loginBusy) return
    loginBusy = true
    try {
      applyLoginStatus(await profileApi.getWeixinLoginStatus(loginSessionKey))
    } catch (err) {
      loginStatus = 'error'
      loginMessage = err instanceof Error ? err.message : t('people.weixinBinding.loginFailed')
    } finally {
      loginBusy = false
    }
  }

  function applyLoginStatus(result: GatewayWeixinLoginStatusResponseDTO) {
    loginStatus = result.status || 'waiting'
    loginMessage = result.message || loginMessage
    loginQRCodeUrl = result.qrcode_url || loginQRCodeUrl
    if (result.connected) {
      message = t('profile.weixin.connected')
      resetLoginState()
      void loadProfile()
      return
    }
    if (result.already_connected || ['expired', 'verify_code_blocked', 'error'].includes(loginStatus)) return
    if (loginStatus === 'need_verifycode') return
    scheduleLoginPoll(1200)
  }

  async function submitVerifyCode() {
    if (!loginSessionKey || !verifyCode.trim()) return
    loginBusy = true
    try {
      const result = await profileApi.submitWeixinVerifyCode(loginSessionKey, verifyCode)
      verifyCode = ''
      loginStatus = 'waiting'
      loginMessage = result.message || t('people.weixinBinding.verifySubmitted')
      scheduleLoginPoll(100)
    } catch (err) {
      loginStatus = 'error'
      loginMessage = err instanceof Error ? err.message : t('profile.weixin.verifySubmitFailed')
    } finally {
      loginBusy = false
    }
  }
</script>

<svelte:head>
  <title>{t('profile.title')} | Orrery</title>
</svelte:head>

<div class="page-shell max-w-[720px] space-y-5">
  <nav class="inline-flex rounded-lg border border-border bg-muted p-1" aria-label={t('profile.title')}>
    <a
      class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {personalAssistantSurface ? 'text-muted-foreground hover:text-foreground' : 'bg-card text-foreground shadow-sm'}"
      href="/profile"
      aria-current={personalAssistantSurface ? undefined : 'page'}
    >
      {t('profile.title')}
    </a>
    <a
      class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {personalAssistantSurface ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
      href="/profile?section=personal-assistant"
      aria-current={personalAssistantSurface ? 'page' : undefined}
    >
      {t('profile.personalAssistant.title')}
    </a>
  </nav>

  <header class="page-header">
    <div>
      <h1 class="page-title">{personalAssistantSurface ? t('profile.personalAssistant.title') : t('profile.title')}</h1>
      <p class="page-description">{personalAssistantSurface ? t('profile.personalAssistant.pageDescription') : t('profile.description')}</p>
    </div>
    <Button variant="outline" disabled={loading} onclick={() => { void loadProfile() }}>
      <RefreshCcw class="h-4 w-4" />
      {t('profile.refresh')}
    </Button>
  </header>

  {#if error}
    <div class="rounded-md border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive" role="alert" aria-live="assertive">
      {error}
    </div>
  {/if}
  {#if message}
    <div class="rounded-md border border-border bg-muted/30 px-4 py-3 text-sm text-muted-foreground" role="status" aria-live="polite">
      {message}
    </div>
  {/if}

  {#if !personalAssistantSurface}
  <section class="rounded-xl border border-border bg-card p-5">
      <h2 class="sr-only">{t('profile.account')}</h2>
      <div class="flex flex-col gap-4 sm:flex-row sm:items-center">
        <div class="h-16 w-16 shrink-0">
          <WorkforceAvatarPicker
            profileId={currentAvatarProfileId}
            name={session.displayName || session.user.displayName || session.username || 'User'}
            human={true}
            class="h-16 w-16 rounded-full"
            textClass="text-[22px]"
            disabled={avatarChanging}
            onupload={uploadAvatar}
            ondefaultselect={selectDefaultAvatar}
          />
        </div>
        <div class="min-w-0 flex-1">
          <h3 class="truncate text-base font-semibold text-foreground">{session.displayName || session.user.displayName || t('profile.emptyValue')}</h3>
          <p class="mt-1 break-all text-xs text-muted-foreground">{session.user.email || session.username || t('profile.emptyValue')}</p>
        </div>
        <div class="flex items-center gap-2 text-xs text-muted-foreground">
          <UserRound class="h-4 w-4" />
          <span>{session.user.role || t('profile.emptyValue')}</span>
        </div>
      </div>
      <p class="mt-4 border-t border-border pt-4 text-xs leading-5 text-muted-foreground">{t('profile.account.description')}</p>
      <dl class="mt-4 grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
        <div class="min-w-0 border-t border-border pt-3">
          <dt class="text-xs text-muted-foreground">{t('people.username')}</dt>
          <dd class="mt-1 break-all text-foreground">{session.username || session.actorId || t('profile.emptyValue')}</dd>
        </div>
        <div class="min-w-0 border-t border-border pt-3">
          <dt class="text-xs text-muted-foreground">{t('profile.userId')}</dt>
          <dd class="mt-1 break-all text-foreground">{session.user.userId || session.actorId || t('profile.emptyValue')}</dd>
        </div>
        <div class="min-w-0 border-t border-border pt-3">
          <dt class="text-xs text-muted-foreground">{t('profile.humanId')}</dt>
          <dd class="mt-1 break-all text-foreground">{profile?.human_id || session.user.agentProfileId || t('profile.notBound')}</dd>
        </div>
        <div class="min-w-0 border-t border-border pt-3">
          <dt class="text-xs text-muted-foreground">{t('profile.tenantId')}</dt>
          <dd class="mt-1 break-all text-foreground">{profile?.tenant_id || t('profile.emptyValue')}</dd>
        </div>
      </dl>
  </section>
  {#if personalAssistant}
    <PersonalAssistantPanel
      {personalAssistant}
      bind:assistantHostingMode
      bind:selectedOwnedAgentId
      bind:selectedWorkspaceGrantId
      {assistantSaving}
      {assistantAvatarSaving}
      oncreate={() => { void createPersonalAssistant() }}
      {personalAssistantNotifications}
      {notificationSaving}
      weixinConnected={personalAssistantWeixinConnected}
      onsave={() => { void savePersonalAssistant() }}
      onavatarchange={changePersonalAssistantAvatar}
      onavatarremove={() => { void removePersonalAssistantAvatar() }}
      onapprovalnotificationschange={(enabled) => { void changeApprovalNotifications(enabled) }}
    />
  {/if}
  {/if}

  {#if personalAssistantSurface && personalAssistant}
    <PersonalAssistantPanel
      {personalAssistant}
      bind:assistantHostingMode
      bind:selectedOwnedAgentId
      bind:selectedWorkspaceGrantId
      {assistantSaving}
      {assistantAvatarSaving}
      oncreate={() => { void createPersonalAssistant() }}
      {personalAssistantNotifications}
      {notificationSaving}
      weixinConnected={personalAssistantWeixinConnected}
      onsave={() => { void savePersonalAssistant() }}
      onavatarchange={changePersonalAssistantAvatar}
      onavatarremove={() => { void removePersonalAssistantAvatar() }}
      onapprovalnotificationschange={(enabled) => { void changeApprovalNotifications(enabled) }}
    />
  {/if}


  {#if !personalAssistantSurface && feishuIdentity?.configured}
    <section class="rounded-xl border border-border bg-card p-5">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div class="flex min-w-0 items-start gap-3">
          <div class="grid h-10 w-10 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground">
            <Building2 class="h-5 w-5" />
          </div>
          <div class="min-w-0">
            <h2 class="text-sm font-semibold text-foreground">{t('profile.feishuSso.title')}</h2>
            <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('profile.feishuSso.description')}</p>
          </div>
        </div>
        <div class="flex shrink-0 flex-wrap gap-2">
          {#if feishuIdentity.bound}
            <Button variant="outline" disabled={feishuIdentityBusy} onclick={() => { void unbindFeishuIdentity() }}>
              <Link2Off class="h-4 w-4" />
              {t('profile.feishuSso.unbind')}
            </Button>
          {:else}
            <Button href="/api/profile/identity/feishu/begin?return_to=%2Fprofile">
              <LogIn class="h-4 w-4" />
              {t('profile.feishuSso.bind')}
            </Button>
          {/if}
        </div>
      </div>
      <dl class="mt-4 grid gap-x-6 gap-y-3 text-sm sm:grid-cols-3">
        <div class="border-t border-border pt-3">
          <dt class="text-xs text-muted-foreground">{t('profile.feishuSso.status')}</dt>
          <dd class="mt-1 flex items-center gap-2 text-foreground">
            {#if feishuIdentity.bound}
              <CheckCircle2 class="h-4 w-4 text-emerald-600" />
              {t('profile.feishuSso.bound')}
            {:else}
              {t('profile.feishuSso.notBound')}
            {/if}
          </dd>
        </div>
        <div class="min-w-0 border-t border-border pt-3">
          <dt class="text-xs text-muted-foreground">{t('profile.feishuSso.account')}</dt>
          <dd class="mt-1 truncate text-foreground">{feishuIdentity.display_name || t('profile.emptyValue')}</dd>
        </div>
        <div class="min-w-0 border-t border-border pt-3">
          <dt class="text-xs text-muted-foreground">{t('profile.feishuSso.email')}</dt>
          <dd class="mt-1 break-all text-foreground">{feishuIdentity.email || t('profile.emptyValue')}</dd>
        </div>
      </dl>
    </section>
  {/if}

  {#if !personalAssistantSurface}
  <section class="rounded-xl border border-border bg-card p-5">
      <div class="flex items-start gap-3">
        <div class="grid h-10 w-10 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground">
          <Palette class="h-5 w-5" />
        </div>
        <div class="min-w-0">
          <h2 class="text-sm font-semibold text-foreground">{t('profile.appearance')}</h2>
          <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('profile.appearance.description')}</p>
        </div>
      </div>

      <div class="mt-4 grid gap-5 sm:grid-cols-2 lg:grid-cols-[auto_12rem_minmax(0,1fr)]">
        <fieldset class="min-w-0">
          <legend class="mb-1.5 text-xs font-medium text-muted-foreground">{t('profile.appearance.mode')}</legend>
          <div class="inline-grid h-9 grid-cols-2 rounded-md border border-border bg-muted p-0.5">
            <button
              class="inline-flex h-8 min-w-24 items-center justify-center gap-2 rounded-sm px-3 text-xs font-medium transition-colors {theme.mode === 'light' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
              type="button"
              aria-pressed={theme.mode === 'light'}
              onclick={() => setThemeMode('light')}
            >
              <Sun class="h-4 w-4" />
              {t('profile.appearance.light')}
            </button>
            <button
              class="inline-flex h-8 min-w-24 items-center justify-center gap-2 rounded-sm px-3 text-xs font-medium transition-colors {theme.mode === 'dark' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
              type="button"
              aria-pressed={theme.mode === 'dark'}
              onclick={() => setThemeMode('dark')}
            >
              <Moon class="h-4 w-4" />
              {t('profile.appearance.dark')}
            </button>
          </div>
        </fieldset>

        <label class="min-w-0 space-y-1.5">
          <span class="block text-xs font-medium text-muted-foreground">{t('topbar.language')}</span>
          <Select value={$locale} options={languageOptions} onchange={(event) => setLocale(event.currentTarget.value as Locale)} />
        </label>

        <fieldset class="min-w-0">
          <legend class="mb-1.5 text-xs font-medium text-muted-foreground">{t('profile.appearance.brand')}</legend>
          <div class="flex h-9 items-center gap-2">
            {#each brandOptions as option}
              <button
                class="relative grid h-9 w-9 shrink-0 place-items-center rounded-md border transition-colors {theme.brand === option.value ? 'border-foreground bg-muted' : 'border-border hover:bg-muted'}"
                type="button"
                aria-label={t(option.label)}
                aria-pressed={theme.brand === option.value}
                title={t(option.label)}
                onclick={() => setThemeBrand(option.value)}
              >
                <span class="h-5 w-5 rounded-full border border-black/10" style={`background-color: ${option.color}`}></span>
                {#if theme.brand === option.value}
                  <Check class="pointer-events-none absolute inset-0 m-auto h-3 w-3 text-white drop-shadow" />
                {/if}
              </button>
            {/each}
          </div>
        </fieldset>
      </div>
  </section>
  {/if}

  {#if loading}
    <section class="rounded-xl border border-border bg-card p-5 text-sm text-muted-foreground" role="status" aria-live="polite" aria-busy="true">
      {t('profile.loading')}
    </section>
  {:else}
    {#if !personalAssistantSurface || personalAssistant?.agent_profile_id?.trim()}
    <PersonalWeixinPanel
      {profile}
      {personalAssistant}
      personalAssistantOnly={personalAssistantSurface}
      bind:selectedAgentId
      bind:weixinTargetKind
      {saving}
      {loginBusy}
      {loginStatus}
      {loginMessage}
      {loginQRCodeUrl}
      {loginQRCodeDataUrl}
      bind:verifyCode
      onscan={() => { void startWeixinBinding() }}
      onunbind={() => { void unbindWeixin() }}
      onsavetarget={() => { void saveWeixinTarget() }}
      onsubmitverify={() => { void submitVerifyCode() }}
    />
    {/if}

    {#if !personalAssistantSurface}
    <section class="rounded-xl border border-border bg-card p-5">
      <div class="flex items-start gap-3">
        <div class="grid h-10 w-10 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground">
          <Link class="h-5 w-5" />
        </div>
        <div class="min-w-0">
          <h2 class="text-sm font-semibold text-foreground">{t('profile.gatewayBinding.title')}</h2>
          <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('profile.gatewayBinding.description')}</p>
        </div>
      </div>

      {#if !gatewayBindingRows.some((row) => row.channel === 'feishu')}
        <p class="mt-4 border-y border-border py-3 text-xs text-muted-foreground">{t('profile.gatewayBinding.noFeishuAccounts')}</p>
      {/if}
      <div class="mt-4 divide-y divide-border border-y border-border">
        {#each gatewayBindingRows as row (gatewayBindingKey(row))}
          {@const binding = gatewayBinding(row)}
          {@const rowKey = gatewayBindingKey(row)}
          <div class="grid gap-3 py-4 md:grid-cols-[9rem_minmax(0,1fr)_auto] md:items-center">
            <div>
              <p class="text-sm font-medium text-foreground">
                {row.channel === 'feishu' ? t('profile.gatewayBinding.feishu') : t('profile.gatewayBinding.dingtalk')}{row.accountId ? ` · ${row.accountId}` : ''}
              </p>
              <p class="mt-0.5 text-xs text-muted-foreground">{binding ? t('profile.gatewayBinding.bound') : gatewayBindingStatus(row)}</p>
            </div>
            <div class="min-w-0 text-xs text-muted-foreground">
              {#if bindingCommands[rowKey]}
                <code class="block break-all rounded-sm bg-muted px-2 py-1.5 text-foreground">{bindingCommands[rowKey]}</code>
              {:else if binding}
                <p class="truncate text-foreground">{binding.display_name || binding.external_user_id}</p>
                <p class="truncate">{binding.external_tenant_id || binding.account_id}</p>
              {:else}
                <p>{t('profile.gatewayBinding.pending')}</p>
              {/if}
              {#if row.agentDisplayName}
                <p class="mt-1 truncate">{t('profile.gatewayBinding.employee', { name: row.agentDisplayName })}</p>
              {/if}
            </div>
            <div class="flex flex-wrap gap-2 md:justify-end">
              <Button
                variant="outline"
                ariaLabel={`${binding ? t('profile.gatewayBinding.rebind') : t('profile.gatewayBinding.bind')} ${row.accountId || 'dingtalk'}`}
                disabled={bindingBusy === rowKey || !gatewayBindingCanCreate(row)}
                onclick={() => { void createGatewayBindingCode(row) }}
              >
                <Link class="h-4 w-4" />
                {binding ? t('profile.gatewayBinding.rebind') : t('profile.gatewayBinding.bind')}
              </Button>
              {#if binding}
                <Button
                  variant="outline"
                  ariaLabel={`${t('profile.gatewayBinding.unbind')} ${row.accountId || 'dingtalk'}`}
                  disabled={bindingBusy === rowKey}
                  onclick={() => { void unbindGateway(row) }}
                >
                  <Link2Off class="h-4 w-4" />
                  {t('profile.gatewayBinding.unbind')}
                </Button>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    </section>
    {/if}

  {/if}
</div>
