package event

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
)

type Payload interface {
	EventPayloadVersion() int
}

type MissionCreatedPayload struct {
	Title                            string `json:"title"`
	FinalGoal                        string `json:"final_goal"`
	OriginKind                       string `json:"origin_kind,omitempty"`
	OriginID                         string `json:"origin_id,omitempty"`
	OriginTodoID                     string `json:"origin_todo_id,omitempty"`
	ExecutionAttempt                 int    `json:"execution_attempt,omitempty"`
	DiscussionMode                   string `json:"discussion_mode,omitempty"`
	OriginDevelopmentProjectID       string `json:"origin_development_project_id,omitempty"`
	PersonalAssistantOriginOwnerID   string `json:"personal_assistant_origin_owner_id,omitempty"`
	PersonalAssistantOriginSubjectID string `json:"personal_assistant_origin_subject_id,omitempty"`
}

func (MissionCreatedPayload) EventPayloadVersion() int { return 1 }

type MissionGoalUpdatedPayload struct {
	FinalGoal string `json:"final_goal"`
}

func (MissionGoalUpdatedPayload) EventPayloadVersion() int { return 1 }

type MissionPausedPayload struct {
	TodoID string `json:"todo_id"`
	Reason string `json:"reason"`
}

func (MissionPausedPayload) EventPayloadVersion() int { return 1 }

type MissionClosedPayload struct {
	Reason string `json:"reason"`
}

func (MissionClosedPayload) EventPayloadVersion() int { return 1 }

type StaffingRequestedPayload struct {
	RequestID string `json:"request_id"`
	RoleName  string `json:"role_name"`
	Reason    string `json:"reason"`
}

func (StaffingRequestedPayload) EventPayloadVersion() int { return 1 }

type AgentPayload struct {
	AgentID     agentcore.AgentID `json:"agent_id"`
	DisplayName string            `json:"display_name"`
	RealName    string            `json:"real_name"`
	RoleName    string            `json:"role_name"`
	Department  string            `json:"department"`
	Position    string            `json:"position"`
}

func (AgentPayload) EventPayloadVersion() int { return 1 }

type MissionAssignmentPayload struct {
	AssignmentID string             `json:"assignment_id"`
	AgentID      agentcore.AgentID  `json:"agent_id"`
	TaskID       missioncore.TaskID `json:"task_id"`
	MissionRole  string             `json:"mission_role"`
}

func (MissionAssignmentPayload) EventPayloadVersion() int { return 1 }

type StaffingFactPayload struct {
	TeamID          string `json:"team_id,omitempty"`
	RequestID       string `json:"request_id,omitempty"`
	DecisionID      string `json:"decision_id,omitempty"`
	MemberID        string `json:"member_id,omitempty"`
	AssignmentID    string `json:"assignment_id,omitempty"`
	AgentProfileID  string `json:"agent_profile_id,omitempty"`
	AgentInstanceID string `json:"agent_instance_id,omitempty"`
	AgentKind       string `json:"agent_kind,omitempty"`
	MissionRole     string `json:"mission_role,omitempty"`
	RationaleRef    string `json:"rationale_ref,omitempty"`
	AuditRef        string `json:"audit_ref,omitempty"`
	ReasonCode      string `json:"reason_code,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

func (StaffingFactPayload) EventPayloadVersion() int { return 1 }

type TaskContractCreatedPayload struct {
	Contract missiontask.TaskContract `json:"contract"`
}

func (TaskContractCreatedPayload) EventPayloadVersion() int { return 1 }

type TaskAssignedPayload struct {
	TaskID   missioncore.TaskID `json:"task_id"`
	AgentID  agentcore.AgentID  `json:"agent_id"`
	Goal     string             `json:"goal"`
	Assignee string             `json:"assignee"`
}

func (TaskAssignedPayload) EventPayloadVersion() int { return 1 }

type TaskAcceptedPayload struct {
	TaskID     missioncore.TaskID `json:"task_id"`
	AcceptedBy agentcore.AgentID  `json:"accepted_by"`
}

func (TaskAcceptedPayload) EventPayloadVersion() int { return 1 }

type TaskStatusPayload struct {
	TaskID  missioncore.TaskID `json:"task_id"`
	Reason  string             `json:"reason"`
	Message string             `json:"message"`
}

func (TaskStatusPayload) EventPayloadVersion() int { return 1 }

type ReworkRequestedPayload struct {
	TaskID  missioncore.TaskID `json:"task_id"`
	QARef   string             `json:"qa_ref"`
	Reason  string             `json:"reason"`
	Message string             `json:"message"`
}

func (ReworkRequestedPayload) EventPayloadVersion() int { return 1 }

type ReworkAssignedPayload struct {
	TaskID    missioncore.TaskID `json:"task_id"`
	ReworkRef string             `json:"rework_ref"`
	Reason    string             `json:"reason"`
	Message   string             `json:"message"`
}

func (ReworkAssignedPayload) EventPayloadVersion() int { return 1 }

type ReworkResolvedPayload struct {
	TaskID    missioncore.TaskID `json:"task_id"`
	ReworkRef string             `json:"rework_ref"`
	Reason    string             `json:"reason"`
	Message   string             `json:"message"`
}

func (ReworkResolvedPayload) EventPayloadVersion() int { return 1 }

type AgentResultSubmittedPayload struct {
	TaskID    missioncore.TaskID `json:"task_id"`
	AgentID   agentcore.AgentID  `json:"agent_id"`
	ResultRef string             `json:"result_ref"`
	Summary   string             `json:"summary"`
}

func (AgentResultSubmittedPayload) EventPayloadVersion() int { return 1 }

type SummarySubmittedPayload struct {
	TaskID     missioncore.TaskID `json:"task_id"`
	AgentID    agentcore.AgentID  `json:"agent_id"`
	SummaryRef string             `json:"summary_ref"`
	Summary    string             `json:"summary"`
	SourceRefs []string           `json:"source_refs"`
}

func (SummarySubmittedPayload) EventPayloadVersion() int { return 1 }

type DeliverableSubmittedPayload struct {
	TaskID                  missioncore.TaskID      `json:"task_id"`
	AgentID                 agentcore.AgentID       `json:"agent_id"`
	DeliverableID           agentcore.DeliverableID `json:"deliverable_id"`
	ExpectedDeliverableID   string                  `json:"expected_deliverable_id"`
	Title                   string                  `json:"title"`
	ArtifactRef             agentcore.ArtifactRef   `json:"artifact_ref"`
	VisibleToPermissionKeys []string                `json:"visible_to_permission_keys,omitempty"`
}

func (DeliverableSubmittedPayload) EventPayloadVersion() int { return 1 }

type EvidenceSubmittedPayload struct {
	TaskID                  missioncore.TaskID     `json:"task_id"`
	AgentID                 agentcore.AgentID      `json:"agent_id"`
	EvidenceID              agentcore.EvidenceID   `json:"evidence_id"`
	EvidenceRequirementID   string                 `json:"evidence_requirement_id"`
	AcceptanceCriterionID   string                 `json:"acceptance_criterion_id"`
	Kind                    agentcore.EvidenceKind `json:"kind"`
	ArtifactRef             agentcore.ArtifactRef  `json:"artifact_ref"`
	ToolLogRef              agentcore.ToolLogRef   `json:"tool_log_ref"`
	ExternalURL             string                 `json:"external_url"`
	VisibleToPermissionKeys []string               `json:"visible_to_permission_keys,omitempty"`
}

func (EvidenceSubmittedPayload) EventPayloadVersion() int { return 1 }

type QAPayload struct {
	TaskID      missioncore.TaskID    `json:"task_id"`
	QARef       string                `json:"qa_ref"`
	Status      string                `json:"status"`
	Reason      string                `json:"reason"`
	Message     string                `json:"message"`
	ArtifactRef agentcore.ArtifactRef `json:"artifact_ref,omitempty"`
}

func (QAPayload) EventPayloadVersion() int { return 1 }

type DiscussionGroupCreatedPayload struct {
	DiscussionGroupID string              `json:"discussion_group_id"`
	MemberIDs         []agentcore.AgentID `json:"member_ids"`
}

func (DiscussionGroupCreatedPayload) EventPayloadVersion() int { return 1 }

type MessagePostedPayload struct {
	MessageID         string              `json:"message_id"`
	DiscussionGroupID string              `json:"discussion_group_id"`
	AuthorID          agentcore.AgentID   `json:"author_id"`
	AuthorType        string              `json:"author_type,omitempty"`
	Body              string              `json:"body"`
	Mentions          []agentcore.AgentID `json:"mentions"`
	ReplyToMessageID  string              `json:"reply_to_message_id"`
	Visibility        []string            `json:"visibility,omitempty"`
}

func (MessagePostedPayload) EventPayloadVersion() int { return 1 }

type InboxItemPayload struct {
	InboxItemID string            `json:"inbox_item_id"`
	OwnerID     agentcore.AgentID `json:"owner_id"`
	MessageID   string            `json:"message_id"`
	Reason      string            `json:"reason"`
}

func (InboxItemPayload) EventPayloadVersion() int { return 1 }

type DecisionRecordedPayload struct {
	DecisionID string   `json:"decision_id"`
	Summary    string   `json:"summary"`
	SourceRefs []string `json:"source_refs"`
}

func (DecisionRecordedPayload) EventPayloadVersion() int { return 1 }

type ApprovalDecidedPayload struct {
	ApprovalRef  string            `json:"approval_ref"`
	ApprovalType string            `json:"approval_type"`
	Decision     string            `json:"decision"`
	ActionID     string            `json:"action_id,omitempty"`
	Message      string            `json:"message,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

func (ApprovalDecidedPayload) EventPayloadVersion() int { return 1 }

type MemoryCandidatePayload struct {
	CandidateID string   `json:"candidate_id"`
	Summary     string   `json:"summary"`
	SourceRefs  []string `json:"source_refs"`
	Reason      string   `json:"reason,omitempty"`
}

func (MemoryCandidatePayload) EventPayloadVersion() int { return 1 }

type MemoryRecordPayload struct {
	MemoryID   string   `json:"memory_id"`
	Summary    string   `json:"summary"`
	SourceRefs []string `json:"source_refs"`
	Reason     string   `json:"reason,omitempty"`
}

func (MemoryRecordPayload) EventPayloadVersion() int { return 1 }

type MemoryDigestPayload struct {
	DigestID   string   `json:"digest_id"`
	Summary    string   `json:"summary"`
	SourceRefs []string `json:"source_refs"`
}

func (MemoryDigestPayload) EventPayloadVersion() int { return 1 }

type MemoryAccessDeniedPayload struct {
	SubjectID string `json:"subject_id"`
	Reason    string `json:"reason"`
}

func (MemoryAccessDeniedPayload) EventPayloadVersion() int { return 1 }

type BlockerRaisedPayload struct {
	BlockerID string             `json:"blocker_id"`
	TaskID    missioncore.TaskID `json:"task_id"`
	RaisedBy  agentcore.AgentID  `json:"raised_by"`
	Summary   string             `json:"summary"`
}

func (BlockerRaisedPayload) EventPayloadVersion() int { return 1 }

type RuntimeLifecyclePayload struct {
	TaskID                missioncore.TaskID               `json:"task_id"`
	AgentID               agentcore.AgentID                `json:"agent_id"`
	RunID                 agentcore.AgentRunID             `json:"run_id"`
	RuntimeKind           string                           `json:"runtime_kind,omitempty"`
	QueueID               string                           `json:"queue_id,omitempty"`
	Status                string                           `json:"status"`
	WorkerID              string                           `json:"worker_id,omitempty"`
	Message               string                           `json:"message,omitempty"`
	LastHeartbeatAt       time.Time                        `json:"last_heartbeat_at,omitempty"`
	LeaseExpiresAt        time.Time                        `json:"lease_expires_at,omitempty"`
	Usage                 map[string]agentcore.TokenUsage  `json:"usage,omitempty"`
	ResponsibilityContext *agentcore.ResponsibilityContext `json:"responsibility_context,omitempty"`
}

func (RuntimeLifecyclePayload) EventPayloadVersion() int { return 1 }

type RuntimeFailurePayload struct {
	TaskID                missioncore.TaskID               `json:"task_id"`
	AgentID               agentcore.AgentID                `json:"agent_id"`
	RunID                 agentcore.AgentRunID             `json:"run_id"`
	Code                  string                           `json:"code"`
	Message               string                           `json:"message"`
	Metadata              map[string]string                `json:"metadata,omitempty"`
	ResponsibilityContext *agentcore.ResponsibilityContext `json:"responsibility_context,omitempty"`
}

func (RuntimeFailurePayload) EventPayloadVersion() int { return 1 }

type ResultCommittedPayload struct {
	Result agentcore.AgentResult
}

func (ResultCommittedPayload) ValidateSignalPayload() error { return nil }
