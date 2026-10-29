<script lang="ts">
  import { goto } from '$app/navigation'
  import { Bot, CheckCircle2, Copy, Download, RefreshCw, Search, SlidersHorizontal, Trash2, Upload, UserPlus } from 'lucide-svelte'
  import TalentMarketImportDialog from '#lib/components/operations/TalentMarketImportDialog.svelte'
  import TalentMarketHireDialog from '#lib/components/operations/TalentMarketHireDialog.svelte'
  import WorkforceMemberEditor from '#lib/components/operations/WorkforceMemberEditor.svelte'
  import WorkforceMemberList from '#lib/components/operations/WorkforceMemberList.svelte'
  import WorkforceMemberProfilePanel from '#lib/components/operations/WorkforceMemberProfilePanel.svelte'
  import WorkforceMemberSelector from '#lib/components/operations/WorkforceMemberSelector.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { hasAllPermissionScopes } from '#lib/permissions/access'
  import { downloadTalentMarketZip } from '#lib/agent-market/download'
  import { formFromMember, memberType } from '#lib/components/operations/workforceMemberForm'
  import { mapWorkforceView } from '#lib/mappers/workforce'
  import { cancelExportSelection, nextExportSelectionState, retainMarketSelection } from './peopleSelection'
  import {
    useWorkforceAvatarDefaultMutation,
    useWorkforceAvatarUploadMutation,
    useWorkforceDisableMutation,
    useWorkforceEnableMutation,
    useWorkforceMembersInfiniteQuery,
    useWorkforceReleaseMutation,
    useWorkforceUpsertMutation,
    useOrgGraphQuery,
    useTalentMarketExportMutation,
    useTalentMarketBatchDeleteMutation,
    useTalentMarketHireMutation,
    useTalentMarketDeleteMutation,
    useTalentMarketImportCommitMutation,
    useTalentMarketImportPreviewMutation,
    useTalentMarketCountQuery,
    useTalentMarketMembersInfiniteQuery,
    useOrgRolesQuery,
    combineWorkforcePages
  } from '#lib/queries/workforce'
  import {
    useDeleteWorkforceChannelBindingMutation,
    usePutWorkforceChannelBindingMutation,
    useWorkforceChannelBindingsQuery
  } from '#lib/queries/workforce-channel-bindings'
  import { resourceCatalogFilters, useMcpServersQuery, useResourceSkillsQuery, useResourceToolsQuery } from '#lib/queries/resources'
  import { useSettingsQuery } from '#lib/queries/settings'
  import { clearSession, session } from '#lib/stores/session.svelte'
  import type { ApiClientError } from '#lib/api/client'
  import type { TalentMarketImportCandidateDTO, TalentMarketImportPreviewResponseDTO, WorkforceMemberDTO } from '../../lib/types/api/workforce'
  import type { TalentMarketHireFormViewModel, OrganizationUnitViewModel, WorkforceChannelAccountViewModel, WorkforceMemberFormViewModel, WorkforceMemberViewModel } from '#lib/types/view-models'

  interface WorkforceMemberEditorOption {
    label: string
    value: string
    meta?: string
    runtimeKind?: string
    tags?: string[]
    mcpServerId?: string
    mcpServerName?: string
    quickAssignable?: boolean
  }

  type ImportPhase = 'idle' | 'uploading' | 'previewing' | 'ready' | 'committing' | 'complete' | 'failed'
  type ImportLogLevel = 'info' | 'success' | 'warning' | 'error'
  interface ImportLogEntry {
    level: ImportLogLevel
    message: string
    fileName?: string
    at?: string
  }

  interface ImportItemStatus {
    state: 'pending' | 'parsing' | 'ready' | 'importing' | 'imported' | 'skipped' | 'conflict' | 'failed'
    message?: string
  }

  let organizationFilter = $state('')
  let placementView = $state<'internal' | 'market'>('internal')
  let search = $state('')
  let departmentFilter = $state('')
  let typeFilter = $state('')
  let showDirectoryFilters = $state(false)
  let showEditorDialog = $state(false)
  let editorKind = $state<'human' | 'agent'>('agent')
  let editorAction = $state<'create' | 'edit'>('create')
  let editorInitial = $state<Partial<WorkforceMemberFormViewModel>>({})
  let editorKey = $state(0)
  // Query results are immutable snapshots. Keeping the selected snapshot raw
  // preserves its identity when the selection effect reconciles refreshed data.
  let selectedMember = $state.raw<WorkforceMemberViewModel | undefined>()
  let hireMember = $state<WorkforceMemberViewModel | undefined>()
  let selectedMarketIds = $state<string[]>([])
  let marketExportSelectionMode = $state(false)
  let importPreview = $state<TalentMarketImportPreviewResponseDTO | undefined>()
  let importPhase = $state<ImportPhase>('idle')
  let importCurrentFile = $state('')
  let importLogs = $state<ImportLogEntry[]>([])
  let importItemStatuses = $state<Record<number, ImportItemStatus>>({})
  let importImportedCount = $state(0)
  let importFailedCount = $state(0)
  let importSkippedCount = $state(0)
  let importInput = $state<HTMLInputElement | undefined>()
  let loadMoreSentinel = $state<HTMLDivElement | undefined>()
  let busyId = $state('')
  let actionMessage = $state('')
  let initialCredential = $state<{ username: string; password: string } | undefined>()
  let t = $derived(createTranslator($locale))
  const editorTitle = $derived(editorAction === 'edit' ? t('people.editMember') : editorKind === 'human' ? t('people.addPerson') : t('people.registerAgent'))
  const showImportDialog = $derived(importPhase !== 'idle' || importPreview !== undefined)

  const workforceQuery = useWorkforceMembersInfiniteQuery(() => ({
    organizationId: organizationFilter,
    departmentId: departmentFilter,
    enabledOnly: undefined,
    includeDetails: true
  }))
  const marketQuery = useTalentMarketMembersInfiniteQuery(() => ({
    enabledOnly: undefined,
    includeDetails: true
  }), () => placementView === 'market' || marketExportSelectionMode || hireMember !== undefined)
  const marketCountQuery = useTalentMarketCountQuery(() => ({
    enabledOnly: undefined
  }))
  const orgQuery = useOrgGraphQuery(() => ({}))
  const loadEditorResources = $derived(showEditorDialog || hireMember !== undefined)
  const skillsQuery = useResourceSkillsQuery(() => resourceCatalogFilters(), () => loadEditorResources)
  const toolsQuery = useResourceToolsQuery(() => resourceCatalogFilters(), () => loadEditorResources)
  const mcpServersQuery = useMcpServersQuery(() => resourceCatalogFilters(), () => loadEditorResources)
  const settingsQuery = useSettingsQuery(() => loadEditorResources)
  const rolesQuery = useOrgRolesQuery()
  const channelBindingsQuery = useWorkforceChannelBindingsQuery(
    () => selectedMember?.profileId ?? '',
    () => placementView === 'internal' && selectedMember?.agentKind.toLowerCase() !== 'human'
  )
  const putChannelBindingMutation = usePutWorkforceChannelBindingMutation()
  const deleteChannelBindingMutation = useDeleteWorkforceChannelBindingMutation()
  const canManageWorkforce = $derived(hasAllPermissionScopes(session.permissionScope, ['workforce.manage']))
  const canManageChannels = $derived(hasAllPermissionScopes(session.permissionScope, ['workforce.manage', 'settings.manage']))
  const channelBusyAccountId = $derived(
    putChannelBindingMutation.isPending
      ? putChannelBindingMutation.variables?.account.accountId ?? ''
      : deleteChannelBindingMutation.isPending
        ? deleteChannelBindingMutation.variables?.account.accountId ?? ''
        : ''
  )
  const upsertMutation = useWorkforceUpsertMutation()
  const releaseMutation = useWorkforceReleaseMutation()
  const hireMutation = useTalentMarketHireMutation()
  const marketDeleteMutation = useTalentMarketDeleteMutation()
  const marketBatchDeleteMutation = useTalentMarketBatchDeleteMutation()
  const importPreviewMutation = useTalentMarketImportPreviewMutation()
  const importCommitMutation = useTalentMarketImportCommitMutation()
  const exportMutation = useTalentMarketExportMutation()
  const enableMutation = useWorkforceEnableMutation()
  const disableMutation = useWorkforceDisableMutation()
  const avatarUploadMutation = useWorkforceAvatarUploadMutation()
  const avatarDefaultMutation = useWorkforceAvatarDefaultMutation()

  function uploadAvatar(member: WorkforceMemberViewModel, file: File) {
    actionMessage = ''
    if (file.type && !['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) {
      actionMessage = t('people.avatarInvalidType')
      return
    }
    if (file.size > 5 * 1024 * 1024) {
      actionMessage = t('people.avatarTooLarge')
      return
    }
    avatarUploadMutation.mutate({ agentProfileId: member.profileId, file }, {
      onSuccess: () => {
        actionMessage = t('people.avatarUploaded')
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('people.avatarUploadFailed')
      }
    })
  }

  function selectDefaultAvatar(member: WorkforceMemberViewModel, defaultId: string) {
    actionMessage = ''
    avatarDefaultMutation.mutate({ agentProfileId: member.profileId, defaultId }, {
      onSuccess: () => {
        actionMessage = t('people.avatarDefaultUpdated')
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('people.avatarDefaultUpdateFailed')
      }
    })
  }

  function assignChannelAccount(account: WorkforceChannelAccountViewModel) {
    if (!selectedMember || !canManageChannels) return
    putChannelBindingMutation.mutate({
      targetAgentProfileId: selectedMember.profileId,
      account,
      inboundEnabled: false
    })
  }

  function toggleChannelInbound(account: WorkforceChannelAccountViewModel, inboundEnabled: boolean) {
    if (!selectedMember || !canManageChannels) return
    putChannelBindingMutation.mutate({ targetAgentProfileId: selectedMember.profileId, account, inboundEnabled })
  }

  function deleteChannelAccount(account: WorkforceChannelAccountViewModel) {
    if (!selectedMember || !canManageChannels) return
    deleteChannelBindingMutation.mutate({ targetAgentProfileId: selectedMember.profileId, account })
  }

  function upsert(form: WorkforceMemberFormViewModel) {
    actionMessage = ''
    upsertMutation.mutate(form, {
      onSuccess: (member: WorkforceMemberDTO) => {
        if (isCurrentUserPasswordChange(form)) {
          showEditorDialog = false
          selectedMember = undefined
          actionMessage = t('people.passwordChangedRelogin')
          void redirectAfterPasswordChange()
          return
        }
        actionMessage = t('people.saved')
        showEditorDialog = false
        const initialPassword = member.account_projection?.initial_password ?? ''
        if (initialPassword) {
          initialCredential = {
            username: member.user_account?.username || form.accountUsername || '',
            password: initialPassword
          }
        }
        if (form.agentProfileId) selectedMember = undefined
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('people.saveFailed')
      }
    })
  }

  function closeInitialCredential() {
    initialCredential = undefined
  }

  async function copyInitialCredential() {
    if (!initialCredential || typeof navigator === 'undefined' || !navigator.clipboard) return
    await navigator.clipboard.writeText(initialCredential.password)
  }

  function isCurrentUserPasswordChange(form: WorkforceMemberFormViewModel) {
    if (!session.authenticated || form.agentKind !== 'human' || !(form.accountPassword ?? '').trim()) return false
    const editedProfileID = (form.agentProfileId ?? '').trim()
    const currentProfileID = session.user.agentProfileId.trim()
    if (editedProfileID && currentProfileID && editedProfileID === currentProfileID) return true
    return (form.accountUsername ?? '').trim().toLowerCase() === session.username.trim().toLowerCase()
  }

  async function redirectAfterPasswordChange() {
    if (typeof window !== 'undefined') {
      window.alert(t('people.passwordChangedRelogin'))
    }
    clearSession()
    await goto('/login')
  }

  function enable(member: WorkforceMemberViewModel) {
    busyId = member.profileId
    actionMessage = ''
    enableMutation.mutate(member.profileId, {
      onSuccess: () => {
        actionMessage = t('people.enabled', { name: member.displayName })
        busyId = ''
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('people.enableFailed')
        busyId = ''
      }
    })
  }

  function disable(member: WorkforceMemberViewModel) {
    busyId = member.profileId
    actionMessage = ''
    disableMutation.mutate(member.profileId, {
      onSuccess: () => {
        actionMessage = t('people.disabled', { name: member.displayName })
        busyId = ''
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('people.disableFailed')
        busyId = ''
      }
    })
  }

  function remove(member: WorkforceMemberViewModel) {
    busyId = member.profileId
    actionMessage = ''
    releaseMutation.mutate(member.profileId, {
      onSuccess: () => {
        actionMessage = t('people.releasedToMarket', { name: member.displayName })
        busyId = ''
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('people.releaseFailed')
        busyId = ''
      }
    })
  }

  function openHire(member: WorkforceMemberViewModel) {
    hireMember = member
  }

  function hire(form: TalentMarketHireFormViewModel) {
    const displayName = hireMember?.displayName ?? form.agentProfileId
    busyId = form.agentProfileId
    actionMessage = ''
    hireMutation.mutate(form, {
      onSuccess: () => {
        actionMessage = t('people.hiredToUnit', { name: displayName })
        hireMember = undefined
        busyId = ''
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('people.hireFailed')
        busyId = ''
      }
    })
  }

  function deleteMarketTalent(member: WorkforceMemberViewModel) {
    if (!confirm(t('people.deleteMarketConfirm', { name: member.displayName }))) return
    busyId = member.profileId
    actionMessage = ''
    marketDeleteMutation.mutate(member.profileId, {
      onSuccess: () => {
        selectedMarketIds = selectedMarketIds.filter((id) => id !== member.profileId)
        if (selectedMarketIds.length === 0) marketExportSelectionMode = false
        if (selectedMember?.profileId === member.profileId) selectedMember = undefined
        actionMessage = t('people.deleteMarketSucceeded', { name: member.displayName })
        busyId = ''
      },
      onError: (error) => {
        actionMessage = error instanceof Error ? error.message : t('people.deleteMarketFailed')
        busyId = ''
      }
    })
  }

  function deleteSelectedMarket() {
    if (selectedMarketIds.length === 0) {
      actionMessage = t('people.batchDeleteEmpty')
      return
    }
    const count = selectedMarketIds.length
    if (!confirm(t('people.batchDeleteConfirm', { count }))) return
    actionMessage = ''
    marketBatchDeleteMutation.mutate(
      {
        agent_profile_ids: selectedMarketIds,
        reason: 'batch delete from talent market'
      },
      {
        onSuccess: (result) => {
          const deleted = new Set(result.deleted ?? [])
          selectedMarketIds = selectedMarketIds.filter((id) => !deleted.has(id))
          if (selectedMarketIds.length === 0) marketExportSelectionMode = false
          if (selectedMember && deleted.has(selectedMember.profileId)) selectedMember = undefined
          actionMessage = t('people.batchDeleteSucceeded', {
            deleted: result.deleted_count ?? deleted.size,
            skipped: result.skipped?.length ?? 0
          })
        },
        onError: (error) => {
          actionMessage = error instanceof Error ? error.message : t('people.batchDeleteFailed')
        }
      }
    )
  }

  function openImportPicker() {
    importInput?.click()
  }

  function appendImportLog(entry: Omit<ImportLogEntry, 'at'>) {
    importLogs = [...importLogs, { ...entry, at: new Date().toISOString() }]
  }

  function candidateFile(candidate: TalentMarketImportCandidateDTO) {
    return candidate.source_path || candidate.source_slug || candidate.agent_profile_id || candidate.real_name
  }

  function resetImportProgress(fileName: string) {
    importPreview = undefined
    importPhase = 'previewing'
    importCurrentFile = fileName
    importLogs = []
    importItemStatuses = {}
    importImportedCount = 0
    importFailedCount = 0
    importSkippedCount = 0
  }

  function buildPreviewStatuses(preview: TalentMarketImportPreviewResponseDTO) {
    const statuses: Record<number, ImportItemStatus> = {}
    for (const item of preview.items) {
      statuses[item.candidate_index] =
        item.status === 'conflict'
          ? { state: 'conflict', message: t('people.importStatusConflict') }
          : { state: 'ready', message: t('people.importStatusReady') }
    }
    return statuses
  }

  function markCommitPending(candidates: TalentMarketImportCandidateDTO[]) {
    const statuses: Record<number, ImportItemStatus> = {}
    let skipped = 0
    for (const [index, candidate] of candidates.entries()) {
      if (candidate.conflict_resolution === 'skip') {
        skipped += 1
        statuses[index] = { state: 'skipped', message: t('people.importStatusSkipped') }
      } else {
        statuses[index] = { state: 'importing', message: t('people.importStatusImporting') }
      }
    }
    importItemStatuses = statuses
    importImportedCount = 0
    importFailedCount = 0
    importSkippedCount = skipped
    const activeCandidate = candidates.find((candidate) => candidate.conflict_resolution !== 'skip') ?? candidates[0]
    importCurrentFile = activeCandidate ? candidateFile(activeCandidate) : importCurrentFile
  }

  function closeImportDialog() {
    if (importPreviewMutation.isPending || importCommitMutation.isPending) return
    importPreview = undefined
    importPhase = 'idle'
    importCurrentFile = ''
    importLogs = []
    importItemStatuses = {}
    importImportedCount = 0
    importFailedCount = 0
    importSkippedCount = 0
  }

  function handleImportFile(event: Event) {
    const input = event.currentTarget as HTMLInputElement
    const file = input.files?.[0]
    input.value = ''
    if (!file) return
    actionMessage = ''
    resetImportProgress(file.name)
    appendImportLog({
      level: 'info',
      fileName: file.name,
      message: t('people.importLogUploadStarted', { file: file.name })
    })
    importPreviewMutation.mutate(file, {
      onSuccess: (preview) => {
        importPreview = preview
        importPhase = 'ready'
        importCurrentFile = preview.items[0] ? candidateFile(preview.items[0].candidate) : file.name
        importItemStatuses = buildPreviewStatuses(preview)
        appendImportLog({
          level: preview.conflicts.length > 0 ? 'warning' : 'success',
          fileName: file.name,
          message: t('people.importLogPreviewReady', {
            count: preview.items.length,
            conflicts: preview.conflicts.length
          })
        })
        actionMessage = t('people.importPreviewReady', { count: preview.items.length })
      },
      onError: (error) => {
        const message = error instanceof Error ? error.message : t('people.importFailed')
        importPhase = 'failed'
        appendImportLog({
          level: 'error',
          fileName: file.name,
          message: t('people.importLogPreviewFailed', { message })
        })
        actionMessage = message
      }
    })
  }

  function commitImport(candidates: TalentMarketImportCandidateDTO[]) {
    actionMessage = ''
    importPhase = 'committing'
    markCommitPending(candidates)
    appendImportLog({
      level: 'info',
      fileName: importCurrentFile,
      message: t('people.importLogCommitStarted', { count: candidates.length })
    })
    importCommitMutation.mutate(
      { candidates, skills: importPreview?.skills ?? [] },
      {
        onSuccess: (result) => {
          const skippedByIndex = new Map((result.skipped ?? []).map((skipped) => [skipped.candidate_index, skipped]))
          const conflictsByIndex = new Map((result.conflicts ?? []).map((conflict) => [conflict.candidate_index, conflict]))
          const statuses: Record<number, ImportItemStatus> = {}
          let skipped = 0
          let failed = 0

          for (const [index, candidate] of candidates.entries()) {
            const skippedResult = skippedByIndex.get(index)
            const conflict = conflictsByIndex.get(index)

            if (candidate.conflict_resolution === 'skip' || skippedResult) {
              skipped += 1
              const reason = skippedResult?.reason ?? t('people.importStatusSkipped')
              statuses[index] = { state: 'skipped', message: reason }
              appendImportLog({
                level: 'warning',
                fileName: candidateFile(candidate),
                message: t('people.importLogItemSkipped', { name: candidate.real_name, reason })
              })
              continue
            }

            if (conflict) {
              failed += 1
              const reason = t('people.importConflictLine', {
                name: conflict.existing_real_name,
                placement: conflict.existing_placement_status ?? '',
                type: conflict.conflict_type
              })
              statuses[index] = { state: 'failed', message: reason }
              appendImportLog({
                level: 'error',
                fileName: candidateFile(candidate),
                message: t('people.importLogItemFailed', { name: candidate.real_name, reason })
              })
              continue
            }

            statuses[index] = { state: 'imported', message: t('people.importStatusImported') }
          }

          importPreview = result.preview ?? importPreview
          importItemStatuses = statuses
          importImportedCount = result.imported.length
          importFailedCount = failed
          importSkippedCount = skipped
          importCurrentFile = candidates.length > 0 ? candidateFile(candidates[candidates.length - 1]) : importCurrentFile
          importPhase = failed > 0 ? 'failed' : 'complete'
          appendImportLog({
            level: failed > 0 ? 'warning' : 'success',
            fileName: importCurrentFile,
            message: t('people.importLogCommitSucceeded', {
              imported: result.imported.length,
              skipped,
              failed
            })
          })
          actionMessage = t('people.importSucceeded', { count: result.imported.length })
        },
        onError: (error) => {
          const message = error instanceof Error ? error.message : t('people.importFailed')
          const statuses: Record<number, ImportItemStatus> = {}
          let skipped = 0
          let failed = 0
          for (const [index, candidate] of candidates.entries()) {
            if (candidate.conflict_resolution === 'skip') {
              skipped += 1
              statuses[index] = { state: 'skipped', message: t('people.importStatusSkipped') }
            } else {
              failed += 1
              statuses[index] = { state: 'failed', message }
            }
          }
          importPhase = 'failed'
          importItemStatuses = statuses
          importImportedCount = 0
          importFailedCount = failed
          importSkippedCount = skipped
          appendImportLog({
            level: 'error',
            fileName: importCurrentFile,
            message: t('people.importLogCommitFailed', { message })
          })
          actionMessage = message
        }
      }
    )
  }

  function exportSelectedMarket() {
    const nextState = nextExportSelectionState(marketExportSelectionMode, selectedMarketIds)
    marketExportSelectionMode = nextState.mode === 'selecting'

    if (!nextState.shouldExport) {
      if (nextState.shouldShowEmptySelectionMessage) actionMessage = t('people.exportEmpty')
      return
    }
    actionMessage = ''
    exportMutation.mutate(
      { agent_profile_ids: selectedMarketIds },
      {
        onSuccess: (blob) => {
          downloadTalentMarketZip(blob)
          actionMessage = t('people.exportSucceeded', { count: selectedMarketIds.length })
          selectedMarketIds = []
          marketExportSelectionMode = false
        },
        onError: (error) => {
          actionMessage = error instanceof Error ? error.message : t('people.exportFailed')
        }
      }
    )
  }

  function cancelMarketExportSelection() {
    const nextState = cancelExportSelection()
    selectedMarketIds = nextState.selectedIds
    marketExportSelectionMode = nextState.selectionMode
    actionMessage = ''
  }

  function openCreate(kind: 'human' | 'agent') {
    if (!canManageWorkforce) return
    editorKind = kind
    editorAction = 'create'
    editorInitial = {}
    editorKey += 1
    showEditorDialog = true
  }

  function openEdit(member: WorkforceMemberViewModel) {
    editorKind = memberType(member)
    editorAction = 'edit'
    editorInitial = formFromMember(member)
    editorKey += 1
    showEditorDialog = true
  }

  function selectMember(member: WorkforceMemberViewModel) {
    selectedMember = member
  }

  function closeSelectedMember() {
    selectedMember = undefined
  }

  function switchPlacementView(view: 'internal' | 'market') {
    if (placementView === view) return
    placementView = view
    closeSelectedMember()
  }

  function flattenUnits(units: OrganizationUnitViewModel[]): OrganizationUnitViewModel[] {
    return units.flatMap((unit) => [unit, ...flattenUnits(unit.children ?? [])])
  }

  function memberOrganizationName(member: WorkforceMemberViewModel) {
    const organizationId = member.organizationId || member.releasedFromOrganizationId
    return orgQuery.data?.organizations.find((organization) => organization.id === organizationId)?.name ?? ''
  }

  function memberDepartmentName(member: WorkforceMemberViewModel) {
    const departmentId = member.departmentId || member.releasedFromDepartmentId
    if (!departmentId) return ''
    for (const organization of orgQuery.data?.organizations ?? []) {
      const unit = flattenUnits(organization.units).find((candidate) => candidate.id === departmentId)
      if (unit) return unit.name
    }
    return ''
  }

  function memberPositionName(member: WorkforceMemberViewModel) {
    const positionId = member.positionId || member.releasedFromPositionId
    if (!positionId) return ''
    for (const organization of orgQuery.data?.organizations ?? []) {
      const positions = [
        ...(organization.positions ?? []),
        ...flattenUnits(organization.units).flatMap((unit) => unit.positions)
      ]
      const position = positions.find((candidate) => candidate.id === positionId)
      if (position) return position.name
    }
    return ''
  }

  function memberManagerName(member: WorkforceMemberViewModel) {
    const managerId = member.managerId.trim()
    if (!managerId || managerId === 'Not assigned') return ''
    const manager = [...workforceView.members, ...marketView.members].find(
      (candidate) => candidate.profileId === managerId || candidate.id === managerId || candidate.agentInstanceId === managerId
    )
    return manager?.displayName ?? ''
  }

  const organizationFilterOptions = $derived([
    { label: t('filter.allOrganizations'), value: '' },
    ...(orgQuery.data?.organizations ?? []).map((organization) => ({ label: organization.name, value: organization.id }))
  ])

  const filterDepartments = $derived(
    (orgQuery.data?.organizations ?? [])
      .filter((organization) => organizationFilter === '' || organization.id === organizationFilter)
      .flatMap((organization) => organization.units.flatMap((unit) => flattenUnits([unit])))
  )

  const departmentFilterOptions = $derived([
    { label: t('filter.allDepartments'), value: '' },
    ...filterDepartments.map((unit) => ({ label: unit.name, value: unit.id }))
  ])

  $effect(() => {
    if (departmentFilter && !departmentFilterOptions.some((department) => department.value === departmentFilter)) {
      departmentFilter = ''
    }
  })

  $effect(() => {
    if (placementView !== 'market') {
      if (selectedMarketIds.length > 0) selectedMarketIds = []
      if (marketExportSelectionMode) marketExportSelectionMode = false
      return
    }
    const marketIds = new Set((marketView.members ?? []).map((member) => member.profileId))
    const nextSelectedMarketIds = retainMarketSelection(selectedMarketIds, marketIds)
    if (nextSelectedMarketIds !== selectedMarketIds) selectedMarketIds = nextSelectedMarketIds
    if (nextSelectedMarketIds.length === 0 && marketExportSelectionMode && marketIds.size === 0) marketExportSelectionMode = false
  })

  const workforcePage = $derived(combineWorkforcePages(workforceQuery.data?.pages ?? []))
  const marketPage = $derived(combineWorkforcePages(marketQuery.data?.pages ?? []))
  const workforceView = $derived(mapWorkforceView(workforcePage))
  const marketView = $derived(mapWorkforceView(marketPage))
  const activeMembers = $derived(placementView === 'market' ? marketView.members : workforceView.members)
  const activeQueryLoading = $derived(placementView === 'market' ? marketQuery.isLoading : workforceQuery.isLoading)
  const activeQueryError = $derived(placementView === 'market' ? marketQuery.error : workforceQuery.error)
  const activeQueryIsError = $derived(placementView === 'market' ? marketQuery.isError : workforceQuery.isError)
  const activeQueryData = $derived(placementView === 'market' ? marketQuery.data ?? marketPage : workforceQuery.data ?? workforcePage)
  const activeHasMore = $derived(placementView === 'market' ? marketQuery.hasNextPage : workforceQuery.hasNextPage)
  const activeFetchingMore = $derived(placementView === 'market' ? marketQuery.isFetchingNextPage : workforceQuery.isFetchingNextPage)

  function loadMoreActiveMembers() {
    if (placementView === 'market') {
      if (marketQuery.hasNextPage && !marketQuery.isFetchingNextPage) marketQuery.fetchNextPage()
      return
    }
    if (workforceQuery.hasNextPage && !workforceQuery.isFetchingNextPage) workforceQuery.fetchNextPage()
  }

  async function refreshDirectory() {
    actionMessage = ''
    await Promise.all([
      placementView === 'market' ? marketQuery.refetch() : workforceQuery.refetch(),
      marketCountQuery.refetch(),
      orgQuery.refetch()
    ])
  }

  $effect(() => {
    if (!loadMoreSentinel || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          loadMoreActiveMembers()
        }
      },
      { root: null, rootMargin: '320px 0px', threshold: 0 }
    )
    observer.observe(loadMoreSentinel)
    return () => observer.disconnect()
  })

  function handleMemberScroll(event: Event) {
    const element = event.currentTarget as HTMLElement
    if (element.scrollTop + element.clientHeight >= element.scrollHeight - 240) {
      loadMoreActiveMembers()
    }
  }

  const filteredMembers = $derived(
    activeMembers.filter((member) => {
      const needle = search.trim().toLowerCase()
      const haystack = [
        member.displayName,
        member.userAccount.username,
        member.userAccount.email,
        member.positionDisplayName,
        member.departmentDisplayName,
        member.email,
        ...member.capabilityTags
      ]
        .join(' ')
        .toLowerCase()
      const matchesSearch = needle.length === 0 || haystack.includes(needle)
      const matchesDepartment = departmentFilter === '' || member.departmentId === departmentFilter
      const matchesType = typeFilter === '' || memberType(member) === typeFilter

      return matchesSearch && matchesDepartment && matchesType
    })
  )
  // Keep the detail preview anchored to a valid row as filters and query data change.
  $effect(() => {
    if (placementView !== 'internal') return
    const current = selectedMember
      ? filteredMembers.find((member) => member.profileId === selectedMember?.profileId)
      : undefined
    const next = current ?? filteredMembers[0]
    if (selectedMember !== next) selectedMember = next
  })

  const internalMembers = $derived(workforceView.members)
  const marketMembers = $derived(marketView.members)
  const humanCount = $derived(internalMembers.filter((member) => memberType(member) === 'human').length)
  const agentCount = $derived(internalMembers.filter((member) => memberType(member) === 'agent').length)
  const totalMembers = $derived(workforcePage.page?.total ?? internalMembers.length)
  const marketCount = $derived(marketCountQuery.data?.page?.total ?? marketPage.page?.total ?? marketMembers.length)
  const skillOptions = $derived(optionList([
    ...(skillsQuery.data?.skills ?? []).map((skill) => ({
      label: skill.name === skill.id ? skill.id : `${skill.name} (${skill.id})`,
      value: skill.id,
      meta: skill.category
    }))
  ]))
  const toolOptions = $derived(optionList([
    ...(toolsQuery.data?.tools ?? []).map((tool) => ({
      label: tool.name === tool.id ? tool.id : `${tool.name} (${tool.id})`,
      value: tool.id,
      meta: toolMeta(tool),
      tags: tool.tags,
      mcpServerId: tool.serverToolId,
      mcpServerName: serverLabel(tool.serverToolId)
    }))
  ]))
  const roleOptions = $derived(optionList([
    ...(rolesQuery.data?.items ?? [])
      .filter((role) => role.status === 'active' && role.role_id !== 'org.owner')
      .map((role) => ({
        label: role.name === role.role_id ? role.role_id : `${role.name} (${role.role_id})`,
        value: role.role_id,
        meta: roleMeta(role.category, role.permission_refs ?? []),
        category: role.category,
        assignableActorTypes: role.assignable_actor_types,
        permissionRefs: role.permission_refs ?? []
      }))
  ]))
  const runtimeOptions = $derived(optionList([
    ...(settingsQuery.data?.runtimeProfiles ?? [])
      .filter((profile) => profile.enabled)
      .map((profile) => ({
        label: profile.displayName === profile.profileId ? profile.profileId : `${profile.displayName} (${profile.profileId})`,
        value: profile.profileId,
        meta: profile.runtimeKind,
        runtimeKind: profile.runtimeKind
      }))
  ]))
  function optionList(options: WorkforceMemberEditorOption[]) {
    const byValue = new Map<string, WorkforceMemberEditorOption>()
    for (const option of options) {
      if (!option.value || byValue.has(option.value)) continue
      byValue.set(option.value, option)
    }
    return Array.from(byValue.values()).sort((left, right) => left.label.localeCompare(right.label))
  }

  function serverLabel(serverId: string) {
    if (!serverId) return ''
    const server = mcpServersQuery.data?.servers.find((item) => item.id === serverId)
    return server?.serverName ?? serverId
  }

  function toolMeta(tool: { kind: string; serverToolId?: string; downstreamToolName?: string }) {
    if (tool.serverToolId || tool.downstreamToolName) {
      return [serverLabel(tool.serverToolId ?? ''), tool.downstreamToolName].filter(Boolean).join(' - ')
    }
    return tool.kind
  }

  function roleMeta(category: string, permissionRefs: string[]) {
    const scope = category === 'platform' ? 'platform' : 'department'
    if (permissionRefs.length === 0) return scope
    const visible = permissionRefs.slice(0, 3).join(', ')
    const remaining = permissionRefs.length - 3
    return remaining > 0 ? `${scope} | ${visible} +${remaining}` : `${scope} | ${visible}`
  }

</script>

<svelte:head>
  <title>{t('nav.people')} | Orrery</title>
</svelte:head>

<div class="workforce-page page-shell flex min-h-[calc(100vh-6rem)] max-w-[1080px] flex-col gap-4">
  <header class="page-header">
    <div>
      <p class="text-[11px] font-medium uppercase leading-[16.5px] text-muted-foreground">Workforce / Personnel Record</p>
      <h1 class="page-title mt-1">
        {t('nav.people')}
      </h1>
      <p class="page-description">
        {t('people.directorySummary', { humans: humanCount, agents: agentCount })}
      </p>
    </div>
    <div class="flex shrink-0 flex-wrap gap-2">
      {#if placementView === 'market'}
        <input bind:this={importInput} class="hidden" type="file" accept=".zip,application/zip" onchange={handleImportFile} />
        <Button variant="outline" class="!rounded-lg !border-border !px-3 !text-[13px]" disabled={importPreviewMutation.isPending} onclick={openImportPicker}>
          <Upload class="h-4 w-4" />
          {importPreviewMutation.isPending ? t('people.importing') : t('people.importMarket')}
        </Button>
        <Button class="!rounded-lg !px-3 !text-[13px]" disabled={exportMutation.isPending} onclick={exportSelectedMarket}>
          <Download class="h-4 w-4" />
          {#if exportMutation.isPending}
            {t('people.exporting')}
          {:else if marketExportSelectionMode}
            {t('people.exportConfirmSelected', { count: selectedMarketIds.length })}
          {:else}
            {t('people.exportStartSelection')}
          {/if}
        </Button>
        {#if marketExportSelectionMode}
          <Button
            variant="outline"
            class="!rounded-lg !border-border !px-3 !text-[13px]"
            disabled={marketBatchDeleteMutation.isPending || selectedMarketIds.length === 0}
            onclick={deleteSelectedMarket}
          >
            <Trash2 class="h-4 w-4" />
            {marketBatchDeleteMutation.isPending ? t('people.batchDeleting') : t('people.batchDeleteSelected', { count: selectedMarketIds.length })}
          </Button>
          <Button variant="outline" class="!rounded-lg !border-border !px-3 !text-[13px]" disabled={exportMutation.isPending} onclick={cancelMarketExportSelection}>
            {t('people.exportCancelSelection')}
          </Button>
        {/if}
      {:else}
        <Button
          variant="outline"
          class="!rounded-lg !border-border !px-3 !text-[13px]"
          disabled={workforceQuery.isFetching || marketCountQuery.isFetching || orgQuery.isFetching}
          onclick={() => void refreshDirectory()}
        >
          <RefreshCw class="h-3.5 w-3.5" />
          {t('topbar.refresh')}
        </Button>
        {#if canManageWorkforce}
          <Button variant="outline" class="!rounded-lg !border-border !px-3 !text-[13px]" onclick={() => openCreate('human')}>
            <UserPlus class="h-4 w-4" />
            {t('people.addPerson')}
          </Button>
          <Button class="!rounded-lg !px-3 !text-[13px]" onclick={() => openCreate('agent')}>
            <Bot class="h-4 w-4" />
            {t('people.registerAgent')}
          </Button>
        {/if}
      {/if}
    </div>
  </header>

  <section data-people-tabs="directory" class="flex items-center gap-5 border-b border-border">
    <button
      type="button"
      class="-mb-px flex items-center gap-1.5 border-b-2 pb-2.5 text-[13.5px] font-medium transition-colors {placementView === 'internal' ? 'border-brand text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
      onclick={() => switchPlacementView('internal')}
    >
      {t('people.internalTalent')}
      <span class="rounded-full px-1.5 text-[11px] {placementView === 'internal' ? 'bg-brand text-brand-foreground' : 'bg-muted text-muted-foreground'}">{totalMembers}</span>
    </button>
    <button
      type="button"
      class="-mb-px flex items-center gap-1.5 border-b-2 pb-2.5 text-[13.5px] font-medium transition-colors {placementView === 'market' ? 'border-brand text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
      onclick={() => switchPlacementView('market')}
    >
      {t('people.marketTalent')}
      <span class="rounded-full px-1.5 text-[11px] {placementView === 'market' ? 'bg-brand text-brand-foreground' : 'bg-muted text-muted-foreground'}">{marketCount}</span>
    </button>
  </section>

  {#if activeQueryLoading}
    <LoadingState title={t('state.loadingWorkforce')} description={t('state.loadingWorkforceDescription')} />
  {:else if activeQueryIsError}
    {@const error = activeQueryError as ApiClientError}
    <ErrorState title={t('state.errorWorkforce')} message={error.message} correlationId={error.correlationId} />
  {:else if activeQueryData}
    <section data-people-filter-bar="top" class="{placementView === 'internal' ? 'hidden' : 'flex'} flex-wrap items-center gap-2">
      <div class="relative min-w-[220px] flex-1">
        <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground/60" />
        <Input bind:value={search} placeholder={t('people.search')} class="bg-card pl-9 text-sm" />
      </div>
      <div class="w-36">
        <Select
          bind:value={typeFilter}
          class="bg-card text-sm"
          options={[
            { label: t('filter.allMembers'), value: '' },
            { label: t('filter.humanOnly'), value: 'human' },
            { label: t('filter.agentOnly'), value: 'agent' }
          ]}
        />
      </div>
      {#if placementView === 'internal'}
        <label class="w-36">
          <span class="sr-only">{t('filter.organization')}</span>
          <Select bind:value={organizationFilter} options={organizationFilterOptions} disabled={orgQuery.isLoading} class="bg-card text-sm" />
        </label>
        <label class="w-36">
          <span class="sr-only">{t('filter.department')}</span>
          <Select bind:value={departmentFilter} options={departmentFilterOptions} disabled={orgQuery.isLoading || departmentFilterOptions.length <= 1} class="bg-card text-sm" />
        </label>
      {/if}
    </section>

    <!-- Action message toast -->
    {#if actionMessage}
      <div class="flex items-center gap-2 rounded-lg border border-brand/20 bg-brand/5 px-4 py-2.5 text-sm font-medium text-brand animate-in fade-in slide-in-from-top-1 duration-300">
        <CheckCircle2 class="h-4 w-4" />
        {actionMessage}
      </div>
    {/if}

    {#if placementView === 'internal'}
      <div data-people-directory="profile-workspace" class="grid min-h-0 flex-1 items-start gap-5 xl:grid-cols-[268px_minmax(0,1fr)]">
        <section class="min-w-0 xl:sticky xl:top-4">
          <div class="mb-2 flex items-center justify-between gap-3">
            <h2 class="text-[12.5px] font-medium text-foreground">{t('people.personnelRecord')}</h2>
            <span class="text-[11px] text-muted-foreground">{filteredMembers.length}</span>
          </div>
          <div class="relative mb-2">
            <div class="flex items-center gap-2 rounded-lg border border-border bg-card px-2.5 py-1.5 text-muted-foreground">
              <Search class="h-3.5 w-3.5 shrink-0" />
              <input bind:value={search} class="min-w-0 flex-1 bg-transparent text-[12.5px] leading-[18.75px] text-foreground outline-none placeholder:text-muted-foreground" placeholder={t('people.search')} />
              <button type="button" class="grid h-5 w-5 shrink-0 place-items-center rounded-md hover:bg-muted hover:text-foreground" aria-label={t('filter.allMembers')} title={t('filter.allMembers')} aria-expanded={showDirectoryFilters} onclick={() => (showDirectoryFilters = !showDirectoryFilters)}>
                <SlidersHorizontal class="h-3.5 w-3.5" />
              </button>
            </div>
            {#if showDirectoryFilters}
              <div class="absolute inset-x-0 top-full z-20 mt-1.5 space-y-2 rounded-xl border border-border bg-card p-2.5 shadow-lg">
                <Select bind:value={typeFilter} class="!h-8 bg-card !text-[12.5px]" options={[{ label: t('filter.allMembers'), value: '' }, { label: t('filter.humanOnly'), value: 'human' }, { label: t('filter.agentOnly'), value: 'agent' }]} />
                <Select bind:value={organizationFilter} options={organizationFilterOptions} disabled={orgQuery.isLoading} class="!h-8 bg-card !text-[12.5px]" />
                <Select bind:value={departmentFilter} options={departmentFilterOptions} disabled={orgQuery.isLoading || departmentFilterOptions.length <= 1} class="!h-8 bg-card !text-[12.5px]" />
              </div>
            {/if}
          </div>
          <div class="min-h-0 space-y-3 xl:max-h-[calc(100vh-19rem)] xl:overflow-y-auto xl:pr-1 subtle-scrollbar" data-people-member-scroll="vertical" onscroll={handleMemberScroll}>
            <WorkforceMemberSelector
              members={filteredMembers}
              activeId={selectedMember?.profileId ?? ''}
              onselect={selectMember}
            />
            {#if activeFetchingMore}
              <div class="py-4 text-center text-xs text-muted-foreground">
                {t('state.loadingWorkforce')}
              </div>
            {:else if !activeHasMore && filteredMembers.length > 0}
              <div class="py-4 text-center text-xs text-muted-foreground">
                {t('state.noMoreItems')}
              </div>
            {/if}
            <div bind:this={loadMoreSentinel} class="h-px" aria-hidden="true"></div>
          </div>
        </section>

        <section class="min-w-0">
          {#if selectedMember}
            <WorkforceMemberProfilePanel
              member={selectedMember}
              organizationName={memberOrganizationName(selectedMember)}
              departmentName={memberDepartmentName(selectedMember)}
              positionName={memberPositionName(selectedMember)}
              managerName={memberManagerName(selectedMember)}
              busy={busyId === selectedMember.profileId}
              onedit={openEdit}
              onenable={enable}
              ondisable={disable}
              onrelease={remove}
              onavatarupload={uploadAvatar}
              onavatardefault={selectDefaultAvatar}
              avatarUploading={
                (avatarUploadMutation.isPending && avatarUploadMutation.variables?.agentProfileId === selectedMember.profileId) ||
                (avatarDefaultMutation.isPending && avatarDefaultMutation.variables?.agentProfileId === selectedMember.profileId)
              }
              channelAccounts={channelBindingsQuery.data?.accounts ?? []}
              channelBindingsLoading={channelBindingsQuery.isPending}
              channelBindingsError={channelBindingsQuery.error instanceof Error ? channelBindingsQuery.error.message : ''}
              {canManageChannels}
              {channelBusyAccountId}
              onchannelassign={assignChannelAccount}
              onchanneltoggle={toggleChannelInbound}
              onchanneldelete={deleteChannelAccount}
            />
          {:else}
            <div data-people-profile-empty class="grid min-h-[420px] place-items-center border border-dashed border-border bg-muted/10 p-8 text-center">
              <div class="max-w-xs">
                <Bot class="mx-auto h-7 w-7 text-muted-foreground/55" />
                <h2 class="mt-3 text-sm font-semibold text-foreground">{t('people.personnelRecord')}</h2>
                <p class="mt-1.5 text-sm leading-6 text-muted-foreground">{t('people.selectProfile')}</p>
              </div>
            </div>
          {/if}
        </section>
      </div>
    {:else}
      <div class="min-h-0 flex-1 overflow-y-auto subtle-scrollbar" data-people-member-scroll="vertical" onscroll={handleMemberScroll}>
        <WorkforceMemberList
          members={filteredMembers}
          {busyId}
          placementAction="hire"
          showAvailabilityActions={false}
          selectable={marketExportSelectionMode}
          selectedIds={selectedMarketIds}
          onselectionchange={(ids) => (selectedMarketIds = ids)}
          onselect={selectMember}
          ondelete={openHire}
          onpurge={deleteMarketTalent}
        />
        {#if activeFetchingMore}
          <div class="py-4 text-center text-xs text-muted-foreground">
            {t('state.loadingWorkforce')}
          </div>
        {:else if !activeHasMore && filteredMembers.length > 0}
          <div class="py-4 text-center text-xs text-muted-foreground">
            {t('state.noMoreItems')}
          </div>
        {/if}
        <div bind:this={loadMoreSentinel} class="h-px" aria-hidden="true"></div>
      </div>
    {/if}
  {/if}
</div>

{#if placementView === 'market'}
  <!-- Talent profiles remain modal because hiring is an independent flow. -->
  <Dialog open={selectedMember !== undefined} title={selectedMember?.displayName ?? t('people.profile')} size="wide">
    {#if selectedMember}
      <WorkforceMemberProfilePanel
        member={selectedMember}
        organizationName={memberOrganizationName(selectedMember)}
        departmentName={memberDepartmentName(selectedMember)}
        positionName={memberPositionName(selectedMember)}
        managerName={memberManagerName(selectedMember)}
        showFeishuConnections={false}
        onclose={closeSelectedMember}
      />
    {/if}
  </Dialog>
{/if}

<!-- Create / Edit dialog -->
<Dialog open={showEditorDialog} title={editorTitle} size="wide">
  <div class="space-y-5">
    {#key editorKey}
      <WorkforceMemberEditor
        showTitle={false}
        defaultKind={editorKind}
        initial={editorInitial}
        busy={upsertMutation.isPending || orgQuery.isLoading}
        organizationTree={orgQuery.data?.organizations ?? []}
        runtimeOptions={runtimeOptions}
        roleOptions={roleOptions}
        skillOptions={skillOptions}
        toolOptions={toolOptions}
        onsubmit={upsert}
      />
    {/key}
    <div class="flex items-center justify-end gap-2 border-t border-border pt-4">
      <Button type="button" variant="outline" onclick={() => (showEditorDialog = false)}>
        {t('organization.cancel')}
      </Button>
    </div>
  </div>
</Dialog>

<Dialog open={initialCredential !== undefined} title={t('people.initialPasswordTitle')} onClose={closeInitialCredential}>
  {#if initialCredential}
    <div class="space-y-4">
      <div class="grid gap-3">
        <label class="grid gap-1">
          <span class="text-xs font-medium text-muted-foreground">{t('people.username')}</span>
          <input readonly value={initialCredential.username} class="h-9 w-full rounded-md border border-border bg-muted px-3 text-sm text-foreground" />
        </label>
        <div class="grid gap-1">
          <span class="text-xs font-medium text-muted-foreground">{t('people.initialPassword')}</span>
          <div class="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
            <input readonly value={initialCredential.password} class="h-9 min-w-0 rounded-md border border-border bg-muted px-3 font-mono text-sm text-foreground" />
            <Button size="icon" variant="outline" ariaLabel={t('people.copyInitialPassword')} title={t('people.copyInitialPassword')} onclick={copyInitialCredential}>
              <Copy class="h-4 w-4" />
            </Button>
          </div>
        </div>
      </div>
      <div class="flex justify-end">
        <Button onclick={closeInitialCredential}>{t('people.closeInitialPassword')}</Button>
      </div>
    </div>
  {/if}
</Dialog>

<Dialog open={showImportDialog} title={t('people.importMarket')} size="wide" onClose={closeImportDialog}>
  <TalentMarketImportDialog
    preview={importPreview}
    busy={importCommitMutation.isPending}
    phase={importPhase}
    currentFileName={importCurrentFile}
    importedCount={importImportedCount}
    failedCount={importFailedCount}
    skippedCount={importSkippedCount}
    logs={importLogs}
    itemStatuses={importItemStatuses}
    oncancel={closeImportDialog}
    onsubmit={commitImport}
  />
</Dialog>

<TalentMarketHireDialog
  open={hireMember !== undefined}
  member={hireMember}
  organizations={orgQuery.data?.organizations ?? []}
  members={internalMembers}
  busy={hireMutation.isPending || orgQuery.isLoading}
  oncancel={() => (hireMember = undefined)}
  onsubmit={hire}
/>
