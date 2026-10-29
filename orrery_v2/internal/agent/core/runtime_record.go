package core

import "strings"

// RuntimeRequestRecordProjection returns the bounded application projection
// retained for progress reconstruction and prompt diagnostics. Launch material,
// credentials, provider configuration, tool secrets, and local workspace paths
// remain in their short-lived execution bindings.
func RuntimeRequestRecordProjection(request RuntimeRequest) RuntimeRequest {
	projected := RuntimeRequest{
		RunID:              request.RunID,
		AgentID:            request.AgentID,
		ExecutionEnvelope:  CloneExecutionEnvelope(request.ExecutionEnvelope),
		ExecutionPrincipal: request.ExecutionPrincipal,
		MissionID:          request.MissionID,
		TaskID:             request.TaskID,
		ContextPack: ContextPack{
			MissionID:             request.ContextPack.MissionID,
			AgentID:               request.ContextPack.AgentID,
			TaskID:                request.ContextPack.TaskID,
			MissionFinalGoal:      request.ContextPack.MissionFinalGoal,
			DiscussionGroupGoal:   request.ContextPack.DiscussionGroupGoal,
			WorkerPersonalGoal:    request.ContextPack.WorkerPersonalGoal,
			ResponsibilityContext: CloneResponsibilityContext(request.ContextPack.ResponsibilityContext),
		},
		InputMessages: cloneRuntimeRecordMessages(request.InputMessages),
		RuntimeOptions: RuntimeOptions{
			Model:               request.RuntimeOptions.Model,
			SystemPrompt:        request.RuntimeOptions.SystemPrompt,
			SystemContextPrompt: request.RuntimeOptions.SystemContextPrompt,
			PromptBlocks:        append([]RuntimePromptBlock(nil), request.RuntimeOptions.PromptBlocks...),
		},
	}
	if kind := RuntimeKind(strings.TrimSpace(request.RuntimeOptions.ProviderSpecific["runtime_kind"])); kind.IsValid() {
		projected.RuntimeOptions.ProviderSpecific = map[string]string{"runtime_kind": string(kind)}
	}
	return projected
}

func cloneRuntimeRecordMessages(messages []RuntimeChatMessage) []RuntimeChatMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]RuntimeChatMessage, len(messages))
	for index, message := range messages {
		out[index] = message
		out[index].ToolCalls = make([]RuntimeToolCall, len(message.ToolCalls))
		for callIndex, call := range message.ToolCalls {
			out[index].ToolCalls[callIndex] = call
			out[index].ToolCalls[callIndex].Input = cloneRuntimeRecordMap(call.Input)
		}
	}
	return out
}

func cloneRuntimeRecordMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = cloneRuntimeRecordValue(value)
	}
	return out
}

func cloneRuntimeRecordValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneRuntimeRecordMap(typed)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = cloneRuntimeRecordValue(item)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}
