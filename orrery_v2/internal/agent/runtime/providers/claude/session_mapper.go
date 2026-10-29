package claude

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *NativeSession) handleClaudeLine(line []byte) error {
	if len(line) == 0 {
		return nil
	}
	var frame map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.UseNumber()
	if err := decoder.Decode(&frame); err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "claude emitted invalid JSON", false)
	}
	typeName := claudeString(frame, "type")
	switch typeName {
	case "system":
		s.mapClaudeSystem(frame)
	case "assistant":
		s.mapClaudeAssistant(frame)
	case "user":
		s.mapClaudeUser(frame)
	case "result":
		s.mapClaudeResult(frame)
	case "stream_event":
		s.mapClaudeStreamEvent(frame)
	case "control_request":
		s.mapClaudePermission(frame)
	case "control_cancel_request":
		s.mapClaudePermissionCancel(frame)
	case "control_response":
		s.mapClaudeControlResponse(frame)
	default:
		s.emitDiagnostic("claude_frame_"+claudeTag(typeName), "unrecognized Claude stream-json frame")
	}
	return nil
}

func (s *NativeSession) mapClaudeSystem(frame map[string]any) {
	subtype := claudeString(frame, "subtype")
	switch subtype {
	case "init":
		generation := s.openClaudeGeneration()
		if backendID := claudeString(frame, "session_id"); backendID != "" {
			if _, err := uuid.Parse(backendID); err == nil {
				s.mu.Lock()
				changed := !s.backendBound || s.backendSessionID != backendID
				s.backendSessionID = backendID
				s.backendBound = true
				s.claudeID = backendID
				s.mu.Unlock()
				if changed {
					s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventBackendBound, BackendBound: &agentcore.BackendBoundEvent{BackendSessionID: backendID}})
				}
			} else {
				s.emitDiagnostic("claude_invalid_session_id", "Claude reported an invalid backend session id")
			}
		}
		s.applyClaudeObservedConfig(generation, claudeString(frame, "model"), claudeString(frame, "permissionMode"))
		if servers, ok := frame["mcp_servers"].([]any); ok {
			for _, raw := range servers {
				server, _ := raw.(map[string]any)
				phase := agentcore.ProvisioningToolsWaiting
				reason := ""
				switch claudeString(server, "status") {
				case "connected":
					phase = agentcore.ProvisioningToolsReady
				case "failed":
					phase, reason = agentcore.ProvisioningLoadFailed, "Claude MCP server failed"
				case "needs-auth", "needs_auth":
					phase, reason = agentcore.ProvisioningDegraded, "Claude MCP server needs authentication"
				}
				s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventProvisioning, Provisioning: &agentcore.ProvisioningEvent{Phase: phase, Reason: reason}})
			}
		}
	case "status":
		s.applyClaudeObservedConfig(s.claudeEventGeneration(), "", claudeString(frame, "permissionMode"))
	case "api_retry", "compact_boundary", "compacting":
		s.emit(s.claudeEventGeneration(), agentcore.SessionEvent{Kind: agentcore.EventHeartbeat, Heartbeat: &agentcore.HeartbeatEvent{}})
	case "task_started", "task_progress", "task_updated", "task_notification":
		s.mapClaudeTask(frame)
	default:
		s.emitDiagnostic("claude_system_"+claudeTag(subtype), "unrecognized Claude system frame")
	}
}

func (s *NativeSession) mapClaudeAssistant(frame map[string]any) {
	message := claudeMap(frame, "message")
	messageID := claudeString(message, "id")
	if messageID == "" {
		messageID = fmt.Sprintf("claude-message-%d", s.claudeEventGeneration())
	}
	parent := claudeString(frame, "parent_tool_use_id")
	content, _ := message["content"].([]any)
	s.mu.Lock()
	streamItem := s.stream.itemID
	streamedText := streamItem == messageID && s.stream.streamedText
	streamedThinking := streamItem == messageID && s.stream.streamedThinking
	s.mu.Unlock()
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		switch claudeString(block, "type") {
		case "text":
			if !streamedText {
				if text := claudeStringRaw(block, "text"); text != "" {
					s.emit(s.claudeEventGeneration(), agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: messageID + ":text", Text: text}})
				}
			}
		case "thinking":
			if !streamedThinking {
				if text := claudeStringRaw(block, "thinking"); text != "" {
					s.emit(s.claudeEventGeneration(), agentcore.SessionEvent{Kind: agentcore.EventThoughtDelta, ThoughtDelta: &agentcore.ThoughtDeltaEvent{ItemID: messageID + ":thinking", Text: text, ReasoningKind: agentcore.ReasoningContentRaw}})
				}
			}
		case "tool_use":
			s.mapClaudeToolUse(block, parent)
		default:
			s.emitDiagnostic("claude_assistant_block", "unrecognized Claude assistant content block")
		}
	}
}

func (s *NativeSession) mapClaudeToolUse(block map[string]any, parent string) {
	id := claudeString(block, "id")
	name := claudeString(block, "name")
	if id == "" || name == "" {
		s.emitDiagnostic("claude_malformed_tool_use", "Claude emitted a malformed tool call")
		return
	}
	input := claudeMap(block, "input")
	safe := s.claudeSafeToolInput(input)
	encoded, _ := json.Marshal(safe)
	generation := s.claudeEventGeneration()
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: id, Name: name, Subagent: agentcore.SubagentInline, Input: encoded, ParentToolUseID: parent}})
	if plan := s.plan.recordToolUse(name, id, input); plan != nil {
		s.emit(generation, claudePlanEvent(plan))
	}
}

func (s *NativeSession) mapClaudeUser(frame map[string]any) {
	message := claudeMap(frame, "message")
	content, _ := message["content"].([]any)
	hasToolResult := false
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if claudeString(block, "type") == "tool_result" {
			hasToolResult = true
			break
		}
	}
	if !hasToolResult {
		if clientID := claudeString(frame, "uuid"); clientID != "" {
			if generation, claimed := s.claimClaudePrompt(clientID); claimed {
				s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPromptAccepted, PromptAccepted: &agentcore.PromptAcceptedEvent{ClientMsgID: clientID}})
			}
		}
	}
	parent := claudeString(frame, "parent_tool_use_id")
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if claudeString(block, "type") != "tool_result" {
			continue
		}
		id := claudeString(block, "tool_use_id")
		if id == "" {
			s.emitDiagnostic("claude_malformed_tool_result", "Claude emitted a malformed tool result")
			continue
		}
		parts, output := claudeToolResultContent(block["content"])
		failed, _ := block["is_error"].(bool)
		generation := s.claudeEventGeneration()
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{ToolUseID: id, IsError: failed, Content: parts, ParentToolUseID: parent}})
		if plan := s.plan.recordToolResult(id, output, failed); plan != nil {
			s.emit(generation, claudePlanEvent(plan))
		}
	}
}

func (s *NativeSession) mapClaudeStreamEvent(frame map[string]any) {
	event := claudeMap(frame, "event")
	subtype := claudeString(event, "type")
	switch subtype {
	case "message_start":
		message := claudeMap(event, "message")
		s.mu.Lock()
		s.stream.itemID = claudeString(message, "id")
		if s.stream.itemID == "" {
			s.stream.itemID = fmt.Sprintf("claude-message-%d", s.claudeEventGenerationLocked())
		}
		s.stream.blockKinds = make(map[uint64]string)
		s.stream.streamedText = false
		s.stream.streamedThinking = false
		s.mu.Unlock()
	case "content_block_start":
		index, ok := claudeUint(event["index"])
		if !ok {
			return
		}
		block := claudeMap(event, "content_block")
		kind := claudeString(block, "type")
		s.mu.Lock()
		s.stream.blockKinds[index] = kind
		s.mu.Unlock()
	case "content_block_delta":
		index, ok := claudeUint(event["index"])
		if !ok {
			return
		}
		delta := claudeMap(event, "delta")
		s.mu.Lock()
		kind := s.stream.blockKinds[index]
		itemID := s.stream.itemID
		if kind == "text" && claudeString(delta, "type") == "text_delta" {
			s.stream.streamedText = true
		}
		if kind == "thinking" && claudeString(delta, "type") == "thinking_delta" {
			s.stream.streamedThinking = true
		}
		generation := s.claudeEventGenerationLocked()
		s.mu.Unlock()
		if kind == "text" {
			if text := claudeStringRaw(delta, "text"); text != "" {
				s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: itemID + ":text", Text: text}})
			}
		} else if kind == "thinking" {
			if text := claudeStringRaw(delta, "thinking"); text != "" {
				s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventThoughtDelta, ThoughtDelta: &agentcore.ThoughtDeltaEvent{ItemID: itemID + ":thinking", Text: text, ReasoningKind: agentcore.ReasoningContentRaw}})
			}
		}
	case "content_block_stop", "message_stop":
	case "message_delta":
		delta := claudeMap(event, "delta")
		if reason := claudeString(delta, "stop_reason"); reason != "" && reason != "tool_use" {
			generation := s.claudeEventGeneration()
			s.completeClaudeTurn(generation, claudeTurnEvent(generation, false, "", nil, reason))
		}
	default:
		s.emitDiagnostic("claude_stream_"+claudeTag(subtype), "unrecognized Claude partial stream event")
	}
}

func (s *NativeSession) mapClaudePermission(frame map[string]any) {
	request := claudeMap(frame, "request")
	if claudeString(request, "subtype") != "can_use_tool" {
		s.emitDiagnostic("claude_control_request", "unsupported Claude control request")
		return
	}
	requestID := claudeString(frame, "request_id")
	toolUseID := claudeString(request, "tool_use_id")
	if requestID == "" || toolUseID == "" {
		s.emitDiagnostic("claude_malformed_permission", "Claude emitted a malformed permission request")
		return
	}
	toolName := claudeString(request, "tool_name")
	if toolName == "" {
		toolName = "Tool"
	}
	input := cloneClaudeObject(claudeMap(request, "input"))
	questions := []agentcore.PermissionQuestion(nil)
	if toolName == "AskUserQuestion" {
		var valid bool
		questions, valid = claudePermissionQuestions(input)
		if !valid {
			_ = s.writeFrame(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": requestID, "error": "invalid AskUserQuestion payload"}})
			s.emitDiagnostic("claude_invalid_questions", "Claude emitted an invalid question request")
			return
		}
	}
	pending := claudePendingPermission{requestID: requestID, toolUseID: toolUseID, toolName: toolName, input: input, questions: questions}
	s.mu.Lock()
	s.pendingPerms[requestID] = pending
	generation := s.claudeEventGenerationLocked()
	s.mu.Unlock()
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: requestID, Kind: agentcore.PermissionKindTool, ToolName: toolName, Questions: cloneClaudeQuestions(questions)}})
}

func (s *NativeSession) mapClaudePermissionCancel(frame map[string]any) {
	requestID := claudeString(frame, "request_id")
	s.mu.Lock()
	_, exists := s.pendingPerms[requestID]
	delete(s.pendingPerms, requestID)
	generation := s.claudeEventGenerationLocked()
	s.mu.Unlock()
	if !exists {
		s.emitDiagnostic("claude_unknown_permission_cancel", "Claude retracted an unknown permission request")
		return
	}
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: requestID, Kind: agentcore.PermissionKindTool}})
}

func (s *NativeSession) mapClaudeControlResponse(frame map[string]any) {
	response := claudeMap(frame, "response")
	requestID := claudeString(response, "request_id")
	subtype := claudeString(response, "subtype")
	inner := claudeMap(response, "response")
	if len(inner) != 0 && (inner["models"] != nil || inner["commands"] != nil) {
		s.applyClaudeDiscovery(inner)
	}
	if strings.HasPrefix(requestID, "ctl-qsi-usage-") {
		s.mapClaudeContextUsage(inner)
	} else if strings.HasPrefix(requestID, "ctl-qsi-cost-") {
		s.mapClaudeSessionCost(inner)
	}

	s.mu.Lock()
	pendingCall := s.pendingControl[requestID]
	delete(s.pendingControl, requestID)
	setting, hasSetting := s.pendingSettings[requestID]
	delete(s.pendingSettings, requestID)
	s.mu.Unlock()

	if pendingCall != nil {
		if subtype == "error" {
			pendingCall.response <- claudeControlResponse{err: agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "claude rejected control request", false)}
		} else {
			pendingCall.response <- claudeControlResponse{response: inner}
		}
		close(pendingCall.response)
	}
	if hasSetting {
		if subtype == "error" {
			errorText := claudeStringRaw(response, "error")
			if setting.kind == "mode" {
				s.emitDiagnostic("mode_switch_rejected", firstClaudeString(errorText, "Claude rejected the requested permission mode"))
			} else {
				s.emitNotice("Claude rejected the requested " + setting.kind + " setting: " + firstClaudeString(errorText, "backend rejection"))
			}
			return
		}
		if subtype != "success" {
			return
		}
		s.applyClaudeSettingAck(setting)
	}
	if pendingCall == nil && !hasSetting && requestID != "" && !strings.HasPrefix(requestID, "ctl-qsi-") {
		s.emitDiagnostic("claude_unmatched_control_response", "Claude returned an unmatched control response")
	}
}

func (s *NativeSession) mapClaudeTask(frame map[string]any) {
	ref := claudeString(frame, "task_id")
	if ref == "" {
		s.emitDiagnostic("claude_task_without_id", "Claude task update has no stable id")
		return
	}
	subtype := claudeString(frame, "subtype")
	status := agentcore.SubagentRunning
	if subtype == "task_notification" {
		switch claudeString(frame, "status") {
		case "failed":
			status = agentcore.SubagentErrored
		case "stopped":
			status = agentcore.SubagentInterrupted
		default:
			status = agentcore.SubagentCompleted
		}
	}
	label := claudeString(frame, "workflow_name", "subagent_type", "description")
	parent := claudeString(frame, "tool_use_id")
	generation := s.claudeEventGeneration()
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventSubagentUpdate, SubagentUpdate: &agentcore.SubagentUpdateEvent{Ref: ref, Label: label, Status: status, ParentRef: parent}})
	if progress, ok := frame["workflow_progress"].([]any); ok {
		for _, raw := range progress {
			agent, _ := raw.(map[string]any)
			if claudeString(agent, "type") != "workflow_agent" {
				continue
			}
			agentRef := claudeString(agent, "agentId", "label")
			if agentRef == "" {
				continue
			}
			loop := agentcore.WorkflowLoopState("")
			switch claudeString(agent, "state") {
			case "start":
				loop = agentcore.WorkflowLoopStart
			case "progress":
				loop = agentcore.WorkflowLoopProgress
			case "done":
				loop = agentcore.WorkflowLoopDone
			}
			tokens := claudeUintPointer(agent["tokens"])
			calls := claudeUintPointer(agent["toolCalls"])
			s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventSubagentDetail, SubagentDetail: &agentcore.SubagentDetailEvent{Ref: agentRef, ParentRef: ref, Label: claudeString(agent, "label"), LoopState: loop, Model: claudeString(agent, "model"), Tokens: tokens, ToolCalls: calls, LastToolName: claudeString(agent, "lastToolName")}})
		}
	}
}

func (s *NativeSession) mapClaudeContextUsage(inner map[string]any) {
	used, _ := claudeUint(inner["totalTokens"])
	maximum, _ := claudeUint(inner["maxTokens"])
	categories := make([]agentcore.ContextUsageCategory, 0)
	if values, ok := inner["categories"].([]any); ok {
		for _, raw := range values {
			entry, _ := raw.(map[string]any)
			name := claudeString(entry, "name")
			tokens, _ := claudeUint(entry["tokens"])
			if name != "" {
				categories = append(categories, agentcore.ContextUsageCategory{Name: name, Tokens: tokens})
			}
		}
	}
	s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventSessionInfo, SessionInfo: &agentcore.SessionInfoEvent{ContextUsage: &agentcore.ContextUsage{Used: used, Max: maximum, Categories: categories}}})
}

func (s *NativeSession) mapClaudeSessionCost(inner map[string]any) {
	text := claudeDiagnosticText(claudeStringRaw(inner, "text"), "Claude did not provide a session cost")
	s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventSessionInfo, SessionInfo: &agentcore.SessionInfoEvent{CostText: text}})
}

func claudePermissionQuestions(input map[string]any) ([]agentcore.PermissionQuestion, bool) {
	values, ok := input["questions"].([]any)
	if !ok || len(values) == 0 {
		return nil, false
	}
	out := make([]agentcore.PermissionQuestion, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		question, _ := raw.(map[string]any)
		text := claudeString(question, "question")
		if text == "" {
			return nil, false
		}
		if _, exists := seen[text]; exists {
			return nil, false
		}
		seen[text] = struct{}{}
		optionsRaw, _ := question["options"].([]any)
		options := make([]agentcore.PermissionQuestionOption, 0, len(optionsRaw))
		for _, optionRaw := range optionsRaw {
			option, _ := optionRaw.(map[string]any)
			label := claudeString(option, "label")
			if label == "" {
				return nil, false
			}
			options = append(options, agentcore.PermissionQuestionOption{Label: label, Description: claudeString(option, "description")})
		}
		if len(options) == 0 {
			return nil, false
		}
		multi, _ := question["multiSelect"].(bool)
		out = append(out, agentcore.PermissionQuestion{Question: text, Header: claudeString(question, "header"), Options: options, MultiSelect: multi})
	}
	return out, true
}

func claudeToolResultContent(value any) ([]agentcore.ToolResultContent, string) {
	parts := make([]agentcore.ToolResultContent, 0)
	var output strings.Builder
	appendText := func(text string) {
		text = claudeCanonicalText(text, 16*1024)
		if text == "" {
			return
		}
		parts = append(parts, agentcore.ToolResultContent{Kind: agentcore.ToolResultText, Text: text})
		output.WriteString(text)
	}
	switch typed := value.(type) {
	case string:
		appendText(typed)
	case []any:
		for _, raw := range typed {
			entry, _ := raw.(map[string]any)
			switch claudeString(entry, "type") {
			case "text":
				appendText(claudeStringRaw(entry, "text"))
			case "image":
				source := claudeMap(entry, "source")
				if claudeString(source, "type") != "base64" {
					continue
				}
				data, err := base64.StdEncoding.DecodeString(claudeStringRaw(source, "data"))
				if err == nil && len(data) != 0 {
					parts = append(parts, agentcore.ToolResultContent{Kind: agentcore.ToolResultImage, MediaType: claudeString(source, "media_type"), Data: data})
				}
			}
		}
	}
	return parts, output.String()
}

func claudePlanEvent(plan *agentcore.RuntimePlanState) agentcore.SessionEvent {
	entries := make([]agentcore.PlanEntry, 0, len(plan.Items))
	for _, item := range plan.Items {
		status := agentcore.PlanPending
		switch item.Status {
		case "in_progress":
			status = agentcore.PlanInProgress
		case "completed", "cancelled":
			status = agentcore.PlanCompleted
		}
		entries = append(entries, agentcore.PlanEntry{Content: item.Text, Status: status})
	}
	return agentcore.SessionEvent{Kind: agentcore.EventPlan, Plan: &agentcore.PlanEvent{Entries: entries}}
}

func (s *NativeSession) openClaudeGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	generation := s.turnGeneration
	if len(s.generationQueue) != 0 {
		generation = s.generationQueue[0]
		s.generationQueue = s.generationQueue[1:]
	}
	s.turnOpen = generation
	return generation
}

func (s *NativeSession) claudeEventGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claudeEventGenerationLocked()
}

func (s *NativeSession) claudeEventGenerationLocked() uint64 {
	if s.turnOpen != 0 {
		return s.turnOpen
	}
	return s.turnGeneration
}

func (s *NativeSession) claimClaudePrompt(clientID string) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	generation, exists := s.clientGeneration[clientID]
	if !exists || generation == 0 {
		return 0, false
	}
	delete(s.clientGeneration, clientID)
	return generation, true
}

func claudeString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func claudeStringRaw(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return value
}

func claudeMap(object map[string]any, key string) map[string]any {
	value, _ := object[key].(map[string]any)
	return value
}

func claudeUint(value any) (uint64, bool) {
	switch typed := value.(type) {
	case json.Number:
		value, err := typed.Int64()
		return uint64(value), err == nil && value >= 0
	case float64:
		if typed >= 0 && typed == float64(uint64(typed)) {
			return uint64(typed), true
		}
	}
	return 0, false
}

func claudeUintPointer(value any) *uint64 {
	number, ok := claudeUint(value)
	if !ok {
		return nil
	}
	return &number
}

func sortedClaudeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
