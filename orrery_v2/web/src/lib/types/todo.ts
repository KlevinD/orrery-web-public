export type TodoStatus =
  | 'inbox'
  | 'planned'
  | 'in_progress'
  | 'blocked'
  | 'review'
  | 'done'
  | 'canceled'

export type TodoPriority = 'low' | 'medium' | 'high' | 'urgent'
export type AssigneeKind = 'human' | 'agent'
export type TodoWorkKind = 'general' | 'development'
export type TodoAutomationMode = 'manual' | 'suggest' | 'managed'
export type TodoAutomationStatus =
  | 'discussing'
  | 'not_ready'
  | 'planning_failed'
  | 'pending_approval'
  | 'approved'
  | 'rejected'
  | 'stale'
  | 'provisioning_workspace'
  | 'executing'
  | 'waiting_runtime_approval'
  | 'executed'
  | 'execution_failed'

export interface TodoAutomationStateDTO {
  proposal_id: string
  approval_id?: string
  todo_id: string
  todo_version: number
  status: TodoAutomationStatus
  conversation_id?: string
  proposal?: string
  plan: TodoExecutionPlanDTO
  plan_digest?: string
  approved_plan_digest?: string
  failure_reason?: string
  requested_by?: string
  approver_id?: string
  decision_by?: string
  execution_requested_by?: string
  decision_message?: string
  created_at: string
  updated_at: string
  next_check_at?: string
  decided_at?: string
  executed_at?: string
}

export interface TodoExecutionPlanStepDTO {
  step_id: string
  title: string
  description?: string
  assignee_agent_id?: string
  acceptance_criteria?: string[]
  depends_on?: string[]
}

export interface TodoExecutionPlanDTO {
  version: number
  summary: string
  steps: TodoExecutionPlanStepDTO[]
  risks?: string[]
  required_approvals?: string[]
}

export interface TodoDiscussionBindingDTO {
  todo_id: string
  conversation_id: string
  managed_source: string
  created_at: string
  updated_at: string
  version: number
}

export interface TodoMissionBindingDTO {
  binding_id: string
  todo_id: string
  mission_id?: string
  conversation_id: string
  development_project_id?: string
  proposal_id: string
  todo_version: number
  execution_attempt: number
  plan_digest: string
  mission_task_ids: string[]
  status: string
  pause_reason?: string
  failure_reason?: string
  created_by: string
  created_at: string
  updated_at: string
  version: number
}

export interface TodoExecutionProgressDTO {
  todo_id: string
  mission_id: string
  execution_attempt: number
  mission_status: string
  current_phase: string
  delivery_rate: number
  total_task_count: number
  active_task_count: number
  done_task_count: number
  blocked_task_count: number
  pending_approval_count: number
  current_agent_ids: string[]
  last_activity_at: string
  code_submission_status?: string
  pull_request_status?: string
  merge_status?: string
  mission_projection_version: number
  can_open_control: boolean
  can_open_discussion: boolean
  conversation_id?: string
}

export interface TodoExecutionAttemptDTO {
  todo_id: string
  execution_attempt: number
  mission_id?: string
  mission_task_ids: string[]
  proposal_id: string
  todo_version: number
  plan_digest: string
  status: string
  failure_reason?: string
  started_at: string
  completed_at?: string
  updated_at: string
  progress?: TodoExecutionProgressDTO
}

export interface TodoExecutionSummaryDTO {
  current?: TodoMissionBindingDTO
  current_progress?: TodoExecutionProgressDTO
  attempts: TodoExecutionAttemptDTO[]
}

export interface TodoAttachmentDTO {
  attachment_id: string
  file_name: string
  content_type: string
  size_bytes: number
  download_url: string
}

export interface TodoItemDTO {
	attachments?: TodoAttachmentDTO[]
	feedback?: { body: string; attachments?: TodoAttachmentDTO[]; author_id: string; created_at: string }
  source?: { conversation_id: string; result?: string; result_by?: string; delivery_status: 'pending' | 'delivered'; can_open?: boolean }
  tenant_id?: string
  organization_id?: string
  department_id?: string
  project_id?: string
  work_kind: TodoWorkKind
  automation_mode: TodoAutomationMode
  development_project_id?: string
  source_idea_id?: string
  todo_id: string
  issue_number?: string
  title: string
  description?: string
  status: TodoStatus
  priority: TodoPriority
  proposer_name?: string
  tags?: string[]
  assignee_id?: string
  assignee_name?: string
  assignee_kind?: AssigneeKind
  reviewer_id?: string
  reviewer_name?: string
  acceptance_criteria?: string
  blocked_reason?: string
  created_by: string
  created_by_kind?: 'user' | 'human' | 'agent' | 'system'
  created_at: string
  updated_at: string
  due_at?: string
  completed_at?: string
  version: number
  automation?: TodoAutomationStateDTO
  discussion_binding?: TodoDiscussionBindingDTO
  execution?: TodoExecutionSummaryDTO
}

export interface TodoPageDTO {
  todos: TodoItemDTO[]
  total: number
  limit?: number
  offset?: number
  has_more?: boolean
}

export type IssueKind = 'todo' | 'idea'

export interface IssueCommentDTO {
  tenant_id?: string
  comment_id: string
  issue_kind: IssueKind
  issue_id: string
  body: string
  author_id: string
  author_name?: string
  created_at: string
  updated_at: string
}

export interface IssueCommentPageDTO {
  comments: IssueCommentDTO[]
  total: number
  limit?: number
  offset?: number
  has_more?: boolean
}

export interface CreateIssueCommentRequest {
  body: string
}

export interface CreateTodoRequest {
  source_conversation_id?: string
  title: string
  description?: string
  status?: TodoStatus
  priority?: TodoPriority
  organization_id?: string
  department_id?: string
  project_id?: string
  work_kind?: TodoWorkKind
  automation_mode?: TodoAutomationMode
  development_project_id?: string
  source_idea_id?: string
  assignee_id?: string
  assignee_name?: string
  assignee_kind?: AssigneeKind
  reviewer_id?: string
  reviewer_name?: string
  due_at?: string
  acceptance_criteria?: string
  tags?: string[]
}

export interface UpdateTodoRequest {
  title?: string
  description?: string
  priority?: TodoPriority
  organization_id?: string
  department_id?: string
  project_id?: string
  work_kind?: TodoWorkKind
  automation_mode?: TodoAutomationMode
  development_project_id?: string
  assignee_id?: string
  assignee_name?: string
  assignee_kind?: AssigneeKind
  reviewer_id?: string
  reviewer_name?: string
  due_at?: string
  clear_due_at?: boolean
  acceptance_criteria?: string
  tags?: string[]
  version?: number
}

export interface ClaimTodoRequest {
  assignee_id?: string
  assignee_name?: string
  assignee_kind?: AssigneeKind
  version?: number
}

export interface MoveTodoRequest {
	result_attachment_ids?: string[]
  result?: string
  status: TodoStatus
  blocked_reason?: string
  version?: number
}

export interface SuggestDraftRequest {
  kind?: 'todo' | 'idea'
  title?: string
  content?: string
  current_tags?: string[]
}

export interface DraftSuggestionDTO {
  title: string
  tags: string[]
}

export interface WorkHubSummaryDTO {
  my_open_tasks: number
  due_today: number
  overdue: number
  blocked: number
  awaiting_task_review: number
  tasks_inbox: number
  tasks_planned: number
  tasks_in_progress: number
  ideas_awaiting_review: number
  ideas_submitted: number
  ideas_under_review: number
  ideas_in_experiment: number
  ideas_converted: number
  unassigned_team_tasks: number
}
