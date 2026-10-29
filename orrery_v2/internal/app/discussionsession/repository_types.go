package discussionsession

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type ConversationRecord struct {
	ConversationID                   string
	TenantID                         string
	PurposeKind                      string
	PurposeID                        string
	WorkspaceID                      string
	ConversationKind                 string
	DirectAgentID                    string
	DirectTargetKind                 string
	DirectTargetID                   string
	MemberAdditionsAllowed           *bool
	WorkspaceRootDir                 string
	HILMode                          string
	Title                            string
	Status                           string
	CreatedBy                        string
	OwnerID                          string
	PersonalAssistantOriginOwnerID   string
	PersonalAssistantOriginSubjectID string
	CreatedByName                    string
	CreatedAt                        time.Time
	UpdatedAt                        time.Time
	ViewedAt                         time.Time
	Revision                         int64
	ActiveTurnID                     string
}

type MessageRecord struct {
	MessageID            string
	ConversationID       string
	TurnID               string
	RunID                string
	MissionID            string
	MissionTaskID        string
	Role                 string
	AuthorID             string
	AuthorName           string
	Content              string
	Mentions             []MessageMentionRecord
	RoutingSchemaVersion int
	Metadata             map[string]string
	CreatedAt            time.Time
}

type MessageMentionRecord struct {
	TargetKind  string
	TargetID    string
	DisplayName string
}

type ConversationMemberRecord struct {
	ConversationID      string
	MemberID            string
	MemberKind          string
	DisplayName         string
	Role                string
	ManagedSource       string
	ManagedSources      []string
	ExplicitGrantorID   string
	ExplicitGrantorKind string
	ExplicitGrantedAt   time.Time
	CreatedAt           time.Time
}

type RunRecord struct {
	RunID            string
	ConversationID   string
	TurnID           string
	MissionID        string
	MissionTaskID    string
	Mode             string
	HasPlan          bool
	Plan             *agentcore.RuntimePlanState
	AgentID          string
	LogicalAgentID   string
	AgentName        string
	Status           string
	RuntimeKind      string
	SessionID        string
	ThinkingSummary  string
	ToolCalls        []ToolCallDTO
	Usage            ModelUsageMap
	DurationMs       int64
	ProviderMetadata map[string]string
	Debug            *RunDebugDTO
	TraceRef         string
	TraceVersion     int
	TraceStatus      string
	StartedAt        time.Time
	CompletedAt      time.Time
	CreatedAt        time.Time
}

type TurnRecord struct {
	TurnID         string
	ConversationID string
	RootMessageID  string
	IdempotencyKey string
	Mode           string
	Status         string
	ErrorCode      string
	ErrorMessage   string
	RunIDs         []string
	StartedAt      time.Time
	CompletedAt    time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CommandRecord struct {
	CommandID      string
	Operation      string
	ViewerID       string
	ViewerKind     string
	IdempotencyKey string
	Fingerprint    string
	ConversationID string
	MessageID      string
	TurnID         string
	Status         string
	ErrorCode      string
	ErrorMessage   string
	RetryCount     int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type EventRecord struct {
	EventID        string
	ConversationID string
	Kind           string
	Status         string
	Summary        string
	Metadata       map[string]string
	CreatedAt      time.Time
}

type LinkedMissionRecord struct {
	ConversationID string
	MissionID      string
	Title          string
	Status         string
	CreatedAt      time.Time
}

type AttachmentRecord struct {
	AttachmentID     string
	ConversationID   string
	MessageID        string
	FileName         string
	ContentType      string
	SizeBytes        int64
	UploadedBy       string
	UploadedByKind   string
	UploadedByName   string
	StoragePath      string
	WorkspaceRootDir string
	IdempotencyKey   string
	CreatedAt        time.Time
}

type ConversationRecordView struct {
	ConversationRecord
	Messages       []MessageRecord
	Members        []ConversationMemberRecord
	Turns          []TurnRecord
	Runs           []RunRecord
	Events         []EventRecord
	LinkedMissions []LinkedMissionRecord
	Attachments    []AttachmentRecord
}

type TurnAggregateMutation struct {
	Conversation        ConversationRecord
	ExpectedRevision    int64
	RequireAbsent       bool
	Turn                *TurnRecord
	Messages            []MessageRecord
	Members             []ConversationMemberRecord
	Runs                []RunRecord
	Events              []EventRecord
	MissionLinks        []LinkedMissionRecord
	Attachments         []AttachmentRecord
	RequireNoActiveTurn bool
	SupersedeActiveTurn bool
	CompleteTurnID      string
}
