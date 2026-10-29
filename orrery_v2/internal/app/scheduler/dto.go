package scheduler

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
	todoapp "orrery_v2/internal/app/todo"
)

type ViewerKind string

const (
	ViewerKindUser   ViewerKind = "user"
	ViewerKindHuman  ViewerKind = "human"
	ViewerKindAgent  ViewerKind = "agent"
	ViewerKindSystem ViewerKind = "system"
)

type ViewerRef struct {
	TenantID        string
	ViewerID        string
	ViewerKind      ViewerKind
	DisplayName     string
	ProfileID       string
	PermissionScope []string
}

type ScheduleType string

const (
	ScheduleTypeOnce ScheduleType = "once"
	ScheduleTypeCron ScheduleType = "cron"
)

type TargetMode string

const (
	TargetModeDiscussionWorkflow           TargetMode = "discussion_workflow"
	TargetModeAgentTask                    TargetMode = "agent_task"
	TargetModeToolCall                     TargetMode = "tool_call"
	TargetModeMemoryExperienceDistillation TargetMode = "memory_experience_distillation"
	TargetModeMemoryWorldConsolidation     TargetMode = "memory_world_consolidation"
	TargetModeMemoryLifecycleSweep         TargetMode = "memory_lifecycle_sweep"
	TargetModeTodoAutomationScan           TargetMode = "todo_automation_scan"
	TargetModeResponsibilityEvaluation     TargetMode = "responsibility_evaluation"
	TargetModeResponsibilityReconcile      TargetMode = "responsibility_reconcile"
	TargetModeOntologyStateRefresh         TargetMode = "ontology_state_refresh"
)

const (
	ResponsibilityEvaluationPeoplePortfolioScope  = "people_responsibility_portfolio"
	ResponsibilityEvaluationPeoplePortfoliosScope = "people_responsibility_portfolios"
)

type MisfirePolicy string

const (
	MisfireSkip    MisfirePolicy = "skip"
	MisfireRunOnce MisfirePolicy = "run_once"
)

type ConcurrencyPolicy string

const (
	ConcurrencySkipIfRunning ConcurrencyPolicy = "skip_if_running"
	ConcurrencyAllowParallel ConcurrencyPolicy = "allow_parallel"
)

type TaskStatus string

const (
	TaskStatusEnabled  TaskStatus = "enabled"
	TaskStatusDisabled TaskStatus = "disabled"
	TaskStatusArchived TaskStatus = "archived"
)

type RunTriggerType string

const (
	RunTriggerDue    RunTriggerType = "due"
	RunTriggerManual RunTriggerType = "manual"
	RunTriggerRetry  RunTriggerType = "retry"
)

type RunStatus string

const (
	RunStatusQueued    RunStatus = "queued"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
	RunStatusSkipped   RunStatus = "skipped"
)

type ResponsibilityEvaluationDecisionKind string

const (
	ResponsibilityEvaluationNoAction            ResponsibilityEvaluationDecisionKind = "no_action"
	ResponsibilityEvaluationLinkExistingMission ResponsibilityEvaluationDecisionKind = "link_existing_mission"
	ResponsibilityEvaluationMaterializeAction   ResponsibilityEvaluationDecisionKind = "materialize_action"
)

type TaskSort string

const (
	SortNextRunAsc  TaskSort = "next_run_asc"
	SortNextRunDesc TaskSort = "next_run_desc"
	SortCreatedAsc  TaskSort = "created_asc"
	SortCreatedDesc TaskSort = "created_desc"
	SortUpdatedAsc  TaskSort = "updated_asc"
	SortUpdatedDesc TaskSort = "updated_desc"
)

type Schedule struct {
	Type     ScheduleType `json:"type"`
	Cron     string       `json:"cron,omitempty"`
	RunAt    time.Time    `json:"run_at,omitempty"`
	Timezone string       `json:"timezone,omitempty"`
}

type Target struct {
	Mode                 TargetMode          `json:"mode"`
	LeadAgentID          string              `json:"lead_agent_id,omitempty"`
	ConversationID       string              `json:"discussion_id,omitempty"`
	InitialMemberIDs     []string            `json:"initial_member_ids,omitempty"`
	AllowMemberAdditions *bool               `json:"allow_member_additions,omitempty"`
	Message              string              `json:"message,omitempty"`
	AgentID              string              `json:"agent_id,omitempty"`
	ApprovalAssigneeID   string              `json:"approval_assignee_id,omitempty"`
	TaskGoal             string              `json:"task_goal,omitempty"`
	Prompt               string              `json:"prompt,omitempty"`
	ToolID               string              `json:"tool_id,omitempty"`
	ToolArguments        map[string]any      `json:"tool_arguments,omitempty"`
	MemoryTopic          string              `json:"memory_topic,omitempty"`
	SourceScopeKind      string              `json:"source_scope_kind,omitempty"`
	SourceScopeID        string              `json:"source_scope_id,omitempty"`
	TargetScopeKind      string              `json:"target_scope_kind,omitempty"`
	TargetScopeID        string              `json:"target_scope_id,omitempty"`
	MemoryRecordLimit    int                 `json:"memory_record_limit,omitempty"`
	ResultContract       AgentResultContract `json:"result_contract,omitempty"`
}

type AgentResultContract struct {
	Artifacts []ArtifactRequirement `json:"artifacts,omitempty"`
}

type ArtifactRequirement struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	PathPattern string `json:"path_pattern"`
	MinBytes    int64  `json:"min_bytes,omitempty"`
}

type Policy struct {
	Misfire     MisfirePolicy     `json:"misfire,omitempty"`
	Concurrency ConcurrencyPolicy `json:"concurrency,omitempty"`
	MaxRetries  int               `json:"max_retries,omitempty"`
	RetryDelay  string            `json:"retry_delay,omitempty"`
	FailureMode string            `json:"failure_mode,omitempty"`
}

type ExecutionPolicy struct {
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	LLMProfileID   string   `json:"llm_profile_id,omitempty"`
	SourceModel    string   `json:"source_model,omitempty"`
	FallbackModels []string `json:"fallback_models,omitempty"`
	Thinking       string   `json:"thinking,omitempty"`
	LightContext   bool     `json:"light_context,omitempty"`
	AllowedToolIDs []string `json:"allowed_tool_ids,omitempty"`
}

type DeliveryPolicy struct {
	Mode              string `json:"mode,omitempty"`
	Channel           string `json:"channel,omitempty"`
	RecipientID       string `json:"recipient_id,omitempty"`
	AccountID         string `json:"account_id,omitempty"`
	ContentArtifactID string `json:"content_artifact_id,omitempty"`
	Format            string `json:"format,omitempty"`
	BestEffort        bool   `json:"best_effort,omitempty"`
}

type CreateTaskRequest struct {
	Title          string          `json:"title"`
	Description    string          `json:"description,omitempty"`
	Schedule       Schedule        `json:"schedule"`
	Target         Target          `json:"target"`
	Policy         Policy          `json:"policy,omitempty"`
	Execution      ExecutionPolicy `json:"execution,omitempty"`
	Delivery       DeliveryPolicy  `json:"delivery,omitempty"`
	Enabled        *bool           `json:"enabled,omitempty"`
	IdempotencyKey string          `json:"idempotency_key"`
}

type UpdateTaskRequest struct {
	ScheduledTaskID string           `json:"scheduled_task_id"`
	Title           *string          `json:"title,omitempty"`
	Description     *string          `json:"description,omitempty"`
	Schedule        *Schedule        `json:"schedule,omitempty"`
	Target          *Target          `json:"target,omitempty"`
	Policy          *Policy          `json:"policy,omitempty"`
	Execution       *ExecutionPolicy `json:"execution,omitempty"`
	Delivery        *DeliveryPolicy  `json:"delivery,omitempty"`
	Enabled         *bool            `json:"enabled,omitempty"`
	IdempotencyKey  string           `json:"idempotency_key,omitempty"`
}

type ListTasksQuery struct {
	TenantID           string     `json:"-"`
	IncludeArchived    bool       `json:"include_archived,omitempty"`
	ExpandOccurrences  bool       `json:"expand_occurrences,omitempty"`
	OccurrenceTimezone string     `json:"occurrence_timezone,omitempty"`
	Limit              int        `json:"limit,omitempty"`
	Offset             int        `json:"offset,omitempty"`
	Q                  string     `json:"q,omitempty"`
	Status             TaskStatus `json:"status,omitempty"`
	From               time.Time  `json:"from,omitempty"`
	To                 time.Time  `json:"to,omitempty"`
	Sort               TaskSort   `json:"sort,omitempty"`
}

type ListRunsQuery struct {
	TenantID        string    `json:"-"`
	ScheduledTaskID string    `json:"scheduled_task_id"`
	ScheduledRunID  string    `json:"scheduled_run_id,omitempty"`
	Status          RunStatus `json:"status,omitempty"`
	CreatedFrom     time.Time `json:"created_from,omitempty"`
	CreatedBefore   time.Time `json:"created_before,omitempty"`
	ActiveOnly      bool      `json:"-"`
	Limit           int       `json:"limit,omitempty"`
	Offset          int       `json:"offset,omitempty"`
}

type ListResponsibilityEvaluationItemsQuery struct {
	TenantID        string `json:"-"`
	ScheduledTaskID string `json:"scheduled_task_id"`
	ScheduledRunID  string `json:"scheduled_run_id"`
	Status          string `json:"status,omitempty"`
	Cursor          string `json:"cursor,omitempty"`
	Limit           int    `json:"limit,omitempty"`
}

type CancelTaskRequest struct {
	ScheduledTaskID string `json:"scheduled_task_id"`
}

type RunNowRequest struct {
	ScheduledTaskID string `json:"scheduled_task_id"`
	Force           bool   `json:"force,omitempty"`
	IdempotencyKey  string `json:"idempotency_key,omitempty"`
}

type ScheduledTask struct {
	TenantID         string          `json:"tenant_id"`
	ScheduledTaskID  string          `json:"scheduled_task_id"`
	MissionID        string          `json:"mission_id,omitempty"`
	AgentTaskID      string          `json:"agent_task_id"`
	Title            string          `json:"title"`
	Description      string          `json:"description,omitempty"`
	Schedule         Schedule        `json:"schedule"`
	Target           Target          `json:"target"`
	Policy           Policy          `json:"policy"`
	Execution        ExecutionPolicy `json:"execution,omitempty"`
	Delivery         DeliveryPolicy  `json:"delivery,omitempty"`
	Status           TaskStatus      `json:"status"`
	Enabled          bool            `json:"enabled"`
	NextRunAt        time.Time       `json:"next_run_at,omitempty"`
	CreatedBy        string          `json:"created_by"`
	CreatedByKind    ViewerKind      `json:"created_by_kind,omitempty"`
	CreatedByName    string          `json:"created_by_name,omitempty"`
	CreatorProfileID string          `json:"creator_profile_id"`
	IdempotencyKey   string          `json:"-"`
	IdempotencyHash  string          `json:"-"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type ScheduledTaskSummary struct {
	ScheduledTaskID     string       `json:"scheduled_task_id"`
	Title               string       `json:"title"`
	Status              TaskStatus   `json:"status"`
	Enabled             bool         `json:"enabled"`
	ScheduleType        ScheduleType `json:"schedule_type"`
	TargetMode          TargetMode   `json:"target_mode"`
	TargetSummary       string       `json:"target_summary"`
	ExecutorAgentID     string       `json:"executor_agent_id,omitempty"`
	NextRunAt           time.Time    `json:"next_run_at,omitempty"`
	ScheduleOccurrences []time.Time  `json:"schedule_occurrences,omitempty"`
	LastRunStatus       RunStatus    `json:"last_run_status,omitempty"`
	FailureReason       string       `json:"failure_reason,omitempty"`
}

type ScheduledTaskPage struct {
	Tasks  []ScheduledTaskSummary `json:"tasks"`
	Total  int                    `json:"total"`
	Limit  int                    `json:"limit,omitempty"`
	Offset int                    `json:"offset,omitempty"`
}

type ScheduledRun struct {
	ScheduledRunID                string                               `json:"scheduled_run_id"`
	ScheduledTaskID               string                               `json:"scheduled_task_id"`
	TriggerType                   RunTriggerType                       `json:"trigger_type"`
	Status                        RunStatus                            `json:"status"`
	TargetMode                    TargetMode                           `json:"target_mode"`
	ConfigurationSnapshotVersion  int                                  `json:"configuration_snapshot_version,omitempty"`
	ConfigurationSnapshot         *ScheduledTask                       `json:"configuration_snapshot,omitempty"`
	TenantID                      string                               `json:"tenant_id,omitempty"`
	ScheduledTaskTitle            string                               `json:"scheduled_task_title,omitempty"`
	ResultContract                AgentResultContract                  `json:"result_contract,omitempty"`
	Delivery                      DeliveryPolicy                       `json:"delivery,omitempty"`
	CommandID                     string                               `json:"command_id,omitempty"`
	ConversationID                string                               `json:"discussion_id,omitempty"`
	AgentID                       string                               `json:"agent_id,omitempty"`
	RuntimeRunID                  string                               `json:"runtime_run_id,omitempty"`
	MissionID                     string                               `json:"mission_id,omitempty"`
	TaskID                        string                               `json:"task_id,omitempty"`
	ResponsibilityID              string                               `json:"responsibility_id,omitempty"`
	ExecutionEnvelope             *agentcore.ExecutionEnvelope         `json:"execution_envelope,omitempty"`
	Attempt                       int                                  `json:"attempt"`
	StartedAt                     time.Time                            `json:"started_at,omitempty"`
	CompletedAt                   time.Time                            `json:"completed_at,omitempty"`
	FailureReason                 string                               `json:"failure_reason,omitempty"`
	Result                        RunResult                            `json:"result,omitempty"`
	TraceRef                      string                               `json:"-"`
	TraceVersion                  int                                  `json:"-"`
	TraceAvailable                bool                                 `json:"trace_available"`
	TraceStatus                   string                               `json:"trace_status,omitempty"`
	DeliveryStatus                string                               `json:"delivery_status,omitempty"`
	DeliveryError                 string                               `json:"delivery_error,omitempty"`
	ExternalMessageID             string                               `json:"external_message_id,omitempty"`
	TodoAutomationScan            *todoapp.TodoAutomationScanResult    `json:"todo_automation_scan,omitempty"`
	ResponsibilityEvaluation      *ResponsibilityEvaluationResult      `json:"responsibility_evaluation,omitempty"`
	ResponsibilityEvaluationBatch *ResponsibilityEvaluationBatchResult `json:"responsibility_evaluation_batch,omitempty"`
	ResponsibilityReconcile       *ResponsibilityReconcileResult       `json:"responsibility_reconcile,omitempty"`
	AggregatedOccurrences         int                                  `json:"aggregated_occurrences,omitempty"`
	LastOccurrenceAt              time.Time                            `json:"last_occurrence_at,omitempty"`
	AggregatedIntoRunID           string                               `json:"aggregated_into_run_id,omitempty"`
	RequestedBy                   string                               `json:"-"`
	IdempotencyKey                string                               `json:"-"`
	IdempotencyHash               string                               `json:"-"`
	CreatedAt                     time.Time                            `json:"created_at"`
}

type ResponsibilityEvaluationBatchResult struct {
	ScopeID                    string `json:"scope_id"`
	DiscoveredCount            int    `json:"discovered_count"`
	EvaluatedCount             int    `json:"evaluated_count"`
	NoActionCount              int    `json:"no_action_count"`
	LinkedExistingMissionCount int    `json:"linked_existing_mission_count"`
	MaterializedCount          int    `json:"materialized_count"`
	SkippedCount               int    `json:"skipped_count"`
	FailedCount                int    `json:"failed_count"`
	CandidateCount             int    `json:"candidate_count"`
	ActionableCount            int    `json:"actionable_count"`
	WaitingCount               int    `json:"waiting_count"`
	ReviewCount                int    `json:"review_count"`
	EscalatedCount             int    `json:"escalated_count"`
	PausedCount                int    `json:"paused_count"`
	InvalidCount               int    `json:"invalid_count"`
}

const (
	ResponsibilityEvaluationItemCompleted = "completed"
	ResponsibilityEvaluationItemFailed    = "failed"
)

type ResponsibilityEvaluationItem struct {
	ItemID             string                          `json:"item_id"`
	TenantID           string                          `json:"-"`
	ScheduledTaskID    string                          `json:"scheduled_task_id"`
	ScheduledRunID     string                          `json:"scheduled_run_id"`
	PortfolioID        string                          `json:"portfolio_id,omitempty"`
	PortfolioBindingID string                          `json:"portfolio_binding_id"`
	PortfolioVersionID string                          `json:"portfolio_version_id"`
	AgentProfileID     string                          `json:"agent_profile_id"`
	Status             string                          `json:"status"`
	Attempt            int                             `json:"attempt"`
	Evaluation         *ResponsibilityEvaluationResult `json:"evaluation,omitempty"`
	CommandID          string                          `json:"command_id,omitempty"`
	MissionID          string                          `json:"mission_id,omitempty"`
	TaskID             string                          `json:"task_id,omitempty"`
	ResponsibilityID   string                          `json:"responsibility_id,omitempty"`
	FailureCode        string                          `json:"failure_code,omitempty"`
	FailureMessage     string                          `json:"failure_message,omitempty"`
	StartedAt          time.Time                       `json:"started_at,omitempty"`
	CompletedAt        time.Time                       `json:"completed_at,omitempty"`
	CreatedAt          time.Time                       `json:"created_at"`
	UpdatedAt          time.Time                       `json:"updated_at"`
}

type ResponsibilityEvaluationItemPage struct {
	Items      []ResponsibilityEvaluationItem `json:"items"`
	NextCursor string                         `json:"next_cursor"`
	HasMore    bool                           `json:"has_more"`
	Limit      int                            `json:"limit"`
}

type ResponsibilityEvaluationResult struct {
	PortfolioID                    string                               `json:"portfolio_id,omitempty"`
	PortfolioBindingID             string                               `json:"portfolio_binding_id,omitempty"`
	PortfolioVersionID             string                               `json:"portfolio_version_id,omitempty"`
	SourceRevision                 string                               `json:"source_revision,omitempty"`
	SourceDigest                   string                               `json:"source_digest,omitempty"`
	AgentProfileID                 string                               `json:"agent_profile_id,omitempty"`
	Evaluated                      bool                                 `json:"evaluated"`
	Actionable                     bool                                 `json:"actionable"`
	DecisionKind                   ResponsibilityEvaluationDecisionKind `json:"decision_kind"`
	DecisionSourceRef              string                               `json:"decision_source_ref,omitempty"`
	Reason                         string                               `json:"reason,omitempty"`
	CandidateCount                 int                                  `json:"candidate_count"`
	ActionableCount                int                                  `json:"actionable_count"`
	WaitingCount                   int                                  `json:"waiting_count"`
	ReviewCount                    int                                  `json:"review_count"`
	EscalatedCount                 int                                  `json:"escalated_count"`
	PausedCount                    int                                  `json:"paused_count"`
	InvalidCount                   int                                  `json:"invalid_count"`
	SelectedMissionID              string                               `json:"selected_mission_id,omitempty"`
	SelectedTaskID                 string                               `json:"selected_task_id,omitempty"`
	SelectedResponsibilityID       string                               `json:"selected_responsibility_id,omitempty"`
	SelectedAssignmentState        string                               `json:"selected_assignment_state,omitempty"`
	SelectedAssignmentVersion      int64                                `json:"selected_assignment_version,omitempty"`
	SelectedAssignmentPriority     int                                  `json:"selected_assignment_priority,omitempty"`
	SelectedAssignmentSourceKind   string                               `json:"selected_assignment_source_kind,omitempty"`
	SelectedAssignmentSourceID     string                               `json:"selected_assignment_source_id,omitempty"`
	SelectedAssignmentSourceStepID string                               `json:"selected_assignment_source_step_id,omitempty"`
	SelectedDueAt                  *time.Time                           `json:"selected_due_at,omitempty"`
	LinkedExistingMission          bool                                 `json:"linked_existing_mission"`
	MissionTaskMaterialized        bool                                 `json:"mission_task_materialized"`
}

type ResponsibilityReconcileResult struct {
	PeopleSourceAttempted     bool `json:"people_source_attempted,omitempty"`
	PeopleSourceThrottled     bool `json:"people_source_throttled,omitempty"`
	PeopleBindingsScanned     int  `json:"people_bindings_scanned,omitempty"`
	PeopleBindingsChanged     int  `json:"people_bindings_changed,omitempty"`
	PeoplePortfoliosChanged   int  `json:"people_portfolios_changed,omitempty"`
	PeopleRoleProfilesChanged int  `json:"people_role_profiles_changed,omitempty"`
	PeoplePulsesEnsured       int  `json:"people_pulses_ensured,omitempty"`
	PeoplePulsesDisabled      int  `json:"people_pulses_disabled,omitempty"`
	PeopleGapsOpen            int  `json:"people_gaps_open,omitempty"`
	PeopleGapsResolved        int  `json:"people_gaps_resolved,omitempty"`
	PeopleFailed              int  `json:"people_failed,omitempty"`
	Candidates                int  `json:"candidates"`
	DependencyBlocked         int  `json:"dependency_blocked"`
	QABlocked                 int  `json:"qa_blocked"`
	QAEscalated               int  `json:"qa_escalated"`
	CapacityBlocked           int  `json:"capacity_blocked"`
	PolicyBlocked             int  `json:"policy_blocked"`
	BudgetBlocked             int  `json:"budget_blocked"`
	RiskBlocked               int  `json:"risk_blocked"`
	BlockersRechecked         int  `json:"blockers_rechecked"`
	BlockersResolved          int  `json:"blockers_resolved"`
	BlockersEscalated         int  `json:"blockers_escalated"`
	AlertsOpened              int  `json:"alerts_opened"`
	AlertsEscalated           int  `json:"alerts_escalated"`
	AlertsResolved            int  `json:"alerts_resolved"`
	Heartbeats                int  `json:"heartbeats"`
	TimedOut                  int  `json:"timed_out"`
	Claimed                   int  `json:"claimed"`
	Dispatched                int  `json:"dispatched"`
	Synchronized              int  `json:"synchronized"`
	RecoveredOutcomes         int  `json:"recovered_outcomes"`
	Cancelled                 int  `json:"cancelled"`
	Failed                    int  `json:"failed"`
}

type RunResult struct {
	Summary     string        `json:"summary,omitempty"`
	Artifacts   []RunArtifact `json:"artifacts,omitempty"`
	AgentReport *AgentReport  `json:"agent_report,omitempty"`
}

// AgentReport is committed with the executing Agent's result. Summary may
// contain an internal receipt; it is never sufficient proof of authorship.
type AgentReport struct {
	AgentID      string `json:"agent_id"`
	RuntimeRunID string `json:"runtime_run_id"`
	Body         string `json:"body"`
}

type RunArtifact struct {
	ID                         string `json:"id"`
	Kind                       string `json:"kind"`
	URI                        string `json:"uri"`
	FileName                   string `json:"file_name,omitempty"`
	ContentType                string `json:"content_type,omitempty"`
	SizeBytes                  int64  `json:"size_bytes"`
	SHA256                     string `json:"sha256,omitempty"`
	EvidenceID                 string `json:"evidence_id,omitempty"`
	ContentAvailable           bool   `json:"-"`
	ContentAvailabilityChecked bool   `json:"-"`
}

type ScheduledRunPage struct {
	Runs   []ScheduledRun `json:"runs"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit,omitempty"`
	Offset int            `json:"offset,omitempty"`
}
