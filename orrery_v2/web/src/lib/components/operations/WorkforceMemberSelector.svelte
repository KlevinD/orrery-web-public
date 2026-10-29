<script lang="ts">
  import { ClipboardCheck } from 'lucide-svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { WorkforceMemberViewModel } from '#lib/types/view-models'
  import WorkforceAvatar from './WorkforceAvatar.svelte'

  interface Props {
    members: WorkforceMemberViewModel[]
    activeId?: string
    onselect?: (member: WorkforceMemberViewModel) => void
  }

  let { members, activeId = '', onselect }: Props = $props()
  let t = $derived(createTranslator($locale))

  function isHuman(member: WorkforceMemberViewModel) {
    return member.agentKind.toLowerCase() === 'human'
  }

  function secondaryLine(member: WorkforceMemberViewModel) {
    const values = [member.departmentDisplayName, member.positionDisplayName]
      .filter((value) => value && value !== 'Not provided' && value !== 'Not assigned')
    return values.join(' / ') || t('people.notConfigured')
  }

  function responsibilityLine(member: WorkforceMemberViewModel) {
    const responsibility = member.responsibilityBoundary?.trim()
    return responsibility && responsibility !== 'Not provided' ? responsibility : t('people.notConfigured')
  }
</script>

<div data-workforce-member-selector role="listbox" aria-label={t('people.personnelRecord')} class="space-y-1">
  {#each members as member (member.profileId)}
    {@const human = isHuman(member)}
    <button
      type="button"
      role="option"
      aria-selected={activeId === member.profileId}
      class="group flex w-full items-center gap-3 rounded-lg border px-2.5 py-2.5 text-left transition-colors {activeId === member.profileId ? 'selection-wash' : 'border-transparent hover:bg-muted'}"
      onclick={() => onselect?.(member)}
    >
      <WorkforceAvatar profileId={member.profileId} name={member.displayName} {human} class="h-12 w-12 rounded-lg" textClass="text-xs" />
      <span class="min-w-0 flex-1">
        <span class="block truncate text-[13px] font-medium leading-[19.5px] text-foreground">{member.displayName}</span>
        <span class="block truncate text-[11px] leading-[16.5px] text-muted-foreground">{secondaryLine(member)}</span>
        <span class="mt-0.5 flex min-w-0 items-center gap-1 text-[10.5px] leading-[15.75px] text-muted-foreground" title={`${t('people.responsibilityBoundary')}: ${responsibilityLine(member)}`}>
          <ClipboardCheck class="h-3 w-3 shrink-0" />
          <span class="truncate">{responsibilityLine(member)}</span>
        </span>
      </span>
      <span class="inline-flex shrink-0 items-center gap-1 text-[10.5px] leading-[15.75px] {member.enabled ? 'text-status-done' : 'text-muted-foreground'}">
        <span class="h-2.5 w-2.5 rounded-full border-2 border-current"></span>
        {member.enabled ? t('kpi.enabled') : t('kpi.disabled')}
      </span>
    </button>
  {/each}
</div>
