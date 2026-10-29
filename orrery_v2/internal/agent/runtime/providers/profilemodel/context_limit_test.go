package profilemodel

import (
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestLimitOpenAICompatibleMessagesKeepsSystemAndNewestCompleteTurn(t *testing.T) {
	messages := []openAICompatibleChatMessage{
		{Role: "system", Content: "system policy " + strings.Repeat("s", 300)},
		{Role: "user", Content: "old user " + strings.Repeat("o", 500)},
		{Role: "assistant", Content: "old assistant " + strings.Repeat("a", 500)},
		{Role: "user", Content: "current request " + strings.Repeat("c", 300)},
		{Role: "assistant", ToolCalls: []openAICompatibleToolCall{{ID: "call-1", Type: "function", Function: openAICompatibleToolCallFunction{Name: "lookup", Arguments: `{"id":"1"}`}}}},
		{Role: "tool", ToolCallID: "call-1", Content: "latest tool result " + strings.Repeat("r", 500)},
	}

	limited := limitOpenAICompatibleMessages(messages, 512, nil)
	joined := openAICompatibleMessagesText(limited)
	if !strings.Contains(joined, "system policy") || !strings.Contains(joined, "current request") || !strings.Contains(joined, "latest tool result") {
		t.Fatalf("required context missing: %+v", limited)
	}
	if strings.Contains(joined, "old user") || strings.Contains(joined, "old assistant") {
		t.Fatalf("old context should be dropped: %+v", limited)
	}
	if !openAICompatibleToolPairPresent(limited, "call-1") {
		t.Fatalf("tool call/result pair should stay intact: %+v", limited)
	}
	if got := estimateOpenAICompatibleMessagesTokens(limited); got > agentcore.RuntimePromptTokenBudget(512) {
		t.Fatalf("limited estimate = %d", got)
	}
}

func TestLimitOpenAICompatibleMessagesAccountsForToolSchemas(t *testing.T) {
	messages := []openAICompatibleChatMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "old " + strings.Repeat("o", 800)},
		{Role: "assistant", Content: "old response " + strings.Repeat("a", 800)},
		{Role: "user", Content: "current request"},
	}
	tools := []openAICompatibleTool{{
		Type: "function",
		Function: openAICompatibleToolFunction{
			Name:        "large_tool",
			Description: strings.Repeat("schema ", 100),
			Parameters:  map[string]any{"type": "object", "description": strings.Repeat("field ", 100)},
		},
	}}

	limited := limitOpenAICompatibleMessages(messages, 1024, tools)
	if strings.Contains(openAICompatibleMessagesText(limited), "old response") {
		t.Fatalf("tool schema reservation should evict old history: %+v", limited)
	}
	if !strings.Contains(openAICompatibleMessagesText(limited), "current request") {
		t.Fatalf("current request missing: %+v", limited)
	}
}

func openAICompatibleMessagesText(messages []openAICompatibleChatMessage) string {
	var parts []string
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}

func openAICompatibleToolPairPresent(messages []openAICompatibleChatMessage, callID string) bool {
	call, result := false, false
	for _, message := range messages {
		for _, toolCall := range message.ToolCalls {
			call = call || toolCall.ID == callID
		}
		result = result || message.ToolCallID == callID
	}
	return call && result
}
