package pi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

type piNativeMapper struct {
	generation      uint64
	textItem        string
	thoughtItem     string
	startedItems    map[string]agentcore.ItemKind
	toolCalls       map[string]struct{}
	text            strings.Builder
	finalText       string
	visible         bool
	terror          string
	usageInput      uint64
	usageOutput     uint64
	usageCacheRead  uint64
	usageCacheWrite uint64
}

func (m *piNativeMapper) reset(generation uint64) {
	*m = piNativeMapper{
		generation: generation,
		textItem:   fmt.Sprintf("pi-text-%d", generation), thoughtItem: fmt.Sprintf("pi-thought-%d", generation),
		startedItems: make(map[string]agentcore.ItemKind), toolCalls: make(map[string]struct{}),
	}
}

func (s *NativeSession) mapPiEvent(raw []byte) error {
	var event streamEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	s.mu.Lock()
	generation := s.generation
	if generation == 0 && piTurnEvent(event.Type) {
		s.mu.Unlock()
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "pi emitted a turn event without an admitted turn", false)
	}
	mapped, terminal, err := s.mapper.mapEvent(event, generation, s.cancelled)
	if terminal && !s.terminated[generation] {
		s.terminated[generation] = true
		s.active = false
	} else if terminal {
		mapped = nil
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for _, item := range mapped {
		s.emit(generation, item)
	}
	if event.Type == "extension_ui_request" {
		return s.mapPiUIRequest(event)
	}
	return nil
}

func (m *piNativeMapper) mapEvent(event streamEvent, generation uint64, cancelled bool) ([]agentcore.SessionEvent, bool, error) {
	var out []agentcore.SessionEvent
	switch event.Type {
	case "session", "session_info_changed", "thinking_level_changed":
		return nil, false, nil
	case "agent_start":
		// The orchestrator owns the canonical turn boundary after admitting
		// Send. PI's native lifecycle event only confirms provider activity.
		// A retry or follow-up starts a new low-level run in the same turn.
		m.terror = ""
	case "turn_start", "message_start":
		// agent_start is the authoritative PI turn boundary. These nested
		// lifecycle events do not create additional canonical turns.
	case "message_update":
		switch event.AssistantMessageEvent.Type {
		case "text_delta":
			text := sanitizePiText(event.AssistantMessageEvent.Delta)
			if text != "" {
				out = append(out, m.startItem(m.textItem, agentcore.ItemText)...)
				m.text.WriteString(text)
				m.visible = true
				out = append(out, agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: m.textItem, Text: text}})
			}
		case "thinking_delta":
			text := sanitizePiText(event.AssistantMessageEvent.Delta)
			if text != "" {
				out = append(out, m.startItem(m.thoughtItem, agentcore.ItemThinking)...)
				m.visible = true
				out = append(out, agentcore.SessionEvent{Kind: agentcore.EventThoughtDelta, ThoughtDelta: &agentcore.ThoughtDeltaEvent{ItemID: m.thoughtItem, Text: text, ReasoningKind: agentcore.ReasoningContentRaw}})
			}
		case "toolcall_end":
			call := event.AssistantMessageEvent.ToolCall
			out = append(out, m.toolCall(call.ID, call.Name, call.Arguments)...)
		case "error":
			m.terror = firstNonEmpty(event.AssistantMessageEvent.ErrorMessage, event.AssistantMessageEvent.Reason, "pi message error")
			out = append(out, piNotice(agentcore.NoticeWarning, m.terror))
		}
	case "message_end":
		if event.Message.Role == "" || event.Message.Role == "assistant" {
			text := sanitizePiText(assistantMessageText(event.Message))
			if text != "" {
				m.visible = true
				m.finalText = text
				m.text.Reset()
				m.text.WriteString(text)
				out = append(out, m.finalizeText(text)...)
			}
			out = append(out, m.usageEvents(event.Message)...)
			if event.Message.StopReason == "error" {
				m.terror = firstNonEmpty(event.Message.ErrorMessage, "pi message error")
			}
		}
	case "turn_end":
		// PI emits the same finalized assistant message in message_end and
		// turn_end. message_end is authoritative for content and usage.
	case "tool_execution_start":
		out = append(out, m.toolCall(event.ToolCallID, event.ToolName, event.Args)...)
	case "tool_execution_update":
		callID := m.toolID(event.ToolCallID, event.ToolName)
		text := sanitizePiText(contentText(event.PartialResult))
		if text != "" {
			m.visible = true
			out = append(out, agentcore.SessionEvent{Kind: agentcore.EventToolOutputDelta, ToolOutputDelta: &agentcore.ToolOutputDeltaEvent{ItemID: callID, Text: text}})
		}
	case "tool_execution_end":
		callID := m.toolID(event.ToolCallID, event.ToolName)
		if _, seen := m.toolCalls[callID]; !seen {
			out = append(out, m.toolCall(callID, event.ToolName, event.Args)...)
		}
		content := piToolResultContents(event.Result)
		text := piToolResultText(content)
		if len(content) == 0 {
			text = sanitizePiText(jsonString(event.Result))
			if text != "" {
				content = []agentcore.ToolResultContent{{Kind: agentcore.ToolResultText, Text: text}}
			}
		}
		m.visible = true
		if !event.IsError && isPlanTool(event.ToolName) {
			if plan := runtimePlanStateFromOutput(event.ToolName, text); plan != nil {
				entries := make([]agentcore.PlanEntry, 0, len(plan.Items))
				for _, item := range plan.Items {
					status := agentcore.PlanPending
					switch strings.ToLower(strings.TrimSpace(item.Status)) {
					case "in_progress", "active", "running":
						status = agentcore.PlanInProgress
					case "completed", "done":
						status = agentcore.PlanCompleted
					}
					entries = append(entries, agentcore.PlanEntry{Content: sanitizePiText(item.Text), Status: status})
				}
				out = append(out, agentcore.SessionEvent{Kind: agentcore.EventPlan, Plan: &agentcore.PlanEvent{Entries: entries}})
			}
		}
		out = append(out,
			agentcore.SessionEvent{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{ToolUseID: callID, IsError: event.IsError, Content: content}},
			agentcore.SessionEvent{Kind: agentcore.EventItemCompleted, ItemCompleted: &agentcore.ItemCompletedEvent{ItemID: callID}},
		)
	case "extension_error":
		m.terror = firstNonEmpty(event.Error, event.ErrorMessage, "pi extension error")
		m.visible = true
		out = append(out, piNotice(agentcore.NoticeWarning, m.terror))
	case "compaction_start", "compaction_end", "auto_retry_start", "auto_retry_end", "queue_update":
		out = append(out, agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "pi_" + event.Type, Summary: boundedPiText("pi "+strings.ReplaceAll(event.Type, "_", " "), 240)}})
	case "extension_ui_request":
		return nil, false, nil
	case "agent_end":
		if event.WillRetry {
			return nil, false, nil
		}
		for _, message := range event.Messages {
			if message.Role != "assistant" {
				continue
			}
			text := sanitizePiText(assistantMessageText(message))
			if text != "" && !piTextEquivalent(m.finalText, text) {
				m.finalText = text
				m.text.Reset()
				m.text.WriteString(text)
				m.visible = true
				out = append(out, m.finalizeText(text)...)
			}
			if message.StopReason == "error" {
				m.terror = firstNonEmpty(message.ErrorMessage, "pi message error")
			}
		}
		// PI 1.3 may retry or continue after this low-level run. Only the
		// settled event releases the admitted turn and publishes its result.
		return out, false, nil
	case "agent_settled":
		if _, started := m.startedItems[m.textItem]; started {
			out = append(out, agentcore.SessionEvent{Kind: agentcore.EventItemCompleted, ItemCompleted: &agentcore.ItemCompletedEvent{ItemID: m.textItem}})
		}
		if _, started := m.startedItems[m.thoughtItem]; started {
			out = append(out, agentcore.SessionEvent{Kind: agentcore.EventItemCompleted, ItemCompleted: &agentcore.ItemCompletedEvent{ItemID: m.thoughtItem}})
		}
		resultText := sanitizePiText(strings.TrimSpace(m.text.String()))
		result := &agentcore.TurnResultEvent{Epoch: generation, ResultText: resultText, Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeEndTurn}}
		if cancelled || event.Aborted {
			result.IsError = true
			result.Outcome = agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCancelled, CancelReason: agentcore.CancelReasonUserCancel}
		} else if m.terror != "" {
			result.IsError = true
			result.ResultText = sanitizePiText(m.terror)
			result.Outcome = agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}
		} else if !m.visible {
			result.IsError = true
			result.ResultText = "pi completed without assistant or tool output"
			result.Outcome = agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}
		}
		out = append(out, agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: result})
		return out, true, nil
	case "tool_confirmation_request", "tool_confirmation", "permission_request", "approval_request", "command_approval_request":
		return nil, false, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "pi emitted an unsupported native approval request", false)
	default:
		out = append(out, agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "pi_unknown_event", Summary: "unknown pi event was ignored"}})
	}
	return out, false, nil
}

const maxPiToolResultImageBytes = 3 * 1024 * 1024

func piToolResultContents(value any) []agentcore.ToolResultContent {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		if text := sanitizePiText(typed); text != "" {
			return []agentcore.ToolResultContent{{Kind: agentcore.ToolResultText, Text: text}}
		}
	case []any:
		var content []agentcore.ToolResultContent
		for _, item := range typed {
			content = append(content, piToolResultContents(item)...)
		}
		return content
	case map[string]any:
		switch strings.TrimSpace(fmt.Sprint(typed["type"])) {
		case "text":
			if text, ok := typed["text"].(string); ok {
				text = sanitizePiText(text)
				if text != "" {
					return []agentcore.ToolResultContent{{Kind: agentcore.ToolResultText, Text: text}}
				}
			}
		case "image":
			data, dataOK := typed["data"].(string)
			mediaType, mediaTypeOK := typed["mimeType"].(string)
			if dataOK && mediaTypeOK {
				if image, ok := decodePiToolResultImage(data, mediaType); ok {
					return []agentcore.ToolResultContent{{Kind: agentcore.ToolResultImage, Data: image, MediaType: mediaType}}
				}
			}
		}
		return piToolResultContents(typed["content"])
	}
	return nil
}

func decodePiToolResultImage(value, mediaType string) ([]byte, bool) {
	value = strings.TrimSpace(value)
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if value == "" || !strings.HasPrefix(mediaType, "image/") || len(mediaType) > 128 || base64.StdEncoding.DecodedLen(len(value)) > maxPiToolResultImageBytes {
		return nil, false
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || len(decoded) == 0 || len(decoded) > maxPiToolResultImageBytes {
		return nil, false
	}
	return decoded, true
}

func piToolResultText(content []agentcore.ToolResultContent) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if item.Kind == agentcore.ToolResultText && strings.TrimSpace(item.Text) != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (m *piNativeMapper) startItem(id string, kind agentcore.ItemKind) []agentcore.SessionEvent {
	if _, exists := m.startedItems[id]; exists {
		return nil
	}
	m.startedItems[id] = kind
	return []agentcore.SessionEvent{{Kind: agentcore.EventItemStarted, ItemStarted: &agentcore.ItemStartedEvent{ItemID: id, Kind: kind}}}
}

func (m *piNativeMapper) finalizeText(text string) []agentcore.SessionEvent {
	out := m.startItem(m.textItem, agentcore.ItemText)
	out = append(out, agentcore.SessionEvent{Kind: agentcore.EventMessageFinalized, MessageFinalized: &agentcore.MessageFinalizedEvent{Message: agentcore.FinalizedMessage{ItemID: m.textItem, Kind: agentcore.ItemText, Content: text}}})
	return out
}

func (m *piNativeMapper) toolCall(callID, name string, input map[string]any) []agentcore.SessionEvent {
	callID = m.toolID(callID, name)
	if _, exists := m.toolCalls[callID]; exists {
		return nil
	}
	name = safePiIdentifier(firstNonEmpty(name, "tool"), "tool")
	raw, _ := json.Marshal(input)
	if unsafePiJSON(raw) {
		raw = nil
	}
	m.toolCalls[callID] = struct{}{}
	m.startedItems[callID] = agentcore.ItemTool
	m.visible = true
	return []agentcore.SessionEvent{
		{Kind: agentcore.EventItemStarted, ItemStarted: &agentcore.ItemStartedEvent{ItemID: callID, Kind: agentcore.ItemTool}},
		{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: callID, Name: name, Input: raw}},
	}
}

func (m *piNativeMapper) toolID(callID, name string) string {
	value := strings.TrimSpace(callID)
	if value == "" {
		value = fmt.Sprintf("%s-%d", firstNonEmpty(name, "tool"), len(m.toolCalls)+1)
	}
	return safePiIdentifier(value, "pi-tool")
}

func (m *piNativeMapper) usageEvents(message piMessage) []agentcore.SessionEvent {
	input := nonNegativePiUsage(message.Usage.Input)
	output := nonNegativePiUsage(message.Usage.Output)
	cacheRead := nonNegativePiUsage(message.Usage.CacheRead)
	cacheWrite := nonNegativePiUsage(message.Usage.CacheWrite)
	if input == 0 && output == 0 && cacheRead == 0 && cacheWrite == 0 {
		return nil
	}
	m.usageInput += input
	m.usageOutput += output
	m.usageCacheRead += cacheRead
	m.usageCacheWrite += cacheWrite
	return []agentcore.SessionEvent{{Kind: agentcore.EventUsageDelta, UsageDelta: &agentcore.UsageDeltaEvent{InputTokens: input, OutputTokens: output, TotalTokens: input + output + cacheRead + cacheWrite}}}
}

func nonNegativePiUsage(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

func piTurnEvent(kind string) bool {
	switch kind {
	case "agent_start", "agent_end", "agent_settled", "turn_start", "turn_end", "message_start", "message_update", "message_end", "tool_execution_start", "tool_execution_update", "tool_execution_end", "extension_error":
		return true
	default:
		return false
	}
}

func piNotice(level agentcore.NoticeLevel, message string) agentcore.SessionEvent {
	message = boundedPiText(sanitizePiText(message), 240)
	if message == "" {
		message = "pi provider notice"
	}
	return agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: level, Message: message}}
}

func (s *NativeSession) mapPiUIRequest(event streamEvent) error {
	wireID := strings.TrimSpace(event.ID)
	id := safePiIdentifier(wireID, "pi-ui")
	method := strings.TrimSpace(event.Method)
	if wireID == "" || method == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "pi extension UI request is invalid", false)
	}
	if method == "notify" {
		s.emit(s.currentGeneration(), piNotice(piNotifyLevel(event.NotifyType), event.Message.UIText))
		return nil
	}
	switch method {
	case "setStatus", "setWidget", "setTitle", "set_editor_text":
		s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "pi_extension_ui_" + strings.ToLower(method), Summary: "pi extension updated non-interactive UI state"}})
		return nil
	case "input", "editor":
		if err := s.writeFrame(map[string]any{"type": "extension_ui_response", "id": wireID, "cancelled": true}); err != nil {
			return err
		}
		s.emit(s.currentGeneration(), piNotice(agentcore.NoticeWarning, "pi extension requested unsupported interactive input and was cancelled"))
		return nil
	case "select", "confirm":
	default:
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "pi extension requested an unknown UI method", false)
	}
	options := append([]string(nil), event.Options...)
	if method == "confirm" {
		options = []string{"Allow", "Deny"}
	}
	questionOptions := make([]agentcore.PermissionQuestionOption, 0, len(options))
	acceptedOptions := make([]string, 0, len(options))
	for _, option := range options {
		if label := boundedPiText(sanitizePiText(option), 120); label != "" {
			questionOptions = append(questionOptions, agentcore.PermissionQuestionOption{Label: label})
			acceptedOptions = append(acceptedOptions, label)
		}
	}
	if len(questionOptions) == 0 {
		return s.writeFrame(map[string]any{"type": "extension_ui_response", "id": wireID, "cancelled": true})
	}
	title := boundedPiText(sanitizePiText(firstNonEmpty(event.Title, event.Message.UIText, "PI request")), 240)
	s.mu.Lock()
	if _, exists := s.ui[id]; exists {
		s.mu.Unlock()
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "duplicate pi extension UI request", false)
	}
	if _, exists := s.approvals[id]; exists {
		s.mu.Unlock()
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "duplicate pi extension UI request", false)
	}
	s.ui[id] = piNativeUIRequest{method: method, title: title, wireID: wireID, options: acceptedOptions}
	generation := s.generation
	s.mu.Unlock()
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: id, Kind: agentcore.PermissionKindTool, ToolName: "AskUserQuestion", Questions: []agentcore.PermissionQuestion{{Question: title, Options: questionOptions}}}})
	return nil
}

func piNotifyLevel(value string) agentcore.NoticeLevel {
	if strings.EqualFold(strings.TrimSpace(value), "info") {
		return agentcore.NoticeInfo
	}
	return agentcore.NoticeWarning
}
