<script lang="ts">
  import { Building2, GitBranch, Network, UserRound } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import WorkforceAvatar from '#lib/components/operations/WorkforceAvatar.svelte'
  import RelationshipHierarchyGraph from '#lib/components/operations/RelationshipHierarchyGraph.svelte'
  import type { OrganizationTreeViewModel, RelationshipHierarchyNode, WorkforceMemberViewModel } from '#lib/types/view-models'
  import {
    buildReportingHierarchy,
    filterReportingMembers,
    findHierarchyNode,
    findMemberByIdentifier
  } from './organizationReportingGraph'

  type GraphView = 'organization' | 'reporting'

  interface Props {
    organization?: OrganizationTreeViewModel
    organizations: OrganizationTreeViewModel[]
    members: WorkforceMemberViewModel[]
    reportingEdges: Array<{ from: string; to: string; lineId: string }>
    selectedOrganizationId?: string
    onselectOrganization?: (id: string) => void
  }

  let {
    organization,
    organizations,
    members,
    reportingEdges,
    selectedOrganizationId = '',
    onselectOrganization
  }: Props = $props()

  let activeView = $state<GraphView>('organization')
  let selectedMemberId = $state('')

  const organizationNodes = $derived(flattenOrganizationNodes(organizations))
  const selectedOrganization = $derived(organization ?? organizationNodes.find((node) => node.id === selectedOrganizationId))
  const profileOrganization = $derived(selectedOrganization)
  const selectedOrganizationResponsibilities = $derived(profileOrganization ? responsibilitiesForNode(profileOrganization) : [])
  const visibleReportingMembers = $derived(filterReportingMembers(members, profileOrganization))
  const selectedMember = $derived(findMemberByIdentifier(visibleReportingMembers, selectedMemberId))
  const organizationHierarchy = $derived(profileOrganization ? [mapOrganizationNode(profileOrganization)] : organizations.map(mapOrganizationNode))
  const reportingHierarchy = $derived(buildReportingHierarchy(visibleReportingMembers, reportingEdges, profileOrganization))
  const selectedReportingNode = $derived(findHierarchyNode(reportingHierarchy, selectedMemberId))

  function flattenOrganizationNodes(nodes: OrganizationTreeViewModel[]): OrganizationTreeViewModel[] {
    return nodes.flatMap((node) => [node, ...flattenOrganizationNodes(node.children ?? [])])
  }

  function mapOrganizationNode(node: OrganizationTreeViewModel): RelationshipHierarchyNode {
    const owner = node.ownerName || node.ownerEmployeeNo || node.ownerProfileId
    const directCount = node.directMemberCount ?? node.directMembers?.length ?? 0
    const totalCount = node.totalMemberCount ?? node.memberCount ?? 0
    return {
      id: node.id,
      label: node.name,
      subtitle: `${node.nodeType === 'company' ? '组织' : '部门'} · ${node.childCount ?? node.children?.length ?? 0} 个下属单位`,
      detail: [
        owner ? `负责人：${owner}` : '未设置负责人',
        `直属 ${directCount} / 总计 ${totalCount}`
      ].join(' · '),
      kind: node.nodeType === 'company' ? 'organization' : 'department',
      children: (node.children ?? []).map(mapOrganizationNode)
    }
  }

  function responsibilitiesForNode(node: OrganizationTreeViewModel): string[] {
    return Array.from(new Set(
      collectMembers(node)
        .map((member) => member.responsibilityBoundary.trim())
        .filter(Boolean)
    ))
  }

  function collectMembers(node: OrganizationTreeViewModel): WorkforceMemberViewModel[] {
    return [
      ...(node.directMembers ?? []),
      ...(node.children ?? []).flatMap((child) => collectMembers(child))
    ]
  }

  function selectOrganization(id: string) {
    onselectOrganization?.(id)
  }

  function selectReportingMember(id: string) {
    selectedMemberId = id
  }
</script>

<section class="overflow-hidden border-y border-border" data-organization-graph-workspace>
  <div class="flex flex-col gap-3 border-b border-border px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
    <div>
      <h2 class="text-sm font-semibold text-foreground">关系图谱</h2>
      <p class="mt-1 text-xs text-muted-foreground">组织关系图以组织为节点，汇报关系图以人员为节点。</p>
    </div>
    <div class="inline-flex w-fit rounded-md border border-border bg-muted/45 p-0.5" role="tablist" aria-label="关系图谱视图">
      <button type="button" role="tab" aria-selected={activeView === 'organization'} class={`inline-flex h-8 items-center gap-1.5 rounded px-2.5 text-xs font-medium transition-colors hover:text-foreground ${activeView === 'organization' ? 'bg-background text-foreground' : 'text-muted-foreground'}`} onclick={() => (activeView = 'organization')}>
        <Network class="h-3.5 w-3.5" />
        组织关系图
      </button>
      <button type="button" role="tab" aria-selected={activeView === 'reporting'} class={`inline-flex h-8 items-center gap-1.5 rounded px-2.5 text-xs font-medium transition-colors hover:text-foreground ${activeView === 'reporting' ? 'bg-background text-foreground' : 'text-muted-foreground'}`} onclick={() => (activeView = 'reporting')}>
        <GitBranch class="h-3.5 w-3.5" />
        汇报关系图
      </button>
    </div>
  </div>

  {#if activeView === 'organization'}
    <div class="grid min-w-0 lg:grid-cols-[minmax(0,1fr)_280px]">
      <RelationshipHierarchyGraph roots={organizationHierarchy} selectedId={selectedOrganizationId} emptyMessage="暂无组织关系数据" onselect={selectOrganization} />
      <aside class="min-w-0 border-t border-border p-4 lg:border-l lg:border-t-0">
        <div class="flex items-center gap-2 text-sm font-semibold text-foreground"><Building2 class="h-4 w-4 text-brand" />组织属性</div>
        {#if selectedOrganization}
          <dl class="mt-4 space-y-3 text-xs">
            <div class="flex items-start justify-between gap-3"><dt class="text-muted-foreground">类型</dt><dd><Badge tone={selectedOrganization.nodeType === 'company' ? 'blue' : 'teal'}>{selectedOrganization.nodeType === 'company' ? '组织' : '部门'}</Badge></dd></div>
            <div class="flex items-start justify-between gap-3"><dt class="text-muted-foreground">组织编码</dt><dd class="font-mono text-right text-foreground">{selectedOrganization.displayCode || selectedOrganization.code || '未编码'}</dd></div>
            <div class="flex items-start justify-between gap-3"><dt class="text-muted-foreground">上级组织</dt><dd class="max-w-40 text-right text-foreground">{selectedOrganization.parentName || selectedOrganization.parentDisplayCode || selectedOrganization.parentCode || '顶层组织'}</dd></div>
            <div class="flex items-start justify-between gap-3"><dt class="text-muted-foreground">组织状态</dt><dd class="text-foreground">{selectedOrganization.status || 'active'}</dd></div>
            <div class="flex items-start justify-between gap-3"><dt class="text-muted-foreground">下级单位</dt><dd class="tabular-nums text-foreground">{selectedOrganization.childCount ?? selectedOrganization.children?.length ?? 0}</dd></div>
            <div class="flex items-start justify-between gap-3"><dt class="text-muted-foreground">人员规模（直属 / 总计）</dt><dd class="tabular-nums text-foreground">{selectedOrganization.directMemberCount ?? selectedOrganization.directMembers?.length ?? 0} / {selectedOrganization.totalMemberCount ?? selectedOrganization.memberCount ?? 0}</dd></div>
          </dl>
          {#if selectedOrganizationResponsibilities.length > 0}
            <div class="mt-5 border-t border-border pt-4"><p class="text-xs text-muted-foreground">组织责任</p><ul class="mt-2 space-y-2">{#each selectedOrganizationResponsibilities.slice(0, 3) as responsibility}<li class="border-l-2 border-brand/50 pl-2.5 text-xs leading-5 text-foreground/80">{responsibility}</li>{/each}</ul></div>
          {/if}
        {:else}
          <p class="mt-3 text-xs leading-5 text-muted-foreground">点击组织节点查看属性。</p>
        {/if}
      </aside>
    </div>
  {:else}
    <div class="grid min-w-0 lg:grid-cols-[minmax(0,1fr)_280px]">
      <RelationshipHierarchyGraph roots={reportingHierarchy} selectedId={selectedMemberId} emptyMessage="暂无人员汇报关系数据" onselect={selectReportingMember} />
      <aside class="min-w-0 border-t border-border p-4 lg:border-l lg:border-t-0">
        <div class="flex items-center gap-2 text-sm font-semibold text-foreground"><UserRound class="h-4 w-4 text-brand" />人员汇报档案</div>
        {#if selectedMember}
          <div class="mt-3 min-w-0">
            <div class="flex items-center gap-2.5">
              <WorkforceAvatar profileId={selectedMember.profileId} name={selectedMember.displayName} human={(selectedMember.agentKind || '').toLowerCase() === 'human'} class="h-10 w-10 rounded-lg" textClass="text-xs" />
              <div class="min-w-0"><div class="truncate text-sm font-semibold text-foreground">{selectedMember.displayName}</div><p class="mt-1 truncate text-xs text-muted-foreground">{selectedMember.departmentDisplayName || '未设置部门'} / {selectedMember.roleName || selectedMember.primaryRoleLabel || '未设置角色'}</p></div>
            </div>
            <p class="mt-4 border-l-2 border-status-blocked/50 pl-2.5 text-xs leading-5 text-foreground/80">{selectedMember.responsibilityBoundary || '该成员尚未维护职责边界。'}</p><p class="mt-4 text-xs text-muted-foreground">直属经理：{selectedMember.managerId || '未设置'}</p>
          </div>
        {:else if selectedReportingNode}
          <div class="mt-3 min-w-0">
            <div class="flex items-center gap-2.5">
              <WorkforceAvatar profileId={selectedReportingNode.profileId || selectedReportingNode.id} name={selectedReportingNode.label} human={selectedReportingNode.human} class="h-10 w-10 rounded-lg" textClass="text-xs" />
              <div class="min-w-0"><div class="truncate text-sm font-semibold text-foreground">{selectedReportingNode.label}</div><p class="mt-1 truncate text-xs text-muted-foreground">{selectedReportingNode.subtitle}</p></div>
            </div>
            <p class="mt-4 border-l-2 border-status-blocked/50 pl-2.5 text-xs leading-5 text-foreground/80">{selectedReportingNode.detail || selectedReportingNode.subtitle}</p>
            <p class="mt-4 text-xs text-muted-foreground">组织负责人或汇报关系中的人员</p>
          </div>
        {:else}
          <p class="mt-3 text-xs leading-5 text-muted-foreground">点击人员节点查看角色、部门和汇报关系。</p>
        {/if}
      </aside>
    </div>
  {/if}
</section>
