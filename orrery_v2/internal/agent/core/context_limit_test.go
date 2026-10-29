package core

import (
	"strings"
	"testing"
)

func TestEffectiveRuntimeContextTokenLimitUsesPlatformCeilingAndSmallerOverrides(t *testing.T) {
	if got := EffectiveRuntimeContextTokenLimit(nil); got != RuntimeContextHardLimitTokens {
		t.Fatalf("default limit = %d", got)
	}
	if got := EffectiveRuntimeContextTokenLimit(map[string]string{"llm_context_length": "128000"}); got != RuntimeContextHardLimitTokens {
		t.Fatalf("large profile limit = %d", got)
	}
	if got := EffectiveRuntimeContextTokenLimit(map[string]string{"llm_context_length": "8192"}); got != 8192 {
		t.Fatalf("small profile limit = %d", got)
	}
	if got := EffectiveRuntimeContextTokenLimit(map[string]string{
		"llm_context_length":      "8192",
		"llm_context_token_limit": "4096",
	}); got != 4096 {
		t.Fatalf("per-run limit = %d", got)
	}
}

func TestLimitRuntimeChatMessagesDropsOldHistoryAndKeepsBoundaries(t *testing.T) {
	messages := []RuntimeChatMessage{
		{Role: "system", Content: "system principles " + strings.Repeat("s", 180)},
		{Role: "user", Content: "current input placed first " + strings.Repeat("c", 120)},
		{Role: "assistant", Content: "oldest-history " + strings.Repeat("a", 150)},
		{Role: "user", Content: "old-history " + strings.Repeat("b", 150)},
		{Role: "assistant", Content: "recent-history " + strings.Repeat("d", 90)},
		{Role: "user", Content: "current input placed last " + strings.Repeat("e", 120)},
	}

	limited, stats := LimitRuntimeChatMessages(messages, 220)
	if !stats.Truncated {
		t.Fatal("expected context to be truncated")
	}
	if stats.EstimatedTokensAfter > 220 {
		t.Fatalf("estimated tokens after = %d", stats.EstimatedTokensAfter)
	}
	joined := runtimeMessagesText(limited)
	for _, want := range []string{"system principles", "current input placed", "current input placed last"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("limited messages lost %q: %+v", want, limited)
		}
	}
	if len(limited) < 3 || !strings.HasPrefix(limited[1].Content, "current input placed") {
		t.Fatalf("first non-system boundary was not preserved: %+v", limited)
	}
	if strings.Contains(joined, "oldest-history") {
		t.Fatalf("oldest history should be dropped: %+v", limited)
	}
}

func TestContextBudgetPreservesWholeInstructionsAndCurrentRequest(t *testing.T) {
	system := "system start " + strings.Repeat("boundary ", 50) + "system end"
	current := strings.TrimSpace("current requirement " + strings.Repeat("detail ", 10))
	messages := []RuntimeChatMessage{{Role: "system", Content: system}, {Role: "user", Content: strings.Repeat("old history ", 400)}, {Role: "user", Content: current}}
	budget := EstimateRuntimeChatMessageTokens(messages[0]) + EstimateRuntimeChatMessageTokens(messages[2]) + 10
	limited, stats := LimitRuntimeChatMessages(messages, budget)
	if stats.RequiredOverflow || len(limited) != 2 || limited[0].Content != system || limited[1].Content != current {
		t.Fatalf("required instructions were clipped: %+v %+v", limited, stats)
	}
	limited, stats = LimitRuntimeChatMessages(messages, budget/2)
	if !stats.RequiredOverflow || len(limited) != 2 || limited[0].Content != system || limited[1].Content != current {
		t.Fatalf("overflow silently rewrote instructions: %+v %+v", limited, stats)
	}
}

func TestTruncateRuntimeTextPreservesHeadAndTail(t *testing.T) {
	value := "important-head " + strings.Repeat("middle ", 100) + "important-tail"
	got := TruncateRuntimeText(value, 40)
	if EstimateRuntimeTextTokens(got) > 40 {
		t.Fatalf("truncated text estimate = %d: %q", EstimateRuntimeTextTokens(got), got)
	}
	if !strings.Contains(got, "important-head") || !strings.Contains(got, "important-tail") || !strings.Contains(got, "context truncated") {
		t.Fatalf("truncated text = %q", got)
	}
}

func TestRuntimePromptTokenBudgetAccountsForToolSchemas(t *testing.T) {
	bindings := []RuntimeToolBinding{{
		ToolID:      "large_tool",
		Summary:     strings.Repeat("schema ", 200),
		InputSchema: map[string]any{"type": "object", "description": strings.Repeat("field ", 300)},
	}}
	budget := RuntimePromptTokenBudget(RuntimeContextHardLimitTokens)
	available := RuntimePromptTokenBudgetAfterTools(budget, bindings)
	if available >= budget {
		t.Fatalf("tool schemas should reduce message budget: available=%d budget=%d", available, budget)
	}
	if available < 1024 {
		t.Fatalf("message budget floor = %d", available)
	}
}

func runtimeMessagesText(messages []RuntimeChatMessage) string {
	var parts []string
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}
