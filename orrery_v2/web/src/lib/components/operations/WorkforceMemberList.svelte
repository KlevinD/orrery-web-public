<script lang="ts">
  import { Bot, LogOut, Pause, Play, Trash2, User, UserRoundPlus } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import EmptyState from '#lib/components/ui/EmptyState.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { WorkforceMemberViewModel } from '#lib/types/view-models'
  import WorkforceAvatar from './WorkforceAvatar.svelte'

  interface Props {
    members: WorkforceMemberViewModel[]
    busyId?: string
    placementAction?: 'release' | 'hire'
    showPlacementAction?: boolean
    showAvailabilityActions?: boolean
    selectable?: boolean
    selectedIds?: string[]
    activeId?: string
    compact?: boolean
    onselectionchange?: (ids: string[]) => void
    onenable?: (member: WorkforceMemberViewModel) => void
    ondisable?: (member: WorkforceMemberViewModel) => void
    ondelete?: (member: WorkforceMemberViewModel) => void
    onpurge?: (member: WorkforceMemberViewModel) => void
    onselect?: (member: WorkforceMemberViewModel) => void
  }

  let {
    members,
    busyId = '',
    placementAction = 'release',
    showPlacementAction = true,
    showAvailabilityActions = true,
    selectable = false,
    selectedIds = [],
    activeId = '',
    compact = false,
    onselectionchange,
    onenable,
    ondisable,
    ondelete,
    onpurge,
    onselect
  }: Props = $props()
  let t = $derived(createTranslator($locale))
  const actionLabel = $derived(placementAction === 'hire' ? t('people.hireToUnit') : t('people.releaseToMarket'))
  const selectedSet = $derived(new Set(selectedIds))

  function displayField(value: string) {
    return value === 'Not provided' || value === 'Not assigned' || value === '' ? t('workspace.notProvided') : value
  }

  function memberDescription(member: WorkforceMemberViewModel): string | null {
    const responsibility = member.responsibilityBoundary?.trim()
    if (responsibility && responsibility !== 'Not provided') return responsibility
    const prompt = member.prompt?.trim()
    if (prompt && prompt !== 'Not provided') return prompt
    return null
  }

  function memberType(member: WorkforceMemberViewModel) {
    return member.agentKind.toLowerCase() === 'human' ? 'human' : 'agent'
  }

  function statusLabel(member: WorkforceMemberViewModel) {
    return member.enabled ? t('kpi.enabled') : t('kpi.disabled')
  }

  function roleLine(member: WorkforceMemberViewModel) {
    return displayField(member.primaryRoleLabel)
  }

  function handleCardKeydown(event: KeyboardEvent, member: WorkforceMemberViewModel) {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      onselect?.(member)
    }
  }

  function toggleMember(event: MouseEvent, member: WorkforceMemberViewModel) {
    event.stopPropagation()
    member.enabled ? ondisable?.(member) : onenable?.(member)
  }

  function deleteMember(event: MouseEvent, member: WorkforceMemberViewModel) {
    event.stopPropagation()
    ondelete?.(member)
  }

  function purgeMember(event: MouseEvent, member: WorkforceMemberViewModel) {
    event.stopPropagation()
    onpurge?.(member)
  }

  function toggleSelection(event: Event, member: WorkforceMemberViewModel) {
    event.stopPropagation()
    const next = new Set(selectedIds)
    if (next.has(member.profileId)) {
      next.delete(member.profileId)
    } else {
      next.add(member.profileId)
    }
    onselectionchange?.(Array.from(next))
  }
</script>

{#if members.length === 0}
  <EmptyState title={t('people.empty')} description={t('people.empty.description')} />
{:else}
  <div data-workforce-list-layout="vertical" class="flex flex-col gap-2">
    {#each members as member (member.profileId)}
      {@const isAgent = memberType(member) === 'agent'}
      <div
        role="button"
        tabindex="0"
        class="surface-hover group relative flex cursor-pointer flex-col items-stretch gap-3 rounded-xl border border-border bg-card p-4 text-left {compact ? '' : 'sm:flex-row sm:items-center sm:gap-4'} {activeId === member.profileId ? 'bg-brand/[0.025] ring-1 ring-brand/25' : ''}"
        onclick={() => onselect?.(member)}
        onkeydown={(event) => handleCardKeydown(event, member)}
      >
        <div class="flex min-w-0 flex-1 items-center gap-3">
          {#if selectable}
            <label class="mt-2 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md border border-border bg-background text-muted-foreground shadow-sm transition-colors hover:text-foreground">
              <input
                type="checkbox"
                class="h-4 w-4 accent-brand"
                checked={selectedSet.has(member.profileId)}
                aria-label={t('people.selectForExport', { name: member.displayName })}
                onclick={(event) => event.stopPropagation()}
                onchange={(event) => toggleSelection(event, member)}
              />
            </label>
          {/if}
          <div class="relative h-11 w-11 shrink-0">
            <WorkforceAvatar profileId={member.profileId} name={member.displayName} human={!isAgent} class="h-12 w-12 rounded-full" />
            <span class="absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full border-2 border-card {member.enabled ? 'bg-status-done' : 'bg-muted-foreground/45'}"></span>
          </div>
          <div class="min-w-0 flex-1 space-y-1.5">
            <div class="flex min-w-0 flex-wrap items-center gap-1.5">
              <h2 class="truncate text-sm font-medium text-foreground transition-colors group-hover:text-brand">
                {member.displayName}
              </h2>
              <Badge tone={isAgent ? 'neutral' : 'success'}>
                {#if isAgent}
                  <Bot class="mr-1 h-3 w-3" />
                  {t('workspace.agent')}
                {:else}
                  <User class="mr-1 h-3 w-3" />
                  {t('workspace.human')}
                {/if}
              </Badge>
              {#if member.personalAssistant}
                <Badge tone="info">{t('people.personalAssistant')}</Badge>
              {/if}
              <Badge tone={member.statusTone}>{statusLabel(member)}</Badge>
              {#if member.placementStatus === 'market'}
                <Badge tone="warning">{t('people.marketTalent')}</Badge>
              {/if}
            </div>
            <p class="truncate text-[12.5px] text-muted-foreground">
              {displayField(member.departmentDisplayName)} / {displayField(member.positionDisplayName)} / {roleLine(member)}
            </p>
            {#if memberDescription(member)}
              <p class="line-clamp-1 text-[12.5px] leading-relaxed text-muted-foreground/80">
                {memberDescription(member)}
              </p>
            {/if}
            <div class="flex min-w-0 flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
              {#each (member.functionalRoleLabels ?? []).slice(0, 3) as role}
                <Badge>{t('people.functionalRole')}: {role}</Badge>
              {/each}
              {#each member.roleLabels.slice(0, 3) as role}
                <Badge tone="info">{t('people.permissionRole')}: {role}</Badge>
              {/each}
              {#if isAgent}
                {#if member.roleLabels.length === 0 && (member.functionalRoleLabels ?? []).length === 0}
                  <span>{displayField(member.roleName)}</span>
                {/if}
                {#if member.runtimeKind && member.runtimeKind !== 'Not provided'}
                  <Badge tone="info">{member.runtimeKind}</Badge>
                {/if}
                {#if member.availabilityStatus && member.availabilityStatus !== 'Not provided'}
                  <Badge>{member.availabilityStatus}</Badge>
                {/if}
              {:else}
                {#if member.email}
                  <span>{displayField(member.email)}</span>
                {/if}
              {/if}
              {#if member.capabilityTags.length > 0}
                {#each member.capabilityTags.slice(0, 4) as tag}
                  <Badge>{tag}</Badge>
                {/each}
              {/if}
            </div>
          </div>
        </div>

        <div class="flex shrink-0 items-center gap-1 self-end sm:self-center">
          {#if showAvailabilityActions}
            <button
              type="button"
              disabled={busyId === member.profileId}
              onclick={(event) => toggleMember(event, member)}
              class="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:pointer-events-none disabled:opacity-50"
              title={member.enabled ? t('people.disable') : t('people.enable')}
            >
              {#if member.enabled}
                <Pause class="h-4 w-4" />
              {:else}
                <Play class="h-4 w-4" />
              {/if}
            </button>
          {/if}
          {#if showPlacementAction && !member.personalAssistant}
            <button
              type="button"
              disabled={busyId === member.profileId}
              onclick={(event) => deleteMember(event, member)}
              class="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors {placementAction === 'hire' ? 'hover:bg-brand/10 hover:text-brand' : 'hover:bg-status-blocked-bg/60 hover:text-status-blocked'} disabled:pointer-events-none disabled:opacity-50"
              title={actionLabel}
            >
              {#if placementAction === 'hire'}
                <UserRoundPlus class="h-3.5 w-3.5" />
              {:else}
                <LogOut class="h-3.5 w-3.5" />
              {/if}
            </button>
          {/if}
          {#if onpurge}
            <button
              type="button"
              disabled={busyId === member.profileId}
              onclick={(event) => purgeMember(event, member)}
              class="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-status-blocked-bg/60 hover:text-status-blocked disabled:pointer-events-none disabled:opacity-50"
              title={t('people.deleteMarketTalent')}
            >
              <Trash2 class="h-3.5 w-3.5" />
            </button>
          {/if}
        </div>
      </div>
    {/each}
  </div>
{/if}
