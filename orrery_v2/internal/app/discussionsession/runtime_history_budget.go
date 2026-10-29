package discussionsession

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

// Background notices remain durable and available through notification recall.
// Bound their share of the prompt without evicting a user's conversation.
func discussionPromptHistory(request DiscussionAgentRequest) []MessageDTO {
	keep := make([]bool, len(request.History))
	count, tokens := 0, 0
	for index := len(request.History) - 1; index >= 0; index-- {
		message := request.History[index]
		passive := !strings.EqualFold(message.Role, "user") &&
			(strings.EqualFold(message.Role, "system") || message.Metadata["delivery"] == "passive_notice" || message.Metadata["kind"] == "scheduled_task_result")
		if passive {
			cost := agentcore.EstimateRuntimeTextTokens(message.Content)
			if count >= 3 || tokens+cost > 1024 {
				continue
			}
			count++
			tokens += cost
		}
		keep[index] = true
	}
	out := make([]MessageDTO, 0, len(request.History))
	for index, message := range request.History {
		if keep[index] {
			out = append(out, message)
		}
	}
	return out
}
