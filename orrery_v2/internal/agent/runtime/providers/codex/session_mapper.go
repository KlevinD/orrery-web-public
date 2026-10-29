package codex

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *NativeSession) handleNativeLine(line []byte) error {
	if len(line) == 0 {
		return nil
	}
	var frame nativeWireEnvelope
	if err := json.Unmarshal(line, &frame); err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "codex emitted invalid JSON", false)
	}
	method := strings.TrimSpace(frame.Method)
	if len(frame.ID) != 0 && method != "" {
		return s.handleNativeReverseRPC(frame.ID, method, frame.Params)
	}
	if len(frame.ID) != 0 {
		key, ok := nativeRPCIDKey(frame.ID)
		if !ok {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "codex response id is invalid", false)
		}
		s.mu.Lock()
		pending := s.pendingRPC[key]
		delete(s.pendingRPC, key)
		s.mu.Unlock()
		if pending == nil {
			s.emitDiagnostic("codex_unmatched_response", "codex returned an unmatched response")
			return nil
		}
		if frame.Error == nil && pending.promptClientMsgID != "" {
			s.emit(pending.promptGeneration, agentcore.SessionEvent{Kind: agentcore.EventPromptAccepted, PromptAccepted: &agentcore.PromptAcceptedEvent{ClientMsgID: pending.promptClientMsgID}})
		}
		pending.response <- nativeRPCResponse{result: append(json.RawMessage(nil), frame.Result...), rpcErr: frame.Error}
		close(pending.response)
		return nil
	}
	if method == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "codex frame has neither method nor id", false)
	}
	var params map[string]any
	if len(frame.Params) != 0 && string(frame.Params) != "null" {
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "codex notification params are invalid", false)
		}
	}
	if params == nil {
		params = make(map[string]any)
	}
	if threadID := nativeString(params, "threadId", "thread_id"); threadID != "" {
		bound := s.boundThread()
		if bound != "" && bound != threadID && method != "thread/started" {
			return nil
		}
	}
	return s.handleNativeNotification(method, params)
}

func (s *NativeSession) handleNativeNotification(method string, params map[string]any) error {
	switch method {
	case "thread/started", "thread/created":
		threadID := nativeString(params, "threadId")
		if thread := nativeMap(params, "thread"); threadID == "" {
			threadID = nativeString(thread, "id")
		}
		if threadID != "" {
			s.bindThread(threadID)
		}
		return nil
	case "turn/started":
		turnID := nativeString(nativeMap(params, "turn"), "id")
		if turnID == "" {
			turnID = nativeString(params, "turnId")
		}
		if turnID != "" {
			s.bindTurn(turnID, s.currentGeneration())
		}
		return nil
	case "turn/completed":
		s.completeNativeTurn(params)
		return nil
	case "thread/status/changed":
		status := nativeString(nativeMap(params, "status"), "type")
		if status == "" {
			status = nativeString(params, "status")
		}
		switch status {
		case "idle":
			s.markNativeIdle()
		case "systemError":
			s.failActiveTurn("codex reported a system error", nil)
		}
		return nil
	case "serverRequest/resolved":
		s.resolveNativeRequest(params)
		return nil
	case "error":
		if retry, _ := params["willRetry"].(bool); retry {
			s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventHeartbeat, Heartbeat: &agentcore.HeartbeatEvent{}})
			return nil
		}
		message := nativeString(nativeMap(params, "error"), "message")
		if message == "" {
			message = nativeString(params, "message")
		}
		s.failActiveTurn(message, nil)
		return nil
	case "item/agentMessage/delta":
		text, _ := params["delta"].(string)
		s.emitNativeMessage(s.generationForParams(params), nativeItemID(params), text, false)
	case "item/reasoning/textDelta":
		s.emit(s.generationForParams(params), agentcore.SessionEvent{Kind: agentcore.EventThoughtDelta, ThoughtDelta: &agentcore.ThoughtDeltaEvent{ItemID: nativeItemID(params), Text: nativeString(params, "delta"), ReasoningKind: agentcore.ReasoningContentRaw}})
	case "item/reasoning/summaryTextDelta":
		s.emit(s.generationForParams(params), agentcore.SessionEvent{Kind: agentcore.EventThoughtDelta, ThoughtDelta: &agentcore.ThoughtDeltaEvent{ItemID: nativeItemID(params), Text: nativeString(params, "delta"), ReasoningKind: agentcore.ReasoningContentSummary}})
	case "item/commandExecution/outputDelta", "item/fileChange/outputDelta", "item/mcpToolCall/progress":
		s.emit(s.generationForParams(params), agentcore.SessionEvent{Kind: agentcore.EventToolOutputDelta, ToolOutputDelta: &agentcore.ToolOutputDeltaEvent{ItemID: nativeItemID(params), Text: nativeString(params, "delta", "message")}})
	case "item/started":
		s.mapNativeItem(params, false)
	case "item/completed":
		s.mapNativeItem(params, true)
	case "thread/tokenUsage/updated":
		s.mapNativeUsage(params)
	case "turn/plan/updated":
		s.mapNativePlan(params)
	case "turn/diff/updated":
		s.emit(s.generationForParams(params), agentcore.SessionEvent{Kind: agentcore.EventTurnDiffUpdated, TurnDiffUpdated: &agentcore.TurnDiffUpdatedEvent{Diff: nativeString(params, "diff")}})
	case "thread/settings/updated":
		s.applyConfigChanged(params)
	case "mcpServer/startupStatus/updated":
		s.mapNativeProvisioning(params)
	case "mcpServer/oauthLogin/completed":
		if success, ok := params["success"].(bool); ok && !success {
			s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventProvisioning, Provisioning: &agentcore.ProvisioningEvent{Phase: agentcore.ProvisioningDegraded, Reason: "Codex MCP server needs authentication"}})
		}
	case "warning", "guardianWarning", "configWarning":
		message := nativeString(params, "summary", "message")
		if details := nativeString(params, "details"); details != "" {
			message = strings.TrimSpace(message + ": " + details)
		}
		s.emitNotice(nativeDiagnosticText(message, "Codex reported a configuration warning"))
	case "deprecationNotice":
		message := nativeString(params, "summary", "message")
		s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeInfo, Message: nativeDiagnosticText(message, "Codex reported a deprecation notice")}})
	default:
		if strings.HasPrefix(method, "hook/") {
			s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventProvisioning, Provisioning: &agentcore.ProvisioningEvent{Phase: agentcore.ProvisioningToolsWaiting}})
			return nil
		}
		s.emitDiagnostic("codex_notif_"+nativeTag(method), "unrecognized Codex notification")
	}
	return nil
}

func (s *NativeSession) handleNativeReverseRPC(id json.RawMessage, method string, raw json.RawMessage) error {
	var params map[string]any
	if len(raw) != 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &params); err != nil {
			return s.writeReverseError(id, -32602, "invalid request parameters")
		}
	}
	requestID := nativeRequestID(method, id)
	generation := s.generationForParams(params)
	switch method {
	case "account/chatgptAuthTokens/refresh":
		pending := nativePendingAuth{requestID: requestID, wireID: append(json.RawMessage(nil), id...)}
		s.mu.Lock()
		s.pendingAuth[requestID] = pending
		s.mu.Unlock()
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: requestID, Kind: agentcore.PermissionKindAuth}})
		return nil
	case "item/commandExecution/requestApproval", "execCommandApproval", "item/fileChange/requestApproval", "applyPatchApproval":
		toolName := "CommandExecution"
		if strings.Contains(strings.ToLower(method), "file") || strings.Contains(strings.ToLower(method), "patch") {
			toolName = "FileChange"
		}
		pending := nativePendingPermission{requestID: requestID, wireID: append(json.RawMessage(nil), id...), payload: append(json.RawMessage(nil), raw...), toolName: toolName}
		s.mu.Lock()
		if toolName == "FileChange" {
			pending.filePaths = append([]string(nil), s.fileChangePaths[fmt.Sprintf("%d:%s", generation, nativeItemID(params))]...)
		}
		s.pendingPermissions[requestID] = pending
		s.mu.Unlock()
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: requestID, Kind: agentcore.PermissionKindTool, ToolName: toolName}})
		return nil
	case "mcpServer/elicitation/request":
		context := &agentcore.PermissionContext{ServerName: nativeString(params, "serverName")}
		pending := nativePendingPermission{requestID: requestID, wireID: append(json.RawMessage(nil), id...), payload: append(json.RawMessage(nil), raw...), toolName: "McpElicitation", eliciting: true}
		s.mu.Lock()
		s.pendingPermissions[requestID] = pending
		s.mu.Unlock()
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: requestID, Kind: agentcore.PermissionKindTool, ToolName: "McpElicitation", Context: context}})
		return nil
	case "item/tool/requestUserInput":
		questions, ids := nativeUserQuestions(params)
		if len(questions) == 0 {
			return s.writeReverseError(id, -32602, "request contains no answerable questions")
		}
		pending := nativePendingPermission{requestID: requestID, wireID: append(json.RawMessage(nil), id...), payload: append(json.RawMessage(nil), raw...), toolName: "AskUserQuestion", questions: true, answerIDs: ids, view: cloneNativeQuestions(questions)}
		s.mu.Lock()
		s.pendingPermissions[requestID] = pending
		s.mu.Unlock()
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: requestID, Kind: agentcore.PermissionKindTool, ToolName: "AskUserQuestion", Questions: questions}})
		return nil
	case "attestation/generate":
		s.emitDiagnostic("codex_reverse_rpc_rejected", "unsupported Codex infrastructure request was rejected")
		return s.writeReverseError(id, -32601, "client cannot provide attestation")
	default:
		s.emitDiagnostic("codex_reverse_rpc_"+nativeTag(method), "unsupported Codex reverse request was rejected")
		return s.writeReverseError(id, -32601, "method is not supported by this client")
	}
}

func (s *NativeSession) resolveNativeRequest(params map[string]any) {
	rawID, ok := params["requestId"]
	if !ok {
		rawID = params["request_id"]
	}
	wire, err := json.Marshal(rawID)
	if err != nil || rawID == nil {
		s.emitDiagnostic("codex_resolved_without_id", "Codex resolved an unknown request")
		return
	}
	s.mu.Lock()
	if _, exists := s.resolvedRequests[string(wire)]; exists {
		delete(s.resolvedRequests, string(wire))
		s.mu.Unlock()
		return
	}
	for requestID, pending := range s.pendingAuth {
		if string(pending.wireID) == string(wire) {
			delete(s.pendingAuth, requestID)
			s.mu.Unlock()
			s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: requestID, Kind: agentcore.PermissionKindAuth}})
			return
		}
	}
	for requestID, pending := range s.pendingPermissions {
		if string(pending.wireID) == string(wire) {
			delete(s.pendingPermissions, requestID)
			s.mu.Unlock()
			s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: requestID, Kind: agentcore.PermissionKindTool}})
			return
		}
	}
	s.mu.Unlock()
	s.emitDiagnostic("codex_resolved_unknown_request", "Codex resolved an unknown request")
}

func (s *NativeSession) mapNativeItem(params map[string]any, completed bool) {
	item := nativeMap(params, "item")
	typeName := nativeString(item, "type")
	id := nativeString(item, "id")
	generation := s.generationForParams(params)
	kind, bracket := nativeItemKind(typeName)
	if bracket && !completed {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventItemStarted, ItemStarted: &agentcore.ItemStartedEvent{ItemID: id, Kind: kind}})
	}
	switch typeName {
	case "agentMessage":
		// Some providers supply the final message only on item/completed. Reconcile
		// that full snapshot with any streamed prefix, preserving exact whitespace.
		text, _ := item["text"].(string)
		s.emitNativeMessage(generation, id, text, true)
	case "reasoning", "userMessage":
	case "commandExecution", "mcpToolCall", "dynamicToolCall", "fileChange", "webSearch", "imageGeneration":
		if typeName == "fileChange" {
			key := fmt.Sprintf("%d:%s", generation, id)
			s.mu.Lock()
			if completed {
				delete(s.fileChangePaths, key)
			} else {
				if s.fileChangePaths == nil {
					s.fileChangePaths = make(map[string][]string)
				}
				s.fileChangePaths[key] = nativeChangedPaths(item)
			}
			s.mu.Unlock()
		}
		if completed {
			s.emit(generation, s.nativeToolResult(item, typeName, id))
		} else {
			s.emit(generation, nativeToolCall(item, typeName, id))
		}
	case "collabAgentToolCall":
		for _, event := range nativeSubagentEvents(item, id, completed) {
			s.emit(generation, event)
		}
	default:
		s.emitDiagnostic("codex_item_"+nativeTag(typeName), "unrecognized Codex item")
	}
	if bracket && completed {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventItemCompleted, ItemCompleted: &agentcore.ItemCompletedEvent{ItemID: id}})
	}
}

func (s *NativeSession) emitNativeMessage(generation uint64, id, text string, snapshot bool) {
	if text == "" {
		return
	}
	key := fmt.Sprintf("%d:%s", generation, id)
	s.mu.Lock()
	if generation < s.messageGeneration {
		s.mu.Unlock()
		return
	}
	if s.messageText == nil || generation > s.messageGeneration {
		s.messageText = make(map[string]string)
		s.messageGeneration = generation
	}
	previous := s.messageText[key]
	delta := text
	if snapshot {
		if !strings.HasPrefix(text, previous) {
			s.mu.Unlock()
			return
		}
		delta = strings.TrimPrefix(text, previous)
	}
	s.messageText[key] = previous + delta
	s.mu.Unlock()
	if delta != "" {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: id, Text: delta}})
	}
}

func nativeToolCall(item map[string]any, typeName, id string) agentcore.SessionEvent {
	name := typeName
	input := nativeSafeToolInput(item, typeName)
	if typeName == "dynamicToolCall" || typeName == "mcpToolCall" {
		if toolName := nativeString(item, "tool"); toolName != "" {
			name = toolName
		}
		if arguments, ok := item["arguments"].(map[string]any); ok {
			if safe, allowed := nativeScrubValue("", arguments); allowed {
				input, _ = safe.(map[string]any)
			}
		}
	}
	encoded, _ := json.Marshal(input)
	return agentcore.SessionEvent{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: id, Name: name, Subagent: agentcore.SubagentInline, Input: encoded}}
}

func (s *NativeSession) nativeToolResult(item map[string]any, typeName, id string) agentcore.SessionEvent {
	status := strings.ToLower(nativeString(item, "status"))
	isError := status == "failed" || status == "declined" || status == "denied" || status == "cancelled" || status == "canceled"
	if code, ok := nativeNumber(item["exitCode"]); ok && code != 0 {
		isError = true
	}
	content := make([]agentcore.ToolResultContent, 0)
	if text := nativeString(item, "aggregatedOutput"); text != "" {
		content = append(content, agentcore.ToolResultContent{Kind: agentcore.ToolResultText, Text: nativeCanonicalToolText(text)})
	}
	if typeName == "fileChange" && !isError {
		if changes, ok := item["changes"].([]any); ok {
			for _, value := range changes {
				change, _ := value.(map[string]any)
				if path, ok := s.workspaceRelativePath(nativeString(change, "path")); ok {
					content = append(content, agentcore.ToolResultContent{Kind: agentcore.ToolResultFilePath, Path: path, NewText: nativeString(change, "diff")})
				}
			}
		}
	}
	if typeName == "imageGeneration" {
		if path, ok := s.workspaceRelativePath(nativeString(item, "savedPath")); ok {
			content = append(content, agentcore.ToolResultContent{Kind: agentcore.ToolResultFilePath, Path: path})
		}
	}
	if result := nativeMap(item, "result"); result != nil {
		content = append(content, nativeMCPContent(result)...)
	}
	if values, ok := item["contentItems"].([]any); ok {
		for _, value := range values {
			entry, _ := value.(map[string]any)
			switch nativeString(entry, "type") {
			case "inputText":
				content = append(content, agentcore.ToolResultContent{Kind: agentcore.ToolResultText, Text: nativeCanonicalToolText(nativeString(entry, "text"))})
			case "inputImage":
				content = append(content, agentcore.ToolResultContent{Kind: agentcore.ToolResultText, Text: "[image output]"})
			}
		}
	}
	if message := nativeString(nativeMap(item, "error"), "message"); message != "" {
		content = append(content, agentcore.ToolResultContent{Kind: agentcore.ToolResultText, Text: nativeDiagnosticText(message, "codex tool failed")})
	}
	if isError && len(content) == 0 {
		content = append(content, agentcore.ToolResultContent{Kind: agentcore.ToolResultText, Text: "Codex tool " + typeName + " ended with status " + status})
	}
	return agentcore.SessionEvent{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{ToolUseID: id, IsError: isError, Content: content}}
}

// Approval requests in the native protocol identify the item, not its patch.
// Bind every path from the matching started item, including rename targets.
func nativeChangedPaths(item map[string]any) []string {
	changes, ok := item["changes"].([]any)
	if !ok || len(changes) == 0 {
		return nil
	}
	var paths []string
	for _, value := range changes {
		change, ok := value.(map[string]any)
		if !ok || nativeString(change, "path") == "" {
			return nil
		}
		paths = append(paths, nativeString(change, "path"))
		if kind := nativeMap(change, "kind"); kind != nil {
			if moved := nativeString(kind, "move_path", "movePath"); moved != "" {
				paths = append(paths, moved)
			}
		}
	}
	return paths
}

func nativeMCPContent(result map[string]any) []agentcore.ToolResultContent {
	out := make([]agentcore.ToolResultContent, 0)
	if values, ok := result["content"].([]any); ok {
		for _, value := range values {
			entry, _ := value.(map[string]any)
			switch nativeString(entry, "type") {
			case "text":
				out = append(out, agentcore.ToolResultContent{Kind: agentcore.ToolResultText, Text: nativeCanonicalToolText(nativeString(entry, "text"))})
			case "image":
				data, err := base64.StdEncoding.DecodeString(nativeString(entry, "data"))
				if err == nil && len(data) != 0 {
					out = append(out, agentcore.ToolResultContent{Kind: agentcore.ToolResultImage, MediaType: firstNativeValue(nativeString(entry, "mimeType"), "image/png"), Data: data})
				}
			}
		}
	}
	if structured, ok := result["structuredContent"]; ok && structured != nil {
		if safe, allowed := nativeScrubValue("", structured); allowed {
			if encoded, err := json.Marshal(safe); err == nil {
				out = append(out, agentcore.ToolResultContent{Kind: agentcore.ToolResultText, Text: string(encoded)})
			}
		}
	}
	return out
}

func (s *NativeSession) mapNativeUsage(params map[string]any) {
	usage := nativeMap(params, "tokenUsage")
	last := nativeMap(usage, "last")
	input, _ := nativeNumber(last["inputTokens"])
	output, _ := nativeNumber(last["outputTokens"])
	total, _ := nativeNumber(last["totalTokens"])
	if total < input+output {
		total = input + output
	}
	s.emit(s.generationForParams(params), agentcore.SessionEvent{Kind: agentcore.EventUsageDelta, UsageDelta: &agentcore.UsageDeltaEvent{InputTokens: input, OutputTokens: output, TotalTokens: total}})
}

func (s *NativeSession) mapNativePlan(params map[string]any) {
	entries := make([]agentcore.PlanEntry, 0)
	if values, ok := params["plan"].([]any); ok {
		for _, value := range values {
			step, _ := value.(map[string]any)
			content := nativeString(step, "step")
			if content == "" {
				continue
			}
			status := agentcore.PlanPending
			switch nativeString(step, "status") {
			case "inProgress", "in_progress":
				status = agentcore.PlanInProgress
			case "completed":
				status = agentcore.PlanCompleted
			}
			entries = append(entries, agentcore.PlanEntry{Content: content, Status: status})
		}
	}
	s.emit(s.generationForParams(params), agentcore.SessionEvent{Kind: agentcore.EventPlan, Plan: &agentcore.PlanEvent{Entries: entries, Explanation: nativeString(params, "explanation")}})
}

func (s *NativeSession) mapNativeProvisioning(params map[string]any) {
	phase := agentcore.ProvisioningToolsWaiting
	reason := ""
	switch nativeString(params, "status") {
	case "ready":
		phase = agentcore.ProvisioningToolsReady
	case "failed":
		phase, reason = agentcore.ProvisioningLoadFailed, "Codex MCP server failed to start"
	case "cancelled", "canceled":
		phase, reason = agentcore.ProvisioningDegraded, "Codex MCP server startup was cancelled"
	}
	s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventProvisioning, Provisioning: &agentcore.ProvisioningEvent{Phase: phase, Reason: reason}})
}

func nativeSubagentEvents(item map[string]any, fallback string, completed bool) []agentcore.SessionEvent {
	parent := nativeString(item, "senderThreadId")
	label := nativeString(item, "model")
	states := nativeMap(item, "agentsStates")
	out := make([]agentcore.SessionEvent, 0, len(states))
	for ref, raw := range states {
		state, _ := raw.(map[string]any)
		out = append(out, agentcore.SessionEvent{Kind: agentcore.EventSubagentUpdate, SubagentUpdate: &agentcore.SubagentUpdateEvent{Ref: ref, Label: label, ParentRef: parent, Status: nativeSubagentStatus(nativeString(state, "status"), completed)}})
	}
	if len(out) == 0 {
		out = append(out, agentcore.SessionEvent{Kind: agentcore.EventSubagentUpdate, SubagentUpdate: &agentcore.SubagentUpdateEvent{Ref: fallback, Label: label, ParentRef: parent, Status: nativeSubagentStatus("", completed)}})
	}
	return out
}

func nativeSubagentStatus(status string, completed bool) agentcore.SubagentStatus {
	switch status {
	case "pendingInit":
		return agentcore.SubagentPendingInit
	case "interrupted":
		return agentcore.SubagentInterrupted
	case "completed":
		return agentcore.SubagentCompleted
	case "errored":
		return agentcore.SubagentErrored
	case "shutdown", "notFound":
		return agentcore.SubagentShutdown
	case "running":
		return agentcore.SubagentRunning
	default:
		if completed {
			return agentcore.SubagentCompleted
		}
		return agentcore.SubagentRunning
	}
}

func nativeItemKind(typeName string) (agentcore.ItemKind, bool) {
	switch typeName {
	case "agentMessage":
		return agentcore.ItemText, true
	case "reasoning":
		return agentcore.ItemThinking, true
	case "imageGeneration":
		return agentcore.ItemImage, true
	case "commandExecution", "mcpToolCall", "dynamicToolCall", "fileChange", "webSearch":
		return agentcore.ItemTool, true
	default:
		return "", false
	}
}

func nativeSafeToolInput(item map[string]any, typeName string) map[string]any {
	keys := map[string][]string{
		"commandExecution": {"command", "commandActions"},
		"mcpToolCall":      {"server", "tool", "arguments"},
		"dynamicToolCall":  {"tool", "arguments"},
		"fileChange":       {"changes"},
		"webSearch":        {"query"},
		"imageGeneration":  {"prompt"},
	}
	out := make(map[string]any)
	for _, key := range keys[typeName] {
		if value, exists := item[key]; exists {
			if safe, ok := nativeScrubValue(key, value); ok {
				out[key] = safe
			}
		}
	}
	return out
}

func nativeScrubValue(key string, value any) (any, bool) {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "env" || normalized == "environment" || normalized == "argv" || normalized == "cwd" || nativeSensitiveKey(normalized) {
		return nil, false
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any)
		for childKey, child := range typed {
			if safe, ok := nativeScrubValue(childKey, child); ok {
				out[childKey] = safe
			}
		}
		return out, true
	case []any:
		out := make([]any, 0, len(typed))
		for _, child := range typed {
			if safe, ok := nativeScrubValue(key, child); ok {
				out = append(out, safe)
			}
		}
		return out, true
	case string:
		if strings.Contains(normalized, "path") && nativeAbsolutePath(typed) {
			return nil, false
		}
		if normalized == "command" {
			return nativeRedactSecrets(typed), true
		}
		return typed, true
	case nil, bool, float64, json.Number:
		return typed, true
	default:
		return nil, false
	}
}

func (s *NativeSession) workspaceRelativePath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		clean := filepath.Clean(path)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", false
		}
		return filepath.ToSlash(clean), true
	}
	relative, err := filepath.Rel(s.workspace.Directory, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func (s *NativeSession) generationForParams(params map[string]any) uint64 {
	turnID := nativeString(params, "turnId")
	if turnID == "" {
		turnID = nativeString(nativeMap(params, "turn"), "id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation := s.turnGenerations[turnID]; generation != 0 {
		return generation
	}
	return s.turnGeneration
}

func (s *NativeSession) bindThread(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	s.mu.Lock()
	changed := s.threadID != threadID
	s.threadID = threadID
	s.mu.Unlock()
	if changed {
		s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventBackendBound, BackendBound: &agentcore.BackendBoundEvent{BackendSessionID: threadID}})
	}
}

func (s *NativeSession) bindTurn(turnID string, generation uint64) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" || generation == 0 {
		return
	}
	s.mu.Lock()
	if s.turnGenerations[turnID] == generation {
		if !s.terminated[generation] {
			s.activeTurnID = turnID
			s.turnInFlight = true
		}
		s.mu.Unlock()
		return
	}
	if s.terminated[generation] {
		s.turnGenerations[turnID] = generation
		s.mu.Unlock()
		return
	}
	s.activeTurnID = turnID
	s.turnInFlight = true
	s.turnGenerations[turnID] = generation
	s.terminated[generation] = false
	delete(s.idlePending, generation)
	s.mu.Unlock()
}

func (s *NativeSession) emitNotice(message string) {
	s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeWarning, Message: message}})
}

func (s *NativeSession) emitDiagnostic(tag, summary string) {
	s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: nativeTag(tag), Summary: nativeDiagnosticText(summary, "Codex adapter diagnostic")}})
}

func nativeItemID(params map[string]any) string {
	if id := nativeString(params, "itemId"); id != "" {
		return id
	}
	return nativeString(nativeMap(params, "item"), "id")
}

func nativeRequestID(method string, id json.RawMessage) string {
	digest := sha256.Sum256(append(append([]byte(method), 0), id...))
	return "codex-request-" + hex.EncodeToString(digest[:12])
}

func nativeTag(value string) string {
	var out strings.Builder
	for _, char := range strings.TrimSpace(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_' || char == '-' || char == ':' {
			out.WriteRune(char)
		} else {
			out.WriteByte('_')
		}
		if out.Len() >= 120 {
			break
		}
	}
	if out.Len() == 0 {
		return "codex_unknown"
	}
	return out.String()
}

func nativeDiagnosticText(value, fallback string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
	if value == "" {
		value = fallback
	}
	value = nativePathPattern.ReplaceAllString(value, "[local-path]")
	value = nativeRedactSecrets(value)
	if len(value) > 240 {
		value = value[:240]
	}
	return value
}

func nativeCanonicalToolText(value string) string {
	if len(value) > 1024*1024 {
		value = value[:1024*1024]
	}
	value = nativePathPattern.ReplaceAllString(value, "[local-path]")
	return nativeRedactSecrets(value)
}

var nativePathPattern = regexp.MustCompile(`(?i)(?:[a-z]:[\\/][^\s]+|/(?:[^\s/]+/)+[^\s]+)`)

var (
	nativeSecretFlagPattern = regexp.MustCompile(`(?i)(--?(?:api[-_]?key|token|password|secret|credential)\s*(?:=|\s)\s*)([^\s]+)`)
	nativeSecretEnvPattern  = regexp.MustCompile(`(?i)\b([a-z0-9_]*(?:token|api_key|apikey|secret|password|credential)[a-z0-9_]*=)([^\s]+)`)
	nativeURLUserPattern    = regexp.MustCompile(`(?i)(https?://)[^/@\s]+@`)
)

func nativeSensitiveKey(key string) bool {
	for _, needle := range []string{"authorization", "api_key", "apikey", "token", "secret", "password", "credential", "cookie", "headers"} {
		if strings.Contains(key, needle) {
			return true
		}
	}
	return false
}

func nativeRedactSecrets(value string) string {
	value = nativeSecretFlagPattern.ReplaceAllString(value, `${1}[redacted]`)
	value = nativeSecretEnvPattern.ReplaceAllString(value, `${1}[redacted]`)
	return nativeURLUserPattern.ReplaceAllString(value, `${1}[redacted]@`)
}

func nativeAbsolutePath(value string) bool {
	value = strings.TrimSpace(value)
	return filepath.IsAbs(value) || strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\\`) || (len(value) >= 3 && value[1] == ':' && (value[2] == '\\' || value[2] == '/'))
}

func nativeUserQuestions(params map[string]any) ([]agentcore.PermissionQuestion, map[string]string) {
	values, _ := params["questions"].([]any)
	questions := make([]agentcore.PermissionQuestion, 0, len(values))
	ids := make(map[string]string, len(values))
	for _, value := range values {
		question, _ := value.(map[string]any)
		text := nativeString(question, "question")
		id := nativeString(question, "id")
		if text == "" || id == "" {
			continue
		}
		options := make([]agentcore.PermissionQuestionOption, 0)
		if rawOptions, ok := question["options"].([]any); ok {
			for _, raw := range rawOptions {
				option, _ := raw.(map[string]any)
				label := nativeString(option, "label")
				if label != "" {
					options = append(options, agentcore.PermissionQuestionOption{Label: label, Description: nativeString(option, "description")})
				}
			}
		}
		if len(options) == 0 {
			continue
		}
		questions = append(questions, agentcore.PermissionQuestion{Question: text, Header: nativeString(question, "header"), Options: options})
		ids[text] = id
	}
	return questions, ids
}

func cloneNativeQuestions(source []agentcore.PermissionQuestion) []agentcore.PermissionQuestion {
	out := make([]agentcore.PermissionQuestion, len(source))
	for i := range source {
		out[i] = source[i]
		out[i].Options = append([]agentcore.PermissionQuestionOption(nil), source[i].Options...)
	}
	return out
}
