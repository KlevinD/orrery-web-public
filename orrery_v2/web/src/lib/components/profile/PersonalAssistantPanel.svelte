<script lang="ts">
  import { BellRing, Bot, Cloud, ImagePlus, Laptop, LoaderCircle, MessageCircle, Plus, Save, ShieldCheck, Trash2 } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import type {
    OwnedAgentRuntimeKind,
    OwnedAgentStatus,
    PersonalAssistantHostingMode,
    PersonalAssistantNotificationPreferencesDTO,
    PersonalAssistantProfileDTO
  } from '#lib/types/api'

  interface Props {
    personalAssistant: PersonalAssistantProfileDTO
    assistantHostingMode?: PersonalAssistantHostingMode
    selectedOwnedAgentId?: string
    selectedWorkspaceGrantId?: string
    assistantSaving?: boolean
    assistantAvatarSaving?: boolean
    personalAssistantNotifications?: PersonalAssistantNotificationPreferencesDTO
    notificationSaving?: boolean
    weixinConnected?: boolean
    readOnly?: boolean
    oncreate?: () => void
    onsave?: () => void
    onavatarchange?: (event: Event) => void
    onavatarremove?: () => void
    onapprovalnotificationschange?: (enabled: boolean) => void
  }

  let {
    personalAssistant,
    assistantHostingMode = $bindable('orrery_managed'),
    selectedOwnedAgentId = $bindable(''),
    selectedWorkspaceGrantId = $bindable(''),
    assistantSaving = false,
    assistantAvatarSaving = false,
    personalAssistantNotifications,
    notificationSaving = false,
    weixinConnected = false,
    readOnly = false,
    oncreate = () => {},
    onsave = () => {},
    onavatarchange = () => {},
    onavatarremove = () => {},
    onapprovalnotificationschange = () => {}
  }: Props = $props()

  let assistantAvatarInput = $state<HTMLInputElement | undefined>()
  let t = $derived(createTranslator($locale))
  const created = $derived(Boolean(personalAssistant.agent_profile_id?.trim()))
  const ownedAgentOptions = $derived([
    {
      label: personalAssistant.owned_agents.length
        ? t('profile.personalAssistant.selectAgent')
        : t('profile.personalAssistant.noAgents'),
      value: ''
    },
    ...personalAssistant.owned_agents.map((agent) => ({
      label: `${agent.display_name} · ${runtimeKindLabel(agent.runtime_kind)} · ${ownedAgentStatusLabel(agent.status)}`,
      value: agent.owned_agent_id
    }))
  ])
  const selectedOwnedAgent = $derived(personalAssistant.owned_agents.find((agent) => agent.owned_agent_id === selectedOwnedAgentId))
  const workspaceGrantOptions = $derived([
    {
      label: selectedOwnedAgent?.workspace_grants.some((grant) => grant.available)
        ? t('profile.personalAssistant.selectGrant')
        : t('profile.personalAssistant.noGrants'),
      value: ''
    },
    ...(selectedOwnedAgent?.workspace_grants ?? [])
      .filter((grant) => grant.available)
      .map((grant) => ({ label: grant.display_name, value: grant.grant_id }))
  ])
  const selectedWorkspaceGrantAvailable = $derived(Boolean(
    selectedOwnedAgent?.workspace_grants.some((grant) => grant.grant_id === selectedWorkspaceGrantId && grant.available)
  ))
  const assistantSourceLabel = $derived(
    assistantHostingMode === 'orrery_managed'
      ? t('profile.personalAssistant.managed')
      : selectedOwnedAgent?.display_name || t('profile.personalAssistant.noAgents')
  )
  const assistantConnectionLabel = $derived(
    assistantHostingMode === 'orrery_managed'
      ? t('profile.personalAssistant.statusOnline')
      : selectedOwnedAgent
        ? ownedAgentStatusLabel(selectedOwnedAgent.status)
        : t('profile.personalAssistant.statusUnavailable')
  )

  function changeOwnedAgent(event: Event & { currentTarget: HTMLSelectElement }) {
    selectedOwnedAgentId = event.currentTarget.value
    selectedWorkspaceGrantId = personalAssistant.owned_agents
      .find((agent) => agent.owned_agent_id === selectedOwnedAgentId)
      ?.workspace_grants.find((grant) => grant.available)?.grant_id ?? ''
  }

  function runtimeKindLabel(kind: OwnedAgentRuntimeKind) {
    const labels: Record<OwnedAgentRuntimeKind, string> = {
      openclaw: 'OpenClaw',
      hermes: 'Hermes',
      codex: 'Codex',
      pi: 'PI',
      aionrs: 'AionRS',
      acp: 'ACP',
      other: t('profile.personalAssistant.runtimeOther')
    }
    return labels[kind]
  }

  function ownedAgentStatusLabel(status: OwnedAgentStatus) {
    const keys: Record<OwnedAgentStatus, TranslationKey> = {
      online: 'profile.personalAssistant.statusOnline',
      offline: 'profile.personalAssistant.statusOffline',
      unavailable: 'profile.personalAssistant.statusUnavailable',
      revoked: 'profile.personalAssistant.statusRevoked'
    }
    return t(keys[status])
  }

  function changeApprovalNotifications(event: Event & { currentTarget: HTMLInputElement }) {
    onapprovalnotificationschange(event.currentTarget.checked)
  }
</script>

{#if !created}
<section id="personal-assistant" class="scroll-mt-16 rounded-xl border border-border bg-card p-5">
  <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
    <div class="flex min-w-0 items-center gap-3">
      <div class="grid h-12 w-12 shrink-0 place-items-center rounded-full bg-muted text-muted-foreground">
        <Bot class="h-5 w-5" />
      </div>
      <div class="min-w-0">
        <h2 class="text-sm font-semibold text-foreground">{t('profile.personalAssistant.title')}</h2>
        <p class="mt-1 text-xs text-muted-foreground">{t('profile.personalAssistant.notCreated')}</p>
      </div>
    </div>
    {#if !readOnly}
      <Button disabled={assistantSaving} onclick={oncreate}>
        {#if assistantSaving}
          <LoaderCircle class="h-4 w-4 animate-spin" />
          {t('profile.personalAssistant.creating')}
        {:else}
          <Plus class="h-4 w-4" />
          {t('profile.personalAssistant.create')}
        {/if}
      </Button>
    {/if}
  </div>
</section>
{:else}
<section id="personal-assistant" class="scroll-mt-16 rounded-xl border border-border bg-card p-5">
  <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
    <div class="flex min-w-0 items-start gap-3">
      <div class="relative h-12 w-12 shrink-0">
        {#if readOnly}
          <div class="grid h-12 w-12 place-items-center overflow-hidden rounded-full bg-emerald-600 text-white">
            {#if personalAssistant.avatar_url}
              <img class="h-full w-full object-cover" src={personalAssistant.avatar_url} alt={personalAssistant.display_name || t('profile.personalAssistant.title')} />
            {:else}
              <Bot class="h-5 w-5" />
            {/if}
          </div>
        {:else}
          <button
            type="button"
            class="group grid h-12 w-12 place-items-center overflow-hidden rounded-full bg-emerald-600 text-white disabled:cursor-wait disabled:opacity-60"
            disabled={assistantAvatarSaving}
            aria-label={t('profile.personalAssistant.changeAvatar')}
            title={t('profile.personalAssistant.changeAvatar')}
            onclick={() => assistantAvatarInput?.click()}
          >
            {#if personalAssistant.avatar_url}
              <img class="h-full w-full object-cover" src={personalAssistant.avatar_url} alt={personalAssistant.display_name || t('profile.personalAssistant.title')} />
            {:else}
              <Bot class="h-5 w-5" />
            {/if}
            <span class="absolute inset-0 grid place-items-center bg-black/45 opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100">
              <ImagePlus class="h-4 w-4" />
            </span>
          </button>
          <input
            bind:this={assistantAvatarInput}
            data-personal-assistant-avatar-input
            class="sr-only"
            type="file"
            accept="image/png,image/jpeg,image/webp"
            aria-label={t('profile.personalAssistant.changeAvatar')}
            onchange={onavatarchange}
          />
        {/if}
      </div>
      <div class="min-w-0">
        <h2 class="text-sm font-semibold text-foreground">{personalAssistant.display_name || t('profile.personalAssistant.title')}</h2>
        <p class="mt-1 text-xs text-muted-foreground">{t('profile.personalAssistant.systemIdentity')}</p>
        <div class="mt-2 inline-flex items-center gap-1.5 text-xs font-medium text-emerald-700 dark:text-emerald-400">
          <ShieldCheck class="h-3.5 w-3.5" />
          {t('profile.personalAssistant.privateToOwner')}
        </div>
      </div>
    </div>
    <div class="flex shrink-0 flex-wrap gap-2">
      {#if !readOnly && personalAssistant.avatar_url}
        <Button variant="ghost" size="icon" disabled={assistantAvatarSaving} onclick={onavatarremove} ariaLabel={t('profile.personalAssistant.removeAvatar')}>
          <Trash2 class="h-4 w-4" />
        </Button>
      {/if}
      <Button variant="outline" href="/discussions?assistant=1">
        <MessageCircle class="h-4 w-4" />
        {t('profile.personalAssistant.openChat')}
      </Button>
      {#if !readOnly}
        <Button disabled={assistantSaving || (assistantHostingMode === 'owned_agent' && (!selectedOwnedAgentId || !selectedWorkspaceGrantAvailable))} onclick={onsave}>
          <Save class="h-4 w-4" />
          {t('profile.personalAssistant.save')}
        </Button>
      {/if}
    </div>
  </div>

  <dl class="mt-4 grid gap-x-5 gap-y-3 text-sm sm:grid-cols-3">
    <div class="border-t border-border pt-3">
      <dt class="text-xs text-muted-foreground">{t('profile.personalAssistant.identity')}</dt>
      <dd class="mt-1 font-medium text-foreground">{personalAssistant.display_name || t('profile.personalAssistant.identityValue')}</dd>
    </div>
    <div class="min-w-0 border-t border-border pt-3">
      <dt class="text-xs text-muted-foreground">{t('profile.personalAssistant.currentSource')}</dt>
      <dd class="mt-1 truncate font-medium text-foreground">{assistantSourceLabel}</dd>
    </div>
    <div class="border-t border-border pt-3">
      <dt class="text-xs text-muted-foreground">{t('profile.personalAssistant.connection')}</dt>
      <dd class="mt-1 flex items-center gap-1.5 font-medium text-foreground">
        <span class="h-1.5 w-1.5 rounded-full {assistantHostingMode === 'orrery_managed' || selectedOwnedAgent?.status === 'online' ? 'bg-emerald-500' : 'bg-muted-foreground/50'}"></span>
        {assistantConnectionLabel}
      </dd>
    </div>
  </dl>

  <div class="mt-4 border-y border-border py-3">
    <p class="text-xs font-medium text-muted-foreground">{t('profile.personalAssistant.agentProfile')}</p>
    <p class="mt-1 break-all text-sm text-foreground">{personalAssistant.agent_profile_id}</p>
  </div>

  <div class="mt-4 border-y border-border py-3">
    <p class="text-xs font-medium text-muted-foreground">{t('profile.personalAssistant.instructions')}</p>
    <p class="mt-1 whitespace-pre-wrap break-words text-sm leading-6 text-foreground">
      {personalAssistant.system_prompt || t('profile.personalAssistant.instructionsEmpty')}
    </p>
  </div>

  {#if !readOnly && personalAssistantNotifications}
    <div class="mt-4 flex items-center gap-3 border-y border-border py-3">
      <BellRing class="h-4 w-4 shrink-0 text-emerald-600" />
      <div class="min-w-0 flex-1">
        <p class="text-sm font-medium text-foreground">{t('profile.personalAssistant.approvalNotifications')}</p>
        <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('profile.personalAssistant.approvalNotificationsDescription')}</p>
        <p class="mt-1 text-xs {weixinConnected ? 'text-emerald-700 dark:text-emerald-400' : 'text-muted-foreground'}">
          {weixinConnected
            ? t('profile.personalAssistant.approvalNotificationsConnected')
            : t('profile.personalAssistant.approvalNotificationsDisconnected')}
        </p>
      </div>
      <label class="relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center">
        <input
          class="peer sr-only"
          type="checkbox"
          role="switch"
          aria-label={t('profile.personalAssistant.approvalNotifications')}
          checked={personalAssistantNotifications.approval_notifications_enabled}
          disabled={notificationSaving}
          onchange={changeApprovalNotifications}
        />
        <span class="absolute inset-0 rounded-full bg-muted transition-colors peer-checked:bg-emerald-600 peer-disabled:cursor-wait peer-disabled:opacity-60"></span>
        <span class="pointer-events-none absolute left-1 h-4 w-4 rounded-full bg-background shadow-sm transition-transform peer-checked:translate-x-5"></span>
      </label>
    </div>
  {/if}

  {#if !readOnly}
  <fieldset class="mt-4">
    <legend class="mb-2 text-xs font-medium text-muted-foreground">{t('profile.personalAssistant.hosting')}</legend>
    <div class="grid grid-cols-2 rounded-md border border-border bg-muted p-0.5">
      <button
        class="inline-flex min-h-10 items-center justify-center gap-2 rounded-sm px-3 text-xs font-medium transition-colors {assistantHostingMode === 'orrery_managed' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
        type="button"
        aria-pressed={assistantHostingMode === 'orrery_managed'}
        onclick={() => { assistantHostingMode = 'orrery_managed' }}
      >
        <Cloud class="h-4 w-4" />
        {t('profile.personalAssistant.managed')}
      </button>
      <button
        class="inline-flex min-h-10 items-center justify-center gap-2 rounded-sm px-3 text-xs font-medium transition-colors {assistantHostingMode === 'owned_agent' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
        type="button"
        aria-pressed={assistantHostingMode === 'owned_agent'}
        onclick={() => { assistantHostingMode = 'owned_agent' }}
      >
        <Laptop class="h-4 w-4" />
        {t('profile.personalAssistant.owned')}
      </button>
    </div>
  </fieldset>

  <div class="mt-4 flex items-start gap-3 border-y border-border py-3">
    <ShieldCheck class="mt-0.5 h-4 w-4 shrink-0 text-emerald-600" />
    <div class="min-w-0">
      <p class="text-sm font-medium text-foreground">
        {assistantHostingMode === 'orrery_managed' ? t('profile.personalAssistant.cloudSecurity') : t('profile.personalAssistant.localSecurity')}
      </p>
      <p class="mt-1 text-xs leading-5 text-muted-foreground">
        {assistantHostingMode === 'orrery_managed' ? t('profile.personalAssistant.cloudSecurityDetail') : t('profile.personalAssistant.localSecurityDetail')}
      </p>
    </div>
  </div>

  {#if assistantHostingMode === 'owned_agent'}
    <div class="mt-4 space-y-3">
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('profile.personalAssistant.agent')}</span>
        <Select bind:value={selectedOwnedAgentId} options={ownedAgentOptions} disabled={assistantSaving || personalAssistant.owned_agents.length === 0} onchange={changeOwnedAgent} />
      </label>
      {#if personalAssistant.owned_agents.length === 0}
        <div class="rounded-md border border-dashed border-border px-3 py-4 text-center text-xs text-muted-foreground">
          {t('profile.personalAssistant.empty')}
        </div>
      {:else}
        <div class="divide-y divide-border border-y border-border">
          {#each personalAssistant.owned_agents as agent}
            <div class="grid gap-2 py-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
              <div class="min-w-0">
                <p class="truncate text-sm font-medium text-foreground">{agent.display_name}</p>
                <p class="mt-0.5 truncate text-xs text-muted-foreground">{runtimeKindLabel(agent.runtime_kind)} · {agent.device_name}</p>
              </div>
              <span class="inline-flex items-center gap-1.5 text-xs {agent.status === 'online' ? 'text-emerald-600' : 'text-muted-foreground'}">
                <span class="h-1.5 w-1.5 rounded-full {agent.status === 'online' ? 'bg-emerald-500' : 'bg-muted-foreground/50'}"></span>
                {ownedAgentStatusLabel(agent.status)}
              </span>
            </div>
          {/each}
        </div>
      {/if}
      <label class="block space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('profile.personalAssistant.grant')}</span>
        <Select bind:value={selectedWorkspaceGrantId} options={workspaceGrantOptions} disabled={assistantSaving || !selectedOwnedAgentId || workspaceGrantOptions.length <= 1} />
      </label>
    </div>
  {/if}
  {/if}
</section>
{/if}
