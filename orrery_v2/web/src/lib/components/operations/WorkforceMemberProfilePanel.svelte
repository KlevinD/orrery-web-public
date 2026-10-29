<script lang="ts">
  import { Bot, Brain, Building2, ClipboardCheck, Database, Edit3, LogOut, Pause, Play, ShieldCheck, Sparkles, User, Wrench, X } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { WorkforceChannelAccountViewModel, WorkforceMemberViewModel } from '#lib/types/view-models'
  import WorkforceAvatarPicker from './WorkforceAvatarPicker.svelte'
  import WorkforceChannelBindingsPanel from './WorkforceChannelBindingsPanel.svelte'
  import WorkforceKnowledgeBasePanel from './WorkforceKnowledgeBasePanel.svelte'
  import WorkforceMemoryPanel from './WorkforceMemoryPanel.svelte'
  import ResponsibilityProfilePanel from './ResponsibilityProfilePanel.svelte'
  import { useAgentResponsibilityProfileQuery } from '#lib/queries/workforce'

  interface Props {
    member: WorkforceMemberViewModel
    organizationName?: string
    departmentName?: string
    positionName?: string
    managerName?: string
    onedit?: (member: WorkforceMemberViewModel) => void
    onenable?: (member: WorkforceMemberViewModel) => void
    ondisable?: (member: WorkforceMemberViewModel) => void
    onrelease?: (member: WorkforceMemberViewModel) => void
    onavatarupload?: (member: WorkforceMemberViewModel, file: File) => void
    onavatardefault?: (member: WorkforceMemberViewModel, defaultId: string) => void
    avatarUploading?: boolean
    busy?: boolean
    onclose?: () => void
    showFeishuConnections?: boolean
    channelAccounts?: WorkforceChannelAccountViewModel[]
    channelBindingsLoading?: boolean
    channelBindingsError?: string
    canManageChannels?: boolean
    channelBusyAccountId?: string
    onchannelassign?: (account: WorkforceChannelAccountViewModel) => void
    onchanneltoggle?: (account: WorkforceChannelAccountViewModel, inboundEnabled: boolean) => void
    onchanneldelete?: (account: WorkforceChannelAccountViewModel) => void
  }

  type ProfileTab = 'profile' | 'skills' | 'tools' | 'knowledge' | 'memory' | 'responsibilities' | 'feishu' | 'roles' | 'employment'

  let {
    member,
    organizationName = '',
    departmentName = '',
    positionName = '',
    managerName = '',
    onedit,
    onenable,
    ondisable,
    onrelease,
    onavatarupload,
    onavatardefault,
    avatarUploading = false,
    busy = false,
    onclose,
    showFeishuConnections = true,
    channelAccounts = [],
    channelBindingsLoading = false,
    channelBindingsError = '',
    canManageChannels = false,
    channelBusyAccountId = '',
    onchannelassign,
    onchanneltoggle,
    onchanneldelete
  }: Props = $props()
  let activeTab = $state<ProfileTab>('profile')
  let discoveredKnowledgeBaseCount = $state<number | undefined>()
  let discoveredMemoryCount = $state<number | undefined>()
  let knowledgeCountMemberId = $state('')
  let t = $derived(createTranslator($locale))
  const isHuman = $derived(member.agentKind.toLowerCase() === 'human')
  const profileTabs = $derived<Array<{ key: ProfileTab; label: string }>>([
    { key: 'profile', label: t('people.profileOverview') },
    { key: 'responsibilities', label: t('people.responsibilities') },
    { key: 'skills', label: t('people.skills') },
    { key: 'tools', label: t('people.tools') },
    { key: 'knowledge', label: t('people.knowledgeBase') },
    { key: 'memory', label: t('people.memory') },
    ...(!isHuman && showFeishuConnections ? [{ key: 'feishu' as const, label: t('people.feishuConnections') }] : []),
    { key: 'roles', label: t('people.roles') },
    { key: 'employment', label: t('people.employmentInformation') }
  ])
  const profileText = $derived(isHuman ? member.resume : member.prompt)
  const profileSummary = $derived(firstAvailable([member.responsibilityBoundary, profileText, member.behaviorBoundary]))
  const memoryCount = $derived(discoveredMemoryCount ?? ((member.personalMemoryRef.memoryProfileId ? 1 : 0) + member.collectiveMemoryRefs.length))
  const knowledgeBaseCount = $derived(discoveredKnowledgeBaseCount ?? (member.knowledgeBaseId?.trim() ? 1 : 0))
  const roleCount = $derived(member.roleLabels.length + (member.functionalRoleLabels?.length ?? 0))
  const currentResponsibilityProfileQuery = useAgentResponsibilityProfileQuery(
    () => member.profileId,
    () => true,
    () => 0
  )
  const responsibilityHistoryProfileQuery = useAgentResponsibilityProfileQuery(
    () => member.profileId,
    () => activeTab === 'responsibilities',
    () => 20
  )
  const responsibilityProfile = $derived(responsibilityHistoryProfileQuery.data ?? currentResponsibilityProfileQuery.data)
  const responsibilityProfileLoading = $derived(!responsibilityProfile && (currentResponsibilityProfileQuery.isLoading || responsibilityHistoryProfileQuery.isLoading))
  const responsibilityProfileFailed = $derived(!responsibilityProfile && (currentResponsibilityProfileQuery.isError || responsibilityHistoryProfileQuery.isError))
  const responsibilityCount = $derived(currentResponsibilityProfileQuery.data?.responsibility_count ?? 0)
  const employmentOrganization = $derived(firstAvailable([organizationName, member.organizationId, member.releasedFromOrganizationId]))
  const employmentDepartment = $derived(firstAvailable([departmentName, member.departmentDisplayName, member.departmentId, member.releasedFromDepartmentId]))
  const employmentPosition = $derived(firstAvailable([positionName, member.positionDisplayName, member.positionId, member.releasedFromPositionId]))

  $effect(() => {
    const currentMemberId = member.profileId
    if (knowledgeCountMemberId === currentMemberId) return
    knowledgeCountMemberId = currentMemberId
    discoveredKnowledgeBaseCount = undefined
    discoveredMemoryCount = undefined
  })

  function firstAvailable(values: Array<string | undefined>) {
    return values.find((value) => value && value !== 'Not provided' && value !== 'Not assigned') ?? ''
  }

  function displayField(value: string | undefined) {
    if (!value || value === 'Not provided' || value === 'Not assigned') return t('people.notConfigured')
    return value
  }

  function compact(parts: Array<string | undefined>) {
    const values = parts.map(displayField).filter((part) => part !== t('people.notConfigured'))
    return values.join(' / ') || t('people.notConfigured')
  }

  function roleSummary(member: WorkforceMemberViewModel) {
    return member.roleLabels.join(' / ')
  }

  function headlineParts(member: WorkforceMemberViewModel) {
    return [isHuman ? member.userAccount.username : member.displayName, member.roleName || member.positionDisplayName]
  }

  function formatJson(value: Record<string, unknown>) {
    return Object.keys(value).length > 0 ? JSON.stringify(value, null, 2) : t('people.notConfigured')
  }

  function placementLabel() {
    return member.placementStatus === 'market' ? t('people.talentMarket') : t('people.internalMember')
  }

  function employmentTypeLabel() {
    switch (member.employmentType) {
      case 'ai_agent':
        return t('people.employmentType.aiAgent')
      case 'human':
        return t('people.employmentType.human')
      case 'system':
        return t('people.employmentType.system')
      default:
        return displayField(member.employmentType)
    }
  }

  function supervisionPolicyLabel() {
    switch (member.supervisionPolicy) {
      case 'managed':
        return t('people.supervision.managed')
      case 'root':
        return t('people.supervision.root')
      case 'supervised':
        return t('people.supervision.supervised')
      default:
        return displayField(member.supervisionPolicy)
    }
  }

  function managerLabel() {
    if (managerName) return managerName
    if (!member.managerId || member.managerId === 'Not assigned') return t('people.noManager')
    return member.managerId
  }

</script>

<section data-member-profile-panel class="overflow-hidden rounded-2xl border border-border bg-card">
  <header data-member-profile-hero class="border-b border-border bg-gradient-to-b from-muted/40 to-background">
    <div class="relative px-6 pb-4 pt-5">
      {#if onclose}
        <div class="absolute right-3 top-3 z-10">
          <Button size="icon" class="!h-8 !w-8 !rounded-lg" variant="ghost" ariaLabel={t('people.closeProfile')} title={t('people.closeProfile')} onclick={onclose}>
            <X class="h-4 w-4" />
          </Button>
        </div>
      {/if}

      <div class="flex flex-col items-stretch gap-4 sm:flex-row sm:items-start sm:justify-between {onclose ? 'pr-7' : ''}">
        <div class="flex min-w-0 flex-1 gap-4">
          <div class="h-20 w-20 shrink-0">
            <WorkforceAvatarPicker
              profileId={member.profileId}
              name={member.displayName}
              human={isHuman}
              class="h-20 w-20 rounded-xl shadow-sm"
              textClass="text-lg"
              disabled={avatarUploading}
              onupload={onavatarupload ? (file) => onavatarupload(member, file) : undefined}
              ondefaultselect={onavatardefault ? (defaultId) => onavatardefault(member, defaultId) : undefined}
            />
          </div>

          <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-2">
            <h2 class="truncate text-[17px] font-semibold text-foreground">{member.displayName}</h2>
            <Badge tone={isHuman ? 'source-human' : 'source-agent'} class="!gap-1 !px-1.5 text-[10px]">
              {#if isHuman}<User class="h-3 w-3" />{:else}<Bot class="h-3 w-3" />{/if}
              {isHuman ? t('people.identityHuman') : t('people.identityAgent')}
            </Badge>
            <span class="inline-flex items-center gap-1 text-xs {member.enabled ? 'text-status-done' : 'text-muted-foreground'}">
              <span class="h-1.5 w-1.5 rounded-full {member.enabled ? 'bg-status-done' : 'bg-muted-foreground/45'}"></span>
              {member.enabled ? t('kpi.enabled') : t('kpi.disabled')}
            </span>
          </div>
          <p class="mt-0.5 text-[12.5px] text-muted-foreground">{compact(headlineParts(member))}</p>
          <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
            <span class="inline-flex items-center gap-1.5">
              <Building2 class="h-3.5 w-3.5 text-muted-foreground" />
              {compact([employmentOrganization, employmentDepartment])}
            </span>
            <span class="inline-flex items-center gap-1.5"><User class="h-3.5 w-3.5 text-muted-foreground" />{t('people.manager')}: {managerLabel()}</span>
          </div>
          {#if member.placementStatus !== 'market' && member.capabilityTags.length > 0}
            <div class="mt-2.5 flex flex-wrap gap-1.5">
              {#each member.capabilityTags.slice(0, 3) as tag}
                <span class="inline-flex items-center gap-1 rounded-md bg-muted px-2 py-0.5 text-[11px] leading-[16.5px] text-muted-foreground"><Sparkles class="h-3 w-3" />{tag}</span>
              {/each}
            </div>
          {/if}
          </div>
        </div>

        {#if onedit || onenable || ondisable || onrelease}
        <div class="flex shrink-0 flex-wrap items-center justify-end gap-1.5 self-end sm:self-auto">
          {#if onedit}
            <Button size="sm" variant="outline" class="!h-auto !rounded-lg !border-border !bg-card !px-2.5 !py-1.5 !text-[12.5px] !leading-[18.75px] hover:!bg-muted" onclick={() => onedit?.(member)}>
              <Edit3 class="h-3.5 w-3.5" />
              {t('people.edit')}
            </Button>
          {/if}
          {#if member.enabled ? ondisable : onenable}
            <Button
              size="icon"
              class="!h-8 !w-8 !rounded-lg !border-border !p-0"
              variant="outline"
              disabled={busy}
              ariaLabel={member.enabled ? t('people.disable') : t('people.enable')}
              title={member.enabled ? t('people.disable') : t('people.enable')}
              onclick={() => (member.enabled ? ondisable?.(member) : onenable?.(member))}
            >
              {#if member.enabled}
                <Pause class="h-4 w-4" />
              {:else}
                <Play class="h-4 w-4" />
              {/if}
            </Button>
          {/if}
          {#if onrelease}
            <Button
              size="icon"
              class="!h-8 !w-8 !rounded-lg !border-border !p-0"
              variant="outline"
              disabled={busy}
              ariaLabel={t('people.releaseToMarket')}
              title={t('people.releaseToMarket')}
              onclick={() => onrelease?.(member)}
            >
              <LogOut class="h-4 w-4" />
            </Button>
          {/if}
        </div>
        {/if}
      </div>

      <div class="mt-3.5">
        <p class="line-clamp-2 text-[13px] leading-relaxed text-foreground/80">{displayField(profileSummary)}</p>
        <p class="mt-1.5 truncate font-mono text-[11.5px] text-muted-foreground/65">{member.profileId}</p>
      </div>

      <div data-member-profile-resources class="mt-5 grid grid-cols-2 divide-x divide-border rounded-xl border border-border bg-card sm:grid-cols-3 xl:grid-cols-6">
        <button type="button" class="flex items-center gap-2.5 px-4 py-3 text-left transition-colors hover:bg-muted" onclick={() => (activeTab = 'responsibilities')}>
          <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-brand/10 text-brand"><ClipboardCheck class="h-4 w-4" /></span>
          <div><p class="text-base font-semibold leading-none tabular-nums text-foreground">{responsibilityCount}</p><p class="mt-1 text-[11px] leading-[16.5px] text-muted-foreground">{t('people.responsibilities')}</p></div>
        </button>
        <button type="button" class="flex items-center gap-2.5 px-4 py-3 text-left transition-colors hover:bg-muted" onclick={() => (activeTab = 'skills')}>
        <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-brand/10 text-brand"><Sparkles class="h-4 w-4" /></span>
        <div><p class="text-base font-semibold leading-none tabular-nums text-foreground">{member.skillRefs.length}</p><p class="mt-1 text-[11px] leading-[16.5px] text-muted-foreground">{t('people.skills')}</p></div>
      </button>
      <button type="button" class="flex items-center gap-2.5 px-4 py-3 text-left transition-colors hover:bg-muted" onclick={() => (activeTab = 'tools')}>
        <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-brand/10 text-brand"><Wrench class="h-4 w-4" /></span>
        <div><p class="text-base font-semibold leading-none tabular-nums text-foreground">{member.toolRefs.length}</p><p class="mt-1 text-[11px] leading-[16.5px] text-muted-foreground">{t('people.tools')}</p></div>
      </button>
      <button type="button" class="flex items-center gap-2.5 px-4 py-3 text-left transition-colors hover:bg-muted" onclick={() => (activeTab = 'knowledge')}>
        <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-brand/10 text-brand"><Database class="h-4 w-4" /></span>
        <div><p class="text-base font-semibold leading-none tabular-nums text-foreground">{knowledgeBaseCount}</p><p class="mt-1 whitespace-nowrap text-[11px] leading-[16.5px] text-muted-foreground">{t('people.knowledgeBase')}</p></div>
      </button>
      <button type="button" class="flex items-center gap-2.5 px-4 py-3 text-left transition-colors hover:bg-muted" onclick={() => (activeTab = 'memory')}>
        <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-brand/10 text-brand"><Brain class="h-4 w-4" /></span>
        <div><p class="text-base font-semibold leading-none tabular-nums text-foreground">{memoryCount}</p><p class="mt-1 text-[11px] leading-[16.5px] text-muted-foreground">{t('people.memory')}</p></div>
      </button>
      <button type="button" class="flex items-center gap-2.5 px-4 py-3 text-left transition-colors hover:bg-muted" onclick={() => (activeTab = 'roles')}>
        <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-brand/10 text-brand"><ShieldCheck class="h-4 w-4" /></span>
        <div><p class="text-base font-semibold leading-none tabular-nums text-foreground">{roleCount}</p><p class="mt-1 text-[11px] leading-[16.5px] text-muted-foreground">{t('people.roles')}</p></div>
      </button>
      </div>
    </div>
  </header>

  <div data-member-profile-tabs role="tablist" aria-label={t('people.profile')} class="flex items-center gap-5 overflow-x-auto border-b border-border px-6 subtle-scrollbar">
    {#each profileTabs as tab (tab.key)}
      <button
        type="button"
        role="tab"
        aria-selected={activeTab === tab.key}
        class="-mb-px shrink-0 whitespace-nowrap border-b-2 py-2.5 text-[13px] leading-[19.5px] transition-colors {activeTab === tab.key ? 'border-brand font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
        onclick={() => (activeTab = tab.key)}
      >
        {tab.label}
      </button>
    {/each}
  </div>

  <div class="px-6 py-5">
  {#if activeTab === 'profile'}
  <div data-member-profile-overview>
    <div class="grid gap-5 lg:grid-cols-[minmax(0,1fr)_240px]">
      <section>
        <p class="text-xs text-muted-foreground">{isHuman ? t('people.resume') : t('people.agentPrompt')}</p>
        <p class="mt-1.5 whitespace-pre-wrap text-[13px] leading-relaxed text-foreground/90">{displayField(profileText)}</p>
      </section>
      <section class="rounded-xl bg-muted/55 p-3.5">
        <div class="mb-2 flex items-center gap-1.5 text-[12px] leading-[18px]">
          <Building2 class="h-3.5 w-3.5 text-muted-foreground" />
          <h3 class="text-[12px] font-medium leading-[18px] text-foreground">{t('people.currentAssignment')}</h3>
        </div>
        <dl class="grid grid-cols-2 gap-y-2 text-[12px] leading-[18px]">
          <div class="min-w-0"><dt class="text-[11px] leading-[16.5px] text-muted-foreground">{t('organization.root')}</dt><dd class="mt-0.5 truncate text-[12.5px] leading-[18.75px] text-foreground">{displayField(employmentOrganization)}</dd></div>
          <div class="min-w-0"><dt class="text-[11px] leading-[16.5px] text-muted-foreground">{t('people.department')}</dt><dd class="mt-0.5 truncate text-[12.5px] leading-[18.75px] text-foreground">{displayField(employmentDepartment)}</dd></div>
          <div class="min-w-0"><dt class="text-[11px] leading-[16.5px] text-muted-foreground">{t('people.position')}</dt><dd class="mt-0.5 truncate text-[12.5px] leading-[18.75px] text-foreground">{displayField(employmentPosition)}</dd></div>
          <div class="min-w-0"><dt class="text-[11px] leading-[16.5px] text-muted-foreground">{t('people.manager')}</dt><dd class="mt-0.5 truncate text-[12.5px] leading-[18.75px] text-foreground">{managerLabel()}</dd></div>
        </dl>
      </section>
    </div>

    <dl class="mt-5 grid gap-4 rounded-xl bg-muted/55 px-4 py-3 sm:grid-cols-2 lg:grid-cols-4">
      <div><dt class="text-[11px] text-muted-foreground">{t('people.permissionRoles')}</dt><dd class="mt-0.5 text-[12.5px] text-foreground">{displayField(roleSummary(member))}</dd></div>
      <div><dt class="text-[11px] text-muted-foreground">{t('people.functionalRoles')}</dt><dd class="mt-0.5 text-[12.5px] text-foreground">{displayField((member.functionalRoleLabels ?? []).join(' / '))}</dd></div>
      <div><dt class="text-[11px] text-muted-foreground">{t('people.runtime')}</dt><dd class="mt-0.5 text-[12.5px] text-foreground">{displayField(member.runtimeKind)}</dd></div>
      <div><dt class="text-[11px] text-muted-foreground">{t('people.availability')}</dt><dd class="mt-0.5 text-[12.5px] text-foreground">{displayField(member.availabilityStatus)}</dd></div>
    </dl>

    {#if !isHuman}
      <section class="mt-5">
        <h3 class="text-[12.5px] font-medium text-foreground">{t('people.runtimeSection')}</h3>
        <div class="mt-2.5 grid gap-5 sm:grid-cols-2 lg:grid-cols-4">
          <div class="min-w-0">
            <p class="flex items-center gap-1.5 text-xs text-muted-foreground"><Sparkles class="h-3.5 w-3.5" />{t('people.skills')}</p>
            <div class="mt-2 space-y-1.5">
              {#each member.skillRefs.slice(0, 2) as skill}
                <p class="truncate font-mono text-xs text-foreground">{skill.skillId}</p>
              {:else}
                <p class="text-xs text-muted-foreground">{t('people.notConfigured')}</p>
              {/each}
            </div>
          </div>
          <div class="min-w-0">
            <p class="flex items-center gap-1.5 text-xs text-muted-foreground"><Wrench class="h-3.5 w-3.5" />{t('people.tools')}</p>
            <div class="mt-2 space-y-1.5">
              {#each member.toolRefs.slice(0, 2) as tool}
                <p class="truncate font-mono text-xs text-foreground">{tool.toolId}</p>
              {:else}
                <p class="text-xs text-muted-foreground">{t('people.notConfigured')}</p>
              {/each}
            </div>
          </div>
          <div class="min-w-0">
            <p class="flex items-center gap-1.5 text-xs text-muted-foreground"><Database class="h-3.5 w-3.5" />{t('people.knowledgeBase')}</p>
            <div class="mt-2 space-y-1.5">
              {#if member.knowledgeBaseId}
                <p class="truncate font-mono text-xs text-foreground">{member.knowledgeBaseId}</p>
              {:else if knowledgeBaseCount > 0}
                <p class="text-xs text-foreground">{t('people.knowledgeBaseCount', { count: knowledgeBaseCount })}</p>
              {:else}
                <p class="text-xs text-muted-foreground">{t('people.notConfigured')}</p>
              {/if}
            </div>
          </div>
          <div class="min-w-0">
            <p class="flex items-center gap-1.5 text-xs text-muted-foreground"><Brain class="h-3.5 w-3.5" />{t('people.memory')}</p>
            <div class="mt-2 space-y-1.5">
              {#if member.personalMemoryRef.memoryProfileId}
                <p class="text-xs text-foreground">{t('people.personalMemoryConnected')}</p>
              {/if}
              {#if member.collectiveMemoryRefs.length > 0}
                <p class="text-xs text-foreground">{t('people.collectiveMemoryCount', { count: member.collectiveMemoryRefs.length })}</p>
              {/if}
              {#if !member.personalMemoryRef.memoryProfileId && member.collectiveMemoryRefs.length === 0}
                <p class="text-xs text-muted-foreground">{t('people.notConfigured')}</p>
              {/if}
            </div>
          </div>
        </div>
      </section>
    {/if}
  </div>

  {#if isHuman && (member.userAccount.username || member.userAccount.email)}
    <section class="mt-8">
      <h3 class="text-sm font-semibold text-foreground">{t('people.loginAccount')}</h3>
      <dl class="mt-3 grid gap-2 text-sm md:grid-cols-2">
        <div class="flex justify-between gap-3">
          <dt class="text-muted-foreground">{t('people.username')}</dt>
          <dd class="text-right text-foreground">{displayField(member.userAccount.username)}</dd>
        </div>
        <div class="flex justify-between gap-3">
          <dt class="text-muted-foreground">{t('people.email')}</dt>
          <dd class="text-right text-foreground">{displayField(member.userAccount.email)}</dd>
        </div>
        <div class="flex justify-between gap-3">
          <dt class="text-muted-foreground">{t('people.availability')}</dt>
          <dd class="text-right text-foreground">{member.userAccount.accountEnabled ? t('kpi.enabled') : t('kpi.disabled')}</dd>
        </div>
      </dl>
    </section>
  {/if}

  {:else if activeTab === 'skills'}
    <section>
      <h3 class="text-sm font-semibold text-foreground">{t('people.skills')}</h3>
      {#if member.skillRefs.length > 0}
        <div class="mt-3 space-y-2">
          {#each member.skillRefs as skill}
            <div class="rounded-md bg-muted/25 p-3">
              <p class="text-sm font-medium text-foreground">{skill.skillId}</p>
              <p class="mt-1 text-xs text-muted-foreground">
                {compact([skill.versionConstraint, skill.alias, skill.level])}
              </p>
              {#if skill.evidenceRefs.length > 0}
                <div class="mt-2 flex flex-wrap gap-1.5">
                  {#each skill.evidenceRefs as evidence}
                    <Badge>{evidence}</Badge>
                  {/each}
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {:else}
        <p class="mt-3 text-sm text-muted-foreground">{t('people.notConfigured')}</p>
      {/if}
      {#if member.capabilityTags.length > 0}
        <div class="mt-4 flex flex-wrap gap-1.5">
          {#each member.capabilityTags as tag}
            <Badge>{tag}</Badge>
          {/each}
        </div>
      {/if}
    </section>

  {:else if activeTab === 'tools'}
    <section>
      <h3 class="text-sm font-semibold text-foreground">{t('people.tools')}</h3>
      {#if member.toolRefs.length > 0}
        <div class="mt-3 space-y-2">
          {#each member.toolRefs as tool}
            <div class="rounded-md bg-muted/25 p-3">
              <p class="text-sm font-medium text-foreground">{tool.toolId}</p>
              <p class="mt-1 text-xs text-muted-foreground">
                {compact([tool.versionConstraint, tool.alias, tool.permissionScope, tool.bindingPolicy])}
              </p>
            </div>
          {/each}
        </div>
      {:else}
        <p class="mt-3 text-sm text-muted-foreground">{t('people.notConfigured')}</p>
      {/if}
    </section>

  {:else if activeTab === 'knowledge'}
    <WorkforceKnowledgeBasePanel
      knowledgeBaseId={member.knowledgeBaseId}
      memberName={member.displayName}
      ownerId={member.profileId}
      ownerType={isHuman ? 'human' : 'agent'}
      oncountchange={(count) => (discoveredKnowledgeBaseCount = count)}
    />

  {:else if activeTab === 'memory'}
    <WorkforceMemoryPanel
      memberName={member.displayName}
      ownerId={member.profileId}
      personalProfileId={member.personalMemoryRef.memoryProfileId}
      collectiveProfileIds={member.collectiveMemoryRefs.map((ref) => ref.memoryProfileId)}
      oncountchange={(count) => (discoveredMemoryCount = count)}
    />

  {:else if activeTab === 'responsibilities'}
    <ResponsibilityProfilePanel
      profile={responsibilityProfile}
      responsibilityBoundary={member.responsibilityBoundary}
      loading={responsibilityProfileLoading}
      failed={responsibilityProfileFailed}
      oneditboundary={onedit ? () => onedit(member) : undefined}
    />

  {:else if activeTab === 'feishu'}
    <WorkforceChannelBindingsPanel
      targetAgentProfileId={member.profileId}
      accounts={channelAccounts}
      loading={channelBindingsLoading}
      error={channelBindingsError}
      canManage={canManageChannels}
      busyAccountId={channelBusyAccountId}
      onassign={onchannelassign}
      ontoggle={onchanneltoggle}
      ondelete={onchanneldelete}
    />

  {:else if activeTab === 'roles'}
    <section>
      <h3 class="text-sm font-semibold text-foreground">{t('people.roles')}</h3>
      <dl class="mt-4 grid gap-x-6 gap-y-5 text-sm sm:grid-cols-2">
        <div><dt class="text-xs text-muted-foreground">{t('people.permissionRoles')}</dt><dd class="mt-1 text-foreground">{displayField(roleSummary(member))}</dd></div>
        <div><dt class="text-xs text-muted-foreground">{t('people.functionalRoles')}</dt><dd class="mt-1 text-foreground">{displayField((member.functionalRoleLabels ?? []).join(' / '))}</dd></div>
        <div><dt class="text-xs text-muted-foreground">{t('people.authorityScope')}</dt><dd class="mt-1 text-foreground">{displayField(member.authorityScope)}</dd></div>
        <div><dt class="text-xs text-muted-foreground">{t('people.supervisionPolicy')}</dt><dd class="mt-1 text-foreground">{supervisionPolicyLabel()}</dd></div>
      </dl>
    </section>
    {#if !isHuman}
      <section class="mt-8">
        <h3 class="text-sm font-semibold text-foreground">{t('people.runtimeConfig')}</h3>
        <p class="mt-2 text-sm text-muted-foreground">{t('people.runtimeConfigRef')}: {displayField(member.runtimeConfigRef)}</p>
        <pre class="mt-3 max-h-56 overflow-auto rounded-md bg-muted/30 p-3 text-xs text-muted-foreground">{formatJson(member.runtimeConfig)}</pre>
      </section>
    {/if}
  {:else}
    <section data-employment-information class="space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-2">
          <Building2 class="h-4 w-4 text-muted-foreground" />
          <h3 class="text-sm font-semibold text-foreground">{t('people.currentAssignment')}</h3>
        </div>
        <div class="flex flex-wrap gap-2">
          <Badge tone={member.placementStatus === 'market' ? 'warning' : 'success'}>{placementLabel()}</Badge>
          <Badge>{employmentTypeLabel()}</Badge>
        </div>
      </div>

      {#if member.placementStatus === 'market'}
        <p class="text-sm text-muted-foreground">{t('people.marketNoAssignment')}</p>
      {:else}
        <dl class="grid gap-3 text-sm md:grid-cols-2">
          <div>
            <dt class="text-xs text-muted-foreground">{t('organization.root')}</dt>
            <dd class="mt-1 text-foreground">{displayField(employmentOrganization)}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted-foreground">{t('people.department')}</dt>
            <dd class="mt-1 text-foreground">{displayField(employmentDepartment)}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted-foreground">{t('people.position')}</dt>
            <dd class="mt-1 text-foreground">{displayField(employmentPosition)}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted-foreground">{t('people.manager')}</dt>
            <dd class="mt-1 text-foreground">{managerLabel()}</dd>
          </div>
        </dl>
      {/if}
    </section>

    {#if member.placementStatus === 'market' && member.releasedAt}
      <section class="mt-8">
        <h3 class="text-sm font-semibold text-foreground">{t('people.recentDepartureInformation')}</h3>
        <dl class="mt-3 grid gap-3 text-sm md:grid-cols-2">
          <div>
            <dt class="text-xs text-muted-foreground">{t('people.previousOrganization')}</dt>
            <dd class="mt-1 text-foreground">{displayField(employmentOrganization)}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted-foreground">{t('people.previousDepartment')}</dt>
            <dd class="mt-1 text-foreground">{displayField(employmentDepartment)}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted-foreground">{t('people.previousPosition')}</dt>
            <dd class="mt-1 text-foreground">{displayField(employmentPosition)}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted-foreground">{t('people.movedToMarketAt')}</dt>
            <dd class="mt-1 font-mono text-xs text-foreground">{member.releasedAt}</dd>
          </div>
          {#if member.releasedReason}
            <div class="md:col-span-2">
              <dt class="text-xs text-muted-foreground">{t('people.movedToMarketReason')}</dt>
              <dd class="mt-1 text-foreground">{member.releasedReason}</dd>
            </div>
          {/if}
        </dl>
      </section>
    {/if}

    <section class="mt-8">
      <h3 class="text-sm font-semibold text-foreground">{t('people.permissionsAndResponsibilities')}</h3>
      <dl class="mt-3 grid gap-3 text-sm md:grid-cols-2">
        <div class="min-w-0">
          <dt class="text-xs text-muted-foreground">{t('people.permissionRoles')}</dt>
          <dd class="mt-1 text-foreground">{displayField(roleSummary(member))}</dd>
        </div>
        <div class="min-w-0">
          <dt class="text-xs text-muted-foreground">{t('people.functionalRoles')}</dt>
          <dd class="mt-1 text-foreground">{displayField((member.functionalRoleLabels ?? []).join(' / '))}</dd>
        </div>
        <div class="min-w-0 md:col-span-2">
          <dt class="text-xs text-muted-foreground">{t('people.responsibilityBoundary')}</dt>
          <dd class="mt-1 whitespace-pre-wrap leading-6 text-muted-foreground">{displayField(member.responsibilityBoundary)}</dd>
        </div>
        <div class="min-w-0 md:col-span-2">
          <dt class="text-xs text-muted-foreground">{t('people.behaviorBoundary')}</dt>
          <dd class="mt-1 whitespace-pre-wrap leading-6 text-muted-foreground">{displayField(member.behaviorBoundary)}</dd>
        </div>
        <div class="min-w-0">
          <dt class="text-xs text-muted-foreground">{t('people.authorityScope')}</dt>
          <dd class="mt-1 text-foreground">{displayField(member.authorityScope)}</dd>
        </div>
        <div class="min-w-0">
          <dt class="text-xs text-muted-foreground">{t('people.supervisionPolicy')}</dt>
          <dd class="mt-1 text-foreground">{supervisionPolicyLabel()}</dd>
        </div>
      </dl>
    </section>
  {/if}
  </div>
</section>
