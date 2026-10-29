// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package core

import (
	"encoding/json"
	"strings"
)

// Reference: aionui-session/event.rs@v0.1.52.

type SessionEventKind string

const (
	EventTurnStarted        SessionEventKind = "turn_started"
	EventCancel             SessionEventKind = "cancel"
	EventMessageDelta       SessionEventKind = "message_delta"
	EventThoughtDelta       SessionEventKind = "thought_delta"
	EventToolCall           SessionEventKind = "tool_call"
	EventToolResult         SessionEventKind = "tool_result"
	EventHeartbeat          SessionEventKind = "heartbeat"
	EventTurnResult         SessionEventKind = "turn_result"
	EventDetached           SessionEventKind = "detached"
	EventAdapterSpecific    SessionEventKind = "adapter_specific"
	EventPlan               SessionEventKind = "plan"
	EventPermission         SessionEventKind = "permission"
	EventPermissionResolved SessionEventKind = "permission_resolved"
	EventPromptAccepted     SessionEventKind = "prompt_accepted"
	EventUsageDelta         SessionEventKind = "usage_delta"
	EventProvisioning       SessionEventKind = "provisioning"
	EventRewound            SessionEventKind = "rewound"
	EventConfigChanged      SessionEventKind = "config_changed"
	EventCatalogUpdated     SessionEventKind = "catalog_updated"
	EventSubagentUpdate     SessionEventKind = "subagent_update"
	EventSubagentDetail     SessionEventKind = "subagent_detail"
	EventNotice             SessionEventKind = "notice"
	EventToolOutputDelta    SessionEventKind = "tool_output_delta"
	EventTurnDiffUpdated    SessionEventKind = "turn_diff_updated"
	EventItemStarted        SessionEventKind = "item_started"
	EventItemCompleted      SessionEventKind = "item_completed"
	EventMessageFinalized   SessionEventKind = "message_finalized"
	EventSnapshot           SessionEventKind = "snapshot"
	EventLagged             SessionEventKind = "lagged"
	EventBackendSuspended   SessionEventKind = "backend_suspended"
	EventCheckpointList     SessionEventKind = "checkpoint_list"
	EventSessionInfo        SessionEventKind = "session_info"
	EventBackendBound       SessionEventKind = "backend_bound"
)

type SessionEvent struct {
	Kind               SessionEventKind         `json:"kind"`
	TurnStarted        *TurnStartedEvent        `json:"turn_started,omitempty"`
	Cancel             *CancelEvent             `json:"cancel,omitempty"`
	MessageDelta       *MessageDeltaEvent       `json:"message_delta,omitempty"`
	ThoughtDelta       *ThoughtDeltaEvent       `json:"thought_delta,omitempty"`
	ToolCall           *ToolCallEvent           `json:"tool_call,omitempty"`
	ToolResult         *ToolResultEvent         `json:"tool_result,omitempty"`
	Heartbeat          *HeartbeatEvent          `json:"heartbeat,omitempty"`
	TurnResult         *TurnResultEvent         `json:"turn_result,omitempty"`
	Detached           *DetachedEvent           `json:"detached,omitempty"`
	AdapterSpecific    *AdapterSpecificEvent    `json:"adapter_specific,omitempty"`
	Plan               *PlanEvent               `json:"plan,omitempty"`
	Permission         *PermissionEvent         `json:"permission,omitempty"`
	PermissionResolved *PermissionResolvedEvent `json:"permission_resolved,omitempty"`
	PromptAccepted     *PromptAcceptedEvent     `json:"prompt_accepted,omitempty"`
	UsageDelta         *UsageDeltaEvent         `json:"usage_delta,omitempty"`
	Provisioning       *ProvisioningEvent       `json:"provisioning,omitempty"`
	Rewound            *RewoundEvent            `json:"rewound,omitempty"`
	ConfigChanged      *ConfigChangedEvent      `json:"config_changed,omitempty"`
	CatalogUpdated     *CatalogUpdatedEvent     `json:"catalog_updated,omitempty"`
	SubagentUpdate     *SubagentUpdateEvent     `json:"subagent_update,omitempty"`
	SubagentDetail     *SubagentDetailEvent     `json:"subagent_detail,omitempty"`
	Notice             *NoticeEvent             `json:"notice,omitempty"`
	ToolOutputDelta    *ToolOutputDeltaEvent    `json:"tool_output_delta,omitempty"`
	TurnDiffUpdated    *TurnDiffUpdatedEvent    `json:"turn_diff_updated,omitempty"`
	ItemStarted        *ItemStartedEvent        `json:"item_started,omitempty"`
	ItemCompleted      *ItemCompletedEvent      `json:"item_completed,omitempty"`
	MessageFinalized   *MessageFinalizedEvent   `json:"message_finalized,omitempty"`
	Snapshot           *SnapshotEvent           `json:"snapshot,omitempty"`
	Lagged             *LaggedEvent             `json:"lagged,omitempty"`
	BackendSuspended   *BackendSuspendedEvent   `json:"backend_suspended,omitempty"`
	CheckpointList     *CheckpointListEvent     `json:"checkpoint_list,omitempty"`
	SessionInfo        *SessionInfoEvent        `json:"session_info,omitempty"`
	BackendBound       *BackendBoundEvent       `json:"backend_bound,omitempty"`
}

type EventClass string

const (
	EventClassProvider     EventClass = "provider"
	EventClassOrchestrator EventClass = "orchestrator"
)

type PersistTier string

const (
	PersistEphemeral       PersistTier = "ephemeral"
	PersistDisplay         PersistTier = "display"
	PersistState           PersistTier = "state"
	PersistDisplayAndState PersistTier = "display_and_state"
)

func (e SessionEvent) Validate() error {
	if !knownSessionEventKind(e.Kind) {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "unknown event kind", false)
	}
	if e.payloadCount() != 1 || !e.payloadMatchesKind() {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "event must contain exactly its matching payload", false)
	}
	switch e.Kind {
	case EventTurnStarted:
		if e.TurnStarted.Epoch == 0 {
			return invalidEvent("turn epoch must be positive")
		}
	case EventMessageDelta:
		return requireID(e.MessageDelta.ItemID, "message item_id")
	case EventThoughtDelta:
		if err := requireID(e.ThoughtDelta.ItemID, "thought item_id"); err != nil {
			return err
		}
		if !validReasoningContentKind(e.ThoughtDelta.ReasoningKind) {
			return invalidEvent("invalid thought reasoning kind")
		}
	case EventToolCall:
		if err := requireID(e.ToolCall.ToolUseID, "tool_use_id"); err != nil {
			return err
		}
		if err := requireID(e.ToolCall.Name, "tool name"); err != nil {
			return err
		}
		switch e.ToolCall.Subagent {
		case "", SubagentInline, SubagentWorkflow:
			if e.ToolCall.SpawnedSession != "" {
				return invalidEvent("spawned_session_id only belongs to spawned subagent")
			}
		case SubagentSpawned:
			if e.ToolCall.SpawnedSession == "" {
				return invalidEvent("spawned subagent requires session id")
			}
		default:
			return invalidEvent("invalid subagent kind")
		}
		return validateSafeJSON(e.ToolCall.Input)
	case EventToolResult:
		if err := requireID(e.ToolResult.ToolUseID, "tool_use_id"); err != nil {
			return err
		}
		for _, content := range e.ToolResult.Content {
			if err := validateToolResultContent(content); err != nil {
				return err
			}
		}
	case EventTurnResult:
		return validateTurnOutcome(e.TurnResult.Outcome)
	case EventDetached:
		if e.Detached.Exit != nil && e.Detached.Exit.Code != nil && e.Detached.Exit.Signal != nil {
			return invalidEvent("detached exit cannot carry both code and signal")
		}
		if len(e.Detached.RedactedSummary) > 240 || strings.ContainsAny(e.Detached.RedactedSummary, "\r\n") {
			return invalidEvent("detached summary must be a single redacted line of at most 240 characters")
		}
	case EventAdapterSpecific:
		if strings.TrimSpace(e.AdapterSpecific.Tag) == "" || len(e.AdapterSpecific.Summary) > 240 || strings.ContainsAny(e.AdapterSpecific.Summary, "\r\n") {
			return invalidEvent("adapter diagnostic requires a tag and bounded summary")
		}
	case EventPlan:
		for _, entry := range e.Plan.Entries {
			if strings.TrimSpace(entry.Content) == "" || !validPlanStatus(entry.Status) || (entry.Priority != "" && entry.Priority != PlanPriorityHigh && entry.Priority != PlanPriorityMedium && entry.Priority != PlanPriorityLow) {
				return invalidEvent("invalid plan entry")
			}
		}
	case EventPermission:
		if err := requireID(e.Permission.RequestID, "permission request_id"); err != nil {
			return err
		}
		if !validPermissionKind(e.Permission.Kind) {
			return invalidEvent("invalid permission kind")
		}
		if e.Permission.Context != nil {
			for _, option := range e.Permission.Context.Options {
				if strings.TrimSpace(option.OptionID) == "" || strings.TrimSpace(option.Kind) == "" {
					return invalidEvent("permission context options require id and kind")
				}
			}
		}
		seenQuestions := make(map[string]struct{}, len(e.Permission.Questions))
		for _, question := range e.Permission.Questions {
			if strings.TrimSpace(question.Question) == "" || len(question.Options) == 0 {
				return invalidEvent("permission questions require text and options")
			}
			if _, exists := seenQuestions[question.Question]; exists {
				return invalidEvent("permission question text must be unique")
			}
			seenQuestions[question.Question] = struct{}{}
			seenLabels := make(map[string]struct{}, len(question.Options))
			for _, option := range question.Options {
				if strings.TrimSpace(option.Label) == "" {
					return invalidEvent("permission question option label is required")
				}
				if _, exists := seenLabels[option.Label]; exists {
					return invalidEvent("permission question option labels must be unique")
				}
				seenLabels[option.Label] = struct{}{}
			}
		}
		if len(e.Permission.Questions) != 0 && e.Permission.ToolName != "AskUserQuestion" {
			return invalidEvent("questions are only allowed for AskUserQuestion")
		}
	case EventPermissionResolved:
		if err := requireID(e.PermissionResolved.RequestID, "permission request_id"); err != nil {
			return err
		}
		if !validPermissionKind(e.PermissionResolved.Kind) {
			return invalidEvent("invalid permission kind")
		}
	case EventPromptAccepted:
		return requireID(e.PromptAccepted.ClientMsgID, "client_msg_id")
	case EventUsageDelta:
		if (e.UsageDelta.CachedInputTokens != nil && *e.UsageDelta.CachedInputTokens > e.UsageDelta.InputTokens) ||
			(e.UsageDelta.ReasoningTokens != nil && *e.UsageDelta.ReasoningTokens > e.UsageDelta.OutputTokens) {
			return invalidEvent("usage detail exceeds its inclusive token count")
		}
		if e.UsageDelta.TotalTokens < e.UsageDelta.InputTokens+e.UsageDelta.OutputTokens {
			return invalidEvent("total tokens cannot be less than input plus output")
		}
		if e.UsageDelta.CostUSD != nil && *e.UsageDelta.CostUSD < 0 {
			return invalidEvent("usage cost cannot be negative")
		}
	case EventProvisioning:
		if !validProvisioning(e.Provisioning.Phase) {
			return invalidEvent("invalid provisioning phase")
		}
		needsReason := e.Provisioning.Phase == ProvisioningDegraded || e.Provisioning.Phase == ProvisioningLoadFailed
		if needsReason != (strings.TrimSpace(e.Provisioning.Reason) != "") {
			return invalidEvent("only failed provisioning phases require a reason")
		}
	case EventConfigChanged:
		for key, value := range e.ConfigChanged.Options {
			if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				return invalidEvent("config changes cannot contain empty option names or values")
			}
		}
	case EventCatalogUpdated:
		catalog := Capabilities{AvailableModels: e.CatalogUpdated.Models, AvailableModes: e.CatalogUpdated.Modes, ConfigOptions: e.CatalogUpdated.ConfigOptions, SlashCommands: e.CatalogUpdated.SlashCommands}
		if err := catalog.Validate(); err != nil {
			return err
		}
	case EventSubagentUpdate:
		if err := requireID(e.SubagentUpdate.Ref, "subagent ref"); err != nil {
			return err
		}
		if !validSubagentStatus(e.SubagentUpdate.Status) {
			return invalidEvent("invalid subagent status")
		}
	case EventSubagentDetail:
		if err := requireID(e.SubagentDetail.Ref, "subagent ref"); err != nil {
			return err
		}
		if e.SubagentDetail.LoopState != "" && e.SubagentDetail.LoopState != WorkflowLoopStart && e.SubagentDetail.LoopState != WorkflowLoopProgress && e.SubagentDetail.LoopState != WorkflowLoopDone {
			return invalidEvent("invalid workflow loop state")
		}
	case EventNotice:
		if (e.Notice.Level != NoticeInfo && e.Notice.Level != NoticeWarning) || strings.TrimSpace(e.Notice.Message) == "" {
			return invalidEvent("invalid notice")
		}
	case EventToolOutputDelta:
		return requireID(e.ToolOutputDelta.ItemID, "tool output item_id")
	case EventItemStarted:
		if err := requireID(e.ItemStarted.ItemID, "item_id"); err != nil {
			return err
		}
		if !validItemKind(e.ItemStarted.Kind) {
			return invalidEvent("invalid item kind")
		}
	case EventItemCompleted:
		if err := requireID(e.ItemCompleted.ItemID, "item_id"); err != nil {
			return err
		}
		if e.ItemCompleted.Truncation != nil && !validTruncation(e.ItemCompleted.Truncation.Kind) {
			return invalidEvent("invalid item truncation kind")
		}
	case EventMessageFinalized:
		if err := requireID(e.MessageFinalized.Message.ItemID, "item_id"); err != nil {
			return err
		}
		if !validItemKind(e.MessageFinalized.Message.Kind) {
			return invalidEvent("invalid finalized item kind")
		}
		if !validReasoningContentKind(e.MessageFinalized.Message.ReasoningKind) {
			return invalidEvent("invalid finalized reasoning kind")
		}
		if e.MessageFinalized.Message.ReasoningKind != "" && e.MessageFinalized.Message.Kind != ItemThinking {
			return invalidEvent("reasoning kind only belongs to thinking items")
		}
	case EventLagged:
		if e.Lagged.Skipped == 0 {
			return invalidEvent("lagged skipped count must be positive")
		}
	case EventCheckpointList:
		for _, entry := range e.CheckpointList.Entries {
			if strings.TrimSpace(entry.ID) == "" {
				return invalidEvent("checkpoint id is required")
			}
		}
	case EventSessionInfo:
		if (e.SessionInfo.ContextUsage == nil) == (e.SessionInfo.CostText == "") {
			return invalidEvent("session info must contain exactly one result")
		}
		if e.SessionInfo.ContextUsage != nil && e.SessionInfo.ContextUsage.Max < e.SessionInfo.ContextUsage.Used {
			return invalidEvent("context usage max cannot be less than used")
		}
		if e.SessionInfo.ContextUsage != nil {
			for _, category := range e.SessionInfo.ContextUsage.Categories {
				if strings.TrimSpace(category.Name) == "" {
					return invalidEvent("context usage category name is required")
				}
			}
		}
	}
	return nil
}

func (e SessionEvent) Class() EventClass {
	switch e.Kind {
	case EventTurnStarted, EventCancel, EventHeartbeat, EventSnapshot, EventLagged, EventBackendBound, EventBackendSuspended:
		return EventClassOrchestrator
	default:
		return EventClassProvider
	}
}

func (e SessionEvent) PersistTier() PersistTier {
	if e.Class() == EventClassOrchestrator {
		return PersistEphemeral
	}
	switch e.Kind {
	case EventMessageDelta, EventThoughtDelta, EventToolOutputDelta, EventTurnDiffUpdated, EventItemStarted, EventItemCompleted, EventCheckpointList, EventCatalogUpdated, EventSessionInfo, EventSubagentDetail, EventPlan:
		return PersistEphemeral
	case EventToolCall, EventToolResult, EventUsageDelta, EventProvisioning, EventConfigChanged, EventNotice, EventMessageFinalized:
		return PersistDisplay
	case EventRewound:
		return PersistState
	case EventAdapterSpecific:
		if e.AdapterSpecific != nil && (e.AdapterSpecific.Tag == "ttft_ms" || e.AdapterSpecific.Tag == "elapsed" || e.AdapterSpecific.Tag == "latency") {
			return PersistEphemeral
		}
		return PersistDisplay
	default:
		return PersistDisplayAndState
	}
}

func (e SessionEvent) Clone() SessionEvent {
	data, err := json.Marshal(e)
	if err != nil {
		return SessionEvent{}
	}
	var out SessionEvent
	if err := json.Unmarshal(data, &out); err != nil {
		return SessionEvent{}
	}
	return out
}

func (e SessionEvent) payloadCount() int {
	count := 0
	for _, present := range []bool{e.TurnStarted != nil, e.Cancel != nil, e.MessageDelta != nil, e.ThoughtDelta != nil, e.ToolCall != nil, e.ToolResult != nil, e.Heartbeat != nil, e.TurnResult != nil, e.Detached != nil, e.AdapterSpecific != nil, e.Plan != nil, e.Permission != nil, e.PermissionResolved != nil, e.PromptAccepted != nil, e.UsageDelta != nil, e.Provisioning != nil, e.Rewound != nil, e.ConfigChanged != nil, e.CatalogUpdated != nil, e.SubagentUpdate != nil, e.SubagentDetail != nil, e.Notice != nil, e.ToolOutputDelta != nil, e.TurnDiffUpdated != nil, e.ItemStarted != nil, e.ItemCompleted != nil, e.MessageFinalized != nil, e.Snapshot != nil, e.Lagged != nil, e.BackendSuspended != nil, e.CheckpointList != nil, e.SessionInfo != nil, e.BackendBound != nil} {
		if present {
			count++
		}
	}
	return count
}

func (e SessionEvent) payloadMatchesKind() bool {
	switch e.Kind {
	case EventTurnStarted:
		return e.TurnStarted != nil
	case EventCancel:
		return e.Cancel != nil
	case EventMessageDelta:
		return e.MessageDelta != nil
	case EventThoughtDelta:
		return e.ThoughtDelta != nil
	case EventToolCall:
		return e.ToolCall != nil
	case EventToolResult:
		return e.ToolResult != nil
	case EventHeartbeat:
		return e.Heartbeat != nil
	case EventTurnResult:
		return e.TurnResult != nil
	case EventDetached:
		return e.Detached != nil
	case EventAdapterSpecific:
		return e.AdapterSpecific != nil
	case EventPlan:
		return e.Plan != nil
	case EventPermission:
		return e.Permission != nil
	case EventPermissionResolved:
		return e.PermissionResolved != nil
	case EventPromptAccepted:
		return e.PromptAccepted != nil
	case EventUsageDelta:
		return e.UsageDelta != nil
	case EventProvisioning:
		return e.Provisioning != nil
	case EventRewound:
		return e.Rewound != nil
	case EventConfigChanged:
		return e.ConfigChanged != nil
	case EventCatalogUpdated:
		return e.CatalogUpdated != nil
	case EventSubagentUpdate:
		return e.SubagentUpdate != nil
	case EventSubagentDetail:
		return e.SubagentDetail != nil
	case EventNotice:
		return e.Notice != nil
	case EventToolOutputDelta:
		return e.ToolOutputDelta != nil
	case EventTurnDiffUpdated:
		return e.TurnDiffUpdated != nil
	case EventItemStarted:
		return e.ItemStarted != nil
	case EventItemCompleted:
		return e.ItemCompleted != nil
	case EventMessageFinalized:
		return e.MessageFinalized != nil
	case EventSnapshot:
		return e.Snapshot != nil
	case EventLagged:
		return e.Lagged != nil
	case EventBackendSuspended:
		return e.BackendSuspended != nil
	case EventCheckpointList:
		return e.CheckpointList != nil
	case EventSessionInfo:
		return e.SessionInfo != nil
	case EventBackendBound:
		return e.BackendBound != nil
	default:
		return false
	}
}

func knownSessionEventKind(kind SessionEventKind) bool {
	e := SessionEvent{Kind: kind}
	switch kind {
	case EventTurnStarted:
		e.TurnStarted = &TurnStartedEvent{}
	case EventCancel:
		e.Cancel = &CancelEvent{}
	case EventMessageDelta:
		e.MessageDelta = &MessageDeltaEvent{}
	case EventThoughtDelta:
		e.ThoughtDelta = &ThoughtDeltaEvent{}
	case EventToolCall:
		e.ToolCall = &ToolCallEvent{}
	case EventToolResult:
		e.ToolResult = &ToolResultEvent{}
	case EventHeartbeat:
		e.Heartbeat = &HeartbeatEvent{}
	case EventTurnResult:
		e.TurnResult = &TurnResultEvent{}
	case EventDetached:
		e.Detached = &DetachedEvent{}
	case EventAdapterSpecific:
		e.AdapterSpecific = &AdapterSpecificEvent{}
	case EventPlan:
		e.Plan = &PlanEvent{}
	case EventPermission:
		e.Permission = &PermissionEvent{}
	case EventPermissionResolved:
		e.PermissionResolved = &PermissionResolvedEvent{}
	case EventPromptAccepted:
		e.PromptAccepted = &PromptAcceptedEvent{}
	case EventUsageDelta:
		e.UsageDelta = &UsageDeltaEvent{}
	case EventProvisioning:
		e.Provisioning = &ProvisioningEvent{}
	case EventRewound:
		e.Rewound = &RewoundEvent{}
	case EventConfigChanged:
		e.ConfigChanged = &ConfigChangedEvent{}
	case EventCatalogUpdated:
		e.CatalogUpdated = &CatalogUpdatedEvent{}
	case EventSubagentUpdate:
		e.SubagentUpdate = &SubagentUpdateEvent{}
	case EventSubagentDetail:
		e.SubagentDetail = &SubagentDetailEvent{}
	case EventNotice:
		e.Notice = &NoticeEvent{}
	case EventToolOutputDelta:
		e.ToolOutputDelta = &ToolOutputDeltaEvent{}
	case EventTurnDiffUpdated:
		e.TurnDiffUpdated = &TurnDiffUpdatedEvent{}
	case EventItemStarted:
		e.ItemStarted = &ItemStartedEvent{}
	case EventItemCompleted:
		e.ItemCompleted = &ItemCompletedEvent{}
	case EventMessageFinalized:
		e.MessageFinalized = &MessageFinalizedEvent{}
	case EventSnapshot:
		e.Snapshot = &SnapshotEvent{}
	case EventLagged:
		e.Lagged = &LaggedEvent{}
	case EventBackendSuspended:
		e.BackendSuspended = &BackendSuspendedEvent{}
	case EventCheckpointList:
		e.CheckpointList = &CheckpointListEvent{}
	case EventSessionInfo:
		e.SessionInfo = &SessionInfoEvent{}
	case EventBackendBound:
		e.BackendBound = &BackendBoundEvent{}
	default:
		return false
	}
	return e.payloadMatchesKind()
}

func validateToolResultContent(content ToolResultContent) error {
	switch content.Kind {
	case ToolResultText:
		if content.Path != "" || content.MIME != "" || content.OldText != "" || content.NewText != "" || content.MediaType != "" || len(content.Data) != 0 {
			return invalidEvent("invalid text tool result")
		}
	case ToolResultFilePath:
		if strings.TrimSpace(content.Path) == "" || isAbsoluteLocalPath(content.Path) || content.Text != "" || content.MediaType != "" || len(content.Data) != 0 {
			return invalidEvent("tool result paths must be workspace-relative")
		}
	case ToolResultImage:
		if len(content.Data) == 0 || strings.TrimSpace(content.MediaType) == "" || content.Text != "" || content.Path != "" || content.MIME != "" || content.OldText != "" || content.NewText != "" {
			return invalidEvent("invalid image tool result")
		}
	default:
		return invalidEvent("unknown tool result content kind")
	}
	return nil
}

func validateTurnOutcome(outcome TurnOutcome) error {
	switch outcome.Kind {
	case TurnOutcomeEndTurn, TurnOutcomeFailed:
		if outcome.StopReason != nil || outcome.CancelReason != "" {
			return invalidEvent("terminal outcome carries incompatible fields")
		}
	case TurnOutcomeCompleted:
		if outcome.StopReason == nil || outcome.CancelReason != "" {
			return invalidEvent("completed outcome requires stop reason")
		}
		switch outcome.StopReason.Kind {
		case StopReasonEndTurn:
		case StopReasonTruncated:
			if !validTruncation(outcome.StopReason.Truncation) {
				return invalidEvent("truncated outcome requires truncation kind")
			}
		case StopReasonRefused:
		default:
			return invalidEvent("invalid stop reason")
		}
	case TurnOutcomeCancelled:
		if !validCancelReason(outcome.CancelReason) || outcome.StopReason != nil {
			return invalidEvent("cancelled outcome requires cancel reason")
		}
	default:
		return invalidEvent("invalid terminal outcome")
	}
	return nil
}

func validateSafeJSON(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if !json.Valid(raw) {
		return invalidEvent("payload must be valid JSON")
	}
	if len(raw) > 1024*1024 {
		return invalidEvent("payload exceeds canonical event limit")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return invalidEvent("payload must be valid JSON")
	}
	if unsafeCanonicalValue("", value) {
		return invalidEvent("payload contains forbidden credentials, argv/env, or absolute local path")
	}
	return nil
}

func unsafeCanonicalValue(key string, value any) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if sensitiveCanonicalKey(normalized) || normalized == "argv" || normalized == "env" || normalized == "environment" {
		return true
	}
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			if unsafeCanonicalValue(childKey, child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if unsafeCanonicalValue(key, child) {
				return true
			}
		}
	case string:
		if (strings.Contains(normalized, "path") || normalized == "cwd") && isAbsoluteLocalPath(typed) {
			return true
		}
	}
	return false
}

func sensitiveCanonicalKey(key string) bool {
	compact := strings.NewReplacer("-", "", "_", "", ".", "").Replace(key)
	switch compact {
	case "authorization", "proxyauthorization", "cookie", "setcookie", "header", "headers", "apikey", "accesstoken", "refreshtoken", "authtoken", "credential", "credentials", "password", "passwd", "secret", "clientsecret":
		return true
	}
	return strings.HasSuffix(compact, "password") || strings.HasSuffix(compact, "secret") || strings.HasSuffix(compact, "credential")
}

func requireID(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return invalidEvent(field + " is required")
	}
	return nil
}

func invalidEvent(message string) error {
	return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, message, false)
}

func validPermissionKind(kind PermissionKind) bool {
	return kind == PermissionKindTool || kind == PermissionKindAuth
}
func validPlanStatus(status PlanStatus) bool {
	return status == PlanPending || status == PlanInProgress || status == PlanCompleted
}
func validProvisioning(phase ProvisioningPhase) bool {
	return phase == ProvisioningInjecting || phase == ProvisioningToolsWaiting || phase == ProvisioningToolsReady || phase == ProvisioningDegraded || phase == ProvisioningLoadFailed
}
func validSubagentStatus(status SubagentStatus) bool {
	return status == SubagentPendingInit || status == SubagentRunning || status == SubagentInterrupted || status == SubagentCompleted || status == SubagentErrored || status == SubagentShutdown
}
func validItemKind(kind ItemKind) bool {
	return kind == ItemText || kind == ItemThinking || kind == ItemTool || kind == ItemImage
}
func validReasoningContentKind(kind ReasoningContentKind) bool {
	return kind == "" || kind == ReasoningContentRaw || kind == ReasoningContentSummary
}
func validTruncation(kind TruncationKind) bool {
	return kind == TruncationMaxTokens || kind == TruncationContextWindow || kind == TruncationMaxTurns || kind == TruncationBudget || kind == TruncationWire
}
func validCancelReason(reason CancelReason) bool {
	return reason == CancelReasonUserCancel || reason == CancelReasonBargeIn || reason == CancelReasonSocketAbort || reason == CancelReasonToolDenyChain
}
