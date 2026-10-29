package discussionsession

import (
	"context"
	"encoding/json"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/retrievalevidence"
)

type DiscussionAgentRunner interface {
	RunDiscussion(ctx context.Context, request DiscussionAgentRequest) (DiscussionAgentResponse, error)
}

type DiscussionAgentStreamRunner interface {
	RunDiscussionStream(ctx context.Context, request DiscussionAgentRequest) (<-chan DiscussionAgentStreamEvent, error)
}

type DiscussionAgentPrewarmer interface {
	PrewarmDiscussion(ctx context.Context, request DiscussionAgentRequest) error
}

type DiscussionAgentRequestRouter interface {
	RouteDiscussionAgentRequest(ctx context.Context, request *DiscussionAgentRequest) error
}

// DiscussionAgentRunSteerer delivers a user interjection to an already-running
// runtime turn. Implementations must return only after the runtime accepts the
// control message.
type DiscussionAgentRunSteerer interface {
	SteerDiscussionRun(ctx context.Context, run RunRecord, message string) error
}

type DiscussionAgentPromptDebugger interface {
	PromptDebug(request DiscussionAgentRequest) *DiscussionAgentPromptDebug
}

type DiscussionAgentIdentityProvider interface {
	DiscussionAgentID() string
}

type DiscussionAgentRunIdentityResolver interface {
	ResolveDiscussionRunAgentID(ctx context.Context, conversation ConversationRecordView) (string, error)
}

type DiscussionAgentRunDisplayNameResolver interface {
	ResolveDiscussionRunAgentDisplayName(ctx context.Context, conversation ConversationRecordView) (string, error)
}

type DiscussionAgentBoundToolProvider interface {
	DiscussionAgentBoundTools() []DiscussionAgentBoundTool
}

type DiscussionAgentBoundSkillProvider interface {
	DiscussionAgentBoundSkills() []DiscussionAgentBoundSkill
}

type DiscussionAgentRequest struct {
	ConversationID                   string
	ConversationKind                 string
	ConversationOwnerID              string
	ConversationTenantID             string
	HILMode                          string
	PersonalAssistantOriginOwnerID   string
	PersonalAssistantOriginSubjectID string
	MessageID                        string
	MissionID                        string
	MissionTaskID                    string
	RunID                            string
	AgentID                          string
	LogicalAgentID                   string
	ExecutionPrincipal               agentcore.ExecutionPrincipal
	DiscussionTitle                  string
	DiscussionGoal                   string
	PersonaPrompt                    string
	PersonalAssistantApprovalContext string
	Viewer                           ViewerRef
	OutputLanguage                   string
	Content                          string
	TriggerMessage                   MessageDTO
	History                          []MessageDTO
	Runs                             []RunRecord
	Members                          []ConversationMemberDTO
	LinkedMissions                   []LinkedMissionDTO
	Attachments                      []AttachmentDTO
	Workspace                        DiscussionWorkspaceContext
	Memory                           DiscussionAgentMemoryContext
	Knowledge                        DiscussionAgentKnowledgeContext
	ActionSemantics                  actioncontract.ActionSemanticFrame
	BoundTools                       []DiscussionAgentBoundTool
	BoundToolsPolicy                 bool
	BoundSkills                      []DiscussionAgentBoundSkill
	ProviderSpecific                 map[string]string
	RuntimeProviderConfig            json.RawMessage
	ResumeSessionID                  string
	ForceNewSession                  bool
	Mode                             string

	PersonalAssistantNotificationContext string
	personalAssistantNotificationScope   string
}

// A prepared request may reuse notification material until execution begins.
// The private marker cannot be supplied by transport JSON or runtime metadata.
func (r *DiscussionAgentRequest) HasPreparedNotificationContext(scope string) bool {
	return r != nil && scope != "" && r.personalAssistantNotificationScope == scope
}

func (r *DiscussionAgentRequest) SetPreparedNotificationContext(scope, material string) {
	r.PersonalAssistantNotificationContext = material
	r.personalAssistantNotificationScope = scope
}

// Execution consumes the preparation snapshot. A request reused for a later
// continuation must read current notification state, even with the same run ID.
func (r *DiscussionAgentRequest) ConsumePreparedNotificationContext() {
	r.personalAssistantNotificationScope = ""
}

type DiscussionWorkspaceContext struct {
	RootDir        string
	AttachmentsDir string
	AgentsDir      string
	AgentID        string
	AgentWorkDir   string
	MemberWorkDirs []DiscussionMemberWorkDir
	NetworkMode    string
	Managed        bool
	fileSnapshot   map[string]workspaceFileState
}

type DiscussionMemberWorkDir struct {
	AgentID     string
	DisplayName string
	Role        string
	Path        string
	Current     bool
}

type DiscussionAgentBoundTool struct {
	ToolID              string
	Alias               string
	Name                string
	Description         string
	Source              string
	ServerToolID        string
	DownstreamToolName  string
	InputSchema         map[string]any
	OutputSchema        map[string]any
	RequiresPostProcess *bool
}

type DiscussionAgentBoundSkill struct {
	SkillID string
	Alias   string
	Name    string
	Summary string
}

type DiscussionAgentMemoryContext struct {
	PersonalDigest    DiscussionAgentMemoryDigest
	CollectiveDigests []DiscussionAgentMemoryDigest
}

type DiscussionAgentMemoryDigest struct {
	Evidence       []retrievalevidence.Item `json:"evidence,omitempty"`
	RetrievalMode  string
	FallbackReason string
	DigestID       string
	MemoryKind     string
	Summary        string
	SourceMemory   []string
	SourceEvidence []string
	Confidence     float64
	Sensitivity    string
}

type DiscussionAgentKnowledgeContext struct {
	Results []DiscussionAgentKnowledgeResult
}

type DiscussionAgentKnowledgeResult struct {
	Evidence        *retrievalevidence.Item `json:"evidence,omitempty"`
	KnowledgeBaseID string
	DocumentID      string
	ChunkID         string
	DocumentName    string
	Score           float32
	Snippet         string
	HeadingPath     []string
}

type DiscussionAgentResponse struct {
	AssistantMessage  string
	Status            string
	RuntimeKind       string
	SessionID         string
	ToolCalls         []ToolCallDTO
	DisplayTimeline   []DiscussionDisplayTimelineSegment `json:"-"`
	Usage             ModelUsageMap
	DurationMs        int64
	ProviderMetadata  map[string]string
	BoundTools        []DiscussionAgentBoundTool
	LinkedMissions    []LinkedMissionDTO
	OutboundPrompt    string
	RuntimeResponse   string
	TraceRef          string `json:"-"`
	ToolLoopCompleted bool   `json:"-"`
}

type DiscussionDisplayTimelineSegment struct {
	Kind       string                      `json:"kind"`
	Text       string                      `json:"text,omitempty"`
	RunID      string                      `json:"run_id,omitempty"`
	ToolCallID string                      `json:"tool_call_id,omitempty"`
	Plan       *agentcore.RuntimePlanState `json:"plan,omitempty"`
}

type DiscussionAgentPromptDebug struct {
	OutboundPrompt string
	Selected       RunDebugSelectedDTO
	Redacted       bool
}

const (
	DiscussionAgentStreamTokenDelta        = "token_delta"
	DiscussionAgentStreamThinkingDelta     = "thinking_delta"
	DiscussionAgentStreamToolCall          = "tool_call"
	DiscussionAgentStreamToolResult        = "tool_result"
	DiscussionAgentStreamApprovalRequested = "approval_requested"
	DiscussionAgentStreamPlan              = "plan"
	DiscussionAgentStreamMessageDone       = "message_completed"
	DiscussionAgentStreamRunCompleted      = "run_completed"
	DiscussionAgentStreamRuntimeError      = "error"
)

type DiscussionAgentStreamEvent struct {
	Kind          string
	Delta         string
	ReasoningKind agentcore.ReasoningContentKind
	ToolCall      ToolCallDTO
	Approval      *EventDTO
	Plan          *agentcore.RuntimePlanState
	Response      DiscussionAgentResponse
	Error         error
}
