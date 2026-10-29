package main

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

// Select only a completed final response. Public fences delimit the author's
// chosen report; internal receipts and progress messages cannot supply it.
func scheduledAgentFinalReport(response agentcore.RuntimeResponse) string {
	if response.Status != agentcore.RuntimeStatusCompleted {
		return ""
	}
	return scheduledAgentFinalMessageReport(response.Messages)
}

func scheduledAgentFinalMessageReport(messages []agentcore.RuntimeMessage) string {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Type != agentcore.RuntimeMessageText || message.Status != agentcore.RuntimeMessageStatusFinal {
			continue
		}
		output := message.Content
		if count := strings.Count(output, scheduledFeishuPublicSummaryFence); count > 0 {
			if count != 1 {
				return ""
			}
			raw := strings.SplitN(output, scheduledFeishuPublicSummaryFence, 2)[1]
			end := strings.Index(raw, "```")
			if end < 0 {
				return ""
			}
			output = raw[:end]
		} else {
			output = stripAgentResultManifest(output)
		}
		output = strings.TrimSpace(output)
		// Withhold unsafe text instead of silently changing the Agent's words.
		if output == "" || personalAssistantScheduledPublicText(output) != output {
			return ""
		}
		return output
	}
	return ""
}
