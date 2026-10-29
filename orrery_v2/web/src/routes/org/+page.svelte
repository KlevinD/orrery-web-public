<script lang="ts">
  import { AlertCircle, Bot, Building2, Check, Link, Network, Plus, Trash2, UserMinus, Users } from 'lucide-svelte'
  import OrganizationProfilePanel from '#lib/components/operations/OrganizationProfilePanel.svelte'
  import OrgTreeNode from '#lib/components/operations/OrgTreeNode.svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import {
    useOrganizationCreateMutation,
    useOrganizationDeleteMutation,
    useOrganizationOwnerUpdateMutation,
    useOrganizationUnitCreateMutation,
    useOrganizationUnitDeleteMutation,
    useOrgGraphQuery,
    useOrgRoleAssignmentDeleteMutation,
    useWorkforceReleaseMutation
  } from '#lib/queries/workforce'
  import type { ApiClientError } from '#lib/api/client'
  import type { OrganizationTreeViewModel, RoleAssignmentViewModel, WorkforceMemberViewModel } from '#lib/types/view-models'
  import { findOrganizationById, selectInitialOrganizationId } from './organizationSelection'

  let t = $derived(createTranslator($locale))
  let showCreateOrgDialog = $state(false)
  let showCreateUnitDialog = $state(false)
  let showOwnerDialog = $state(false)
  let selectedOrganizationId = $state('')
  let organizationName = $state('')
  let unitOrganizationId = $state('')
  let unitParentDepartmentId = $state('')
  let unitName = $state('')
  let selectedOwnerProfileId = $state('')
  let actionMessage = $state('')
  let createError = $state('')
  let unitError = $state('')
  let ownerError = $state('')
  let deleteError = $state('')
  let releaseError = $state('')
  let releasingMemberId = $state('')
  let deletingRoleAssignmentId = $state('')
  const orgQuery = useOrgGraphQuery(() => ({ includeMembers: true }))
  const createOrgMutation = useOrganizationCreateMutation()
  const createUnitMutation = useOrganizationUnitCreateMutation()
  const updateOwnerMutation = useOrganizationOwnerUpdateMutation()
  const deleteOrgMutation = useOrganizationDeleteMutation()
  const deleteUnitMutation = useOrganizationUnitDeleteMutation()
  const releaseMemberMutation = useWorkforceReleaseMutation()
  const deleteRoleAssignmentMutation = useOrgRoleAssignmentDeleteMutation()

  const canCreateOrganization = $derived(
    organizationName.trim().length > 0
  )
  const canCreateUnit = $derived(
    unitOrganizationId.trim().length > 0 &&
      unitName.trim().length > 0
  )
  const organizationTree = $derived(orgQuery.data?.organizations ?? [])
  const selectedOrganization = $derived(findOrganizationById(organizationTree, selectedOrganizationId))
  const agentCount = $derived((orgQuery.data?.members ?? []).filter((member) => member.agentKind !== 'human').length)
  const organizationNameById = $derived(new Map(organizationTree.map((organization) => [organization.organizationId || organization.id, organization.name])))
  const selectedMembers = $derived(membersForSelectedOrganization(orgQuery.data?.members ?? [], selectedOrganization))
  const ownerCandidates = $derived.by(() => {
    if (!selectedOrganization) return []
    const organizationId = selectedOrganization.organizationId || selectedOrganization.id
    return (orgQuery.data?.members ?? [])
      .filter((member) => member.agentKind === 'human' && member.enabled && member.organizationId === organizationId)
      .toSorted((left, right) => {
        const leftDirect = left.departmentId === selectedOrganization.id ? 1 : 0
        const rightDirect = right.departmentId === selectedOrganization.id ? 1 : 0
        return rightDirect - leftDirect || memberLabel(left).localeCompare(memberLabel(right), 'zh-CN')
      })
  })
  const ownerOptions = $derived([
    { value: '', label: '未设置' },
    ...ownerCandidates.map((member) => ({
      value: member.profileId,
      label: [memberLabel(member), member.positionDisplayName || member.roleName].filter(Boolean).join(' · ')
    }))
  ])
  const selectedIsOrganization = $derived(selectedOrganization?.nodeType === 'company')
  const selectedDeleteLabel = $derived(selectedIsOrganization ? t('organization.delete') : t('organization.deleteUnit'))
  const deletingSelection = $derived(deleteOrgMutation.isPending || deleteUnitMutation.isPending)

  $effect(() => {
    const nextSelection = selectInitialOrganizationId(organizationTree, selectedOrganizationId)
    if (nextSelection !== selectedOrganizationId) selectedOrganizationId = nextSelection
  })

  function closeCreateOrgDialog() {
    showCreateOrgDialog = false
    createError = ''
  }

  function resetCreateOrgForm() {
    organizationName = ''
  }

  function openCreateUnitDialog(organizationId: string, parentDepartmentId = '') {
    unitOrganizationId = organizationId
    unitParentDepartmentId = parentDepartmentId
    unitName = ''
    unitError = ''
    showCreateUnitDialog = true
  }

  function openCreateUnitForSelection() {
    if (!selectedOrganization) return
    const organizationId = selectedOrganization.organizationId || selectedOrganization.id
    const parentDepartmentId = selectedOrganization.nodeType === 'company' ? '' : selectedOrganization.id
    openCreateUnitDialog(organizationId, parentDepartmentId)
  }

  function closeCreateUnitDialog() {
    showCreateUnitDialog = false
    unitError = ''
  }

  function openOwnerDialog() {
    if (!selectedOrganization) return
    selectedOwnerProfileId = selectedOrganization.ownerProfileId || ''
    ownerError = ''
    showOwnerDialog = true
  }

  function closeOwnerDialog() {
    showOwnerDialog = false
    ownerError = ''
  }

  async function updateOwner() {
    if (!selectedOrganization || updateOwnerMutation.isPending) return
    actionMessage = ''
    ownerError = ''
    try {
      await updateOwnerMutation.mutateAsync({
        organizationNodeId: selectedOrganization.id,
        ownerProfileId: selectedOwnerProfileId
      })
      actionMessage = selectedOwnerProfileId ? '组织负责人已更新' : '组织负责人已清除'
      showOwnerDialog = false
    } catch (error) {
      ownerError = error instanceof Error ? error.message : '组织负责人更新失败'
    }
  }

  function createOrganization() {
    if (!canCreateOrganization) return
    actionMessage = ''
    createError = ''
    createOrgMutation.mutate(
      {
        name: organizationName,
      },
      {
        onSuccess: () => {
          actionMessage = t('organization.createSucceeded')
          resetCreateOrgForm()
          showCreateOrgDialog = false
        },
        onError: (error) => {
          createError = error instanceof Error ? error.message : t('organization.createFailed')
        }
      }
    )
  }

  function createUnit() {
    if (!canCreateUnit) return
    actionMessage = ''
    unitError = ''
    createUnitMutation.mutate(
      {
        organizationId: unitOrganizationId,
        parentDepartmentId: unitParentDepartmentId,
        name: unitName
      },
      {
        onSuccess: () => {
          actionMessage = t('organization.unitCreateSucceeded')
          unitName = ''
          showCreateUnitDialog = false
        },
        onError: (error) => {
          unitError = error instanceof Error ? error.message : t('organization.unitCreateFailed')
        }
      }
    )
  }

  async function deleteSelectedOrganizationNode() {
    if (!selectedOrganization || deletingSelection) return
    const name = selectedOrganization.name
    const isOrganization = selectedOrganization.nodeType === 'company'
    const confirmed = window.confirm(deleteConfirmMessage(isOrganization, name))
    if (!confirmed) return
    actionMessage = ''
    deleteError = ''
    try {
      if (isOrganization) {
        await deleteOrgMutation.mutateAsync(selectedOrganization.organizationId || selectedOrganization.id)
        actionMessage = t('organization.deleteSucceeded', { name })
      } else {
        await deleteUnitMutation.mutateAsync(selectedOrganization.id)
        actionMessage = t('organization.unitDeleteSucceeded', { name })
      }
      selectedOrganizationId = ''
    } catch (error) {
      deleteError = error instanceof Error
        ? error.message
        : deleteFallbackMessage(isOrganization)
    }
  }

  async function releaseMemberFromSelection(member: WorkforceMemberViewModel) {
    if (releaseMemberMutation.isPending) return
    const name = memberLabel(member)
    if (!window.confirm(t('organization.confirmRemoveMember', { name }))) return
    actionMessage = ''
    releaseError = ''
    releasingMemberId = member.profileId
    try {
      await releaseMemberMutation.mutateAsync({
        agentProfileId: member.profileId,
        reason: 'removed from organization detail'
      })
      actionMessage = t('organization.memberRemoveSucceeded', { name })
    } catch (error) {
      releaseError = error instanceof Error ? error.message : t('organization.memberRemoveFailed')
    } finally {
      releasingMemberId = ''
    }
  }

  async function deleteRoleAssignment(assignment: RoleAssignmentViewModel) {
    if (!assignment.assignmentId || assignment.source === 'system' || deleteRoleAssignmentMutation.isPending) return
    const confirmed = window.confirm(t('organization.confirmDeleteRoleAssignment', { role: assignment.roleId }))
    if (!confirmed) return
    actionMessage = ''
    deleteError = ''
    deletingRoleAssignmentId = assignment.assignmentId
    try {
      await deleteRoleAssignmentMutation.mutateAsync(assignment.assignmentId)
      actionMessage = t('organization.roleAssignmentDeleteSucceeded', { role: assignment.roleId })
    } catch (error) {
      deleteError = error instanceof Error ? error.message : t('organization.roleAssignmentDeleteFailed')
    } finally {
      deletingRoleAssignmentId = ''
    }
  }

  function deleteConfirmMessage(isOrganization: boolean, name: string) {
    return isOrganization
      ? t('organization.confirmDelete', { name })
      : t('organization.confirmDeleteUnit', { name })
  }

  function deleteFallbackMessage(isOrganization: boolean) {
    return isOrganization ? t('organization.deleteFailed') : t('organization.unitDeleteFailed')
  }

  function selectOrganization(id: string) {
    selectedOrganizationId = id
  }

  function memberOrganizationName(member: WorkforceMemberViewModel) {
    return organizationNameById.get(member.organizationId) || member.organizationId || '-'
  }

  function memberLabel(member: WorkforceMemberViewModel) {
    return member.displayName || member.realName || member.profileId
  }

  function membersForSelectedOrganization(members: WorkforceMemberViewModel[], organization?: OrganizationTreeViewModel) {
    if (!organization) return []
    const organizationId = organization.organizationId || organization.id
    if (organization.nodeType === 'company') {
      return members.filter((member) => member.organizationId === organizationId)
    }
    const departmentIds = collectDepartmentIds(organization)
    return members.filter((member) => departmentIds.has(member.departmentId))
  }

  function collectDepartmentIds(organization: OrganizationTreeViewModel): Set<string> {
    return new Set([
      organization.id,
      ...(organization.children ?? []).flatMap((child) => Array.from(collectDepartmentIds(child)))
    ])
  }

</script>

<svelte:head>
  <title>{t('nav.org')} | Orrery</title>
</svelte:head>

<div class="workforce-page page-shell max-w-[1000px] space-y-4">
  <header class="page-header">
    <div>
      <h1 class="page-title">{t('nav.org')}</h1>
      <p class="page-description">{t('nav.org.description')}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <Button href="/squads" variant="outline" class="shrink-0">
        <Users class="h-4 w-4" />
        {t('nav.squads')}
      </Button>
      <Button onclick={() => (showCreateOrgDialog = true)} class="shrink-0">
        <Plus class="h-4 w-4" />
        {t('organization.create')}
      </Button>
    </div>
  </header>
  {#if actionMessage}
      <div
        class="flex items-center gap-2 rounded-lg border border-brand/20 bg-brand/5 px-4 py-3 text-sm font-medium text-brand"
      >
        <span class="inline-flex h-5 w-5 items-center justify-center rounded-full bg-brand/15 text-brand">
          <Check class="h-3 w-3" strokeWidth={3} />
        </span>
        {actionMessage}
      </div>
  {/if}

  {#if deleteError}
      <div class="flex items-center gap-2 rounded-lg border border-status-blocked/25 bg-status-blocked/5 px-4 py-3 text-sm font-medium text-status-blocked">
        {deleteError}
      </div>
  {/if}

  {#if releaseError}
    <div class="flex items-center gap-2 rounded-lg border border-status-blocked/25 bg-status-blocked/5 px-4 py-3 text-sm font-medium text-status-blocked">
      {releaseError}
    </div>
  {/if}

  {#if orgQuery.isLoading}
    <LoadingState title={t('state.loadingOrganization')} description={t('state.loadingOrganizationDescription')} />
  {:else if orgQuery.isError}
    {@const error = orgQuery.error as ApiClientError}
    <ErrorState title={t('state.errorOrganization')} message={error.message} correlationId={error.correlationId} />
  {:else if orgQuery.data}
    <section
      data-org-kpi-grid
      aria-label={t('organization.totalHeadcount')}
      class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4"
    >
      {#each [
        { icon: Network, label: '组织节点', value: orgQuery.data.summary.organizationNodes ?? orgQuery.data.summary.organizations },
        { icon: Building2, label: t('organization.departments'), value: orgQuery.data.summary.departments },
        { icon: Users, label: t('organization.totalHeadcount'), value: orgQuery.data.summary.totalMemberCount ?? orgQuery.data.summary.members },
        { icon: Bot, label: t('squads.kpi.agents'), value: agentCount }
      ] as metric (metric.label)}
        <div class="flex items-center gap-3 rounded-xl border border-border bg-card p-4">
          <span class="grid h-9 w-9 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground">
            <metric.icon class="h-[18px] w-[18px]" />
          </span>
          <span>
            <span class="block text-[22px] font-semibold leading-none text-foreground">{metric.value}</span>
            <span class="mt-1 block text-xs text-muted-foreground">{metric.label}</span>
          </span>
        </div>
      {/each}
    </section>

    <section data-org-workspace="tree-detail" class="grid gap-4 lg:grid-cols-[260px_minmax(0,1fr)]">
      <aside class="self-start overflow-hidden rounded-xl border border-border bg-card">
        <div class="flex items-center justify-between gap-3 border-b border-border px-3 py-2.5">
          <h2 class="truncate text-[13px] font-medium text-foreground">组织树</h2>
          <Button size="icon" variant="ghost" class="h-6 w-6" ariaLabel={t('organization.create')} onclick={() => (showCreateOrgDialog = true)}>
            <Plus class="h-3.5 w-3.5" />
          </Button>
        </div>

        {#if organizationTree.length > 0}
          <div class="max-h-[calc(100vh-18rem)] space-y-0.5 overflow-auto p-1.5 subtle-scrollbar">
            {#each organizationTree as organization (organization.id)}
              <OrgTreeNode node={organization} selectedId={selectedOrganizationId} onselect={selectOrganization} />
            {/each}
          </div>
        {:else}
          <div class="m-2 rounded-md border border-dashed border-border p-4 text-center text-sm text-muted-foreground">
            {t('organization.emptyUnits')}
          </div>
        {/if}
      </aside>

      <div class="min-w-0 space-y-4">
        <OrganizationProfilePanel
          organization={selectedOrganization}
          organizations={organizationTree}
          members={orgQuery.data.members}
          reportingEdges={orgQuery.data.edges}
          onselectOrganization={selectOrganization}
          onCreateUnit={openCreateUnitForSelection}
          onConfigureOwner={openOwnerDialog}
          onDeleteSelection={deleteSelectedOrganizationNode}
          deletingSelection={deletingSelection}
          deleteSelectionLabel={selectedDeleteLabel}
          onDeleteRoleAssignment={deleteRoleAssignment}
          {deletingRoleAssignmentId}
        />
        <section class="rounded-xl border border-border bg-card p-4">
          <div class="flex items-center justify-between gap-3">
            <h2 class="text-sm font-semibold text-foreground">{t('organization.members')}</h2>
            <span class="rounded-md bg-muted px-2 py-1 text-xs font-medium text-muted-foreground">{selectedMembers.length}</span>
          </div>
          {#if selectedMembers.length > 0}
            <div class="mt-3 grid max-h-[28rem] gap-2 overflow-auto pr-1 md:grid-cols-2">
              {#each selectedMembers as member (member.profileId)}
                <div class="flex min-w-0 items-start justify-between gap-3 rounded-md border border-border bg-background px-3 py-2.5 text-xs">
                  <div class="min-w-0">
                    <div class="truncate font-medium text-foreground">{memberLabel(member)}</div>
                    <div class="mt-1 truncate text-muted-foreground">
                      {memberOrganizationName(member)} / {member.departmentDisplayName || '-'} / {member.positionDisplayName || '-'}
                    </div>
                  </div>
                  <Button
                    size="sm"
                    variant="destructive"
                    disabled={releaseMemberMutation.isPending}
                    onclick={() => releaseMemberFromSelection(member)}
                    class="shrink-0"
                  >
                    <UserMinus class="h-3.5 w-3.5" />
                    {releasingMemberId === member.profileId ? t('organization.removingMember') : t('organization.removeMember')}
                  </Button>
                </div>
              {/each}
            </div>
          {:else}
            <div class="mt-3 rounded-md border border-dashed border-border bg-background p-4 text-sm text-muted-foreground">
              {t('organization.emptyMembers')}
            </div>
          {/if}
        </section>
      </div>
    </section>
  {/if}
</div>

<!-- Create Organization Dialog -->
<Dialog open={showCreateOrgDialog} title={t('organization.create')} onClose={closeCreateOrgDialog}>
  <form class="space-y-5" onsubmit={(event) => { event.preventDefault(); createOrganization() }}>
    <div class="space-y-3">
      <label class="grid gap-1.5">
        <span class="text-sm font-medium text-foreground">{t('organization.field.name')}</span>
        <Input bind:value={organizationName} class="h-9" />
      </label>
    </div>

    {#if createError}
      <div class="flex items-center gap-2 rounded-lg border border-status-blocked/25 bg-status-blocked/5 px-4 py-3 text-sm text-status-blocked">
        <AlertCircle class="h-3.5 w-3.5 shrink-0" />
        {createError}
      </div>
    {/if}

    <div class="flex justify-end gap-2 pt-1">
      <Button type="button" variant="outline" onclick={closeCreateOrgDialog}>{t('organization.cancel')}</Button>
      <Button type="submit" disabled={!canCreateOrganization || createOrgMutation.isPending}>
        {createOrgMutation.isPending ? t('organization.creating') : t('organization.submitCreate')}
      </Button>
    </div>
  </form>
</Dialog>
<Dialog open={showOwnerDialog} title="配置组织负责人" onClose={closeOwnerDialog}>
  <form class="space-y-5" onsubmit={(event) => { event.preventDefault(); updateOwner() }}>
    <label class="grid gap-1.5">
      <span class="text-sm font-medium text-foreground">负责人</span>
      <Select bind:value={selectedOwnerProfileId} options={ownerOptions} disabled={updateOwnerMutation.isPending} />
    </label>

    {#if ownerError}
      <div class="flex items-center gap-2 rounded-lg border border-status-blocked/25 bg-status-blocked/5 px-4 py-3 text-sm text-status-blocked">
        <AlertCircle class="h-3.5 w-3.5 shrink-0" />
        {ownerError}
      </div>
    {/if}

    <div class="flex justify-end gap-2 pt-1">
      <Button type="button" variant="outline" onclick={closeOwnerDialog}>取消</Button>
      <Button type="submit" disabled={updateOwnerMutation.isPending}>
        {updateOwnerMutation.isPending ? '保存中' : '保存'}
      </Button>
    </div>
  </form>
</Dialog>
<!-- Create Unit Dialog -->
<Dialog open={showCreateUnitDialog} title={t('organization.addUnit')} onClose={closeCreateUnitDialog}>
  <form class="space-y-5" onsubmit={(event) => { event.preventDefault(); createUnit() }}>
    <div class="space-y-3">
      <label class="grid gap-1.5">
        <span class="text-sm font-medium text-foreground">{t('organization.field.name')}</span>
        <Input bind:value={unitName} class="h-9" />
      </label>
      <div class="flex items-center gap-2 rounded-lg bg-muted/50 px-3 py-2.5 text-xs text-muted-foreground">
        <Link class="h-3 w-3 shrink-0" />
        {t('organization.parentHint')}: <span class="font-mono font-medium text-foreground/80">{unitParentDepartmentId || unitOrganizationId}</span>
      </div>
    </div>

    {#if unitError}
      <div class="flex items-center gap-2 rounded-lg border border-status-blocked/25 bg-status-blocked/5 px-4 py-3 text-sm text-status-blocked">
        <AlertCircle class="h-3.5 w-3.5 shrink-0" />
        {unitError}
      </div>
    {/if}

    <div class="flex justify-end gap-2 pt-1">
      <Button type="button" variant="outline" onclick={closeCreateUnitDialog}>{t('organization.cancel')}</Button>
      <Button type="submit" disabled={!canCreateUnit || createUnitMutation.isPending}>
        {createUnitMutation.isPending ? t('organization.creating') : t('organization.submitCreateUnit')}
      </Button>
    </div>
  </form>
</Dialog>
