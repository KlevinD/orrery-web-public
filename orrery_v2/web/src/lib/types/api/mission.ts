import type { PageDTO } from './common';

export interface MissionListDTO {
  missions?: MissionDTO[];
  summaries?: MissionSummaryDTO[];
  page?: PageDTO;
}

export interface MissionDTO {
  mission_id: string;
  title: string;
  final_goal?: string;
  status: string;
  phase?: string;
  phase_updated_at?: string;
  deadline?: string;
  risk_level?: string;
  origin_kind?: string;
  origin_id?: string;
  origin_todo_id?: string;
  execution_attempt?: number;
  discussion_mode?: string;
  conversation_id?: string;
  origin_development_project_id?: string;
  created_at: string;
}

export interface MissionSummaryDTO {
  mission_id: string;
  title: string;
  status: string;
  phase?: string;
  risk_level?: string;
  team_size?: number;
  active_task_count?: number;
  active_run_count?: number;
  pending_approval_count?: number;
  blocker_count?: number;
  delivery_rate?: number;
  updated_at?: string;
}

export interface MissionControlViewDTO {
  mission: MissionDTO;
  summary: MissionControlSummaryDTO;
  team: TeamDTO;
  task_board: TaskBoardDTO;
  evidence: EvidenceDTO;
  qa: QADTO;
  discussion: DiscussionDTO;
  inbox_summary: InboxSummaryDTO;
  progress_graph: ProgressGraphDTO;
  latest_decisions: DecisionDTO[];
  blockers: BlockerDTO[];
  updated_at: string;
  version: number;
}

export interface MissionControlSummaryDTO {
  active_task_count: number;
  pending_approval_count: number;
  risk_count: number;
  blocker_count: number;
  team_size: number;
  delivery_rate: number;
  updated_at: string;
}

export interface TeamDTO {
  members?: TeamMemberDTO[];
  assignments?: AssignmentDTO[];
  staffing_requests?: StaffingRequestDTO[];
}

export interface TeamMemberDTO {
  agent_id: string;
  agent_profile_id?: string;
  agent_instance_id?: string;
  agent_kind?: string;
  display_name?: string;
  real_name?: string;
  role_name?: string;
  department?: string;
  position?: string;
  mission_role?: string;
  status?: string;
  joined_at?: string;
  personal_memory_ref?: string;
}

export interface AssignmentDTO {
  assignment_id: string;
  agent_id: string;
  agent_profile_id?: string;
  agent_instance_id?: string;
  agent_kind?: string;
  task_id: string;
  mission_role: string;
  changed_at: string;
}

export interface StaffingRequestDTO {
  request_id: string;
  role_name: string;
  reason: string;
  created_at: string;
}

export interface TaskBoardDTO {
  tasks?: TaskCardDTO[];
  dependencies?: TaskDependencyDTO[];
}

export interface TaskCardDTO {
  task_id: string;
  title?: string;
  assignee?: string;
  assignee_display_name?: string;
  assignee_type?: string;
  goal: string;
  source_kind?: string;
  source_id?: string;
  source_step_id?: string;
  worker_personal_goal_summary?: string;
  status: string;
  priority?: string;
  due_at?: string;
  acceptance_criteria?: AcceptanceCriterionDTO[];
  expected_deliverables?: ExpectedDeliverableDTO[];
  required_evidence?: RequiredEvidenceDTO[];
  latest_result_ref?: string;
  qa_status?: string;
  blocker_refs?: string[];
  blocked_reason?: string;
  updated_at: string;
}

export interface AcceptanceCriterionDTO {
  id: string;
  description: string;
}

export interface ExpectedDeliverableDTO {
  id: string;
  title: string;
}

export interface ExpectedDeliverableRequest {
  title: string;
}

export interface RequiredEvidenceDTO {
  id: string;
  description: string;
  kind: string;
  required: boolean;
}

export interface TaskDependencyDTO {
  task_id: string;
  depends_on: string;
}

export interface EvidenceDTO {
  evidence_items?: EvidenceItemDTO[];
  missing_requirements?: RequiredEvidenceDTO[];
}

export interface EvidenceItemDTO {
  evidence_id: string;
  task_id: string;
  agent_id: string;
  evidence_requirement_id?: string;
  kind: string;
  artifact_id?: string;
  artifact_uri?: string;
  tool_log_ref?: string;
  external_url?: string;
  submitted_at: string;
}

export interface QADTO {
  checks?: QACheckDTO[];
  approvals?: QADecisionDTO[];
  rejections?: QADecisionDTO[];
  rework_requests?: ReworkRequestDTO[];
}

export interface QACheckDTO {
  qa_ref: string;
  task_id: string;
  status: string;
  message: string;
  created_at: string;
}

export interface QADecisionDTO {
  qa_ref: string;
  task_id: string;
  reason: string;
  message: string;
  decided_at: string;
}

export interface ReworkRequestDTO {
  task_id: string;
  qa_ref: string;
  reason: string;
  message: string;
  status: string;
  created_at: string;
  resolved_at?: string;
}

export interface DiscussionDTO {
  discussion_group_id: string;
  conversation_id?: string;
  restricted?: boolean;
  messages?: MessageDTO[];
  pending_mentions?: MentionDTO[];
  decisions?: DecisionDTO[];
  blockers?: BlockerDTO[];
  discussion_digest_ref?: string;
}

export interface MessageDTO {
  message_id: string;
  discussion_group_id: string;
  kind?: string;
  author_id: string;
  author_display_name?: string;
  author_type?: string;
  body: string;
  mentions?: string[];
  reply_to_message_id?: string;
  reply_preview?: string;
  metadata?: Record<string, string>;
  posted_at: string;
}

export interface MentionDTO {
  message_id: string;
  agent_id: string;
  created_at: string;
}

export interface InboxSummaryDTO {
  open_count: number;
  items?: InboxItemDTO[];
}

export interface InboxItemDTO {
  inbox_item_id: string;
  owner_id: string;
  message_id: string;
  item_type?: string;
  priority?: string;
  source_type?: string;
  source_id?: string;
  reason: string;
  status: string;
  available_actions?: InboxActionDTO[];
  created_at: string;
  closed_at?: string;
}

export interface InboxActionDTO {
  action_id: string;
  label: string;
  method: string;
  path: string;
}

export interface DecisionDTO {
  decision_id: string;
  summary: string;
  source_refs?: string[];
  recorded_at: string;
}

export interface BlockerDTO {
  blocker_id: string;
  task_id: string;
  raised_by: string;
  summary: string;
  raised_at: string;
}

export interface ProgressGraphDTO {
  nodes?: ProgressNodeDTO[];
  edges?: ProgressEdgeDTO[];
  current_focus?: string[];
}

export interface ProgressNodeDTO {
  id: string;
  event_id: string;
  event_type: string;
  label: string;
  status: string;
  details?: string;
  occurred_at: string;
  ref_type?: string;
  ref_id?: string;
}

export interface ProgressEdgeDTO {
  from: string;
  to: string;
  kind: string;
}

export interface CreateMissionRequest {
  title: string;
  final_goal: string;
  business_direction: string;
  source_personal_assistant_conversation_id?: string;
}

export interface CreateTaskRequest {
  assignee_id?: string;
  goal: string;
  worker_personal_goal?: string;
  scope?: string;
  priority?: string;
  due_at?: string;
  expected_deliverables?: ExpectedDeliverableRequest[];
}

export interface AssignTaskRequest {
  assignee_id: string;
  reason?: string;
}

export interface SubmitTaskResultRequest {
  agent_result_id: string;
  agent_id: string;
  run_id: string;
  summary: string;
  created_at: string;
  deliverables?: Array<{ id?: string; title?: string; uri?: string }>;
  evidence?: Array<{
    evidence_requirement_id?: string;
    kind?: string;
    artifact_uri?: string;
    external_url?: string;
  }>;
}

export interface QADecisionRequest {
  task_id: string;
  submission_id?: string;
  decision: string;
  message?: string;
  checked_criteria?: Array<{
    criterion_id?: string;
    status?: string;
    message?: string;
  }>;
  evidence_results?: Array<{
    evidence_id?: string;
    status?: string;
    message?: string;
  }>;
  missing_evidence?: string[];
  rejection_reasons?: string[];
}

export interface DiscussionMessageRequest {
  discussion_group_id: string;
  content: string;
  summary?: string;
  message_kind?: string;
  visibility?: string;
  related_task_id?: string;
  related_evidence_id?: string;
  source_refs?: string[];
  required_response?: boolean;
  due_at?: string;
}

export interface DiscussionReplyRequest {
  discussion_group_id: string;
  content: string;
  summary?: string;
  inbox_item_id?: string;
}

export interface MissionOpsViewDTO {
  mission_id: string;
  title: string;
  status: string;
  phase: string;
  current_focus?: OpsFocusDTO[];
  agent_lanes?: AgentLaneDTO[];
  runtime_sessions?: RuntimeSessionDTO[];
  action_cards?: ActionCardDTO[];
  evidence_gates?: EvidenceGateDTO[];
  approval_gates?: ApprovalGateDTO[];
  autonomy_policy: AutonomyPolicyDTO;
  updated_at: string;
  version: number;
}

export interface OpsFocusDTO {
  focus_id: string;
  kind: string;
  priority: string;
  title: string;
  summary?: string;
  mission_id: string;
  task_id?: string;
  agent_id?: string;
  source_type?: string;
  source_id?: string;
  created_at: string;
}

export interface AgentLaneDTO {
  agent_id: string;
  agent_profile_id?: string;
  agent_instance_id?: string;
  agent_kind?: string;
  display_name: string;
  mission_role?: string;
  status: string;
  active_task_ids?: string[];
  waiting_task_ids?: string[];
  inbox_open_count?: number;
  runtime_sessions?: RuntimeSessionDTO[];
  resource_bindings?: ResourceRefDTO[];
  next_actions?: CommandLinkDTO[];
}

export interface RuntimeSessionDTO {
  run_id: string;
  agent_id: string;
  task_id?: string;
  runtime_kind?: string;
  queue_id?: string;
  status: string;
  last_event?: string;
  error_summary?: string;
  last_heartbeat_at?: string;
  lease_expires_at?: string;
  is_alive?: boolean;
  is_stale?: boolean;
  lane_health?: string;
  started_at?: string;
  updated_at?: string;
}

export interface ResourceRefDTO {
  resource_type: string;
  resource_id: string;
  alias?: string;
  source?: string;
}

export interface ActionCardDTO {
  action_id: string;
  kind: string;
  priority: string;
  title: string;
  summary?: string;
  mission_id: string;
  task_id?: string;
  agent_id?: string;
  source_type?: string;
  source_id?: string;
  status: string;
  commands?: CommandLinkDTO[];
  created_at: string;
}

export interface CommandLinkDTO {
  action_id: string;
  label: string;
  method: string;
  path: string;
  required_permission?: string;
}

export interface EvidenceGateDTO {
  task_id: string;
  status: string;
  required_count: number;
  submitted_count: number;
  missing_requirement_ids?: string[];
  submitted_evidence_ids?: string[];
  last_submitted_at?: string;
}

export interface ApprovalGateDTO {
  approval_id: string;
  approval_type: string;
  status: string;
  priority: string;
  mission_id: string;
  task_id?: string;
  source_type?: string;
  source_id?: string;
  title: string;
  summary?: string;
  requested_by?: string;
  assigned_to?: string;
  evidence_refs?: string[];
  commands?: CommandLinkDTO[];
  created_at: string;
  decided_at?: string;
}

export interface AutonomyPolicyDTO {
  mode: string;
  auto_start_allowed: boolean;
  max_risk_without_approval: string;
  required_approvals?: string[];
}

export interface StartAgentRunRequest {
  agent_id: string;
  runtime_kind: string;
  prompt?: string;
  queue_id?: string;
}

export interface CancelAgentRunRequest {
  reason?: string;
}
