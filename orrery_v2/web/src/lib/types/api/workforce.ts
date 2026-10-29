import type { PageDTO } from './common';
import type { SkillDTO } from './resources';

export interface WorkforcePageDTO {
  members?: WorkforceMemberDTO[];
  page?: PageDTO;
}

export interface WorkforceMemberDTO {
  agent_id: string;
  agent_profile_id: string;
  personal_assistant?: boolean;
  personal_assistant_owner_id?: string;
  agent_instance_id?: string;
  real_name: string;
  display_name?: string;
  agent_kind: string;
  user_account?: WorkforceUserAccountDTO;
  account_projection?: WorkforceAccountProjectionDTO;
  employment_type: string;
  placement_status?: "internal" | "market" | string;
  organization_id?: string;
  department_id?: string;
  position_id?: string;
  department_display_name?: string;
  position_display_name?: string;
  role_name?: string;
  availability_status?: string;
  capability_tags?: string[];
  email?: string;
  person_source?: WorkforcePersonSourceFactsDTO;
  role_ids?: string[];
  functional_role_ids?: string[];
  manager_agent_id?: string;
  reporting_line_id?: string;
  authority_scope?: string;
  supervision_policy?: string;
  workspace_root?: string;
  runtime_kind?: string;
  runtime_config_ref?: string;
  runtime_config?: Record<string, unknown>;
  base_agent?: WorkforceBaseAgentDTO;
  persona?: WorkforcePersonaDTO;
  knowledge_base_id?: string;
  personal_memory_ref?: WorkforceMemoryProfileRefDTO;
  collective_memory_refs?: WorkforceMemoryProfileRefDTO[];
  skill_refs?: WorkforceSkillRefDTO[];
  tool_refs?: WorkforceToolRefDTO[];
  qa_enabled?: boolean;
  enabled: boolean;
  released_from_organization_id?: string;
  released_from_department_id?: string;
  released_from_position_id?: string;
  released_at?: string;
  released_reason?: string;
}

export interface WorkforcePersonSourceFactsDTO {
  employee_number?: string;
  source?: string;
  source_record_id?: string;
  source_tenant_id?: string;
}

export interface WorkforceAccountProjectionDTO {
  status?: string;
  revision?: number;
  kratos_identity_id?: string;
  initial_password?: string;
}

export interface WorkforceAvatarDTO {
  agent_profile_id: string
  avatar_url: string
  content_type: 'image/png' | 'image/jpeg' | 'image/webp'
  size: number
  updated_at: string
  etag: string
}

export interface WorkforceAvatarDefaultDTO {
  id: string
  avatar_url: string
  content_type: 'image/png' | 'image/jpeg' | 'image/webp'
  size: number
  etag: string
}

export interface WorkforceUserAccountDTO {
  username?: string;
  nickname?: string;
  email?: string;
  role?: string;
  account_enabled?: boolean;
  permission_scope?: string[];
}

export interface WorkforceUserAccountCommandDTO {
  username?: string;
  nickname?: string;
  email?: string;
  role?: string;
  account_enabled?: boolean;
  password?: string;
}

export interface WorkforceBaseAgentDTO {
  inherits_base_agent: boolean;
  audit_required: boolean;
  system_prompt?: string;
  has_system_prompt: boolean;
  has_llm_api_key_ref: boolean;
  llm_api_key_ref?: string;
  skill_scope?: string;
  tool_scope?: string;
  mcp_scope?: string;
}

export interface WorkforcePersonaDTO {
  backstory?: string;
  responsibility_boundary?: string;
  behavior_boundary?: string;
}

export interface WorkforceSkillRefDTO {
  skill_id: string;
  version_constraint?: string;
  alias?: string;
  level?: string;
  evidence_refs?: string[];
}

export interface WorkforceToolRefDTO {
  tool_id: string;
  version_constraint?: string;
  alias?: string;
  permission_scope?: string;
  binding_policy?: string;
}

export interface WorkforceMemoryProfileRefDTO {
  memory_profile_id?: string;
  owner_agent_id?: string;
  memory_kind?: string;
  access_scope?: string;
  retrieval_policy?: string;
  compression_policy?: string;
}

export interface PeopleResponsibilityBindingDTO {
  binding_id: string
  id?: string
  responsibility_id: string
  target_type: string
  target_id: string
  valid_from: string
  valid_to?: string | null
  revision: number
}

export interface PeopleResolvedResponsibilityDTO {
  id: string
  code: string
  title: string
  description: string
  valid_from: string
  valid_to?: string | null
  revision: number
  sources: PeopleResponsibilityBindingDTO[]
}

export interface PeopleResponsibilityPortfolioVersionDTO {
  version_id: string
  source_revision: string
  source_digest: string
  responsibilities: PeopleResolvedResponsibilityDTO[]
  source_as_of: string
  recorded_at: string
}

export interface PeopleResponsibilityGapDTO {
  id: string
  code: string
  message: string
  open: boolean
  first_seen_at: string
  last_seen_at: string
  resolved_at?: string
}

export interface PeopleResponsibilityAuditDTO {
  audit_id: string
  entity_type: string
  entity_id: string
  action: string
  actor_id?: string
  from_state?: string
  to_state?: string
  details?: string
  occurred_at: string
}

export interface PeopleResponsibilityPortfolioProfileDTO {
  binding_id: string
  people_tenant_id: string
  subject_id: string
  agent_profile_id: string
  agent_display_name: string
  orrery_organization_id?: string
  orrery_department_id?: string
  orrery_position_id?: string
  status: string
  current_version_id: string
  current_source_revision: string
  current_source_digest: string
  responsibilities: PeopleResolvedResponsibilityDTO[]
  versions: PeopleResponsibilityPortfolioVersionDTO[]
  pulse_scheduled_task_id?: string
  gap?: PeopleResponsibilityGapDTO
  audit: PeopleResponsibilityAuditDTO[]
  source_as_of: string
  synced_at: string
}

export interface PeopleResponsibilityProfileDTO {
  scope_type: 'agent' | 'organization' | string
  scope_id: string
  source: string
  portfolios: PeopleResponsibilityPortfolioProfileDTO[]
  responsibility_count: number
  version_count: number
  updated_at: string
}

export interface OrgGraphDTO {
  organizations?: OrganizationDTO[];
  departments?: DepartmentDTO[];
  positions?: PositionDTO[];
  reporting_edges?: ReportingEdgeDTO[];
  members?: WorkforceMemberDTO[];
  organization_nodes?: OrganizationNodeDTO[];
  summary?: OrgGraphSummaryDTO;
}

export interface CreateOrganizationCommand {
  name: string;
  default_department_name?: string;
  default_position_name?: string;
}

export interface CreateOrganizationUnitCommand {
  organization_id: string;
  name: string;
  parent_department_id?: string;
  default_position_name?: string;
}

export interface CreateOrganizationPositionCommand {
  organization_id: string;
  department_id?: string;
  name: string;
}

export interface OrganizationDTO {
  organization_id: string;
  name: string;
  default_department_id?: string;
  default_position_id?: string;
  code?: string;
  display_code?: string;
  status?: string;
  owner_profile_id?: string;
  owner_employee_no?: string;
  owner_name?: string;
  knowledge_base_id?: string;
  node_type?: string;
}

export interface DepartmentDTO {
  department_id: string;
  organization_id: string;
  name?: string;
  parent_department_id?: string;
  default_position_id?: string;
  member_count: number;
  code?: string;
  display_code?: string;
  status?: string;
  owner_profile_id?: string;
  owner_employee_no?: string;
  owner_name?: string;
  node_type?: string;
}

export interface ActorRefDTO {
  actor_type?: string;
  actor_id?: string;
}

export interface PermissionSummaryDTO {
  effective_role_count?: number;
  direct_role_count?: number;
  inherited_role_count?: number;
  key_permissions?: string[];
  deny_policy_count?: number;
  approval_required_count?: number;
}

export interface RoleDefinitionDTO {
  role_id: string;
  name: string;
  description?: string;
  category: string;
  status: string;
  assignable_actor_types: string[];
  built_in: boolean;
  permission_refs?: string[];
  approval_capability: boolean;
}

export interface RoleDefinitionPageDTO {
  items: RoleDefinitionDTO[];
  page?: PageDTO;
}

export interface RoleAssignmentDTO {
  assignment_id?: string;
  actor?: ActorRefDTO;
  role_id?: string;
  organization_node_id?: string;
  organization_code?: string;
  scope_mode?: string;
  source?: string;
  starts_at?: string;
  expires_at?: string;
  assigned_by?: string;
}

export interface RoleAssignmentPageDTO {
  items: RoleAssignmentDTO[];
  page?: PageDTO;
}

export interface PutRoleAssignmentsCommand {
  assignments: RoleAssignmentDTO[];
}

export interface OrganizationNodeDTO {
  id: string;
  node_type?: string;
  code?: string;
  display_code?: string;
  name: string;
  level?: number;
  status?: string;
  owner_profile_id?: string;
  owner_employee_no?: string;
  owner_name?: string;
  knowledge_base_id?: string;
  parent_id?: string;
  parent_code?: string;
  parent_display_code?: string;
  parent_name?: string;
  direct_member_count?: number;
  total_member_count?: number;
  child_count?: number;
  permission_summary?: PermissionSummaryDTO;
  role_assignments?: RoleAssignmentDTO[];
  approval_policy_refs?: string[];
  children?: OrganizationNodeDTO[];
}

export interface OrgGraphSummaryDTO {
  organization_count?: number;
  root_count?: number;
  active_count?: number;
  inactive_count?: number;
  total_member_count?: number;
  role_assignment_count?: number;
  pending_approval_count?: number;
}

export interface PermissionDecisionSourceDTO {
  source_type?: string;
  source_id?: string;
  role_id?: string;
  organization_code?: string;
  resource_id?: string;
  effect?: string;
  reason?: string;
}

export interface ApprovalRequirementDTO {
  requirement_id?: string;
  policy_id?: string;
  approval_type?: string;
  organization_code?: string;
  approver_type?: string;
  approver_ref?: string;
  reason?: string;
}

export interface PermissionDecisionRequestDTO {
  subject: ActorRefDTO;
  on_behalf_of?: ActorRefDTO;
  action: string;
  resource_type: string;
  resource_id: string;
  organization_code?: string;
  mission_id?: string;
  task_id?: string;
  risk_level?: string;
  environment?: string;
  metadata?: Record<string, string>;
}

export interface PermissionDecisionDTO {
  decision: string;
  reason?: string;
  sources?: PermissionDecisionSourceDTO[];
  required_approvals?: ApprovalRequirementDTO[];
  expires_at?: string;
  audit_id?: string;
}

export interface PositionDTO {
  position_id: string;
  department_id: string;
  name?: string;
  knowledge_base_id?: string;
  member_count: number;
}

export interface ReportingEdgeDTO {
  from_agent_id: string;
  to_manager_agent_id: string;
  reporting_line_id?: string;
}

export interface UpsertWorkforceMemberCommand {
  agent_profile_id?: string;
  real_name?: string;
  display_name?: string;
  email?: string;
  agent_kind?: string;
  user_account?: WorkforceUserAccountCommandDTO;
  employment_type?: string;
  organization_id?: string;
  department_id?: string;
  position_id?: string;
  role_ids?: string[];
  manager_agent_id?: string;
  reporting_line_id?: string;
  authority_scope?: string;
  supervision_policy?: string;
  workspace_root?: string;
  runtime_kind?: string;
  runtime_config_ref?: string;
  runtime_config?: Record<string, unknown>;
  system_prompt?: string;
  persona?: WorkforcePersonaDTO;
  personal_memory_id?: string;
  collective_memory_refs?: WorkforceMemoryProfileRefDTO[];
  skill_refs?: WorkforceSkillRefDTO[];
  tool_refs?: WorkforceToolRefDTO[];
  qa_enabled?: boolean;
  enabled?: boolean;
  update_mask?: string[];
}

export type CreateWorkforceMemberCommand = Omit<
  UpsertWorkforceMemberCommand,
  "agent_profile_id"
>;

export interface ReleaseWorkforceMemberCommand {
  agent_profile_id?: string;
  reason?: string;
}

export interface HireTalentFromMarketCommand {
  agent_profile_id: string;
  organization_id: string;
  department_id: string;
  position_id: string;
  role_ids?: string[];
  manager_agent_id?: string;
  reporting_line_id?: string;
}

export interface DeleteTalentMarketMembersCommand {
  agent_profile_ids: string[];
  reason?: string;
}

export interface DeleteTalentMarketMembersResponseDTO {
  deleted: string[];
  skipped?: TalentMarketDeleteSkippedDTO[];
  deleted_count: number;
}

export interface TalentMarketDeleteSkippedDTO {
  agent_profile_id: string;
  reason: string;
}

export interface TalentMarketImportCandidateDTO {
  agent_profile_id?: string;
  real_name: string;
  display_name?: string;
  role_name?: string;
  agent_kind?: string;
  description?: string;
  category?: string;
  source_slug?: string;
  source_path?: string;
  source_hash?: string;
  source_prompt?: string;
  runtime_kind?: string;
  runtime_config_ref?: string;
  runtime_config?: Record<string, unknown>;
  persona?: WorkforcePersonaDTO;
  personal_memory_ref?: WorkforceMemoryProfileRefDTO;
  collective_memory_refs?: WorkforceMemoryProfileRefDTO[];
  skill_refs?: WorkforceSkillRefDTO[];
  tool_refs?: WorkforceToolRefDTO[];
  qa_enabled?: boolean;
  enabled?: boolean;
  conflict_resolution?: "create" | "overwrite" | "rename" | "skip" | string;
  resolved_real_name?: string;
}

export interface TalentMarketImportPreviewRequest {
  candidates: TalentMarketImportCandidateDTO[];
  skills?: SkillDTO[];
}

export interface TalentMarketImportPreviewResponseDTO {
  items: TalentMarketImportPreviewItemDTO[];
  conflicts: TalentMarketImportConflictDTO[];
  skills?: SkillDTO[];
}

export interface TalentMarketImportPreviewItemDTO {
  candidate_index: number;
  candidate: TalentMarketImportCandidateDTO;
  status: "create" | "conflict" | string;
  conflicts?: TalentMarketImportConflictDTO[];
}

export interface TalentMarketImportConflictDTO {
  candidate_index: number;
  conflict_type: "agent_profile_id" | "real_name" | "display_name" | string;
  existing_agent_profile_id: string;
  existing_real_name: string;
  existing_display_name?: string;
  existing_placement_status?: "internal" | "market" | string;
  candidate_real_name: string;
  candidate_display_name?: string;
  candidate_slug?: string;
}

export interface TalentMarketImportCommitRequest {
  candidates: TalentMarketImportCandidateDTO[];
  skills?: SkillDTO[];
}

export interface TalentMarketImportCommitResponseDTO {
  imported: WorkforceMemberDTO[];
  skipped?: TalentMarketImportSkippedDTO[];
  conflicts?: TalentMarketImportConflictDTO[];
  preview?: TalentMarketImportPreviewResponseDTO;
}

export interface TalentMarketImportSkippedDTO {
  candidate_index: number;
  real_name?: string;
  reason: string;
}

export interface TalentMarketExportRequest {
  agent_profile_ids: string[];
}

export interface TalentMarketExportPackageDTO {
  manifest: TalentMarketExportManifestDTO;
  agents: TalentMarketImportCandidateDTO[];
  skills?: SkillDTO[];
}

export interface TalentMarketExportManifestDTO {
  format: "orrery.talent-market" | string;
  version: number;
  count: number;
  exported_at?: string;
  source_app?: string;
  description?: string;
}
