// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package core

import "encoding/json"

// Reference: aionui-session/event.rs@v0.1.52.

type TurnStartedEvent struct {
	Epoch uint64 `json:"epoch"`
}
type CancelEvent struct{}
type MessageDeltaEvent struct {
	ItemID string `json:"item_id"`
	Text   string `json:"text"`
}

type ReasoningContentKind string

const (
	ReasoningContentRaw     ReasoningContentKind = "raw"
	ReasoningContentSummary ReasoningContentKind = "summary"
)

type ThoughtDeltaEvent struct {
	ItemID        string               `json:"item_id"`
	Text          string               `json:"text"`
	ReasoningKind ReasoningContentKind `json:"reasoning_kind,omitempty"`
}

type SubagentKind string

const (
	SubagentInline   SubagentKind = "inline"
	SubagentSpawned  SubagentKind = "spawned"
	SubagentWorkflow SubagentKind = "workflow"
)

type ToolCallEvent struct {
	ToolUseID       string          `json:"tool_use_id"`
	Name            string          `json:"name"`
	Subagent        SubagentKind    `json:"subagent"`
	SpawnedSession  SessionID       `json:"spawned_session_id,omitempty"`
	Input           json.RawMessage `json:"input,omitempty"`
	ParentToolUseID string          `json:"parent_tool_use_id,omitempty"`
}

type ToolResultContentKind string

const (
	ToolResultText     ToolResultContentKind = "text"
	ToolResultFilePath ToolResultContentKind = "file_path"
	ToolResultImage    ToolResultContentKind = "image"
)

type ToolResultContent struct {
	Kind      ToolResultContentKind `json:"kind"`
	Text      string                `json:"text,omitempty"`
	Path      string                `json:"path,omitempty"`
	MIME      string                `json:"mime,omitempty"`
	OldText   string                `json:"old_text,omitempty"`
	NewText   string                `json:"new_text,omitempty"`
	MediaType string                `json:"media_type,omitempty"`
	Data      []byte                `json:"data,omitempty"`
}

type ToolResultEvent struct {
	ToolUseID       string              `json:"tool_use_id"`
	IsError         bool                `json:"is_error"`
	Content         []ToolResultContent `json:"content,omitempty"`
	ParentToolUseID string              `json:"parent_tool_use_id,omitempty"`
}

type HeartbeatEvent struct{}

type TurnOutcomeKind string

const (
	TurnOutcomeCompleted TurnOutcomeKind = "completed"
	TurnOutcomeCancelled TurnOutcomeKind = "cancelled"
	TurnOutcomeFailed    TurnOutcomeKind = "failed"
	TurnOutcomeEndTurn   TurnOutcomeKind = "end_turn"
)

type StopReasonKind string

const (
	StopReasonEndTurn   StopReasonKind = "end_turn"
	StopReasonTruncated StopReasonKind = "truncated"
	StopReasonRefused   StopReasonKind = "refused"
)

type TruncationKind string

const (
	TruncationMaxTokens     TruncationKind = "max_tokens"
	TruncationContextWindow TruncationKind = "context_window"
	TruncationMaxTurns      TruncationKind = "max_turns"
	TruncationBudget        TruncationKind = "budget"
	TruncationWire          TruncationKind = "truncated"
)

type CancelReason string

const (
	CancelReasonUserCancel    CancelReason = "user_cancel"
	CancelReasonBargeIn       CancelReason = "barge_in"
	CancelReasonSocketAbort   CancelReason = "socket_abort"
	CancelReasonToolDenyChain CancelReason = "tool_deny_chain"
)

type StopReason struct {
	Kind       StopReasonKind `json:"kind"`
	Truncation TruncationKind `json:"truncation,omitempty"`
	Category   string         `json:"category,omitempty"`
}

type TurnOutcome struct {
	Kind         TurnOutcomeKind `json:"kind"`
	StopReason   *StopReason     `json:"stop_reason,omitempty"`
	CancelReason CancelReason    `json:"cancel_reason,omitempty"`
}

type TurnResultEvent struct {
	IsError        bool        `json:"is_error"`
	APIErrorStatus *uint16     `json:"api_error_status,omitempty"`
	ResultText     string      `json:"result_text"`
	Epoch          uint64      `json:"epoch"`
	Outcome        TurnOutcome `json:"outcome"`
}

type ExitStatus struct {
	Code   *int `json:"code,omitempty"`
	Signal *int `json:"signal,omitempty"`
}

type DetachedEvent struct {
	Exit            *ExitStatus `json:"exit,omitempty"`
	RedactedSummary string      `json:"redacted_summary,omitempty"`
}

type AdapterSpecificEvent struct {
	Tag     string `json:"tag"`
	Summary string `json:"summary,omitempty"`
}

type PlanStatus string

const (
	PlanPending    PlanStatus = "pending"
	PlanInProgress PlanStatus = "in_progress"
	PlanCompleted  PlanStatus = "completed"
)

type PlanPriority string

const (
	PlanPriorityHigh   PlanPriority = "high"
	PlanPriorityMedium PlanPriority = "medium"
	PlanPriorityLow    PlanPriority = "low"
)

type PlanEntry struct {
	Content  string       `json:"content"`
	Status   PlanStatus   `json:"status"`
	Priority PlanPriority `json:"priority,omitempty"`
}

type PlanEvent struct {
	Entries     []PlanEntry `json:"entries"`
	Explanation string      `json:"explanation,omitempty"`
}

type PermissionKind string

const (
	PermissionKindTool PermissionKind = "tool"
	PermissionKindAuth PermissionKind = "auth"
)

type PermissionEvent struct {
	RequestID string               `json:"request_id"`
	Kind      PermissionKind       `json:"kind"`
	Context   *PermissionContext   `json:"context,omitempty"`
	ToolName  string               `json:"tool_name,omitempty"`
	Questions []PermissionQuestion `json:"questions,omitempty"`
}

type PermissionContext struct {
	ServerName string             `json:"server_name,omitempty"`
	Options    []PermissionOption `json:"options,omitempty"`
}

type PermissionOption struct {
	OptionID string `json:"option_id"`
	Kind     string `json:"kind"`
}

type PermissionQuestion struct {
	Question    string                     `json:"question"`
	Header      string                     `json:"header,omitempty"`
	Options     []PermissionQuestionOption `json:"options"`
	MultiSelect bool                       `json:"multi_select"`
}

type PermissionQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type PermissionResolvedEvent struct {
	RequestID string         `json:"request_id"`
	Kind      PermissionKind `json:"kind"`
}

type PromptAcceptedEvent struct {
	ClientMsgID string `json:"client_msg_id"`
}
type UsageDeltaEvent struct {
	// Optional subset counters: nil means the provider did not report them.
	CachedInputTokens *uint64  `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   *uint64  `json:"reasoning_tokens,omitempty"`
	InputTokens       uint64   `json:"input_tokens"`
	OutputTokens      uint64   `json:"output_tokens"`
	TotalTokens       uint64   `json:"total_tokens"`
	CostUSD           *float64 `json:"cost_usd,omitempty"`
}

type ProvisioningPhase string

const (
	ProvisioningInjecting    ProvisioningPhase = "injecting"
	ProvisioningToolsWaiting ProvisioningPhase = "tools_waiting"
	ProvisioningToolsReady   ProvisioningPhase = "tools_ready"
	ProvisioningDegraded     ProvisioningPhase = "degraded"
	ProvisioningLoadFailed   ProvisioningPhase = "load_failed"
)

type ProvisioningEvent struct {
	Phase  ProvisioningPhase `json:"phase"`
	Reason string            `json:"reason,omitempty"`
}

type RewoundEvent struct {
	ToTurn uint64 `json:"to_turn"`
}
type ConfigChangedEvent struct {
	Mode    string            `json:"mode,omitempty"`
	Model   string            `json:"model,omitempty"`
	Options map[string]string `json:"options,omitempty"`
}
type CatalogUpdatedEvent struct {
	Models        []ModelInfo        `json:"models,omitempty"`
	Modes         []ModeInfo         `json:"modes,omitempty"`
	ConfigOptions []ConfigOptionInfo `json:"config_options,omitempty"`
	SlashCommands []SlashCommandInfo `json:"slash_commands,omitempty"`
}

type SubagentStatus string

const (
	SubagentPendingInit SubagentStatus = "pending_init"
	SubagentRunning     SubagentStatus = "running"
	SubagentInterrupted SubagentStatus = "interrupted"
	SubagentCompleted   SubagentStatus = "completed"
	SubagentErrored     SubagentStatus = "errored"
	SubagentShutdown    SubagentStatus = "shutdown"
)

func (s SubagentStatus) IsTerminal() bool {
	switch s {
	case SubagentInterrupted, SubagentCompleted, SubagentErrored, SubagentShutdown:
		return true
	default:
		return false
	}
}

type SubagentUpdateEvent struct {
	Ref       string         `json:"ref"`
	Label     string         `json:"label,omitempty"`
	Status    SubagentStatus `json:"status"`
	ParentRef string         `json:"parent_ref,omitempty"`
}

type WorkflowLoopState string

const (
	WorkflowLoopStart    WorkflowLoopState = "start"
	WorkflowLoopProgress WorkflowLoopState = "progress"
	WorkflowLoopDone     WorkflowLoopState = "done"
)

type SubagentDetailEvent struct {
	Ref          string            `json:"ref"`
	ParentRef    string            `json:"parent_ref,omitempty"`
	Label        string            `json:"label,omitempty"`
	LoopState    WorkflowLoopState `json:"loop_state,omitempty"`
	Model        string            `json:"model,omitempty"`
	Tokens       *uint64           `json:"tokens,omitempty"`
	ToolCalls    *uint64           `json:"tool_calls,omitempty"`
	LastToolName string            `json:"last_tool_name,omitempty"`
}

type NoticeLevel string

const (
	NoticeInfo    NoticeLevel = "info"
	NoticeWarning NoticeLevel = "warning"
)

type NoticeEvent struct {
	Level   NoticeLevel `json:"level"`
	Message string      `json:"message"`
}
type ToolOutputDeltaEvent struct {
	ItemID string `json:"item_id"`
	Text   string `json:"text"`
}
type TurnDiffUpdatedEvent struct {
	Diff string `json:"diff"`
}

type ItemKind string

const (
	ItemText     ItemKind = "text"
	ItemThinking ItemKind = "thinking"
	ItemTool     ItemKind = "tool"
	ItemImage    ItemKind = "image"
)

type ItemStartedEvent struct {
	ItemID string   `json:"item_id"`
	Kind   ItemKind `json:"kind"`
}
type TruncationInfo struct {
	Kind        TruncationKind `json:"kind"`
	PartialText string         `json:"partial_text,omitempty"`
}
type ItemCompletedEvent struct {
	ItemID     string          `json:"item_id"`
	Truncation *TruncationInfo `json:"truncation,omitempty"`
}
type FinalizedMessage struct {
	ItemID        string               `json:"item_id"`
	Kind          ItemKind             `json:"kind"`
	Content       string               `json:"content"`
	ReasoningKind ReasoningContentKind `json:"reasoning_kind,omitempty"`
	Truncation    *TruncationInfo      `json:"truncation,omitempty"`
	Sequence      uint64               `json:"sequence"`
}
type MessageFinalizedEvent struct {
	Message FinalizedMessage `json:"message"`
}
type SnapshotEvent struct {
	StateRepresentation string `json:"state_representation"`
	TurnGeneration      uint64 `json:"turn_generation"`
}
type LaggedEvent struct {
	Skipped uint64 `json:"skipped"`
}
type BackendSuspendedEvent struct{}
type CheckpointEntry struct {
	ID             string  `json:"id"`
	Label          string  `json:"label,omitempty"`
	TurnGeneration *uint64 `json:"turn_generation,omitempty"`
}
type CheckpointListEvent struct {
	Entries []CheckpointEntry `json:"entries"`
}
type ContextUsageCategory struct {
	Name   string `json:"name"`
	Tokens uint64 `json:"tokens"`
}
type ContextUsage struct {
	Used       uint64                 `json:"used"`
	Max        uint64                 `json:"max"`
	Categories []ContextUsageCategory `json:"categories,omitempty"`
}
type SessionInfoEvent struct {
	ContextUsage *ContextUsage `json:"context_usage,omitempty"`
	CostText     string        `json:"cost_text,omitempty"`
}
type BackendBoundEvent struct {
	BackendSessionID string `json:"backend_session_id,omitempty"`
}
