<script lang="ts">
  import { ArrowLeft, ArrowRight, Minus, Plus, Save } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import Tabs from '#lib/components/ui/Tabs.svelte'
  import { createTranslator, locale, type TranslationKey } from '#lib/i18n'
  import {
    availableOptions,
    buildInitialForm,
    optionsWithCurrent,
    optionsWithSelected,
    selectedOptions,
    selectedSummary,
    selectedValues,
    transferIn as buildTransferInValue,
    transferOut as buildTransferOutValue,
    type WorkforceMemberEditorOption
  } from './workforceMemberEditorHelpers'
  import type {
    OrganizationTreeViewModel,
    OrganizationUnitViewModel,
    WorkforceMemberFormViewModel
  } from '#lib/types/view-models'

  interface Props {
    busy?: boolean
    organizationTree?: OrganizationTreeViewModel[]
    showTitle?: boolean
    defaultKind?: 'human' | 'agent'
    initial?: Partial<WorkforceMemberFormViewModel>
    runtimeOptions?: WorkforceMemberEditorOption[]
    roleOptions?: WorkforceMemberEditorOption[]
    skillOptions?: WorkforceMemberEditorOption[]
    toolOptions?: WorkforceMemberEditorOption[]
    onsubmit?: (form: WorkforceMemberFormViewModel) => void
  }

  type ValidationField =
    | 'displayName'
    | 'mailboxEmail'
    | 'accountUsername'
    | 'organizationId'
    | 'departmentId'
    | 'positionId'
    | 'responsibilityBoundary'

  type ValidationErrors = Partial<Record<ValidationField, TranslationKey>>
  type EditorTab = 'profile' | 'access' | 'security' | 'capabilities'

  let {
    busy = false,
    organizationTree = [],
    showTitle = true,
    defaultKind = 'agent',
    initial = {},
    runtimeOptions = [],
    roleOptions = [],
    skillOptions = [],
    toolOptions = [],
    onsubmit
  }: Props = $props()

  const initialForm = (() => buildInitialForm(initial, defaultKind))()
  const agentProfileId = initialForm.agentProfileId
  const personalAssistant = initialForm.personalAssistant === true
  const agentInstanceId = initialForm.agentInstanceId
  let editorTab = $state<EditorTab>('profile')
  let displayName = $state(initialForm.displayName)
  let mailboxEmail = $state(initialForm.mailboxEmail ?? '')
  let agentKind = $state(initialForm.agentKind)
  let accountUsername = $state(initialForm.accountUsername ?? '')
  let accountEmail = $state(initialForm.accountEmail ?? '')
  let accountEnabled = $state(initialForm.accountEnabled ?? 'false')
  let organizationId = $state(initialForm.organizationId)
  let departmentId = $state(initialForm.departmentId)
  let positionId = $state(initialForm.positionId)
  let roles = $state(initialForm.roles)
  const functionalRoles = initialForm.functionalRoles
  let resume = $state(initialForm.resume)
  let prompt = $state(initialForm.prompt)
  let responsibilityBoundary = $state(initialForm.responsibilityBoundary)
  let behaviorBoundary = $state(initialForm.behaviorBoundary)
  let skillRefs = $state(initialForm.skillRefs)
  let skillAvailableSelection = $state<string[]>([])
  let skillSelectedSelection = $state<string[]>([])
  let toolRefs = $state(initialForm.toolRefs)
  let toolAvailableSelection = $state<string[]>([])
  let toolSelectedSelection = $state<string[]>([])
  let qaEnabled = $state(initialForm.qaEnabled)
  let workspaceRoot = $state(initialForm.workspaceRoot ?? '')
  let runtimeKind = $state(initialForm.runtimeKind)
  let runtimeConfigRef = $state(initialForm.runtimeConfigRef)
  const runtimeConfig = initialForm.runtimeConfig
  let enabled = $state(initialForm.enabled)
  let transferDialog = $state<'skills' | 'tools' | ''>('')
  let validationAttempted = $state(false)
  let t = $derived(createTranslator($locale))
  const isHuman = $derived(agentKind === 'human')
  const usesRuntime = $derived(!isHuman && !personalAssistant && runtimeConfigRef.trim().length > 0)
  const requiresChineseResponsibility = $derived(!isHuman)
  const hasRequiredPersona = $derived(
    isHuman ||
      (responsibilityBoundary.trim().length > 0 && (!requiresChineseResponsibility || containsHan(responsibilityBoundary)))
  )
  const isCreating = $derived(!agentProfileId)
  const accountProjectionStatus = initialForm.accountProjectionStatus ?? ''
  const accountProjectionRevision = initialForm.accountProjectionRevision ?? 0
  const validationErrors = $derived(buildValidationErrors())
  const hasValidationErrors = $derived(Object.keys(validationErrors).length > 0)
  const canSubmit = $derived(!hasValidationErrors)
  const editorTabs = $derived([
    { value: 'profile', label: editorTabLabel('people.editorTabProfile', 'profile') },
    { value: 'access', label: editorTabLabel('people.editorTabAccess', 'access') },
    ...(isHuman
      ? [{ value: 'security', label: editorTabLabel('people.editorTabSecurity', 'security') }]
      : [{ value: 'capabilities', label: editorTabLabel('people.editorTabCapabilities', 'capabilities') }])
  ])

  $effect(() => {
    if (organizationTree.length === 0) return
    if (!organizationTree.some((organization) => organization.id === organizationId)) {
      organizationId = ''
      departmentId = ''
      positionId = ''
      return
    }
    if (!departmentOptions.some((department) => department.value === departmentId)) {
      departmentId = ''
      positionId = ''
      return
    }
    if (isCreating && positionId && positionOptions.length > 1 && !selectedPosition()) {
      positionId = ''
    }
  })

  $effect(() => {
    if (!isHuman) return
    runtimeKind = ''
    runtimeConfigRef = ''
    workspaceRoot = ''
    skillRefs = ''
    toolRefs = ''
    qaEnabled = 'false'
    transferDialog = ''
  })

  $effect(() => {
    const byValue = new Map(roleOptions.map((option) => [option.value, option]))
    const selected = selectedValues(roles)
    const compatible = selected.filter((value) => {
      const option = byValue.get(value)
      return !option || roleOptionAssignable(option)
    })
    if (compatible.length !== selected.length) {
      roles = compatible.join(', ')
    }
  })

  $effect(() => {
    if (!runtimeConfigRef) return
    const profile = runtimeOptions.find((option) => option.value === runtimeConfigRef)
    if (profile?.runtimeKind && runtimeKind !== profile.runtimeKind) {
      runtimeKind = profile.runtimeKind
    }
  })

  $effect(() => {
    if ((isHuman && editorTab === 'capabilities') || (!isHuman && editorTab === 'security')) {
      editorTab = 'profile'
    }
  })

  const selectedDepartments = $derived(organizationTree.find((organization) => organization.id === organizationId)?.units.flatMap((unit) => flattenUnits([unit])) ?? [])

  const organizationOptions = $derived([
    { label: t('organization.root'), value: '' },
    ...organizationTree.map((organization) => ({ label: organization.name, value: organization.id }))
  ])

  const departmentOptions = $derived([
    { label: t('people.department'), value: '' },
    ...selectedDepartments.map((unit) => ({ label: unit.name, value: unit.id }))
  ])

  const positionOptions = $derived([
    { label: t('people.position'), value: '' },
    ...(selectedDepartment()?.positions ?? []).map((position) => ({
      label: position.name,
      value: position.id,
      meta: position.departmentId
    }))
  ])
  const hasPositionCatalog = $derived(positionOptions.length > 1)

  const agentKindOptions = $derived(personalAssistant
    ? [{ label: t('kpi.ai'), value: 'ai' }]
    : [
        { label: t('kpi.ai'), value: 'ai' },
        { label: t('workspace.human'), value: 'human' },
        { label: t('people.compositeAgent'), value: 'composite' }
      ])

  const runtimeSelectOptions = $derived(optionsWithCurrent([{ label: t('people.notConfigured'), value: '' }, ...runtimeOptions], runtimeConfigRef))
  const skillTransferOptions = $derived(optionsWithSelected(skillOptions, skillRefs))
  const toolTransferOptions = $derived(optionsWithSelected(toolOptions, toolRefs))
  const quickAssignableToolCount = $derived(toolTransferOptions.filter((option) => option.quickAssignable !== false).length)
  const toolTagGroups = $derived(groupToolTags(toolTransferOptions))
  const toolMcpServerGroups = $derived(groupToolMcpServers(toolTransferOptions))
  const assignableRoleOptions = $derived(roleOptions.filter(roleOptionAssignable))
  const roleSelectOptions = $derived(optionsWithSelected(assignableRoleOptions, roles))
  const invalidFieldClasses = 'border-destructive focus-visible:border-destructive focus-visible:ring-destructive/30'

  function submit() {
    validationAttempted = true
    if (!canSubmit) {
      editorTab = firstInvalidTab()
      return
    }
    const selectedRuntimeKind = !isHuman && runtimeConfigRef.trim().length > 0 ? runtimeKind : ''
    onsubmit?.({
      agentProfileId,
      personalAssistant,
      agentInstanceId,
      realName: displayName,
      displayName,
      mailboxEmail,
      agentKind,
      accountUsername,
      accountEmail,
      accountEnabled,
      employmentType: isHuman ? 'human' : 'ai_agent',
      organizationId,
      departmentId,
      positionId,
      roles,
      ...(functionalRoles ? { functionalRoles } : {}),
      resume,
      prompt,
      responsibilityBoundary,
      behaviorBoundary,
      skillRefs,
      toolRefs,
      qaEnabled,
      workspaceRoot,
      runtimeKind: selectedRuntimeKind,
      runtimeConfigRef: selectedRuntimeKind ? runtimeConfigRef : '',
      runtimeConfig,
      enabled
    })
  }

  function buildValidationErrors(): ValidationErrors {
    const errors: ValidationErrors = {}

    if (displayName.trim().length === 0) {
      errors.displayName = 'people.validationDisplayNameRequired'
    }
    if (mailboxEmail.trim().length > 0 && !isMailboxAddress(mailboxEmail)) {
      errors.mailboxEmail = 'people.validationMailboxEmailInvalid'
    }
    if (organizationId.trim().length === 0) {
      errors.organizationId = 'people.validationOrganizationRequired'
    }
    if (departmentId.trim().length === 0) {
      errors.departmentId = 'people.validationDepartmentRequired'
    } else if (organizationId.trim().length > 0 && !hasSelectedDepartmentPath()) {
      errors.departmentId = 'people.validationDepartmentPositionRequired'
    }
    if (isCreating && hasPositionCatalog && departmentId.trim().length > 0 && !selectedPosition()) {
      errors.positionId = 'people.validationPositionRequired'
    }
    if (!hasRequiredPersona) {
      errors.responsibilityBoundary =
        responsibilityBoundary.trim().length === 0 ? 'people.validationResponsibilityRequired' : 'people.validationChineseResponsibility'
    }
    if (isHuman) {
      if (accountUsername.trim().length === 0) {
        errors.accountUsername = 'people.validationUsernameRequired'
      }
    }

    return errors
  }

  function validationMessage(field: ValidationField) {
    const message = validationErrors[field]
    return validationAttempted && message ? t(message) : ''
  }

  function projectionStatusLabel(status: string) {
    switch (status) {
      case 'applied': return t('people.provisioningApplied')
      case 'pending': return t('people.provisioningPending')
      case 'processing': return t('people.provisioningProcessing')
      case 'provisioning_failed': return t('people.provisioningFailed')
      default: return status
    }
  }

  function editorTabLabel(label: TranslationKey, tab: EditorTab) {
    const count = validationAttempted ? tabValidationCount(tab) : 0
    return count > 0 ? `${t(label)} (${count})` : t(label)
  }

  function tabValidationCount(tab: EditorTab) {
    const fields: Record<EditorTab, ValidationField[]> = {
      profile: ['displayName', 'mailboxEmail', 'responsibilityBoundary'],
      access: ['organizationId', 'departmentId', 'positionId'],
      security: ['accountUsername'],
      capabilities: []
    }
    return fields[tab].filter((field) => validationErrors[field]).length
  }

  function firstInvalidTab(): EditorTab {
    for (const tab of ['profile', 'access', isHuman ? 'security' : 'capabilities'] as EditorTab[]) {
      if (tabValidationCount(tab) > 0) return tab
    }
    return 'profile'
  }

  function selectedDepartment() {
    return selectedDepartments.find((unit) => unit.id === departmentId)
  }

  function selectedPosition() {
    return selectedDepartment()?.positions.find((position) => position.id === positionId && position.departmentId === departmentId)
  }

  function selectDepartment(id: string) {
    departmentId = id
    positionId = ''
  }

  function hasSelectedDepartmentPath() {
    if (organizationTree.length === 0) return false
    const department = selectedDepartment()
    if (!department || department.organizationId !== organizationId) return false
    return true
  }

  function flattenUnits(units: OrganizationUnitViewModel[]): OrganizationUnitViewModel[] {
    return units.flatMap((unit) => [unit, ...flattenUnits(unit.children)])
  }

  function selectedOptionsFromEvent(event: Event & { currentTarget: HTMLSelectElement }) {
    return Array.from(event.currentTarget.selectedOptions).map((option) => option.value)
  }

  function toggleRole(value: string) {
    const selected = selectedValues(roles)
    roles = selected.includes(value)
      ? selected.filter((item) => item !== value).join(', ')
      : [...selected, value].join(', ')
  }

  function transferIn(value: string, additions: string[], setter: (value: string) => void, clearAvailable: (value: string[]) => void, clearSelected: (value: string[]) => void) {
    setter(buildTransferInValue(value, additions))
    clearAvailable([])
    clearSelected([])
  }

  function transferOut(value: string, removals: string[], setter: (value: string) => void, clearAvailable: (value: string[]) => void, clearSelected: (value: string[]) => void) {
    setter(buildTransferOutValue(value, removals))
    clearAvailable([])
    clearSelected([])
  }

  function addToolValues(values: string[]) {
    toolRefs = buildTransferInValue(toolRefs, values)
    toolAvailableSelection = []
    toolSelectedSelection = []
  }

  function removeToolValues(values: string[]) {
    toolRefs = buildTransferOutValue(toolRefs, values)
    toolAvailableSelection = []
    toolSelectedSelection = []
  }

  function groupToolTags(options: WorkforceMemberEditorOption[]) {
    const groups = new Map<string, string[]>()
    for (const option of options) {
      if (option.quickAssignable === false) continue
      for (const tag of option.tags ?? []) {
        const normalized = tag.trim()
        if (!normalized) continue
        groups.set(normalized, uniqueValues([...(groups.get(normalized) ?? []), option.value]))
      }
    }
    return Array.from(groups, ([name, values]) => ({ name, values }))
      .sort((left, right) => left.name.localeCompare(right.name))
  }

  function groupToolMcpServers(options: WorkforceMemberEditorOption[]) {
    const groups = new Map<string, { name: string; values: string[] }>()
    for (const option of options) {
      if (option.quickAssignable === false) continue
      if (!option.mcpServerId) continue
      const id = option.mcpServerId
      const group = groups.get(id) ?? { name: option.mcpServerName || id, values: [] }
      group.values = uniqueValues([...group.values, option.value])
      groups.set(id, group)
    }
    return Array.from(groups.values()).sort((left, right) => left.name.localeCompare(right.name))
  }

  function uniqueValues(values: string[]) {
    return Array.from(new Set(values.filter(Boolean)))
  }

  function groupActionLabel(action: TranslationKey, kind: TranslationKey, name: string, count: number) {
    return t(action, { kind: t(kind), name, count })
  }

  function optionDisplay(option: WorkforceMemberEditorOption) {
    return option.meta ? `${option.label} - ${option.meta}` : option.label
  }

  function roleOptionAssignable(option: WorkforceMemberEditorOption) {
    if (!option.assignableActorTypes || option.assignableActorTypes.length === 0) return true
    const actorType = isHuman ? 'human' : 'agent'
    return option.assignableActorTypes.some((value) => {
      const normalized = value.trim().toLowerCase()
      if (actorType === 'human') return normalized === 'human' || normalized === 'user'
      return normalized === 'agent' || normalized === 'ai'
    })
  }

  function transferListSize(options: WorkforceMemberEditorOption[]) {
    return Math.min(Math.max(options.length, 2), 8)
  }

  function selectRuntimeProfile(profileRef: string) {
    runtimeConfigRef = profileRef
    const profile = runtimeOptions.find((option) => option.value === profileRef)
    runtimeKind = profileRef ? profile?.runtimeKind || profile?.meta || runtimeKind : ''
    if (profileRef && !personalAssistant) {
      skillRefs = ''
      toolRefs = ''
      skillAvailableSelection = []
      skillSelectedSelection = []
      toolAvailableSelection = []
      toolSelectedSelection = []
      transferDialog = ''
    }
  }

  function containsHan(value: string) {
    return /[\u3400-\u9fff]/u.test(value)
  }

  function isMailboxAddress(value: string) {
    return /^[^@\s<>]+@[^@\s<>]+$/.test(value.trim())
  }

</script>

{#snippet fieldLabel(label: TranslationKey)}
  <span class="flex h-9 min-w-24 items-center rounded-l-md border border-r-0 border-border bg-muted px-3 text-xs font-medium text-muted-foreground">
    {t(label)}
  </span>
{/snippet}

{#snippet inputField(label: TranslationKey, value: string, setter: (value: string) => void, error = '', type = 'text')}
  <div class="min-w-0">
    <label class="flex min-w-0">
      {@render fieldLabel(label)}
      <Input
        {type}
        name={label}
        autocomplete={type === 'password' ? 'new-password' : 'off'}
        value={value}
        oninput={(event) => setter(event.currentTarget.value)}
        class="rounded-l-none {error ? invalidFieldClasses : ''}"
      />
    </label>
    {#if error}
      <p class="mt-1 text-xs text-destructive">{error}</p>
    {/if}
  </div>
{/snippet}

{#snippet selectField(label: TranslationKey, value: string, setter: (value: string) => void, options: WorkforceMemberEditorOption[], disabled = false, error = '')}
  <div class="min-w-0">
    <label class="flex min-w-0">
      {@render fieldLabel(label)}
      <Select value={value} {options} {disabled} onchange={(event) => setter(event.currentTarget.value)} class="rounded-l-none {error ? invalidFieldClasses : ''}" />
    </label>
    {#if error}
      <p class="mt-1 text-xs text-destructive">{error}</p>
    {/if}
  </div>
{/snippet}

{#snippet textareaField(label: TranslationKey, value: string, setter: (value: string) => void, error = '')}
  <div class="min-w-0 md:col-span-2 xl:col-span-3">
    <label>
      <span class="mb-1 block text-xs font-medium text-muted-foreground">{t(label)}</span>
      <textarea
        value={value}
        rows="5"
        oninput={(event) => setter(event.currentTarget.value)}
        class="border-input dark:bg-input/30 placeholder:text-muted-foreground selection:bg-primary selection:text-primary-foreground min-h-28 w-full resize-y rounded-md border bg-input-background px-3 py-2 text-sm leading-6 shadow-xs outline-none transition-[color,box-shadow] focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] {error ? invalidFieldClasses : ''}"
      ></textarea>
    </label>
    {#if error}
      <p class="mt-1 text-xs text-destructive">{error}</p>
    {/if}
  </div>
{/snippet}

{#snippet transferSummaryField(label: TranslationKey, configureLabel: TranslationKey, value: string, options: WorkforceMemberEditorOption[], openKind: 'skills' | 'tools')}
  {@const selected = selectedSummary(options, value)}
  <div class="min-w-0 md:col-span-2 xl:col-span-3">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <span class="text-xs font-medium text-muted-foreground">{t(label)}</span>
      <Button size="sm" variant="outline" onclick={() => (transferDialog = openKind)}>{t(configureLabel)}</Button>
    </div>
    <div class="mt-2 min-h-10 rounded-md border border-border bg-muted px-3 py-2">
      {#if selected.length > 0}
        <div class="flex flex-wrap gap-1.5">
          {#each selected as option}
            <span class="rounded-md border border-border bg-background px-2 py-1 text-xs text-foreground">{option.label}</span>
          {/each}
          {#if selectedValues(value).length > selected.length}
            <span class="rounded-md border border-border bg-background px-2 py-1 text-xs text-muted-foreground">+{selectedValues(value).length - selected.length}</span>
          {/if}
        </div>
      {:else}
        <span class="text-xs text-muted-foreground">{t('people.notConfigured')}</span>
      {/if}
    </div>
  </div>
{/snippet}

{#snippet transferField(
  label: TranslationKey,
  availableLabel: TranslationKey,
  selectedLabel: TranslationKey,
  addLabel: TranslationKey,
  removeLabel: TranslationKey,
  value: string,
  setter: (value: string) => void,
  options: WorkforceMemberEditorOption[],
  availableSelection: string[],
  setAvailableSelection: (value: string[]) => void,
  selectedSelection: string[],
  setSelectedSelection: (value: string[]) => void
)}
  {@const available = availableOptions(options, value)}
  {@const selected = selectedOptions(options, value)}
  <div class="min-w-0 md:col-span-2 xl:col-span-3">
    <span class="mb-1 block text-xs font-medium text-muted-foreground">{t(label)}</span>
    <div class="grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] lg:items-center">
      <label class="min-w-0">
        <span class="mb-1 block text-xs text-muted-foreground">{t(availableLabel)}</span>
        <select
          multiple
          size={transferListSize(available)}
          disabled={available.length === 0}
          onchange={(event) => setAvailableSelection(selectedOptionsFromEvent(event))}
          class="border-input dark:bg-input/30 min-h-36 w-full rounded-md border bg-input-background px-3 py-2 text-sm shadow-xs outline-none transition-[color,box-shadow] focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50"
        >
          {#each available as option}
            <option value={option.value} selected={availableSelection.includes(option.value)}>{optionDisplay(option)}</option>
          {/each}
        </select>
        {#if options.length === 0}
          <p class="mt-1 text-xs text-muted-foreground">{t('people.resourceCatalogEmpty')}</p>
        {/if}
      </label>

      <div class="flex justify-center gap-2 lg:flex-col">
        <Button
          size="icon"
          variant="outline"
          ariaLabel={t(addLabel)}
          disabled={availableSelection.length === 0}
          onclick={() => transferIn(value, availableSelection, setter, setAvailableSelection, setSelectedSelection)}
        >
          <ArrowRight class="h-4 w-4" />
        </Button>
        <Button
          size="icon"
          variant="outline"
          ariaLabel={t(removeLabel)}
          disabled={selectedSelection.length === 0}
          onclick={() => transferOut(value, selectedSelection, setter, setAvailableSelection, setSelectedSelection)}
        >
          <ArrowLeft class="h-4 w-4" />
        </Button>
      </div>

      <label class="min-w-0">
        <span class="mb-1 block text-xs text-muted-foreground">{t(selectedLabel)}</span>
        <select
          multiple
          size={transferListSize(selected)}
          disabled={selected.length === 0}
          onchange={(event) => setSelectedSelection(selectedOptionsFromEvent(event))}
          class="border-input dark:bg-input/30 min-h-36 w-full rounded-md border bg-input-background px-3 py-2 text-sm shadow-xs outline-none transition-[color,box-shadow] focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50"
        >
          {#each selected as option}
            <option value={option.value} selected={selectedSelection.includes(option.value)}>{optionDisplay(option)}</option>
          {/each}
        </select>
      </label>
    </div>
  </div>
{/snippet}

{#snippet toolQuickAssignPanel()}
  <section class="rounded-md border border-border bg-muted/30 p-3">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
      <div>
        <h3 class="text-sm font-semibold text-foreground">{t('people.toolQuickAssign')}</h3>
      </div>
      <span class="rounded-md border border-border bg-background px-2 py-1 text-xs text-muted-foreground">
        {t('people.toolOptionCount', { count: quickAssignableToolCount })}
      </span>
    </div>
    <div class="grid gap-3 lg:grid-cols-2">
      {@render toolQuickAssignGroup('people.assignByTag', 'people.toolTag', toolTagGroups, 'people.noToolTags')}
      {@render toolQuickAssignGroup('people.assignByMcpServer', 'people.mcpServer', toolMcpServerGroups, 'people.noMcpServerTools')}
    </div>
  </section>
{/snippet}

{#snippet toolQuickAssignGroup(title: TranslationKey, kind: TranslationKey, groups: Array<{ name: string; values: string[] }>, emptyLabel: TranslationKey)}
  <section class="min-w-0 rounded-md border border-border bg-background p-3">
    <h4 class="text-xs font-medium text-muted-foreground">{t(title)}</h4>
    {#if groups.length > 0}
      <div class="mt-2 max-h-44 space-y-2 overflow-y-auto pr-1">
        {#each groups as group}
          <div class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-2 rounded-md border border-border bg-muted/20 px-2 py-1.5">
            <div class="min-w-0">
              <p class="truncate text-sm text-foreground" title={group.name}>{group.name}</p>
              <p class="text-xs text-muted-foreground">{t('people.toolGroupCount', { count: group.values.length })}</p>
            </div>
            <Button
              size="icon"
              variant="outline"
              ariaLabel={groupActionLabel('people.addToolGroup', kind, group.name, group.values.length)}
              onclick={() => addToolValues(group.values)}
              class="h-8 w-8"
            >
              <Plus class="h-4 w-4" />
              <span class="sr-only">{groupActionLabel('people.addToolGroup', kind, group.name, group.values.length)}</span>
            </Button>
            <Button
              size="icon"
              variant="outline"
              ariaLabel={groupActionLabel('people.removeToolGroup', kind, group.name, group.values.length)}
              onclick={() => removeToolValues(group.values)}
              class="h-8 w-8"
            >
              <Minus class="h-4 w-4" />
              <span class="sr-only">{groupActionLabel('people.removeToolGroup', kind, group.name, group.values.length)}</span>
            </Button>
          </div>
        {/each}
      </div>
    {:else}
      <p class="mt-2 rounded-md border border-border bg-muted px-3 py-2 text-xs text-muted-foreground">{t(emptyLabel)}</p>
    {/if}
  </section>
{/snippet}

{#snippet switchField(label: TranslationKey, ariaLabel: TranslationKey, value: string, setter: (value: string) => void)}
  <div class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-3 rounded-md border border-border bg-background px-3 py-2">
    <span class="truncate text-xs font-medium text-muted-foreground">{t(label)}</span>
    <button
      type="button"
      role="switch"
      aria-checked={value === 'true'}
      aria-label={t(ariaLabel)}
      onclick={() => setter(value === 'true' ? 'false' : 'true')}
      class="relative h-6 w-11 shrink-0 rounded-full border transition-colors {value === 'true' ? 'border-brand bg-brand' : 'border-border bg-muted'}"
    >
      <span class="absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-background shadow transition-transform {value === 'true' ? 'translate-x-5' : 'translate-x-0'}"></span>
    </button>
    <span class="justify-self-end text-sm text-foreground">{value === 'true' ? t('kpi.enabled') : t('kpi.disabled')}</span>
  </div>
{/snippet}

{#snippet roleCheckboxField()}
  <div class="min-w-0 md:col-span-2 xl:col-span-3">
    <span class="mb-1 block text-xs font-medium text-muted-foreground">{t('people.permissionRoles')}</span>
    {#if roleSelectOptions.length > 0}
      <div class="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
        {#each roleSelectOptions as option}
          <label class="flex h-9 min-w-0 items-center gap-2 rounded-md border border-border bg-background px-3 text-sm">
            <input
              type="checkbox"
              checked={selectedValues(roles).includes(option.value)}
              onchange={() => toggleRole(option.value)}
              class="h-4 w-4 rounded border-border text-brand focus:ring-ring"
            />
            <span class="truncate text-foreground" title={optionDisplay(option)}>{optionDisplay(option)}</span>
          </label>
        {/each}
      </div>
    {:else}
      <p class="rounded-md border border-border bg-muted px-3 py-2 text-xs text-muted-foreground">{t('people.resourceCatalogEmpty')}</p>
    {/if}
  </div>
{/snippet}

<section class={showTitle ? 'panel p-4' : 'p-0'}>
  {#if showTitle}
    <h2 class="text-sm font-semibold text-foreground">{t('people.member')}</h2>
  {/if}
  <div class="mt-4 overflow-x-auto pb-1">
    <Tabs bind:value={editorTab} tabs={editorTabs} variant="underline" />
  </div>

  <div class="mt-5 min-h-[320px]">
    {#if editorTab === 'profile'}
      <section class="space-y-4">
        <h3 class="text-sm font-semibold text-foreground">{t('people.identitySection')}</h3>
        <div class="grid gap-3 md:grid-cols-2">
          {@render inputField('people.displayName', displayName, (value) => (displayName = value), validationMessage('displayName'))}
          {@render selectField('people.agentKind', agentKind, (value) => (agentKind = value), agentKindOptions)}
          {@render inputField('people.mailboxEmail', mailboxEmail, (value) => (mailboxEmail = value), validationMessage('mailboxEmail'), 'email')}
        </div>
        <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {#if isHuman}
            {@render textareaField('people.resume', resume, (value) => (resume = value))}
          {:else}
            {@render textareaField('people.agentPrompt', prompt, (value) => (prompt = value))}
          {/if}
          {@render textareaField('people.responsibilityBoundary', responsibilityBoundary, (value) => (responsibilityBoundary = value), validationMessage('responsibilityBoundary'))}
          {#if !isHuman}
            {@render textareaField('people.behaviorBoundary', behaviorBoundary, (value) => (behaviorBoundary = value))}
          {/if}
        </div>
      </section>
    {:else if editorTab === 'access'}
      <section class="space-y-5">
        <div class="space-y-3">
          <h3 class="text-sm font-semibold text-foreground">{t('people.placementSection')}</h3>
          <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            {@render selectField('organization.root', organizationId, (value) => (organizationId = value), organizationOptions, organizationTree.length === 0, validationMessage('organizationId'))}
            {@render selectField('people.department', departmentId, selectDepartment, departmentOptions, !organizationId, validationMessage('departmentId'))}
            {#if isCreating && hasPositionCatalog}
              {@render selectField('people.position', positionId, (value) => (positionId = value), positionOptions, !departmentId || !hasSelectedDepartmentPath(), validationMessage('positionId'))}
            {/if}
          </div>
          {#if organizationTree.length === 0}
            <p class="text-xs text-muted-foreground">{t('organization.emptyUnits')}</p>
          {/if}
        </div>
        <div class="border-t border-border pt-5">
          <h3 class="mb-3 text-sm font-semibold text-foreground">{t('people.permissionSection')}</h3>
          {@render roleCheckboxField()}
        </div>
        <div class="border-t border-border pt-5">
          {@render switchField('workspace.memberStatus', 'people.enabledSwitch', enabled, (value) => (enabled = value))}
        </div>
      </section>
    {:else if editorTab === 'security' && isHuman}
      <section class="space-y-5">
        <div class="space-y-3">
          <h3 class="text-sm font-semibold text-foreground">{t('people.accountSection')}</h3>
          <div class="grid gap-3 md:grid-cols-2">
            {@render inputField('people.username', accountUsername, (value) => (accountUsername = value), validationMessage('accountUsername'))}
            {@render inputField('people.email', accountEmail, (value) => (accountEmail = value), '', 'email')}
          </div>
          {@render switchField('people.loginAccount', 'people.loginAccountSwitch', accountEnabled, (value) => (accountEnabled = value))}
        </div>
        {#if accountProjectionStatus}
          <div class="grid gap-3 border-t border-border pt-5 md:grid-cols-2">
            <div class="flex min-h-9 items-center justify-between gap-3 border-b border-border pb-2">
              <span class="text-xs font-medium text-muted-foreground">{t('people.provisioningStatus')}</span>
              <span class="text-sm text-foreground">{projectionStatusLabel(accountProjectionStatus)}</span>
            </div>
            <div class="flex min-h-9 items-center justify-between gap-3 border-b border-border pb-2">
              <span class="text-xs font-medium text-muted-foreground">{t('people.provisioningRevision')}</span>
              <span class="text-sm text-foreground">{accountProjectionRevision}</span>
            </div>
          </div>
        {/if}
      </section>
    {:else if editorTab === 'capabilities' && !isHuman}
      <section class="space-y-5">
        <div class="space-y-3">
          <h3 class="text-sm font-semibold text-foreground">{t('people.runtimeSection')}</h3>
          <div class="grid gap-3 md:grid-cols-2">
            {@render selectField('people.runtime', runtimeConfigRef, selectRuntimeProfile, runtimeSelectOptions)}
            {@render switchField('people.qaEnabled', 'people.qaEnabledSwitch', qaEnabled, (value) => (qaEnabled = value))}
            {@render inputField('people.workspaceRoot', workspaceRoot, (value) => (workspaceRoot = value))}
          </div>
        </div>
        {#if !usesRuntime}
          <div class="grid gap-4 border-t border-border pt-5 md:grid-cols-2 xl:grid-cols-3">
            {@render transferSummaryField('people.skills', 'people.configureSkills', skillRefs, skillTransferOptions, 'skills')}
            {@render transferSummaryField('people.tools', 'people.configureTools', toolRefs, toolTransferOptions, 'tools')}
          </div>
        {/if}
      </section>
    {/if}
  </div>

  <div class="mt-5 flex flex-col gap-3 border-t border-border pt-4 sm:flex-row sm:items-center sm:justify-between">
    {#if validationAttempted && hasValidationErrors}
      <p class="text-xs text-destructive">{t('people.validationFixFields')}</p>
    {:else}
      <span></span>
    {/if}
    <Button disabled={busy} onclick={submit}>
      <Save class="h-4 w-4" />
      {t('people.upsert')}
    </Button>
  </div>
</section>

<Dialog open={transferDialog === 'skills'} title={t('people.configureSkills')} size="wide">
  <div class="space-y-4">
    {@render transferField(
        'people.skills',
        'people.skillsAvailable',
        'people.skillsSelected',
        'people.addSkills',
        'people.removeSkills',
        skillRefs,
        (value) => (skillRefs = value),
        skillTransferOptions,
        skillAvailableSelection,
        (value) => (skillAvailableSelection = value),
        skillSelectedSelection,
        (value) => (skillSelectedSelection = value)
      )}
    <div class="flex justify-end">
      <Button type="button" variant="outline" onclick={() => (transferDialog = '')}>{t('people.transferDone')}</Button>
    </div>
  </div>
</Dialog>

<Dialog open={transferDialog === 'tools'} title={t('people.configureTools')} size="wide">
  <div class="space-y-4">
    {@render toolQuickAssignPanel()}
    {@render transferField(
        'people.tools',
        'people.toolsAvailable',
        'people.toolsSelected',
        'people.addTools',
        'people.removeTools',
        toolRefs,
        (value) => (toolRefs = value),
        toolTransferOptions,
        toolAvailableSelection,
        (value) => (toolAvailableSelection = value),
        toolSelectedSelection,
        (value) => (toolSelectedSelection = value)
      )}
    <div class="flex justify-end">
      <Button type="button" variant="outline" onclick={() => (transferDialog = '')}>{t('people.transferDone')}</Button>
    </div>
  </div>
</Dialog>
