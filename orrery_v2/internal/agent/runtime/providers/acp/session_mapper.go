package acp

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *Session) handshake(ctx context.Context) error {
	initializeRaw, err := s.call(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{"fs": map[string]any{"readTextFile": false, "writeTextFile": false}},
		"clientInfo":         map[string]any{"name": "Orrery", "version": "1"},
	})
	if err != nil {
		return bridgeError(err, "ACP initialize")
	}
	var initialize map[string]any
	if json.Unmarshal(initializeRaw, &initialize) != nil || jsonNumber(initialize, "protocolVersion") < 1 {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "ACP initialize response is invalid", false)
	}
	capabilities := baseCapabilities(s.workspace.Version)
	capabilities.PayloadLimitBytes = uint64(s.native.MaxRecordBytes)
	agentCapabilities := jsonMap(initialize, "agentCapabilities")
	capabilities.SupportsResume = jsonBool(agentCapabilities, "loadSession")
	capabilities.AuthMethods = authMethodIDs(initialize["authMethods"])

	method := "session/new"
	params := map[string]any{"cwd": s.workspace.Directory, "mcpServers": mcpServers(s.workspace.MCPServers)}
	if s.spec.Mode == agentcore.SessionOpenResume {
		if !capabilities.SupportsResume {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "ACP agent does not support session/load", false)
		}
		method = "session/load"
		params["sessionId"] = strings.TrimSpace(s.spec.BackendSessionID)
	}
	sessionRaw, err := s.call(ctx, method, params)
	if err != nil {
		return bridgeError(err, method)
	}
	var response map[string]any
	if json.Unmarshal(sessionRaw, &response) != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "ACP session response is invalid", false)
	}
	backendID := jsonString(response, "sessionId")
	if method == "session/load" && backendID == "" {
		backendID = strings.TrimSpace(s.spec.BackendSessionID)
	}
	if backendID == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "ACP session response is missing sessionId", false)
	}
	if method == "session/load" && backendID != strings.TrimSpace(s.spec.BackendSessionID) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeSessionMismatch, "ACP loaded a different backend session", false)
	}
	applyCatalog(&capabilities, response)
	s.mu.Lock()
	s.backendID = backendID
	s.capabilities = capabilities
	s.mu.Unlock()
	s.emit(0, agentcore.SessionEvent{Kind: agentcore.EventBackendBound, BackendBound: &agentcore.BackendBoundEvent{BackendSessionID: backendID}})
	s.emit(0, agentcore.SessionEvent{Kind: agentcore.EventCatalogUpdated, CatalogUpdated: &agentcore.CatalogUpdatedEvent{
		Models: capabilities.AvailableModels, Modes: capabilities.AvailableModes, ConfigOptions: capabilities.ConfigOptions, SlashCommands: capabilities.SlashCommands,
	}})
	return nil
}

func (s *Session) handleSessionUpdate(raw json.RawMessage) error {
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "ACP session/update params are invalid", false)
	}
	if jsonString(params, "sessionId") != s.boundSession() {
		return nil
	}
	update := jsonMap(params, "update")
	kind := jsonString(update, "sessionUpdate")
	generation := s.currentGeneration()
	switch kind {
	case "agent_message_chunk":
		if text := contentText(update["content"]); text != "" {
			s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: s.nextMessageID("message"), Text: text}})
		}
	case "agent_thought_chunk":
		if text := contentText(update["content"]); text != "" {
			s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventThoughtDelta, ThoughtDelta: &agentcore.ThoughtDeltaEvent{ItemID: s.nextMessageID("thought"), Text: text, ReasoningKind: agentcore.ReasoningContentRaw}})
		}
	case "tool_call":
		id := jsonString(update, "toolCallId")
		name := jsonString(update, "title")
		if name == "" {
			name = jsonString(update, "kind")
		}
		if id != "" && name != "" {
			event := agentcore.SessionEvent{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: id, Name: boundedText(name, "ACP tool"), Input: safeJSON(update["rawInput"])}}
			if err := event.Validate(); err != nil {
				event.ToolCall.Input = nil
			}
			s.emit(generation, event)
		}
	case "tool_call_update":
		id := jsonString(update, "toolCallId")
		status := strings.ToLower(jsonString(update, "status"))
		if id == "" {
			break
		}
		if text := toolContentText(update["content"]); text != "" && status != "completed" && status != "failed" {
			s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventToolOutputDelta, ToolOutputDelta: &agentcore.ToolOutputDeltaEvent{ItemID: id, Text: text}})
		}
		if status == "completed" || status == "failed" {
			content := toolContentText(update["content"])
			if content == "" {
				content = rawOutputText(update["rawOutput"])
			}
			result := &agentcore.ToolResultEvent{ToolUseID: id, IsError: status == "failed"}
			if content != "" {
				result.Content = []agentcore.ToolResultContent{{Kind: agentcore.ToolResultText, Text: content}}
			}
			s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventToolResult, ToolResult: result})
		}
	case "plan":
		entriesRaw, _ := update["entries"].([]any)
		entries := make([]agentcore.PlanEntry, 0, len(entriesRaw))
		for _, rawEntry := range entriesRaw {
			entry, _ := rawEntry.(map[string]any)
			content := jsonString(entry, "content")
			if content == "" {
				continue
			}
			entries = append(entries, agentcore.PlanEntry{Content: content, Status: planStatus(jsonString(entry, "status")), Priority: planPriority(jsonString(entry, "priority"))})
		}
		if len(entries) != 0 {
			s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPlan, Plan: &agentcore.PlanEvent{Entries: entries}})
		}
	case "available_commands_update":
		commands := slashCommands(update["availableCommands"])
		s.mu.Lock()
		s.capabilities.SlashCommands = commands
		s.mu.Unlock()
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventCatalogUpdated, CatalogUpdated: &agentcore.CatalogUpdatedEvent{SlashCommands: commands}})
	case "current_mode_update":
		mode := jsonString(update, "currentModeId")
		if mode != "" {
			s.mu.Lock()
			s.capabilities.CurrentMode = mode
			s.mu.Unlock()
			s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventConfigChanged, ConfigChanged: &agentcore.ConfigChangedEvent{Mode: mode}})
		}
	case "config_option_update":
		options := configOptions(update["configOptions"])
		if len(options) != 0 {
			s.mu.Lock()
			s.capabilities.ConfigOptions = options
			s.mu.Unlock()
			s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventCatalogUpdated, CatalogUpdated: &agentcore.CatalogUpdatedEvent{ConfigOptions: options}})
		}
	default:
		s.emitDiagnostic("acp_update_unknown", "unrecognized ACP session update was ignored")
	}
	return nil
}

func (s *Session) nextMessageID(prefix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messageSerial++
	return prefix + "-" + strings.TrimSpace(s.backendID) + "-" + jsonIntString(s.messageSerial)
}

func mcpServers(servers []MCPServer) []map[string]any {
	out := make([]map[string]any, 0, len(servers))
	for _, server := range servers {
		entry := map[string]any{"name": server.Name}
		switch server.Transport {
		case agentcore.MCPTransportStdio:
			entry["command"] = server.Command
			entry["args"] = append([]string(nil), server.Args...)
			entry["env"] = keyValues(server.Environment)
		case agentcore.MCPTransportHTTP:
			entry["type"] = "http"
			entry["url"] = server.URL
			entry["headers"] = keyValues(server.Headers)
		case agentcore.MCPTransportSSE:
			entry["type"] = "sse"
			entry["url"] = server.URL
			entry["headers"] = keyValues(server.Headers)
		}
		out = append(out, entry)
	}
	return out
}

func keyValues(values map[string]string) []map[string]string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, map[string]string{"name": key, "value": values[key]})
	}
	return out
}

func applyCatalog(capabilities *agentcore.Capabilities, response map[string]any) {
	if models := jsonMap(response, "models"); models != nil {
		capabilities.CurrentModel = jsonString(models, "currentModelId")
		capabilities.AvailableModels = modelsList(models["availableModels"])
	}
	if modes := jsonMap(response, "modes"); modes != nil {
		capabilities.CurrentMode = jsonString(modes, "currentModeId")
		capabilities.AvailableModes = modesList(modes["availableModes"])
	}
	capabilities.ConfigOptions = configOptions(response["configOptions"])
	capabilities.SlashCommands = slashCommands(response["availableCommands"])
	capabilities.SupportedCommands.SetModel = len(capabilities.AvailableModels) != 0
	capabilities.SupportedCommands.SetMode = len(capabilities.AvailableModes) != 0
	capabilities.SupportedCommands.SetConfigOption = len(capabilities.ConfigOptions) != 0
}

func modelsList(value any) []agentcore.ModelInfo {
	items, _ := value.([]any)
	out := make([]agentcore.ModelInfo, 0, len(items))
	for _, item := range items {
		model, _ := item.(map[string]any)
		id := jsonString(model, "modelId")
		if id == "" {
			continue
		}
		name := jsonString(model, "name")
		if name == "" {
			name = id
		}
		out = append(out, agentcore.ModelInfo{ID: id, Name: name, Description: jsonString(model, "description")})
	}
	return out
}

func modesList(value any) []agentcore.ModeInfo {
	items, _ := value.([]any)
	out := make([]agentcore.ModeInfo, 0, len(items))
	for _, item := range items {
		mode, _ := item.(map[string]any)
		id := jsonString(mode, "id")
		if id == "" {
			continue
		}
		name := jsonString(mode, "name")
		if name == "" {
			name = id
		}
		out = append(out, agentcore.ModeInfo{ID: id, Name: name, Description: jsonString(mode, "description")})
	}
	return out
}

func configOptions(value any) []agentcore.ConfigOptionInfo {
	items, _ := value.([]any)
	out := make([]agentcore.ConfigOptionInfo, 0, len(items))
	for _, item := range items {
		option, _ := item.(map[string]any)
		id := jsonString(option, "id")
		name := jsonString(option, "name")
		choices := optionValues(option)
		if id == "" || name == "" || len(choices) == 0 {
			continue
		}
		out = append(out, agentcore.ConfigOptionInfo{ID: id, Name: name, Values: choices, Current: jsonString(option, "currentValue")})
	}
	return out
}

func optionValues(option map[string]any) []string {
	items, _ := option["options"].([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				out = append(out, strings.TrimSpace(typed))
			}
		case map[string]any:
			if value := jsonString(typed, "value"); value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}

func slashCommands(value any) []agentcore.SlashCommandInfo {
	items, _ := value.([]any)
	out := make([]agentcore.SlashCommandInfo, 0, len(items))
	for _, item := range items {
		command, _ := item.(map[string]any)
		name := strings.TrimPrefix(jsonString(command, "name"), "/")
		if name != "" {
			out = append(out, agentcore.SlashCommandInfo{Name: name, Description: jsonString(command, "description")})
		}
	}
	return out
}

func authMethodIDs(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		method, _ := item.(map[string]any)
		if id := jsonString(method, "id"); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func contentText(value any) string {
	content, _ := value.(map[string]any)
	if jsonString(content, "type") != "text" {
		return ""
	}
	return jsonString(content, "text")
}

func toolContentText(value any) string {
	items, _ := value.([]any)
	var parts []string
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if jsonString(entry, "type") == "content" {
			if text := contentText(entry["content"]); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func rawOutputText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		for _, key := range []string{"content", "message", "output"} {
			if text := jsonString(typed, key); text != "" {
				return text
			}
		}
	}
	return ""
}

func safeJSON(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(redactValue("", value))
	if err != nil || len(raw) > 1024*1024 {
		return nil
	}
	return raw
}

func redactValue(key string, value any) any {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.TrimSpace(key)))
	if strings.Contains(normalized, "password") || strings.Contains(normalized, "secret") || strings.Contains(normalized, "credential") || strings.Contains(normalized, "token") || normalized == "authorization" || normalized == "cookie" || normalized == "env" || normalized == "argv" {
		return "[redacted]"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			out[childKey] = redactValue(childKey, child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = redactValue(key, child)
		}
		return out
	case string:
		if isAbsolutePath(typed) {
			return "[redacted-path]"
		}
	}
	return value
}

func isAbsolutePath(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\\`) || (len(value) >= 3 && value[1] == ':' && (value[2] == '\\' || value[2] == '/'))
}

func planStatus(value string) agentcore.PlanStatus {
	switch strings.ToLower(value) {
	case "in_progress", "in-progress":
		return agentcore.PlanInProgress
	case "completed", "done":
		return agentcore.PlanCompleted
	default:
		return agentcore.PlanPending
	}
}

func planPriority(value string) agentcore.PlanPriority {
	switch strings.ToLower(value) {
	case "high":
		return agentcore.PlanPriorityHigh
	case "medium":
		return agentcore.PlanPriorityMedium
	case "low":
		return agentcore.PlanPriorityLow
	default:
		return ""
	}
}

func jsonMap(value map[string]any, key string) map[string]any {
	result, _ := value[key].(map[string]any)
	return result
}

func jsonString(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return strings.TrimSpace(result)
}

func jsonBool(value map[string]any, key string) bool {
	result, _ := value[key].(bool)
	return result
}

func jsonNumber(value map[string]any, key string) float64 {
	result, _ := value[key].(float64)
	return result
}

func jsonIntString(value uint64) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = digits[value%10]
		value /= 10
	}
	return string(buffer[index:])
}
