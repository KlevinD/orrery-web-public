<script lang="ts">
  import { ChevronDown, ChevronRight, LockKeyhole, Plus, Save, Search, ShieldCheck, UserRound } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import LoadingState from '#lib/components/ui/LoadingState.svelte'
  import Tabs from '#lib/components/ui/Tabs.svelte'
  import type {
    PermissionEffectiveGrantPageDTO,
    PermissionPackageCapabilityDTO,
    PermissionPackageDTO,
    PermissionRoleSummaryDTO,
    RoleAssignmentDTO,
    RoleDefinitionDTO
  } from '#lib/types/api'
  import type { WorkforceMemberViewModel } from '#lib/types/view-models'
  import type { PermissionChangeEffect } from '#lib/permissions/permissionDeltaDraft'

  interface Props {
    users?: WorkforceMemberViewModel[]
    usersLoading?: boolean
    usersHasMore?: boolean
    usersError?: string
    selectedUserId?: string
    assignableRoles?: RoleDefinitionDTO[]
    assignableRolesLoading?: boolean
    assignableRolesError?: string
    userAssignments?: RoleAssignmentDTO[]
    userAssignmentsLoading?: boolean
    userAssignmentsError?: string
    selectedUserRoleIds?: string[]
    userAssignmentDirty?: boolean
    userAssignmentBusy?: boolean
    userAssignmentMessage?: string
    roles?: PermissionRoleSummaryDTO[]
    rolesNextCursor?: string
    rolesLoading?: boolean
    selectedRoleId?: string
    packages?: PermissionPackageDTO[]
    packagesNextCursor?: string
    packagesLoading?: boolean
    catalogPackages?: PermissionPackageDTO[]
    catalogNextCursor?: string
    catalogLoading?: boolean
    detailsPackageId?: string
    packageDetails?: PermissionPackageCapabilityDTO[]
    detailsNextCursor?: string
    detailsLoading?: boolean
    effectiveGrants?: PermissionEffectiveGrantPageDTO['items']
    effectiveNextCursor?: string
    effectiveLoading?: boolean
    dirty?: boolean
    busy?: boolean
    message?: string
    onopenusers?: () => void
    onselectuser?: (profileId: string) => void
    onloadmoreusers?: () => void
    ontoggleuserrole?: (roleId: string, enabled: boolean) => void
    onsaveuserroles?: () => void
    onselectrole?: (roleId: string) => void
    onloadmoreroles?: () => void
    onloadmorepackages?: () => void
    onloadmorecatalog?: () => void
    onopenpackagedetails?: (packageId: string, roleScoped?: boolean) => void
    onloadmoredetails?: () => void
    onopeneffective?: () => void
    onloadmoreeffective?: () => void
    onaddrole?: (role: { roleId: string; labelZh: string; labelEn: string }) => void
    onconfirmpackagechange?: (change: { roleId: string; packageId: string; effect: PermissionChangeEffect }) => void
    onsave?: () => void
  }

  let {
    users = [],
    usersLoading = false,
    usersHasMore = false,
    usersError = '',
    selectedUserId = '',
    assignableRoles = [],
    assignableRolesLoading = false,
    assignableRolesError = '',
    userAssignments = [],
    userAssignmentsLoading = false,
    userAssignmentsError = '',
    selectedUserRoleIds = [],
    userAssignmentDirty = false,
    userAssignmentBusy = false,
    userAssignmentMessage = '',
    roles = [],
    rolesNextCursor = '',
    rolesLoading = false,
    selectedRoleId = '',
    packages = [],
    packagesNextCursor = '',
    packagesLoading = false,
    catalogPackages = [],
    catalogNextCursor = '',
    catalogLoading = false,
    detailsPackageId = '',
    packageDetails = [],
    detailsNextCursor = '',
    detailsLoading = false,
    effectiveGrants = [],
    effectiveNextCursor = '',
    effectiveLoading = false,
    dirty = false,
    busy = false,
    message = '',
    onopenusers,
    onselectuser,
    onloadmoreusers,
    ontoggleuserrole,
    onsaveuserroles,
    onselectrole,
    onloadmoreroles,
    onloadmorepackages,
    onloadmorecatalog,
    onopenpackagedetails,
    onloadmoredetails,
    onopeneffective,
    onloadmoreeffective,
    onaddrole,
    onconfirmpackagechange,
    onsave
  }: Props = $props()

  let tab = $state('users')
  let usersOpened = $state(false)
  let effectiveOpened = $state(false)
  let expandedPackageId = $state('')
  let pendingPackage = $state<PermissionPackageDTO>()
  let pendingEffect = $state<PermissionChangeEffect>('grant')
  let showRoleDialog = $state(false)
  let roleName = $state('')
  let userSearch = $state('')

  const tabs = [
    { value: 'users', label: '用户授权 / User grants', mobileLabel: '用户授权' },
    { value: 'roles', label: '角色列表 / Roles', mobileLabel: '角色' },
    { value: 'catalog', label: '权限字典 / Dictionary', mobileLabel: '权限字典' },
    { value: 'effective', label: '有效权限 / Effective', mobileLabel: '有效权限' }
  ]
  const selectedUser = $derived(users.find((user) => user.profileId === selectedUserId))
  const selectedUserRoles = $derived(new Set(selectedUserRoleIds))
  const selectedAssignableRoleCount = $derived(assignableRoles.filter((role) => selectedUserRoles.has(role.role_id)).length)
  const lockedUserRoles = $derived(new Set(
    userAssignments
      .filter((assignment) => assignment.source === 'system')
      .map((assignment) => assignment.role_id ?? '')
  ))
  const filteredUsers = $derived.by(() => {
    const search = userSearch.trim().toLowerCase()
    if (!search) return users
    return users.filter((user) => [
      user.displayName,
      user.realName,
      user.userAccount.username,
      user.userAccount.email,
      user.profileId
    ].some((value) => value.toLowerCase().includes(search)))
  })
  const selectedRole = $derived(roles.find((role) => role.role_id === selectedRoleId))
  const packageGroups = $derived(groupPackages(packages))

  $effect(() => {
    if (tab === 'users' && !usersOpened) {
      usersOpened = true
      onopenusers?.()
    }
  })

  $effect(() => {
    if (tab === 'effective' && !effectiveOpened) {
      effectiveOpened = true
      onopeneffective?.()
    }
  })

  function selectRole(roleId: string) {
    expandedPackageId = ''
    onselectrole?.(roleId)
  }

  function toggleUserRole(roleId: string) {
    if (lockedUserRoles.has(roleId)) return
    ontoggleuserrole?.(roleId, !selectedUserRoles.has(roleId))
  }

  function toggleDetails(packageId: string, roleScoped = true) {
    if (expandedPackageId === packageId) {
      expandedPackageId = ''
      return
    }
    expandedPackageId = packageId
    onopenpackagedetails?.(packageId, roleScoped)
  }

  function requestPackageChange(permissionPackage: PermissionPackageDTO) {
    pendingPackage = permissionPackage
    pendingEffect = permissionPackage.grant_state === 'granted' ? 'revoke' : 'grant'
    onopenpackagedetails?.(permissionPackage.package_id, true)
  }

  function closeConfirmation() {
    pendingPackage = undefined
  }

  function confirmPackageChange() {
    if (!pendingPackage || !selectedRoleId) return
    onconfirmpackagechange?.({ roleId: selectedRoleId, packageId: pendingPackage.package_id, effect: pendingEffect })
    closeConfirmation()
  }

  function addRole() {
    const label = roleName.trim()
    if (!label) return
    const slug = label.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 48)
    const roleId = uniqueRoleId(`custom.${slug || `role-${shortHash(label)}`}`)
    onaddrole?.({ roleId, labelZh: label, labelEn: slug ? titleCase(slug) : label })
    roleName = ''
    showRoleDialog = false
  }

  function uniqueRoleId(base: string) {
    const existing = new Set(roles.map((role) => role.role_id))
    let candidate = base
    let suffix = 2
    while (existing.has(candidate)) {
      candidate = `${base}-${suffix}`
      suffix += 1
    }
    return candidate
  }

  function titleCase(value: string) {
    return value.split('-').filter(Boolean).map((word) => word[0].toUpperCase() + word.slice(1)).join(' ')
  }

  function shortHash(value: string) {
    let hash = 2166136261
    for (let index = 0; index < value.length; index += 1) {
      hash ^= value.charCodeAt(index)
      hash = Math.imul(hash, 16777619)
    }
    return (hash >>> 0).toString(36)
  }

  function groupPackages(items: PermissionPackageDTO[]) {
    const groups = new Map<string, { id: string; label: string; items: PermissionPackageDTO[] }>()
    for (const item of items) {
      const group = groups.get(item.page_id) ?? {
        id: item.page_id,
        label: `${item.page_label_zh} / ${item.page_label_en}`,
        items: []
      }
      group.items.push(item)
      groups.set(item.page_id, group)
    }
    return Array.from(groups.values())
  }
</script>

<section class="space-y-4">
  <div class="panel p-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold text-foreground">权限策略 / Permission policy</h2>
        <div class="mt-2 flex flex-wrap gap-2">
          <Badge tone="neutral">用户 {users.length}</Badge>
          <Badge tone="neutral">角色 {roles.length}</Badge>
          {#if tab === 'users' && userAssignmentDirty}
            <Badge tone="warning">用户授权未保存</Badge>
          {:else if dirty}
            <Badge tone="warning">角色策略未保存</Badge>
          {/if}
        </div>
      </div>
      {#if tab !== 'users'}
        <Button onclick={onsave} disabled={!dirty || busy} ariaLabel="保存策略 / Save policy">
          <Save class="h-4 w-4" />
          保存策略 / Save policy
        </Button>
      {/if}
    </div>
    {#if message}
      <div class="mt-3 border-l-2 border-brand px-3 text-sm text-muted-foreground">{message}</div>
    {/if}
  </div>

  <Tabs bind:value={tab} {tabs} variant="underline" class="overflow-x-auto" />

  {#if tab === 'users'}
    <div class="grid gap-4 lg:grid-cols-[320px_minmax(0,1fr)]">
      <section class="panel min-w-0 p-4">
        <div class="flex items-center justify-between gap-2">
          <div class="flex items-center gap-2">
            <UserRound class="h-4 w-4 text-brand" />
            <h3 class="text-sm font-semibold text-foreground">选择用户</h3>
          </div>
          <Badge tone="neutral">{users.length}</Badge>
        </div>
        <label class="mt-3 grid gap-1.5 text-xs font-medium text-muted-foreground">
          搜索用户
          <span class="relative block">
            <Search class="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
            <Input bind:value={userSearch} class="pl-9" ariaLabel="搜索用户" />
          </span>
        </label>

        {#if usersError}
          <div class="mt-3 border-l-2 border-destructive px-3 text-sm text-destructive">{usersError}</div>
        {:else if usersLoading && users.length === 0}
          <div class="mt-3"><LoadingState title="正在加载用户" description="" /></div>
        {:else}
          <div class="mt-3 max-h-[520px] divide-y divide-border overflow-y-auto">
            {#each filteredUsers as user (user.profileId)}
              <button
                type="button"
                class="flex min-h-16 w-full items-center gap-3 px-1 py-3 text-left transition-colors hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                class:bg-muted={selectedUserId === user.profileId}
                aria-label={`选择用户 ${user.displayName}`}
                aria-pressed={selectedUserId === user.profileId}
                onclick={() => onselectuser?.(user.profileId)}
              >
                <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-foreground/5 text-sm font-semibold text-foreground">
                  {(user.displayName || user.realName || '?').slice(0, 1).toUpperCase()}
                </span>
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-sm font-medium text-foreground">{user.displayName}</span>
                  <span class="block truncate text-xs text-muted-foreground">{user.userAccount.username || user.userAccount.email || user.profileId}</span>
                </span>
                {#if !user.userAccount.accountEnabled}<Badge tone="neutral">停用</Badge>{/if}
              </button>
            {/each}
          </div>
          {#if filteredUsers.length === 0}
            <div class="py-8 text-center text-sm text-muted-foreground">未找到用户</div>
          {/if}
          {#if usersHasMore}
            <Button class="mt-3 w-full" variant="outline" size="sm" disabled={usersLoading} onclick={onloadmoreusers}>加载更多用户</Button>
          {/if}
        {/if}
      </section>

      <section class="panel min-w-0 p-4">
        {#if !selectedUser}
          <div class="py-16 text-center text-sm text-muted-foreground">请选择要授权的用户</div>
        {:else}
          <div class="flex flex-wrap items-start justify-between gap-3 border-b border-border pb-4">
            <div class="min-w-0">
              <h3 class="truncate text-sm font-semibold text-foreground">{selectedUser.displayName}</h3>
              <div class="mt-1 truncate text-xs text-muted-foreground">{selectedUser.userAccount.username || selectedUser.userAccount.email || selectedUser.profileId}</div>
              <div class="mt-2 flex flex-wrap gap-2">
                <Badge tone="source-human">用户</Badge>
                <Badge tone="neutral">已选 {selectedAssignableRoleCount}</Badge>
              </div>
            </div>
            <Button
              onclick={onsaveuserroles}
              disabled={!userAssignmentDirty || userAssignmentBusy || userAssignmentsLoading}
              ariaLabel={`保存 ${selectedUser.displayName} 的用户授权`}
            >
              <Save class="h-4 w-4" />
              保存授权
            </Button>
          </div>

          {#if userAssignmentMessage}
            <div class="mt-3 border-l-2 border-brand px-3 text-sm text-muted-foreground">{userAssignmentMessage}</div>
          {/if}
          {#if userAssignmentsError}
            <div class="mt-3 border-l-2 border-destructive px-3 text-sm text-destructive">{userAssignmentsError}</div>
          {/if}

          {#if assignableRolesError}
            <div class="mt-4 border-l-2 border-destructive px-3 text-sm text-destructive">{assignableRolesError}</div>
          {:else if assignableRolesLoading || (userAssignmentsLoading && userAssignments.length === 0)}
            <div class="mt-4"><LoadingState title="正在加载可授予角色" description="" /></div>
          {:else}
            <div class="mt-2 divide-y divide-border">
              {#each assignableRoles as role (role.role_id)}
                {@const locked = lockedUserRoles.has(role.role_id)}
                <label class="flex min-h-16 items-start gap-3 py-3" class:cursor-not-allowed={locked}>
                  <input
                    type="checkbox"
                    class="mt-1 h-4 w-4 shrink-0 accent-brand"
                    checked={selectedUserRoles.has(role.role_id)}
                    disabled={locked || userAssignmentBusy}
                    aria-label={`${role.name || role.role_id} 角色`}
                    onchange={() => toggleUserRole(role.role_id)}
                  />
                  <span class="min-w-0 flex-1">
                    <span class="flex flex-wrap items-center gap-2">
                      <span class="text-sm font-medium text-foreground">{role.name || role.role_id}</span>
                      <Badge tone={role.category === 'platform' ? 'blue' : 'teal'}>{role.category}</Badge>
                      {#if locked}<Badge tone="neutral"><LockKeyhole class="mr-1 h-3 w-3" />系统来源</Badge>{/if}
                    </span>
                    <span class="mt-1 block text-xs text-muted-foreground">{role.description || role.role_id}</span>
                    <span class="mt-1 block font-mono text-[11px] text-muted-foreground">{role.permission_refs?.length ?? 0} scopes</span>
                  </span>
                </label>
              {/each}
            </div>
            {#if assignableRoles.length === 0}
              <div class="py-10 text-center text-sm text-muted-foreground">暂无可授予角色</div>
            {/if}
          {/if}
        {/if}
      </section>
    </div>
  {:else if tab === 'roles'}
    <div class="grid gap-4 lg:grid-cols-[320px_minmax(0,1fr)]">
      <section class="panel p-4">
        <div class="flex items-center justify-between gap-2">
          <h3 class="text-sm font-semibold text-foreground">角色列表 / Roles</h3>
          <Button size="sm" onclick={() => (showRoleDialog = true)} ariaLabel="新建角色 / Create role">
            <Plus class="h-4 w-4" /> 新建角色
          </Button>
        </div>
        <div class="mt-3 divide-y divide-border">
          {#each roles as role (role.role_id)}
            <div class="py-3">
              <div class="flex items-start justify-between gap-2">
                <div class="min-w-0">
                  <div class="truncate text-sm font-medium text-foreground">{role.label_zh}</div>
                  <div class="truncate text-xs text-muted-foreground">{role.label_en}</div>
                </div>
                <Badge tone={role.granted_package_count > 0 ? 'success' : 'neutral'}>{role.granted_package_count}</Badge>
              </div>
              <Button
                class="mt-2"
                variant={selectedRoleId === role.role_id ? 'secondary' : 'outline'}
                size="sm"
                ariaLabel={`${role.built_in ? '查看权限 / View permissions' : '编辑权限 / Edit permissions'} for ${role.label_en || role.role_id}`}
                onclick={() => selectRole(role.role_id)}
              >
                {role.built_in ? '查看权限 / View permissions' : '编辑权限 / Edit permissions'}
              </Button>
            </div>
          {/each}
        </div>
        {#if rolesLoading}<div class="py-3 text-sm text-muted-foreground">正在加载角色...</div>{/if}
        {#if rolesNextCursor}
          <Button class="mt-3 w-full" variant="outline" size="sm" onclick={onloadmoreroles}>加载更多角色</Button>
        {/if}
      </section>

      <section class="panel p-4">
        {#if !selectedRole}
          <div class="py-12 text-center text-sm text-muted-foreground">请选择角色</div>
        {:else}
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div>
              <h3 class="text-sm font-semibold text-foreground">{selectedRole.label_zh}</h3>
              <div class="mt-1 font-mono text-xs text-muted-foreground">{selectedRole.role_id}</div>
            </div>
            {#if selectedRole.built_in}<Badge tone="info">系统模板</Badge>{/if}
          </div>
          {#if packagesLoading && packages.length === 0}
            <div class="mt-4"><LoadingState title="正在加载权限包" description="" /></div>
          {:else}
            <div class="mt-4 space-y-4">
              {#each packageGroups as group (group.id)}
                <section class="border-t border-border pt-3">
                  <div class="mb-2 text-xs font-semibold text-muted-foreground">{group.label}</div>
                  <div class="divide-y divide-border">
                    {#each group.items as permissionPackage (permissionPackage.package_id)}
                      <div class="py-3">
                        <div class="flex items-start justify-between gap-3">
                          <button
                            type="button"
                            class="flex min-w-0 items-start gap-2 text-left"
                            aria-label={`展开${permissionPackage.label_zh}`}
                            onclick={() => toggleDetails(permissionPackage.package_id, true)}
                          >
                            {#if expandedPackageId === permissionPackage.package_id}
                              <ChevronDown class="mt-0.5 h-4 w-4 shrink-0" />
                            {:else}
                              <ChevronRight class="mt-0.5 h-4 w-4 shrink-0" />
                            {/if}
                            <span>
                              <span class="block text-sm font-medium text-foreground">{permissionPackage.label_zh}</span>
                              <span class="block text-xs text-muted-foreground">{permissionPackage.label_en} · {permissionPackage.capability_count} operations</span>
                            </span>
                          </button>
                          <Button
                            size="sm"
                            variant={permissionPackage.grant_state === 'granted' ? 'outline' : 'default'}
                            disabled={selectedRole.built_in}
                            ariaLabel={permissionPackage.grant_state === 'granted'
                              ? `撤销${permissionPackage.label_zh} / Revoke ${permissionPackage.label_en}`
                              : `授予${permissionPackage.label_zh} / Grant ${permissionPackage.label_en}`}
                            onclick={() => requestPackageChange(permissionPackage)}
                          >
                            {permissionPackage.grant_state === 'granted' ? '撤销' : '授予'}
                          </Button>
                        </div>
                        {#if expandedPackageId === permissionPackage.package_id}
                          <div class="ml-6 mt-3 border-l border-border pl-3">
                            {#if detailsLoading && detailsPackageId === permissionPackage.package_id}
                              <div class="text-xs text-muted-foreground">正在加载权限明细...</div>
                            {:else if detailsPackageId === permissionPackage.package_id}
                              {#each packageDetails as capability (capability.capability_id)}
                                <div class="py-2 text-xs">
                                  <div class="font-medium text-foreground">{capability.label_zh} / {capability.label_en}</div>
                                  <div class="mt-0.5 font-mono text-muted-foreground">{capability.method} {capability.path}</div>
                                </div>
                              {/each}
                              {#if detailsNextCursor}<Button variant="ghost" size="sm" onclick={onloadmoredetails}>加载更多权限</Button>{/if}
                            {/if}
                          </div>
                        {/if}
                      </div>
                    {/each}
                  </div>
                </section>
              {/each}
              {#if packages.length === 0}<div class="py-8 text-sm text-muted-foreground">暂无权限包</div>{/if}
              {#if packagesNextCursor}<Button variant="outline" onclick={onloadmorepackages}>加载更多权限包</Button>{/if}
            </div>
          {/if}
        {/if}
      </section>
    </div>
  {:else if tab === 'catalog'}
    <div class="panel p-4">
      <h3 class="text-sm font-semibold text-foreground">权限字典 / Dictionary</h3>
      {#if catalogLoading && catalogPackages.length === 0}
        <div class="mt-3"><LoadingState title="正在加载权限字典" description="" /></div>
      {:else}
        <div class="mt-3 divide-y divide-border">
          {#each catalogPackages as permissionPackage (permissionPackage.package_id)}
            <button type="button" class="flex w-full items-center justify-between gap-3 py-3 text-left" onclick={() => toggleDetails(permissionPackage.package_id, false)}>
              <span>
                <span class="block text-sm font-medium text-foreground">{permissionPackage.label_zh} / {permissionPackage.label_en}</span>
                <span class="block text-xs text-muted-foreground">{permissionPackage.scopes.join(', ')} · {permissionPackage.capability_count} operations</span>
              </span>
              {#if expandedPackageId === permissionPackage.package_id}
                <ChevronDown class="h-4 w-4 shrink-0" />
              {:else}
                <ChevronRight class="h-4 w-4 shrink-0" />
              {/if}
            </button>
            {#if expandedPackageId === permissionPackage.package_id}
              <div class="border-l border-border pb-3 pl-3">
                {#if detailsLoading && detailsPackageId === permissionPackage.package_id}
                  <div class="py-2 text-xs text-muted-foreground">正在加载权限明细...</div>
                {:else if detailsPackageId === permissionPackage.package_id}
                  {#each packageDetails as capability (capability.capability_id)}
                    <div class="py-2 text-xs">
                      <div class="font-medium text-foreground">{capability.label_zh} / {capability.label_en}</div>
                      <div class="mt-0.5 font-mono text-muted-foreground">{capability.method} {capability.path} · {capability.scope}</div>
                    </div>
                  {/each}
                  {#if detailsNextCursor}<Button variant="ghost" size="sm" onclick={onloadmoredetails}>加载更多权限</Button>{/if}
                {/if}
              </div>
            {/if}
          {/each}
        </div>
        {#if catalogPackages.length === 0}<div class="py-8 text-sm text-muted-foreground">暂无权限包</div>{/if}
        {#if catalogNextCursor}<Button class="mt-3" variant="outline" onclick={onloadmorecatalog}>加载更多权限包</Button>{/if}
      {/if}
    </div>
  {:else}
    <div class="panel overflow-x-auto p-0">
      {#if effectiveLoading && effectiveGrants.length === 0}
        <div class="p-4"><LoadingState title="正在加载有效权限" description="" /></div>
      {:else}
        <table class="w-full min-w-[680px] text-left text-sm">
          <thead class="bg-muted text-xs text-muted-foreground"><tr><th class="px-4 py-3">主体</th><th class="px-4 py-3">Scope</th><th class="px-4 py-3">来源</th></tr></thead>
          <tbody class="divide-y divide-border">
            {#each effectiveGrants as grant (grant.principal_kind + ':' + grant.principal_id + ':' + grant.scope + ':' + grant.source_id)}
              <tr><td class="px-4 py-3">{grant.principal_kind}:{grant.principal_id}</td><td class="px-4 py-3 font-mono text-xs">{grant.scope}</td><td class="px-4 py-3">{grant.source}:{grant.source_id}</td></tr>
            {/each}
          </tbody>
        </table>
        {#if effectiveGrants.length === 0}<div class="border-t border-border px-4 py-6 text-sm text-muted-foreground">暂无有效授权</div>{/if}
        {#if effectiveNextCursor}<div class="border-t border-border p-3"><Button variant="outline" size="sm" onclick={onloadmoreeffective}>加载更多有效权限</Button></div>{/if}
      {/if}
    </div>
  {/if}
</section>

<Dialog open={showRoleDialog} title="新建角色 / Create role" onClose={() => (showRoleDialog = false)}>
  <div class="space-y-4">
    <div class="flex items-center gap-2 text-sm font-semibold"><ShieldCheck class="h-4 w-4" />角色信息</div>
    <label class="grid gap-1 text-xs font-medium text-muted-foreground">角色名称<Input value={roleName} oninput={(event) => (roleName = event.currentTarget.value)} /></label>
    <div class="flex justify-end gap-2"><Button variant="outline" onclick={() => (showRoleDialog = false)}>取消</Button><Button onclick={addRole} disabled={!roleName.trim()}>确认新建</Button></div>
  </div>
</Dialog>

<Dialog
  open={Boolean(pendingPackage)}
  title={pendingEffect === 'grant' ? '确认授予权限包' : '确认撤销权限包'}
  size="wide"
  onClose={closeConfirmation}
>
  <div class="space-y-4">
    <div>
      <div class="text-sm font-semibold text-foreground">{pendingEffect === 'grant' ? '将授予这些权限' : '将撤销这些权限'}</div>
      <div class="mt-1 text-xs text-muted-foreground">{pendingPackage?.label_zh} / {pendingPackage?.label_en}</div>
    </div>
    <div class="max-h-[45vh] divide-y divide-border overflow-y-auto border-y border-border">
      {#if detailsLoading && packageDetails.length === 0}
        <div class="py-4 text-sm text-muted-foreground">正在加载权限明细...</div>
      {:else}
        {#each packageDetails as capability (capability.capability_id)}
          <div class="py-3">
            <div class="text-sm font-medium text-foreground">{capability.label_zh}</div>
            <div class="mt-0.5 text-xs text-muted-foreground">{capability.label_en}</div>
            <div class="mt-1 font-mono text-xs text-muted-foreground">{capability.method} {capability.path} · {capability.scope}</div>
          </div>
        {/each}
      {/if}
    </div>
    {#if detailsNextCursor}<Button variant="outline" size="sm" onclick={onloadmoredetails}>加载更多权限</Button>{/if}
    <div class="flex justify-end gap-2">
      <Button variant="outline" onclick={closeConfirmation}>取消</Button>
      <Button onclick={confirmPackageChange} disabled={detailsLoading}>{pendingEffect === 'grant' ? '确认授予' : '确认撤销'}</Button>
    </div>
  </div>
</Dialog>
