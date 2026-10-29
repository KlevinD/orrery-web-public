<script lang="ts">
  import { Plus, Save, Search, ShieldCheck, UsersRound } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Dialog from '#lib/components/ui/Dialog.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import Tabs from '#lib/components/ui/Tabs.svelte'
  import type { PermissionPolicyCapabilityViewModel, PermissionPolicyCenterViewModel } from '#lib/types/view-models'

  interface RoleForm {
    roleId: string
    labelZh: string
    labelEn: string
    permissionScopes: string[]
  }

  interface PermissionPackage {
    key: string
    page: string
    kind: string
    scopes: string[]
    accessMode: string
    labelZh: string
    labelEn: string
    categoryLabel: string
    categories: string[]
    capabilities: PermissionPolicyCapabilityViewModel[]
  }

  interface PermissionPackageGroup {
    key: string
    categoryLabel: string
    page: string
    categories: string[]
    packages: PermissionPackage[]
  }

  type GroupSelectionAction = 'all' | 'clear' | 'invert'

  interface Props {
    view: PermissionPolicyCenterViewModel
    dirty?: boolean
    busy?: boolean
    ontogglerolegrant?: (roleId: string, scopes: string[], capabilityIds: string[], enabled: boolean) => void
    onaddrole?: (form: RoleForm) => void
    onsave?: () => void
  }

  let {
    view,
    dirty = false,
    busy = false,
    ontogglerolegrant,
    onaddrole,
    onsave
  }: Props = $props()

  let tab = $state('roles')
  let selectedRoleId = $state('')
  let showRoleDialog = $state(false)
  let roleZh = $state('')
  let selectedNewRolePackageKeys = $state<string[]>([])
  let newRoleSearch = $state('')
  let newRoleCategory = $state('all')
  let newRoleMode = $state('all')
  let catalogSearch = $state('')
  let catalogCategory = $state('all')
  let catalogMode = $state('all')
  let roleSearch = $state('')
  let roleCategory = $state('all')
  let roleMode = $state('all')

  const tabs = [
    { value: 'roles', label: '角色列表 / Roles' },
    { value: 'catalog', label: '权限字典 / Dictionary' },
    { value: 'effective', label: '有效权限 / Effective' }
  ]

  const modeOptions = [
    { label: '全部 / All', value: 'all' },
    { label: '查看 / Read', value: 'read' },
    { label: '编辑 / Write', value: 'write' }
  ]

  const categoryOptions = $derived([
    { label: '全部功能 / All functions', value: 'all' },
    ...Array.from(
      new Map(view.capabilities.map((capability) => [capability.categoryEn, { label: capability.categoryLabel, value: capability.categoryEn }])).values()
    )
  ])

  const selectedRole = $derived(selectedRoleId ? view.roles.find((role) => role.id === selectedRoleId) : undefined)
  const catalogCapabilities = $derived(filterCapabilities(view.capabilities, catalogSearch, catalogCategory, catalogMode))
  const allPermissionPackageGroups = $derived(groupCapabilitiesByPage(view.capabilities))
  const editableRolePackageGroups = $derived(filterPackageGroups(allPermissionPackageGroups, roleSearch, roleCategory, roleMode))
  const newRolePackageGroups = $derived(filterPackageGroups(allPermissionPackageGroups, newRoleSearch, newRoleCategory, newRoleMode))
  const allPermissionPackages = $derived(allPermissionPackageGroups.flatMap((group) => group.packages))

  $effect(() => {
    if (selectedRoleId && !view.roles.some((role) => role.id === selectedRoleId)) {
      selectedRoleId = ''
    }
  })

  function openRoleDialog() {
    showRoleDialog = true
  }

  function closeRoleDialog() {
    showRoleDialog = false
  }

  function addRole() {
    const name = roleZh.trim()
    const generatedRoleId = generateRoleId(name, view.roles.map((role) => role.id))
    const permissionScopes = allPermissionPackages
      .filter((permissionPackage) => selectedNewRolePackageKeys.includes(permissionPackage.key))
      .flatMap((permissionPackage) => permissionPackage.scopes)
    const form = {
      roleId: generatedRoleId,
      labelZh: name,
      labelEn: generateRoleLabelEn(name, generatedRoleId),
      permissionScopes: Array.from(new Set(permissionScopes))
    }
    if (!form.labelZh) return
    onaddrole?.(form)
    roleZh = ''
    newRoleSearch = ''
    newRoleCategory = 'all'
    newRoleMode = 'all'
    selectedNewRolePackageKeys = []
    showRoleDialog = false
  }

  function toggleNewRolePackage(key: string, enabled: boolean) {
    selectedNewRolePackageKeys = enabled
      ? Array.from(new Set([...selectedNewRolePackageKeys, key]))
      : selectedNewRolePackageKeys.filter((item) => item !== key)
  }

  function roleGrant(roleId: string, capabilityId: string) {
    const row = view.rolePermissionRows.find((item) => item.capability.id === capabilityId)
    return row?.roles.find((role) => role.roleId === roleId)?.granted ?? false
  }

  function roleGroupGrant(roleId: string, group: PermissionPackageGroup) {
    const grants = group.packages.map((permissionPackage) => rolePackageGrant(roleId, permissionPackage))
    const checked = grants.length > 0 && grants.every((grant) => grant.checked)
    const any = grants.some((grant) => grant.checked || grant.partial)
    return { checked, any, partial: any && !checked }
  }

  function setRoleGroupGrant(roleId: string, group: PermissionPackageGroup, action: GroupSelectionAction) {
    if (!ontogglerolegrant) return
    if (action === 'invert') {
      for (const permissionPackage of group.packages) {
        const grant = rolePackageGrant(roleId, permissionPackage)
        ontogglerolegrant(
          roleId,
          permissionPackage.scopes,
          permissionPackage.capabilities.map((capability) => capability.id),
          !(grant.checked || grant.partial)
        )
      }
      return
    }
    ontogglerolegrant(
      roleId,
      uniqueGroupScopes(group),
      uniqueGroupCapabilityIds(group),
      action === 'all'
    )
  }

  function newRoleGroupGrant(group: PermissionPackageGroup) {
    const selected = group.packages.filter((permissionPackage) => selectedNewRolePackageKeys.includes(permissionPackage.key)).length
    return {
      checked: selected > 0 && selected === group.packages.length,
      any: selected > 0,
      partial: selected > 0 && selected < group.packages.length
    }
  }

  function setNewRoleGroupGrant(group: PermissionPackageGroup, action: GroupSelectionAction) {
    const groupKeys = group.packages.map((permissionPackage) => permissionPackage.key)
    const covered = new Set(groupKeys)
    const outsideGroup = selectedNewRolePackageKeys.filter((key) => !covered.has(key))
    if (action === 'all') {
      selectedNewRolePackageKeys = [...outsideGroup, ...groupKeys]
      return
    }
    if (action === 'clear') {
      selectedNewRolePackageKeys = outsideGroup
      return
    }
    const selected = new Set(selectedNewRolePackageKeys)
    selectedNewRolePackageKeys = [
      ...outsideGroup,
      ...groupKeys.filter((key) => !selected.has(key))
    ]
  }

  function uniqueGroupScopes(group: PermissionPackageGroup) {
    return Array.from(new Set(group.packages.flatMap((permissionPackage) => permissionPackage.scopes)))
  }

  function uniqueGroupCapabilityIds(group: PermissionPackageGroup) {
    return Array.from(new Set(group.packages.flatMap((permissionPackage) => permissionPackage.capabilities.map((capability) => capability.id))))
  }

  function rolePermissionCount(roleId: string) {
    return allPermissionPackages.reduce((count, permissionPackage) => {
      const grant = rolePackageGrant(roleId, permissionPackage)
      return count + (grant.checked || grant.partial ? 1 : 0)
    }, 0)
  }

  function groupCapabilitiesByPage(capabilities: PermissionPolicyCapabilityViewModel[]) {
    const groups = new Map<string, PermissionPackageGroup>()
    for (const capability of capabilities) {
      const page = capabilityPage(capability)
      const key = page
      const group = groups.get(key)
      if (group) {
        if (!group.categories.includes(capability.categoryLabel)) {
          group.categories = [...group.categories, capability.categoryLabel]
          group.categoryLabel = group.categories.join(' · ')
        }
        addCapabilityToPackage(group, page, capability)
      } else {
        const nextGroup = {
          key,
          categoryLabel: capability.categoryLabel,
          page,
          categories: [capability.categoryLabel],
          packages: []
        }
        addCapabilityToPackage(nextGroup, page, capability)
        groups.set(key, nextGroup)
      }
    }
    for (const group of groups.values()) {
      group.packages = group.packages.map(finalizePermissionPackage).sort((a, b) => packageSortKey(a).localeCompare(packageSortKey(b)))
    }
    return Array.from(groups.values())
  }

  function filterPackageGroups(groups: PermissionPackageGroup[], search: string, category: string, mode: string) {
    return groups
      .map((group) => {
        const packages = group.packages.filter((permissionPackage) => {
          return permissionPackage.capabilities.some((capability) => capabilityMatchesFilter(capability, search, category, mode))
        })
        return {
          ...group,
          categoryLabel: packageGroupCategoryLabel(packages, group.categoryLabel),
          packages
        }
      })
      .filter((group) => group.packages.length > 0)
  }

  function packageGroupCategoryLabel(packages: PermissionPackage[], fallback: string) {
    const categories = Array.from(new Set(packages.flatMap((permissionPackage) => permissionPackage.categories)))
    return categories.length > 0 ? categories.join(' · ') : fallback
  }

  function addCapabilityToPackage(group: PermissionPackageGroup, page: string, capability: PermissionPolicyCapabilityViewModel) {
    const kind = packageKind(capability)
    const key = `${page}:${kind}`
    let permissionPackage = group.packages.find((item) => item.key === key)
    if (!permissionPackage) {
      permissionPackage = {
        key,
        page,
        kind,
        scopes: [],
        accessMode: capability.accessMode,
        labelZh: '',
        labelEn: '',
        categoryLabel: capability.categoryLabel,
        categories: [capability.categoryLabel],
        capabilities: []
      }
      group.packages.push(permissionPackage)
    }
    if (!permissionPackage.scopes.includes(capability.scope)) {
      permissionPackage.scopes = [...permissionPackage.scopes, capability.scope]
    }
    if (!permissionPackage.categories.includes(capability.categoryLabel)) {
      permissionPackage.categories = [...permissionPackage.categories, capability.categoryLabel]
      permissionPackage.categoryLabel = permissionPackage.categories.join(' · ')
    }
    if (capability.accessMode !== 'read') {
      permissionPackage.accessMode = 'write'
    }
    permissionPackage.capabilities.push(capability)
  }

  function finalizePermissionPackage(permissionPackage: PermissionPackage) {
    const subject = packageSubject(permissionPackage)
    const action = packageActionLabel(permissionPackage)
    permissionPackage.labelZh = `${subject.zh}${action.zh}`
    permissionPackage.labelEn = `${subject.en} ${action.en}`.trim()
    return permissionPackage
  }

  function packageSubject(permissionPackage: PermissionPackage) {
    const categories = Array.from(new Set(permissionPackage.capabilities.map((capability) => capability.categoryZh || capability.categoryLabel).filter(Boolean)))
    const enCategories = Array.from(new Set(permissionPackage.capabilities.map((capability) => capability.categoryEn || capability.categoryLabel).filter(Boolean)))
    if (categories.length === 1) {
      return { zh: shortChineseSubject(categories[0]), en: shortEnglishSubject(enCategories[0] || categories[0]) }
    }
    if (permissionPackage.kind === 'read' || permissionPackage.kind === 'write') {
      const [zhPage, enPage] = splitBilingual(permissionPackage.page)
      return { zh: shortChineseSubject(zhPage), en: shortEnglishSubject(enPage || zhPage) }
    }
    const [zhPage, enPage] = splitBilingual(permissionPackage.page)
    return { zh: shortChineseSubject(zhPage), en: shortEnglishSubject(enPage || zhPage) }
  }

  function packageKind(capability: PermissionPolicyCapabilityViewModel) {
    const scope = capability.scope.trim()
    const last = scope.split('.').pop() ?? ''
    if (scope === 'tool.call' || last === 'call') return 'call'
    if (last === 'decide') return 'decide'
    if (last === 'run') return 'run'
    if (last === 'review') return 'review'
    if (last === 'distill') return 'distill'
    if (last === 'read' || capability.accessMode === 'read') return 'read'
    return 'write'
  }

  function packageActionLabel(permissionPackage: PermissionPackage) {
    switch (permissionPackage.kind) {
      case 'call':
        return { zh: '调用', en: 'Call' }
      case 'decide':
        return { zh: '处理', en: 'Decision' }
      case 'run':
        return { zh: '运行', en: 'Run' }
      case 'review':
        return { zh: '审核', en: 'Review' }
      case 'distill':
        return { zh: '沉淀', en: 'Distill' }
      case 'read':
        return { zh: '查看', en: 'Read' }
      default:
        return { zh: '编辑', en: 'Edit' }
    }
  }

  function shortChineseSubject(value: string) {
    return value.replace(/\s*\/.*$/, '').replace(/中心$/, '').replace(/^系统设置$/, '设置').trim()
  }

  function shortEnglishSubject(value: string) {
    return value.replace(/\s+center$/i, '').trim()
  }

  function splitBilingual(value: string) {
    const parts = value.split('/').map((part) => part.trim())
    return [parts[0] ?? value, parts[1] ?? '']
  }

  function packageSortKey(permissionPackage: PermissionPackage) {
    const accessOrder = permissionPackage.accessMode === 'read' ? '0' : '1'
    return `${accessOrder}:${permissionPackage.labelZh}:${permissionPackage.key}`
  }

  function rolePackageGrant(roleId: string, permissionPackage: PermissionPackage) {
    const grantedCount = permissionPackage.capabilities.reduce((count, capability) => count + (roleGrant(roleId, capability.id) ? 1 : 0), 0)
    return {
      checked: grantedCount === permissionPackage.capabilities.length,
      partial: grantedCount > 0 && grantedCount < permissionPackage.capabilities.length,
      grantedCount
    }
  }

  function capabilityPage(capability: PermissionPolicyCapabilityViewModel) {
    switch (capability.domain.trim()) {
      case 'mission':
      case 'project':
      case 'agent':
        return '任务 / Mission'
      case 'approval':
        return '审批中心 / Approval center'
      case 'permissions':
      case 'authorization':
      case 'identity':
        return '权限中心 / Permission center'
      case 'organization':
      case 'workforce':
      case 'roles':
        return '组织中心 / Organization center'
      case 'resource':
      case 'tool':
        return '资源中心 / Resource center'
      case 'memory':
        return '记忆中心 / Memory center'
      case 'knowledge':
        return '知识中心 / Knowledge center'
      case 'settings':
        return '系统设置 / Settings'
      case 'discussion':
        return '聊天 / Chat'
      case 'scheduler':
        return '自动化 / Scheduler'
      case 'todo':
        return '待办 / Todo'
      case 'idea':
        return '想法 / Ideas'
      case 'audit':
        return '审计中心 / Audit center'
      case 'operations':
        return '评估中心 / Evaluation center'
      case 'ontology_scope':
      case 'ontology_schema':
      case 'ontology_context':
      case 'ontology_lineage':
      case 'ontology_governance':
      case 'ontology_projection':
        return '本体中心 / Ontology center'
      default:
        throw new Error(`Unmapped permission domain: ${capability.domain}`)
    }
  }

  function filterCapabilities(capabilities: PermissionPolicyCapabilityViewModel[], search: string, category: string, mode: string) {
    return capabilities.filter((capability) => capabilityMatchesFilter(capability, search, category, mode))
  }

  function capabilityMatchesFilter(capability: PermissionPolicyCapabilityViewModel, search: string, category: string, mode: string) {
    const needle = search.trim().toLowerCase()
    const matchesSearch =
      !needle ||
      [
        capability.displayName,
        capability.labelZh,
        capability.labelEn,
        capability.categoryLabel,
        capability.method,
        capability.path,
        capability.scope
      ]
        .join(' ')
        .toLowerCase()
        .includes(needle)
    const matchesCategory = category === 'all' || capability.categoryEn === category
    const matchesMode = mode === 'all' || capability.accessMode === mode
    return matchesSearch && matchesCategory && matchesMode
  }

  function generateRoleId(label: string, existingRoleIds: string[]) {
    const slug = slugify(label)
    const base = `custom.${slug || `role-${shortHash(label)}`}`
    const existing = new Set(existingRoleIds)
    let candidate = base
    let suffix = 2
    while (existing.has(candidate)) {
      candidate = `${base}-${suffix}`
      suffix += 1
    }
    return candidate
  }

  function generateRoleLabelEn(label: string, roleId: string) {
    const slug = slugify(label)
    if (!slug) return `Custom Role ${shortHash(roleId)}`
    return slug
      .split('-')
      .filter(Boolean)
      .map((word) => word.slice(0, 1).toUpperCase() + word.slice(1))
      .join(' ')
  }

  function slugify(value: string) {
    return value
      .normalize('NFKD')
      .replace(/[\u0300-\u036f]/g, '')
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 48)
  }

  function shortHash(value: string) {
    let hash = 2166136261
    for (let index = 0; index < value.length; index += 1) {
      hash ^= value.charCodeAt(index)
      hash = Math.imul(hash, 16777619)
    }
    return (hash >>> 0).toString(36)
  }
</script>

<section class="space-y-4">
  <div class="panel p-4">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold text-foreground">权限策略 / Permission policy</h2>
        <div class="mt-2 flex flex-wrap gap-2">
          <Badge tone="info">权限 {view.summary.capabilities}</Badge>
          <Badge tone="success">查看 {view.summary.readCapabilities}</Badge>
          <Badge tone="warning">编辑 {view.summary.writeCapabilities}</Badge>
          <Badge tone="neutral">角色 {view.summary.roles}</Badge>
        </div>
      </div>
      <Button onclick={onsave} disabled={busy || !dirty || !view.policyMutable}>
        <Save class="h-4 w-4" />
        保存策略 / Save policy
      </Button>
    </div>
  </div>

  {#if !view.policyMutable}
    <div class="rounded-md border border-[#d8e4ef] bg-[#f8fafc] px-4 py-3 text-sm text-muted-foreground">
      当前由统一授权服务执行权限判定，本页策略只读。角色与关系请通过统一授权数据源管理。
    </div>
  {/if}

  <div class="flex flex-wrap items-center justify-between gap-3">
    <Tabs bind:value={tab} {tabs} variant="underline" />
    {#if dirty}
      <span class="text-xs font-medium text-status-blocked">有未保存修改 / Unsaved changes</span>
    {/if}
  </div>

  {#if tab === 'roles'}
    <div class="space-y-4">
      <div class="panel p-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="flex items-center gap-2 text-sm font-semibold text-foreground">
            <UsersRound class="h-4 w-4" />
            角色列表 / Roles
          </div>
          <Button onclick={openRoleDialog} disabled={busy || !view.policyMutable}>
            <Plus class="h-4 w-4" />
            新建角色 / Create role
          </Button>
        </div>
        <div class="mt-4 grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {#each view.roles as role (role.id)}
            {@const permissionCount = rolePermissionCount(role.id)}
            <article class="rounded-md border border-border bg-background p-3">
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0">
                  <div class="truncate text-sm font-semibold text-foreground">{role.labelZh}</div>
                  {#if role.labelEn && role.labelEn !== role.labelZh}
                    <div class="mt-1 truncate text-xs text-muted-foreground">{role.labelEn}</div>
                  {/if}
                  <div class="mt-1 font-mono text-xs text-muted-foreground">{role.id}</div>
                </div>
                <div class="flex shrink-0 flex-col items-end gap-1.5">
                  {#if role.builtIn}
                    <Badge tone="info">系统模板</Badge>
                  {/if}
                  <Badge tone={permissionCount > 0 ? 'success' : 'neutral'}>{permissionCount} permissions</Badge>
                </div>
              </div>
              <div class="mt-3">
                <Button
                  variant="outline"
                  size="sm"
                  ariaLabel={`${role.builtIn ? '查看权限 / View permissions' : '编辑权限 / Edit permissions'} for ${role.labelEn || role.id}`}
                  onclick={() => (selectedRoleId = role.id)}
                >
                  {role.builtIn ? '查看权限 / View permissions' : '编辑权限 / Edit permissions'}
                </Button>
              </div>
            </article>
          {/each}
        </div>
      </div>

      {#if selectedRole}
        <div class="panel p-4">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 class="text-sm font-semibold text-foreground">{selectedRole.displayName}</h3>
              <p class="mt-1 text-xs text-muted-foreground">
                {selectedRole.builtIn ? '系统角色模板只读；如需调整，请新建自定义角色。' : '为当前角色配置功能权限包。'}
              </p>
            </div>
            <Badge tone="info">已授予 {rolePermissionCount(selectedRole.id)} / Granted</Badge>
          </div>

          <div class="mt-4 grid gap-2 md:grid-cols-[minmax(180px,1fr)_180px_140px]">
            <label class="grid gap-1 text-xs font-medium text-muted-foreground">
              搜索角色权限
              <Input value={roleSearch} oninput={(event) => (roleSearch = event.currentTarget.value)} />
            </label>
            <label class="grid gap-1 text-xs font-medium text-muted-foreground">
              功能分类
              <Select value={roleCategory} options={categoryOptions} onchange={(event) => (roleCategory = event.currentTarget.value)} />
            </label>
            <label class="grid gap-1 text-xs font-medium text-muted-foreground">
              权限类型
              <Select value={roleMode} options={modeOptions} onchange={(event) => (roleMode = event.currentTarget.value)} />
            </label>
          </div>

          <div class="mt-4 space-y-3">
            {#each editableRolePackageGroups as group (group.key)}
              {@const groupGrant = roleGroupGrant(selectedRole.id, group)}
              <section class="overflow-hidden rounded-md border border-border bg-background">
                <div class="flex flex-wrap items-start justify-between gap-2 border-b border-border bg-muted/40 px-3 py-2">
                  <div class="min-w-0">
                    <div class="text-sm font-semibold text-foreground">{group.page}</div>
                    {#if group.categoryLabel !== group.page}
                      <div class="mt-0.5 truncate text-xs text-muted-foreground">{group.categoryLabel}</div>
                    {/if}
                  </div>
                  <div class="flex flex-wrap items-center justify-end gap-x-3 gap-y-1.5">
                    <label class="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                      <input
                        class="h-3.5 w-3.5 accent-brand"
                        type="checkbox"
                        checked={groupGrant.checked}
                        indeterminate={groupGrant.partial}
                        disabled={!view.policyMutable || selectedRole.builtIn}
                        aria-label={`Select all permissions in ${group.page} for ${selectedRole.labelEn || selectedRole.id}`}
                        onchange={() => setRoleGroupGrant(selectedRole.id, group, 'all')}
                      />
                      全选
                    </label>
                    <label class="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                      <input
                        class="h-3.5 w-3.5 accent-brand"
                        type="checkbox"
                        checked={!groupGrant.any}
                        disabled={!view.policyMutable || selectedRole.builtIn}
                        aria-label={`Clear all permissions in ${group.page} for ${selectedRole.labelEn || selectedRole.id}`}
                        onchange={() => setRoleGroupGrant(selectedRole.id, group, 'clear')}
                      />
                      全部取消
                    </label>
                    <label class="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                      <input
                        class="h-3.5 w-3.5 accent-brand"
                        type="checkbox"
                        checked={false}
                        disabled={!view.policyMutable || selectedRole.builtIn}
                        aria-label={`Invert permissions in ${group.page} for ${selectedRole.labelEn || selectedRole.id}`}
                        onchange={() => setRoleGroupGrant(selectedRole.id, group, 'invert')}
                      />
                      反选
                    </label>
                    <Badge tone="neutral">{group.packages.length} permission packages</Badge>
                  </div>
                </div>
                <div class="divide-y divide-border">
                  {#each group.packages as permissionPackage (permissionPackage.key)}
                    {@const grant = rolePackageGrant(selectedRole.id, permissionPackage)}
                    <label class="flex items-start gap-3 px-3 py-3">
                      <input
                        class="mt-1 h-4 w-4 accent-brand"
                        type="checkbox"
                        checked={grant.checked}
                        disabled={!view.policyMutable || selectedRole.builtIn}
                        aria-label={`Grant ${permissionPackage.labelEn} to ${selectedRole.labelEn || selectedRole.id}`}
                        onchange={(event) => ontogglerolegrant?.(selectedRole.id, permissionPackage.scopes, permissionPackage.capabilities.map((capability) => capability.id), event.currentTarget.checked)}
                      />
                      <span class="min-w-0">
                        <span class="flex flex-wrap items-center gap-2 text-sm font-medium text-foreground">
                          {permissionPackage.labelZh}
                          {#if grant.partial}
                            <Badge tone="warning">部分</Badge>
                          {/if}
                        </span>
                        <span class="mt-0.5 block text-xs text-muted-foreground">{permissionPackage.labelEn}</span>
                        <span class="mt-1 block text-xs text-muted-foreground">
                          {permissionPackage.scopes.join(', ')} · {permissionPackage.capabilities.length} operations
                        </span>
                      </span>
                    </label>
                  {/each}
                </div>
              </section>
            {/each}
            {#if editableRolePackageGroups.length === 0}
              <div class="rounded-md border border-border bg-background px-3 py-6 text-sm text-muted-foreground">
                没有匹配的权限包
              </div>
            {/if}
          </div>
        </div>
      {/if}
    </div>

    <Dialog open={showRoleDialog} title="新建角色 / Create role" size="wide" onClose={closeRoleDialog}>
      <div class="space-y-4">
        <div class="flex items-center gap-2 text-sm font-semibold text-foreground">
          <ShieldCheck class="h-4 w-4" />
          角色信息
        </div>
        <label class="grid gap-1 text-xs font-medium text-muted-foreground">
          角色名称
          <Input value={roleZh} oninput={(event) => (roleZh = event.currentTarget.value)} />
        </label>

        <div class="grid gap-2 md:grid-cols-[minmax(180px,1fr)_180px_140px]">
          <label class="grid gap-1 text-xs font-medium text-muted-foreground">
            搜索操作
            <div class="relative">
              <Search class="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
              <Input value={newRoleSearch} class="pl-9" oninput={(event) => (newRoleSearch = event.currentTarget.value)} />
            </div>
          </label>
          <label class="grid gap-1 text-xs font-medium text-muted-foreground">
            功能分类
            <Select value={newRoleCategory} options={categoryOptions} onchange={(event) => (newRoleCategory = event.currentTarget.value)} />
          </label>
          <label class="grid gap-1 text-xs font-medium text-muted-foreground">
            权限类型
            <Select value={newRoleMode} options={modeOptions} onchange={(event) => (newRoleMode = event.currentTarget.value)} />
          </label>
        </div>

        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="text-sm font-semibold text-foreground">可选权限包</div>
          <Badge tone={selectedNewRolePackageKeys.length > 0 ? 'success' : 'neutral'}>已选 {selectedNewRolePackageKeys.length}</Badge>
        </div>

        <div class="max-h-[52vh] space-y-3 overflow-y-auto pr-1" data-testid="new-role-permission-groups">
          {#each newRolePackageGroups as group (group.key)}
            {@const groupGrant = newRoleGroupGrant(group)}
            <section class="overflow-hidden rounded-md border border-border bg-background">
              <div class="flex flex-wrap items-start justify-between gap-2 border-b border-border bg-muted/40 px-3 py-2">
                <div class="min-w-0">
                  <div class="text-sm font-semibold text-foreground">{group.page}</div>
                  {#if group.categoryLabel !== group.page}
                    <div class="mt-0.5 truncate text-xs text-muted-foreground">{group.categoryLabel}</div>
                  {/if}
                </div>
                <div class="flex flex-wrap items-center justify-end gap-x-3 gap-y-1.5">
                  <label class="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                    <input
                      class="h-3.5 w-3.5 accent-brand"
                      type="checkbox"
                      checked={groupGrant.checked}
                      indeterminate={groupGrant.partial}
                      aria-label={`Select all permissions in ${group.page} for new role`}
                      onchange={() => setNewRoleGroupGrant(group, 'all')}
                    />
                    全选
                  </label>
                  <label class="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                    <input
                      class="h-3.5 w-3.5 accent-brand"
                      type="checkbox"
                      checked={!groupGrant.any}
                      aria-label={`Clear all permissions in ${group.page} for new role`}
                      onchange={() => setNewRoleGroupGrant(group, 'clear')}
                    />
                    全部取消
                  </label>
                  <label class="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                    <input
                      class="h-3.5 w-3.5 accent-brand"
                      type="checkbox"
                      checked={false}
                      aria-label={`Invert permissions in ${group.page} for new role`}
                      onchange={() => setNewRoleGroupGrant(group, 'invert')}
                    />
                    反选
                  </label>
                  <Badge tone="neutral">{group.packages.length} permission packages</Badge>
                </div>
              </div>
              <div class="divide-y divide-border">
                {#each group.packages as permissionPackage (permissionPackage.key)}
                  {@const checked = selectedNewRolePackageKeys.includes(permissionPackage.key)}
                  <label class="flex items-start gap-3 px-3 py-3">
                    <input
                      class="mt-1 h-4 w-4 accent-brand"
                      type="checkbox"
                      checked={checked}
                      aria-label={`Select ${permissionPackage.labelEn} for new role`}
                      onchange={(event) => toggleNewRolePackage(permissionPackage.key, event.currentTarget.checked)}
                    />
                    <span class="min-w-0">
                      <span class="block text-sm font-medium text-foreground">{permissionPackage.labelZh}</span>
                      <span class="mt-0.5 block text-xs text-muted-foreground">{permissionPackage.labelEn}</span>
                      <span class="mt-1 block text-xs text-muted-foreground">
                        {permissionPackage.scopes.join(', ')} · {permissionPackage.capabilities.length} operations
                      </span>
                    </span>
                  </label>
                {/each}
              </div>
            </section>
          {/each}
          {#if newRolePackageGroups.length === 0}
            <div class="rounded-md border border-border bg-background px-3 py-6 text-sm text-muted-foreground">
              没有匹配的权限包
            </div>
          {/if}
        </div>

        <div class="flex flex-wrap justify-end gap-2">
          <Button variant="outline" onclick={closeRoleDialog}>取消 / Cancel</Button>
          <Button onclick={addRole} disabled={busy || !roleZh.trim()}>确认新建 / Create</Button>
        </div>
      </div>
    </Dialog>
  {:else if tab === 'catalog'}
    <div class="panel p-4">
      <div class="grid gap-2 md:grid-cols-[minmax(180px,1fr)_180px_140px]">
        <label class="grid gap-1 text-xs font-medium text-muted-foreground">
          搜索权限
          <div class="relative">
            <Search class="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
            <Input value={catalogSearch} class="pl-9" oninput={(event) => (catalogSearch = event.currentTarget.value)} />
          </div>
        </label>
        <label class="grid gap-1 text-xs font-medium text-muted-foreground">
          功能分类
          <Select value={catalogCategory} options={categoryOptions} onchange={(event) => (catalogCategory = event.currentTarget.value)} />
        </label>
        <label class="grid gap-1 text-xs font-medium text-muted-foreground">
          读写类型
          <Select value={catalogMode} options={modeOptions} onchange={(event) => (catalogMode = event.currentTarget.value)} />
        </label>
      </div>
    </div>
    <div class="panel overflow-x-auto p-0" data-testid="permission-dictionary">
      <table class="w-full min-w-[760px] border-collapse text-left text-sm">
        <thead class="bg-muted text-xs uppercase text-muted-foreground">
          <tr>
            <th class="px-4 py-3">权限</th>
            <th class="px-4 py-3">读写</th>
            <th class="px-4 py-3">分类</th>
            <th class="px-4 py-3">Router</th>
            <th class="px-4 py-3">Scope</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          {#each catalogCapabilities as capability (capability.id)}
            <tr>
              <td class="px-4 py-3">
                <div class="font-medium text-foreground">{capability.labelZh}</div>
                <div class="mt-0.5 text-xs text-muted-foreground">{capability.labelEn}</div>
              </td>
              <td class="px-4 py-3 text-muted-foreground">{capability.accessLabelZh} / {capability.accessLabelEn}</td>
              <td class="px-4 py-3 text-muted-foreground">{capability.categoryLabel}</td>
              <td class="px-4 py-3 font-mono text-xs text-muted-foreground">{capability.method} {capability.path}</td>
              <td class="px-4 py-3 font-mono text-xs text-muted-foreground">{capability.scope}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <div class="panel overflow-x-auto p-0">
      <table class="w-full min-w-[760px] border-collapse text-left text-sm">
        <thead class="bg-muted text-xs uppercase text-muted-foreground">
          <tr>
            <th class="px-4 py-3">用户 / Agent</th>
            <th class="px-4 py-3">权限</th>
            <th class="px-4 py-3">Scope</th>
            <th class="px-4 py-3">来源</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          {#each view.effective as grant (grant.principalKind + ':' + grant.principalId + ':' + grant.capabilityId + ':' + grant.sourceLabel)}
            <tr>
              <td class="px-4 py-3 font-medium text-foreground">{grant.principalDisplayName}</td>
              <td class="px-4 py-3 text-muted-foreground">{grant.capabilityDisplayName}</td>
              <td class="px-4 py-3 font-mono text-xs text-muted-foreground">{grant.scope}</td>
              <td class="px-4 py-3 text-muted-foreground">{grant.sourceLabel}</td>
            </tr>
          {/each}
        </tbody>
      </table>
      {#if view.effective.length === 0}
        <div class="border-t border-border px-4 py-6 text-sm text-muted-foreground">
          暂无有效授权
        </div>
      {/if}
    </div>
  {/if}
</section>
