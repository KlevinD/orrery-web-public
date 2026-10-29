package missioncontrol

import "time"

type MissionControlViewDTO struct {
	Mission         MissionDTO               `json:"mission"`
	Summary         MissionControlSummaryDTO `json:"summary"`
	Team            TeamDTO                  `json:"team"`
	TaskBoard       TaskBoardDTO             `json:"task_board"`
	Evidence        EvidenceDTO              `json:"evidence"`
	QA              QADTO                    `json:"qa"`
	Discussion      DiscussionDTO            `json:"discussion"`
	InboxSummary    InboxSummaryDTO          `json:"inbox_summary"`
	ProgressGraph   ProgressGraphDTO         `json:"progress_graph"`
	LatestDecisions []DecisionDTO            `json:"latest_decisions"`
	Blockers        []BlockerDTO             `json:"blockers"`
	UpdatedAt       time.Time                `json:"updated_at"`
	Version         int                      `json:"version"`
}

type MissionControlSummaryDTO struct {
	ActiveTaskCount      int       `json:"active_task_count"`
	PendingApprovalCount int       `json:"pending_approval_count"`
	RiskCount            int       `json:"risk_count"`
	BlockerCount         int       `json:"blocker_count"`
	TeamSize             int       `json:"team_size"`
	DeliveryRate         float64   `json:"delivery_rate"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type MissionDTO struct {
	MissionID                  string    `json:"mission_id"`
	Title                      string    `json:"title"`
	FinalGoal                  string    `json:"final_goal"`
	Status                     string    `json:"status"`
	Phase                      string    `json:"phase,omitempty"`
	PhaseUpdatedAt             time.Time `json:"phase_updated_at,omitempty"`
	Deadline                   time.Time `json:"deadline,omitempty"`
	RiskLevel                  string    `json:"risk_level,omitempty"`
	OriginKind                 string    `json:"origin_kind,omitempty"`
	OriginID                   string    `json:"origin_id,omitempty"`
	OriginTodoID               string    `json:"origin_todo_id,omitempty"`
	ExecutionAttempt           int       `json:"execution_attempt,omitempty"`
	DiscussionMode             string    `json:"discussion_mode,omitempty"`
	ConversationID             string    `json:"conversation_id,omitempty"`
	OriginDevelopmentProjectID string    `json:"origin_development_project_id,omitempty"`
	CreatedAt                  time.Time `json:"created_at"`
}

type MissionListDTO struct {
	Missions  []MissionDTO        `json:"missions"`
	Summaries []MissionSummaryDTO `json:"summaries"`
	Page      PageDTO             `json:"page"`
}

type MissionSummaryDTO struct {
	MissionID            string    `json:"mission_id"`
	Title                string    `json:"title"`
	Status               string    `json:"status"`
	Phase                string    `json:"phase"`
	RiskLevel            string    `json:"risk_level"`
	TeamSize             int       `json:"team_size"`
	ActiveTaskCount      int       `json:"active_task_count"`
	ActiveRunCount       int       `json:"active_run_count"`
	PendingApprovalCount int       `json:"pending_approval_count"`
	BlockerCount         int       `json:"blocker_count"`
	DeliveryRate         float64   `json:"delivery_rate"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type PageDTO struct {
	Limit    int  `json:"limit,omitempty"`
	Offset   int  `json:"offset,omitempty"`
	Total    int  `json:"total"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

type TeamDTO struct {
	Members          []TeamMemberDTO      `json:"members"`
	Assignments      []AssignmentDTO      `json:"assignments"`
	StaffingRequests []StaffingRequestDTO `json:"staffing_requests"`
}

type TeamMemberDTO struct {
	AgentID           string    `json:"agent_id"`
	AgentProfileID    string    `json:"agent_profile_id,omitempty"`
	AgentInstanceID   string    `json:"agent_instance_id,omitempty"`
	AgentKind         string    `json:"agent_kind,omitempty"`
	DisplayName       string    `json:"display_name"`
	RealName          string    `json:"real_name"`
	RoleName          string    `json:"role_name"`
	Department        string    `json:"department"`
	Position          string    `json:"position"`
	MissionRole       string    `json:"mission_role"`
	Status            string    `json:"status"`
	JoinedAt          time.Time `json:"joined_at"`
	PersonalMemoryRef string    `json:"personal_memory_ref,omitempty"`
}

type AssignmentDTO struct {
	AssignmentID    string    `json:"assignment_id"`
	AgentID         string    `json:"agent_id"`
	AgentProfileID  string    `json:"agent_profile_id,omitempty"`
	AgentInstanceID string    `json:"agent_instance_id,omitempty"`
	AgentKind       string    `json:"agent_kind,omitempty"`
	TaskID          string    `json:"task_id"`
	MissionRole     string    `json:"mission_role"`
	ChangedAt       time.Time `json:"changed_at"`
}

type StaffingRequestDTO struct {
	RequestID string    `json:"request_id"`
	RoleName  string    `json:"role_name"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

type TaskBoardDTO struct {
	Tasks        []TaskCardDTO       `json:"tasks"`
	Dependencies []TaskDependencyDTO `json:"dependencies"`
}

type TaskCardDTO struct {
	TaskID                    string                   `json:"task_id"`
	Title                     string                   `json:"title,omitempty"`
	Assignee                  string                   `json:"assignee"`
	AssigneeDisplayName       string                   `json:"assignee_display_name,omitempty"`
	AssigneeType              string                   `json:"assignee_type,omitempty"`
	Goal                      string                   `json:"goal"`
	SourceKind                string                   `json:"source_kind,omitempty"`
	SourceID                  string                   `json:"source_id,omitempty"`
	SourceStepID              string                   `json:"source_step_id,omitempty"`
	ExecutionPermissionScopes []string                 `json:"execution_permission_scopes,omitempty"`
	WorkerPersonalGoalSummary string                   `json:"worker_personal_goal_summary"`
	Status                    string                   `json:"status"`
	Priority                  string                   `json:"priority,omitempty"`
	DueAt                     time.Time                `json:"due_at,omitempty"`
	AcceptanceCriteria        []AcceptanceCriterionDTO `json:"acceptance_criteria"`
	ExpectedDeliverables      []ExpectedDeliverableDTO `json:"expected_deliverables"`
	RequiredEvidence          []RequiredEvidenceDTO    `json:"required_evidence"`
	LatestResultRef           string                   `json:"latest_result_ref"`
	QAStatus                  string                   `json:"qa_status"`
	BlockerRefs               []string                 `json:"blocker_refs"`
	BlockedReason             string                   `json:"blocked_reason,omitempty"`
	UpdatedAt                 time.Time                `json:"updated_at"`
}

type AcceptanceCriterionDTO struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type ExpectedDeliverableDTO struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type RequiredEvidenceDTO struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Required    bool   `json:"required"`
}

type TaskDependencyDTO struct {
	TaskID    string `json:"task_id"`
	DependsOn string `json:"depends_on"`
}

type EvidenceDTO struct {
	EvidenceItems       []EvidenceItemDTO     `json:"evidence_items"`
	MissingRequirements []RequiredEvidenceDTO `json:"missing_requirements"`
}

type EvidenceItemDTO struct {
	EvidenceID            string    `json:"evidence_id"`
	TaskID                string    `json:"task_id"`
	AgentID               string    `json:"agent_id"`
	EvidenceRequirementID string    `json:"evidence_requirement_id"`
	Kind                  string    `json:"kind"`
	ArtifactID            string    `json:"artifact_id,omitempty"`
	ArtifactURI           string    `json:"artifact_uri,omitempty"`
	ToolLogRef            string    `json:"tool_log_ref,omitempty"`
	ExternalURL           string    `json:"external_url,omitempty"`
	SubmittedAt           time.Time `json:"submitted_at"`
}

type QADTO struct {
	Checks         []QACheckDTO       `json:"checks"`
	Approvals      []QADecisionDTO    `json:"approvals"`
	Rejections     []QADecisionDTO    `json:"rejections"`
	ReworkRequests []ReworkRequestDTO `json:"rework_requests"`
}

type QACheckDTO struct {
	QARef     string    `json:"qa_ref"`
	TaskID    string    `json:"task_id"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type QADecisionDTO struct {
	QARef     string    `json:"qa_ref"`
	TaskID    string    `json:"task_id"`
	Reason    string    `json:"reason"`
	Message   string    `json:"message"`
	DecidedAt time.Time `json:"decided_at"`
}

type ReworkRequestDTO struct {
	TaskID     string    `json:"task_id"`
	QARef      string    `json:"qa_ref"`
	Reason     string    `json:"reason"`
	Message    string    `json:"message"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	ResolvedAt time.Time `json:"resolved_at"`
}

type DiscussionDTO struct {
	DiscussionGroupID   string        `json:"discussion_group_id"`
	ConversationID      string        `json:"conversation_id,omitempty"`
	Restricted          bool          `json:"restricted,omitempty"`
	Messages            []MessageDTO  `json:"messages"`
	PendingMentions     []MentionDTO  `json:"pending_mentions"`
	Decisions           []DecisionDTO `json:"decisions"`
	Blockers            []BlockerDTO  `json:"blockers"`
	DiscussionDigestRef string        `json:"discussion_digest_ref,omitempty"`
}

type MessageDTO struct {
	MessageID         string            `json:"message_id"`
	DiscussionGroupID string            `json:"discussion_group_id"`
	Kind              string            `json:"kind,omitempty"`
	AuthorID          string            `json:"author_id"`
	AuthorDisplayName string            `json:"author_display_name,omitempty"`
	AuthorType        string            `json:"author_type,omitempty"`
	Body              string            `json:"body"`
	Mentions          []string          `json:"mentions"`
	ReplyToMessageID  string            `json:"reply_to_message_id,omitempty"`
	ReplyPreview      string            `json:"reply_preview,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	PostedAt          time.Time         `json:"posted_at"`
}

type MentionDTO struct {
	MessageID string    `json:"message_id"`
	AgentID   string    `json:"agent_id"`
	CreatedAt time.Time `json:"created_at"`
}

type InboxSummaryDTO struct {
	OpenCount int            `json:"open_count"`
	Items     []InboxItemDTO `json:"items"`
}

type InboxItemDTO struct {
	InboxItemID      string           `json:"inbox_item_id"`
	OwnerID          string           `json:"owner_id"`
	MessageID        string           `json:"message_id"`
	ItemType         string           `json:"item_type,omitempty"`
	Priority         string           `json:"priority,omitempty"`
	SourceType       string           `json:"source_type,omitempty"`
	SourceID         string           `json:"source_id,omitempty"`
	Reason           string           `json:"reason"`
	Status           string           `json:"status"`
	AvailableActions []InboxActionDTO `json:"available_actions,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	ClosedAt         time.Time        `json:"closed_at,omitempty"`
}

type InboxActionDTO struct {
	ActionID string `json:"action_id"`
	Label    string `json:"label"`
	Method   string `json:"method"`
	Path     string `json:"path"`
}

type DecisionDTO struct {
	DecisionID string    `json:"decision_id"`
	Summary    string    `json:"summary"`
	SourceRefs []string  `json:"source_refs"`
	RecordedAt time.Time `json:"recorded_at"`
}

type BlockerDTO struct {
	BlockerID string    `json:"blocker_id"`
	TaskID    string    `json:"task_id"`
	RaisedBy  string    `json:"raised_by"`
	Summary   string    `json:"summary"`
	RaisedAt  time.Time `json:"raised_at"`
}

type ProgressGraphDTO struct {
	Nodes        []ProgressNodeDTO `json:"nodes"`
	Edges        []ProgressEdgeDTO `json:"edges"`
	CurrentFocus []string          `json:"current_focus"`
}

type ProgressNodeDTO struct {
	ID         string    `json:"id"`
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	Label      string    `json:"label"`
	Status     string    `json:"status"`
	Details    string    `json:"details,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
	RefType    string    `json:"ref_type,omitempty"`
	RefID      string    `json:"ref_id,omitempty"`
}

type ProgressEdgeDTO struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

type MissionControlPatchDTO struct {
	PatchID      string                `json:"patch_id"`
	MissionID    string                `json:"mission_id"`
	BaseVersion  int                   `json:"base_version"`
	NextVersion  int                   `json:"next_version"`
	ChangedViews []string              `json:"changed_views"`
	Payload      MissionControlViewDTO `json:"payload"`
	EmittedAt    time.Time             `json:"emitted_at"`
	RawEvents    any                   `json:"-"`
}
