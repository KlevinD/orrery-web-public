package discussionsession

import (
	"context"
	"encoding/json"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

type MissionToolingPort interface {
	Apply(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, calls []ToolCallResult, existingMissions ...LinkedMissionDTO) ([]LinkedMissionDTO, error)
}

type MissionMessagePort interface {
	PostDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, mission LinkedMissionDTO, content string, idempotencyKey string) error
}

type ExecutionDeleter interface {
	DeleteMission(ctx context.Context, viewer missioncontrol.ViewerRef, missionID string) error
	DeleteMissionTask(ctx context.Context, viewer missioncontrol.ViewerRef, missionID string, taskID string) error
}

type ConversationArtifactCleaner interface {
	DeleteConversationArtifacts(ctx context.Context, conversationID string) error
	DeleteRunArtifacts(ctx context.Context, conversationID string, runID string) error
}

type ManagedWorkspaceContext struct {
	RootDir     string
	WorkDir     string
	NetworkMode string
}

type ManagedWorkspaceResolver interface {
	ResolveManagedWorkspace(context.Context, string, string) (ManagedWorkspaceContext, error)
}

type DiscussionRuntimeRecordCleaner interface {
	DeleteDiscussionRuntimeRecord(ctx context.Context, runID string) error
}

type DiscussionRuntimeWorkspaceReleaser interface {
	ReleaseRuntimeWorkspace(ctx context.Context, workspaceRoot string) error
}

type DiscussionAgentMemoryProvider interface {
	DiscussionAgentMemoryContext(ctx context.Context, agentID string) (DiscussionAgentMemoryContext, error)
}

// DiscussionAgentQueryMemoryProvider lets providers rank memory against the
// current turn while preserving compatibility with providers that only expose
// the legacy agent-scoped lookup.
type DiscussionAgentQueryMemoryProvider interface {
	DiscussionAgentMemoryContextForQuery(ctx context.Context, agentID, query string) (DiscussionAgentMemoryContext, error)
}

type DiscussionAgentMemoryAccessContext struct {
	TenantID      string
	PrincipalID   string
	PrincipalKind string
	AgentID       string
}

type DiscussionAgentAuthorizedQueryMemoryProvider interface {
	DiscussionAgentMemoryContextForAuthorizedQuery(ctx context.Context, access DiscussionAgentMemoryAccessContext, agentID, query string) (DiscussionAgentMemoryContext, error)
}

type PersonalAssistantMemoryCapturePort interface {
	CapturePersonalAssistantMemoryExchange(ctx context.Context, exchange PersonalAssistantMemoryExchange) error
}

type PersonalAssistantMemoryExchange struct {
	PreferenceExtractionAllowed bool
	TenantID                    string
	OwnerID                     string
	AgentID                     string
	ConversationID              string
	RunID                       string
	UserMessageID               string
	AssistantMessageID          string
	UserContent                 string
	AssistantContent            string
	OccurredAt                  time.Time
}

type PersonalAssistantPreferenceOwner struct {
	TenantID string `json:"tenant_id"`
	OwnerID  string `json:"owner_id"`
	AgentID  string `json:"agent_id"`
}

type PersonalAssistantPreferenceProvider interface {
	PersonalAssistantPreferences(context.Context, PersonalAssistantPreferenceOwner) (DiscussionAgentMemoryDigest, error)
}

type DiscussionAgentKnowledgeProvider interface {
	DiscussionAgentKnowledgeContext(ctx context.Context, request DiscussionAgentRequest) (DiscussionAgentKnowledgeContext, error)
}

type DiscussionHumanProfileBootstrapPort interface {
	EnsureDiscussionHumanProfile(ctx context.Context, viewer ViewerRef) (agentcore.AgentProfile, error)
}

type EvaluationReviewUsagePort interface {
	RecordEvaluationReviewUsage(ctx context.Context, reviewID string, tokenConsumed int64) error
}

type DiscussionAgentMentionResponder interface {
	RespondToDiscussionMention(ctx context.Context, request DiscussionAgentMentionRequest) (DiscussionAgentResponse, error)
}

type DiscussionAgentMentionStreamResponder interface {
	RespondToDiscussionMentionStream(ctx context.Context, request DiscussionAgentMentionRequest) (<-chan DiscussionAgentStreamEvent, error)
}

type DiscussionMentionConfigProvider interface {
	DiscussionAgentPairLoopLimit(ctx context.Context) (int, error)
}

const (
	RealtimeAggregateConversation = "conversation"
	RealtimeAggregateUser         = "user"
	RealtimeAggregateRun          = "run"
)

const (
	RealtimeEventDiscussionSnapshotInvalidated = "discussion.snapshot.invalidated"
	RealtimeEventDiscussionHILModeUpdated      = "discussion.hil_mode.updated"
	RealtimeEventDiscussionTurnStarted         = "discussion.turn.started"
	RealtimeEventDiscussionTurnCompleted       = "discussion.turn.completed"
	RealtimeEventDiscussionTurnFailed          = "discussion.turn.failed"
	RealtimeEventDiscussionMessageCreated      = "discussion.message.created"
	RealtimeEventDiscussionMessageCompleted    = "discussion.message.completed"
	RealtimeEventDiscussionRunStarted          = "discussion.run.started"
	RealtimeEventDiscussionRunCompleted        = "discussion.run.completed"
	RealtimeEventDiscussionRunFailed           = "discussion.run.failed"
	RealtimeEventDiscussionTokenDelta          = "discussion.token.delta"
	RealtimeEventDiscussionThinkingDelta       = "discussion.thinking.delta"
	RealtimeEventDiscussionToolCall            = "discussion.tool.call"
	RealtimeEventDiscussionToolResult          = "discussion.tool.result"
	RealtimeEventDiscussionPlanUpdated         = "discussion.plan.updated"
	RealtimeEventUserDiscussionListInvalidated = "user.discussion_list.invalidated"
)

type RealtimeEvent struct {
	EventID       string
	Type          string
	Channel       string
	AggregateType string
	AggregateID   string
	Payload       json.RawMessage
}

type RealtimePublisher interface {
	AppendRealtimeEvent(ctx context.Context, event RealtimeEvent) error
}

type DiscussionAgentMentionRequest struct {
	ConversationID                   string
	ConversationKind                 string
	ConversationOwnerID              string
	ConversationTenantID             string
	HILMode                          string
	PersonalAssistantOriginOwnerID   string
	PersonalAssistantOriginSubjectID string
	PersonalAssistantHost            bool
	LogicalAgentID                   string
	RunID                            string
	Mode                             string
	OutputLanguage                   string
	Content                          string
	SourceMessage                    MessageDTO
	TargetAgent                      ConversationMemberDTO
	History                          []MessageDTO
	Runs                             []RunRecord
	Members                          []ConversationMemberDTO
	LinkedMissions                   []LinkedMissionDTO
	Attachments                      []AttachmentDTO
	Workspace                        DiscussionWorkspaceContext
	BoundTools                       []DiscussionAgentBoundTool
	BoundToolsPolicy                 bool
	ProviderSpecific                 map[string]string
	ResumeSessionID                  string
	ForceNewSession                  bool
}

const defaultDiscussionAgentPairLoopLimit = 8

type discussionMentionQueueItem struct {
	Message  MessageRecord
	PairLoop discussionAgentPairLoopState
	IsRoot   bool
}

type discussionMentionPreparedRun struct {
	Target       ConversationMemberRecord
	Run          RunRecord
	Request      DiscussionAgentMentionRequest
	NextPairLoop discussionAgentPairLoopState
}

type discussionMentionRunResult struct {
	Response DiscussionAgentResponse
	Err      error
}

type discussionAgentPairLoopState struct {
	AgentA string
	AgentB string
	Count  int
}

type discussionMentionDispatchOptions struct {
	TurnID               string
	Mode                 string
	SkipInitialTargetIDs map[string]bool
	InitialLatestAt      time.Time
	OnEvent              func(StreamEventDTO) bool
	ActiveRunCancel      context.CancelFunc
	ForceNewSession      bool
	OutputLanguage       string
}
