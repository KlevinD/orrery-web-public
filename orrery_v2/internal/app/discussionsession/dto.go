package discussionsession

import (
	"io"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type ViewerKind string

const (
	ViewerKindUser   ViewerKind = "user"
	ViewerKindHuman  ViewerKind = "human"
	ViewerKindAgent  ViewerKind = "agent"
	ViewerKindSystem ViewerKind = "system"
)

type ViewerRef struct {
	ViewerID        string
	OwnerID         string
	IdentityIDs     []string
	AgentProfileID  string
	ViewerKind      ViewerKind
	DisplayName     string
	TenantID        string
	Language        string
	PermissionScope []string
}

type ConversationDTO struct {
	ConversationID           string                  `json:"conversation_id"`
	TenantID                 string                  `json:"tenant_id,omitempty"`
	PurposeKind              string                  `json:"purpose_kind,omitempty"`
	PurposeID                string                  `json:"purpose_id,omitempty"`
	WorkspaceID              string                  `json:"workspace_id,omitempty"`
	Revision                 int64                   `json:"revision"`
	ConversationKind         string                  `json:"conversation_kind,omitempty"`
	PersonalAssistantRuntime bool                    `json:"personal_assistant_runtime,omitempty"`
	DirectAgentID            string                  `json:"direct_agent_id,omitempty"`
	DirectTargetKind         string                  `json:"direct_target_kind,omitempty"`
	DirectTargetID           string                  `json:"direct_target_id,omitempty"`
	MemberAdditionsAllowed   bool                    `json:"member_additions_allowed"`
	WorkspaceRootDir         string                  `json:"workspace_root_dir,omitempty"`
	HILMode                  string                  `json:"hil_mode"`
	Title                    string                  `json:"title"`
	Status                   string                  `json:"status"`
	CreatedBy                string                  `json:"created_by"`
	CreatedByName            string                  `json:"created_by_name,omitempty"`
	CreatedAt                time.Time               `json:"created_at"`
	UpdatedAt                time.Time               `json:"updated_at"`
	ViewedAt                 time.Time               `json:"viewed_at,omitempty"`
	UnreadCompleted          bool                    `json:"unread_completed"`
	Messages                 []MessageDTO            `json:"messages"`
	MessagePage              *MessagePageMetaDTO     `json:"message_page,omitempty"`
	Members                  []ConversationMemberDTO `json:"members"`
	Turns                    []TurnDTO               `json:"turns,omitempty"`
	Runs                     []RunDTO                `json:"runs"`
	Events                   []EventDTO              `json:"events,omitempty"`
	LinkedMissions           []LinkedMissionDTO      `json:"linked_missions"`
	Attachments              []AttachmentDTO         `json:"attachments"`
}

type ConversationHILModeDTO struct {
	ConversationID string    `json:"conversation_id"`
	Revision       int64     `json:"revision"`
	HILMode        string    `json:"hil_mode"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ConversationSummaryDTO struct {
	ConversationID           string    `json:"conversation_id"`
	PurposeKind              string    `json:"purpose_kind,omitempty"`
	PurposeID                string    `json:"purpose_id,omitempty"`
	Revision                 int64     `json:"revision"`
	ConversationKind         string    `json:"conversation_kind,omitempty"`
	PersonalAssistantRuntime bool      `json:"personal_assistant_runtime,omitempty"`
	DirectAgentID            string    `json:"direct_agent_id,omitempty"`
	DirectTargetKind         string    `json:"direct_target_kind,omitempty"`
	DirectTargetID           string    `json:"direct_target_id,omitempty"`
	MemberAdditionsAllowed   bool      `json:"member_additions_allowed"`
	WorkspaceRootDir         string    `json:"workspace_root_dir,omitempty"`
	HILMode                  string    `json:"hil_mode"`
	Title                    string    `json:"title"`
	Status                   string    `json:"status"`
	LastMessage              string    `json:"last_message,omitempty"`
	LinkedMissions           int       `json:"linked_missions"`
	AttachmentCount          int       `json:"attachment_count"`
	UpdatedAt                time.Time `json:"updated_at"`
	ViewedAt                 time.Time `json:"viewed_at,omitempty"`
	UnreadCompleted          bool      `json:"unread_completed"`
}

type ConversationPageDTO struct {
	Conversations []ConversationSummaryDTO `json:"conversations"`
	Page          PageDTO                  `json:"page"`
}

type PageDTO struct {
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
	Total    int `json:"total"`
}

type MessagePageDTO struct {
	Messages []MessageDTO       `json:"messages"`
	Page     MessagePageMetaDTO `json:"page"`
}

type MessagePageMetaDTO struct {
	Limit    int  `json:"limit"`
	Offset   int  `json:"offset"`
	Total    int  `json:"total"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

type MessageDTO struct {
	MessageID      string              `json:"message_id"`
	ConversationID string              `json:"conversation_id"`
	TurnID         string              `json:"turn_id,omitempty"`
	RunID          string              `json:"run_id,omitempty"`
	MissionID      string              `json:"mission_id,omitempty"`
	MissionTaskID  string              `json:"mission_task_id,omitempty"`
	Role           string              `json:"role"`
	AuthorID       string              `json:"author_id"`
	AuthorName     string              `json:"author_name,omitempty"`
	Content        string              `json:"content"`
	Mentions       []MessageMentionDTO `json:"mentions,omitempty"`
	Attachments    []AttachmentDTO     `json:"attachments,omitempty"`
	Metadata       map[string]string   `json:"metadata,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
}

type MessageMentionDTO struct {
	TargetKind  string `json:"target_kind"`
	TargetID    string `json:"target_id"`
	DisplayName string `json:"display_name,omitempty"`
}

type ConversationMemberDTO struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	Role          string `json:"role,omitempty"`
	ManagedSource string `json:"managed_source,omitempty"`
}

type RunDTO struct {
	RunID           string                      `json:"run_id"`
	ConversationID  string                      `json:"conversation_id"`
	TurnID          string                      `json:"turn_id,omitempty"`
	MissionID       string                      `json:"mission_id,omitempty"`
	MissionTaskID   string                      `json:"mission_task_id,omitempty"`
	Mode            string                      `json:"mode,omitempty"`
	HasPlan         bool                        `json:"has_plan,omitempty"`
	Plan            *agentcore.RuntimePlanState `json:"plan,omitempty"`
	AgentID         string                      `json:"agent_id"`
	LogicalAgentID  string                      `json:"logical_agent_id,omitempty"`
	AgentName       string                      `json:"agent_name,omitempty"`
	Status          string                      `json:"status"`
	RuntimeKind     string                      `json:"runtime_kind"`
	SessionID       string                      `json:"session_id,omitempty"`
	ThinkingSummary string                      `json:"thinking_summary,omitempty"`
	ToolCalls       []ToolCallDTO               `json:"tool_calls,omitempty"`
	Usage           ModelUsageMap               `json:"usage,omitempty"`
	DurationMs      int64                       `json:"duration_ms,omitempty"`
	TraceAvailable  bool                        `json:"trace_available"`
	TraceStatus     string                      `json:"trace_status,omitempty"`
	StartedAt       time.Time                   `json:"started_at,omitempty"`
	CompletedAt     time.Time                   `json:"completed_at,omitempty"`
	CreatedAt       time.Time                   `json:"created_at"`
}

type TurnDTO struct {
	TurnID         string    `json:"turn_id"`
	ConversationID string    `json:"conversation_id"`
	RootMessageID  string    `json:"root_message_id"`
	Mode           string    `json:"mode"`
	Status         string    `json:"status"`
	ErrorCode      string    `json:"error_code,omitempty"`
	ErrorMessage   string    `json:"error_message,omitempty"`
	RunIDs         []string  `json:"run_ids,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ModelUsageMap map[string]ModelUsageDTO

type ModelUsageDTO struct {
	// Subsets of the inclusive input/output totals. Known covers every call.
	CachedInputTokens      int64 `json:"cached_input_tokens,omitempty"`
	ReasoningTokens        int64 `json:"reasoning_tokens,omitempty"`
	CachedInputTokensKnown bool  `json:"cached_input_tokens_known,omitempty"`
	ReasoningTokensKnown   bool  `json:"reasoning_tokens_known,omitempty"`
	InputTokens            int64 `json:"input_tokens,omitempty"`
	OutputTokens           int64 `json:"output_tokens,omitempty"`
	CacheReadTokens        int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens       int64 `json:"cache_write_tokens,omitempty"`
	TotalTokens            int64 `json:"total_tokens,omitempty"`
}

type RunDebugDTO struct {
	ConversationID   string              `json:"conversation_id"`
	RunID            string              `json:"run_id"`
	OutboundPrompt   string              `json:"outbound_prompt"`
	RuntimeResponse  string              `json:"runtime_response"`
	ProviderMetadata map[string]string   `json:"provider_metadata,omitempty"`
	Selected         RunDebugSelectedDTO `json:"selected"`
	CreatedAt        time.Time           `json:"created_at"`
	Redacted         bool                `json:"redacted"`
}

type RunDebugSelectedDTO struct {
	AgentID     string `json:"agent_id"`
	RuntimeKind string `json:"runtime_kind"`
	Model       string `json:"model,omitempty"`
	ConfigRef   string `json:"config_ref,omitempty"`
}

type ToolCallDTO struct {
	ToolCallID string            `json:"tool_call_id"`
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	Arguments  map[string]string `json:"arguments,omitempty"`
	Result     map[string]string `json:"result,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
}

type EventDTO struct {
	EventID          string              `json:"event_id"`
	ConversationID   string              `json:"conversation_id"`
	Kind             string              `json:"kind"`
	Status           string              `json:"status,omitempty"`
	Summary          string              `json:"summary,omitempty"`
	Metadata         map[string]string   `json:"metadata,omitempty"`
	AvailableActions []ApprovalActionDTO `json:"available_actions,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
}

type ApprovalActionDTO struct {
	ActionID             string `json:"action_id"`
	Label                string `json:"label"`
	Style                string `json:"style,omitempty"`
	Decision             string `json:"decision,omitempty"`
	Effect               string `json:"effect,omitempty"`
	InputKind            string `json:"input_kind,omitempty"`
	DefaultMessage       string `json:"default_message,omitempty"`
	Method               string `json:"method"`
	Path                 string `json:"path"`
	RequiresBody         bool   `json:"requires_body"`
	ConfirmationRequired bool   `json:"confirmation_required"`
	ReasonRequired       bool   `json:"reason_required"`
	AllowsModification   bool   `json:"allows_modification,omitempty"`
	RememberAllowed      bool   `json:"remember_allowed,omitempty"`
}

type AttachmentDTO struct {
	AttachmentID   string    `json:"attachment_id"`
	ConversationID string    `json:"conversation_id"`
	MessageID      string    `json:"message_id,omitempty"`
	FileName       string    `json:"file_name"`
	ContentType    string    `json:"content_type,omitempty"`
	SizeBytes      int64     `json:"size_bytes"`
	UploadedBy     string    `json:"uploaded_by"`
	UploadedByKind string    `json:"uploaded_by_kind,omitempty"`
	UploadedByName string    `json:"uploaded_by_name,omitempty"`
	StoragePath    string    `json:"storage_path,omitempty"`
	DownloadURL    string    `json:"download_url"`
	PreviewURL     string    `json:"preview_url,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type AttachmentPageDTO struct {
	Attachments []AttachmentDTO `json:"attachments"`
}

type AttachmentDraftDTO struct {
	Conversation ConversationDTO `json:"conversation"`
	Attachment   AttachmentDTO   `json:"attachment"`
}

type UploadAttachmentCommand struct {
	ConversationID       string
	ConversationKind     string
	ForceNewConversation bool
	DirectAgentID        string
	DirectTargetKind     string
	DirectTargetID       string
	FileName             string
	ContentType          string
	SizeBytes            int64
	Reader               io.Reader
	IdempotencyKey       string
}

type AttachmentDownloadDTO struct {
	Attachment  AttachmentDTO
	Reader      io.ReadCloser
	FileName    string
	ContentType string
	SizeBytes   int64
}

type RetryRequestDTO struct {
	RequestID        string    `json:"request_id"`
	ConversationID   string    `json:"conversation_id"`
	MessageID        string    `json:"message_id"`
	Status           string    `json:"status"`
	RequestedBy      string    `json:"requested_by"`
	ApprovedBy       string    `json:"approved_by,omitempty"`
	RequiresApproval bool      `json:"requires_approval"`
	CreatedAt        time.Time `json:"created_at"`
	ApprovedAt       time.Time `json:"approved_at,omitempty"`
}

type LinkedMissionDTO struct {
	MissionID         string    `json:"mission_id"`
	Title             string    `json:"title"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
	ReplacesMissionID string    `json:"-"`
}

const (
	StreamEventSnapshot          = "snapshot"
	StreamEventRunStarted        = "run_started"
	StreamEventTokenDelta        = "token_delta"
	StreamEventThinkingDelta     = "thinking_delta"
	StreamEventToolCall          = "tool_call"
	StreamEventToolResult        = "tool_result"
	StreamEventApprovalRequested = "approval_requested"
	StreamEventPlan              = "plan"
	StreamEventMessageCompleted  = "message_completed"
	StreamEventRunCompleted      = "run_completed"
	StreamEventRunFailed         = "run_failed"
	StreamEventTurnStarted       = "turn_started"
	StreamEventTurnCompleted     = "turn_completed"
	StreamEventTurnFailed        = "turn_failed"
	StreamEventError             = "error"
)

type StreamEventDTO struct {
	Kind           string                      `json:"kind"`
	EventID        string                      `json:"event_id,omitempty"`
	TurnID         string                      `json:"turn_id,omitempty"`
	ConversationID string                      `json:"conversation_id,omitempty"`
	Revision       int64                       `json:"revision,omitempty"`
	Conversation   *ConversationDTO            `json:"conversation,omitempty"`
	Run            *RunDTO                     `json:"run,omitempty"`
	Message        *MessageDTO                 `json:"message,omitempty"`
	ToolCall       *ToolCallDTO                `json:"tool_call,omitempty"`
	Approval       *EventDTO                   `json:"approval,omitempty"`
	Plan           *agentcore.RuntimePlanState `json:"plan,omitempty"`
	Delta          string                      `json:"delta,omitempty"`
	RunID          string                      `json:"run_id,omitempty"`
	Sequence       int                         `json:"sequence,omitempty"`
	Error          string                      `json:"error,omitempty"`
	ErrorCode      string                      `json:"error_code,omitempty"`
	Recoverable    bool                        `json:"recoverable,omitempty"`
}

type CreateConversationCommand struct {
	Title          string
	IdempotencyKey string
}

type PrewarmConversationCommand struct {
	ConversationID   string
	ConversationKind string
	Title            string
	IdempotencyKey   string
}

type SendMessageCommand struct {
	ConversationID       string
	ConversationKind     string
	ForceNewConversation bool
	// WorkspaceRootDir is an optional server-controlled workspace for system
	// automation. It is excluded from command fingerprints so retries keep the
	// same execution identity.
	WorkspaceRootDir     string `json:"-"`
	MissionID            string
	MissionTaskID        string
	Content              string
	DirectAgentID        string
	DirectTargetKind     string
	DirectTargetID       string
	InitialMemberIDs     []string
	AllowMemberAdditions *bool
	AttachmentIDs        []string
	Mentions             []MessageMentionDTO
	IdempotencyKey       string
}

type SendTodoAutomationMessageCommand struct {
	ConversationID string
	TodoID         string
	ManagedSource  string
	Content        string
	Mentions       []MessageMentionDTO
	IdempotencyKey string
}

type AppendNoticeCommand struct {
	AttachmentIDs  []string `json:",omitempty"`
	ConversationID string
	MissionID      string
	MissionTaskID  string
	Content        string
	Metadata       map[string]string
	IdempotencyKey string
}

type InterjectMessageCommand struct {
	ConversationID string
	Content        string
	Mentions       []MessageMentionDTO
	ExpectedTurnID string
	ExpectedRunID  string
	IdempotencyKey string
}

type AddConversationMembersCommand struct {
	ConversationID string
	TargetIDs      []string
	IdempotencyKey string
}

type EnsureTodoConversationCommand struct {
	TenantID               string
	TodoID                 string
	Title                  string
	WorkspaceID            string
	ExistingConversationID string
	InitialMemberIDs       []string
	ManagedSource          string
}

type ReconcileConversationMembersCommand struct {
	ConversationID string
	TargetIDs      []string
	ManagedSource  string
}

type RetryMessageCommand struct {
	ConversationID string
	MessageID      string
	IdempotencyKey string
	Approve        bool
}

type ListConversationsQuery struct {
	Page     int
	PageSize int
}

type ListMessagesQuery struct {
	Limit  int
	Offset int
}
