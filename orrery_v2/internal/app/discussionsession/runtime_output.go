package discussionsession

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func firstRuntimeText(messages []agentcore.RuntimeMessage) string {
	if final := finalRuntimeText(messages); final != "" {
		return final
	}
	var chunks []runtimeTextChunk
	for _, message := range messages {
		if message.Type == agentcore.RuntimeMessageText && strings.TrimSpace(message.Content) != "" {
			chunks = append(chunks, runtimeTextChunk{Content: message.Content})
			continue
		}
		if text := discussionOutputToolText(message); text != "" {
			chunks = append(chunks, runtimeTextChunk{Content: text, Discrete: true})
		}
	}
	if len(chunks) == 0 {
		return ""
	}
	contents := make([]string, 0, len(chunks))
	hasDiscrete := false
	for _, chunk := range chunks {
		contents = append(contents, chunk.Content)
		hasDiscrete = hasDiscrete || chunk.Discrete
	}
	if !hasDiscrete {
		last := strings.TrimSpace(contents[len(contents)-1])
		if len(contents) > 1 && strings.TrimSpace(strings.Join(contents[:len(contents)-1], "")) == last {
			return last
		}
		return strings.TrimSpace(strings.Join(contents, ""))
	}
	parts := make([]string, 0, len(chunks))
	var textBuffer strings.Builder
	flushText := func() {
		if text := strings.TrimSpace(textBuffer.String()); text != "" {
			parts = append(parts, text)
		}
		textBuffer.Reset()
	}
	for _, chunk := range chunks {
		if chunk.Discrete {
			flushText()
			if text := strings.TrimSpace(chunk.Content); text != "" {
				parts = append(parts, text)
			}
			continue
		}
		textBuffer.WriteString(chunk.Content)
	}
	flushText()
	if len(parts) == 0 {
		return ""
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if len(parts) > 1 && runtimeTextEquivalent(parts[:len(parts)-1], last) {
		return last
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func finalRuntimeText(messages []agentcore.RuntimeMessage) string {
	var final string
	for _, message := range messages {
		if message.Type == agentcore.RuntimeMessageText && message.Status == agentcore.RuntimeMessageStatusFinal && strings.TrimSpace(message.Content) != "" {
			final = strings.TrimSpace(message.Content)
		}
	}
	return final
}

type runtimeTextChunk struct {
	Content  string
	Discrete bool
}

func runtimeTextEquivalent(parts []string, final string) bool {
	final = strings.TrimSpace(final)
	if final == "" {
		return false
	}
	prior := strings.TrimSpace(strings.Join(parts, "\n"))
	if prior == final {
		return true
	}
	return collapseRuntimeTextWhitespace(strings.Join(parts, " ")) == collapseRuntimeTextWhitespace(final)
}

func collapseRuntimeTextWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func discussionOutputToolText(message agentcore.RuntimeMessage) string {
	if message.Type != agentcore.RuntimeMessageToolUse {
		return ""
	}
	switch normalizeToolName(message.Tool) {
	default:
		return ""
	}
}

func firstRuntimeInputText(input map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(runtimeInputValue(input, key)); value != "" {
			return value
		}
	}
	return ""
}

func runtimeInputValue(input map[string]any, key string) string {
	raw, ok := input[key]
	if !ok || raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(encoded)
	}
}

func firstRuntimeSessionID(response agentcore.RuntimeResponse) string {
	if response.ProviderMetadata != nil {
		if value := strings.TrimSpace(response.ProviderMetadata["session_id"]); value != "" {
			return value
		}
	}
	for _, message := range response.Messages {
		if strings.TrimSpace(message.SessionID) != "" {
			return strings.TrimSpace(message.SessionID)
		}
	}
	return ""
}

func mapRuntimeToolCalls(messages []agentcore.RuntimeMessage) []ToolCallDTO {
	out := make([]ToolCallDTO, 0)
	results := runtimeToolResultsByCallID(messages)
	indexByCallID := map[string]int{}
	now := time.Now()
	for _, message := range messages {
		if message.Type != agentcore.RuntimeMessageToolUse || strings.TrimSpace(message.Tool) == "" {
			continue
		}
		if isEmptyMissionBriefCreateRuntimeTool(message) {
			continue
		}
		rawCallID := strings.TrimSpace(message.CallID)
		callID := firstNonBlank(rawCallID, message.Tool)
		status := runtimeToolCallStatus(message, "completed")
		result, hasResult := results[callID]
		if hasResult {
			status = firstNonBlank(result.Status, "completed")
		}
		call := ToolCallDTO{
			ToolCallID: callID,
			Name:       strings.TrimSpace(message.Tool),
			Status:     status,
			Arguments:  mapAnyStringValues(message.Input),
			Result:     cloneStringMap(result.Result),
			CreatedAt:  now,
		}
		if rawCallID != "" {
			if idx, ok := indexByCallID[rawCallID]; ok {
				out[idx] = mergeRuntimeToolCall(out[idx], call, strings.TrimSpace(message.Status) != "" || hasResult)
				continue
			}
			indexByCallID[rawCallID] = len(out)
		}
		out = append(out, call)
	}
	return out
}

type runtimeToolResult struct {
	Result map[string]string
	Status string
}

func runtimeToolResultsByCallID(messages []agentcore.RuntimeMessage) map[string]runtimeToolResult {
	out := map[string]runtimeToolResult{}
	for _, message := range messages {
		if message.Type != agentcore.RuntimeMessageToolResult {
			continue
		}
		callID := firstNonBlank(message.CallID, message.Tool)
		if callID == "" {
			continue
		}
		result := runtimeToolResultMap(message.Output)
		status := firstNonBlank(message.Status, "completed")
		if outputStatus := strings.TrimSpace(strings.ToLower(result["status"])); outputStatus == "failed" &&
			(isCompletedToolStatus(status) || strings.TrimSpace(message.Status) == "") {
			status = "failed"
		}
		if len(result) == 0 && strings.TrimSpace(status) == "" {
			continue
		}
		out[callID] = runtimeToolResult{Result: result, Status: status}
	}
	return out
}

func mergeRuntimeToolCall(existing ToolCallDTO, next ToolCallDTO, explicitStatus bool) ToolCallDTO {
	if strings.TrimSpace(existing.ToolCallID) == "" {
		existing.ToolCallID = next.ToolCallID
	}
	if strings.TrimSpace(existing.Name) == "" {
		existing.Name = next.Name
	}
	if len(next.Arguments) > 0 {
		existing.Arguments = cloneStringMap(next.Arguments)
	}
	if len(next.Result) > 0 {
		existing.Result = cloneStringMap(next.Result)
	}
	if explicitStatus {
		existing.Status = next.Status
	}
	if existing.CreatedAt.IsZero() {
		existing.CreatedAt = next.CreatedAt
	}
	return existing
}

func runtimeToolResultMap(output string) map[string]string {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err == nil {
		return mapAnyStringValues(decoded)
	}
	return map[string]string{"output": output}
}

func isEmptyMissionBriefCreateRuntimeTool(message agentcore.RuntimeMessage) bool {
	if normalizeToolName(message.Tool) != string(toolstd.MissionBriefCreate) {
		return false
	}
	return firstRuntimeInputText(message.Input, "title", "mission_title", "name", "final_goal", "goal", "user_request", "summary") == ""
}

func runtimeToolCallStatus(message agentcore.RuntimeMessage, fallback string) string {
	return firstNonBlank(message.Status, fallback)
}

func mapAnyStringValues(values map[string]any) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case string:
			out[key] = typed
		case fmt.Stringer:
			out[key] = typed.String()
		case []any, map[string]any:
			raw, err := json.Marshal(typed)
			if err != nil {
				out[key] = fmt.Sprint(typed)
				continue
			}
			out[key] = string(raw)
		default:
			out[key] = fmt.Sprint(typed)
		}
	}
	return out
}
