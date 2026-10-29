package protocol

import "context"

type ChannelKey string

const (
	ChannelFeishu   ChannelKey = "feishu"
	ChannelWeixin   ChannelKey = "weixin"
	ChannelDingTalk ChannelKey = "dingtalk"
	ChannelEmail    ChannelKey = "email"
	ChannelWebsite  ChannelKey = "website"
)

type Mode string

const (
	ModeDefault           Mode = "default"
	ModePlan              Mode = "plan"
	ModeAcceptEdits       Mode = "acceptEdits"
	ModeBypassPermissions Mode = "bypassPermissions"
)

type AdvisorTier string

const (
	AdvisorTierPublic   AdvisorTier = "public"
	AdvisorTierCustomer AdvisorTier = "customer"
)

type Attachment struct {
	Type     string         `json:"type"`
	Name     string         `json:"name,omitempty"`
	Path     string         `json:"path,omitempty"`
	MIMEType string         `json:"mime_type,omitempty"`
	Content  string         `json:"content,omitempty"`
	Bytes    int64          `json:"bytes,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// InboundIdentity is set by a channel transport after upstream authentication.
type InboundIdentity struct {
	AccountID        string `json:"account_id,omitempty"`
	ExternalTenantID string `json:"external_tenant_id,omitempty"`
	ExternalUserID   string `json:"external_user_id,omitempty"`
	ExternalChatID   string `json:"external_chat_id,omitempty"`
	ChatKind         string `json:"chat_kind,omitempty"`
	DisplayName      string `json:"display_name,omitempty"`
	Verified         bool   `json:"verified"`
}

type SubmitTurnInput struct {
	SessionKey       string            `json:"session_key"`
	ChannelKey       ChannelKey        `json:"channel_key"`
	Message          string            `json:"message"`
	InputModality    string            `json:"input_modality,omitempty"`
	TranscriptSource string            `json:"transcript_source,omitempty"`
	ProjectKey       string            `json:"project_key,omitempty"`
	WorkspaceCWD     string            `json:"workspace_cwd,omitempty"`
	Attachments      []Attachment      `json:"attachments,omitempty"`
	Mode             Mode              `json:"mode,omitempty"`
	RunID            string            `json:"run_id,omitempty"`
	MaxTurns         int               `json:"max_turns,omitempty"`
	Telemetry        map[string]string `json:"telemetry,omitempty"`
	Identity         *InboundIdentity  `json:"identity,omitempty"`
	AdvisorTier      AdvisorTier       `json:"advisor_tier,omitempty"`
}

type EventType string

const (
	EventTurnStarted               EventType = "turn_started"
	EventAssistantTextDelta        EventType = "assistant_text_delta"
	EventAssistantImage            EventType = "assistant_image"
	EventAssistantThinkingDelta    EventType = "assistant_thinking_delta"
	EventToolCallStarted           EventType = "tool_call_started"
	EventToolCallFinished          EventType = "tool_call_finished"
	EventToolResultDetailAvailable EventType = "tool_result_detail_available"
	EventPermissionRequest         EventType = "permission_request"
	EventElicitationRequest        EventType = "elicitation_request"
	EventElicitationCancelled      EventType = "elicitation_cancelled"
	EventStructuredOutput          EventType = "structured_output"
	EventPlanModeChanged           EventType = "plan_mode_changed"
	EventConfigChanged             EventType = "config_changed"
	EventWorktreeCreated           EventType = "worktree_created"
	EventWorktreeRemoved           EventType = "worktree_removed"
	EventContextBudget             EventType = "context_budget"
	EventTurnCompleted             EventType = "turn_completed"
	EventAgentStatus               EventType = "agent_status"
	EventError                     EventType = "error"
)

const (
	ErrorCodeGatewayRouteUnavailable   = "gateway_route_unavailable"
	ErrorCodeGatewayRuntimeUnavailable = "gateway_runtime_unavailable"
	ErrorCodeGatewayRuntimeInvalid     = "gateway_runtime_invalid"
	ErrorCodeGatewayPermissionDenied   = "gateway_permission_denied"
	ErrorCodeGatewayRequestInvalid     = "gateway_request_invalid"
)

type Event struct {
	Attachment    *Attachment    `json:"attachment,omitempty"`
	Type          EventType      `json:"type"`
	RunID         string         `json:"run_id,omitempty"`
	Text          string         `json:"text,omitempty"`
	Sources       []string       `json:"sources,omitempty"`
	ToolCallID    string         `json:"tool_call_id,omitempty"`
	Name          string         `json:"name,omitempty"`
	ArgsPreview   string         `json:"args_preview,omitempty"`
	OK            bool           `json:"ok,omitempty"`
	ResultPreview string         `json:"result_preview,omitempty"`
	ErrorCode     string         `json:"error_code,omitempty"`
	RequestID     string         `json:"request_id,omitempty"`
	ToolName      string         `json:"tool_name,omitempty"`
	Payload       any            `json:"payload,omitempty"`
	Mode          Mode           `json:"mode,omitempty"`
	ChangedPaths  []string       `json:"changed_paths,omitempty"`
	ChangeClasses []string       `json:"change_classes,omitempty"`
	CWD           string         `json:"cwd,omitempty"`
	Used          int            `json:"used,omitempty"`
	Total         int            `json:"total,omitempty"`
	Ratio         float64        `json:"ratio,omitempty"`
	State         string         `json:"state,omitempty"`
	Usage         map[string]int `json:"usage,omitempty"`
	FinishReason  string         `json:"finish_reason,omitempty"`
	Message       string         `json:"message,omitempty"`
	Code          string         `json:"code,omitempty"`
	Recoverable   bool           `json:"recoverable,omitempty"`
	Detail        map[string]any `json:"detail,omitempty"`
}

type ActiveTurnSnapshot struct {
	Active     bool    `json:"active"`
	SessionKey string  `json:"session_key"`
	RunID      string  `json:"run_id,omitempty"`
	Events     []Event `json:"events"`
	Truncated  bool    `json:"truncated,omitempty"`
}

type Runner interface {
	RunTurn(ctx context.Context, input SubmitTurnInput) (<-chan Event, error)
}
