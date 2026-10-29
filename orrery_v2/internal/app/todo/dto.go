package todo

import (
	"time"

	"orrery_v2/internal/app/operations"
)

type ViewerKind string

const (
	ViewerKindUser   ViewerKind = "user"
	ViewerKindHuman  ViewerKind = "human"
	ViewerKindAgent  ViewerKind = "agent"
	ViewerKindSystem ViewerKind = "system"
)

type ViewerRef struct {
	TenantID               string
	OrganizationID         string
	DepartmentID           string
	TimeZone               string
	ViewerID               string
	ViewerKind             ViewerKind
	AuthorizationSubjectID string
	DisplayName            string
	PermissionScope        []string
}

type TodoStatus string

const (
	TodoStatusInbox      TodoStatus = "inbox"
	TodoStatusPlanned    TodoStatus = "planned"
	TodoStatusInProgress TodoStatus = "in_progress"
	TodoStatusBlocked    TodoStatus = "blocked"
	TodoStatusReview     TodoStatus = "review"
	TodoStatusDone       TodoStatus = "done"
	TodoStatusCanceled   TodoStatus = "canceled"
)

type TodoPriority string

const (
	TodoPriorityLow    TodoPriority = "low"
	TodoPriorityMedium TodoPriority = "medium"
	TodoPriorityHigh   TodoPriority = "high"
	TodoPriorityUrgent TodoPriority = "urgent"
)

type TodoWorkKind string

const (
	TodoWorkKindGeneral     TodoWorkKind = "general"
	TodoWorkKindDevelopment TodoWorkKind = "development"
)

type TodoAutomationMode string

const (
	TodoAutomationModeManual  TodoAutomationMode = "manual"
	TodoAutomationModeSuggest TodoAutomationMode = "suggest"
	TodoAutomationModeManaged TodoAutomationMode = "managed"
)

type AssigneeKind string

const (
	AssigneeKindHuman AssigneeKind = "human"
	AssigneeKindAgent AssigneeKind = "agent"
)

type IdeaStatus string

const (
	IdeaStatusDraft       IdeaStatus = "draft"
	IdeaStatusSubmitted   IdeaStatus = "submitted"
	IdeaStatusUnderReview IdeaStatus = "under_review"
	IdeaStatusAccepted    IdeaStatus = "accepted"
	IdeaStatusExperiment  IdeaStatus = "experiment"
	IdeaStatusConverted   IdeaStatus = "converted"
	IdeaStatusRejected    IdeaStatus = "rejected"
	IdeaStatusArchived    IdeaStatus = "archived"
)

type IdeaVisibility string

const (
	IdeaVisibilityOrganization IdeaVisibility = "organization"
	IdeaVisibilityDepartment   IdeaVisibility = "department"
	IdeaVisibilityPrivate      IdeaVisibility = "private"
)

type IdeaImpact string

const (
	IdeaImpactLow    IdeaImpact = "low"
	IdeaImpactMedium IdeaImpact = "medium"
	IdeaImpactHigh   IdeaImpact = "high"
)

type IdeaEffort string

const (
	IdeaEffortSmall  IdeaEffort = "small"
	IdeaEffortMedium IdeaEffort = "medium"
	IdeaEffortLarge  IdeaEffort = "large"
)

type CreateTodoRequest struct {
	SourceConversationID string             `json:"source_conversation_id,omitempty"`
	Title                string             `json:"title"`
	Description          string             `json:"description,omitempty"`
	Status               TodoStatus         `json:"status,omitempty"`
	Priority             TodoPriority       `json:"priority,omitempty"`
	OrganizationID       string             `json:"organization_id,omitempty"`
	DepartmentID         string             `json:"department_id,omitempty"`
	ProjectID            string             `json:"project_id,omitempty"`
	WorkKind             TodoWorkKind       `json:"work_kind,omitempty"`
	AutomationMode       TodoAutomationMode `json:"automation_mode,omitempty"`
	DevelopmentProjectID string             `json:"development_project_id,omitempty"`
	SourceIdeaID         string             `json:"source_idea_id,omitempty"`
	AssigneeID           string             `json:"assignee_id,omitempty"`
	AssigneeName         string             `json:"assignee_name,omitempty"`
	AssigneeKind         AssigneeKind       `json:"assignee_kind,omitempty"`
	ReviewerID           string             `json:"reviewer_id,omitempty"`
	ReviewerName         string             `json:"reviewer_name,omitempty"`
	DueAt                time.Time          `json:"due_at,omitempty"`
	AcceptanceCriteria   string             `json:"acceptance_criteria,omitempty"`
	Tags                 []string           `json:"tags,omitempty"`
}

type UpdateTodoRequest struct {
	TodoID               string              `json:"todo_id"`
	Title                *string             `json:"title,omitempty"`
	Description          *string             `json:"description,omitempty"`
	Priority             *TodoPriority       `json:"priority,omitempty"`
	OrganizationID       *string             `json:"organization_id,omitempty"`
	DepartmentID         *string             `json:"department_id,omitempty"`
	ProjectID            *string             `json:"project_id,omitempty"`
	WorkKind             *TodoWorkKind       `json:"work_kind,omitempty"`
	AutomationMode       *TodoAutomationMode `json:"automation_mode,omitempty"`
	DevelopmentProjectID *string             `json:"development_project_id,omitempty"`
	AssigneeID           *string             `json:"assignee_id,omitempty"`
	AssigneeName         *string             `json:"assignee_name,omitempty"`
	AssigneeKind         *AssigneeKind       `json:"assignee_kind,omitempty"`
	ReviewerID           *string             `json:"reviewer_id,omitempty"`
	ReviewerName         *string             `json:"reviewer_name,omitempty"`
	DueAt                *time.Time          `json:"due_at,omitempty"`
	ClearDueAt           bool                `json:"clear_due_at,omitempty"`
	AcceptanceCriteria   *string             `json:"acceptance_criteria,omitempty"`
	Tags                 *[]string           `json:"tags,omitempty"`
	Version              *int64              `json:"version,omitempty"`
}

type DeleteTodoRequest struct {
	TodoID  string `json:"todo_id"`
	Version *int64 `json:"version,omitempty"`
}

type ClaimTodoRequest struct {
	TodoID       string       `json:"todo_id"`
	AssigneeID   string       `json:"assignee_id,omitempty"`
	AssigneeName string       `json:"assignee_name,omitempty"`
	AssigneeKind AssigneeKind `json:"assignee_kind,omitempty"`
	Version      *int64       `json:"version,omitempty"`
}

type MoveTodoRequest struct {
	ResultAttachmentIDs []string   `json:"result_attachment_ids,omitempty"`
	Result              string     `json:"result,omitempty"`
	TodoID              string     `json:"todo_id"`
	Status              TodoStatus `json:"status"`
	BlockedReason       string     `json:"blocked_reason,omitempty"`
	Version             *int64     `json:"version,omitempty"`
}

type CompleteTodoRequest struct {
	ResultAttachmentIDs []string `json:"result_attachment_ids,omitempty"`
	Result              string   `json:"result,omitempty"`
	TodoID              string   `json:"todo_id"`
	Version             *int64   `json:"version,omitempty"`
}

type CancelTodoRequest struct {
	TodoID  string `json:"todo_id"`
	Version *int64 `json:"version,omitempty"`
}

type ReopenTodoRequest struct {
	TodoID  string `json:"todo_id"`
	Version *int64 `json:"version,omitempty"`
}

type ListTodosQuery struct {
	IncludePendingDelivery bool         `json:"include_pending_delivery,omitempty"`
	Status                 TodoStatus   `json:"status,omitempty"`
	Priority               TodoPriority `json:"priority,omitempty"`
	OrganizationID         string       `json:"organization_id,omitempty"`
	DepartmentID           string       `json:"department_id,omitempty"`
	ProjectID              string       `json:"project_id,omitempty"`
	DevelopmentProjectID   string       `json:"development_project_id,omitempty"`
	SourceIdeaID           string       `json:"source_idea_id,omitempty"`
	AssigneeID             string       `json:"assignee_id,omitempty"`
	CreatedBy              string       `json:"created_by,omitempty"`
	Q                      string       `json:"q,omitempty"`
	DueBefore              time.Time    `json:"due_before,omitempty"`
	Mine                   bool         `json:"mine,omitempty"`
	Unassigned             bool         `json:"unassigned,omitempty"`
	IncludeCompleted       bool         `json:"include_completed,omitempty"`
	Limit                  int          `json:"limit,omitempty"`
	Offset                 int          `json:"offset,omitempty"`
	authorizedIDs          []string
	authorizationFiltered  bool
}

type IssueKind string

const (
	IssueKindTodo IssueKind = "todo"
	IssueKindIdea IssueKind = "idea"
)

type CreateIssueCommentRequest struct {
	IssueKind IssueKind `json:"issue_kind,omitempty"`
	IssueID   string    `json:"issue_id,omitempty"`
	Body      string    `json:"body"`
}

type ListIssueCommentsQuery struct {
	IssueKind IssueKind `json:"issue_kind,omitempty"`
	IssueID   string    `json:"issue_id,omitempty"`
	Limit     int       `json:"limit,omitempty"`
	Offset    int       `json:"offset,omitempty"`
}

type IssueComment struct {
	TenantID   string    `json:"tenant_id,omitempty"`
	CommentID  string    `json:"comment_id"`
	IssueKind  IssueKind `json:"issue_kind"`
	IssueID    string    `json:"issue_id"`
	Body       string    `json:"body"`
	AuthorID   string    `json:"author_id"`
	AuthorName string    `json:"author_name,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type IssueCommentPage struct {
	Comments []IssueComment `json:"comments"`
	Total    int            `json:"total"`
	Limit    int            `json:"limit,omitempty"`
	Offset   int            `json:"offset,omitempty"`
	HasMore  bool           `json:"has_more"`
}

type TodoItem struct {
	Attachments          []TodoAttachment `json:"attachments,omitempty"`
	Feedback             *TodoFeedback    `json:"feedback,omitempty"`
	attachmentRecords    []todoAttachmentRecord
	Source               *TodoSource `json:"source,omitempty"`
	sourceNotices        []todoSourceNotice
	TenantID             string                 `json:"tenant_id,omitempty"`
	OrganizationID       string                 `json:"organization_id,omitempty"`
	DepartmentID         string                 `json:"department_id,omitempty"`
	ProjectID            string                 `json:"project_id,omitempty"`
	WorkKind             TodoWorkKind           `json:"work_kind"`
	AutomationMode       TodoAutomationMode     `json:"automation_mode"`
	DevelopmentProjectID string                 `json:"development_project_id,omitempty"`
	SourceIdeaID         string                 `json:"source_idea_id,omitempty"`
	TodoID               string                 `json:"todo_id"`
	IssueNumber          string                 `json:"issue_number,omitempty"`
	Title                string                 `json:"title"`
	Description          string                 `json:"description,omitempty"`
	Status               TodoStatus             `json:"status"`
	Priority             TodoPriority           `json:"priority"`
	ProposerName         string                 `json:"proposer_name,omitempty"`
	Tags                 []string               `json:"tags,omitempty"`
	AssigneeID           string                 `json:"assignee_id,omitempty"`
	AssigneeName         string                 `json:"assignee_name,omitempty"`
	AssigneeKind         AssigneeKind           `json:"assignee_kind,omitempty"`
	ReviewerID           string                 `json:"reviewer_id,omitempty"`
	ReviewerName         string                 `json:"reviewer_name,omitempty"`
	AcceptanceCriteria   string                 `json:"acceptance_criteria,omitempty"`
	BlockedReason        string                 `json:"blocked_reason,omitempty"`
	CreatedBy            string                 `json:"created_by"`
	CreatedByKind        ViewerKind             `json:"created_by_kind,omitempty"`
	CreatedBySubjectID   string                 `json:"-"`
	CreatedAt            time.Time              `json:"created_at"`
	UpdatedAt            time.Time              `json:"updated_at"`
	DueAt                time.Time              `json:"due_at,omitempty"`
	CompletedAt          time.Time              `json:"completed_at,omitempty"`
	Version              int64                  `json:"version"`
	Automation           *TodoAutomationState   `json:"automation,omitempty"`
	DiscussionBinding    *TodoDiscussionBinding `json:"discussion_binding,omitempty"`
	Execution            *TodoExecutionSummary  `json:"execution,omitempty"`
}

type TodoAutomationStatus string

const (
	TodoAutomationStatusDiscussing             TodoAutomationStatus = "discussing"
	TodoAutomationStatusNotReady               TodoAutomationStatus = "not_ready"
	TodoAutomationStatusPlanningFailed         TodoAutomationStatus = "planning_failed"
	TodoAutomationStatusPendingApproval        TodoAutomationStatus = "pending_approval"
	TodoAutomationStatusApproved               TodoAutomationStatus = "approved"
	TodoAutomationStatusRejected               TodoAutomationStatus = "rejected"
	TodoAutomationStatusStale                  TodoAutomationStatus = "stale"
	TodoAutomationStatusProvisioningWorkspace  TodoAutomationStatus = "provisioning_workspace"
	TodoAutomationStatusExecuting              TodoAutomationStatus = "executing"
	TodoAutomationStatusWaitingRuntimeApproval TodoAutomationStatus = "waiting_runtime_approval"
	TodoAutomationStatusExecuted               TodoAutomationStatus = "executed"
	TodoAutomationStatusExecutionFailed        TodoAutomationStatus = "execution_failed"
)

type TodoAutomationState struct {
	TenantID             string               `json:"tenant_id,omitempty"`
	ProposalID           string               `json:"proposal_id"`
	ApprovalID           string               `json:"approval_id,omitempty"`
	HILDecisionID        string               `json:"hil_decision_id,omitempty"`
	TodoID               string               `json:"todo_id"`
	TodoVersion          int64                `json:"todo_version"`
	Status               TodoAutomationStatus `json:"status"`
	ConversationID       string               `json:"conversation_id,omitempty"`
	Proposal             string               `json:"proposal,omitempty"`
	Plan                 TodoExecutionPlan    `json:"plan"`
	PlanDigest           string               `json:"plan_digest,omitempty"`
	ApprovedPlanDigest   string               `json:"approved_plan_digest,omitempty"`
	FailureReason        string               `json:"failure_reason,omitempty"`
	RequestedBy          string               `json:"requested_by,omitempty"`
	ApproverID           string               `json:"approver_id,omitempty"`
	DecisionBy           string               `json:"decision_by,omitempty"`
	ExecutionRequestedBy string               `json:"execution_requested_by,omitempty"`
	DecisionMessage      string               `json:"decision_message,omitempty"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
	NextCheckAt          time.Time            `json:"next_check_at,omitempty"`
	DecidedAt            time.Time            `json:"decided_at,omitempty"`
	ExecutedAt           time.Time            `json:"executed_at,omitempty"`

	discussionMembersFingerprint             string
	decisionViewerKind                       ViewerKind
	decisionAuthorizationSubjectID           string
	decisionPermissionScope                  []string
	decisionDisplayName                      string
	executionRequesterViewerKind             ViewerKind
	executionRequesterAuthorizationSubjectID string
	executionRequesterPermissionScope        []string
	executionRequesterDisplayName            string
	pendingAuditEvents                       []operations.AuditEventDTO
}

type TodoExecutionPlan struct {
	TenantID               string                         `json:"-"`
	Version                int                            `json:"version"`
	Summary                string                         `json:"summary"`
	PlanID                 string                         `json:"plan_id"`
	TodoID                 string                         `json:"todo_id"`
	TodoVersion            int64                          `json:"todo_version"`
	ProposalRevision       int                            `json:"proposal_revision"`
	ConversationID         string                         `json:"conversation_id"`
	DevelopmentProjectID   string                         `json:"development_project_id,omitempty"`
	ResourceSnapshotDigest string                         `json:"resource_snapshot_digest,omitempty"`
	RepositoryScopes       []TodoExecutionRepositoryScope `json:"repository_scopes"`
	BaseCommits            []TodoExecutionBaseCommit      `json:"base_commits"`
	AllowedFileScopes      []string                       `json:"allowed_file_scopes"`
	PlannedCommands        []string                       `json:"planned_commands"`
	NetworkPolicy          TodoExecutionNetworkPolicy     `json:"network_policy"`
	RequiredSecretRefs     []string                       `json:"required_secret_refs"`
	RiskSummary            []string                       `json:"risk_summary"`
	AcceptanceChecks       []string                       `json:"acceptance_checks"`
	Steps                  []TodoExecutionPlanStep        `json:"steps"`
	RequiredApprovals      []string                       `json:"required_approvals"`
	Status                 string                         `json:"status"`
	CreatedAt              time.Time                      `json:"created_at"`
	UpdatedAt              time.Time                      `json:"updated_at"`
}

// TodoExecutionPlanView is the immutable plan snapshot returned by the Todo
// API together with its digest and current lifecycle state.
type TodoExecutionPlanView struct {
	Plan            TodoExecutionPlan    `json:"plan"`
	PlanDigest      string               `json:"plan_digest"`
	LifecycleStatus TodoAutomationStatus `json:"lifecycle_status"`
}

type TodoExecutionPlanPage struct {
	ExecutionPlans []TodoExecutionPlanView `json:"execution_plans"`
}

type TodoExecutionRepositoryScope struct {
	BindingID          string `json:"binding_id"`
	DisplayName        string `json:"display_name"`
	Role               string `json:"role"`
	AccessMode         string `json:"access_mode"`
	DefaultBranch      string `json:"default_branch"`
	SourceSubdirectory string `json:"source_subdirectory,omitempty"`
}

type TodoExecutionBaseCommit struct {
	BindingID string `json:"binding_id"`
	CommitSHA string `json:"commit_sha"`
}

type TodoExecutionNetworkPolicy struct {
	Mode         string   `json:"mode"`
	AllowedHosts []string `json:"allowed_hosts"`
}

type TodoExecutionPlanStep struct {
	StepID               string                             `json:"step_id"`
	Title                string                             `json:"title"`
	Description          string                             `json:"description,omitempty"`
	AssigneeAgentID      string                             `json:"assignee_agent_id,omitempty"`
	AcceptanceCriteria   []string                           `json:"acceptance_criteria,omitempty"`
	EvidenceRequirements []TodoExecutionEvidenceRequirement `json:"evidence_requirements"`
	DependsOn            []string                           `json:"depends_on,omitempty"`
}

type TodoExecutionEvidenceRequirement struct {
	EvidenceRequirementID string `json:"evidence_requirement_id"`
	AcceptanceCriterion   string `json:"acceptance_criterion"`
	Kind                  string `json:"kind"`
	Required              bool   `json:"required"`
}

type TodoDiscussionBinding struct {
	TenantID       string    `json:"tenant_id,omitempty"`
	TodoID         string    `json:"todo_id"`
	ConversationID string    `json:"conversation_id"`
	ManagedSource  string    `json:"managed_source"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Version        int64     `json:"version"`
}

const (
	TodoMissionBindingStatusCreating       = "creating"
	TodoMissionBindingStatusMaterialized   = "materialized"
	TodoMissionBindingStatusProvisioning   = "provisioning_workspace"
	TodoMissionBindingStatusDispatching    = "dispatching"
	TodoMissionBindingStatusExecuting      = "executing"
	TodoMissionBindingStatusWaiting        = "waiting_runtime_approval"
	TodoMissionBindingStatusPauseRequested = "pause_requested"
	TodoMissionBindingStatusPaused         = "paused"
	TodoMissionBindingStatusExecuted       = "executed"
	TodoMissionBindingStatusFailed         = "execution_failed"
)

type TodoMissionBinding struct {
	TenantID             string    `json:"tenant_id,omitempty"`
	BindingID            string    `json:"binding_id"`
	TodoID               string    `json:"todo_id"`
	MissionID            string    `json:"mission_id,omitempty"`
	ConversationID       string    `json:"conversation_id"`
	DevelopmentProjectID string    `json:"development_project_id,omitempty"`
	ProposalID           string    `json:"proposal_id"`
	TodoVersion          int64     `json:"todo_version"`
	ExecutionAttempt     int       `json:"execution_attempt"`
	PlanDigest           string    `json:"plan_digest"`
	MissionTaskIDs       []string  `json:"mission_task_ids"`
	WorkspaceID          string    `json:"workspace_id,omitempty"`
	ExecutionRunIDs      []string  `json:"execution_run_ids,omitempty"`
	Status               string    `json:"status"`
	PauseReason          string    `json:"pause_reason,omitempty"`
	FailureReason        string    `json:"failure_reason,omitempty"`
	CreatedBy            string    `json:"created_by"`
	IdempotencyKey       string    `json:"-"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
	Version              int64     `json:"version"`

	leaseToken     string
	leaseExpiresAt time.Time
}

type TodoMissionPauseRequest struct {
	TenantID         string
	TodoID           string
	BindingID        string
	MissionID        string
	ExecutionAttempt int
	Reason           string
	IdempotencyKey   string
}

type TodoExecutionAttempt struct {
	TenantID             string                 `json:"tenant_id,omitempty"`
	TodoID               string                 `json:"todo_id"`
	ExecutionAttempt     int                    `json:"execution_attempt"`
	MissionID            string                 `json:"mission_id,omitempty"`
	DevelopmentProjectID string                 `json:"development_project_id,omitempty"`
	MissionTaskIDs       []string               `json:"mission_task_ids"`
	WorkspaceID          string                 `json:"workspace_id,omitempty"`
	ExecutionRunIDs      []string               `json:"execution_run_ids,omitempty"`
	ProposalID           string                 `json:"proposal_id"`
	TodoVersion          int64                  `json:"todo_version"`
	PlanDigest           string                 `json:"plan_digest"`
	Status               string                 `json:"status"`
	FailureReason        string                 `json:"failure_reason,omitempty"`
	StartedAt            time.Time              `json:"started_at"`
	CompletedAt          time.Time              `json:"completed_at,omitempty"`
	UpdatedAt            time.Time              `json:"updated_at"`
	Progress             *TodoExecutionProgress `json:"progress,omitempty"`
}

type TodoExecutionSummary struct {
	Current         *TodoMissionBinding    `json:"current,omitempty"`
	CurrentProgress *TodoExecutionProgress `json:"current_progress,omitempty"`
	Attempts        []TodoExecutionAttempt `json:"attempts"`
}

// TodoExecutionProgressProjection is the durable, authorization-neutral summary
// projected from Mission Control and code submission facts.
type TodoExecutionProgressProjection struct {
	TenantID                 string    `json:"tenant_id,omitempty"`
	TodoID                   string    `json:"todo_id"`
	MissionID                string    `json:"mission_id"`
	ExecutionAttempt         int       `json:"execution_attempt"`
	MissionStatus            string    `json:"mission_status"`
	CurrentPhase             string    `json:"current_phase"`
	DeliveryRate             float64   `json:"delivery_rate"`
	TotalTaskCount           int       `json:"total_task_count"`
	ActiveTaskCount          int       `json:"active_task_count"`
	DoneTaskCount            int       `json:"done_task_count"`
	BlockedTaskCount         int       `json:"blocked_task_count"`
	PendingApprovalCount     int       `json:"pending_approval_count"`
	CurrentAgentIDs          []string  `json:"current_agent_ids"`
	LastActivityAt           time.Time `json:"last_activity_at"`
	CodeSubmissionStatus     string    `json:"code_submission_status,omitempty"`
	PullRequestStatus        string    `json:"pull_request_status,omitempty"`
	MergeStatus              string    `json:"merge_status,omitempty"`
	MissionProjectionVersion int       `json:"mission_projection_version"`
	SubmissionRevision       int64     `json:"submission_revision"`
	ProjectedAt              time.Time `json:"projected_at"`
}

// TodoExecutionProgress adds request-scoped capabilities to the durable
// projection. These fields are never persisted by the projection repository.
type TodoExecutionProgress struct {
	TodoExecutionProgressProjection
	CanOpenControl    bool   `json:"can_open_control"`
	CanOpenDiscussion bool   `json:"can_open_discussion"`
	ConversationID    string `json:"conversation_id,omitempty"`
}

// ProjectExecutionTodoSnapshot is the narrow, authorization-neutral input for
// a DevelopmentProject execution monitor. It intentionally excludes Todo
// descriptions, acceptance criteria, comments, plans, and approval content.
type ProjectExecutionTodoSnapshot struct {
	TenantID             string
	DevelopmentProjectID string
	TodoID               string
	Title                string
	Status               TodoStatus
	AutomationMode       TodoAutomationMode
	AutomationStatus     TodoAutomationStatus
	AssigneeID           string
	AssigneeName         string
	CreatedBy            string
	CreatedBySubjectID   string
	ReviewerID           string
	MissionID            string
	ExecutionAttempt     int
	ConversationID       string
	UpdatedAt            time.Time
}

type TodoAutomationScanRequest struct {
	ScheduledTaskID string    `json:"scheduled_task_id,omitempty"`
	ScheduledRunID  string    `json:"scheduled_run_id,omitempty"`
	TriggeredAt     time.Time `json:"triggered_at,omitempty"`
}

type CheckTodoAutomationRequest struct {
	TodoID         string `json:"todo_id"`
	IdempotencyKey string `json:"-"`
}

type TodoAutomationScanResult struct {
	ScannedCount        int `json:"scanned_count"`
	EligibleCount       int `json:"eligible_count"`
	ClaimedCount        int `json:"claimed_count"`
	DiscussionSyncCount int `json:"discussion_sync_count"`
	PlannedCount        int `json:"planned_count"`
	RecoveredCount      int `json:"recovered_count"`
	SkippedCount        int `json:"skipped_count"`
	FailedCount         int `json:"failed_count"`
}

type TodoAutomationApproval struct {
	ApprovalID     string             `json:"approval_id"`
	Status         string             `json:"status"`
	TodoID         string             `json:"todo_id"`
	TodoTitle      string             `json:"todo_title"`
	TodoVersion    int64              `json:"todo_version"`
	ConversationID string             `json:"conversation_id,omitempty"`
	Proposal       string             `json:"proposal,omitempty"`
	Plan           TodoExecutionPlan  `json:"plan"`
	PlanDigest     string             `json:"plan_digest"`
	AutomationMode TodoAutomationMode `json:"automation_mode"`
	RequestedBy    string             `json:"requested_by"`
	ApproverID     string             `json:"approver_id,omitempty"`
	HILDecisionID  string             `json:"hil_decision_id,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	DecidedAt      time.Time          `json:"decided_at,omitempty"`
}

type ListTodoAutomationApprovalsQuery struct {
	Status string
}

type DecideTodoAutomationApprovalRequest struct {
	ApprovalID     string
	ActionID       string
	Message        string
	PlanDigest     string
	IdempotencyKey string
}

type StartTodoAutomationExecutionRequest struct {
	TodoID         string `json:"todo_id"`
	PlanID         string `json:"plan_id"`
	PlanDigest     string `json:"plan_digest"`
	TodoVersion    int64  `json:"todo_version"`
	IdempotencyKey string `json:"-"`
}

type TodoPage struct {
	Todos   []TodoItem `json:"todos"`
	Total   int        `json:"total"`
	Limit   int        `json:"limit,omitempty"`
	Offset  int        `json:"offset,omitempty"`
	HasMore bool       `json:"has_more"`
}

type CreateIdeaRequest struct {
	Title          string         `json:"title"`
	Content        string         `json:"content,omitempty"`
	Status         IdeaStatus     `json:"status,omitempty"`
	Visibility     IdeaVisibility `json:"visibility,omitempty"`
	OrganizationID string         `json:"organization_id,omitempty"`
	DepartmentID   string         `json:"department_id,omitempty"`
	OwnerID        string         `json:"owner_id,omitempty"`
	OwnerName      string         `json:"owner_name,omitempty"`
	Impact         IdeaImpact     `json:"impact,omitempty"`
	Effort         IdeaEffort     `json:"effort,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
}

type UpdateIdeaRequest struct {
	IdeaID         string          `json:"idea_id"`
	Title          *string         `json:"title,omitempty"`
	Content        *string         `json:"content,omitempty"`
	Visibility     *IdeaVisibility `json:"visibility,omitempty"`
	OrganizationID *string         `json:"organization_id,omitempty"`
	DepartmentID   *string         `json:"department_id,omitempty"`
	OwnerID        *string         `json:"owner_id,omitempty"`
	OwnerName      *string         `json:"owner_name,omitempty"`
	ReviewerID     *string         `json:"reviewer_id,omitempty"`
	ReviewerName   *string         `json:"reviewer_name,omitempty"`
	Impact         *IdeaImpact     `json:"impact,omitempty"`
	Effort         *IdeaEffort     `json:"effort,omitempty"`
	DecisionReason *string         `json:"decision_reason,omitempty"`
	Tags           *[]string       `json:"tags,omitempty"`
	Version        *int64          `json:"version,omitempty"`
}

type TransitionIdeaRequest struct {
	IdeaID         string     `json:"idea_id"`
	Status         IdeaStatus `json:"status"`
	DecisionReason string     `json:"decision_reason,omitempty"`
	ReviewerID     string     `json:"reviewer_id,omitempty"`
	ReviewerName   string     `json:"reviewer_name,omitempty"`
	Version        *int64     `json:"version,omitempty"`
}

type DeleteIdeaRequest struct {
	IdeaID  string `json:"idea_id"`
	Version *int64 `json:"version,omitempty"`
}

type ArchiveIdeaRequest struct {
	IdeaID  string `json:"idea_id"`
	Version *int64 `json:"version,omitempty"`
}

type RestoreIdeaRequest struct {
	IdeaID  string `json:"idea_id"`
	Version *int64 `json:"version,omitempty"`
}

type VoteIdeaRequest struct {
	IdeaID string `json:"idea_id"`
}

type ConvertIdeaToTodoRequest struct {
	IdeaID             string       `json:"idea_id"`
	TodoTitle          string       `json:"todo_title,omitempty"`
	TodoDescription    string       `json:"todo_description,omitempty"`
	Priority           TodoPriority `json:"priority,omitempty"`
	OrganizationID     string       `json:"organization_id,omitempty"`
	DepartmentID       string       `json:"department_id,omitempty"`
	ProjectID          string       `json:"project_id,omitempty"`
	AssigneeID         string       `json:"assignee_id,omitempty"`
	AssigneeName       string       `json:"assignee_name,omitempty"`
	AssigneeKind       AssigneeKind `json:"assignee_kind,omitempty"`
	ReviewerID         string       `json:"reviewer_id,omitempty"`
	ReviewerName       string       `json:"reviewer_name,omitempty"`
	DueAt              time.Time    `json:"due_at,omitempty"`
	AcceptanceCriteria string       `json:"acceptance_criteria,omitempty"`
	DecisionReason     string       `json:"decision_reason,omitempty"`
	Version            *int64       `json:"version,omitempty"`
}

type ListIdeasQuery struct {
	Status         IdeaStatus     `json:"status,omitempty"`
	Visibility     IdeaVisibility `json:"visibility,omitempty"`
	OrganizationID string         `json:"organization_id,omitempty"`
	DepartmentID   string         `json:"department_id,omitempty"`
	OwnerID        string         `json:"owner_id,omitempty"`
	ReviewerID     string         `json:"reviewer_id,omitempty"`
	Q              string         `json:"q,omitempty"`
	Limit          int            `json:"limit,omitempty"`
	Offset         int            `json:"offset,omitempty"`
}

type SuggestDraftRequest struct {
	Kind        string   `json:"kind,omitempty"`
	Title       string   `json:"title,omitempty"`
	Content     string   `json:"content,omitempty"`
	CurrentTags []string `json:"current_tags,omitempty"`
}

type DraftSuggestion struct {
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
}

type IdeaItem struct {
	TenantID        string         `json:"tenant_id,omitempty"`
	OrganizationID  string         `json:"organization_id,omitempty"`
	DepartmentID    string         `json:"department_id,omitempty"`
	IdeaID          string         `json:"idea_id"`
	IssueNumber     string         `json:"issue_number,omitempty"`
	Title           string         `json:"title"`
	Content         string         `json:"content,omitempty"`
	Status          IdeaStatus     `json:"status"`
	Visibility      IdeaVisibility `json:"visibility"`
	Impact          IdeaImpact     `json:"impact"`
	Effort          IdeaEffort     `json:"effort"`
	ProposerName    string         `json:"proposer_name,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	OwnerID         string         `json:"owner_id,omitempty"`
	OwnerName       string         `json:"owner_name,omitempty"`
	ReviewerID      string         `json:"reviewer_id,omitempty"`
	ReviewerName    string         `json:"reviewer_name,omitempty"`
	DecisionReason  string         `json:"decision_reason,omitempty"`
	VoteCount       int            `json:"vote_count"`
	ViewerHasVoted  bool           `json:"viewer_has_voted"`
	VoterIDs        []string       `json:"-"`
	CreatedBy       string         `json:"created_by"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	SubmittedAt     time.Time      `json:"submitted_at,omitempty"`
	ReviewedAt      time.Time      `json:"reviewed_at,omitempty"`
	ArchivedAt      time.Time      `json:"archived_at,omitempty"`
	ConvertedAt     time.Time      `json:"converted_at,omitempty"`
	ConvertedTodoID string         `json:"converted_todo_id,omitempty"`
	Version         int64          `json:"version"`
}

type IdeaPage struct {
	Ideas   []IdeaItem `json:"ideas"`
	Total   int        `json:"total"`
	Limit   int        `json:"limit,omitempty"`
	Offset  int        `json:"offset,omitempty"`
	HasMore bool       `json:"has_more"`
}

type IdeaConversionResult struct {
	Idea IdeaItem `json:"idea"`
	Todo TodoItem `json:"todo"`
}

type WorkHubSummary struct {
	MyOpenTasks         int `json:"my_open_tasks"`
	DueToday            int `json:"due_today"`
	Overdue             int `json:"overdue"`
	Blocked             int `json:"blocked"`
	AwaitingTaskReview  int `json:"awaiting_task_review"`
	TasksInbox          int `json:"tasks_inbox"`
	TasksPlanned        int `json:"tasks_planned"`
	TasksInProgress     int `json:"tasks_in_progress"`
	IdeasAwaitingReview int `json:"ideas_awaiting_review"`
	IdeasSubmitted      int `json:"ideas_submitted"`
	IdeasUnderReview    int `json:"ideas_under_review"`
	IdeasInExperiment   int `json:"ideas_in_experiment"`
	IdeasConverted      int `json:"ideas_converted"`
	UnassignedTeamTasks int `json:"unassigned_team_tasks"`
}
