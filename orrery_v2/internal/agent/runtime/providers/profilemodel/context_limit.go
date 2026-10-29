package profilemodel

import (
	"encoding/json"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func limitOpenAICompatibleMessages(messages []openAICompatibleChatMessage, configuredContextTokens int, tools []openAICompatibleTool) []openAICompatibleChatMessage {
	if len(messages) == 0 {
		return nil
	}
	contextTokens := agentcore.EffectiveRuntimeContextTokenLimit(map[string]string{
		"llm_context_length": contextLengthString(configuredContextTokens),
	})
	promptBudget := agentcore.RuntimePromptTokenBudget(contextTokens)
	promptBudget = agentcore.RuntimePromptTokenBudgetAfterReserved(promptBudget, estimateOpenAICompatibleTokens(tools))
	if estimateOpenAICompatibleMessagesTokens(messages) <= promptBudget {
		return cloneOpenAICompatibleMessages(messages)
	}

	keep := make(map[int]int)
	var systemIndexes []int
	var groups [][]int
	for index, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "system" {
			systemIndexes = append(systemIndexes, index)
			continue
		}
		if role == "user" || len(groups) == 0 {
			groups = append(groups, []int{index})
			continue
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], index)
	}

	used := 0
	for _, index := range systemIndexes {
		cost := estimateOpenAICompatibleMessageTokens(messages[index])
		keep[index] = cost
		used += cost
	}
	remaining := promptBudget - used
	keptGroup := false
	for i := len(groups) - 1; i >= 0 && (remaining > 0 || !keptGroup); i-- {
		cost := estimateOpenAICompatibleMessageIndexes(messages, groups[i])
		if cost <= remaining {
			for _, index := range groups[i] {
				messageCost := estimateOpenAICompatibleMessageTokens(messages[index])
				keep[index] = messageCost
				remaining -= messageCost
			}
			keptGroup = true
			continue
		}
		if keptGroup {
			break
		}
		used = assignOpenAICompatibleMessageBudgets(messages, groups[i], remaining, keep)
		// Keep the current request complete. Tool outputs may still be bounded,
		// but never rewrite the request or the system rules to make them fit.
		for _, index := range groups[i] {
			if _, exists := keep[index]; !exists {
				fixed := cloneOpenAICompatibleMessage(messages[index])
				fixed.Content = ""
				keep[index] = estimateOpenAICompatibleMessageTokens(fixed)
			}
			if strings.EqualFold(messages[index].Role, "user") {
				keep[index] = estimateOpenAICompatibleMessageTokens(messages[index])
			}
		}
		remaining -= used
		keptGroup = true
	}

	out := make([]openAICompatibleChatMessage, 0, len(keep))
	for index, message := range messages {
		budget, ok := keep[index]
		if !ok {
			continue
		}
		out = append(out, limitOpenAICompatibleMessage(message, budget))
	}
	return enforceOpenAICompatibleMessageBudget(out, promptBudget)
}

func enforceOpenAICompatibleMessageBudget(messages []openAICompatibleChatMessage, tokenBudget int) []openAICompatibleChatMessage {
	for estimateOpenAICompatibleMessagesTokens(messages) > tokenBudget {
		largestIndex := -1
		largestTokens := 0
		for i, message := range messages {
			if message.Role == "system" || message.Role == "user" {
				continue
			}
			contentTokens := agentcore.EstimateRuntimeTextTokens(message.Content)
			if contentTokens > largestTokens {
				largestIndex = i
				largestTokens = contentTokens
			}
		}
		if largestIndex < 0 || largestTokens == 0 {
			break
		}
		over := estimateOpenAICompatibleMessagesTokens(messages) - tokenBudget
		nextBudget := largestTokens - over - 1
		if nextBudget < 0 {
			nextBudget = 0
		}
		truncated := agentcore.TruncateRuntimeText(messages[largestIndex].Content, nextBudget)
		if truncated == messages[largestIndex].Content {
			truncated = ""
		}
		messages[largestIndex].Content = truncated
	}
	return messages
}

func assignOpenAICompatibleMessageBudgets(messages []openAICompatibleChatMessage, indexes []int, tokenBudget int, keep map[int]int) int {
	if len(indexes) == 0 || tokenBudget <= 0 {
		return 0
	}
	used := 0
	remaining := tokenBudget
	for i, index := range indexes {
		if remaining <= 0 {
			break
		}
		cost := estimateOpenAICompatibleMessageTokens(messages[index])
		share := remaining / (len(indexes) - i)
		if cost < share {
			share = cost
		}
		if share < 1 {
			continue
		}
		keep[index] = share
		used += share
		remaining -= share
	}
	return used
}

func limitOpenAICompatibleMessage(message openAICompatibleChatMessage, tokenBudget int) openAICompatibleChatMessage {
	out := cloneOpenAICompatibleMessage(message)
	if message.Role == "system" || message.Role == "user" {
		return out
	}
	out.Content = ""
	fixed := estimateOpenAICompatibleMessageTokens(out)
	out.Content = agentcore.TruncateRuntimeText(message.Content, tokenBudget-fixed)
	return out
}

func estimateOpenAICompatibleMessageIndexes(messages []openAICompatibleChatMessage, indexes []int) int {
	total := 0
	for _, index := range indexes {
		total += estimateOpenAICompatibleMessageTokens(messages[index])
	}
	return total
}

func estimateOpenAICompatibleMessagesTokens(messages []openAICompatibleChatMessage) int {
	total := 0
	for _, message := range messages {
		total += estimateOpenAICompatibleMessageTokens(message)
	}
	return total
}

func estimateOpenAICompatibleMessageTokens(message openAICompatibleChatMessage) int {
	message = cloneOpenAICompatibleMessage(message)
	images := 0
	for i := range message.ContentParts {
		if image := message.ContentParts[i].ImageURL; image != nil {
			image.URL = "[image]"
			images++
		}
	}
	return estimateOpenAICompatibleTokens(message) + 4 + images*2048
}

func estimateOpenAICompatibleTokens(value any) int {
	raw, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return agentcore.EstimateRuntimeTextTokens(string(raw))
}

func cloneOpenAICompatibleMessages(messages []openAICompatibleChatMessage) []openAICompatibleChatMessage {
	out := make([]openAICompatibleChatMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, cloneOpenAICompatibleMessage(message))
	}
	return out
}

func cloneOpenAICompatibleMessage(message openAICompatibleChatMessage) openAICompatibleChatMessage {
	message.ToolCalls = append([]openAICompatibleToolCall(nil), message.ToolCalls...)
	message.ContentParts = append([]openAICompatibleContentPart(nil), message.ContentParts...)
	for i := range message.ContentParts {
		if image := message.ContentParts[i].ImageURL; image != nil {
			copy := *image
			message.ContentParts[i].ImageURL = &copy
		}
	}
	return message
}

func contextLengthString(value int) string {
	if value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}
