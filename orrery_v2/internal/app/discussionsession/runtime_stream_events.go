package discussionsession

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func runtimeMessageStreamEvents(message agentcore.RuntimeMessage) []DiscussionAgentStreamEvent {
	switch message.Type {
	case agentcore.RuntimeMessageText:
		if message.Content == "" || message.Status == agentcore.RuntimeMessageStatusFinal {
			return nil
		}
		return []DiscussionAgentStreamEvent{{Kind: DiscussionAgentStreamTokenDelta, Delta: message.Content}}
	case agentcore.RuntimeMessageThinking:
		if message.Content == "" || message.ReasoningKind != agentcore.ReasoningContentSummary {
			return nil
		}
		return []DiscussionAgentStreamEvent{{Kind: DiscussionAgentStreamThinkingDelta, Delta: message.Content, ReasoningKind: message.ReasoningKind}}
	case agentcore.RuntimeMessageToolUse:
		if isEmptyMissionBriefCreateRuntimeTool(message) {
			return nil
		}
		return []DiscussionAgentStreamEvent{{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{
			ToolCallID: firstNonBlank(message.CallID, message.Tool),
			Name:       strings.TrimSpace(message.Tool),
			Status:     runtimeToolCallStatus(message, "running"),
			Arguments:  mapAnyStringValues(message.Input),
		}}}
	case agentcore.RuntimeMessageToolResult:
		return []DiscussionAgentStreamEvent{{Kind: DiscussionAgentStreamToolResult, ToolCall: ToolCallDTO{
			ToolCallID: firstNonBlank(message.CallID, message.Tool),
			Name:       strings.TrimSpace(message.Tool),
			Status:     firstNonBlank(message.Status, "completed"),
			Result:     runtimeToolResultMap(message.Output),
		}}}
	case agentcore.RuntimeMessagePlan:
		if message.Plan == nil {
			return nil
		}
		plan := cloneRuntimePlanState(message.Plan)
		return []DiscussionAgentStreamEvent{{Kind: DiscussionAgentStreamPlan, Plan: &plan}}
	default:
		return nil
	}
}
