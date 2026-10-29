import type { components } from './api.generated'

export type ScheduledTaskStatsDTO = components['schemas']['ScheduledTaskStatsDTO']
export type ScheduledTaskStatus = 'enabled' | 'disabled' | 'archived'
export type ScheduleType = 'once' | 'cron'
export type TargetMode =
  | 'discussion_workflow'
  | 'agent_task'
  | 'tool_call'
  | 'memory_experience_distillation'
  | 'memory_world_consolidation'
  | 'memory_lifecycle_sweep'
  | 'todo_automation_scan'
  | 'responsibility_evaluation'
  | 'responsibility_reconcile'
export type RunTriggerType = 'due' | 'manual' | 'retry'
export type RunStatus = 'queued' | 'running' | 'completed' | 'failed' | 'skipped'
export type ScheduledRunReadFilter = 'all' | 'unread' | 'read'
export type ScheduledRunDeliveryStatus = 'not_requested' | 'pending' | 'sending' | 'retrying' | 'delivered' | 'failed' | 'dead_letter' | 'not_recovered'
export type MisfirePolicy = 'skip' | 'run_once'
export type ConcurrencyPolicy = 'skip_if_running' | 'allow_parallel'
export type ScheduledTaskSort = 'next_run_asc' | 'next_run_desc' | 'created_asc' | 'created_desc' | 'updated_asc' | 'updated_desc'

export interface ScheduledTaskSchedule {
  type: ScheduleType
  cron?: string
  run_at?: string
  timezone?: string
}

export interface ScheduledArtifactRequirement {
  id: string
  kind: 'code' | 'log' | 'report' | 'visual'
  path_pattern: string
  min_bytes?: number
}

export interface ScheduledAgentResultContract {
  artifacts?: ScheduledArtifactRequirement[]
}

export interface ScheduledTaskTarget {
  mode: TargetMode
  lead_agent_id?: string
  discussion_id?: string
  initial_member_ids?: string[]
  allow_member_additions?: boolean
  message?: string
  agent_id?: string
  task_goal?: string
  prompt?: string
  tool_id?: string
  tool_arguments?: Record<string, unknown>
  memory_topic?: string
  source_scope_kind?: string
  source_scope_id?: string
  target_scope_kind?: string
  target_scope_id?: string
  memory_record_limit?: number
  result_contract?: ScheduledAgentResultContract
}

export interface ScheduledTaskPolicy {
  misfire?: MisfirePolicy
  concurrency?: ConcurrencyPolicy
  max_retries?: number
  retry_delay?: string
  failure_mode?: string
}

export interface CreateScheduledTaskRequest {
  title: string
  description?: string
  schedule: ScheduledTaskSchedule
  target: ScheduledTaskTarget
  policy?: ScheduledTaskPolicy
  enabled?: boolean
}

export interface UpdateScheduledTaskRequest {
  title?: string
  description?: string
  schedule?: ScheduledTaskSchedule
  target?: ScheduledTaskTarget
  policy?: ScheduledTaskPolicy
  enabled?: boolean
}

export interface ScheduledTaskDTO {
  tenant_id?: string
  scheduled_task_id: string
  mission_id?: string
  agent_task_id?: string
  title: string
  description?: string
  schedule: ScheduledTaskSchedule
  target: ScheduledTaskTarget
  policy: ScheduledTaskPolicy
  status: ScheduledTaskStatus
  enabled: boolean
  next_run_at?: string
  created_by: string
  created_at: string
  updated_at: string
}

export interface ScheduledTaskSummaryDTO {
  scheduled_task_id: string
  title: string
  status: ScheduledTaskStatus
  enabled: boolean
  schedule_type: ScheduleType
  target_mode: TargetMode
  target_summary: string
  executor_agent_id?: string
  next_run_at?: string
  schedule_occurrences?: string[]
  last_run_status?: RunStatus
  failure_reason?: string
}

export interface ScheduledTaskPageDTO {
  tasks: ScheduledTaskSummaryDTO[]
  total: number
  limit?: number
  offset?: number
}

export interface RunScheduledTaskNowRequest {
  force?: boolean
}

export interface ScheduledRunDTO {
  scheduled_run_id: string
  scheduled_task_id: string
  trigger_type: RunTriggerType
  status: RunStatus
  target_mode: TargetMode
  command_id?: string
  discussion_id?: string
  agent_id?: string
  mission_id?: string
  task_id?: string
  responsibility_id?: string
  attempt: number
  started_at?: string
  completed_at?: string
  failure_reason?: string
  delivery_status?: ScheduledRunDeliveryStatus
  result?: ScheduledRunResult
  todo_automation_scan?: {
    scanned_count: number
    eligible_count: number
    claimed_count: number
    discussion_sync_count: number
    planned_count: number
    recovered_count: number
    skipped_count: number
    failed_count: number
  }
  responsibility_evaluation?: ResponsibilityEvaluationResultDTO
  responsibility_evaluation_batch?: ResponsibilityEvaluationBatchResultDTO
  responsibility_reconcile?: ResponsibilityReconcileResultDTO
  created_at: string
}

export interface ResponsibilityEvaluationResultDTO {
  portfolio_id?: string
  portfolio_binding_id?: string
  portfolio_version_id?: string
  source_revision?: string
  source_digest?: string
  agent_profile_id?: string
  evaluated: boolean
  actionable: boolean
  decision_kind: 'no_action' | 'link_existing_mission' | 'materialize_action'
  decision_source_ref?: string
  reason?: string
  candidate_count: number
  actionable_count: number
  waiting_count: number
  review_count: number
  escalated_count: number
  paused_count: number
  invalid_count: number
  selected_mission_id?: string
  selected_task_id?: string
  selected_responsibility_id?: string
  selected_assignment_state?: string
  selected_assignment_version?: number
  selected_assignment_priority?: number
  selected_assignment_source_kind?: string
  selected_assignment_source_id?: string
  selected_assignment_source_step_id?: string
  selected_due_at?: string
  linked_existing_mission: boolean
  mission_task_materialized: boolean
}

export interface ResponsibilityEvaluationBatchResultDTO {
  scope_id: string
  discovered_count: number
  evaluated_count: number
  no_action_count: number
  linked_existing_mission_count: number
  materialized_count: number
  skipped_count: number
  failed_count: number
  candidate_count: number
  actionable_count: number
  waiting_count: number
  review_count: number
  escalated_count: number
  paused_count: number
  invalid_count: number
}

export type ResponsibilityEvaluationItemStatus = 'completed' | 'failed'

export interface ResponsibilityEvaluationItemDTO {
  item_id: string
  scheduled_task_id: string
  scheduled_run_id: string
  portfolio_id: string
  portfolio_binding_id: string
  portfolio_version_id: string
  agent_profile_id: string
  status: ResponsibilityEvaluationItemStatus
  attempt: number
  evaluation?: ResponsibilityEvaluationResultDTO
  command_id?: string
  mission_id?: string
  task_id?: string
  responsibility_id?: string
  failure_code?: string
  failure_message?: string
  started_at?: string
  completed_at?: string
  created_at: string
  updated_at: string
}

export interface ResponsibilityEvaluationItemPageDTO {
  items: ResponsibilityEvaluationItemDTO[]
  next_cursor: string
  has_more: boolean
  limit: number
}

export interface ResponsibilityReconcileResultDTO {
  candidates: number
  dependency_blocked: number
  qa_blocked: number
  qa_escalated: number
  capacity_blocked: number
  policy_blocked: number
  budget_blocked: number
  risk_blocked: number
  blockers_rechecked: number
  blockers_resolved: number
  blockers_escalated: number
  alerts_opened: number
  alerts_escalated: number
  alerts_resolved: number
  heartbeats: number
  timed_out: number
  claimed: number
  dispatched: number
  synchronized: number
  recovered_outcomes: number
  cancelled: number
  failed: number
}

export interface ScheduledRunArtifact {
  id: string
  kind: string
  file_name: string
  size_bytes?: number
  evidence_id?: string
  previewable: boolean
  content_available: boolean
  content_availability_checked: boolean
}

export interface ScheduledRunResult {
  summary?: string
  artifacts?: ScheduledRunArtifact[]
}

export interface ScheduledRunPageDTO {
  runs: ScheduledRunDTO[]
  total: number
  limit?: number
  offset?: number
}

export interface ScheduledRunInboxItemDTO {
  run: ScheduledRunDTO
  task_title: string
  is_read: boolean
  read_at?: string
  attention_required: boolean
}

export interface ScheduledRunInboxPageDTO {
  items: ScheduledRunInboxItemDTO[]
  total: number
  unread_total: number
  attention_total: number
  limit: number
  offset: number
}

export interface ScheduledRunReadStateDTO {
  scheduled_run_id: string
  is_read: boolean
  read_at?: string
}
