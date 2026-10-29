package projection

import (
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

type MissionStatus string

const (
	MissionStatusActive    MissionStatus = "active"
	MissionStatusPaused    MissionStatus = "paused"
	MissionStatusCompleted MissionStatus = "completed"
	MissionStatusCancelled MissionStatus = "cancelled"
)

type TaskStatus string

const (
	TaskStatusCreated         TaskStatus = "created"
	TaskStatusAssigned        TaskStatus = "assigned"
	TaskStatusAccepted        TaskStatus = "accepted"
	TaskStatusStarted         TaskStatus = "started"
	TaskStatusBlocked         TaskStatus = "blocked"
	TaskStatusSubmitted       TaskStatus = "submitted"
	TaskStatusReworkRequested TaskStatus = "rework_requested"
	TaskStatusApproved        TaskStatus = "approved"
	TaskStatusRejected        TaskStatus = "rejected"
	TaskStatusCancelled       TaskStatus = "cancelled"
)

type QAStatus string

const (
	QAStatusNone      QAStatus = ""
	QAStatusRequested QAStatus = "requested"
	QAStatusApproved  QAStatus = "approved"
	QAStatusRejected  QAStatus = "rejected"
)

type ReworkStatus string

const (
	ReworkStatusOpen     ReworkStatus = "open"
	ReworkStatusAssigned ReworkStatus = "assigned"
	ReworkStatusResolved ReworkStatus = "resolved"
)

type RuntimeSessionStatus string

const (
	RuntimeSessionQueued    RuntimeSessionStatus = "queued"
	RuntimeSessionRunning   RuntimeSessionStatus = "running"
	RuntimeSessionCompleted RuntimeSessionStatus = "completed"
	RuntimeSessionFailed    RuntimeSessionStatus = "failed"
	RuntimeSessionTimedOut  RuntimeSessionStatus = "timed_out"
	RuntimeSessionCancelled RuntimeSessionStatus = "cancelled"
	RuntimeSessionBlocked   RuntimeSessionStatus = "blocked"
)

type TeamMemberStatus string

const (
	TeamMemberInvited  TeamMemberStatus = "invited"
	TeamMemberJoined   TeamMemberStatus = "joined"
	TeamMemberReleased TeamMemberStatus = "released"
)

type ReadModel struct {
	TenantID         string
	Mission          MissionView
	Team             TeamView
	TaskBoard        TaskBoardView
	Evidence         EvidenceView
	QA               QAView
	ApprovalRequests []ApprovalRequestView
	RuntimeSessions  []RuntimeSessionView
	Discussion       DiscussionView
	InboxSummary     InboxSummaryView
	ProgressGraph    ProgressGraphView
	LatestDecisions  []DecisionView
	Blockers         []BlockerView
	UpdatedAt        time.Time

	Checkpoint             Checkpoint
	AppliedEventIDs        []string
	AppliedIdempotencyKeys []string
	seenEventIDs           map[string]struct{}
	seenIdempotencyKeys    map[string]struct{}
}

type Checkpoint struct {
	MissionID missioncore.MissionID
	Version   int
	UpdatedAt time.Time
}

type MissionView struct {
	TenantID                         string
	MissionID                        missioncore.MissionID
	Title                            string
	FinalGoal                        string
	Status                           MissionStatus
	OriginKind                       string
	OriginID                         string
	OriginTodoID                     string
	ExecutionAttempt                 int
	DiscussionMode                   string
	OriginDevelopmentProjectID       string
	PersonalAssistantOriginOwnerID   string `json:"personal_assistant_origin_owner_id,omitempty"`
	PersonalAssistantOriginSubjectID string `json:"personal_assistant_origin_subject_id,omitempty"`
	CreatedByID                      string
	CreatedByKind                    string
	CreatedAt                        time.Time
}

type TeamView struct {
	Members          []TeamMemberView
	Assignments      []AssignmentView
	StaffingRequests []StaffingRequestView
}

type TeamMemberView struct {
	MemberID        string
	AgentID         agentcore.AgentID
	AgentProfileID  agentcore.AgentProfileID
	AgentInstanceID agentcore.AgentInstanceID
	AgentKind       agentcore.AgentKind
	DisplayName     string
	RealName        string
	RoleName        string
	Department      string
	Position        string
	MissionRole     string
	Status          TeamMemberStatus
	JoinedAt        time.Time
}

type AssignmentView struct {
	AssignmentID    string
	MemberID        string
	AgentID         agentcore.AgentID
	AgentProfileID  agentcore.AgentProfileID
	AgentInstanceID agentcore.AgentInstanceID
	AgentKind       agentcore.AgentKind
	TaskID          missioncore.TaskID
	MissionRole     string
	ChangedAt       time.Time
}

type StaffingRequestView struct {
	RequestID string
	RoleName  string
	Reason    string
	CreatedAt time.Time
}

type TaskBoardView struct {
	Tasks        []TaskCardView
	Dependencies []TaskDependencyView
}

type TaskCardView struct {
	TaskID                    missioncore.TaskID
	Assignee                  agentcore.AgentID
	Goal                      string
	SourceKind                string
	SourceID                  string
	SourceStepID              string
	ExecutionPermissionScopes []string
	ExecutionAllowedToolIDs   []string
	ExecutionEnvelope         *agentcore.ExecutionEnvelope
	WorkerPersonalGoalSummary string
	Status                    TaskStatus
	Priority                  string
	DueAt                     time.Time
	AcceptanceCriteria        []AcceptanceCriterionView
	ExpectedDeliverables      []ExpectedDeliverableView
	RequiredEvidence          []RequiredEvidenceView
	LatestResultRef           string
	QAStatus                  QAStatus
	BlockerRefs               []string
	UpdatedAt                 time.Time
}

type AcceptanceCriterionView struct {
	ID          string
	Description string
}

type ExpectedDeliverableView struct {
	ID    string
	Title string
}

type RequiredEvidenceView struct {
	ID          string
	Description string
	Kind        string
	Required    bool
}

type TaskDependencyView struct {
	TaskID    missioncore.TaskID
	DependsOn missioncore.TaskID
}

type EvidenceView struct {
	EvidenceItems       []EvidenceItemView
	MissingRequirements []RequiredEvidenceView
}

type EvidenceItemView struct {
	EvidenceID              agentcore.EvidenceID
	TaskID                  missioncore.TaskID
	AgentID                 agentcore.AgentID
	EvidenceRequirementID   string
	Kind                    agentcore.EvidenceKind
	ArtifactRef             agentcore.ArtifactRef
	ToolLogRef              agentcore.ToolLogRef
	ExternalURL             string
	VisibleToPermissionKeys []string
	SubmittedAt             time.Time
}

type QAView struct {
	Checks         []QACheckView
	Approvals      []QADecisionView
	Rejections     []QADecisionView
	ReworkRequests []ReworkRequestView
}

type QACheckView struct {
	QARef     string
	TaskID    missioncore.TaskID
	Status    QAStatus
	Message   string
	CreatedAt time.Time
}

type QADecisionView struct {
	QARef     string
	TaskID    missioncore.TaskID
	Reason    string
	Message   string
	DecidedAt time.Time
}

type ApprovalRequestView struct {
	RequestID    string
	ApprovalType string
	Status       string
	Priority     string
	MissionID    missioncore.MissionID
	TaskID       missioncore.TaskID
	SourceType   string
	SourceID     string
	Title        string
	Summary      string
	RequestedBy  agentcore.AgentID
	AssignedTo   agentcore.AgentID
	EvidenceRefs []string
	Metadata     map[string]string
	CreatedAt    time.Time
	DecidedAt    time.Time
}

type ReworkRequestView struct {
	TaskID     missioncore.TaskID
	QARef      string
	Reason     string
	Message    string
	Status     ReworkStatus
	CreatedAt  time.Time
	ResolvedAt time.Time
}

type RuntimeSessionView struct {
	RunID                 agentcore.AgentRunID
	AgentID               agentcore.AgentID
	TaskID                missioncore.TaskID
	RuntimeKind           string
	QueueID               string
	Status                RuntimeSessionStatus
	WorkerID              string
	LastEvent             string
	LastMessage           string
	ErrorCode             string
	ErrorSummary          string
	QueuedAt              time.Time
	StartedAt             time.Time
	CompletedAt           time.Time
	LastHeartbeatAt       time.Time
	LeaseExpiresAt        time.Time
	IsAlive               bool
	IsStale               bool
	Usage                 map[string]agentcore.TokenUsage
	ResponsibilityContext *agentcore.ResponsibilityContext
	UpdatedAt             time.Time
}

type DiscussionView struct {
	DiscussionGroupID   string
	ConversationID      string
	Messages            []MessageView
	PendingMentions     []MentionView
	Decisions           []DecisionView
	Blockers            []BlockerView
	DiscussionDigestRef string
}

type MessageView struct {
	MessageID         string
	DiscussionGroupID string
	AuthorID          agentcore.AgentID
	AuthorType        string
	Body              string
	Mentions          []agentcore.AgentID
	ReplyToMessageID  string
	Visibility        []string
	PostedAt          time.Time
}

type MentionView struct {
	MessageID string
	AgentID   agentcore.AgentID
	CreatedAt time.Time
}

type InboxSummaryView struct {
	OpenCount int
	Items     []InboxItemView
}

type InboxItemView struct {
	InboxItemID string
	OwnerID     agentcore.AgentID
	MessageID   string
	Reason      string
	Status      string
	CreatedAt   time.Time
	ClosedAt    time.Time
}

type DecisionView struct {
	DecisionID string
	Summary    string
	SourceRefs []string
	RecordedAt time.Time
}

type BlockerView struct {
	BlockerID string
	TaskID    missioncore.TaskID
	RaisedBy  agentcore.AgentID
	Summary   string
	RaisedAt  time.Time
}

type ProgressGraphView struct {
	Nodes        []ProgressNodeView
	Edges        []ProgressEdgeView
	CurrentFocus []string
}

type ProgressNodeView struct {
	ID         string
	EventID    string
	EventType  string
	Label      string
	Status     string
	Details    string
	OccurredAt time.Time
	RefType    string
	RefID      string
}

type ProgressEdgeView struct {
	From string
	To   string
	Kind string
}

func NewReadModel(missionID missioncore.MissionID) *ReadModel {
	return NewReadModelForTenant("global", missionID)
}

func NewReadModelForTenant(tenantID string, missionID missioncore.MissionID) *ReadModel {
	tenantID = normalizeTenantID(tenantID)
	model := &ReadModel{
		TenantID:   tenantID,
		Mission:    MissionView{TenantID: tenantID, MissionID: missionID},
		Checkpoint: Checkpoint{MissionID: missionID},
	}
	model.ensureRuntimeState()
	return model
}

func normalizeTenantID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "global"
	}
	return value
}

func (m *ReadModel) ensureRuntimeState() {
	if m == nil {
		return
	}
	if m.seenEventIDs == nil {
		m.seenEventIDs = makeSet(m.AppliedEventIDs)
	}
	if m.seenIdempotencyKeys == nil {
		m.seenIdempotencyKeys = makeSet(m.AppliedIdempotencyKeys)
	}
}

func (m *ReadModel) markApplied(eventID, idempotencyKey string) {
	m.ensureRuntimeState()
	if eventID != "" {
		if _, ok := m.seenEventIDs[eventID]; !ok {
			m.seenEventIDs[eventID] = struct{}{}
			m.AppliedEventIDs = append(m.AppliedEventIDs, eventID)
		}
	}
	if idempotencyKey != "" {
		if _, ok := m.seenIdempotencyKeys[idempotencyKey]; !ok {
			m.seenIdempotencyKeys[idempotencyKey] = struct{}{}
			m.AppliedIdempotencyKeys = append(m.AppliedIdempotencyKeys, idempotencyKey)
		}
	}
}

func makeSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}
