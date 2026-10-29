import type { BadgeTone } from './common';

export interface WorkforceSummaryViewModel {
  total: number;
  enabled: number;
  disabled: number;
  ai: number;
  human: number;
}

export interface WorkforceMemberViewModel {
  id: string;
  profileId: string;
  personalAssistant?: boolean;
  personalAssistantOwnerId?: string;
  agentInstanceId: string;
  realName: string;
  displayName: string;
  agentKind: string;
  userAccount: WorkforceUserAccountViewModel;
  accountProjection?: WorkforceAccountProjectionViewModel;
  employmentType: string;
  placementStatus: "internal" | "market" | string;
  organizationId: string;
  departmentId: string;
  positionId: string;
  departmentDisplayName: string;
  positionDisplayName: string;
  roleName: string;
  roleLabels: string[];
  primaryRoleLabel: string;
  capabilityTags: string[];
  roles: string[];
  functionalRoles?: string[];
  functionalRoleLabels?: string[];
  managerId: string;
  authorityScope: string;
  supervisionPolicy: string;
  workspaceRoot: string;
  runtimeKind: string;
  runtimeConfigRef: string;
  runtimeConfig: Record<string, unknown>;
  baseAgent: WorkforceBaseAgentViewModel;
  resume: string;
  prompt: string;
  responsibilityBoundary: string;
  behaviorBoundary: string;
  knowledgeBaseId?: string;
  skillRefs: WorkforceSkillRefViewModel[];
  toolRefs: WorkforceToolRefViewModel[];
  personalMemoryRef: WorkforceMemoryRefViewModel;
  collectiveMemoryRefs: WorkforceMemoryRefViewModel[];
  qaEnabled: boolean;
  enabled: boolean;
  statusLabel: string;
  statusTone: BadgeTone;
  email: string;
  availabilityStatus: string;
  releasedFromOrganizationId: string;
  releasedFromDepartmentId: string;
  releasedFromPositionId: string;
  releasedAt: string;
  releasedReason: string;
}

export interface WorkforceUserAccountViewModel {
  username: string;
  nickname: string;
  email: string;
  role: string;
  accountEnabled: boolean;
  permissionScope: string[];
}

export interface WorkforceAccountProjectionViewModel {
  status: string;
  revision: number;
  kratosIdentityId: string;
}

export interface WorkforceBaseAgentViewModel {
  inheritsBaseAgent: boolean;
  auditRequired: boolean;
  systemPrompt?: string;
  hasSystemPrompt: boolean;
  hasLlmApiKeyRef: boolean;
  llmApiKeyRef: string;
  skillScope: string;
  toolScope: string;
  mcpScope: string;
}

export interface WorkforceSkillRefViewModel {
  skillId: string;
  versionConstraint: string;
  alias: string;
  level: string;
  evidenceRefs: string[];
}

export interface WorkforceToolRefViewModel {
  toolId: string;
  versionConstraint: string;
  alias: string;
  permissionScope: string;
  bindingPolicy: string;
}

export interface WorkforceMemoryRefViewModel {
  memoryProfileId: string;
  ownerAgentId: string;
  memoryKind: string;
  accessScope: string;
  retrievalPolicy: string;
  compressionPolicy: string;
}

export interface WorkforceMemberFormViewModel {
  agentProfileId?: string;
  personalAssistant?: boolean;
  agentInstanceId?: string;
  realName?: string;
  displayName: string;
  mailboxEmail?: string;
  agentKind: string;
  accountUsername?: string;
  accountNickname?: string;
  accountEmail?: string;
  accountRole?: string;
  accountEnabled?: string;
  accountProjectionStatus?: string;
  accountProjectionRevision?: number;
  accountPermissionScope?: string;
  accountPassword?: string;
  accountPasswordConfirm?: string;
  employmentType?: string;
  organizationId: string;
  departmentId: string;
  positionId: string;
  roles: string;
  functionalRoles?: string;
  resume?: string;
  prompt?: string;
  responsibilityBoundary?: string;
  behaviorBoundary?: string;
  skillRefs?: string;
  toolRefs?: string;
  personalMemoryId?: string;
  collectiveMemoryRefs?: string;
  qaEnabled?: string;
  workspaceRoot?: string;
  runtimeKind: string;
  runtimeConfigRef: string;
  runtimeConfig: Record<string, string | string[]>;
  enabled: string;
}

export interface TalentMarketHireFormViewModel {
  agentProfileId: string;
  organizationId: string;
  departmentId: string;
  positionId: string;
  roles: string;
  managerId?: string;
  reportingLineId?: string;
}

export interface WorkforceViewModel {
  summary: WorkforceSummaryViewModel;
  members: WorkforceMemberViewModel[];
}

export interface OrgGraphViewModel {
  summary: {
    organizations: number;
    departments: number;
    positions: number;
    members: number;
    edges: number;
    organizationNodes?: number;
    rootCount?: number;
    activeOrganizations?: number;
    inactiveOrganizations?: number;
    totalMemberCount?: number;
    roleAssignmentCount?: number;
    pendingApprovalCount?: number;
  };
  organizations: OrganizationTreeViewModel[];
  departments: Array<{
    id: string;
    organizationId: string;
    memberCount: number;
  }>;
  positions: Array<{ id: string; departmentId: string; memberCount: number }>;
  members: WorkforceMemberViewModel[];
  edges: Array<{ from: string; to: string; lineId: string }>;
  chartOption: Record<string, any>;
}

export interface OrganizationTreeViewModel {
  id: string;
  organizationId?: string;
  nodeType?: string;
  code?: string;
  displayCode?: string;
  name: string;
  level?: number;
  status?: string;
  ownerProfileId?: string;
  ownerEmployeeNo?: string;
  ownerName?: string;
  knowledgeBaseId?: string;
  parentId?: string;
  parentCode?: string;
  parentDisplayCode?: string;
  parentName?: string;
  directMemberCount?: number;
  totalMemberCount?: number;
  memberCount: number;
  unitCount: number;
  childCount?: number;
  directMembers?: WorkforceMemberViewModel[];
  permissionSummary?: OrganizationPermissionSummaryViewModel;
  roleAssignments?: RoleAssignmentViewModel[];
  approvalPolicyRefs?: string[];
  positions?: OrganizationPositionViewModel[];
  units: OrganizationUnitViewModel[];
  children?: OrganizationTreeViewModel[];
}

export interface OrganizationPermissionSummaryViewModel {
  effectiveRoleCount: number;
  directRoleCount: number;
  inheritedRoleCount: number;
  keyPermissions: string[];
  denyPolicyCount: number;
  approvalRequiredCount: number;
}

export interface ActorRefViewModel {
  actorType: string;
  actorId: string;
}

export interface RoleAssignmentViewModel {
  assignmentId: string;
  actor: ActorRefViewModel;
  roleId: string;
  organizationNodeId: string;
  organizationCode: string;
  scopeMode: string;
  source: string;
  startsAt: string;
  expiresAt: string;
  assignedBy: string;
}

export interface OrganizationUnitViewModel {
  id: string;
  name: string;
  organizationId: string;
  parentId: string;
  defaultPositionId: string;
  code?: string;
  displayCode?: string;
  status?: string;
  ownerProfileId?: string;
  ownerEmployeeNo?: string;
  ownerName?: string;
  knowledgeBaseId?: string;
  directMemberCount?: number;
  totalMemberCount?: number;
  memberCount: number;
  directMembers?: WorkforceMemberViewModel[];
  positions: OrganizationPositionViewModel[];
  children: OrganizationUnitViewModel[];
}

export interface OrganizationPositionViewModel {
  id: string;
  name: string;
  departmentId: string;
  knowledgeBaseId?: string;
  memberCount: number;
  members: WorkforceMemberViewModel[];
}

export interface RelationshipHierarchyNode {
  id: string;
  label: string;
  subtitle: string;
  detail?: string;
  kind: "organization" | "department" | "person";
  profileId?: string;
  human?: boolean;
  disabled?: boolean;
  children: RelationshipHierarchyNode[];
}

export interface OrganizationCreateFormViewModel {
  name: string;
  defaultDepartmentName?: string;
  defaultPositionName?: string;
}

export interface OrganizationUnitCreateFormViewModel {
  organizationId: string;
  name: string;
  parentDepartmentId: string;
  defaultPositionName?: string;
}

export interface OrganizationPositionCreateFormViewModel {
  organizationId: string;
  departmentId?: string;
  name: string;
}

export interface SquadMemberViewModel {
  id: string;
  profileId: string;
  name: string;
  role: string;
  source: "human" | "agent";
  status: string;
}

export interface SquadViewModel {
  id: string;
  name: string;
  status: string;
  humanCount: number;
  agentCount: number;
  missionId: string;
  members: SquadMemberViewModel[];
}

export interface SquadDirectoryViewModel {
  summary: {
    squads: number;
    humans: number;
    agents: number;
    active: number;
  };
  squads: SquadViewModel[];
}
