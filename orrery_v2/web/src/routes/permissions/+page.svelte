<script lang="ts">
  import { tick } from 'svelte'
  import LayeredPermissionPolicyManager from '#lib/components/operations/LayeredPermissionPolicyManager.svelte'
  import ErrorState from '#lib/components/ui/ErrorState.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { mapWorkforceView } from '#lib/mappers/workforce'
  import {
    createPermissionDeltaDraft,
    permissionBatchCommand,
    permissionDraftDirty,
    rebasePermissionDeltaDraft,
    resolvePackageGrantState,
    stagePackageChange,
    stageRoleCreate,
    type PermissionDeltaDraft
  } from '#lib/permissions/permissionDeltaDraft'
  import {
    assignedUserRoleIds,
    buildDirectUserRoleAssignment,
    humanAssignableRoles,
    roleSelectionChanged,
    userRoleAssignmentChanges
  } from '#lib/permissions/userRoleAssignments'
  import {
    usePermissionEffectiveGrantsQuery,
    usePermissionPackageCapabilitiesQuery,
    usePermissionPackagesQuery,
    usePermissionPolicyBatchMutation,
    usePermissionRolesQuery
  } from '#lib/queries/operations'
  import {
    combineWorkforcePages,
    useOrgGraphQuery,
    useOrgRoleAssignmentDeleteMutation,
    useOrgRoleAssignmentsMutation,
    useOrgRoleAssignmentsQuery,
    useOrgRolesQuery,
    useWorkforceMembersInfiniteQuery
  } from '#lib/queries/workforce'
  import { ApiClientError } from '#lib/api/client'
  import type {
    PermissionEffectiveGrantPageDTO,
    PermissionPackageCapabilityDTO,
    PermissionPackageDTO,
    PermissionRoleSummaryDTO,
    RoleAssignmentDTO
  } from '#lib/types/api'

  const pageSize = 20
  let t = $derived(createTranslator($locale))

  let roleCursor = $state('')
  let roles = $state<PermissionRoleSummaryDTO[]>([])
  let rolePageSignature = $state('')
  let draft = $state<PermissionDeltaDraft>()
  let selectedRoleId = $state('')

  let packageCursor = $state('')
  let packages = $state<PermissionPackageDTO[]>([])
  let packagePageSignature = $state('')

  let catalogCursor = $state('')
  let catalogPackages = $state<PermissionPackageDTO[]>([])
  let catalogPageSignature = $state('')

  let detailsPackageId = $state('')
  let detailsRoleScoped = $state(true)
  let detailsCursor = $state('')
  let packageDetails = $state<PermissionPackageCapabilityDTO[]>([])
  let detailPageSignature = $state('')

  let effectiveOpened = $state(false)
  let effectiveCursor = $state('')
  let effectiveGrants = $state<PermissionEffectiveGrantPageDTO['items']>([])
  let effectivePageSignature = $state('')
  let message = $state('')

  let usersOpened = $state(false)
  let selectedUserId = $state('')
  let userAssignments = $state<RoleAssignmentDTO[]>([])
  let userAssignmentBaselineRoleIds = $state<string[]>([])
  let selectedUserRoleIds = $state<string[]>([])
  let userAssignmentSignature = $state('')
  let userSelectionTouched = $state(false)
  let userAssignmentMessage = $state('')

  const rolesQuery = usePermissionRolesQuery(() => ({ cursor: roleCursor, limit: pageSize }))
  const stagedRoleIds = $derived(new Set((draft?.roleCreates ?? []).map((role) => role.roleId)))
  const selectedRoleIsDraft = $derived(stagedRoleIds.has(selectedRoleId))
  const packagesQuery = usePermissionPackagesQuery(
    () => ({ roleId: selectedRoleIsDraft ? undefined : selectedRoleId, cursor: packageCursor, limit: pageSize }),
    () => Boolean(selectedRoleId)
  )
  const catalogQuery = usePermissionPackagesQuery(
    () => ({ cursor: catalogCursor, limit: pageSize }),
    () => true
  )
  const detailsQuery = usePermissionPackageCapabilitiesQuery(
    () => detailsPackageId,
    () => ({
      roleId: detailsRoleScoped && !selectedRoleIsDraft ? selectedRoleId || undefined : undefined,
      cursor: detailsCursor,
      limit: pageSize
    }),
    () => Boolean(detailsPackageId)
  )
  const effectiveQuery = usePermissionEffectiveGrantsQuery(
    () => ({ cursor: effectiveCursor, limit: pageSize }),
    () => effectiveOpened
  )
  const saveMutation = usePermissionPolicyBatchMutation()
  const workforceQuery = useWorkforceMembersInfiniteQuery(
    () => ({ enabledOnly: true, includeDetails: true, limit: 50 }),
    () => usersOpened
  )
  const orgRolesQuery = useOrgRolesQuery(() => usersOpened)
  const orgGraphQuery = useOrgGraphQuery(() => ({}), () => usersOpened)
  const userAssignmentsQuery = useOrgRoleAssignmentsQuery(
    () => ({ actorType: 'human', actorId: selectedUserId }),
    () => usersOpened && Boolean(selectedUserId)
  )
  const userAssignmentsMutation = useOrgRoleAssignmentsMutation()
  const userAssignmentDeleteMutation = useOrgRoleAssignmentDeleteMutation()

  const permissionUsers = $derived(
    mapWorkforceView(combineWorkforcePages(workforceQuery.data?.pages ?? []))
      .members
      .filter((member) => member.agentKind.trim().toLowerCase() === 'human')
  )
  const selectedUser = $derived(permissionUsers.find((member) => member.profileId === selectedUserId))
  const assignableRoles = $derived(humanAssignableRoles(orgRolesQuery.data?.items ?? []))
  const assignableRoleIds = $derived(assignableRoles.map((role) => role.role_id))
  const userAssignmentDirty = $derived(roleSelectionChanged(
    userAssignmentBaselineRoleIds,
    selectedUserRoleIds,
    assignableRoleIds
  ))
  const userAssignmentBusy = $derived(userAssignmentsMutation.isPending || userAssignmentDeleteMutation.isPending)

  const visibleRoles = $derived([
    ...roles,
    ...(draft?.roleCreates ?? [])
      .filter((role) => !roles.some((item) => item.role_id === role.roleId))
      .map((role) => ({
        role_id: role.roleId,
        label_zh: role.labelZh,
        label_en: role.labelEn,
        built_in: false,
        granted_package_count: draft?.packageChanges.filter((item) => item.roleId === role.roleId && item.effect === 'grant').length ?? 0
      }))
  ])
  const visiblePackages = $derived(
    packages.map((item) => ({
      ...item,
      grant_state: draft ? resolvePackageGrantState(item.grant_state, draft, selectedRoleId, item.package_id) : item.grant_state
    }))
  )
  const dirty = $derived(Boolean(draft && permissionDraftDirty(draft)))

  $effect(() => {
    if (usersOpened && !selectedUserId && permissionUsers.length > 0) {
      selectUser(permissionUsers[0].profileId)
    }
  })

  $effect(() => {
    const member = selectedUser
    const page = userAssignmentsQuery.data
    if (!member || !page) return
    const matching = page.items.filter((assignment) =>
      assignment.actor?.actor_type === 'human' && assignment.actor.actor_id === member.profileId
    )
    const signature = `${member.profileId}:${matching.map((assignment) => [assignment.assignment_id, assignment.role_id, assignment.source].join(':')).sort().join(',')}`
    if (signature === userAssignmentSignature || (userSelectionTouched && selectedUserId === member.profileId)) return
    userAssignmentSignature = signature
    userAssignments = matching
    const roleIds = assignedUserRoleIds(member.roles, matching)
    userAssignmentBaselineRoleIds = roleIds
    selectedUserRoleIds = roleIds
  })

  $effect(() => {
    const page = rolesQuery.data
    if (!page) return
    const signature = `${roleCursor}:${page.revision}:${page.next_cursor ?? ''}:${page.items.map((item) => item.role_id).join(',')}`
    if (signature === rolePageSignature) return
    rolePageSignature = signature
    const nextRoles = roleCursor ? mergeByKey(roles, page.items, (item) => item.role_id) : page.items
    roles = nextRoles
    if (!selectedRoleId && nextRoles.length > 0) selectRole(nextRoles[0].role_id)
    if (!draft || !permissionDraftDirty(draft)) draft = createPermissionDeltaDraft(page.revision)
  })

  $effect(() => {
    const page = packagesQuery.data
    if (!page || !selectedRoleId) return
    const signature = `${selectedRoleId}:${packageCursor}:${page.next_cursor ?? ''}:${page.items.map((item) => item.package_id).join(',')}`
    if (signature === packagePageSignature) return
    packagePageSignature = signature
    packages = packageCursor ? mergeByKey(packages, page.items, (item) => item.package_id) : page.items
  })

  $effect(() => {
    const page = catalogQuery.data
    if (!page) return
    const signature = `${catalogCursor}:${page.next_cursor ?? ''}:${page.items.map((item) => item.package_id).join(',')}`
    if (signature === catalogPageSignature) return
    catalogPageSignature = signature
    catalogPackages = catalogCursor ? mergeByKey(catalogPackages, page.items, (item) => item.package_id) : page.items
  })

  $effect(() => {
    const page = detailsQuery.data
    if (!page || !detailsPackageId) return
    const signature = `${detailsPackageId}:${detailsCursor}:${page.next_cursor ?? ''}:${page.items.map((item) => item.capability_id).join(',')}`
    if (signature === detailPageSignature) return
    detailPageSignature = signature
    packageDetails = detailsCursor ? mergeByKey(packageDetails, page.items, (item) => item.capability_id) : page.items
  })

  $effect(() => {
    const page = effectiveQuery.data
    if (!page || !effectiveOpened) return
    const signature = `${effectiveCursor}:${page.next_cursor ?? ''}:${page.items.map((item) => `${item.principal_kind}:${item.principal_id}:${item.scope}:${item.source_id}`).join(',')}`
    if (signature === effectivePageSignature) return
    effectivePageSignature = signature
    effectiveGrants = effectiveCursor
      ? mergeByKey(effectiveGrants, page.items, (item) => `${item.principal_kind}:${item.principal_id}:${item.scope}:${item.source_id}`)
      : page.items
  })

  function selectRole(roleId: string) {
    selectedRoleId = roleId
    packageCursor = ''
    packages = []
    packagePageSignature = ''
    detailsPackageId = ''
    packageDetails = []
    message = ''
  }

  function selectUser(profileId: string) {
    if (profileId === selectedUserId) return
    selectedUserId = profileId
    const member = permissionUsers.find((item) => item.profileId === profileId)
    const roleIds = assignedUserRoleIds(member?.roles ?? [])
    userAssignments = []
    userAssignmentBaselineRoleIds = roleIds
    selectedUserRoleIds = roleIds
    userAssignmentSignature = ''
    userSelectionTouched = false
    userAssignmentMessage = ''
  }

  function toggleUserRole(roleId: string, enabled: boolean) {
    const next = new Set(selectedUserRoleIds)
    if (enabled) next.add(roleId)
    else next.delete(roleId)
    selectedUserRoleIds = Array.from(next).sort()
    userSelectionTouched = true
    userAssignmentMessage = ''
  }

  async function saveUserAssignments() {
    const member = selectedUser
    if (!member || !userAssignmentDirty || userAssignmentBusy) return
    const changes = userRoleAssignmentChanges(userAssignmentBaselineRoleIds, selectedUserRoleIds, userAssignments)
    const additions: RoleAssignmentDTO[] = []
    for (const roleId of changes.addedRoleIds) {
      const role = assignableRoles.find((item) => item.role_id === roleId)
      if (!role) continue
      const assignment = buildDirectUserRoleAssignment(member, role, orgGraphQuery.data?.organizations ?? [])
      if (!assignment) {
        userAssignmentMessage = `无法确定 ${member.displayName} 的组织作用域，请先完善该用户的部门归属。`
        return
      }
      additions.push(assignment)
    }

    userAssignmentMessage = ''
    try {
      if (additions.length > 0) {
        await userAssignmentsMutation.mutateAsync({ assignments: additions })
      }
      for (const assignment of changes.removableAssignments) {
        if (assignment.assignment_id) await userAssignmentDeleteMutation.mutateAsync(assignment.assignment_id)
      }
      userAssignmentBaselineRoleIds = [...selectedUserRoleIds]
      userAssignmentSignature = ''
      await userAssignmentsQuery.refetch()
      userSelectionTouched = false
      userAssignmentSignature = ''
      userAssignmentMessage = `已保存 ${member.displayName} 的用户授权。`
      if (effectiveOpened) {
        effectiveCursor = ''
        effectiveGrants = []
        effectivePageSignature = ''
        await tick()
        await effectiveQuery.refetch()
      }
    } catch (error) {
      userAssignmentMessage = error instanceof Error ? error.message : '用户授权保存失败。'
    }
  }

  function openPackageDetails(packageId: string, roleScoped = true) {
    if (detailsPackageId === packageId && detailsRoleScoped === roleScoped) return
    detailsPackageId = packageId
    detailsRoleScoped = roleScoped
    detailsCursor = ''
    packageDetails = []
    detailPageSignature = ''
  }

  function addRole(role: { roleId: string; labelZh: string; labelEn: string }) {
    draft = stageRoleCreate(draft ?? createPermissionDeltaDraft(0), role)
    selectRole(role.roleId)
  }

  function confirmPackageChange(change: { roleId: string; packageId: string; effect: 'grant' | 'revoke' }) {
    draft = stagePackageChange(draft ?? createPermissionDeltaDraft(0), change)
    packages = packages.map((item) => item.package_id === change.packageId
      ? { ...item, grant_state: change.effect === 'grant' ? 'granted' : 'none' }
      : item)
    message = ''
  }

  function savePolicy() {
    if (!draft || !permissionDraftDirty(draft)) return
    message = ''
    saveMutation.mutate(permissionBatchCommand(draft), {
      onSuccess: (result) => {
        draft = createPermissionDeltaDraft(result.revision)
        message = t('permissions.saved')
        roleCursor = ''
        rolePageSignature = ''
        void rolesQuery.refetch()
        void orgRolesQuery.refetch()
        if (selectedRoleId) {
          packageCursor = ''
          packagePageSignature = ''
          void packagesQuery.refetch()
        }
      },
      onError: (error) => {
        if (error instanceof ApiClientError && error.status === 409) {
          message = '权限策略已被其他管理员更新。当前修改已保留，请刷新核对后再次保存。'
          roleCursor = ''
          rolePageSignature = ''
          if (selectedRoleId) {
            packageCursor = ''
            packages = []
            packagePageSignature = ''
          }
          void tick().then(async () => {
            const result = await rolesQuery.refetch()
            if (result.data && draft && permissionDraftDirty(draft)) {
              draft = rebasePermissionDeltaDraft(draft, result.data.revision)
            }
            if (selectedRoleId) await packagesQuery.refetch()
          })
          return
        }
        message = error instanceof Error ? error.message : t('permissions.saveFailed')
      }
    })
  }

  function mergeByKey<T>(current: T[], next: T[], key: (item: T) => string) {
    const merged = new Map(current.map((item) => [key(item), item]))
    for (const item of next) merged.set(key(item), item)
    return Array.from(merged.values())
  }

  function queryErrorMessage(error: unknown) {
    return error instanceof Error ? error.message : ''
  }
</script>

<svelte:head>
  <title>{t('nav.permissions')} | Orrery</title>
</svelte:head>

<div class="page-shell max-w-[1080px] space-y-5">
  <header class="page-header">
    <div>
      <h1 class="page-title">{t('nav.permissions')}</h1>
      <p class="page-description">{t('permissions.description')}</p>
    </div>
  </header>

  {#if rolesQuery.isLoading && roles.length === 0}
    <LoadingState title={t('state.loadingPermissions')} description={t('state.loadingPermissionsDescription')} />
  {:else if rolesQuery.isError && roles.length === 0}
    {@const error = rolesQuery.error as ApiClientError}
    <ErrorState title={t('state.errorPermissions')} message={error.message} correlationId={error.correlationId} />
  {:else}
    <LayeredPermissionPolicyManager
      users={permissionUsers}
      usersLoading={workforceQuery.isFetching}
      usersHasMore={Boolean(workforceQuery.hasNextPage)}
      usersError={queryErrorMessage(workforceQuery.error)}
      {selectedUserId}
      {assignableRoles}
      assignableRolesLoading={orgRolesQuery.isFetching}
      assignableRolesError={queryErrorMessage(orgRolesQuery.error)}
      {userAssignments}
      userAssignmentsLoading={userAssignmentsQuery.isFetching}
      userAssignmentsError={queryErrorMessage(userAssignmentsQuery.error)}
      {selectedUserRoleIds}
      {userAssignmentDirty}
      {userAssignmentBusy}
      {userAssignmentMessage}
      roles={visibleRoles}
      rolesNextCursor={rolesQuery.data?.next_cursor ?? ''}
      rolesLoading={rolesQuery.isFetching}
      {selectedRoleId}
      packages={visiblePackages}
      packagesNextCursor={packagesQuery.data?.next_cursor ?? ''}
      packagesLoading={packagesQuery.isFetching}
      {catalogPackages}
      catalogNextCursor={catalogQuery.data?.next_cursor ?? ''}
      catalogLoading={catalogQuery.isFetching}
      {detailsPackageId}
      {packageDetails}
      detailsNextCursor={detailsQuery.data?.next_cursor ?? ''}
      detailsLoading={detailsQuery.isFetching}
      {effectiveGrants}
      effectiveNextCursor={effectiveQuery.data?.next_cursor ?? ''}
      effectiveLoading={effectiveQuery.isFetching}
      {dirty}
      busy={saveMutation.isPending}
      {message}
      onopenusers={() => (usersOpened = true)}
      onselectuser={selectUser}
      onloadmoreusers={() => void workforceQuery.fetchNextPage()}
      ontoggleuserrole={toggleUserRole}
      onsaveuserroles={() => void saveUserAssignments()}
      onselectrole={selectRole}
      onloadmoreroles={() => (roleCursor = rolesQuery.data?.next_cursor ?? '')}
      onloadmorepackages={() => (packageCursor = packagesQuery.data?.next_cursor ?? '')}
      onloadmorecatalog={() => (catalogCursor = catalogQuery.data?.next_cursor ?? '')}
      onopenpackagedetails={openPackageDetails}
      onloadmoredetails={() => (detailsCursor = detailsQuery.data?.next_cursor ?? '')}
      onopeneffective={() => (effectiveOpened = true)}
      onloadmoreeffective={() => (effectiveCursor = effectiveQuery.data?.next_cursor ?? '')}
      onaddrole={addRole}
      onconfirmpackagechange={confirmPackageChange}
      onsave={savePolicy}
    />
  {/if}
</div>
