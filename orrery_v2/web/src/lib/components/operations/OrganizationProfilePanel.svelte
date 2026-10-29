<script lang="ts">
  import { Building2, CheckCircle2, Layers3, Network, Pencil, Plus, ShieldCheck, Stamp, Trash2, UserRound, Users } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import OrganizationGraphWorkspace from '#lib/components/operations/OrganizationGraphWorkspace.svelte'
  import ResponsibilityProfilePanel from '#lib/components/operations/ResponsibilityProfilePanel.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { useOrganizationResponsibilityProfileQuery } from '#lib/queries/workforce'
  import type { PeopleResolvedResponsibilityDTO } from '#lib/types/api'
  import type { OrganizationTreeViewModel, RoleAssignmentViewModel, WorkforceMemberViewModel } from '#lib/types/view-models'

  type ProfileTab = 'profile' | 'structure' | 'members' | 'relationships' | 'responsibilities' | 'governance'

  interface Props {
    organization?: OrganizationTreeViewModel
    organizations?: OrganizationTreeViewModel[]
    members?: WorkforceMemberViewModel[]
    reportingEdges?: Array<{ from: string; to: string; lineId: string }>
    onselectOrganization?: (id: string) => void
    onCreateUnit?: () => void
    onConfigureOwner?: () => void
    onDeleteSelection?: () => void
    deletingSelection?: boolean
    deleteSelectionLabel?: string
    onDeleteRoleAssignment?: (assignment: RoleAssignmentViewModel) => void
    deletingRoleAssignmentId?: string
  }

  let {
    organization,
    organizations = [],
    members = [],
    reportingEdges = [],
    onselectOrganization,
    onCreateUnit,
    onConfigureOwner,
    onDeleteSelection,
    deletingSelection = false,
    deleteSelectionLabel = '删除',
    onDeleteRoleAssignment,
    deletingRoleAssignmentId = ''
  }: Props = $props()

  let activeTab = $state<ProfileTab>('profile')
  const currentResponsibilityProfileQuery = useOrganizationResponsibilityProfileQuery(
    () => organization?.id ?? '',
    () => Boolean(organization),
    () => 0
  )
  const responsibilityHistoryProfileQuery = useOrganizationResponsibilityProfileQuery(
    () => organization?.id ?? '',
    () => Boolean(organization) && activeTab === 'responsibilities',
    () => 20
  )
  let t = $derived(createTranslator($locale))
  let permissionSummary = $derived(organization?.permissionSummary)
  let roleAssignments = $derived(organization?.roleAssignments ?? [])
  let approvalPolicyRefs = $derived(organization?.approvalPolicyRefs ?? [])
  let memberScope = $state<'direct' | 'all'>('direct')
  let directOrganizationMembers = $derived.by(() => {
    if (!organization) return []
    return uniqueMembers(membersAtNode(organization))
  })
  let organizationMembers = $derived.by(() => {
    if (!organization) return []
    return uniqueMembers(collectMembers(organization))
  })
  let visibleOrganizationMembers = $derived(memberScope === 'direct' ? directOrganizationMembers : organizationMembers)
  let responsibilityProfile = $derived(responsibilityHistoryProfileQuery.data ?? currentResponsibilityProfileQuery.data)
  let responsibilityProfileLoading = $derived(!responsibilityProfile && (currentResponsibilityProfileQuery.isLoading || responsibilityHistoryProfileQuery.isLoading))
  let responsibilityProfileFailed = $derived(!responsibilityProfile && (currentResponsibilityProfileQuery.isError || responsibilityHistoryProfileQuery.isError))
  let responsibilities = $derived.by(() => {
    const unique = new Map<string, PeopleResolvedResponsibilityDTO>()
    for (const portfolio of responsibilityProfile?.portfolios ?? []) {
      for (const responsibility of portfolio.responsibilities) unique.set(responsibility.id, responsibility)
    }
    return Array.from(unique.values())
  })
  let children = $derived(organization?.children ?? [])
  let organizationType = $derived(organization?.nodeType === 'company' ? '组织' : '部门')
  let organizationCode = $derived(organization ? organization.displayCode || organization.code || '未编码' : '未编码')
  let ownerLabel = $derived([organization?.ownerEmployeeNo, organization?.ownerName || organization?.ownerProfileId].filter(Boolean).join(' / ') || '未设置')
  let parentLabel = $derived(organization?.parentName || organization?.parentDisplayCode || organization?.parentCode || '顶层组织')
  let profileSummary = $derived(
    responsibilityProfileLoading
      ? '正在读取责任边界...'
      : responsibilityProfileFailed
        ? '责任边界读取失败。'
        : responsibilities[0]?.description || '当前组织尚未配置责任边界。'
  )
  const profileTabs: Array<{ key: ProfileTab; label: string }> = [
    { key: 'profile', label: '档案概览' },
    { key: 'structure', label: '组织结构' },
    { key: 'members', label: '组织成员' },
    { key: 'relationships', label: '关系图谱' },
    { key: 'responsibilities', label: '组织责任' },
    { key: 'governance', label: '权限与治理' }
  ]

  function displayField(value: string | number | undefined) {
    return value === undefined || value === '' ? '未设置' : value
  }

  function membersAtNode(node: OrganizationTreeViewModel): WorkforceMemberViewModel[] {
    if ((node.directMembers ?? []).length > 0) return node.directMembers ?? []
    return (node.positions ?? []).flatMap((position) => position.members)
  }

  function collectMembers(node: OrganizationTreeViewModel): WorkforceMemberViewModel[] {
    return [...membersAtNode(node), ...(node.children ?? []).flatMap(collectMembers)]
  }

  function uniqueMembers(members: WorkforceMemberViewModel[]): WorkforceMemberViewModel[] {
    const seen = new Set<string>()
    return members.filter((member) => {
      const key = member.profileId || member.id || member.agentInstanceId
      if (!key || seen.has(key)) return false
      seen.add(key)
      return true
    })
  }

  function setActiveTab(tab: ProfileTab) {
    activeTab = tab
  }
</script>

{#if organization}
  <section data-organization-profile-panel class="overflow-hidden rounded-2xl border border-border bg-card">
    <header data-organization-profile-hero class="border-b border-border bg-gradient-to-b from-muted/40 to-background">
      <div class="relative px-6 pb-4 pt-5">
        <div class="flex flex-col items-stretch gap-4 sm:flex-row sm:items-start sm:justify-between">
          <div class="flex min-w-0 flex-1 gap-4">
            <span class="grid h-20 w-20 shrink-0 place-items-center rounded-xl bg-brand/10 text-brand shadow-sm">
              <Building2 class="h-8 w-8" />
            </span>

            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-2">
                <h2 class="truncate text-[17px] font-semibold text-foreground">{organization.name}</h2>
                <Badge tone={organization.nodeType === 'company' ? 'blue' : 'teal'}>{organizationType}</Badge>
                <span class="inline-flex items-center gap-1 text-xs {organization.status === 'active' || !organization.status ? 'text-status-done' : 'text-muted-foreground'}">
                  <span class="h-1.5 w-1.5 rounded-full {organization.status === 'active' || !organization.status ? 'bg-status-done' : 'bg-muted-foreground/45'}"></span>
                  {organization.status || 'active'}
                </span>
              </div>
              <p class="mt-0.5 text-[12.5px] text-muted-foreground">{organizationType} / {organizationCode}</p>
              <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
                <span class="inline-flex items-center gap-1.5"><Building2 class="h-3.5 w-3.5 text-muted-foreground" />上级：{parentLabel}</span>
                <span class="inline-flex items-center gap-1.5">
                  <UserRound class="h-3.5 w-3.5 text-muted-foreground" />负责人：{ownerLabel}
                  {#if onConfigureOwner}
                    <button type="button" class="grid h-6 w-6 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground" aria-label="配置组织负责人" title="配置组织负责人" onclick={onConfigureOwner}>
                      <Pencil class="h-3.5 w-3.5" />
                    </button>
                  {/if}
                </span>
              </div>
              <div class="mt-2.5 flex flex-wrap gap-1.5">
                <span class="inline-flex items-center gap-1 rounded-md bg-muted px-2 py-0.5 text-[11px] leading-[16.5px] text-muted-foreground"><Layers3 class="h-3 w-3" />层级 {displayField(organization.level)}</span>
              </div>
            </div>
          </div>

          {#if onCreateUnit || onDeleteSelection}
            <div class="flex shrink-0 flex-wrap items-center justify-end gap-1.5 self-end sm:self-auto">
              {#if onCreateUnit}
                <Button size="sm" variant="outline" class="!h-auto !rounded-lg !border-border !bg-card !px-2.5 !py-1.5 !text-[12.5px] !leading-[18.75px] hover:!bg-muted" onclick={onCreateUnit}>
                  <Plus class="h-3.5 w-3.5" />
                  {t('organization.addUnit')}
                </Button>
              {/if}
              {#if onDeleteSelection}
                <Button size="sm" variant="destructive" class="!h-auto !rounded-lg !px-2.5 !py-1.5 !text-[12.5px] !leading-[18.75px]" disabled={deletingSelection} onclick={onDeleteSelection}>
                  <Trash2 class="h-3.5 w-3.5" />
                  {deletingSelection ? t('organization.deleting') : deleteSelectionLabel}
                </Button>
              {/if}
            </div>
          {/if}
        </div>

        <div class="mt-3.5">
          <p class="line-clamp-2 text-[13px] leading-relaxed text-foreground/80">{profileSummary}</p>
          <p class="mt-1.5 truncate font-mono text-[11.5px] text-muted-foreground/65">{organization.id}</p>
        </div>

        <div data-organization-profile-resources class="mt-5 grid grid-cols-2 divide-x divide-border rounded-xl border border-border bg-card sm:grid-cols-3">
          <button type="button" class="flex items-center gap-2.5 px-4 py-3 text-left transition-colors hover:bg-muted" onclick={() => setActiveTab('structure')}>
            <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-brand/10 text-brand"><Network class="h-4 w-4" /></span>
            <div><p class="text-base font-semibold leading-none tabular-nums text-foreground">{children.length}</p><p class="mt-1 text-[11px] leading-[16.5px] text-muted-foreground">下级单位</p></div>
          </button>
          <button type="button" class="flex items-center gap-2.5 px-4 py-3 text-left transition-colors hover:bg-muted" onclick={() => setActiveTab('responsibilities')}>
            <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-brand/10 text-brand"><CheckCircle2 class="h-4 w-4" /></span>
            <div><p class="text-base font-semibold leading-none tabular-nums text-foreground">{responsibilities.length}</p><p class="mt-1 text-[11px] leading-[16.5px] text-muted-foreground">组织责任</p></div>
          </button>
          <button type="button" class="flex items-center gap-2.5 px-4 py-3 text-left transition-colors hover:bg-muted" onclick={() => setActiveTab('governance')}>
            <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-brand/10 text-brand"><ShieldCheck class="h-4 w-4" /></span>
            <div><p class="text-base font-semibold leading-none tabular-nums text-foreground">{permissionSummary?.effectiveRoleCount ?? 0}</p><p class="mt-1 text-[11px] leading-[16.5px] text-muted-foreground">节点角色</p></div>
          </button>
        </div>
      </div>
    </header>

    <div data-organization-profile-tabs role="tablist" aria-label="组织档案" class="flex items-center gap-5 overflow-x-auto border-b border-border px-6 subtle-scrollbar">
      {#each profileTabs as tab (tab.key)}
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === tab.key}
          class="-mb-px shrink-0 whitespace-nowrap border-b-2 py-2.5 text-[13px] leading-[19.5px] transition-colors {activeTab === tab.key ? 'border-brand font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
          onclick={() => setActiveTab(tab.key)}
        >
          {tab.label}
        </button>
      {/each}
    </div>

    <div class="px-6 py-5">
      {#if activeTab === 'profile'}
        <div data-organization-profile-overview>
          <div class="grid gap-5 lg:grid-cols-[minmax(0,1fr)_240px]">
            <section>
              <p class="text-xs text-muted-foreground">组织责任摘要</p>
              <p class="mt-1.5 whitespace-pre-wrap text-[13px] leading-relaxed text-foreground/90">{profileSummary}</p>
            </section>
            <section class="rounded-xl bg-muted/55 p-3.5">
              <div class="mb-2 flex items-center gap-1.5 text-[12px] leading-[18px]">
                <Building2 class="h-3.5 w-3.5 text-muted-foreground" />
                <h3 class="text-[12px] font-medium leading-[18px] text-foreground">当前归属</h3>
              </div>
              <dl class="grid grid-cols-2 gap-y-2 text-[12px] leading-[18px]">
                <div class="min-w-0"><dt class="text-[11px] leading-[16.5px] text-muted-foreground">组织类型</dt><dd class="mt-0.5 truncate text-[12.5px] leading-[18.75px] text-foreground">{organizationType}</dd></div>
                <div class="min-w-0"><dt class="text-[11px] leading-[16.5px] text-muted-foreground">组织层级</dt><dd class="mt-0.5 truncate text-[12.5px] leading-[18.75px] text-foreground">{displayField(organization.level)}</dd></div>
                <div class="min-w-0"><dt class="text-[11px] leading-[16.5px] text-muted-foreground">上级组织</dt><dd class="mt-0.5 truncate text-[12.5px] leading-[18.75px] text-foreground">{parentLabel}</dd></div>
                <div class="min-w-0"><dt class="text-[11px] leading-[16.5px] text-muted-foreground">负责人</dt><dd class="mt-0.5 truncate text-[12.5px] leading-[18.75px] text-foreground">{ownerLabel}</dd></div>
              </dl>
            </section>
          </div>

          <dl class="mt-5 grid gap-4 rounded-xl bg-muted/55 px-4 py-3 sm:grid-cols-2 lg:grid-cols-4">
            <div><dt class="text-[11px] text-muted-foreground">组织编码</dt><dd class="mt-0.5 text-[12.5px] text-foreground">{organizationCode}</dd></div>
            <div><dt class="text-[11px] text-muted-foreground">组织状态</dt><dd class="mt-0.5 text-[12.5px] text-foreground">{organization.status || 'active'}</dd></div>
            <div><dt class="text-[11px] text-muted-foreground">人员规模（直属 / 总计）</dt><dd class="mt-0.5 text-[12.5px] text-foreground">{directOrganizationMembers.length} / {organization.totalMemberCount ?? organization.memberCount ?? organizationMembers.length}</dd></div>
            <div><dt class="text-[11px] text-muted-foreground">知识库</dt><dd class="mt-0.5 truncate font-mono text-[12.5px] text-foreground">{displayField(organization.knowledgeBaseId)}</dd></div>
          </dl>
        </div>
      {:else if activeTab === 'structure'}
        <section data-organization-structure>
          <div class="flex items-center gap-2"><Network class="h-4 w-4 text-brand" /><h3 class="text-sm font-semibold text-foreground">组织结构</h3><span class="text-xs text-muted-foreground">{children.length} 个下级单位</span></div>
          {#if children.length > 0}
            <div class="mt-3 divide-y divide-border border-y border-border">
              {#each children as child (child.id)}
                <button type="button" class="flex w-full min-w-0 items-center justify-between gap-3 py-3 text-left hover:bg-muted" onclick={() => onselectOrganization?.(child.id)}>
                  <span class="min-w-0"><span class="block truncate text-[13px] font-medium text-foreground">{child.name}</span><span class="mt-0.5 block truncate text-[11px] text-muted-foreground">{child.displayCode || child.code || '未编码'} · {child.nodeType === 'company' ? '组织' : '部门'}</span></span>
                  <span class="shrink-0 text-xs tabular-nums text-muted-foreground">{child.totalMemberCount ?? child.memberCount ?? 0} 人</span>
                </button>
              {/each}
            </div>
          {:else}
            <p class="mt-3 text-sm text-muted-foreground">暂无下级组织。</p>
          {/if}

        </section>
      {:else if activeTab === 'members'}
        <section data-organization-members>
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div class="flex items-center gap-2"><Users class="h-4 w-4 text-brand" /><h3 class="text-sm font-semibold text-foreground">组织成员</h3><span class="text-xs text-muted-foreground">直属 {directOrganizationMembers.length} / 含下属 {organizationMembers.length}</span></div>
            <div class="inline-flex rounded-md border border-border bg-muted p-0.5" role="tablist" aria-label="成员范围">
              <button type="button" role="tab" aria-selected={memberScope === 'direct'} class="rounded px-2.5 py-1 text-[11px] leading-[16px] {memberScope === 'direct' ? 'bg-card font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}" onclick={() => (memberScope = 'direct')}>直属成员</button>
              <button type="button" role="tab" aria-selected={memberScope === 'all'} class="rounded px-2.5 py-1 text-[11px] leading-[16px] {memberScope === 'all' ? 'bg-card font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}" onclick={() => (memberScope = 'all')}>含下属单位</button>
            </div>
          </div>
          <p class="mt-1.5 text-xs text-muted-foreground">{memberScope === 'direct' ? '当前组织直接归属的成员。' : '当前组织及所有下属单位的成员。'}</p>
          {#if visibleOrganizationMembers.length > 0}
            <div class="mt-4 divide-y divide-border border-y border-border">
              {#each visibleOrganizationMembers as member (member.profileId || member.id)}
                <div class="flex min-w-0 items-center justify-between gap-3 py-3">
                  <div class="flex min-w-0 items-center gap-2.5">
                    <WorkforceAvatar
                      profileId={member.profileId || member.id}
                      name={member.displayName || member.realName || member.profileId || member.id}
                      human={(member.agentKind || '').toLowerCase() === 'human'}
                      class="h-8 w-8 rounded-lg"
                      textClass="text-xs"
                    />
                    <div class="min-w-0">
                      <p class="truncate text-[13px] font-medium text-foreground">{member.displayName || member.realName || member.profileId}</p>
                      <p class="mt-0.5 truncate text-[11px] text-muted-foreground">{member.departmentDisplayName || '未设置部门'} / {member.roleName || member.primaryRoleLabel || '未设置角色'}</p>
                    </div>
                  </div>
                  <Badge tone={member.enabled ? 'success' : 'neutral'}>{member.enabled ? '启用' : '停用'}</Badge>
                </div>
              {/each}
            </div>
          {:else}
            <p class="mt-4 text-sm text-muted-foreground">{memberScope === 'direct' ? '当前组织暂无直属成员。' : '当前组织及下属单位暂无成员。'}</p>
          {/if}
        </section>
      {:else if activeTab === 'relationships'}
        <OrganizationGraphWorkspace organization={organization} {organizations} {members} {reportingEdges} selectedOrganizationId={organization.id} {onselectOrganization} />
      {:else if activeTab === 'responsibilities'}
        <ResponsibilityProfilePanel
          profile={responsibilityProfile}
          loading={responsibilityProfileLoading}
          failed={responsibilityProfileFailed}
        />
      {:else}
        <div data-organization-governance class="grid gap-6 lg:grid-cols-3">
          <section class="min-w-0 lg:col-span-2">
            <div class="flex items-center gap-2"><ShieldCheck class="h-4 w-4 text-brand" /><h3 class="text-sm font-semibold text-foreground">角色</h3></div>
            <dl class="mt-3 grid grid-cols-3 divide-x divide-border border-y border-border text-center">
              <div class="py-3"><dt class="text-[11px] text-muted-foreground">直接角色</dt><dd class="mt-1 text-lg font-semibold tabular-nums text-foreground">{permissionSummary?.directRoleCount ?? 0}</dd></div>
              <div class="py-3"><dt class="text-[11px] text-muted-foreground">继承角色</dt><dd class="mt-1 text-lg font-semibold tabular-nums text-foreground">{permissionSummary?.inheritedRoleCount ?? 0}</dd></div>
              <div class="py-3"><dt class="text-[11px] text-muted-foreground">节点角色</dt><dd class="mt-1 text-lg font-semibold tabular-nums text-foreground">{permissionSummary?.effectiveRoleCount ?? 0}</dd></div>
            </dl>
            {#if roleAssignments.length > 0}
              <div class="mt-4 divide-y divide-border border-y border-border">
                {#each roleAssignments as assignment (assignment.assignmentId || `${assignment.actor.actorId}-${assignment.roleId}`)}
                  <div class="flex min-w-0 items-center justify-between gap-3 py-2.5"><div class="min-w-0"><Badge tone="blue">{assignment.roleId || '-'}</Badge><p class="mt-1 truncate text-[11px] text-muted-foreground">{assignment.actor.actorId}</p></div>{#if onDeleteRoleAssignment && assignment.source !== 'system' && assignment.assignmentId}<Button size="icon" variant="ghost" class="h-7 w-7 shrink-0 text-muted-foreground hover:text-status-blocked" ariaLabel={t('organization.deleteRoleAssignment', { role: assignment.roleId })} disabled={deletingRoleAssignmentId === assignment.assignmentId} onclick={() => onDeleteRoleAssignment?.(assignment)}><Trash2 class="h-3.5 w-3.5" /></Button>{/if}</div>
                {/each}
              </div>
            {:else}
              <p class="mt-3 text-sm text-muted-foreground">当前组织未配置角色。</p>
            {/if}
          </section>
          <aside class="min-w-0 border-t border-border pt-5 lg:border-l lg:border-t-0 lg:pl-6 lg:pt-0">
            <div class="flex items-center gap-2"><Stamp class="h-4 w-4 text-brand" /><h3 class="text-sm font-semibold text-foreground">权限与审批</h3></div>
            <p class="mt-3 text-xs text-muted-foreground">拒绝策略 {permissionSummary?.denyPolicyCount ?? 0} · 需要审批 {permissionSummary?.approvalRequiredCount ?? 0}</p>
            <div class="mt-3 flex flex-wrap gap-1.5">{#each permissionSummary?.keyPermissions ?? [] as permission}<Badge tone="teal">{permission}</Badge>{:else}<span class="text-xs text-muted-foreground">未配置关键权限</span>{/each}</div>
            <div class="mt-7 flex flex-wrap gap-1.5">{#each approvalPolicyRefs as policy}<Badge tone="amber">{policy}</Badge>{:else}<span class="text-xs text-muted-foreground">未绑定审批策略</span>{/each}</div>
          </aside>
        </div>
      {/if}
    </div>
  </section>
{:else}
  <section class="grid min-h-[420px] place-items-center border border-dashed border-border bg-muted/10 p-8 text-center">
    <div>
      <Building2 class="mx-auto h-7 w-7 text-muted-foreground/55" />
      <h2 class="mt-3 text-sm font-semibold text-foreground">组织档案</h2>
      <p class="mt-1.5 text-sm leading-6 text-muted-foreground">请选择左侧组织查看档案。</p>
    </div>
  </section>
{/if}
