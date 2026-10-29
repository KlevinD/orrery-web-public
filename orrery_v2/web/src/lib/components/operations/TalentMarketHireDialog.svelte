<script lang="ts">
  import { UserRoundPlus } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type {
    OrganizationTreeViewModel,
    OrganizationUnitViewModel,
    TalentMarketHireFormViewModel,
    WorkforceMemberViewModel
  } from '#lib/types/view-models'

  interface SelectOption {
    label: string
    value: string
  }

  interface Props {
    open?: boolean
    member?: WorkforceMemberViewModel
    organizations?: OrganizationTreeViewModel[]
    members?: WorkforceMemberViewModel[]
    busy?: boolean
    oncancel?: () => void
    onsubmit?: (form: TalentMarketHireFormViewModel) => void
  }

  let {
    open = false,
    member = undefined,
    organizations = [],
    members = [],
    busy = false,
    oncancel,
    onsubmit
  }: Props = $props()

  let organizationId = $state('')
  let departmentId = $state('')
  let positionId = $state('')
  let roles = $state('')
  let managerId = $state('')
  let currentMemberProfileId = $state('')
  let t = $derived(createTranslator($locale))

  const departments = $derived(
    organizations
      .filter((organization) => organization.id === organizationId)
      .flatMap((organization) => organization.units.flatMap((unit) => flattenUnits([unit])))
  )

  const organizationOptions = $derived([
    option(t('organization.root'), ''),
    ...organizations.map((organization) => option(organization.name, organization.id))
  ])

  const departmentOptions = $derived([
    option(t('people.department'), ''),
    ...departments.map((unit) => option(unit.name, unit.id))
  ])

  const positionOptions = $derived([
    option(t('people.position'), ''),
    ...(departments.find((unit) => unit.id === departmentId)?.positions ?? []).map((position) => option(position.name, position.id))
  ])

  const managerOptions = $derived([
    option(t('people.noManager'), ''),
    ...members.filter((candidate) => isAssignableManager(candidate)).map((candidate) => option(managerLabel(candidate), candidate.profileId))
  ])

  const canSubmit = $derived(
    !!member &&
      organizationId.trim().length > 0 &&
      departmentId.trim().length > 0 &&
      positionId.trim().length > 0 &&
      departments.some(
        (unit) =>
          unit.organizationId === organizationId &&
          unit.id === departmentId &&
          unit.positions.some((position) => position.id === positionId)
      )
  )

  $effect(() => {
    const profileId = member?.profileId ?? ''
    if (profileId === currentMemberProfileId) return

    currentMemberProfileId = profileId
    organizationId = ''
    departmentId = ''
    positionId = ''
    roles = member?.roles.join(', ') ?? ''
    managerId = cleanOptional(member?.managerId)
  })

  $effect(() => {
    if (!organizationId) {
      departmentId = ''
      positionId = ''
      managerId = ''
      return
    }
    if (!departmentOptions.some((department) => department.value === departmentId)) {
      departmentId = ''
      positionId = ''
    }
    if (!positionOptions.some((position) => position.value === positionId)) {
      positionId = ''
    }
    if (managerId && !managerOptions.some((manager) => manager.value === managerId)) {
      managerId = ''
    }
  })

  function submit() {
    if (!member || !canSubmit) return

    onsubmit?.({
      agentProfileId: member.profileId,
      organizationId,
      departmentId,
      positionId,
      roles,
      managerId,
      reportingLineId: ''
    })
  }

  function option(label: string, value: string): SelectOption {
    return { label, value }
  }

  function cleanOptional(value: string | undefined) {
    if (!value || value === 'Not provided' || value === 'Not assigned') return ''
    return value
  }

  function isAssignableManager(candidate: WorkforceMemberViewModel) {
    return (
      organizationId.length > 0 &&
      candidate.placementStatus === 'internal' &&
      candidate.profileId !== member?.profileId &&
      candidate.organizationId === organizationId
    )
  }

  function managerLabel(candidate: WorkforceMemberViewModel) {
    const affiliation = [candidate.departmentDisplayName, candidate.positionDisplayName].filter(isPresentLabel).join(' / ')
    return affiliation ? `${candidate.displayName} - ${affiliation}` : candidate.displayName
  }

  function isPresentLabel(value: string | undefined) {
    return !!value && value !== 'Not provided' && value !== 'Not assigned'
  }

  function flattenUnits(units: OrganizationUnitViewModel[]): OrganizationUnitViewModel[] {
    return units.flatMap((unit) => [unit, ...flattenUnits(unit.children ?? [])])
  }
</script>

<Dialog {open} title={t('people.hireToUnit')} size="wide" onClose={oncancel}>
  {#if member}
    <div class="space-y-5">
      <div class="rounded-md border border-border bg-muted/20 p-4">
        <div class="flex flex-wrap items-center gap-2">
          <UserRoundPlus class="h-4 w-4 text-brand" />
          <h2 class="text-sm font-semibold text-foreground">{member.displayName}</h2>
        </div>
        <p class="mt-2 text-xs text-muted-foreground">
          {t('people.marketCannotWork')}
        </p>
      </div>

      <div class="grid gap-3 md:grid-cols-2">
        <label class="min-w-0">
          <span class="mb-1 block text-xs font-medium text-muted-foreground">{t('organization.root')}</span>
          <Select bind:value={organizationId} options={organizationOptions} disabled={busy} />
        </label>
        <label class="min-w-0">
          <span class="mb-1 block text-xs font-medium text-muted-foreground">{t('people.department')}</span>
          <Select bind:value={departmentId} options={departmentOptions} disabled={!organizationId || busy} />
        </label>
        <label class="min-w-0">
          <span class="mb-1 block text-xs font-medium text-muted-foreground">{t('people.position')}</span>
          <Select bind:value={positionId} options={positionOptions} disabled={!departmentId || busy} />
        </label>
        <label class="min-w-0">
          <span class="mb-1 block text-xs font-medium text-muted-foreground">{t('people.manager')}</span>
          <Select bind:value={managerId} options={managerOptions} disabled={!organizationId || busy} />
        </label>
      </div>

      <div class="flex items-center justify-end gap-2 border-t border-border pt-4">
        <Button type="button" variant="outline" disabled={busy} onclick={oncancel}>
          {t('organization.cancel')}
        </Button>
        <Button type="button" disabled={!canSubmit || busy} onclick={submit}>
          <UserRoundPlus class="h-4 w-4" />
          {t('people.hireToUnit')}
        </Button>
      </div>
    </div>
  {/if}
</Dialog>
