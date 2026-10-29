import type { PageDTO } from './common';
import type { PermissionDecisionSourceDTO } from './workforce';
import type { components } from '../api.generated';

export type HILDecisionPageDTO = components['schemas']['HILDecisionPage'];
export type HILDecisionRequestDTO = components['schemas']['HILDecisionRequest'];
export type HILCouncilSessionDTO = components['schemas']['HILCouncilSession'];
export type SubmitHILCouncilContributionRequest = components['schemas']['SubmitHILCouncilContributionRequest'];
export type CastHILCouncilVoteRequest = components['schemas']['CastHILCouncilVoteRequest'];
export type HILWorkflowInstanceStatusDTO = components['schemas']['HILWorkflowInstanceStatus'];
export type HILWorkflowInstanceDetailDTO = components['schemas']['HILWorkflowInstanceDetail'];
export type HILWorkflowInstancePageDTO = components['schemas']['HILWorkflowInstancePage'];
export type HILWorkflowTaskDTO = components['schemas']['HILWorkflowTask'];
export type HILWorkflowStageInstanceDTO = components['schemas']['HILWorkflowStageInstance'];
export type HILWorkflowAuditEventDTO = components['schemas']['HILWorkflowAuditEvent'];
export type HILWorkflowNotificationDTO = components['schemas']['HILWorkflowNotification'];
export type DecideHILWorkflowTaskRequest = components['schemas']['DecideHILWorkflowTaskRequest'];
export type TransferHILWorkflowTaskRequest = components['schemas']['TransferHILWorkflowTaskRequest'];
export type ResubmitHILWorkflowRequest = components['schemas']['ResubmitHILWorkflowRequest'];
export type HILWorkflowTemplateDTO = components['schemas']['HILWorkflowTemplate'];
export type HILWorkflowTemplateSummaryDTO = components['schemas']['HILWorkflowTemplateSummary'];
export type HILWorkflowTemplatePageDTO = components['schemas']['HILWorkflowTemplatePage'];
export type HILWorkflowDefinitionDTO = components['schemas']['HILWorkflowDefinition'];
export type SaveHILWorkflowTemplateRequest = components['schemas']['SaveHILWorkflowTemplateRequest'];
export type PublishHILWorkflowTemplateRequest = components['schemas']['PublishHILWorkflowTemplateRequest'];
export type HILWorkflowTemplateVersionDTO = components['schemas']['HILWorkflowTemplateVersion'];
export type HILWorkflowTemplateBindingDTO = components['schemas']['HILWorkflowTemplateBinding'];

export interface HILDecisionMutationRequest {
  decision: 'approved' | 'rejected';
  reason?: string;
  action_id?: string;
}

export interface ApprovalPageDTO {
  items?: ApprovalItemDTO[];
  page?: PageDTO;
}

export interface ApprovalItemDTO {
  approval_id: string;
  approval_type: string;
  display_kind?: string;
  status: string;
  priority?: string;
  mission_id?: string;
  task_id?: string;
  source_type: string;
  source_id: string;
  provider?: string;
  tool_name?: string;
  title: string;
  summary?: string;
  details?: string;
  risk_summary?: string;
  requested_by: string;
  triggered_by?: string;
  decided_by?: string;
  assigned_to?: string;
  organization_code?: string;
  organization_name?: string;
  requested_by_actor?: ApprovalActorRefDTO;
  triggered_by_actor?: ApprovalActorRefDTO;
  decided_by_actor?: ApprovalActorRefDTO;
  target_actor?: ApprovalActorRefDTO;
  policy_id?: string;
  risk_level?: string;
  decision_effect?: string;
  payload_preview?: ApprovalPayloadDTO;
  choices?: ApprovalChoiceDTO[];
  available_actions?: ApprovalActionDTO[];
  evidence_refs?: string[];
  created_at: string;
  decided_at?: string;
  expires_at?: string;
}

export interface ApprovalActorRefDTO {
  actor_type: string;
  actor_id: string;
  display_name?: string;
}

export interface ApprovalPayloadDTO {
  kind?: string;
  language?: string;
  title?: string;
  content?: string;
  fields?: Record<string, string>;
}

export interface ApprovalChoiceDTO {
  choice_id: string;
  label: string;
  description?: string;
}

export interface ApprovalActionDTO {
  action_id: string;
  label: string;
  style?: string;
  decision?: string;
  effect?: string;
  input_kind?: string;
  default_message?: string;
  method: string;
  path: string;
  requires_body?: boolean;
  confirmation_required?: boolean;
  reason_required?: boolean;
  allows_modification?: boolean;
  remember_allowed?: boolean;
}

export interface ApprovalDecisionRequest {
  action_id: string;
  message?: string;
  modified_input?: string;
  choice_id?: string;
  remember?: boolean;
  metadata?: Record<string, string>;
  task_id?: string;
  reply_message_id?: string;
  plan_digest?: string;
}

export interface AuditEventPageDTO {
  events?: AuditEventDTO[];
  page?: PageDTO;
}

export interface AuditEventDTO {
  audit_id: string;
  actor_id: string;
  viewer_id?: string;
  mission_id?: string;
  entity_type: string;
  entity_id: string;
  action: string;
  result: string;
  severity: string;
  correlation_id: string;
  summary?: string;
  metadata?: Record<string, string>;
  occurred_at: string;
}

export interface AuthIPLockPageDTO {
  locks?: AuthIPLockDTO[];
  page?: PageDTO;
}

export interface AuthIPLockDTO {
  ip_address: string;
  status: string;
  failure_count: number;
  last_username?: string;
  last_reason?: string;
  locked_at?: string;
  updated_at?: string;
  unlocked_at?: string;
  unlocked_by?: string;
  unlock_reason?: string;
}

export interface EvaluationPageDTO {
  items?: EvaluationItemDTO[];
  page?: PageDTO;
}

export interface EvaluationItemDTO {
  entity_type: string;
  entity_id: string;
  display_name?: string;
  mission_id?: string;
  role_name?: string;
  department?: string;
  position?: string;
  runtime_kind?: string;
  assigned_tasks: number;
  completed_tasks: number;
  rework_count?: number;
  blocker_count?: number;
  resource_usage_count?: number;
  discussion_reply_count?: number;
  average_duration_seconds?: number;
  token_consumed?: number;
  quality_score: number;
  on_time_rate?: number;
  collaboration_score?: number;
  efficiency_score?: number;
  stability_score?: number;
  summary?: string;
  capabilities?: EvaluationCapabilityDTO[];
  investment?: EvaluationInvestmentDTO;
  latest_review?: EvaluationReviewDTO;
  task_history?: EvaluationTaskHistoryDTO[];
  trend?: EvaluationTrendPointDTO[];
  representative_cases?: EvaluationCaseDTO[];
  suggestions?: string[];
  advantages?: string[];
  risks?: string[];
  scenarios?: string[];
  last_activity_at?: string;
}

export interface EvaluationCapabilityDTO {
  name: string;
  score: number;
}

export interface EvaluationInvestmentDTO {
  token_consumed: number;
  estimated_value_yuan?: number;
  estimated_human_days?: number;
  token_value_per_10k?: number;
  return_multiple: number;
  summary?: string;
}

export interface EvaluationReviewDTO {
  review_id: string;
  entity_type: string;
  entity_id: string;
  scope_kind?: string;
  scope_id?: string;
  evaluator_agent_id?: string;
  conversation_id?: string;
  scheduled_run_id?: string;
  window_days?: number;
  summary?: string;
  estimated_human_days?: number;
  evaluation_token_consumed?: number;
  created_at?: string;
}

export interface EvaluationTaskHistoryDTO {
  goal: string;
  responsibility: string;
  type: string;
  result: string;
  quality: number;
  token_consumed: number;
  duration_seconds: number;
  risk?: string;
  time?: string;
}

export interface EvaluationTrendPointDTO {
  date: string;
  tasks: number;
  score: number;
  token_consumed?: number;
}

export interface EvaluationCaseDTO {
  title: string;
  result: string;
  summary: string;
  date?: string;
}

export interface PermissionMatrixDTO {
  rows?: EffectivePermissionRowDTO[];
}

export interface EffectivePermissionRowDTO {
  agent_id: string;
  resources?: EffectiveResourcePermissionDTO[];
}

export interface EffectiveResourcePermissionDTO {
  tool_id: string;
  tool_name: string;
  declarations?: EffectivePermissionDeclarationDTO[];
}

export interface EffectivePermissionDeclarationDTO {
  scope: string;
  risk: string;
  required: boolean;
  effective: boolean;
  source: string;
  decision?: string;
  sources?: PermissionDecisionSourceDTO[];
}

export interface PermissionCapabilityCatalogDTO {
  capability_id: string;
  domain: string;
  action: string;
  access_mode: "read" | "write" | string;
  scope: string;
  method: string;
  path: string;
  label_zh: string;
  label_en: string;
  category_zh: string;
  category_en: string;
  description?: string;
}

export interface PermissionRoleDTO {
  role_id: string;
  label_zh: string;
  label_en: string;
  built_in?: boolean;
}

export interface PermissionRoleAssignmentDTO {
  principal_kind: string;
  principal_id: string;
  role_id: string;
}

export interface PermissionGrantDTO {
  grant_id?: string;
  subject_kind: string;
  subject_id: string;
  capability_id?: string;
  scope?: string;
  resource_kind?: string;
  resource_id?: string;
}

export interface PermissionEffectiveGrantDTO {
  principal_kind: string;
  principal_id: string;
  capability_id: string;
  scope: string;
  source: string;
  source_id?: string;
}

export interface PermissionPolicyDTO {
  roles?: PermissionRoleDTO[];
  role_assignments?: PermissionRoleAssignmentDTO[];
  grants?: PermissionGrantDTO[];
}

export interface PermissionPolicyViewDTO {
  capabilities?: PermissionCapabilityCatalogDTO[];
  policy?: PermissionPolicyDTO;
  effective?: PermissionEffectiveGrantDTO[];
  enforcement_mode?: 'authorization' | 'local_policy' | 'scope_only' | string;
  policy_mutable?: boolean;
}

export interface PermissionRoleSummaryDTO {
  role_id: string;
  label_zh: string;
  label_en: string;
  built_in: boolean;
  granted_package_count: number;
}

export interface PermissionRolePageDTO {
  items: PermissionRoleSummaryDTO[];
  next_cursor?: string;
  revision: number;
}

export interface PermissionPackageDTO {
  package_id: string;
  page_id: string;
  page_label_zh: string;
  page_label_en: string;
  label_zh: string;
  label_en: string;
  access_mode: "read" | "write" | string;
  scopes: string[];
  capability_count: number;
  grant_state: "none" | "partial" | "granted" | string;
}

export interface PermissionPackagePageDTO {
  items: PermissionPackageDTO[];
  next_cursor?: string;
}

export interface PermissionPackageCapabilityDTO extends PermissionCapabilityCatalogDTO {
  granted: boolean;
}

export interface PermissionPackageCapabilityPageDTO {
  items: PermissionPackageCapabilityDTO[];
  next_cursor?: string;
}

export interface PermissionEffectiveGrantPageDTO {
  items: Array<{
    principal_kind: string;
    principal_id: string;
    scope: string;
    source: string;
    source_id: string;
  }>;
  next_cursor?: string;
}

export interface PermissionPolicyBatchDTO {
  expected_revision: number;
  role_creates?: Array<{ role_id: string; label_zh: string; label_en: string }>;
  package_changes?: Array<{ role_id: string; package_id: string; effect: "grant" | "revoke" }>;
  capability_changes?: Array<{ role_id: string; capability_id: string; effect: "grant" | "revoke" }>;
}

export interface PermissionPolicyBatchResultDTO {
  revision: number;
}
