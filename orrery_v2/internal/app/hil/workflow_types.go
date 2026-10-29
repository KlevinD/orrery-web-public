package hil

import "time"

type WorkflowStageType string

const (
	WorkflowStageApproval WorkflowStageType = "approval"
	WorkflowStageCouncil  WorkflowStageType = "council"
)

type ApprovalStrategy string

const (
	ApprovalSingle    ApprovalStrategy = "single"
	ApprovalAny       ApprovalStrategy = "any_one"
	ApprovalAll       ApprovalStrategy = "all"
	ApprovalThreshold ApprovalStrategy = "threshold"
)

type WorkflowInstanceStatus string

const (
	WorkflowRunning          WorkflowInstanceStatus = "running"
	WorkflowApproved         WorkflowInstanceStatus = "approved"
	WorkflowRejected         WorkflowInstanceStatus = "rejected"
	WorkflowChangesRequested WorkflowInstanceStatus = "changes_requested"
	WorkflowExpired          WorkflowInstanceStatus = "expired"
	WorkflowSuperseded       WorkflowInstanceStatus = "superseded"
)

type WorkflowTaskStatus string

const (
	TaskScheduled        WorkflowTaskStatus = "scheduled"
	TaskPending          WorkflowTaskStatus = "pending"
	TaskApproved         WorkflowTaskStatus = "approved"
	TaskRejected         WorkflowTaskStatus = "rejected"
	TaskChangesRequested WorkflowTaskStatus = "changes_requested"
	TaskTransferred      WorkflowTaskStatus = "transferred"
	TaskSuperseded       WorkflowTaskStatus = "superseded"
	TaskExpired          WorkflowTaskStatus = "expired"
)

type StageOutcome string

const (
	StageOpen   StageOutcome = "open"
	StagePassed StageOutcome = "passed"
	StageFailed StageOutcome = "failed"
)

type WorkflowSelectorKind string

const (
	WorkflowSelectorHuman                WorkflowSelectorKind = "explicit_human"
	WorkflowSelectorPeopleRole           WorkflowSelectorKind = "people_role"
	WorkflowSelectorOrganizationPosition WorkflowSelectorKind = "organization_position"
	WorkflowSelectorRequesterManager     WorkflowSelectorKind = "requester_manager"
	WorkflowSelectorResourceOwner        WorkflowSelectorKind = "resource_owner"
	WorkflowSelectorOntologyRecommended  WorkflowSelectorKind = "ontology_recommended"
)

type WorkflowActorSelector struct {
	Kind WorkflowSelectorKind `json:"kind"`
	ID   string               `json:"id,omitempty"`
}

type WorkflowEscalationRung struct {
	Actors          []WorkflowActorSelector `json:"actors"`
	DeadlineMinutes int                     `json:"deadline_minutes"`
}

type ApprovalStagePolicy struct {
	Strategy               ApprovalStrategy         `json:"strategy"`
	Threshold              int                      `json:"threshold,omitempty"`
	Assignees              []WorkflowActorSelector  `json:"assignees"`
	DeadlineMinutes        int                      `json:"deadline_minutes"`
	ReminderOffsetsMinutes []int                    `json:"reminder_offsets_minutes,omitempty"`
	Escalation             []WorkflowEscalationRung `json:"escalation,omitempty"`
}

type CouncilStagePolicy struct {
	Participants      []WorkflowActorSelector `json:"participants"`
	Quorum            int                     `json:"quorum"`
	ApprovalThreshold int                     `json:"approval_threshold"`
	DeadlineMinutes   int                     `json:"deadline_minutes"`
}

type WorkflowStageDefinition struct {
	Key      string               `json:"key"`
	Name     string               `json:"name"`
	Order    int                  `json:"order"`
	Type     WorkflowStageType    `json:"type"`
	Approval *ApprovalStagePolicy `json:"approval,omitempty"`
	Council  *CouncilStagePolicy  `json:"council,omitempty"`
}

type WorkflowDefinition struct {
	RiskLevels               []RiskLevel               `json:"risk_levels"`
	Stages                   []WorkflowStageDefinition `json:"stages"`
	RequireDistinctApprovers bool                      `json:"require_distinct_approvers,omitempty"`
}

type WorkflowTemplateStatus string

const (
	WorkflowTemplateDisabled WorkflowTemplateStatus = "disabled"
	WorkflowTemplateActive   WorkflowTemplateStatus = "active"
)

type WorkflowStageStatus string

const (
	WorkflowStageScheduled        WorkflowStageStatus = "scheduled"
	WorkflowStageActive           WorkflowStageStatus = "active"
	WorkflowStagePassed           WorkflowStageStatus = "passed"
	WorkflowStageFailed           WorkflowStageStatus = "failed"
	WorkflowStageSuperseded       WorkflowStageStatus = "superseded"
	WorkflowStageChangesRequested WorkflowStageStatus = "changes_requested"
	WorkflowStageExpired          WorkflowStageStatus = "expired"
)

type WorkflowTaskDecision string

const (
	WorkflowTaskApprove        WorkflowTaskDecision = "approve"
	WorkflowTaskReject         WorkflowTaskDecision = "reject"
	WorkflowTaskRequestChanges WorkflowTaskDecision = "request_changes"
)

type WorkflowNotificationStatus string

const (
	WorkflowNotificationPending        WorkflowNotificationStatus = "pending"
	WorkflowNotificationProcessing     WorkflowNotificationStatus = "processing"
	WorkflowNotificationDelivered      WorkflowNotificationStatus = "delivered"
	WorkflowNotificationFailed         WorkflowNotificationStatus = "failed"
	WorkflowNotificationCancelled      WorkflowNotificationStatus = "cancelled"
	WorkflowNotificationNeedsAttention WorkflowNotificationStatus = "needs_attention"
)

type WorkflowTemplate struct {
	ID                     string                 `json:"id"`
	TenantID               string                 `json:"tenant_id"`
	Name                   string                 `json:"name"`
	Description            string                 `json:"description,omitempty"`
	Status                 WorkflowTemplateStatus `json:"status"`
	DraftDefinition        WorkflowDefinition     `json:"draft_definition"`
	LatestPublishedVersion int                    `json:"latest_published_version"`
	CreatedBy              ActorRef               `json:"created_by"`
	CreatedAt              time.Time              `json:"created_at"`
	UpdatedAt              time.Time              `json:"updated_at"`
}

type WorkflowTemplateVersion struct {
	ID             string             `json:"id"`
	TenantID       string             `json:"tenant_id"`
	TemplateID     string             `json:"template_id"`
	Version        int                `json:"version"`
	Definition     WorkflowDefinition `json:"definition"`
	DefinitionHash string             `json:"definition_hash"`
	PublishedBy    ActorRef           `json:"published_by"`
	PublishedAt    time.Time          `json:"published_at"`
}

type WorkflowTemplateBinding struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	ActionTypeID string    `json:"action_type_id"`
	ToolName     string    `json:"tool_name,omitempty"`
	RiskLevel    RiskLevel `json:"risk_level,omitempty"`
	TemplateID   string    `json:"template_id"`
	Active       bool      `json:"active"`
	CreatedBy    ActorRef  `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type WorkflowTemplateSelector struct {
	TenantID     string
	ActionTypeID string
	ToolName     string
	RiskLevel    RiskLevel
}

type WorkflowTemplateListQuery struct {
	TenantID string
	Status   WorkflowTemplateStatus
}

type WorkflowTemplateSummary struct {
	ID                     string                 `json:"id"`
	TenantID               string                 `json:"tenant_id"`
	Name                   string                 `json:"name"`
	Description            string                 `json:"description,omitempty"`
	Status                 WorkflowTemplateStatus `json:"status"`
	LatestPublishedVersion int                    `json:"latest_published_version"`
	UpdatedAt              time.Time              `json:"updated_at"`
}

type WorkflowCase struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	Requester    ActorRef  `json:"requester"`
	ActionTypeID string    `json:"action_type_id"`
	ToolName     string    `json:"tool_name,omitempty"`
	Resource     ObjectRef `json:"resource"`
	CreatedAt    time.Time `json:"created_at"`
}

type WorkflowInstance struct {
	ID                    string                 `json:"id"`
	TenantID              string                 `json:"tenant_id"`
	CaseID                string                 `json:"case_id"`
	Revision              int                    `json:"revision"`
	DecisionRequestID     string                 `json:"decision_request_id"`
	TemplateVersionID     string                 `json:"template_version_id"`
	PredecessorInstanceID string                 `json:"predecessor_instance_id,omitempty"`
	DefinitionHash        string                 `json:"definition_hash"`
	MaterialHash          string                 `json:"material_hash"`
	ArgsHash              string                 `json:"args_hash"`
	Status                WorkflowInstanceStatus `json:"status"`
	CurrentStageKey       string                 `json:"current_stage_key,omitempty"`
	Version               int64                  `json:"version"`
	CreatedAt             time.Time              `json:"created_at"`
	UpdatedAt             time.Time              `json:"updated_at"`
	ResolvedAt            time.Time              `json:"resolved_at,omitempty"`
	ExpiresAt             time.Time              `json:"expires_at,omitempty"`
}

type WorkflowStageInstance struct {
	ID                       string                  `json:"id"`
	TenantID                 string                  `json:"tenant_id"`
	WorkflowInstanceID       string                  `json:"workflow_instance_id"`
	Definition               WorkflowStageDefinition `json:"definition"`
	ResolvedParticipants     []ActorRef              `json:"resolved_participants"`
	ResolvedEscalationActors []ActorRef              `json:"resolved_escalation_actors,omitempty"`
	Status                   WorkflowStageStatus     `json:"status"`
	DeadlineAt               time.Time               `json:"deadline_at,omitempty"`
	EscalationIndex          int                     `json:"escalation_index"`
	CreatedAt                time.Time               `json:"created_at"`
	ActivatedAt              time.Time               `json:"activated_at,omitempty"`
	ResolvedAt               time.Time               `json:"resolved_at,omitempty"`
}

type WorkflowTask struct {
	ID                 string               `json:"id"`
	TenantID           string               `json:"tenant_id"`
	WorkflowInstanceID string               `json:"workflow_instance_id"`
	StageInstanceID    string               `json:"stage_instance_id"`
	SeatKey            string               `json:"seat_key"`
	OriginalAssignee   ActorRef             `json:"original_assignee"`
	CurrentAssignee    ActorRef             `json:"current_assignee"`
	Status             WorkflowTaskStatus   `json:"status"`
	Decision           WorkflowTaskDecision `json:"decision,omitempty"`
	Reason             string               `json:"reason,omitempty"`
	TransferCount      int                  `json:"transfer_count"`
	TransferredFromID  string               `json:"transferred_from_id,omitempty"`
	TransferredToID    string               `json:"transferred_to_id,omitempty"`
	TransferReason     string               `json:"transfer_reason,omitempty"`
	DeadlineAt         time.Time            `json:"deadline_at,omitempty"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
	CompletedAt        time.Time            `json:"completed_at,omitempty"`
}

type WorkflowAuditEvent struct {
	ID                 string         `json:"id"`
	TenantID           string         `json:"tenant_id"`
	CaseID             string         `json:"case_id"`
	WorkflowInstanceID string         `json:"workflow_instance_id"`
	StageInstanceID    string         `json:"stage_instance_id,omitempty"`
	TaskID             string         `json:"task_id,omitempty"`
	Actor              ActorRef       `json:"actor"`
	EventType          string         `json:"event_type"`
	Reason             string         `json:"reason,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	CorrelationID      string         `json:"correlation_id,omitempty"`
	IdempotencyKey     string         `json:"-"`
	RequestFingerprint string         `json:"-"`
	CreatedAt          time.Time      `json:"created_at"`
}

type WorkflowNotification struct {
	ID                 string                     `json:"id"`
	TenantID           string                     `json:"tenant_id"`
	WorkflowInstanceID string                     `json:"workflow_instance_id"`
	StageInstanceID    string                     `json:"stage_instance_id,omitempty"`
	TaskID             string                     `json:"task_id,omitempty"`
	Channel            string                     `json:"channel"`
	Recipient          ActorRef                   `json:"recipient"`
	EventType          string                     `json:"event_type"`
	Payload            map[string]any             `json:"payload,omitempty"`
	Status             WorkflowNotificationStatus `json:"status"`
	AttemptCount       int                        `json:"attempt_count"`
	NextAttemptAt      time.Time                  `json:"next_attempt_at,omitempty"`
	ProviderReceipt    string                     `json:"provider_receipt,omitempty"`
	LastError          string                     `json:"last_error,omitempty"`
	IdempotencyKey     string                     `json:"-"`
	CreatedAt          time.Time                  `json:"created_at"`
	UpdatedAt          time.Time                  `json:"updated_at"`
	DeliveredAt        time.Time                  `json:"delivered_at,omitempty"`
}

type WorkflowInstanceDetail struct {
	Case          WorkflowCase            `json:"case"`
	Instance      WorkflowInstance        `json:"instance"`
	Decision      DecisionRequest         `json:"decision"`
	Stages        []WorkflowStageInstance `json:"stages"`
	Tasks         []WorkflowTask          `json:"tasks"`
	Audit         []WorkflowAuditEvent    `json:"audit"`
	Notifications []WorkflowNotification  `json:"notifications"`
	Receipts      []DecisionReceipt       `json:"receipts"`
}

type WorkflowListQuery struct {
	TenantID          string
	DecisionRequestID string
	Status            WorkflowInstanceStatus
	ActionableBy      ActorRef
	Limit             int
	Offset            int
}

type WorkflowInstancePage struct {
	Items []WorkflowInstanceDetail `json:"items"`
	Total int                      `json:"total"`
}

type WorkflowCommand struct {
	TenantID                string
	InstanceID              string
	ExpectedInstanceVersion int64
	Status                  WorkflowInstanceStatus
	CurrentStageKey         string
	UpdatedAt               time.Time
	Task                    *WorkflowTask
	Tasks                   []WorkflowTask
	NewTasks                []WorkflowTask
	Stages                  []WorkflowStageInstance
	Audit                   []WorkflowAuditEvent
	Notifications           []WorkflowNotification
	DecisionStatus          DecisionStatus
	Receipt                 *DecisionReceipt
	CouncilVote             *CouncilVote
	CouncilID               string
	CouncilToCreate         *CouncilSession
	CouncilToResolve        *CouncilSession
	IdempotencyKey          string
	RequestFingerprint      string
}

type DecideWorkflowTaskInput struct {
	delegation              *DelegationEvidence
	Decision                WorkflowTaskDecision `json:"decision"`
	Reason                  string               `json:"reason,omitempty"`
	IdempotencyKey          string               `json:"-"`
	ExpectedInstanceVersion int64                `json:"expected_instance_version"`
}

type TransferWorkflowTaskInput struct {
	NewAssignee             ActorRef `json:"new_assignee"`
	Reason                  string   `json:"reason"`
	IdempotencyKey          string   `json:"-"`
	ExpectedInstanceVersion int64    `json:"expected_instance_version"`
}

type ReassignWorkflowTaskInput = TransferWorkflowTaskInput

type ResubmitWorkflowInput struct {
	ArgsHash                string         `json:"args_hash"`
	MaterialHash            string         `json:"material_hash"`
	PayloadPreview          map[string]any `json:"payload_preview"`
	ExpiresAt               time.Time      `json:"expires_at"`
	IdempotencyKey          string         `json:"-"`
	ExpectedInstanceVersion int64          `json:"expected_instance_version"`
}

type WorkflowDueItem struct {
	Instance WorkflowInstance      `json:"instance"`
	Stage    WorkflowStageInstance `json:"stage"`
}

type WorkflowSweepResult struct {
	Reminders   int `json:"reminders"`
	Escalations int `json:"escalations"`
	Expired     int `json:"expired"`
	Conflicts   int `json:"conflicts"`
}

type WorkflowNotificationResult struct {
	TenantID        string
	NotificationID  string
	Delivered       bool
	Pending         bool
	Cancelled       bool
	NeedsAttention  bool
	ProviderReceipt string
	Failure         string
	NextAttemptAt   time.Time
	CompletedAt     time.Time
}
