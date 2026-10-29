<script lang="ts">
  import { BriefcaseBusiness, CheckCircle2, GitBranch, ListChecks, ShieldCheck, Stamp, Trash2, UserRound, Users } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import OrganizationGraphWorkspace from '#lib/components/operations/OrganizationGraphWorkspace.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { OrganizationPositionViewModel, OrganizationTreeViewModel, RoleAssignmentViewModel, WorkforceMemberViewModel } from '#lib/types/view-models'

  interface Props {
    organization?: OrganizationTreeViewModel
    organizations?: OrganizationTreeViewModel[]
    members?: WorkforceMemberViewModel[]
    reportingEdges?: Array<{ from: string; to: string; lineId: string }>
    onselectOrganization?: (id: string) => void
    onDeletePosition?: (position: OrganizationPositionViewModel) => void
    deletingPositionId?: string
    onDeleteRoleAssignment?: (assignment: RoleAssignmentViewModel) => void
    deletingRoleAssignmentId?: string
  }

  let {
    organization,
    organizations = [],
    members = [],
    reportingEdges = [],
    onselectOrganization,
    onDeletePosition,
    deletingPositionId = '',
    onDeleteRoleAssignment,
    deletingRoleAssignmentId = ''
  }: Props = $props()

  let t = $derived(createTranslator($locale))
  const permissionSummary = $derived(organization?.permissionSummary)
  const roleAssignments = $derived(organization?.roleAssignments ?? [])
  const approvalPolicyRefs = $derived(organization?.approvalPolicyRefs ?? [])
  const visiblePositions = $derived(organization ? organizationPositions(organization) : [])
  const visibleMembers = $derived(organization ? directMembers(organization) : [])
  const responsibilities = $derived(organization ? organizationResponsibilities(organization) : [])
  const ownerLabel = $derived(
    [organization?.ownerEmployeeNo, organization?.ownerName].filter(Boolean).join(' / ') || '-'
  )

  function value(value: string | number | undefined) {
    if (value === undefined || value === '') return '-'
    return value
  }

  function organizationPositions(node: OrganizationTreeViewModel): OrganizationPositionViewModel[] {
    if (node.nodeType === 'department') return node.positions ?? []
    return collectPositions(node)
  }

  function collectPositions(node: OrganizationTreeViewModel): OrganizationPositionViewModel[] {
    return [
      ...(node.positions ?? []),
      ...(node.children ?? []).flatMap((child) => collectPositions(child))
    ]
  }

  function directMembers(node: OrganizationTreeViewModel): WorkforceMemberViewModel[] {
    if (node.directMembers) return node.directMembers
    return (node.positions ?? []).flatMap((position) => position.members)
  }

  function organizationResponsibilities(node: OrganizationTreeViewModel): string[] {
    return Array.from(new Set(collectOrganizationMembers(node)
      .map((member) => member.responsibilityBoundary.trim())
      .filter(Boolean)))
  }

  function collectOrganizationMembers(node: OrganizationTreeViewModel): WorkforceMemberViewModel[] {
    return [
      ...(node.directMembers ?? []),
      ...(node.children ?? []).flatMap((child) => collectOrganizationMembers(child))
    ]
  }
</script>

{#if organization}
  <article class="space-y-4">
      <section class="rounded-xl border border-border bg-card p-4">
        <div class="mb-3 flex items-center gap-2 text-sm font-semibold text-foreground">
          <GitBranch class="h-4 w-4 text-brand" />
          组织属性
        </div>
        <dl class="grid gap-x-6 gap-y-4 sm:grid-cols-2 xl:grid-cols-3">
          <div class="min-w-0">
            <dt class="text-xs text-muted-foreground">组织编码</dt>
            <dd class="mt-1 font-mono text-sm font-semibold text-foreground">{value(organization.displayCode || organization.code)}</dd>
          </div>
          <div class="min-w-0">
            <dt class="text-xs text-muted-foreground">组织名称</dt>
            <dd class="mt-1 truncate text-sm font-semibold text-foreground">{organization.name}</dd>
          </div>
          <div class="min-w-0">
            <dt class="text-xs text-muted-foreground">组织层级</dt>
            <dd class="mt-1 text-sm font-semibold text-foreground">{value(organization.level)}</dd>
          </div>
          <div class="min-w-0">
            <dt class="text-xs text-muted-foreground">组织状态</dt>
            <dd class="mt-1 text-sm font-semibold text-foreground">{organization.status || 'active'}</dd>
          </div>
          <div class="min-w-0">
            <dt class="text-xs text-muted-foreground">组织负责人工号</dt>
            <dd class="mt-1 text-sm font-semibold text-foreground">{value(organization.ownerEmployeeNo)}</dd>
          </div>
          <div class="min-w-0">
            <dt class="text-xs text-muted-foreground">组织负责人姓名</dt>
            <dd class="mt-1 text-sm font-semibold text-foreground">{value(organization.ownerName)}</dd>
          </div>
          <div class="min-w-0">
            <dt class="text-xs text-muted-foreground">上级组织</dt>
            <dd class="mt-1 font-mono text-sm font-semibold text-foreground">{value(organization.parentDisplayCode || organization.parentCode)}</dd>
          </div>
          <div class="min-w-0">
            <dt class="text-xs text-muted-foreground">上级组织名称</dt>
            <dd class="mt-1 truncate text-sm font-semibold text-foreground">{value(organization.parentName)}</dd>
          </div>
          <div class="min-w-0">
            <dt class="text-xs text-muted-foreground">负责人</dt>
            <dd class="mt-1 truncate text-sm font-semibold text-foreground">{ownerLabel}</dd>
          </div>
        </dl>
      </section>

      <OrganizationGraphWorkspace
        organization={organization}
        organizations={organizations}
        {members}
        {reportingEdges}
        selectedOrganizationId={organization.id}
        {onselectOrganization}
      />

      <section class="rounded-xl border border-border bg-card p-4">
        <div class="mb-3 flex items-center gap-2 text-sm font-semibold text-foreground">
          <ListChecks class="h-4 w-4 text-brand" />
          组织职责
        </div>
        {#if responsibilities.length > 0}
          <ul class="grid gap-2 md:grid-cols-2">
            {#each responsibilities as responsibility}
              <li class="border-l-2 border-brand/50 pl-2.5 text-xs leading-5 text-foreground/80">{responsibility}</li>
            {/each}
          </ul>
        {:else}
          <p class="text-sm text-muted-foreground">当前组织尚未从人员管理档案汇总到职责边界。</p>
        {/if}
      </section>

      <section class="rounded-xl border border-border bg-card p-4">
        <div class="mb-3 flex items-center gap-2 text-sm font-semibold text-foreground">
          <BriefcaseBusiness class="h-4 w-4 text-brand" />
          {t('organization.positions')}
        </div>
        {#if visiblePositions.length > 0}
          <div class="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
            {#each visiblePositions as position (position.id)}
              <div class="min-w-0 rounded-md bg-muted/45 p-3">
                <div class="flex min-w-0 items-center justify-between gap-2">
                  <div class="truncate text-sm font-semibold text-foreground">{position.name}</div>
                  {#if onDeletePosition}
                    <Button
                      size="icon"
                      variant="ghost"
                      class="h-7 w-7 shrink-0 text-muted-foreground hover:text-status-blocked"
                      ariaLabel={t('organization.deletePosition', { name: position.name })}
                      disabled={deletingPositionId === position.id}
                      onclick={() => onDeletePosition?.(position)}
                    >
                      <Trash2 class="h-3.5 w-3.5" />
                    </Button>
                  {/if}
                </div>
                <div class="mt-1 flex items-center justify-between gap-3 text-xs text-muted-foreground">
                  <span class="truncate font-mono">{position.departmentId}</span>
                  <span>{position.memberCount}人</span>
                </div>
              </div>
            {/each}
          </div>
        {:else}
          <div class="rounded-md border border-dashed border-border bg-background p-4 text-sm text-muted-foreground">
            {t('organization.emptyPositions')}
          </div>
        {/if}
      </section>

      <section class="rounded-xl border border-border bg-card p-4">
        <div class="mb-3 flex items-center gap-2 text-sm font-semibold text-foreground">
          <Users class="h-4 w-4 text-brand" />
          {t('people.member')}
        </div>
        {#if visibleMembers.length > 0}
          <div class="grid gap-2 md:grid-cols-2">
            {#each visibleMembers as member (member.profileId)}
              <div class="flex min-w-0 items-center gap-2 rounded-md bg-muted/45 px-3 py-2 text-xs">
                <WorkforceAvatar profileId={member.profileId || member.id} name={member.displayName} human={(member.agentKind || '').toLowerCase() === 'human'} class="h-8 w-8 rounded-md" textClass="text-[10px]" />
                <div class="min-w-0">
                  <div class="truncate font-medium text-foreground">{member.displayName}</div>
                  <div class="mt-1 truncate text-muted-foreground">
                  {member.departmentDisplayName || '-'} / {member.positionDisplayName || '-'}
                  </div>
                </div>
              </div>
            {/each}
          </div>
        {:else}
          <div class="rounded-md border border-dashed border-border bg-background p-4 text-sm text-muted-foreground">
            {t('organization.emptyDirectMembers')}
          </div>
        {/if}
      </section>

      <section class="grid gap-4 lg:grid-cols-3">
        <div class="rounded-xl border border-border bg-card p-4">
          <div class="flex items-center gap-2 text-sm font-semibold text-foreground">
            <UserRound class="h-4 w-4 text-brand" />
            角色
          </div>
          <div class="mt-3 grid gap-2 text-sm">
            <div class="flex justify-between gap-3">
              <span class="text-muted-foreground">直接角色</span>
              <span class="font-semibold text-foreground">{permissionSummary?.directRoleCount ?? 0}</span>
            </div>
            <div class="flex justify-between gap-3">
              <span class="text-muted-foreground">继承角色</span>
              <span class="font-semibold text-foreground">{permissionSummary?.inheritedRoleCount ?? 0}</span>
            </div>
            <div class="flex justify-between gap-3">
              <span class="text-muted-foreground">有效角色</span>
              <span class="font-semibold text-foreground">{permissionSummary?.effectiveRoleCount ?? 0}</span>
            </div>
          </div>
          {#if roleAssignments.length > 0}
            <div class="mt-3 grid gap-1.5">
              {#each roleAssignments as assignment (assignment.assignmentId || `${assignment.actor.actorId}-${assignment.roleId}`)}
                <div class="flex min-w-0 items-center justify-between gap-2 rounded-md border border-border px-2 py-1.5">
                  <div class="min-w-0">
                    <Badge tone="blue">{assignment.roleId || '-'}</Badge>
                    <div class="mt-1 truncate text-xs text-muted-foreground">{assignment.actor.actorId}</div>
                  </div>
                  {#if onDeleteRoleAssignment && assignment.source !== 'system' && assignment.assignmentId}
                    <Button
                      size="icon"
                      variant="ghost"
                      class="h-7 w-7 shrink-0 text-muted-foreground hover:text-status-blocked"
                      ariaLabel={t('organization.deleteRoleAssignment', { role: assignment.roleId })}
                      disabled={deletingRoleAssignmentId === assignment.assignmentId}
                      onclick={() => onDeleteRoleAssignment?.(assignment)}
                    >
                      <Trash2 class="h-3.5 w-3.5" />
                    </Button>
                  {/if}
                </div>
              {/each}
            </div>
          {/if}
        </div>

        <div class="rounded-xl border border-border bg-card p-4">
          <div class="flex items-center gap-2 text-sm font-semibold text-foreground">
            <ShieldCheck class="h-4 w-4 text-brand" />
            权限
          </div>
          <div class="mt-3 grid gap-2 text-sm">
            <div class="flex justify-between gap-3">
              <span class="text-muted-foreground">拒绝策略</span>
              <span class="font-semibold text-foreground">{permissionSummary?.denyPolicyCount ?? 0}</span>
            </div>
            <div class="flex justify-between gap-3">
              <span class="text-muted-foreground">需审批</span>
              <span class="font-semibold text-foreground">{permissionSummary?.approvalRequiredCount ?? 0}</span>
            </div>
          </div>
          <div class="mt-3 flex flex-wrap gap-1.5">
            {#each permissionSummary?.keyPermissions ?? [] as permission}
              <Badge tone="teal">
                <CheckCircle2 class="mr-1 h-3 w-3" />
                {permission}
              </Badge>
            {:else}
              <span class="text-xs text-muted-foreground">-</span>
            {/each}
          </div>
        </div>

        <div class="rounded-xl border border-border bg-card p-4">
          <div class="flex items-center gap-2 text-sm font-semibold text-foreground">
            <Stamp class="h-4 w-4 text-brand" />
            审批
          </div>
          <div class="mt-3 flex flex-wrap gap-1.5">
            {#each approvalPolicyRefs as policy}
              <Badge tone="amber">{policy}</Badge>
            {:else}
              <span class="text-xs text-muted-foreground">-</span>
            {/each}
          </div>
        </div>
      </section>
  </article>
{:else}
  <article class="rounded-xl border border-dashed border-border bg-card p-8 text-center text-sm text-muted-foreground">
    暂无组织数据
  </article>
{/if}
